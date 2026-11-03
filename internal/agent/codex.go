package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"blux/internal/core"
	"blux/internal/platform"
)

// Codex uses the installed CLI's app-server. The CLI owns model access and its
// native sandbox; Blux neither reads credential files nor wraps the whole agent
// in another sandbox. Wire baseline: codex-cli 0.163.0.
type Codex struct{ Binary string }

type CodexInfo struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
	Path      string `json:"path"`
}

var codexVersionPattern = regexp.MustCompile(`(?m)^codex-cli\s+(\d+)\.(\d+)\.(\d+)(?:[-+][^\s]+)?\s*$`)

func executionError(code, message string, err error) *core.ExecutionError {
	return &core.ExecutionError{Code: code, Message: message, Err: err}
}

// ProbeCodex only invokes --version and app-server --help; it never starts a
// model turn or inspects the user's configuration or credentials.
func ProbeCodex(ctx context.Context, binary string) (CodexInfo, error) {
	if strings.TrimSpace(binary) == "" {
		binary = "codex"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return CodexInfo{}, executionError("codex_not_found", "找不到 Codex CLI，请先安装并配置 Codex。", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return CodexInfo{}, executionError("codex_not_found", "无法解析 Codex CLI 路径。", err)
	}
	info := CodexInfo{Path: path}
	output, err := probeCommand(ctx, path, "--version")
	if err != nil {
		return info, executionError("codex_probe_failed", "无法运行 Codex CLI 版本检查。", err)
	}
	match := codexVersionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if match == nil {
		return info, executionError("codex_unsupported_version", "无法识别 Codex CLI 版本，请使用 0.163.0 或更新版本。", nil)
	}
	info.Version = match[1] + "." + match[2] + "." + match[3]
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major == 0 && minor < 163 {
		return info, executionError("codex_unsupported_version", "Codex CLI 版本过旧，请使用 0.163.0 或更新版本。", nil)
	}
	help, err := probeCommand(ctx, path, "app-server", "--help")
	if err != nil || !strings.Contains(help, "--listen") || !strings.Contains(help, "stdio://") {
		return info, executionError("codex_app_server_unsupported", "当前 Codex CLI 不支持所需的 app-server stdio 协议。", err)
	}
	info.Available = true
	return info, nil
}

func probeCommand(ctx context.Context, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command, err := platform.CodexCommand(ctx, binary, args...)
	if err != nil {
		return "", err
	}
	output := &codexBoundedBuffer{limit: 64 << 10}
	command.Stdout = output
	command.Stderr = &codexBoundedBuffer{limit: 4096}
	err = command.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return output.String(), err
}

func (c Codex) Name() string { return "codex" }
func (c Codex) Capabilities() Capabilities {
	return Capabilities{Streaming: true, Resume: true, Steer: true, Interrupt: true}
}

func (c Codex) Start(ctx context.Context, config SessionConfig) (Session, error) {
	if config.ThreadID != "" {
		return nil, executionError("codex_invalid_session", "创建 Codex 会话时不能指定已有线程。", nil)
	}
	return c.open(ctx, config, false)
}

func (c Codex) Resume(ctx context.Context, config SessionConfig) (Session, error) {
	if strings.TrimSpace(config.ThreadID) == "" {
		return nil, executionError("codex_invalid_session", "恢复 Codex 会话需要已保存的原生线程标识。", nil)
	}
	return c.open(ctx, config, true)
}

type codexThreadResponse struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Thread          struct {
		ID     string `json:"id"`
		Status struct {
			Type string `json:"type"`
		} `json:"status"`
	} `json:"thread"`
	Model          string             `json:"model"`
	ModelProvider  string             `json:"modelProvider"`
	Cwd            string             `json:"cwd"`
	ApprovalPolicy json.RawMessage    `json:"approvalPolicy"`
	Sandbox        codexSandboxPolicy `json:"sandbox"`
}

type codexSandboxPolicy struct {
	Type                string   `json:"type"`
	WritableRoots       []string `json:"writableRoots"`
	NetworkAccess       bool     `json:"networkAccess"`
	ExcludeTmpdirEnvVar bool     `json:"excludeTmpdirEnvVar"`
	ExcludeSlashTmp     bool     `json:"excludeSlashTmp"`
}

func canonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("directory must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	stat, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !stat.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return filepath.Clean(canonical), nil
}

