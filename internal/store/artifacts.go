package store

import (
	"blux/internal/core"
	"context"
	"database/sql"
	"errors"
)

type Snapshot struct {
	ID         string            `json:"id"`
	TaskID     string            `json:"task_id"`
	TurnID     string            `json:"turn_id"`
	Generation int64             `json:"generation"`
	Commit     string            `json:"commit"`
	Diff       string            `json:"diff"`
	CreatedAt  string            `json:"created_at"`
	Artifacts  []ArtifactVersion `json:"artifacts"`
}
type ArtifactVersion struct {
	ID         string `json:"id"`
	SnapshotID string `json:"snapshot_id"`
	TaskID     string `json:"task_id"`
	TurnID     string `json:"turn_id"`
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Size       int64  `json:"size"`
	MIME       string `json:"mime"`
	CreatedAt  string `json:"created_at"`
}
type Acceptance struct {
	ID              string `json:"id"`
	SnapshotID      string `json:"snapshot_id"`
	TaskID          string `json:"task_id"`
	Status          string `json:"status"`
	SourceHead      string `json:"source_head"`
	ResultCommit    string `json:"result_commit"`
	IntegrationPath string `json:"integration_path"`
	Reason          string `json:"reason"`
	UpdatedAt       string `json:"updated_at"`
}

func (s *Store) SaveSnapshot(ctx context.Context, v Snapshot) error {
	return s.transaction(ctx, func(q querier) error {
		if err := taskVisible(ctx, q, v.TaskID); err != nil {
			return err
		}
		var status string
		var gen int64
		if err := q.QueryRowContext(ctx, "SELECT status,generation FROM turns WHERE id=? AND task_id=?", v.TurnID, v.TaskID).Scan(&status, &gen); err != nil {
			return err
		}
		if gen != v.Generation || status != "running" {
			return core.ErrStaleGeneration
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO snapshots(id,task_id,turn_id,generation,commit_id,diff,created_at) VALUES(?,?,?,?,?,?,?)", v.ID, v.TaskID, v.TurnID, v.Generation, v.Commit, v.Diff, v.CreatedAt); err != nil {
			return err
		}
		for _, a := range v.Artifacts {
			if _, err := q.ExecContext(ctx, "INSERT INTO artifact_versions(id,snapshot_id,task_id,turn_id,path,hash,size,mime,created_at) VALUES(?,?,?,?,?,?,?,?,?)", a.ID, v.ID, v.TaskID, v.TurnID, a.Path, a.Hash, a.Size, a.MIME, v.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddSnapshotArtifacts indexes additional representations of a completed,
// immutable commit. Existing paths can never be replaced with different bytes.
func (s *Store) AddSnapshotArtifacts(ctx context.Context, v Snapshot, files []ArtifactVersion) (added int, err error) {
	err = s.transaction(ctx, func(q querier) error {
		if err := taskVisible(ctx, q, v.TaskID); err != nil {
			return err
		}
		var status, commit, created string
		var generation int64
		if err := q.QueryRowContext(ctx, `SELECT t.status,s.commit_id,s.generation,s.created_at FROM snapshots s JOIN turns t ON t.id=s.turn_id WHERE s.id=? AND s.task_id=? AND s.turn_id=?`, v.ID, v.TaskID, v.TurnID).Scan(&status, &commit, &generation, &created); err != nil {
			return err
		}
		if status != "completed" || commit != v.Commit || generation != v.Generation {
			return core.ErrStaleGeneration
		}
		for _, a := range files {
			var hash string
			var size int64
			e := q.QueryRowContext(ctx, "SELECT hash,size FROM artifact_versions WHERE snapshot_id=? AND path=?", v.ID, a.Path).Scan(&hash, &size)
			if e == nil {
				if hash != a.Hash || size != a.Size {
					return core.ErrConflict
				}
				continue
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if _, e = q.ExecContext(ctx, "INSERT INTO artifact_versions(id,snapshot_id,task_id,turn_id,path,hash,size,mime,created_at) VALUES(?,?,?,?,?,?,?,?,?)", a.ID, v.ID, v.TaskID, v.TurnID, a.Path, a.Hash, a.Size, a.MIME, created); e != nil {
				return e
			}
			added++
		}
		return nil
	})
	if err != nil {
		added = 0
	}
	return
}
func (s *Store) Snapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	if err := taskVisible(ctx, s.db, taskID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,task_id,turn_id,generation,commit_id,diff,created_at FROM snapshots WHERE task_id=? ORDER BY created_at", taskID)
	if err != nil {
		return nil, err
	}
	var out = []Snapshot{}
	for rows.Next() {
		var v Snapshot
		if err = rows.Scan(&v.ID, &v.TaskID, &v.TurnID, &v.Generation, &v.Commit, &v.Diff, &v.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Artifacts, err = s.Artifacts(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) Snapshot(ctx context.Context, id string) (Snapshot, error) {
	var v Snapshot
	err := s.db.QueryRowContext(ctx, "SELECT id,task_id,turn_id,generation,commit_id,diff,created_at FROM snapshots WHERE id=?", id).Scan(&v.ID, &v.TaskID, &v.TurnID, &v.Generation, &v.Commit, &v.Diff, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, core.ErrNotFound
	}
	if err == nil {
		if err = taskVisible(ctx, s.db, v.TaskID); err != nil {
			return v, err
		}
		v.Artifacts, err = s.Artifacts(ctx, id)
	}
	return v, err
}
func (s *Store) Artifacts(ctx context.Context, id string) ([]ArtifactVersion, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,snapshot_id,task_id,turn_id,path,hash,size,mime,created_at FROM artifact_versions WHERE snapshot_id=? ORDER BY path", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArtifactVersion{}
	for rows.Next() {
		var a ArtifactVersion
		if err = rows.Scan(&a.ID, &a.SnapshotID, &a.TaskID, &a.TurnID, &a.Path, &a.Hash, &a.Size, &a.MIME, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Artifact(ctx context.Context, id string) (a ArtifactVersion, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT id,snapshot_id,task_id,turn_id,path,hash,size,mime,created_at FROM artifact_versions WHERE id=?", id).Scan(&a.ID, &a.SnapshotID, &a.TaskID, &a.TurnID, &a.Path, &a.Hash, &a.Size, &a.MIME, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	if err == nil {
		err = taskVisible(ctx, s.db, a.TaskID)
	}
	return
}
func (s *Store) GetAcceptance(ctx context.Context, id string) (v Acceptance, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT id,snapshot_id,task_id,status,source_head,result_commit,integration_path,reason,updated_at FROM acceptances WHERE id=?", id).Scan(&v.ID, &v.SnapshotID, &v.TaskID, &v.Status, &v.SourceHead, &v.ResultCommit, &v.IntegrationPath, &v.Reason, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	return
}
func (s *Store) SaveAcceptance(ctx context.Context, v Acceptance) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO acceptances(id,snapshot_id,task_id,status,source_head,result_commit,integration_path,reason,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,source_head=excluded.source_head,result_commit=excluded.result_commit,integration_path=excluded.integration_path,reason=excluded.reason,updated_at=excluded.updated_at`, v.ID, v.SnapshotID, v.TaskID, v.Status, v.SourceHead, v.ResultCommit, v.IntegrationPath, v.Reason, core.Now())
	return err
}
