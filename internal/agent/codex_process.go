package agent

import (
	"blux/internal/platform"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

const codexMaxFrameBytes = 16 << 20

type codexEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type codexBoundedBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *codexBoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
	} else {
		if excess := len(b.data) + len(p) - b.limit; excess > 0 {
			b.data = b.data[excess:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *codexBoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func newCodexSession(ctx context.Context, info CodexInfo, config SessionConfig, resumed bool) (*codexSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, executionError("codex_canceled", "Codex 会话已取消。", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &codexSession{
		config: config, info: info, resumed: resumed, ctx: ctx, cancel: cancel,
		pending: make(map[int64]chan codexEnvelope), notifications: make(chan codexEnvelope, 1024),
		readerDone: make(chan struct{}), waitDone: make(chan struct{}), closed: make(chan struct{}), cleanupDone: make(chan struct{}),
	}
	command, commandErr := platform.CodexCommand(nil, info.Path, "app-server", "--listen", "stdio://")
	if commandErr != nil {
		cancel()
		return nil, executionError("codex_start_failed", "无法启动 Codex CLI，请检查安装。", commandErr)
	}
	command.Dir = config.Workspace
	platform.ConfigureProcess(command)
	command.Stderr = &codexBoundedBuffer{limit: 16 << 10}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, executionError("codex_start_failed", "无法打开 Codex 输入连接。", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, executionError("codex_start_failed", "无法打开 Codex 输出连接。", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		return nil, executionError("codex_start_failed", "无法启动 Codex app-server。", err)
	}
	releaseTree, attachErr := platform.AttachProcessTree(command)
	if attachErr != nil {
		_ = platform.TerminateTree(command, true)
		_ = stdin.Close()
		_ = stdout.Close()
		_ = command.Wait()
		cancel()
		return nil, executionError("codex_process_tracking_failed", "无法管理 Codex 子进程树。", attachErr)
	}
	s.process, s.stdin = command, stdin
	forget, err := platform.RecordProcess(config.AgentPath, command.Process.Pid)
	if err != nil {
		_ = platform.TerminateTree(command, true)
		_ = stdin.Close()
		_ = stdout.Close()
		_ = command.Wait()
		releaseTree()
		cancel()
		return nil, executionError("codex_process_tracking_failed", "无法保存 Codex 进程标识。", err)
	}
	s.forgetProcess = forget
	go s.read(stdout)
	go func() {
		// Wait closes StdoutPipe, so drain it before Wait to preserve trailing
		// notifications from a process that exits immediately after its turn.
		<-s.readerDone
		err := command.Wait()
		releaseTree()
		s.errMu.Lock()
		s.waitErr = err
		s.errMu.Unlock()
		if s.forgetProcess != nil {
			s.forgetProcess()
		}
		close(s.waitDone)
	}()
	go func() {
		select {
		case <-ctx.Done():
			s.shutdown()
		case <-s.waitDone:
		}
	}()
	return s, nil
}

func (s *codexSession) read(stdout io.Reader) {
	defer close(s.readerDone)
	defer close(s.notifications)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), codexMaxFrameBytes)
	for scanner.Scan() {
		frame := scanner.Bytes()
		if len(strings.TrimSpace(string(frame))) == 0 {
			continue
		}
		var envelope codexEnvelope
		if err := json.Unmarshal(frame, &envelope); err != nil {
			s.failReader(executionError("codex_protocol_error", "Codex 返回了无效的协议消息。", err))
			return
		}
		hasID := len(envelope.ID) > 0 && string(envelope.ID) != "null"
		if hasID && envelope.Method == "" {
			var id int64
			if err := json.Unmarshal(envelope.ID, &id); err != nil {
				s.failReader(executionError("codex_protocol_error", "Codex 响应包含无法识别的请求标识。", err))
				return
			}
			s.pendingMu.Lock()
			response := s.pending[id]
			s.pendingMu.Unlock()
			if response != nil {
				select {
				case response <- envelope:
				default:
				}
			}
			continue
		}
		if hasID {
			// These flows are intentionally not auto-approved or broadened.
			// Reply before failing so app-server cannot wait forever for a UI
			// that phase two does not implement.
			response := map[string]any{"id": envelope.ID}
			switch envelope.Method {
			case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
				response["result"] = map[string]string{"decision": "decline"}
			default:
				response["error"] = codexRPCError{Code: -32601, Message: "Blux does not support this server request"}
			}
			_ = s.writeContext(s.ctx, response)
			s.failReader(executionError("codex_interaction_unsupported", "Codex 请求了当前阶段不支持的审批或交互，执行已停止。", nil))
			return
		}
		if envelope.Method == "" {
			s.failReader(executionError("codex_protocol_error", "Codex 返回了缺少方法的通知。", nil))
			return
		}
		select {
		case s.notifications <- envelope:
		case <-s.ctx.Done():
			return
		default:
			s.failReader(executionError("codex_protocol_overflow", "Codex 事件积压过多，执行已停止以保留明确状态。", nil))
			return
		}
	}
	if err := scanner.Err(); err != nil && s.ctx.Err() == nil {
		s.failReader(executionError("codex_protocol_error", "Codex 输出连接中断或单条消息超过 16 MiB。", err))
	}
}

func (s *codexSession) failReader(err error) {
	s.errMu.Lock()
	if s.readErr == nil {
		s.readErr = err
	}
	s.errMu.Unlock()
	s.shutdown()
}

func (s *codexSession) readFailure() error {
	s.errMu.Lock()
	err := s.readErr
	s.errMu.Unlock()
	if err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return executionError("codex_canceled", "Codex 进程已停止。", s.ctx.Err())
	}
	return executionError("codex_process_exited", "Codex 在确认回合完成前关闭了输出连接。", nil)
}

func (s *codexSession) writeContext(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return executionError("codex_protocol_error", "无法编码 Codex 请求。", err)
	}
	data = append(data, '\n')
	s.pendingMu.Lock()
	if s.closing {
		s.pendingMu.Unlock()
		return s.readFailure()
	}
	s.writes.Add(1)
	s.pendingMu.Unlock()
	done := make(chan error, 1)
	go func() {
		defer s.writes.Done()
		s.writeMu.Lock()
		_, err := s.stdin.Write(data)
		s.writeMu.Unlock()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return executionError("codex_write_failed", "无法向 Codex 发送请求。", err)
		}
		return nil
	case <-ctx.Done():
		s.shutdown()
		return executionError("codex_request_timeout", "Codex 请求已取消或等待超时。", ctx.Err())
	case <-s.ctx.Done():
		return s.readFailure()
	}
}

