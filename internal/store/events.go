package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"blux/internal/core"
)

const eventColumns = "id,schema_version,task_id,session_id,turn_id,generation,seq,type,payload,created_at"

func scanEvent(row scanner) (core.Event, error) {
	var e core.Event
	var payload string
	err := row.Scan(&e.ID, &e.SchemaVersion, &e.TaskID, &e.SessionID, &e.TurnID, &e.Generation, &e.Seq, &e.Type, &payload, &e.CreatedAt)
	e.Payload = json.RawMessage(payload)
	return e, err
}

func canonicalPayload(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid event JSON: %w", core.ErrInvalidState)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("event payload must be an object: %w", core.ErrInvalidState)
	}
	data, err := json.Marshal(object)
	return data, err
}

func (s *Store) Events(ctx context.Context, taskID string, after int64, limit int) ([]core.Event, error) {
	if after < 0 {
		return nil, fmt.Errorf("negative event cursor: %w", core.ErrInvalidState)
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var exists int
	if err = conn.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id = ? AND id NOT IN(SELECT task_id FROM deleted_tasks)", taskID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE task_id = ? AND seq > ? ORDER BY seq LIMIT ?", taskID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []core.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) ClaimNext(ctx context.Context, workerID string) (*core.Job, error) {
	return s.ClaimNextLimited(ctx, workerID, 0)
}

// ClaimNextLimited claims the oldest eligible queued job. Jobs that start an
// automation run are eligible only while fewer than backgroundLimit of them are
// running, so unattended work cannot occupy every worker slot. A limit of zero
// or less disables the cap.
func (s *Store) ClaimNextLimited(ctx context.Context, workerID string, backgroundLimit int) (*core.Job, error) {
	if workerID == "" {
		return nil, fmt.Errorf("worker id required: %w", core.ErrInvalidState)
	}
	var job *core.Job
	err := s.transaction(ctx, func(q querier) error {
		var j core.Job
		var runtimeJSON string
		err := q.QueryRowContext(ctx, `SELECT j.id,j.task_id,j.session_id,j.turn_id,j.generation,j.prompt,t.adapter,t.repository_path,t.base_commit,COALESCE(NULLIF(u.model,''),t.model),COALESCE(NULLIF(u.model_provider,''),t.model_provider),u.reasoning_effort,s.runtime_json,EXISTS(SELECT 1 FROM manager_tasks m WHERE m.task_id=j.task_id) FROM jobs j JOIN tasks t ON t.id=j.task_id JOIN turns u ON u.id=j.turn_id JOIN sessions s ON s.id=j.session_id WHERE j.status = 'queued' AND t.id NOT IN(SELECT task_id FROM deleted_tasks) AND (?<=0 OR NOT EXISTS(SELECT 1 FROM automation_runs b WHERE b.turn_id=j.turn_id) OR (SELECT COUNT(*) FROM jobs r JOIN automation_runs a ON a.turn_id=r.turn_id WHERE r.status='running')<?) ORDER BY j.created_at,j.id LIMIT 1`, backgroundLimit, backgroundLimit).Scan(&j.ID, &j.TaskID, &j.SessionID, &j.TurnID, &j.Generation, &j.Prompt, &j.Adapter, &j.RepositoryPath, &j.BaseCommit, &j.Model, &j.ModelProvider, &j.ReasoningEffort, &runtimeJSON, &j.Manager)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(runtimeJSON), &j.Runtime); err != nil {
			return err
		}
		var status string
		var generation int64
		if err = q.QueryRowContext(ctx, "SELECT status,generation FROM turns WHERE id = ?", j.TurnID).Scan(&status, &generation); err != nil {
			return err
		}
		if status != core.StatusQueued || generation != j.Generation {
			return fmt.Errorf("queued job does not match its turn: %w", core.ErrInvalidState)
		}
		if _, err = q.ExecContext(ctx, "UPDATE jobs SET status = 'running',worker_id = ?,started_at = ? WHERE id = ? AND status = 'queued'", workerID, core.Now(), j.ID); err != nil {
			return err
		}
		if _, err = appendEvent(ctx, q, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.started"}); err != nil {
			return err
		}
		job = &j
		return nil
	})
	return job, err
}

