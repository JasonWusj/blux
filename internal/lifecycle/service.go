package lifecycle

import (
	"blux/internal/platform"
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Blux runs as two launchd jobs, one for the web server and one for the worker,
// so each is restarted on its own and `launchctl` shows them separately.
var serviceNames = []string{"serve", "worker"}

func serviceLabel(name string) string { return "local.blux." + name }

// environmentKeys are copied from the installing shell into the service. launchd
// starts jobs with almost no environment, so without PATH the agent CLIs (often
// installed by npm or Homebrew) would not be found. Only locations and binary
// names are copied: credentials stay in each CLI's own configuration.
var environmentKeys = []string{"PATH", "BLUX_CODEX_BIN", "BLUX_CLAUDE_BIN", "BLUX_DEEPSEEK_BIN", "BLUX_GEMINI_BIN", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "TZ"}

// serviceJob describes one launchd job. It is plain data so the generated
// property list can be tested on any platform.
type serviceJob struct {
	Label     string
	Arguments []string
	Env       map[string]string
	LogPath   string
	WorkDir   string
}

// plist renders the launchd property list. Every value is XML-escaped, and the
// job restarts only after a crash, so `launchctl bootout` stops it for good.
func (j serviceJob) plist() (string, error) {
	var b bytes.Buffer
	text := func(s string) (string, error) {
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(s)); err != nil {
			return "", err
		}
		return escaped.String(), nil
	}
	write := func(format string, values ...string) error {
		escaped := make([]any, len(values))
		for i, v := range values {
			e, err := text(v)
			if err != nil {
				return err
			}
			escaped[i] = e
		}
		_, err := fmt.Fprintf(&b, format, escaped...)
		return err
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	if err := write("\t<key>Label</key>\n\t<string>%s</string>\n", j.Label); err != nil {
		return "", err
	}
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, argument := range j.Arguments {
		if err := write("\t\t<string>%s</string>\n", argument); err != nil {
			return "", err
		}
	}
	b.WriteString("\t</array>\n")
	if len(j.Env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		keys := make([]string, 0, len(j.Env))
		for key := range j.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := write("\t\t<key>%s</key>\n\t\t<string>%s</string>\n", key, j.Env[key]); err != nil {
				return "", err
			}
		}
		b.WriteString("\t</dict>\n")
	}
	if j.WorkDir != "" {
		if err := write("\t<key>WorkingDirectory</key>\n\t<string>%s</string>\n", j.WorkDir); err != nil {
			return "", err
		}
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n")
	b.WriteString("\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n")
	if err := write("\t<key>StandardOutPath</key>\n\t<string>%s</string>\n\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", j.LogPath, j.LogPath); err != nil {
		return "", err
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String(), nil
}

// installPlan holds everything install derives, so it can be built and tested
// without touching launchd.
type installPlan struct {
	DataDir string
	WebDir  string
	Addr    string
	Binary  string
	Jobs    []serviceJob
	Paths   []string // property list file per job, in the same order
}

func buildPlan(binary, dataDir, webDir, addr, home string, environ func(string) string) (installPlan, error) {
	plan := installPlan{DataDir: dataDir, WebDir: webDir, Addr: addr, Binary: binary}
	env := map[string]string{}
	for _, key := range environmentKeys {
		if value := environ(key); value != "" {
			env[key] = value
		}
	}
	env["BLUX_DATA_DIR"] = dataDir
	for _, name := range serviceNames {
		arguments := []string{binary, name, "--data-dir", dataDir}
		if name == "serve" {
			arguments = append(arguments, "--addr", addr, "--web-dir", webDir)
		}
		plan.Jobs = append(plan.Jobs, serviceJob{
			Label: serviceLabel(name), Arguments: arguments, Env: env,
			LogPath: filepath.Join(dataDir, "logs", name+".log"), WorkDir: dataDir,
		})
		plan.Paths = append(plan.Paths, filepath.Join(home, "Library", "LaunchAgents", serviceLabel(name)+".plist"))
	}
	return plan, nil
}

// launchctl runs launchctl and returns its combined output.
var launchctl = func(arguments ...string) (string, error) {
	out, err := exec.Command("launchctl", arguments...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// RunService handles `blux service install|uninstall|status`.
func RunService(args []string, defaultData string) error {
	if len(args) == 0 {
		return errors.New("用法：blux service install|uninstall|status [--data-dir DIR] [--web-dir DIR] [--addr ADDR]")
	}
	action := args[0]
	flags := flag.NewFlagSet("service "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	data := flags.String("data-dir", defaultData, "本地数据目录")
	web := flags.String("web-dir", "web/dist", "构建后的前端目录")
	addr := flags.String("addr", "127.0.0.1:8787", "网页服务地址")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("存在无法识别的参数")
	}
	if action != "install" && action != "uninstall" && action != "status" {
		return fmt.Errorf("未知的 service 子命令 %q", action)
	}
	if runtime.GOOS != "darwin" {
		return errors.New("常驻服务目前只支持 macOS（launchd）。其他系统请继续使用 blux start")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dataDir, err := filepath.Abs(*data)
	if err != nil {
		return err
	}
	switch action {
	case "install":
		return installService(dataDir, *web, *addr, home)
	case "uninstall":
		return uninstallService(home)
	default:
		return serviceStatus()
	}
}

func installService(dataDir, web, addr, home string) error {
	webDir, err := filepath.Abs(web)
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(webDir, "index.html")); err != nil {
		return errors.New("请先构建前端，再安装常驻服务")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	if binary, err = filepath.EvalSymlinks(binary); err != nil {
		return err
	}
	for _, name := range []string{"server", "worker"} {
		if platform.RecordedAlive(filepath.Join(dataDir, "runtime", name)) {
			return errors.New("检测到由 blux start 启动的服务仍在运行，请先执行 blux stop")
		}
	}
	plan, err := buildPlan(binary, dataDir, webDir, addr, home, os.Getenv)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(dataDir, "logs"), 0o700); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		return err
	}
	for i, job := range plan.Jobs {
		content, err := job.plist()
		if err != nil {
			return err
		}
		if err = os.WriteFile(plan.Paths[i], []byte(content), 0o644); err != nil {
			return err
		}
		// Reinstalling replaces a loaded job: unload first, ignoring "not loaded".
		_, _ = launchctl("bootout", domain()+"/"+job.Label)
		if out, err := launchctl("bootstrap", domain(), plan.Paths[i]); err != nil {
			return fmt.Errorf("launchctl bootstrap %s 失败：%s", job.Label, firstNonEmpty(out, err.Error()))
		}
	}
	fmt.Println("Blux 常驻服务已安装并启动：http://" + addr)
	fmt.Println("日志：" + filepath.Join(dataDir, "logs"))
	fmt.Println("停止并卸载：blux service uninstall")
	fmt.Println("提示：登录后自动启动；合上笔记本期间不会运行自动化，开盖后会补跑最近一次错过的时间点。")
	return nil
}

func uninstallService(home string) error {
	var failures []string
	for _, name := range serviceNames {
		label := serviceLabel(name)
		if out, err := launchctl("bootout", domain()+"/"+label); err != nil && !notLoaded(out) {
			failures = append(failures, label+"："+firstNonEmpty(out, err.Error()))
		}
		path := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New("卸载未完成：" + strings.Join(failures, "；"))
	}
	fmt.Println("Blux 常驻服务已停止并卸载。")
	return nil
}

func serviceStatus() error {
	for _, name := range serviceNames {
		label := serviceLabel(name)
		out, err := launchctl("print", domain()+"/"+label)
		if err != nil {
			fmt.Printf("%-8s 未安装或未加载\n", name)
			continue
		}
		state, pid := parsePrint(out)
		fmt.Printf("%-8s 已加载  状态=%s  pid=%s\n", name, state, pid)
	}
	return nil
}

// notLoaded recognises launchctl's complaint about a job that is not loaded,
// which is the normal case when uninstalling something already stopped.
func notLoaded(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no such process") || strings.Contains(lower, "could not find") || strings.Contains(lower, "not found")
}

// parsePrint reads the state and pid out of `launchctl print` output.
func parsePrint(output string) (state, pid string) {
	state, pid = "unknown", "-"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "state = "); ok && state == "unknown" {
			state = value
		}
		if value, ok := strings.CutPrefix(line, "pid = "); ok && pid == "-" {
			pid = value
		}
	}
	return state, pid
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