func (s *codexSession) call(ctx context.Context, method string, params any, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.pendingMu.Lock()
	if s.closing {
		s.pendingMu.Unlock()
		return s.readFailure()
	}
	s.nextID++
	id := s.nextID
	response := make(chan codexEnvelope, 1)
	s.pending[id] = response
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
	}()
	if err := s.writeContext(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	var envelope codexEnvelope
	select {
	case envelope = <-response:
	case <-ctx.Done():
		return executionError("codex_request_timeout", "Codex 请求已取消或等待超时。", ctx.Err())
	case <-s.readerDone:
		// The final response may have been queued immediately before EOF.
		select {
		case envelope = <-response:
		default:
			return s.readFailure()
		}
	}
	if envelope.Error != nil {
		code := "codex_request_failed"
		message := "Codex 拒绝了会话或回合请求，请检查现有 Codex 配置。"
		if envelope.Error.Code == -32601 {
			code, message = "codex_app_server_unsupported", "当前 Codex CLI 不支持所需的 app-server 方法。"
		}
		text := strings.ToLower(envelope.Error.Message)
		if strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication") || strings.Contains(text, "api key") {
			code, message = "codex_auth_required", "Codex 凭据不可用，请先在现有 Codex CLI 中完成配置。"
		}
		return executionError(code, message, fmt.Errorf("%s failed with RPC code %d", method, envelope.Error.Code))
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return executionError("codex_protocol_error", "Codex 响应缺少有效结果。", nil)
	}
	if target != nil {
		if err := json.Unmarshal(envelope.Result, target); err != nil {
			return executionError("codex_protocol_error", "Codex 返回结果与所需协议不一致。", err)
		}
	}
	return nil
}

func (s *codexSession) shutdown() {
	s.closeOnce.Do(func() {
		s.pendingMu.Lock()
		s.closing = true
		s.pendingMu.Unlock()
		close(s.closed)
		s.cancel()
		_ = s.stdin.Close()
		force := platform.BeginTermination(s.process)
		go func() {
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-s.waitDone:
			case <-timer.C:
			}
			force()
			close(s.cleanupDone)
		}()
	})
}

func (s *codexSession) Close() error {
	s.shutdown()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-s.waitDone:
		select {
		case <-s.cleanupDone:
		case <-timer.C:
			return executionError("codex_close_timeout", "清理 Codex 子进程超时。", nil)
		}
		s.writes.Wait()
		return nil
	case <-timer.C:
		_ = s.process.Process.Kill()
		return executionError("codex_close_timeout", "等待 Codex 进程退出超时。", nil)
	}
}

// Keep integer IDs under our control, but accept raw server request IDs when
// replying. App-server permits both strings and integers for server requests.
func codexRequestID(id int64) json.RawMessage { return json.RawMessage(strconv.FormatInt(id, 10)) }

var _ = errors.Is
