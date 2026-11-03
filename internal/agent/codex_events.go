package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"blux/internal/core"
)

type codexUsage struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

func (u codexUsage) valid() bool {
	return u.InputTokens >= 0 && u.CachedInputTokens >= 0 && u.OutputTokens >= 0 && u.ReasoningOutputTokens >= 0 && u.TotalTokens >= 0
}

func (u codexUsage) core() core.TokenUsage {
	return core.TokenUsage{InputTokens: u.InputTokens, CachedInputTokens: u.CachedInputTokens,
		OutputTokens: u.OutputTokens, ReasoningOutputTokens: u.ReasoningOutputTokens, TotalTokens: u.TotalTokens}
}

func (u codexUsage) add(v codexUsage) codexUsage {
	return codexUsage{InputTokens: u.InputTokens + v.InputTokens, CachedInputTokens: u.CachedInputTokens + v.CachedInputTokens,
		OutputTokens: u.OutputTokens + v.OutputTokens, ReasoningOutputTokens: u.ReasoningOutputTokens + v.ReasoningOutputTokens, TotalTokens: u.TotalTokens + v.TotalTokens}
}

func (u codexUsage) subtract(v codexUsage) codexUsage {
	return codexUsage{InputTokens: max(0, u.InputTokens-v.InputTokens), CachedInputTokens: max(0, u.CachedInputTokens-v.CachedInputTokens),
		OutputTokens: max(0, u.OutputTokens-v.OutputTokens), ReasoningOutputTokens: max(0, u.ReasoningOutputTokens-v.ReasoningOutputTokens), TotalTokens: max(0, u.TotalTokens-v.TotalTokens)}
}

type codexTurnError struct {
	Message           string          `json:"message"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo"`
	AdditionalDetails *string         `json:"additionalDetails"`
}

func (e codexTurnError) failure(fallback string) error {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "Codex 未能完成本轮任务。"
	}
	code := fallback
	var kind string
	_ = json.Unmarshal(e.CodexErrorInfo, &kind)
	switch kind {
	case "unauthorized":
		code = "codex_auth_required"
	case "sandboxError":
		code = "codex_sandbox_error"
	case "usageLimitExceeded", "rateLimitExceeded", "sessionBudgetExceeded":
		code = "codex_usage_limit"
	case "contextWindowExceeded":
		code = "codex_context_limit"
	}
	return executionError(code, message, nil)
}

type codexItem struct {
	Server           string            `json:"server"`
	Tool             string            `json:"tool"`
	Arguments        json.RawMessage   `json:"arguments"`
	Result           json.RawMessage   `json:"result"`
	Error            json.RawMessage   `json:"error"`
	Type             string            `json:"type"`
	ID               string            `json:"id"`
	Text             string            `json:"text"`
	Phase            string            `json:"phase"`
	Command          string            `json:"command"`
	Cwd              string            `json:"cwd"`
	Status           string            `json:"status"`
	AggregatedOutput *string           `json:"aggregatedOutput"`
	ExitCode         *int              `json:"exitCode"`
	DurationMs       *int64            `json:"durationMs"`
	Changes          []codexFileChange `json:"changes"`
}

type codexFileChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string  `json:"type"`
		MovePath *string `json:"move_path"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

func normalizedChanges(changes []codexFileChange) []core.FileChange {
	result := make([]core.FileChange, 0, len(changes))
	for _, change := range changes {
		result = append(result, core.FileChange{Path: change.Path, Kind: change.Kind.Type, Diff: change.Diff})
	}
	return result
}

type codexTurnWire struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  *codexTurnError `json:"error"`
}

type codexNotification struct {
	ThreadID   string            `json:"threadId"`
	TurnID     string            `json:"turnId"`
	ItemID     string            `json:"itemId"`
	Delta      string            `json:"delta"`
	Item       codexItem         `json:"item"`
	Turn       codexTurnWire     `json:"turn"`
	Error      codexTurnError    `json:"error"`
	WillRetry  bool              `json:"willRetry"`
	Changes    []codexFileChange `json:"changes"`
	ResponseID string            `json:"responseId"`
	Usage      *codexUsage       `json:"usage"`
	TokenUsage struct {
		Total  codexUsage  `json:"total"`
		Last   *codexUsage `json:"last"`
		Window *int64      `json:"modelContextWindow"`
	} `json:"tokenUsage"`
	Message   string `json:"message"`
	FromModel string `json:"fromModel"`
	ToModel   string `json:"toModel"`
}

type codexToolState struct {
	name      string
	output    string
	completed bool
	changes   []codexFileChange
}

type codexTurnState struct {
	messageID     string
	model         string
	nativeID      string
	texts         map[string]string
	tools         map[string]*codexToolState
	responses     map[string]bool
	usage         codexUsage
	hasExactUsage bool
	baseline      codexUsage
	baselineKnown bool
	latestTotal   codexUsage
	hasTotal      bool
	lastError     error
	emit          func(string, any) error
}

func (s *codexSession) runTurn(ctx context.Context, request TurnRequest, updates chan<- Update) {
	defer close(updates)
	if err := s.streamTurn(ctx, request, updates); err != nil {
		select {
		case updates <- Update{Err: err}:
		case <-ctx.Done():
		}
	}
}

func (s *codexSession) streamTurn(ctx context.Context, request TurnRequest, updates chan<- Update) error {
	emit := func(kind string, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return executionError("codex_protocol_error", "无法编码 Codex 执行事件。", err)
		}
		select {
		case updates <- Update{Type: kind, Payload: payload}:
			return nil
		case <-ctx.Done():
			return executionError("codex_canceled", "Codex 执行已取消。", ctx.Err())
		case <-s.ctx.Done():
			return s.readFailure()
		}
	}
	if err := emit("agent.session.ready", map[string]string{
		"native_thread_id": s.thread.Thread.ID, "model": s.thread.Model,
		"model_provider": s.thread.ModelProvider, "cli_version": s.info.Version, "reasoning_effort": s.thread.ReasoningEffort,
	}); err != nil {
		return err
	}
	var result struct {
		Turn codexTurnWire `json:"turn"`
	}
	params := map[string]any{
		"threadId": s.thread.Thread.ID, "clientUserMessageId": request.TurnID,
		"input": []any{map[string]any{"type": "text", "text": request.Prompt, "text_elements": []any{}}},
		"cwd":   s.config.Workspace, "approvalPolicy": "never", "sandboxPolicy": s.sandboxPolicy(),
	}
	if s.config.ReasoningEffort != "" {
		params["effort"] = s.config.ReasoningEffort
	}
	if err := s.call(ctx, "turn/start", params, &result); err != nil {
		return err
	}
	if result.Turn.ID == "" {
		return executionError("codex_protocol_error", "Codex 未返回原生回合标识。", nil)
	}
	if err := emit("agent.turn.started", map[string]string{"native_turn_id": result.Turn.ID}); err != nil {
		return err
	}
	state := codexTurnState{
		messageID: "msg_" + request.TurnID, nativeID: result.Turn.ID,
		texts: make(map[string]string), tools: make(map[string]*codexToolState), responses: make(map[string]bool),
		baselineKnown: !s.resumed, emit: emit, model: s.thread.Model,
	}
	for {
		select {
		case <-ctx.Done():
			return executionError("codex_canceled", "Codex 执行已取消。", ctx.Err())
		case envelope, ok := <-s.notifications:
			if !ok {
				if state.lastError != nil {
					return state.lastError
				}
				return s.readFailure()
			}
			var notification codexNotification
			if err := json.Unmarshal(envelope.Params, &notification); err != nil {
				return executionError("codex_protocol_error", "Codex 事件格式无效。", err)
			}
			if notification.ThreadID != "" && notification.ThreadID != s.thread.Thread.ID {
				continue
			}
			if envelope.Method == "thread/tokenUsage/updated" && notification.TurnID != state.nativeID {
				// Resume replays the previous turn's cumulative usage. It is a
				// baseline, never a new-turn charge or an assistant message.
				if !state.hasTotal && !state.hasExactUsage && notification.TokenUsage.Total.valid() {
					state.baseline, state.baselineKnown = notification.TokenUsage.Total, true
				}
				continue
			}
			if envelope.Method == "turn/completed" {
				if notification.Turn.ID != state.nativeID || notification.ThreadID != s.thread.Thread.ID {
					continue
				}
				switch notification.Turn.Status {
				case "completed":
					return state.finishUsage()
				case "failed":
					if notification.Turn.Error != nil {
						return notification.Turn.Error.failure("codex_turn_failed")
					}
					if state.lastError != nil {
						return state.lastError
					}
					return executionError("codex_turn_failed", "Codex 未能完成本轮任务。", nil)
				case "interrupted":
					return executionError("codex_interrupted", "Codex 回合已中断。", nil)
				default:
					return executionError("codex_protocol_error", "Codex 完成通知包含非终态状态。", nil)
				}
			}
			if notification.TurnID != "" && notification.TurnID != state.nativeID {
				continue
			}
			if err := state.handle(envelope.Method, notification); err != nil {
				return err
			}
		}
	}
}

func (state *codexTurnState) handle(method string, n codexNotification) error {
	switch method {
	case "item/agentMessage/delta":
		if n.ItemID == "" {
			return executionError("codex_protocol_error", "Codex 文本事件缺少条目标识。", nil)
		}
		if n.Delta == "" {
			return nil
		}
		state.texts[n.ItemID] += n.Delta
		return state.emit("message.delta", map[string]string{"message_id": state.messageID, "item_id": n.ItemID, "text": n.Delta})
	case "item/started", "item/completed":
		return state.item(n.Item, method == "item/completed")
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		kind := "commandExecution"
		if method == "item/fileChange/outputDelta" {
			kind = "fileChange"
		}
		if err := state.startTool(codexItem{ID: n.ItemID, Type: kind}); err != nil {
			return err
		}
		tool := state.tools[n.ItemID]
		if tool.completed || n.Delta == "" {
			return nil
		}
		tool.output = core.OutputTail(tool.output + n.Delta)
		return state.emit("tool.output", map[string]string{"message_id": state.messageID, "tool_call_id": n.ItemID, "delta": n.Delta})
	case "item/fileChange/patchUpdated":
		if err := state.startTool(codexItem{ID: n.ItemID, Type: "fileChange", Changes: n.Changes}); err != nil {
			return err
		}
		state.tools[n.ItemID].changes = n.Changes
		return state.emitFiles(n.ItemID, n.Changes, "inProgress")
	case "rawResponse/completed":
		if n.ResponseID == "" || n.Usage == nil || state.responses[n.ResponseID] {
			return nil
		}
		if !n.Usage.valid() {
			return executionError("codex_protocol_error", "Codex 用量事件包含无效计数。", nil)
		}
		state.responses[n.ResponseID] = true
		state.usage = state.usage.add(*n.Usage)
		if !state.usage.valid() {
			return executionError("codex_protocol_error", "Codex 用量计数溢出。", nil)
		}
		state.hasExactUsage = true
		return state.emit("usage.updated", state.usage.core())
	case "thread/tokenUsage/updated":
		if !n.TokenUsage.Total.valid() {
			return executionError("codex_protocol_error", "Codex 累计用量包含无效计数。", nil)
		}
		state.latestTotal, state.hasTotal = n.TokenUsage.Total, true
		if n.TokenUsage.Last != nil && n.TokenUsage.Last.valid() {
			context := core.ContextUsage{UsedTokens: n.TokenUsage.Last.TotalTokens, Source: "codex", Model: state.model}
			if n.TokenUsage.Window != nil && *n.TokenUsage.Window > 0 {
				context.WindowTokens = *n.TokenUsage.Window
			}
			if err := state.emit("context.updated", context); err != nil {
				return err
			}
		}
		// `last` is one response, and can also be a context estimate. Never
		// add it per notification. Exact response usage takes precedence.
	case "error":
		if n.WillRetry {
			message := strings.TrimSpace(n.Error.Message)
			if message == "" {
				message = "Codex 遇到暂时错误，正在重试。"
			}
			return state.emit("agent.warning", map[string]string{"code": "codex_retry", "message": message})
		}
		state.lastError = n.Error.failure("codex_turn_failed")
	case "model/rerouted":
		return state.emit("agent.warning", map[string]string{"code": "codex_model_rerouted", "message": fmt.Sprintf("Codex 将本次模型请求从 %s 路由到 %s。", n.FromModel, n.ToModel)})
	case "warning", "configWarning", "deprecationNotice":
		if n.Message != "" {
			return state.emit("agent.warning", map[string]string{"code": "codex_warning", "message": n.Message})
		}
	}
	return nil
}

func (state *codexTurnState) item(item codexItem, completed bool) error {
	switch item.Type {
	case "agentMessage":
		if item.ID == "" {
			return executionError("codex_protocol_error", "Codex 消息缺少条目标识。", nil)
		}
		if completed && state.texts[item.ID] != item.Text {
			state.texts[item.ID] = item.Text
			return state.emit("message.snapshot", map[string]string{"message_id": state.messageID, "item_id": item.ID, "text": item.Text})
		}
	case "commandExecution", "fileChange", "mcpToolCall":
		if err := state.startTool(item); err != nil {
			return err
		}
		tool := state.tools[item.ID]
		if !completed || tool.completed {
			return nil
		}
		tool.completed = true
		output := tool.output
		if item.Type == "mcpToolCall" {
			if len(item.Error) > 0 && string(item.Error) != "null" {
				output = string(item.Error)
			} else {
				output = string(item.Result)
			}
		}
		if item.AggregatedOutput != nil {
			output = *item.AggregatedOutput
		}
		if item.Type == "fileChange" {
			changes := item.Changes
			if changes == nil {
				changes = tool.changes
			}
			if err := state.emitFiles(item.ID, changes, item.Status); err != nil {
				return err
			}
			if output == "" {
				output = fmt.Sprintf("文件修改状态：%s，共 %d 项。", item.Status, len(changes))
			}
		}
		return state.emit("tool.completed", map[string]any{
			"message_id": state.messageID, "tool_call_id": item.ID, "name": tool.name,
			"output": output, "status": item.Status, "exit_code": item.ExitCode, "duration_ms": item.DurationMs,
		})
	}
	return nil
}

func (state *codexTurnState) startTool(item codexItem) error {
	if item.ID == "" {
		return executionError("codex_protocol_error", "Codex 工具事件缺少条目标识。", nil)
	}
	if state.tools[item.ID] != nil {
		return nil
	}
	name, input := "shell", item.Command
	if item.Type == "mcpToolCall" {
		name = item.Server + "/" + item.Tool
		input = string(item.Arguments)
		var text string
		if json.Unmarshal(item.Arguments, &text) == nil {
			input = text
		}
	}
	if item.Type == "fileChange" {
		name = "文件修改"
		encoded, _ := json.Marshal(normalizedChanges(item.Changes))
		input = string(encoded)
	}
	state.tools[item.ID] = &codexToolState{name: name, changes: item.Changes}
	return state.emit("tool.started", map[string]string{"message_id": state.messageID, "tool_call_id": item.ID, "name": name, "input": input})
}

func (state *codexTurnState) emitFiles(id string, changes []codexFileChange, status string) error {
	return state.emit("file.changed", map[string]any{
		"message_id": state.messageID, "item_id": id, "changes": normalizedChanges(changes), "status": status,
	})
}

func (state *codexTurnState) finishUsage() error {
	if state.hasExactUsage {
		return nil
	}
	if state.hasTotal && state.baselineKnown {
		return state.emit("usage.updated", state.latestTotal.subtract(state.baseline).core())
	}
	return state.emit("agent.warning", map[string]string{"code": "codex_usage_unavailable", "message": "Codex 未提供可确定归属本轮的用量，未将历史累计计入本轮。"})
}
