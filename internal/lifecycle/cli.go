package lifecycle

import (
	"blux/internal/backup"
	"blux/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func Run(args []string, defaultData string) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	data := flags.String("data-dir", defaultData, "本地数据目录")
	addr := flags.String("addr", "127.0.0.1:8787", "网页服务地址")
	web := flags.String("web-dir", "web/dist", "构建后的前端目录")
	output := flags.String("output", "", "备份 ZIP 路径（不能已存在）")
	defaultCodex := os.Getenv("BLUX_CODEX_BIN")
	if defaultCodex == "" {
		defaultCodex = "codex"
	}
	binary := flags.String("codex-bin", defaultCodex, "Codex CLI 路径")
	concurrency := flags.Int("concurrency", 3, "同时执行任务数")
	background := flags.Int("background-concurrency", 1, "同时执行的自动化运行数")
	timeout := flags.Duration("timeout", 2*time.Hour, "单回合上限")
	if e := flags.Parse(args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("存在无法识别的参数")
	}
	dir, e := filepath.Abs(*data)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	serviceDir := func(name string) string { return filepath.Join(dir, "runtime", name) }
	switch args[0] {
	case "status":
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"server": platform.RecordedAlive(serviceDir("server")), "worker": platform.RecordedAlive(serviceDir("worker")), "data_dir": dir})
	case "stop":
		if e = platform.StopRecorded(serviceDir("worker")); e != nil {
			return e
		}
		if e = platform.ReapRecorded(dir); e != nil {
			return e
		}
		if e = platform.StopRecorded(serviceDir("server")); e != nil {
			return e
		}
		fmt.Println("Blux 已停止。")
		return nil
	case "backup":
		if *output == "" {
			return errors.New("backup 需要 --output 路径")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if e = backup.Export(ctx, dir, *output); e != nil {
			return e
		}
		fmt.Println("备份已保存：" + *output)
		return nil
	case "start":
		if *concurrency < 1 || *concurrency > 32 || *timeout <= 0 || *background < 1 || *background > *concurrency {
			return errors.New("无效的并发或超时设置")
		}
		webPath, e := filepath.Abs(*web)
		if e != nil {
			return e
		}
		if _, e = os.Stat(filepath.Join(webPath, "index.html")); e != nil {
			return errors.New("请先构建前端，再启动服务")
		}
		for _, name := range []string{"server", "worker"} {
			if platform.RecordedAlive(serviceDir(name)) {
				return fmt.Errorf("%s 已经运行", name)
			}
		}
		for _, name := range []string{"serve.lock", "worker.lock"} {
			release, e := platform.Lock(filepath.Join(dir, name))
			if e != nil {
				return errors.New("同一数据目录已有手动启动的服务，请先停止")
			}
			release()
		}
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Join(dir, "logs"), 0700); e != nil {
			return e
		}
		started := []string{}
		success := false
		defer func() {
			if !success {
				for _, name := range started {
					_ = platform.StopRecorded(serviceDir(name))
				}
			}
		}()
		for _, name := range []string{"server", "worker"} {
			if e = os.MkdirAll(serviceDir(name), 0700); e != nil {
				return e
			}
			log, e := os.OpenFile(filepath.Join(dir, "logs", name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			argv := []string{"serve", "--data-dir", dir, "--addr", *addr, "--web-dir", webPath, "--codex-bin", *binary}
			if name == "worker" {
				argv = []string{"worker", "--data-dir", dir, "--codex-bin", *binary, "--concurrency", fmt.Sprint(*concurrency), "--background-concurrency", fmt.Sprint(*background), "--timeout", timeout.String()}
			}
			cmd := exec.Command(exe, argv...)
			cmd.Stdout = log
			cmd.Stderr = log
			platform.ConfigureProcess(cmd)
			e = cmd.Start()
			log.Close()
			if e != nil {
				return e
			}
			if _, e = platform.RecordProcess(serviceDir(name), cmd.Process.Pid); e != nil {
				_ = platform.TerminateTree(cmd, true)
				return e
			}
			started = append(started, name)
			_ = cmd.Process.Release()
		}
		time.Sleep(250 * time.Millisecond)
		for _, name := range started {
			if !platform.RecordedAlive(serviceDir(name)) {
				return fmt.Errorf("%s 启动失败，请查看 %s", name, filepath.Join(dir, "logs", name+".log"))
			}
		}
		success = true
		fmt.Println("Blux 已启动：http://" + *addr)
		return nil
	}
	return errors.New("未知启动管理命令")
}
