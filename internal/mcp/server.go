package mcp

import (
	"blux/internal/core"
	"blux/internal/flow"
	"blux/internal/store"
	"blux/internal/workspace"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Server struct {
	Store   *store.Store
	DataDir string
	Project string
}
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func object(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func str() map[string]string { return map[string]string{"type": "string"} }
func tools() []tool {
	return []tool{{"list_tasks", "查询本地任务状态。", object(map[string]any{})}, {"get_task", "查询任务最近回复与状态。", object(map[string]any{"task_id": str()}, "task_id")}, {"list_deliveries", "查询交付流程与人工验收关口。", object(map[string]any{})}, {"get_delivery", "查询交付精确阶段和版本。", object(map[string]any{"delivery_id": str()}, "delivery_id")}, {"create_task", "在用户已选择的项目中创建独立执行任务。必须使用稳定的幂等键。", object(map[string]any{"prompt": str(), "mode": map[string]any{"type": "string", "enum": []string{"spec", "development", "analysis", "explore"}}, "adapter": map[string]any{"type": "string", "enum": []string{"codex"}}, "idempotency_key": str()}, "prompt", "idempotency_key")}, {"start_delivery", "从一句目标启动设计、开发和验证交付。设计与验证都等待用户人工验收。", object(map[string]any{"goal": str(), "adapter": map[string]any{"type": "string", "enum": []string{"codex"}}, "idempotency_key": str()}, "goal", "idempotency_key")}, {"request_review", "通知用户当前阶段需要人工验收；不会通过验收或推进流程。", object(map[string]any{"delivery_id": str(), "phase_id": str(), "generation": map[string]string{"type": "integer"}, "message": str()}, "delivery_id", "phase_id", "generation", "message")}}
}
func (s Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	writer := bufio.NewWriter(out)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if e := json.Unmarshal(scanner.Bytes(), &req); e != nil {
			if e = respond(writer, nil, nil, -32700, "Invalid JSON"); e != nil {
				return e
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		var result any
		code := 0
		message := ""
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &p)
			version := p.ProtocolVersion
			switch version {
			case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
			default:
				version = "2025-11-25"
			}
			result = map[string]any{"protocolVersion": version, "serverInfo": map[string]string{"name": "blux", "version": "0.7.0"}, "capabilities": map[string]any{"tools": map[string]any{}}, "instructions": "Blux 本地个人管家。当前项目：" + s.Project + "。工具可查询、创建任务和启动交付，只能请求人工验收，不能代替用户审批或接收。"}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": tools()}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if e := json.Unmarshal(req.Params, &p); e != nil {
				code, message = -32602, "Invalid params"
				break
			}
			v, e := s.call(ctx, p.Name, p.Arguments)
			text := ""
			if e != nil {
				text = e.Error()
			} else {
				b, _ := json.Marshal(v)
				text = string(b)
			}
			result = map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}, "isError": e != nil}
		default:
			code, message = -32601, "Method not found"
		}
		if e := respond(writer, req.ID, result, code, message); e != nil {
			return e
		}
	}
	return scanner.Err()
}
func respond(w *bufio.Writer, id json.RawMessage, result any, code int, message string) error {
	v := map[string]any{"jsonrpc": "2.0", "id": id}
	if code != 0 {
		v["error"] = map[string]any{"code": code, "message": message}
	} else {
		v["result"] = result
	}
	if e := json.NewEncoder(w).Encode(v); e != nil {
		return e
	}
	return w.Flush()
}
func decode(b json.RawMessage, v any) error {
	if len(b) == 0 {
		b = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return core.ErrInvalidState
	}
	return nil
}
func (s Server) call(ctx context.Context, name string, b json.RawMessage) (any, error) {
	switch name {
	case "list_tasks":
		if e := decode(b, &struct{}{}); e != nil {
			return nil, e
		}
		return s.Store.ListTasks(ctx)
	case "get_task":
		var p struct {
			TaskID string `json:"task_id"`
		}
		if e := decode(b, &p); e != nil {
			return nil, e
		}
		d, e := s.Store.GetTask(ctx, p.TaskID)
		if e != nil {
			return nil, e
		}
		var last *core.Message
		if len(d.Messages) > 0 {
			m := d.Messages[len(d.Messages)-1]
			for i := range m.Segments {
				m.Segments[i].Text = core.OutputTail(m.Segments[i].Text)
				m.Segments[i].Output = core.OutputTail(m.Segments[i].Output)
			}
			last = &m
		}
		return map[string]any{"task": d.Task, "turns": d.Turns, "last_message": last}, nil
	case "list_deliveries":
		if e := decode(b, &struct{}{}); e != nil {
			return nil, e
		}
		return s.Store.Deliveries(ctx)
	case "get_delivery":
		var p struct {
			DeliveryID string `json:"delivery_id"`
		}
		if e := decode(b, &p); e != nil {
			return nil, e
		}
		return s.Store.Delivery(ctx, p.DeliveryID)
	case "create_task":
		var p struct {
			Prompt  string `json:"prompt"`
			Mode    string `json:"mode"`
			Adapter string `json:"adapter"`
			Key     string `json:"idempotency_key"`
		}
		if e := decode(b, &p); e != nil {
			return nil, e
		}
		if p.Adapter == "" {
			p.Adapter = "codex"
		}
		if p.Key == "" || strings.TrimSpace(p.Prompt) == "" || len(p.Prompt) > 64000 {
			return nil, core.ErrInvalidState
		}
		in := core.CreateTaskInput{Prompt: p.Prompt, Mode: p.Mode, Adapter: p.Adapter, RepositoryPath: s.Project, IdempotencyKey: "mcp:" + p.Key}
		if d, found, e := s.Store.LookupTaskCreation(ctx, in); found || e != nil {
			return d, e
		}
		if p.Adapter != "" {
			repo, e := (workspace.Manager{DataDir: s.DataDir}).Freeze(ctx, s.Project)
			if e != nil {
				return nil, e
			}
			in.BaseCommit = repo.BaseCommit
		}
		d, _, e := s.Store.CreateTask(ctx, in)
		return d, e
	case "start_delivery":
		var p struct {
			Goal    string `json:"goal"`
			Adapter string `json:"adapter"`
			Key     string `json:"idempotency_key"`
		}
		if e := decode(b, &p); e != nil {
			return nil, e
		}
		if p.Adapter == "" {
			p.Adapter = "codex"
		}
		if p.Key == "" || strings.TrimSpace(p.Goal) == "" || len(p.Goal) > 64000 {
			return nil, core.ErrInvalidState
		}
		service := flow.Service{Store: s.Store, DataDir: s.DataDir}
		in := flow.CreateInput{Goal: p.Goal, Adapter: p.Adapter, RepositoryPath: s.Project}
		if d, found, e := service.LookupCreate(ctx, in, "mcp:"+p.Key); found || e != nil {
			return d, e
		}
		if p.Adapter != "" {
			repo, e := (workspace.Manager{DataDir: s.DataDir}).Freeze(ctx, s.Project)
			if e != nil {
				return nil, e
			}
			in.BaseCommit = repo.BaseCommit
		}
		return service.Create(ctx, in, "mcp:"+p.Key)
	case "request_review":
		var p struct {
			DeliveryID string `json:"delivery_id"`
			PhaseID    string `json:"phase_id"`
			Generation int64  `json:"generation"`
			Message    string `json:"message"`
		}
		if e := decode(b, &p); e != nil {
			return nil, e
		}
		if len(p.Message) > 16000 {
			return nil, core.ErrInvalidState
		}
		e := s.Store.RequestReview(ctx, p.DeliveryID, p.Generation, p.PhaseID, p.Message)
		return map[string]string{"status": "requested", "message": "等待用户人工验收，流程未推进。"}, e
	default:
		return nil, fmt.Errorf("unknown or unavailable tool %q", name)
	}
}