func (s *Store) AppendEvent(ctx context.Context, in core.EventInput) (core.Event, bool, error) {
	var result core.Event
	var replayed bool
	err := s.transaction(ctx, func(q querier) error {
		payload, err := canonicalPayload(in.Payload)
		if err != nil {
			return err
		}
		in.Payload = payload
		if err = taskVisible(ctx, q, in.TaskID); err != nil {
			return err
		}
		if in.ID != "" {
			existing, err := scanEvent(q.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM events WHERE id = ?", in.ID))
			if err == nil {
				if existing.TaskID != in.TaskID || existing.SessionID != in.SessionID || existing.TurnID != in.TurnID || existing.Generation != in.Generation || existing.Type != in.Type || !bytes.Equal(existing.Payload, in.Payload) {
					return fmt.Errorf("event id reused with different content: %w", core.ErrConflict)
				}
				result = existing
				replayed = true
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		switch in.Type {
		case "message.delta", "message.snapshot", "tool.started", "tool.output", "tool.completed", "file.changed", "usage.updated", "context.updated", "workspace.prepared", "agent.session.ready", "agent.turn.started", "agent.warning", "turn.completed", "turn.failed":
		default:
			return fmt.Errorf("unsupported worker event %q: %w", in.Type, core.ErrInvalidState)
		}
		var taskID, sessionID, status string
		var generation int64
		err = q.QueryRowContext(ctx, "SELECT task_id,session_id,status,generation FROM turns WHERE id = ?", in.TurnID).Scan(&taskID, &sessionID, &status, &generation)
		if errors.Is(err, sql.ErrNoRows) {
			return core.ErrNotFound
		}
		if err != nil {
			return err
		}
		if taskID != in.TaskID || sessionID != in.SessionID {
			return fmt.Errorf("event identity does not match turn: %w", core.ErrConflict)
		}
		if in.Generation != generation {
			return core.ErrStaleGeneration
		}
		if status != core.StatusRunning {
			return fmt.Errorf("turn is %s: %w", status, core.ErrInvalidState)
		}
		result, err = appendEvent(ctx, q, in)
		if err != nil {
			return err
		}
		if in.Type == "turn.completed" || in.Type == "turn.failed" {
			terminal := core.StatusCompleted
			if in.Type == "turn.failed" {
				terminal = core.StatusFailed
			}
			_, err = q.ExecContext(ctx, "UPDATE jobs SET status = ?,ended_at = ? WHERE turn_id = ? AND generation = ? AND status = 'running'", terminal, result.CreatedAt, in.TurnID, in.Generation)
			if err == nil {
				_, err = q.ExecContext(ctx, "UPDATE controls SET status='rejected',error='回合已结束，操作未执行' WHERE turn_id=? AND status='pending'", in.TurnID)
			}
		}
		return err
	})
	return result, replayed, err
}

func appendEvent(ctx context.Context, q querier, in core.EventInput) (core.Event, error) {
	payload, err := canonicalPayload(in.Payload)
	if err != nil {
		return core.Event{}, err
	}
	if in.ID == "" {
		in.ID = core.NewID("event")
	}
	e := core.Event{ID: in.ID, SchemaVersion: 1, TaskID: in.TaskID, SessionID: in.SessionID, TurnID: in.TurnID, Generation: in.Generation, Type: in.Type, Payload: payload, CreatedAt: core.Now()}
	if err = q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE task_id = ?", in.TaskID).Scan(&e.Seq); err != nil {
		return core.Event{}, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO events(id,schema_version,task_id,session_id,turn_id,generation,seq,type,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, e.ID, e.SchemaVersion, e.TaskID, e.SessionID, e.TurnID, e.Generation, e.Seq, e.Type, string(e.Payload), e.CreatedAt)
	if err != nil {
		return core.Event{}, err
	}
	if err = projectEvent(ctx, q, e); err != nil {
		return core.Event{}, err
	}
	return e, nil
}

type eventPayload struct {
	Title           string            `json:"title"`
	Prompt          string            `json:"prompt"`
	Adapter         string            `json:"adapter"`
	MessageID       string            `json:"message_id"`
	Text            string            `json:"text"`
	ToolCallID      string            `json:"tool_call_id"`
	Name            string            `json:"name"`
	Input           string            `json:"input"`
	Output          string            `json:"output"`
	Summary         string            `json:"summary"`
	Code            string            `json:"code"`
	Message         string            `json:"message"`
	RepositoryPath  string            `json:"repository_path"`
	BaseCommit      string            `json:"base_commit"`
	Model           string            `json:"model"`
	ModelProvider   string            `json:"model_provider"`
	ReasoningEffort string            `json:"reasoning_effort"`
	NativeTurnID    string            `json:"native_turn_id"`
	ItemID          string            `json:"item_id"`
	Delta           string            `json:"delta"`
	Changes         []core.FileChange `json:"changes"`
	Status          string            `json:"status"`
}

func projectEvent(ctx context.Context, q querier, e core.Event) error {
	var p eventPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("decode event %s: %w", e.ID, err)
	}
	switch e.Type {
	case "task.created":
		if p.Adapter == "" {
			p.Adapter = "codex"
		}
		if _, err := q.ExecContext(ctx, "UPDATE tasks SET title=?,prompt=?,adapter=?,status=?,current_turn_id=?,repository_path=?,base_commit=?,model=?,model_provider=? WHERE id=?", p.Title, p.Prompt, p.Adapter, core.StatusQueued, e.TurnID, p.RepositoryPath, p.BaseCommit, p.Model, p.ModelProvider, e.TaskID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "UPDATE sessions SET runtime_json='{}' WHERE id=?", e.SessionID); err != nil {
			return err
		}
	case "turn.queued":
		if _, err := q.ExecContext(ctx, `INSERT INTO turns(id,task_id,session_id,prompt,status,generation,created_at,created_seq,model,model_provider,reasoning_effort) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, e.TurnID, e.TaskID, e.SessionID, p.Prompt, core.StatusQueued, e.Generation, e.CreatedAt, e.Seq, p.Model, p.ModelProvider, p.ReasoningEffort); err != nil {
			return err
		}
		userSegments, _ := json.Marshal([]core.Segment{{Type: "text", Text: p.Prompt}})
		if _, err := q.ExecContext(ctx, `INSERT INTO messages(id,session_id,turn_id,role,status,segments,created_seq,position) VALUES(?,?,?,?,?,?,?,?)`, "usr_"+e.TurnID, e.SessionID, e.TurnID, "user", core.StatusCompleted, string(userSegments), e.Seq, 0); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO messages(id,session_id,turn_id,role,status,segments,created_seq,position) VALUES(?,?,?,?,?,?,?,?)`, "msg_"+e.TurnID, e.SessionID, e.TurnID, "assistant", core.StatusQueued, "[]", e.Seq, 1); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "UPDATE tasks SET status=?,current_turn_id=? WHERE id=?", core.StatusQueued, e.TurnID, e.TaskID); err != nil {
			return err
		}
	case "turn.started":
		if _, err := q.ExecContext(ctx, "UPDATE turns SET status=?,started_at=? WHERE id=?", core.StatusRunning, e.CreatedAt, e.TurnID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "UPDATE messages SET status='streaming' WHERE id=?", "msg_"+e.TurnID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "UPDATE tasks SET status=? WHERE id=?", core.StatusRunning, e.TaskID); err != nil {
			return err
		}
	case "workspace.prepared", "agent.session.ready":
		if err := projectRuntime(ctx, q, e); err != nil {
			return err
		}
	case "agent.turn.started":
		if p.NativeTurnID == "" {
			return fmt.Errorf("native turn identity required: %w", core.ErrInvalidState)
		}
		var current string
		if err := q.QueryRowContext(ctx, "SELECT native_turn_id FROM turns WHERE id=?", e.TurnID).Scan(&current); err != nil {
			return err
		}
		if current != "" && current != p.NativeTurnID {
			return fmt.Errorf("native turn identity changed: %w", core.ErrConflict)
		}
		if _, err := q.ExecContext(ctx, "UPDATE turns SET native_turn_id=? WHERE id=?", p.NativeTurnID, e.TurnID); err != nil {
			return err
		}
	case "context.updated":
		var usage core.ContextUsage
		if err := json.Unmarshal(e.Payload, &usage); err != nil {
			return err
		}
		if usage.UsedTokens < 0 || usage.WindowTokens < 0 || usage.Source == "" {
			return core.ErrInvalidState
		}
		var raw string
		if err := q.QueryRowContext(ctx, "SELECT runtime_json FROM sessions WHERE id=?", e.SessionID).Scan(&raw); err != nil {
			return err
		}
		var runtime core.RuntimeInfo
		if err := json.Unmarshal([]byte(raw), &runtime); err != nil {
			return err
		}
		usage.ObservedAt = e.CreatedAt
		runtime.ContextUsage = &usage
		encoded, _ := json.Marshal(runtime)
		if _, err := q.ExecContext(ctx, "UPDATE sessions SET runtime_json=? WHERE id=?", string(encoded), e.SessionID); err != nil {
			return err
		}
	case "usage.updated":
		var usage core.TokenUsage
		if err := json.Unmarshal(e.Payload, &usage); err != nil {
			return err
		}
		if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CachedInputTokens < 0 || usage.ReasoningOutputTokens < 0 || usage.TotalTokens < 0 {
			return fmt.Errorf("negative token usage: %w", core.ErrInvalidState)
		}
		raw, _ := json.Marshal(usage)
		if _, err := q.ExecContext(ctx, "UPDATE turns SET usage_json=? WHERE id=?", string(raw), e.TurnID); err != nil {
			return err
		}
	case "agent.warning":
		// A diagnostic fact is not a terminal execution result.
	case "message.delta", "message.snapshot", "tool.started", "tool.output", "tool.completed", "file.changed":
		if p.MessageID != "msg_"+e.TurnID {
			return fmt.Errorf("unexpected assistant message id: %w", core.ErrConflict)
		}
		segments, err := readSegments(ctx, q, p.MessageID)
		if err != nil {
			return err
		}
		switch e.Type {
		case "message.delta", "message.snapshot":
			index := -1
			if p.ItemID != "" {
				for i, segment := range segments {
					if segment.Type == "text" && segment.ItemID == p.ItemID {
						index = i
						break
					}
				}
			}
			if index >= 0 {
				if e.Type == "message.snapshot" {
					segments[index].Text = p.Text
				} else {
					segments[index].Text += p.Text
				}
			} else if p.ItemID == "" && e.Type == "message.delta" && len(segments) > 0 && segments[len(segments)-1].Type == "text" && segments[len(segments)-1].ItemID == "" {
				segments[len(segments)-1].Text += p.Text
			} else {
				segments = append(segments, core.Segment{Type: "text", Text: p.Text, ItemID: p.ItemID})
			}
		case "tool.started":
			if p.ToolCallID == "" || p.Name == "" {
				return fmt.Errorf("tool call identity required: %w", core.ErrInvalidState)
			}
			for _, segment := range segments {
				if segment.ToolCallID == p.ToolCallID {
					return fmt.Errorf("tool call already exists: %w", core.ErrConflict)
				}
			}
			segments = append(segments, core.Segment{Type: "tool_call", Status: core.StatusRunning, ToolCallID: p.ToolCallID, Name: p.Name, Input: p.Input})
		case "tool.output", "tool.completed":
			found := false
			resultIndex := -1
			for i, segment := range segments {
				if segment.ToolCallID == p.ToolCallID && segment.Type == "tool_call" {
					found = true
					if p.Name == "" {
						p.Name = segment.Name
					} else if p.Name != segment.Name {
						return fmt.Errorf("tool name mismatch: %w", core.ErrConflict)
					}
				}
				if segment.ToolCallID == p.ToolCallID && segment.Type == "tool_result" {
					resultIndex = i
				}
			}
			if !found {
				return fmt.Errorf("tool call not started: %w", core.ErrInvalidState)
			}
			if resultIndex < 0 {
				segments = append(segments, core.Segment{Type: "tool_result", ToolCallID: p.ToolCallID, Name: p.Name})
				resultIndex = len(segments) - 1
			}
			if e.Type == "tool.output" {
				segments[resultIndex].Output = core.OutputTail(segments[resultIndex].Output + p.Delta)
				segments[resultIndex].Status = core.StatusRunning
			} else {
				segments[resultIndex].Output = core.OutputTail(p.Output)
				status := core.StatusCompleted
				if p.Status == "failed" || p.Status == "declined" {
					status = core.StatusFailed
				}
				for i := range segments {
					if segments[i].ToolCallID == p.ToolCallID && (segments[i].Type == "tool_call" || segments[i].Type == "tool_result") {
						segments[i].Status = status
					}
				}
			}
		case "file.changed":
			if p.ItemID == "" {
				return fmt.Errorf("file change item identity required: %w", core.ErrInvalidState)
			}
			index := -1
			for i, segment := range segments {
				if segment.Type == "file_change" && segment.ItemID == p.ItemID {
					index = i
					break
				}
			}
			segment := core.Segment{Type: "file_change", ItemID: p.ItemID, Changes: p.Changes}
			if index < 0 {
				segments = append(segments, segment)
			} else {
				segments[index] = segment
			}
		}
		if err = writeSegments(ctx, q, p.MessageID, segments); err != nil {
			return err
		}
	case "turn.completed", "turn.failed":
		status := core.StatusCompleted
		if e.Type == "turn.failed" {
			status = core.StatusFailed
			segments, err := readSegments(ctx, q, "msg_"+e.TurnID)
			if err != nil {
				return err
			}
			for i := range segments {
				if (segments[i].Type == "tool_call" || segments[i].Type == "tool_result") && segments[i].Status == core.StatusRunning {
					segments[i].Status = core.StatusFailed
				}
			}
			if err = writeSegments(ctx, q, "msg_"+e.TurnID, segments); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, "UPDATE turns SET status=?,ended_at=?,error_code=?,error=? WHERE id=?", status, e.CreatedAt, p.Code, p.Message, e.TurnID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "UPDATE messages SET status=? WHERE id=?", status, "msg_"+e.TurnID); err != nil {
			return err
		}
		if p.Summary != "" && status == core.StatusCompleted {
			segments, err := readSegments(ctx, q, "msg_"+e.TurnID)
			if err != nil {
				return err
			}
			if len(segments) == 0 {
				if err = writeSegments(ctx, q, "msg_"+e.TurnID, []core.Segment{{Type: "text", Text: p.Summary}}); err != nil {
					return err
				}
			}
		}
		if _, err := q.ExecContext(ctx, "UPDATE tasks SET status=? WHERE id=?", status, e.TaskID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported event %q: %w", e.Type, core.ErrInvalidState)
	}
	_, err := q.ExecContext(ctx, "UPDATE tasks SET updated_at=?,last_event_seq=? WHERE id=?", e.CreatedAt, e.Seq, e.TaskID)
	return err
}

func readSegments(ctx context.Context, q querier, messageID string) ([]core.Segment, error) {
	var raw string
	if err := q.QueryRowContext(ctx, "SELECT segments FROM messages WHERE id=?", messageID).Scan(&raw); err != nil {
		return nil, err
	}
	var segments []core.Segment
	err := json.Unmarshal([]byte(raw), &segments)
	return segments, err
}

func writeSegments(ctx context.Context, q querier, messageID string, segments []core.Segment) error {
	raw, err := json.Marshal(segments)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE messages SET segments=? WHERE id=?", string(raw), messageID)
	return err
}

func (s *Store) Replay(ctx context.Context, taskID string) error {
	return s.transaction(ctx, func(q querier) error {
		var exists int
		if err := q.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=? AND id NOT IN(SELECT task_id FROM deleted_tasks)", taskID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return core.ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, "SELECT "+eventColumns+" FROM events WHERE task_id=? ORDER BY seq", taskID)
		if err != nil {
			return err
		}
		events := []core.Event{}
		for rows.Next() {
			event, err := scanEvent(rows)
			if err != nil {
				rows.Close()
				return err
			}
			events = append(events, event)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, "DELETE FROM messages WHERE session_id=(SELECT session_id FROM tasks WHERE id=?)", taskID); err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, "DELETE FROM turns WHERE task_id=?", taskID); err != nil {
			return err
		}
		for _, event := range events {
			if err = projectEvent(ctx, q, event); err != nil {
				return err
			}
		}
		return nil
	})
}
