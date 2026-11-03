package store

import (
	"blux/internal/core"
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) IsManager(ctx context.Context, id string) (bool, error) {
	var n int
	e := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM manager_tasks WHERE task_id=?", id).Scan(&n)
	return n > 0, e
}
func (s *Store) ManagerTask(ctx context.Context, project string) (string, error) {
	var id string
	e := s.db.QueryRowContext(ctx, "SELECT m.task_id FROM manager_tasks m JOIN tasks t ON t.id=m.task_id WHERE m.project_path=? AND t.id NOT IN(SELECT task_id FROM deleted_tasks) ORDER BY t.created_at DESC LIMIT 1", project).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return id, e
}
func (s *Store) RequestReview(ctx context.Context, delivery string, generation int64, phase, message string) error {
	d, e := s.Delivery(ctx, delivery)
	if e != nil {
		return e
	}
	if d.Run.Generation != generation {
		return core.ErrStaleGeneration
	}
	ready := false
	for _, p := range d.Run.Phases {
		if p.ID == phase && p.Status == "awaiting_review" {
			ready = true
		}
	}
	if !ready {
		return core.ErrInvalidState
	}
	_, e = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO review_requests(id,delivery_id,generation,phase_id,message,created_at) VALUES(?,?,?,?,?,?)", core.NewID("review"), delivery, generation, phase, message, core.Now())
	return e
}
func (s *Store) ExportDatabase(ctx context.Context, path string) error {
	_, e := s.db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(path, "'", "''")+"'")
	return e
}
