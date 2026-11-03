package store

import (
	"blux/internal/contextpack"
	"blux/internal/core"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type ContextEntry struct {
	ID          string `json:"id"`
	ProjectPath string `json:"project_path"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	PreviousID  string `json:"previous_id"`
	CreatedAt   string `json:"created_at"`
}
type ContextSnapshot struct {
	Mode     contextpack.Mode `json:"mode"`
	Entries  []ContextEntry   `json:"entries"`
	FrozenAt string           `json:"frozen_at"`
}

const entryColumns = "id,project_path,kind,title,content,previous_id,created_at"

func scanEntry(row scanner) (e ContextEntry, err error) {
	err = row.Scan(&e.ID, &e.ProjectPath, &e.Kind, &e.Title, &e.Content, &e.PreviousID, &e.CreatedAt)
	return
}
func (s *Store) SaveContext(ctx context.Context, e ContextEntry) (ContextEntry, error) {
	e.Title = strings.TrimSpace(e.Title)
	if e.ProjectPath == "" || e.Title == "" || len(e.Content) > 1<<20 || (e.Kind != "reference" && e.Kind != "memory" && e.Kind != "skill") {
		return e, core.ErrInvalidState
	}
	e.ID = core.NewID("context")
	e.CreatedAt = core.Now()
	err := s.transaction(ctx, func(q querier) error {
		if e.PreviousID != "" {
			r, err := q.ExecContext(ctx, "UPDATE context_entries SET current=0 WHERE id=? AND project_path=? AND kind=? AND current=1", e.PreviousID, e.ProjectPath, e.Kind)
			if err != nil {
				return err
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return core.ErrConflict
			}
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO context_entries(id,project_path,kind,title,content,previous_id,created_at) VALUES(?,?,?,?,?,?,?)", e.ID, e.ProjectPath, e.Kind, e.Title, e.Content, e.PreviousID, e.CreatedAt); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO context_fts(entry_id,title,content) VALUES(?,?,?)", e.ID, e.Title, e.Content); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, "INSERT OR IGNORE INTO projects(id,path,name,created_at) VALUES(?,?,?,?)", core.NewID("project"), e.ProjectPath, filepath.Base(e.ProjectPath), core.Now())
		return err
	})
	return e, err
}
func (s *Store) ContextEntries(ctx context.Context, path, search string) ([]ContextEntry, error) {
	query := "SELECT " + entryColumns + " FROM context_entries WHERE project_path=? AND current=1"
	args := []any{path}
	if search != "" {
		query += " AND id IN (SELECT entry_id FROM context_fts WHERE context_fts MATCH ?)"
		args = append(args, `"`+strings.ReplaceAll(search, `"`, `""`)+`"`)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY created_at", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContextEntry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func freezeContext(ctx context.Context, q querier, taskID string, in core.CreateTaskInput) error {
	m, err := contextpack.Get(in.Mode)
	if err != nil {
		return core.ErrInvalidState
	}
	snapshot := ContextSnapshot{Mode: m, Entries: []ContextEntry{}, FrozenAt: core.Now()}
	total := 0
	seen := map[string]bool{}
	for _, id := range in.ContextIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		e, err := scanEntry(q.QueryRowContext(ctx, "SELECT "+entryColumns+" FROM context_entries WHERE id=? AND project_path=?", id, in.RepositoryPath))
		if errors.Is(err, sql.ErrNoRows) {
			return core.ErrNotFound
		}
		if err != nil {
			return err
		}
		total += len(e.Content)
		if total > 2<<20 {
			return core.ErrInvalidState
		}
		snapshot.Entries = append(snapshot.Entries, e)
	}
	b, _ := json.Marshal(snapshot)
	_, err = q.ExecContext(ctx, "INSERT INTO task_context(task_id,mode,snapshot_json) VALUES(?,?,?)", taskID, m.ID, string(b))
	return err
}
func (s *Store) TaskContext(ctx context.Context, taskID string) (v ContextSnapshot, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx, "SELECT snapshot_json FROM task_context WHERE task_id=?", taskID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		v.Mode, _ = contextpack.Get("")
		v.Entries = []ContextEntry{}
		return v, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return
}

func freezeTurnContext(ctx context.Context, q querier, taskID, turnID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 100 {
		return core.ErrInvalidState
	}
	var project string
	if err := q.QueryRowContext(ctx, "SELECT repository_path FROM tasks WHERE id=?", taskID).Scan(&project); err != nil {
		return err
	}
	selected := []ContextEntry{}
	seen := map[string]bool{}
	hasSkill := false
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		e, err := scanEntry(q.QueryRowContext(ctx, "SELECT "+entryColumns+" FROM context_entries WHERE id=? AND project_path=?", id, project))
		if errors.Is(err, sql.ErrNoRows) {
			return core.ErrNotFound
		}
		if err != nil {
			return err
		}
		selected = append(selected, e)
		hasSkill = hasSkill || e.Kind == "skill"
	}
	var raw string
	var snapshot ContextSnapshot
	err := q.QueryRowContext(ctx, "SELECT snapshot_json FROM task_context WHERE task_id=?", taskID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return err
		}
	} else {
		snapshot.Mode, _ = contextpack.Get("")
	}
	entries := []ContextEntry{}
	total := 0
	for _, e := range snapshot.Entries {
		if seen[e.ID] || hasSkill && e.Kind == "skill" {
			continue
		}
		entries = append(entries, e)
		total += len(e.Content)
	}
	for _, e := range selected {
		entries = append(entries, e)
		total += len(e.Content)
	}
	if total > 2<<20 {
		return core.ErrInvalidState
	}
	snapshot.Entries = entries
	snapshot.FrozenAt = core.Now()
	b, _ := json.Marshal(snapshot)
	_, err = q.ExecContext(ctx, "INSERT INTO turn_context(turn_id,task_id,snapshot_json) VALUES(?,?,?)", turnID, taskID, string(b))
	return err
}

// ExecutionContext preserves the task's original snapshot and freezes explicit
// selections independently for each continuation. Replay never rewrites inputs.
func (s *Store) ExecutionContext(ctx context.Context, taskID, turnID string) (v ContextSnapshot, err error) {
	var exists int
	if err = s.db.QueryRowContext(ctx, "SELECT 1 FROM turns WHERE task_id=? AND id=?", taskID, turnID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = core.ErrNotFound
		}
		return
	}
	var raw string
	err = s.db.QueryRowContext(ctx, "SELECT snapshot_json FROM turn_context WHERE task_id=? AND turn_id=?", taskID, turnID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s.TaskContext(ctx, taskID)
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &v)
	}
	return
}
