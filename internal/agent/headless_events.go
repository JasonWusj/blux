package agent

import (
	"blux/internal/core"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type headlessFrame struct {
	Type            string          `json:"type"`
	SessionID       string          `json:"sessionId"`
	GeminiSessionID string          `json:"session_id"`
	Cwd             string          `json:"cwd"`
	Model           string          `json:"model"`
	Text            string          `json:"text"`
	Content         string          `json:"content"`
	Role            string          `json:"role"`
	Delta           bool            `json:"delta"`
	Phase           string          `json:"phase"`
	Status          string          `json:"status"`
	Message         string          `json:"message"`
	Severity        string          `json:"severity"`
	CallID          string          `json:"callId"`
	ToolID          string          `json:"tool_id"`
	Tool            string          `json:"tool"`
	ToolName        string          `json:"tool_name"`
	Input           json.RawMessage `json:"input"`
	Parameters      json.RawMessage `json:"parameters"`
	Result          string          `json:"result"`
	Output          string          `json:"output"`
	Reason          struct {
		Kind    string          `json:"kind"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	} `json:"reason"`
	Error json.RawMessage `json:"error"`
	Usage *struct {
		Input  int64 `json:"inputTokens"`
		Output int64 `json:"outputTokens"`
		Cached int64 `json:"cacheReadTokens"`
		Total  int64 `json:"totalTokens"`
	} `json:"usage"`
	Stats *struct {
		Input  int64 `json:"input_tokens"`
		Output int64 `json:"output_tokens"`
		Cached int64 `json:"cached"`
		Total  int64 `json:"total_tokens"`
	} `json:"stats"`
}
type headlessTool struct {
	name  string
	input json.RawMessage
}
type headlessState struct {
	adapter          Headless
	config           SessionConfig
	info             CLIInfo
	turn, id         string
	ready, completed bool
	step             int
	tools            map[string]headlessTool
	results          map[string]bool
	texts            map[string]string
	usage            core.TokenUsage
	emit             func(string, any) error
}

func newHeadlessState(a Headless, c SessionConfig, info CLIInfo, turn string, emit func(string, any) error) *headlessState {
	return &headlessState{adapter: a, config: c, info: info, turn: turn, tools: map[string]headlessTool{}, results: map[string]bool{}, texts: map[string]string{}, emit: emit}
}
func (s *headlessState) textItem() string { return fmt.Sprintf("%s-step-%d", s.turn, s.step) }
func (s *headlessState) fail(code, message string) error {
	return s.adapter.failure(code, core.OutputTail(message), nil)
}
func (s *headlessState) consume(raw []byte) error {
	var f headlessFrame
	if e := json.Unmarshal(raw, &f); e != nil {
		return s.fail("protocol_error", "CLI 返回了无效的结构化事件。")
	}
	id := f.SessionID
	if s.adapter.ID == "gemini" {
		id = f.GeminiSessionID
	}
	if id != "" && s.id != "" && id != s.id {
		return s.fail("session_mismatch", "CLI 返回的会话与持久记录不一致。")
	}
	if f.Type == "error" {
		message := f.Message
		if message == "" {
			message = "CLI 执行失败。"
		}
		if f.Severity == "warning" {
			return s.emit("agent.warning", map[string]string{"code": s.adapter.ID + "_warning", "message": core.OutputTail(message)})
		}
		return s.fail("execution_failed", message)
	}
	if f.Type == "session" && s.adapter.ID == "deepseek" || f.Type == "init" && s.adapter.ID == "gemini" {
		if id == "" {
			return s.fail("protocol_error", "初始化缺少原生会话标识。")
		}
		if s.config.ThreadID != "" && s.config.ThreadID != id {
			return s.fail("session_mismatch", "恢复了错误的原生会话。")
		}
		if s.adapter.ID == "deepseek" {
			p, e := filepath.EvalSymlinks(f.Cwd)
			if e != nil || p != s.config.Workspace {
				return s.fail("workspace_mismatch", "CLI 未确认本轮工作区。")
			}
		}
		if s.adapter.ID == "gemini" && (f.Model == "" || s.config.Model != "" && f.Model != s.config.Model) {
			return s.fail("model_mismatch", "CLI 未确认要求的模型。")
		}
		if s.ready {
			return nil
		}
		s.id = id
		s.ready = true
		model, provider := f.Model, s.adapter.ID
		if s.adapter.ID == "deepseek" {
			model = s.config.Model
			provider = s.config.ModelProvider
			if provider == "" {
				provider = "deepseek"
			}
		}
		if e := s.emit("agent.session.ready", map[string]string{"native_thread_id": id, "model": model, "model_provider": provider, "cli_version": s.info.Version, "reasoning_effort": s.config.ReasoningEffort}); e != nil {
			return e
		}
		return s.emit("agent.turn.started", map[string]string{"native_turn_id": "native_" + s.turn})
	}
	if !s.ready {
		return s.fail("protocol_error", "在初始化确认前收到执行事件。")
	}
	if s.completed {
		return nil
	}
	switch f.Type {
	case "status":
		if f.Phase == "step_start" {
			s.step++
		}
		if f.Phase == "step_end" && f.Usage != nil {
			s.usage.InputTokens += f.Usage.Input
			s.usage.OutputTokens += f.Usage.Output
			s.usage.CachedInputTokens += f.Usage.Cached
			stepTotal := f.Usage.Total
			if stepTotal == 0 {
				stepTotal = f.Usage.Input + f.Usage.Output
			}
			s.usage.TotalTokens += stepTotal
			return s.emit("usage.updated", s.usage)
		}
	case "text":
		item := s.textItem()
		s.texts[item] += f.Text
		return s.emit("message.delta", map[string]string{"message_id": "msg_" + s.turn, "item_id": item, "text": f.Text})
	case "message":
		if f.Role != "assistant" {
			return nil
		}
		item := s.textItem()
		kind := "message.delta"
		if f.Delta {
			s.texts[item] += f.Content
		} else {
			s.texts[item] = f.Content
			kind = "message.snapshot"
		}
		return s.emit(kind, map[string]string{"message_id": "msg_" + s.turn, "item_id": item, "text": f.Content})
	case "tool_call", "tool_use":
		toolID, name, input := f.CallID, f.Tool, f.Input
		if s.adapter.ID == "gemini" {
			toolID, name, input = f.ToolID, f.ToolName, f.Parameters
		}
		if toolID == "" || name == "" {
			return s.fail("protocol_error", "工具调用缺少标识。")
		}
		if _, exists := s.tools[toolID]; exists {
			return nil
		}
		s.tools[toolID] = headlessTool{name, input}
		return s.emit("tool.started", map[string]string{"message_id": "msg_" + s.turn, "tool_call_id": toolID, "name": name, "input": string(input)})
	case "tool_result":
		toolID, output := f.CallID, f.Result
		if s.adapter.ID == "gemini" {
			toolID, output = f.ToolID, f.Output
		}
		tool, exists := s.tools[toolID]
		if !exists || s.results[toolID] {
			return nil
		}
		s.results[toolID] = true
		status := "completed"
		if f.Status == "error" {
			status = "failed"
			if output == "" {
				output = headlessError(f.Error)
			}
		}
		if e := s.emit("tool.completed", map[string]string{"message_id": "msg_" + s.turn, "tool_call_id": toolID, "name": tool.name, "status": status, "output": core.OutputTail(output)}); e != nil {
			return e
		}
		if status == "completed" && (strings.Contains(strings.ToLower(tool.name), "write") || tool.name == "replace") {
			var input struct {
				Path      string `json:"file_path"`
				OtherPath string `json:"path"`
				Content   string `json:"content"`
				Old       string `json:"old_string"`
				New       string `json:"new_string"`
			}
			if json.Unmarshal(tool.input, &input) == nil {
				p := input.Path
				if p == "" {
					p = input.OtherPath
				}
				if p != "" {
					diff := "+" + core.OutputTail(input.Content)
					if tool.name == "replace" {
						diff = "-" + core.OutputTail(input.Old) + "\n+" + core.OutputTail(input.New)
					}
					return s.emit("file.changed", map[string]any{"message_id": "msg_" + s.turn, "item_id": toolID, "changes": []core.FileChange{{Path: p, Kind: "update", Diff: diff}}})
				}
			}
		}
	case "final":
		if s.adapter.ID != "deepseek" || f.Reason.Kind != "completed" {
			message := f.Reason.Message
			if message == "" {
				message = headlessError(f.Reason.Error)
			}
			if message == "" {
				message = "CLI 未成功完成本轮任务。"
			}
			return s.fail("execution_failed", message)
		}
		s.completed = true
		return s.emit("message.snapshot", map[string]string{"message_id": "msg_" + s.turn, "item_id": s.textItem(), "text": f.Text})
	case "result":
		if s.adapter.ID != "gemini" || f.Status != "success" {
			message := headlessError(f.Error)
			if message == "" {
				message = "CLI 未成功完成本轮任务。"
			}
			return s.fail("execution_failed", message)
		}
		s.completed = true
		if f.Stats != nil {
			return s.emit("usage.updated", core.TokenUsage{InputTokens: f.Stats.Input, OutputTokens: f.Stats.Output, CachedInputTokens: f.Stats.Cached, TotalTokens: f.Stats.Total})
		}
	}
	return nil
}
func headlessError(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return object.Message
	}
	return ""
}
