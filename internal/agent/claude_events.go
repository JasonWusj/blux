package agent

import (
	"blux/internal/core"
	"encoding/json"
	"fmt"
	"strings"
)

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}
type claudeUsage struct {
	Input   int64 `json:"input_tokens"`
	Output  int64 `json:"output_tokens"`
	Cached  int64 `json:"cache_read_input_tokens"`
	Created int64 `json:"cache_creation_input_tokens"`
}
type claudeMessage struct {
	Model   string        `json:"model"`
	Usage   *claudeUsage  `json:"usage"`
	ID      string        `json:"id"`
	Content []claudeBlock `json:"content"`
}
type claudeFrame struct {
	Type            string        `json:"type"`
	Subtype         string        `json:"subtype"`
	SessionID       string        `json:"session_id"`
	Model           string        `json:"model"`
	PermissionMode  string        `json:"permissionMode"`
	Message         claudeMessage `json:"message"`
	ParentToolUseID *string       `json:"parent_tool_use_id"`
	Result          string        `json:"result"`
	IsError         bool          `json:"is_error"`
	Errors          []string      `json:"errors"`
	Usage           struct {
		Input   int64 `json:"input_tokens"`
		Output  int64 `json:"output_tokens"`
		Cached  int64 `json:"cache_read_input_tokens"`
		Created int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		ContextWindow int64 `json:"contextWindow"`
	} `json:"modelUsage"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
	Event             struct {
		Type    string        `json:"type"`
		Index   int           `json:"index"`
		Message claudeMessage `json:"message"`
		Delta   struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}
type claudeState struct {
	messageID, id          string
	context                *core.ContextUsage
	actualModel            string
	info                   ClaudeInfo
	config                 SessionConfig
	emit                   func(string, any) error
	ready, completed       bool
	streamID, lastTextItem string
	texts                  map[string]string
	tools                  map[string]claudeBlock
	results                map[string]bool
}

func newClaudeState(turn, id string, info ClaudeInfo, config SessionConfig, emit func(string, any) error) *claudeState {
	return &claudeState{messageID: "msg_" + turn, id: id, info: info, config: config, emit: emit, texts: map[string]string{}, tools: map[string]claudeBlock{}, results: map[string]bool{}}
}
func (s *claudeState) consume(raw []byte) error {
	var f claudeFrame
	if e := json.Unmarshal(raw, &f); e != nil {
		return executionError("claude_protocol_error", "Claude Code 返回了无效的事件。", e)
	}
	if f.SessionID != "" && f.SessionID != s.id {
		return executionError("claude_session_mismatch", "Claude Code 返回的会话与保存记录不一致。", nil)
	}
	if f.ParentToolUseID != nil {
		return nil
	}
	switch f.Type {
	case "system":
		if f.Subtype == "init" {
			if f.SessionID == "" || f.Model == "" || f.PermissionMode != "dontAsk" {
				return executionError("claude_protocol_error", "Claude Code 未确认要求的会话或权限模式。", nil)
			}
			if s.config.Model != "" && canonicalModel(s.config.Model) != canonicalModel(f.Model) {
				return executionError("claude_model_mismatch", "Claude Code 返回的模型与冻结配置不一致。", nil)
			}
			s.actualModel = f.Model
			s.ready = true
			if e := s.emit("agent.session.ready", map[string]string{"native_thread_id": f.SessionID, "model": canonicalModel(f.Model), "model_provider": "claude-code", "cli_version": s.info.Version, "reasoning_effort": s.config.ReasoningEffort}); e != nil {
				return e
			}
			return s.emit("agent.turn.started", map[string]string{"native_turn_id": s.messageID})
		}
		if f.Subtype == "permission_denied" {
			return s.emit("agent.warning", map[string]string{"code": "claude_permission_denied", "message": "Claude Code 拒绝了超出本轮权限的工具操作。"})
		}
	case "stream_event":
		if f.Event.Type == "message_start" {
			s.streamID = f.Event.Message.ID
		}
		if f.Event.Type == "content_block_delta" && f.Event.Delta.Type == "text_delta" && s.streamID != "" {
			id := fmt.Sprintf("%s:%d", s.streamID, f.Event.Index)
			s.texts[id] += f.Event.Delta.Text
			s.lastTextItem = id
			return s.emit("message.delta", map[string]string{"message_id": s.messageID, "item_id": id, "text": f.Event.Delta.Text})
		}
	case "assistant":
		if f.Message.Model != "" {
			s.actualModel = f.Message.Model
		}
		if u := f.Message.Usage; u != nil && u.Input >= 0 && u.Cached >= 0 && u.Created >= 0 && u.Output >= 0 {
			s.context = &core.ContextUsage{UsedTokens: u.Input + u.Cached + u.Created + u.Output, Model: s.actualModel, Source: "claude"}
			if e := s.emit("context.updated", s.context); e != nil {
				return e
			}
		}
		for index, b := range f.Message.Content {
			if b.Type == "text" {
				id := fmt.Sprintf("%s:%d", f.Message.ID, index)
				s.texts[id] = b.Text
				s.lastTextItem = id
				if e := s.emit("message.snapshot", map[string]string{"message_id": s.messageID, "item_id": id, "text": b.Text}); e != nil {
					return e
				}
			}
			if b.Type == "tool_use" && b.ID != "" {
				if _, ok := s.tools[b.ID]; ok {
					continue
				}
				s.tools[b.ID] = b
				if e := s.emit("tool.started", map[string]string{"message_id": s.messageID, "tool_call_id": b.ID, "name": b.Name, "input": string(b.Input)}); e != nil {
					return e
				}
			}
		}
	case "user":
		for _, b := range f.Message.Content {
			if b.Type != "tool_result" || s.results[b.ToolUseID] {
				continue
			}
			tool, ok := s.tools[b.ToolUseID]
			if !ok {
				continue
			}
			s.results[b.ToolUseID] = true
			status := "completed"
			if b.IsError {
				status = "failed"
			}
			output := claudeResultText(b.Content)
			if e := s.emit("tool.completed", map[string]string{"message_id": s.messageID, "tool_call_id": b.ToolUseID, "name": tool.Name, "output": core.OutputTail(output), "status": status}); e != nil {
				return e
			}
			if !b.IsError && (tool.Name == "Edit" || tool.Name == "Write") {
				var in struct {
					Path    string `json:"file_path"`
					Old     string `json:"old_string"`
					New     string `json:"new_string"`
					Content string `json:"content"`
				}
				json.Unmarshal(tool.Input, &in)
				diff := "-" + in.Old + "\n+" + in.New
				if tool.Name == "Write" {
					diff = "+" + core.OutputTail(in.Content)
				}
				if in.Path != "" {
					if e := s.emit("file.changed", map[string]any{"message_id": s.messageID, "item_id": tool.ID, "changes": []core.FileChange{{Path: in.Path, Kind: "update", Diff: diff}}}); e != nil {
						return e
					}
				}
			}
		}
	case "result":
		if f.IsError || f.Subtype != "success" {
			message := f.Result
			if message == "" && len(f.Errors) > 0 {
				message = f.Errors[0]
			}
			if message == "" {
				message = "Claude Code 未能完成本轮任务。"
			}
			code := "claude_execution_failed"
			lower := strings.ToLower(message)
			if strings.Contains(lower, "auth") || strings.Contains(lower, "api key") || strings.Contains(lower, "login") {
				code = "claude_auth_required"
			} else if strings.Contains(lower, "429") || strings.Contains(lower, "quota") || strings.Contains(message, "套餐已到期") {
				code = "claude_provider_unavailable"
			}
			return executionError(code, core.OutputTail(message), nil)
		}
		if !s.ready {
			return executionError("claude_protocol_error", "Claude Code 没有返回会话初始化信息。", nil)
		}
		if s.completed {
			return nil
		}
		s.completed = true
		if f.Result != "" {
			id := s.lastTextItem
			if id == "" {
				id = "claude-result"
			}
			if s.texts[id] != f.Result {
				if e := s.emit("message.snapshot", map[string]string{"message_id": s.messageID, "item_id": id, "text": f.Result}); e != nil {
					return e
				}
			}
		}
		if len(f.PermissionDenials) > 0 {
			if e := s.emit("agent.warning", map[string]string{"code": "claude_permission_denied", "message": "部分工具操作被 Claude Code 拒绝，请结合结果检查。"}); e != nil {
				return e
			}
		}
		if s.context != nil {
			if m, ok := f.ModelUsage[s.actualModel]; ok && m.ContextWindow > 0 {
				s.context.WindowTokens = m.ContextWindow
				if e := s.emit("context.updated", s.context); e != nil {
					return e
				}
			}
		}
		usage := core.TokenUsage{InputTokens: f.Usage.Input + f.Usage.Cached + f.Usage.Created, OutputTokens: f.Usage.Output, CachedInputTokens: f.Usage.Cached, TotalTokens: f.Usage.Input + f.Usage.Cached + f.Usage.Created + f.Usage.Output}
		return s.emit("usage.updated", usage)
	}
	return nil
}
func claudeResultText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var out []string
		for _, b := range blocks {
			if b.Text != "" {
				out = append(out, b.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return string(raw)
}
