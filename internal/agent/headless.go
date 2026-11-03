package agent

import (
	"blux/internal/core"
	"blux/internal/platform"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Headless integrates the CLIs' native JSONL protocols and native sandboxes.
// It does not emulate either CLI with a direct model API call.
type Headless struct{ ID, Binary string }
type CLIInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Path      string `json:"path"`
}

func (a Headless) Name() string { return a.ID }
func (a Headless) label() string {
	if a.ID == "deepseek" {
		return "DeepSeek Harness"
	}
	return "Gemini CLI"
}
func (a Headless) Capabilities() Capabilities {
	return Capabilities{Streaming: true, Resume: true, Interrupt: true}
}
func (a Headless) failure(code, message string, err error) error {
	return executionError(a.ID+"_"+code, a.label()+"："+message, err)
}
func (a Headless) Probe(ctx context.Context) (CLIInfo, error) {
	var info CLIInfo
	if a.ID != "deepseek" && a.ID != "gemini" {
		return info, core.ErrInvalidState
	}
	binary := a.Binary
	if binary == "" {
		if a.ID == "deepseek" {
			binary = "dsh"
		} else {
			binary = "gemini"
		}
	}
	p, e := exec.LookPath(binary)
	if e != nil {
		return info, a.failure("not_found", "未安装 CLI，请先在本机安装并配置。", e)
	}
	p, e = filepath.Abs(p)
	if e != nil {
		return info, e
	}
	info.Path = p
	if runtime.GOOS == "windows" {
		return info, a.failure("platform_unavailable", "当前接入尚未验证 Windows 原生 CLI，请使用 Codex。", nil)
	}
	version, e := probeCommand(ctx, p, "--version")
	if e != nil || strings.TrimSpace(version) == "" {
		return info, a.failure("probe_failed", "无法检查 CLI 版本。", e)
	}
	info.Version = strings.TrimSpace(version)
	args := []string{"--help"}
	required := []string{"--output-format", "stream-json", "--resume", "--sandbox", "--approval-mode", "--include-directories"}
	if a.ID == "deepseek" {
		args = []string{"headless", "--help"}
		required = []string{"--json", "--session-id", "--permission-mode", "--approval-policy", "--add-dir"}
	}
	help, e := probeCommand(ctx, p, args...)
	if e != nil {
		return info, a.failure("protocol_unsupported", "无法检查非交互协议，请升级 CLI。", e)
	}
	for _, flag := range required {
		if !strings.Contains(help, flag) {
			return info, a.failure("protocol_unsupported", "当前 CLI 不支持所需参数 "+flag+"，请升级。", nil)
		}
	}
	// macOS is the initial native Gemini sandbox target. Other platforms need
	// their own tested sandbox configuration, never an unsandboxed fallback.
	if a.ID == "gemini" && runtime.GOOS != "darwin" {
		return info, a.failure("sandbox_unavailable", "当前 Gemini 接入仅验证 macOS 原生沙箱。", nil)
	}
	info.Available = true
	return info, nil
}
func (a Headless) Start(ctx context.Context, c SessionConfig) (Session, error) {
	if c.ThreadID != "" {
		return nil, a.failure("invalid_session", "新会话不能携带已有线程。", nil)
	}
	return a.open(ctx, c)
}
func (a Headless) Resume(ctx context.Context, c SessionConfig) (Session, error) {
	if c.ThreadID == "" {
		return nil, a.failure("invalid_session", "恢复会话缺少原生会话标识。", nil)
	}
	return a.open(ctx, c)
}
func (a Headless) open(ctx context.Context, c SessionConfig) (Session, error) {
	if a.ID == "gemini" && c.ReasoningEffort != "" {
		return nil, a.failure("effort_unsupported", "当前 CLI 执行协议不支持单独设置推理强度。", nil)
	}
	if c.Manager {
		return nil, ErrUnsupported
	}
	c, e := prepareCodexConfig(c)
	if e != nil {
		return nil, e
	}
	info, e := a.Probe(ctx)
	if e != nil {
		return nil, e
	}

	return &headlessSession{adapter: a, config: c, info: info, done: make(chan struct{})}, nil
}

type headlessSession struct {
	adapter      Headless
	config       SessionConfig
	info         CLIInfo
	mu           sync.Mutex
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	done         chan struct{}
	sent, closed bool
	stopOnce     sync.Once
}