func prepareCodexConfig(config SessionConfig) (SessionConfig, error) {
	var err error
	config.Workspace, err = canonicalDirectory(config.Workspace)
	if err != nil {
		return config, executionError("codex_invalid_workspace", "Codex 工作区不存在或不是绝对目录。", err)
	}
	config.ArtifactsPath, err = canonicalDirectory(config.ArtifactsPath)
	if err != nil {
		return config, executionError("codex_invalid_workspace", "Codex 产物目录不存在或不是绝对目录。", err)
	}
	config.AgentPath, err = canonicalDirectory(config.AgentPath)
	if err != nil {
		return config, executionError("codex_invalid_workspace", "Codex 运行目录不存在或不是绝对目录。", err)
	}
	return config, nil
}

func (c Codex) open(ctx context.Context, config SessionConfig, resume bool) (_ Session, err error) {
	config, err = prepareCodexConfig(config)
	if err != nil {
		return nil, err
	}
	info, err := ProbeCodex(ctx, c.Binary)
	if err != nil {
		return nil, err
	}
	s, err := newCodexSession(ctx, info, config, resume)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	if err = s.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "blux", "title": "Blux", "version": "0.2.0"},
	}, nil); err != nil {
		return nil, err
	}
	if err = s.writeContext(ctx, map[string]any{"method": "initialized"}); err != nil {
		return nil, err
	}
	params := map[string]any{
		"cwd": config.Workspace, "approvalPolicy": "never", "sandbox": "workspace-write",
		"config": map[string]any{
			"sandbox_workspace_write.network_access": true,
			"sandbox_workspace_write.writable_roots": []string{config.ArtifactsPath, config.AgentPath},
			// Request overrides are in-memory. Marking the canonical cwd here
			// avoids app-server auto-persisting project trust into global config.
			"projects": map[string]any{config.Workspace: map[string]string{"trust_level": "trusted"}},
		},
		"developerInstructions": fmt.Sprintf(
			"Work only in the code workspace %s and the task directories %s and %s. Do not modify other repositories. Do not run git commit, git push, or merge. Do not send messages to external services or people. Return your result to this conversation. The task controller manages Git and delivery.",
			config.Workspace, config.ArtifactsPath, config.AgentPath) + "\n" + config.AdditionalInstructions,
	}
	if config.Manager {
		params["sandbox"] = "read-only"
		override := params["config"].(map[string]any)
		override["features.shell_tool"] = false
		override["features.unified_exec"] = false
		override["web_search"] = "disabled"
		override["mcp_servers"] = map[string]any{"blux": map[string]any{"command": config.MCPCommand, "args": config.MCPArgs, "enabled": true, "required": true, "default_tools_approval_mode": "approve", "tool_timeout_sec": 60, "enabled_tools": []string{"list_tasks", "get_task", "list_deliveries", "get_delivery", "create_task", "start_delivery", "request_review"}}}
	}
	if config.ReasoningEffort != "" {
		params["config"].(map[string]any)["model_reasoning_effort"] = config.ReasoningEffort
	}
	if config.Model != "" {
		params["model"] = config.Model
	}
	if config.ModelProvider != "" {
		params["modelProvider"] = config.ModelProvider
	}
	method := "thread/start"
	if resume {
		method = "thread/resume"
		params["threadId"] = config.ThreadID
		// Normal resume restores usage notifications. Old items are not
		// projected again; the adapter consumes only the new native turn.
		params["excludeTurns"] = false
	} else {
		params["ephemeral"] = false
	}
	if err = s.call(ctx, method, params, &s.thread); err != nil {
		return nil, err
	}
	if err = s.validateThread(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *codexSession) sandboxPolicy() codexSandboxPolicy {
	if s.config.Manager {
		return codexSandboxPolicy{Type: "readOnly", NetworkAccess: false}
	}
	return codexSandboxPolicy{
		Type: "workspaceWrite", WritableRoots: []string{s.config.ArtifactsPath, s.config.AgentPath},
		NetworkAccess: true,
	}
}

