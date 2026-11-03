package store

import (
	"blux/internal/core"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

type Collaboration struct {
	PinnedAt       string      `json:"pinned_at,omitempty"`
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Goal           string      `json:"goal"`
	RepositoryPath string      `json:"repository_path"`
	ContextIDs     []string    `json:"context_ids"`
	CreatedAt      string      `json:"created_at"`
	Tasks          []core.Task `json:"tasks"`
	Status         string      `json:"status"`
	ArtifactCount  int         `json:"artifact_count"`
	UpdatedAt      string      `json:"updated_at"`
}
type CreateCollaborationInput struct {
	Title          string   `json:"title"`
	Goal           string   `json:"goal"`
	RepositoryPath string   `json:"repository_path"`
	ContextIDs     []string `json:"context_ids"`
}
type SharedSnapshot struct {
	Snapshot
	TaskTitle string `json:"task_title"`
	Adapter   string `json:"adapter"`
}

func (s *Store) CreateCollaboration(ctx context.Context, in CreateCollaborationInput, key string) (v Collaboration, replayed bool, err error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Goal = strings.TrimSpace(in.Goal)
	if in.Title == "" {
		in.Title = string([]rune(in.Goal)[:min(120, utf8.RuneCountInString(in.Goal))])
	}
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 120 || utf8.RuneCountInString(in.Goal) > 16000 || key == "" {
		return v, false, core.ErrInvalidState
	}
	h := digest(in)
	err = s.transaction(ctx, func(q querier) error {
		var old, id string
		e := q.QueryRowContext(ctx, "SELECT digest,entity_id FROM collaboration_receipts WHERE scope='create' AND key=?", key).Scan(&old, &id)
		if e == nil {
			if old != h {
				return core.ErrConflict
			}
			v.ID = id
			replayed = true
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		ids, _ := json.Marshal(in.ContextIDs)
		v.ID = core.NewID("collaboration")
		if _, e = q.ExecContext(ctx, "INSERT INTO collaborations(id,title,goal,repository_path,context_ids_json,created_at) VALUES(?,?,?,?,?,?)", v.ID, in.Title, in.Goal, in.RepositoryPath, string(ids), core.Now()); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, "INSERT INTO collaboration_receipts(scope,key,digest,entity_id) VALUES('create',?,?,?)", key, h, v.ID)
		return e
	})
	if err == nil {
		v, err = s.Collaboration(ctx, v.ID)
	}
	return
}
func (s *Store) Collaboration(ctx context.Context, id string) (v Collaboration, err error) {
	if err = collaborationVisible(ctx, s.db, id); err != nil {
		return
	}
	var raw string
	err = s.db.QueryRowContext(ctx, "SELECT id,title,goal,repository_path,context_ids_json,created_at,COALESCE((SELECT pinned_at FROM pins WHERE kind='collaboration' AND entity_id=collaborations.id),'') FROM collaborations WHERE id=?", id).Scan(&v.ID, &v.Title, &v.Goal, &v.RepositoryPath, &raw, &v.CreatedAt, &v.PinnedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	if err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &v.ContextIDs); err != nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id NOT IN(SELECT task_id FROM deleted_tasks) AND id IN(SELECT task_id FROM collaboration_tasks WHERE collaboration_id=?) ORDER BY created_at,id", id)
	if err != nil {
		return
	}
	v.Tasks = []core.Task{}
	v.UpdatedAt = v.CreatedAt
	active, failed := false, false
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return v, e
		}
		v.Tasks = append(v.Tasks, t)
		active = active || t.Status == "running" || t.Status == "queued"
		failed = failed || t.Status == "failed"
		if t.UpdatedAt > v.UpdatedAt {
			v.UpdatedAt = t.UpdatedAt
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	v.Status = "pending"
	if len(v.Tasks) > 0 {
		v.Status = "completed"
	}
	if failed {
		v.Status = "attention"
	}
	if active {
		v.Status = "running"
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifact_versions a JOIN turns u ON u.id=a.turn_id WHERE u.status='completed' AND a.task_id NOT IN(SELECT task_id FROM deleted_tasks) AND a.task_id IN(SELECT task_id FROM collaboration_tasks WHERE collaboration_id=?)`, id).Scan(&v.ArtifactCount)
	return
}
func (s *Store) Collaborations(ctx context.Context) ([]Collaboration, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM collaborations WHERE id NOT IN(SELECT collaboration_id FROM deleted_collaborations) ORDER BY COALESCE((SELECT pinned_at FROM pins WHERE kind='collaboration' AND entity_id=collaborations.id),'') DESC,created_at DESC")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []Collaboration{}
	for _, id := range ids {
		v, e := s.Collaboration(ctx, id)
		if errors.Is(e, core.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) SharedSnapshots(ctx context.Context, id string) ([]SharedSnapshot, error) {
	group, err := s.Collaboration(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []SharedSnapshot{}
	for _, task := range group.Tasks {
		versions, e := s.Snapshots(ctx, task.ID)
		if errors.Is(e, core.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		for _, v := range versions {
			var status string
			if e = s.db.QueryRowContext(ctx, "SELECT status FROM turns WHERE id=?", v.TurnID).Scan(&status); e != nil {
				return nil, e
			}
			if status == "completed" {
				out = append(out, SharedSnapshot{Snapshot: v, TaskTitle: task.Title, Adapter: task.Adapter})
			}
		}
	}
	return out, nil
}

func (s *Store) TaskCollaboration(ctx context.Context, taskID string) (id string, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT collaboration_id FROM collaboration_tasks WHERE task_id=?", taskID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}
