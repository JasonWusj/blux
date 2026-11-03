package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"blux/internal/artifact"
	"blux/internal/core"
	"blux/internal/httpapi"
	"blux/internal/lifecycle"
	"blux/internal/mcp"
	"blux/internal/platform"
	"blux/internal/store"
	"blux/internal/worker"
)

const version = "0.7.0-phase7"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("blux stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Blux " + version + " — 本地个人 Agent 工作台\n\n" +
			"用法：\n  blux serve  [--addr 127.0.0.1:8787] [--data-dir ~/.blux] [--web-dir web/dist]\n" +
			"  blux worker [--data-dir ~/.blux] [--codex-bin codex]\n" +
			"  blux start|stop|status [--data-dir ~/.blux]\n  blux backup --output backup.zip [--data-dir ~/.blux]\n  blux mcp --project PATH [--data-dir ~/.blux]\n  blux replay --task TASK_ID [--data-dir ~/.blux]\n  blux repair-artifacts --task TASK_ID [--data-dir ~/.blux]\n  blux version\n\n" +
			"网页服务和 Worker 分别启动，使用相同数据目录。执行器使用本机已有 CLI 配置。")
		return nil
	}
	if args[0] == "start" || args[0] == "stop" || args[0] == "status" || args[0] == "backup" {
		return lifecycle.Run(args, defaultDataDir())
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if args[0] != "serve" && args[0] != "worker" && args[0] != "replay" && args[0] != "repair-artifacts" && args[0] != "mcp" {
		return fmt.Errorf("未知命令 %q；使用 blux help 查看用法", args[0])
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dataDir := flags.String("data-dir", defaultDataDir(), "SQLite 与工作区目录")
	var addr, webDir, taskID, project string
	var concurrency int
	var timeout time.Duration
	deepSeekBinary := os.Getenv("BLUX_DEEPSEEK_BIN")
	geminiBinary := os.Getenv("BLUX_GEMINI_BIN")
	claudeBinary := os.Getenv("BLUX_CLAUDE_BIN")
	if claudeBinary == "" {
		claudeBinary = "claude"
	}
	codexBinary := os.Getenv("BLUX_CODEX_BIN")
	if codexBinary == "" {
		codexBinary = "codex"
	}
	if args[0] == "serve" || args[0] == "worker" {
		flags.StringVar(&codexBinary, "codex-bin", codexBinary, "Codex CLI 可执行文件")
		flags.StringVar(&deepSeekBinary, "deepseek-bin", deepSeekBinary, "DeepSeek Harness CLI 可执行文件")
		flags.StringVar(&geminiBinary, "gemini-bin", geminiBinary, "Gemini CLI 可执行文件")
		flags.StringVar(&claudeBinary, "claude-bin", claudeBinary, "Claude Code CLI 可执行文件")
	}
	switch args[0] {
	case "serve":
		flags.StringVar(&addr, "addr", "127.0.0.1:8787", "HTTP 监听地址")
		flags.StringVar(&webDir, "web-dir", "web/dist", "构建后的前端目录")
	case "worker":
		flags.IntVar(&concurrency, "concurrency", 2, "同时执行任务数量")
		flags.DurationVar(&timeout, "timeout", 2*time.Hour, "单回合执行上限")
	case "mcp":
		flags.StringVar(&project, "project", "", "管家已选择的项目目录")
	case "replay", "repair-artifacts":
		flags.StringVar(&taskID, "task", "", "要重建投影的任务 ID")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不接受额外参数：%v", flags.Args())
	}
	if (args[0] == "replay" || args[0] == "repair-artifacts") && taskID == "" {
		return fmt.Errorf("%s 需要 --task TASK_ID", args[0])
	}
	if args[0] == "worker" && (concurrency < 1 || concurrency > 32 || timeout <= 0) {
		return errors.New("concurrency 必须为 1..32，timeout 必须大于 0")
	}
	directory, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败：%w", err)
	}
	db, err := store.Open(filepath.Join(directory, "blux.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.CleanupRetiredData(context.Background(), directory); err != nil {
		return fmt.Errorf("清理旧数据失败: %w", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch args[0] {
	case "repair-artifacts":
		n, err := (artifact.Service{Store: db, DataDir: directory}).BackfillProjectArtifacts(ctx, taskID)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"task_id": taskID, "added_artifacts": n})
	case "mcp":
		return (mcp.Server{Store: db, DataDir: directory, Project: project}).Run(ctx, os.Stdin, os.Stdout)
	case "serve":
		release, e := platform.Lock(filepath.Join(directory, "serve.lock"))
		if e != nil {
			return errors.New("同一数据目录的网页服务已经运行")
		}
		defer release()
		webPath, err := filepath.Abs(webDir)
		if err != nil {
			return err
		}
		api := &httpapi.Server{Store: db, DataDir: directory, WebDir: webPath, Logger: logger, CodexBinary: codexBinary, ClaudeBinary: claudeBinary, DeepSeekBinary: deepSeekBinary, GeminiBinary: geminiBinary}
		server := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("启动本地网页服务失败：%w", err)
		}
		go func() {
			<-ctx.Done()
			// Shutdown alone waits for long-lived SSE connections. Close the
			// browser connections as well; Worker ownership is independent.
			_ = server.Close()
		}()
		logger.Info("web server started", "addr", listener.Addr().String(), "data_dir", directory, "default_adapter", "codex")
		if err = server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case "worker":
		release, err := platform.Lock(filepath.Join(directory, "worker.lock"))
		if err != nil {
			return err
		}
		defer release()
		w := &worker.Worker{Store: db, Concurrency: concurrency, Timeout: timeout, CodexBinary: codexBinary, ClaudeBinary: claudeBinary, DeepSeekBinary: deepSeekBinary, GeminiBinary: geminiBinary, DataDir: directory, ID: core.NewID("worker"), Logger: logger}
		return w.Run(ctx)
	case "replay":
		if err := db.Replay(ctx, taskID); err != nil {
			return err
		}
		detail, err := db.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(detail)
	}
	return nil
}

func defaultDataDir() string {
	if value := os.Getenv("BLUX_DATA_DIR"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".blux"
	}
	return filepath.Join(home, ".blux")
}