func (s *codexSession) validateThread() error {
	thread := s.thread
	if thread.Thread.ID == "" || thread.Model == "" || thread.ModelProvider == "" {
		return executionError("codex_protocol_error", "Codex 会话响应缺少线程、模型或服务商信息。", nil)
	}
	if s.resumed && thread.Thread.ID != s.config.ThreadID {
		return executionError("codex_session_mismatch", "Codex 返回的线程与已保存会话不一致。", nil)
	}
	if thread.Thread.Status.Type != "idle" {
		return executionError("codex_session_busy", "Codex 会话不是空闲状态，无法开始新的回合。", nil)
	}
	if s.config.Model != "" && thread.Model != s.config.Model || s.config.ModelProvider != "" && thread.ModelProvider != s.config.ModelProvider {
		return executionError("codex_model_mismatch", "Codex 返回的模型或服务商与任务冻结配置不一致。", nil)
	}
	if s.config.ReasoningEffort != "" && thread.ReasoningEffort != s.config.ReasoningEffort {
		return executionError("codex_effort_mismatch", "Codex 未确认本轮选择的推理强度。", nil)
	}
	actualCwd, err := canonicalDirectory(thread.Cwd)
	if err != nil || actualCwd != s.config.Workspace {
		return executionError("codex_sandbox_mismatch", "Codex 返回的工作目录与任务工作区不一致。", err)
	}
	var approval string
	if s.config.Manager {
		if json.Unmarshal(thread.ApprovalPolicy, &approval) != nil || approval != "never" || thread.Sandbox.Type != "readOnly" || thread.Sandbox.NetworkAccess {
			return executionError("codex_sandbox_mismatch", "管家未采用只读、禁用直连网络的策略。", nil)
		}
		return nil
	}
	if json.Unmarshal(thread.ApprovalPolicy, &approval) != nil || approval != "never" || thread.Sandbox.Type != "workspaceWrite" || !thread.Sandbox.NetworkAccess {
		return executionError("codex_sandbox_mismatch", "Codex 未采用要求的工作区沙箱、网络或无需审批配置。", nil)
	}
	expected := map[string]bool{s.config.Workspace: true, s.config.ArtifactsPath: true, s.config.AgentPath: true}
	found := map[string]bool{}
	for _, root := range thread.Sandbox.WritableRoots {
		canonical, err := canonicalDirectory(root)
		if err != nil || !expected[canonical] {
			return executionError("codex_sandbox_mismatch", "Codex 返回了任务范围之外的可写目录。", err)
		}
		found[canonical] = true
	}
	if !found[s.config.ArtifactsPath] || !found[s.config.AgentPath] {
		return executionError("codex_sandbox_mismatch", "Codex 未保留任务产物与运行目录的写入权限。", nil)
	}
	return nil
}

func (s *codexSession) Send(ctx context.Context, request TurnRequest) (<-chan Update, error) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.sent {
		return nil, executionError("codex_invalid_session", "一个 Codex 进程会话只能提交一个回合；续聊请恢复已保存线程。", nil)
	}
	if strings.TrimSpace(request.TurnID) == "" || strings.TrimSpace(request.Prompt) == "" {
		return nil, executionError("codex_invalid_turn", "Codex 回合标识与目标不能为空。", nil)
	}
	select {
	case <-s.closed:
		return nil, executionError("codex_process_exited", "Codex 进程已退出。", nil)
	default:
	}
	s.sent = true
	updates := make(chan Update)
	go s.runTurn(ctx, request, updates)
	return updates, nil
}

func (s *codexSession) Steer(ctx context.Context, nativeID, prompt string) error {
	return s.call(ctx, "turn/steer", map[string]any{"threadId": s.thread.Thread.ID, "expectedTurnId": nativeID, "input": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}}, nil)
}
func (s *codexSession) Interrupt(ctx context.Context, nativeID string) error {
	return s.call(ctx, "turn/interrupt", map[string]any{"threadId": s.thread.Thread.ID, "turnId": nativeID}, nil)
}

// The session lifecycle is kept out of the worker: every process is explicitly
// closed and reaped before a later continuation resumes the persisted thread.
type codexSession struct {
	config  SessionConfig
	info    CodexInfo
	thread  codexThreadResponse
	resumed bool
	process *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
	stdin   interface {
		Write([]byte) (int, error)
		Close() error
	}
	writeMu       sync.Mutex
	writes        sync.WaitGroup
	pendingMu     sync.Mutex
	pending       map[int64]chan codexEnvelope
	closing       bool
	nextID        int64
	notifications chan codexEnvelope
	readerDone    chan struct{}
	waitDone      chan struct{}
	closed        chan struct{}
	closeOnce     sync.Once
	forgetProcess func()
	cleanupDone   chan struct{}
	errMu         sync.Mutex
	readErr       error
	waitErr       error
	sendMu        sync.Mutex
	sent          bool
}
