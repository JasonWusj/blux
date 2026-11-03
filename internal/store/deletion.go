package store

import (
	"blux/internal/core"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type Deletion struct {
	TaskIDs     []string `json:"task_ids"`
	PendingStop bool     `json:"pending_stop"`
}

func taskVisible(ctx context.Context, q querier, id string) error {
	var found int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=? AND id NOT IN(SELECT task_id FROM deleted_tasks)", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	}
	return err
}
func collaborationVisible(ctx context.Context, q querier, id string) error {
	var found int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM collaborations WHERE id=? AND id NOT IN(SELECT collaboration_id FROM deleted_collaborations)", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	}
	return err
}
func (s *Store) IsTaskDeleted(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deleted_tasks WHERE task_id=?", id).Scan(&n)
	return n > 0, err
}

// Tombstones are independent of projections, so replay and late execution
// reports cannot restore a task. Previously pinned input versions are retained.
func deleteTask(ctx context.Context, q querier, id string) (bool, error) {
	var found int
	if err := q.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=?", id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, core.ErrNotFound
		}
		return false, err
	}
	r, err := q.ExecContext(ctx, "INSERT OR IGNORE INTO deleted_tasks(task_id,deleted_at) VALUES(?,?)", id, core.Now())
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return false, nil
	}
	if _, err = q.ExecContext(ctx, "DELETE FROM pins WHERE kind='task' AND entity_id=?", id); err != nil {
		return false, err
	}
	var session, turn, status string
	var generation int64
	err = q.QueryRowContext(ctx, "SELECT u.session_id,u.id,u.status,u.generation FROM turns u JOIN tasks t ON t.current_turn_id=u.id WHERE t.id=?", id).Scan(&session, &turn, &status, &generation)
	if err != nil {
		return false, err
	}
	if status == core.StatusRunning || status == core.StatusQueued {
		p, _ := json.Marshal(map[string]string{"code": "user_deleted", "message": "任务已删除，执行已取消。"})
		if _, err = appendEvent(ctx, q, core.EventInput{TaskID: id, SessionID: session, TurnID: turn, Generation: generation, Type: "turn.failed", Payload: p}); err != nil {
			return false, err
		}
	}
	if _, err = q.ExecContext(ctx, "UPDATE jobs SET status='failed',ended_at=? WHERE task_id=? AND status IN ('queued','running')", core.Now(), id); err != nil {
		return false, err
	}
	_, err = q.ExecContext(ctx, "UPDATE controls SET status='rejected',error='任务已删除' WHERE task_id=? AND status IN ('pending','claimed')", id)
	return status == core.StatusRunning, err
}
func (s *Store) DeleteTask(ctx context.Context, id string) (result Deletion, err error) {
	result.TaskIDs = []string{id}
	err = s.transaction(ctx, func(q querier) error { var e error; result.PendingStop, e = deleteTask(ctx, q, id); return e })
	return
}
func (s *Store) DeleteCollaboration(ctx context.Context, id string) (result Deletion, err error) {
	result.TaskIDs = []string{}
	err = s.transaction(ctx, func(q querier) error {
		var found int
		if e := q.QueryRowContext(ctx, "SELECT 1 FROM collaborations WHERE id=?", id).Scan(&found); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return core.ErrNotFound
			}
			return e
		}
		if _, e := q.ExecContext(ctx, "INSERT OR IGNORE INTO deleted_collaborations(collaboration_id,deleted_at) VALUES(?,?)", id, core.Now()); e != nil {
			return e
		}
		if _, e := q.ExecContext(ctx, "DELETE FROM pins WHERE kind='collaboration' AND entity_id=?", id); e != nil {
			return e
		}
		rows, e := q.QueryContext(ctx, "SELECT task_id FROM collaboration_tasks WHERE collaboration_id=?", id)
		if e != nil {
			return e
		}
		for rows.Next() {
			var task string
			if e = rows.Scan(&task); e != nil {
				rows.Close()
				return e
			}
			result.TaskIDs = append(result.TaskIDs, task)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, task := range result.TaskIDs {
			pending, e := deleteTask(ctx, q, task)
			if e != nil {
				return e
			}
			result.PendingStop = result.PendingStop || pending
		}
		return nil
	})
	return
}
