package store

import (
	"blux/internal/core"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRetiredSimulatorMigrationPreservesRealHistoryAndSharedBlobs(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	path := filepath.Join(data, "blux.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	var oldID, realID string
	for _, old := range []bool{true, false} {
		d, _, e := s.CreateTask(ctx, core.CreateTaskInput{Prompt: "history", Adapter: "codex", RepositoryPath: "/source", BaseCommit: "head", IdempotencyKey: core.NewID("key")})
		if e != nil {
			t.Fatal(e)
		}
		j := executionJob(t, s)
		id := core.NewID("snapshot")
		artifacts := []ArtifactVersion{{ID: core.NewID("artifact"), TaskID: j.TaskID, TurnID: j.TurnID, Path: "shared.md", Hash: "shared_hash"}}
		if old {
			oldID = d.Task.ID
			artifacts = append(artifacts, ArtifactVersion{ID: core.NewID("artifact"), TaskID: j.TaskID, TurnID: j.TurnID, Path: "old.md", Hash: "old_hash"})
		} else {
			realID = d.Task.ID
		}
		if e = s.SaveSnapshot(ctx, Snapshot{ID: id, TaskID: j.TaskID, TurnID: j.TurnID, Generation: j.Generation, CreatedAt: core.Now(), Artifacts: artifacts}); e != nil {
			t.Fatal(e)
		}
		executionEvent(t, s, j, "turn.completed", map[string]string{})
		if _, e = s.db.Exec("INSERT INTO task_inputs(task_id,artifact_id)VALUES(?,?)", j.TaskID, artifacts[0].ID); e != nil {
			t.Fatal(e)
		}
		workspace := filepath.Join(data, "workspaces", d.Task.ID)
		os.MkdirAll(workspace, 0700)
		os.WriteFile(filepath.Join(workspace, "result.md"), []byte("output"), 0600)
	}
	for _, hash := range []string{"shared_hash", "old_hash"} {
		os.MkdirAll(filepath.Join(data, "artifacts", "blobs"), 0700)
		os.WriteFile(filepath.Join(data, "artifacts", "blobs", hash), []byte(hash), 0600)
	}
	before, e := s.GetTask(ctx, realID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("UPDATE tasks SET adapter='demo' WHERE id=?", oldID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("DROP TABLE pending_cleanup;ALTER TABLE turns DROP COLUMN reasoning_effort;PRAGMA user_version=10"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.GetTask(ctx, oldID); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("retired task remains", e)
	}
	after, e := s.GetTask(ctx, realID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("real task history changed", e)
	}
	if e = s.CleanupRetiredData(ctx, data); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{filepath.Join(data, "workspaces", oldID), filepath.Join(data, "artifacts", "blobs", "old_hash")} {
		if _, e = os.Stat(path); !os.IsNotExist(e) {
			t.Fatal("retired data remains", path, e)
		}
	}
	for _, path := range []string{filepath.Join(data, "workspaces", realID, "result.md"), filepath.Join(data, "artifacts", "blobs", "shared_hash")} {
		if _, e = os.Stat(path); e != nil {
			t.Fatal("real/shared data was removed", path, e)
		}
	}
	if e = s.CleanupRetiredData(ctx, data); e != nil {
		t.Fatal("cleanup not restartable", e)
	}
	if _, _, e = s.CreateTask(ctx, core.CreateTaskInput{Adapter: "demo", Prompt: "removed"}); !errors.Is(e, core.ErrInvalidState) {
		t.Fatal("retired adapter accepted", e)
	}
}
