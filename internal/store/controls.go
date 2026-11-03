package store

import (
	"blux/internal/core"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type Control struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	TurnID     string `json:"turn_id"`
	Generation int64  `json:"generation"`
	Kind       string `json:"kind"`
	Prompt     string `json:"prompt"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

func scanControl(r scanner) (c Control, err error) {
	err = r.Scan(&c.ID, &c.TaskID, &c.TurnID, &c.Generation, &c.Kind, &c.Prompt, &c.Status, &c.Error)
	return
}

const controlColumns = "id,task_id,turn_id,generation,kind,prompt,status,error"

func (s *Store) RequestControl(ctx context.Context, taskID, turnID, key, kind, prompt string) (c Control, err error) {
	if key == "" || (kind != "stop" && kind != "steer") || kind == "steer" && prompt == "" {
		return c, core.ErrInvalidState
	}
	err = s.transaction(ctx, func(q querier) error {
		if err := taskVisible(ctx, q, taskID); err != nil {
			return err
		}
		c, e := scanControl(q.QueryRowContext(ctx, "SELECT "+controlColumns+" FROM controls WHERE id=?", taskID+":"+key))
		if e == nil {
			if c.TurnID != turnID || c.Kind != kind || c.Prompt != prompt {
				return core.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var status, adapter, session string
		var generation int64
		e = q.QueryRowContext(ctx, "SELECT u.status,u.generation,u.session_id,t.adapter FROM turns u JOIN tasks t ON t.id=u.task_id WHERE u.id=? AND u.task_id=?", turnID, taskID).Scan(&status, &generation, &session, &adapter)
		if errors.Is(e, sql.ErrNoRows) {
			return core.ErrNotFound
		}
		if e != nil {
			return e
		}
		if status != "queued" && status != "running" {
			return core.ErrConflict
		}
		if kind == "steer" && (status != "running" || adapter != "codex") {
			return fmt.Errorf("steer unavailable: %w", core.ErrInvalidState)
		}
		c = Control{ID: taskID + ":" + key, TaskID: taskID, TurnID: turnID, Generation: generation, Kind: kind, Prompt: prompt, Status: "pending"}
		if kind == "stop" && status == "queued" {
			p, _ := json.Marshal(map[string]string{"code": "user_stopped", "message": "你已停止排队中的任务。"})
			if _, e = appendEvent(ctx, q, core.EventInput{TaskID: taskID, SessionID: session, TurnID: turnID, Generation: generation, Type: "turn.failed", Payload: p}); e != nil {
				return e
			}
			if _, e = q.ExecContext(ctx, "UPDATE jobs SET status='failed',ended_at=? WHERE turn_id=?", core.Now(), turnID); e != nil {
				return e
			}
			c.Status = "applied"
		}
		_, e = q.ExecContext(ctx, "INSERT INTO controls(id,task_id,turn_id,generation,kind,prompt,status,created_at) VALUES(?,?,?,?,?,?,?,?)", c.ID, c.TaskID, c.TurnID, c.Generation, c.Kind, c.Prompt, c.Status, core.Now())
		return e
	})
	if err == nil {
		c, err = s.GetControl(ctx, taskID, key)
	}
	return
}
func (s *Store) GetControl(ctx context.Context, taskID, key string) (Control, error) {
	if err := taskVisible(ctx, s.db, taskID); err != nil {
		return Control{}, err
	}
	return scanControl(s.db.QueryRowContext(ctx, "SELECT "+controlColumns+" FROM controls WHERE id=?", taskID+":"+key))
}
func (s *Store) ClaimControl(ctx context.Context, turnID string) (c *Control, err error) {
	err = s.transaction(ctx, func(q querier) error {
		v, e := scanControl(q.QueryRowContext(ctx, "SELECT "+controlColumns+" FROM controls WHERE turn_id=? AND status='pending' ORDER BY created_at,id LIMIT 1", turnID))
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if _, e = q.ExecContext(ctx, "UPDATE controls SET status='claimed' WHERE id=?", v.ID); e != nil {
			return e
		}
		c = &v
		return nil
	})
	return
}
func (s *Store) FinishControl(ctx context.Context, id string, cause error) error {
	status, msg := "applied", ""
	if cause != nil {
		status, msg = "rejected", cause.Error()
	}
	_, err := s.db.ExecContext(ctx, "UPDATE controls SET status=?,error=? WHERE id=? AND status='claimed'", status, msg, id)
	return err
}

// RecoverRunning never re-dispatches a turn whose external effects are unknown.
// A new user-authored turn can resume its persisted native thread and worktree.
func (s *Store) RecoverRunning(ctx context.Context) error {
	return s.transaction(ctx, func(q querier) error {
		rows, err := q.QueryContext(ctx, "SELECT task_id,session_id,turn_id,generation FROM jobs WHERE status='running'")
		if err != nil {
			return err
		}
		var jobs []core.Job
		for rows.Next() {
			var j core.Job
			if err = rows.Scan(&j.TaskID, &j.SessionID, &j.TurnID, &j.Generation); err != nil {
				rows.Close()
				return err
			}
			jobs = append(jobs, j)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, j := range jobs {
			p := json.RawMessage(`{"code":"recovery_required","message":"上次 Worker 意外退出。已保留会话与文件，请检查结果后提交新回合继续；不会自动重复执行。"}`)
			if _, err = appendEvent(ctx, q, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.failed", Payload: p}); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(ctx, "UPDATE jobs SET status='failed',ended_at=? WHERE status='running'", core.Now())
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, "UPDATE controls SET status='rejected',error='执行已结束或 Worker 中断；控制操作未重放' WHERE status IN ('pending','claimed') AND turn_id NOT IN (SELECT turn_id FROM jobs WHERE status='queued')")
		return err
	})
}