func (s *headlessSession) Steer(context.Context, string, string) error { return ErrUnsupported }
func (s *headlessSession) Interrupt(context.Context, string) error     { s.stop(); return nil }
func (s *headlessSession) stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		cancel, cmd := s.cancel, s.cmd
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if cmd != nil {
			force := platform.BeginTermination(cmd)
			timer := time.NewTimer(400 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-s.done:
			case <-timer.C:
			}
			force()
		}
	})
}
func (s *headlessSession) Close() error {
	s.stop()
	s.mu.Lock()
	sent := s.sent
	s.mu.Unlock()
	if !sent {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-time.After(3 * time.Second):
		return s.adapter.failure("close_timeout", "等待进程退出超时。", nil)
	}
}
func (s *headlessSession) command(r TurnRequest) (*exec.Cmd, error) {
	instructions := fmt.Sprintf("Blux task workspace: %s\nSave deliverables in %s. Internal context and exact inputs are in %s. Modify only these directories. Blux manages Git: do not commit, merge or push.\n%s\n\nUser task:\n%s", s.config.Workspace, s.config.ArtifactsPath, s.config.AgentPath, s.config.AdditionalInstructions, r.Prompt)
	args := []string{}
	env := os.Environ()
	if s.adapter.ID == "deepseek" {
		args = []string{"headless", "--json", "--permission-mode", "workspace-write", "--approval-policy", "never", "--add-dir", s.config.ArtifactsPath, "--add-dir", s.config.AgentPath}
		if s.config.Model != "" || s.config.ReasoningEffort != "" {
			patch, provider, e := deepSeekModelPatch(context.Background(), s.info.Path, s.config)
			if e != nil {
				return nil, e
			}
			s.config.ModelProvider = provider
			args = append([]string{"--profile", "headless", "--patch", patch}, args[1:]...)
		}
		if s.config.ThreadID != "" {
			args = append(args, "--session-id", s.config.ThreadID)
		}
		args = append(args, "-")
	} else {
		args = []string{"--output-format", "stream-json", "--sandbox", "--approval-mode", "yolo", "--include-directories", s.config.ArtifactsPath, "--include-directories", s.config.AgentPath, "--prompt", "Execute the Blux task provided on stdin."}
		if s.config.ThreadID != "" {
			args = append(args, "--resume", s.config.ThreadID)
		}
		if s.config.Model != "" {
			args = append(args, "--model", s.config.Model)
		}
		// Gemini treats an inherited SANDBOX marker as a reason to skip its own
		// sandbox. Clear that marker and pin the native backend/profile explicitly.
		env = replaceEnvironment(env, map[string]string{"GEMINI_SANDBOX": "sandbox-exec", "SEATBELT_PROFILE": "permissive-open"}, "SANDBOX", "SANDBOX_FLAGS", "SANDBOX_MOUNTS", "GEMINI_SANDBOX_PROXY_COMMAND")
	}
	cmd, e := platform.CodexCommand(context.Background(), s.info.Path, args...)
	if e != nil {
		return nil, e
	}
	cmd.Dir = s.config.Workspace
	cmd.Env = env
	cmd.Stdin = strings.NewReader(instructions)
	cmd.Stderr = &codexBoundedBuffer{limit: 16 << 10}
	platform.ConfigureProcess(cmd)
	return cmd, nil
}
func replaceEnvironment(env []string, values map[string]string, remove ...string) []string {
	drop := map[string]bool{}
	for k := range values {
		drop[k] = true
	}
	for _, k := range remove {
		drop[k] = true
	}
	out := []string{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if !drop[k] {
			out = append(out, v)
		}
	}
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	return out
}
func (s *headlessSession) Send(parent context.Context, r TurnRequest) (<-chan Update, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent || s.closed {
		return nil, s.adapter.failure("invalid_session", "本轮已提交或关闭。", nil)
	}
	ctx, cancel := context.WithCancel(parent)
	cmd, e := s.command(r)
	if e != nil {
		cancel()
		return nil, e
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		stdout.Close()
		cancel()
		return nil, s.adapter.failure("start_failed", "无法启动 CLI。", e)
	}
	release, e := platform.AttachProcessTree(cmd)
	if e != nil {
		platform.TerminateTree(cmd, true)
		cmd.Wait()
		cancel()
		return nil, e
	}
	forget, e := platform.RecordProcess(s.config.AgentPath, cmd.Process.Pid)
	if e != nil {
		platform.TerminateTree(cmd, true)
		cmd.Wait()
		release()
		cancel()
		return nil, e
	}
	s.sent = true
	s.cmd = cmd
	s.cancel = cancel
	updates := make(chan Update)
	go func() {
		defer close(updates)
		defer close(s.done)
		defer cancel()
		defer release()
		defer forget()
		state := newHeadlessState(s.adapter, s.config, s.info, r.TurnID, func(kind string, v any) error {
			b, e := json.Marshal(v)
			if e != nil {
				return e
			}
			select {
			case updates <- Update{Type: kind, Payload: b}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), codexMaxFrameBytes)
		var failure error
		for scanner.Scan() {
			if failure = state.consume(scanner.Bytes()); failure != nil {
				break
			}
		}
		if failure == nil {
			failure = scanner.Err()
		}
		if failure != nil {
			force := platform.BeginTermination(cmd)
			force()
		}
		waitErr := cmd.Wait()
		if failure == nil && ctx.Err() == nil {
			if !state.completed {
				failure = s.adapter.failure("process_exited", "CLI 在确认结果前退出，请检查本机配置。", waitErr)
			} else if waitErr != nil {
				failure = s.adapter.failure("execution_failed", "CLI 进程返回失败。", waitErr)
			}
		}
		if failure != nil {
			select {
			case updates <- Update{Err: failure}:
			case <-ctx.Done():
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			s.stop()
		case <-s.done:
		}
	}()
	return updates, nil
}
