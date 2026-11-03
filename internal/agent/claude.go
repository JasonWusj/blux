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
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Claude struct{ Binary string }
type ClaudeInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Path      string `json:"path"`
}

var claudeVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)\s+\(Claude Code\)`)

func ProbeClaude(ctx context.Context, binary string) (info ClaudeInfo, err error) {
	if binary == "" {
		binary = "claude"
	}
	p, e := exec.LookPath(binary)
	if e != nil {
		return info, executionError("claude_not_found", "找不到 Claude Code CLI，请先在本机安装并配置。", e)
	}
	info.Path, e = filepath.Abs(p)
	if e != nil {
		return info, e
	}
	output, e := probeCommand(ctx, info.Path, "--version")
	if e != nil {
		return info, executionError("claude_probe_failed", "无法运行 Claude Code CLI。", e)
	}
	match := claudeVersionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if match == nil {
		return info, executionError("claude_protocol_unsupported", "无法识别 Claude Code 版本。", nil)
	}
	info.Version = match[1] + "." + match[2] + "." + match[3]
	help, e := probeCommand(ctx, info.Path, "--help")
	if e != nil || !strings.Contains(help, "--restricted") || !strings.Contains(help, "--permission-prompts") || !strings.Contains(help, "--system-prompt-snapshot") {
		return info, executionError("claude_protocol_unsupported", "Claude Code 版本不支持当前接入协议，请升级至 2.1.301 或更新版本。", e)
	}
	if runtime.GOOS == "windows" {
		return info, executionError("claude_sandbox_unavailable", "原生 Windows 的 Claude Code 不提供所需沙箱；当前请使用 Codex，或在受支持的 macOS/WSL2 环境运行。", nil)
	}
	info.Available = true
	return info, nil
}
func (c Claude) Name() string { return "claude" }
func (c Claude) Capabilities() Capabilities {
	return Capabilities{Streaming: true, Resume: true, Interrupt: true}
}
func (c Claude) Start(ctx context.Context, config SessionConfig) (Session, error) {
	if config.ThreadID != "" {
		return nil, executionError("claude_invalid_session", "新建 Claude Code 会话不能携带已有线程。", nil)
	}
	return c.open(ctx, config, false)
}
func (c Claude) Resume(ctx context.Context, config SessionConfig) (Session, error) {
	if config.ThreadID == "" {
		return nil, executionError("claude_invalid_session", "恢复 Claude Code 会话缺少线程标识。", nil)
	}
	return c.open(ctx, config, true)
}
func (c Claude) open(ctx context.Context, config SessionConfig, resume bool) (Session, error) {
	config, e := prepareCodexConfig(config)
	if e != nil {
		return nil, e
	}
	info, e := ProbeClaude(ctx, c.Binary)
	if e != nil {
		return nil, e
	}
	if config.Manager {
		return nil, ErrUnsupported
	}
	id := config.ThreadID
	if !resume {
		v := strings.TrimPrefix(core.NewID("native"), "native_")
		id = v[:8] + "-" + v[8:12] + "-4" + v[13:16] + "-a" + v[17:20] + "-" + v[20:]
	}
	return &claudeSession{config: config, info: info, id: id, resume: resume, done: make(chan struct{})}, nil
}

type claudeSession struct {
	config   SessionConfig
	info     ClaudeInfo
	id       string
	resume   bool
	mu       sync.Mutex
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	done     chan struct{}
	sent     bool
	closed   bool
	stopOnce sync.Once
}

func (s *claudeSession) Steer(context.Context, string, string) error { return ErrUnsupported }
func (s *claudeSession) Interrupt(context.Context, string) error     { s.stop(); return nil }
func (s *claudeSession) stop() {
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
func (s *claudeSession) Close() error {
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
		return executionError("claude_close_timeout", "等待 Claude Code 退出超时。", nil)
	}
}
func (s *claudeSession) Send(ctx context.Context, r TurnRequest) (<-chan Update, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent || s.closed {
		return nil, executionError("claude_invalid_session", "本轮 Claude Code 会话已提交或关闭。", nil)
	}
	ctx, cancel := context.WithCancel(ctx)
	settings := map[string]any{"disableAllHooks": true, "autoMemoryEnabled": false, "disableClaudeAiConnectors": true, "permissions": map[string]any{"additionalDirectories": []string{s.config.ArtifactsPath, s.config.AgentPath}}, "sandbox": map[string]any{"enabled": true, "failIfUnavailable": true, "autoAllowBashIfSandboxed": true, "allowUnsandboxedCommands": false, "excludedCommands": []string{}, "filesystem": map[string]any{"allowWrite": []string{s.config.Workspace, s.config.ArtifactsPath, s.config.AgentPath}}, "network": map[string]any{"allowedDomains": []string{"*"}, "strictAllowlist": true}}}
	encoded, _ := json.Marshal(settings)
	instructions := fmt.Sprintf("You are executing a Blux task in %s. Modify only this task workspace and %s and %s. Blux manages Git; do not commit, merge or push. Return results to the task and save deliverables in %s. Do not contact other people or services for messaging.\n%s", s.config.Workspace, s.config.ArtifactsPath, s.config.AgentPath, s.config.ArtifactsPath, s.config.AdditionalInstructions)
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--restricted", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--tools", "Read,Edit,Write,Bash,Glob,Grep,WebFetch,WebSearch", "--allowedTools", "Read,Edit,Write,Bash,Glob,Grep,WebFetch,WebSearch", "--disable-slash-commands", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--settings", string(encoded), "--system-prompt-snapshot", "off", "--append-system-prompt", instructions, "--add-dir", s.config.ArtifactsPath, s.config.AgentPath}
	if s.resume {
		args = append(args, "--resume", s.id)
	} else {
		args = append(args, "--session-id", s.id)
	}
	if s.config.ReasoningEffort != "" {
		args = append(args, "--effort", s.config.ReasoningEffort)
	}
	if s.config.Model != "" {
		args = append(args, "--model", s.config.Model)
	}
	cmd := exec.Command(s.info.Path, args...)
	// Restricted mode deliberately skips user settings. Reuse only provider env
	// and model selection from that file, without loading hooks/plugins or
	// putting credentials in arguments, logs, or a generated settings file.
	environment, e := claudeEnvironment()
	if e != nil {
		cancel()
		return nil, e
	}
	cmd.Env = environment
	cmd.Dir = s.config.Workspace
	cmd.Stdin = strings.NewReader(r.Prompt)
	cmd.Stderr = &codexBoundedBuffer{limit: 16 << 10}
	platform.ConfigureProcess(cmd)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		cancel()
		stdout.Close()
		return nil, executionError("claude_start_failed", "无法启动 Claude Code。", e)
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
	s.cancel = cancel
	s.cmd = cmd
	updates := make(chan Update)
	go func() {
		defer close(updates)
		defer close(s.done)
		defer cancel()
		defer release()
		defer forget()
		state := newClaudeState(r.TurnID, s.id, s.info, s.config, func(kind string, value any) error {
			b, e := json.Marshal(value)
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
			if e = state.consume(scanner.Bytes()); e != nil {
				failure = e
				break
			}
		}
		if failure == nil {
			failure = scanner.Err()
		}
		if failure != nil {
			_ = platform.TerminateTree(cmd, true)
		}
		waitErr := cmd.Wait()
		if failure == nil && ctx.Err() == nil {
			if !state.completed {
				failure = executionError("claude_process_exited", "Claude Code 在确认结果前退出，请检查其本机配置或运行日志。", waitErr)
			} else if waitErr != nil {
				failure = executionError("claude_execution_failed", "Claude Code 进程返回失败。", waitErr)
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

func claudeEnvironment() ([]string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".claude")
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if os.IsNotExist(err) {
		return os.Environ(), nil
	}
	if err != nil {
		return nil, executionError("claude_config_unavailable", "无法读取 Claude Code 本机配置。", nil)
	}
	var settings struct {
		Env   map[string]string `json:"env"`
		Model string            `json:"model"`
	}
	if len(data) > 1<<20 || json.Unmarshal(data, &settings) != nil {
		return nil, executionError("claude_config_invalid", "Claude Code 本机配置格式无效。", nil)
	}
	env := os.Environ()
	for key, value := range settings.Env {
		if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_USE_") || key == "CLAUDE_CODE_SUBAGENT_MODEL" {
			if _, exists := os.LookupEnv(key); !exists {
				env = append(env, key+"="+value)
			}
		}
	}
	if settings.Model != "" && settings.Env["ANTHROPIC_MODEL"] == "" && os.Getenv("ANTHROPIC_MODEL") == "" {
		env = append(env, "ANTHROPIC_MODEL="+settings.Model)
	}
	return env, nil
}
