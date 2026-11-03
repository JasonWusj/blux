package store_test

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"errors"
	"testing"
)

func TestCollaborationMembershipAndCompletedPublicVersions(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	input := store.CreateCollaborationInput{Title: "Shared goal", Goal: "Build something", RepositoryPath: "/project"}
	c, dup, e := s.CreateCollaboration(ctx, input, "create")
	if e != nil || dup {
		t.Fatal(e, dup)
	}
	again, dup, e := s.CreateCollaboration(ctx, input, "create")
	if e != nil || !dup || again.ID != c.ID {
		t.Fatal(e, dup)
	}
	input.Title = "changed"
	if _, _, e = s.CreateCollaboration(ctx, input, "create"); !errors.Is(e, core.ErrConflict) {
		t.Fatal(e)
	}
	makeTask := func(key string) core.TaskDetail {
		t.Helper()
		d, _, e := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", CollaborationID: c.ID, Adapter: "codex", Prompt: key, IdempotencyKey: key})
		if e != nil || d.CollaborationID != c.ID || d.Task.CollaborationID != c.ID {
			t.Fatal(d, e)
		}
		return d
	}
	first := makeTask("first")
	j := claim(t, s)
	v := store.Snapshot{ID: "snapshot-1", TaskID: first.Task.ID, TurnID: j.TurnID, Generation: j.Generation, CreatedAt: core.Now(), Artifacts: []store.ArtifactVersion{{ID: "artifact-1", TaskID: first.Task.ID, TurnID: j.TurnID, Path: "Result.md", Hash: "aaa", Size: 3, MIME: "text/markdown"}}}
	if e = s.SaveSnapshot(ctx, v); e != nil {
		t.Fatal(e)
	}
	versions, e := s.SharedSnapshots(ctx, c.ID)
	if e != nil || len(versions) != 0 {
		t.Fatal("running output leaked", versions, e)
	}
	appendOK(t, s, eventFor(j, "turn.completed", map[string]string{}))
	second := makeTask("second")
	j = claim(t, s)
	v.ID = "snapshot-2"
	v.TaskID = second.Task.ID
	v.TurnID = j.TurnID
	v.Artifacts[0].ID = "artifact-2"
	v.Artifacts[0].TaskID = second.Task.ID
	v.Artifacts[0].TurnID = j.TurnID
	if e = s.SaveSnapshot(ctx, v); e != nil {
		t.Fatal(e)
	}
	appendOK(t, s, eventFor(j, "turn.failed", map[string]string{"error": "fixture failure"}))
	versions, e = s.SharedSnapshots(ctx, c.ID)
	if e != nil || len(versions) != 1 || versions[0].TaskID != first.Task.ID {
		t.Fatal("failed result published", versions, e)
	}
	group, e := s.Collaboration(ctx, c.ID)
	if e != nil || group.Status != "attention" || group.ArtifactCount != 1 || len(group.Tasks) != 2 {
		t.Fatal(group, e)
	}
	pinned := core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", CollaborationID: c.ID, Adapter: "codex", Prompt: "consume", IdempotencyKey: "consume", InputArtifacts: []string{"artifact-1"}}
	d, _, e := s.CreateTask(ctx, pinned)
	if e != nil {
		t.Fatal(e)
	}
	inputs, e := s.TaskInputs(ctx, d.Task.ID)
	if e != nil || len(inputs) != 1 || inputs[0].ID != "artifact-1" {
		t.Fatal(inputs, e)
	}
	pinned.InputArtifacts = nil // A dispatch retry must retain the original frozen input set.
	replay, dup, e := s.CreateTask(ctx, pinned)
	if e != nil || !dup || replay.Task.ID != d.Task.ID {
		t.Fatal(replay, dup, e)
	}
	_, _, e = s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", CollaborationID: "missing", Adapter: "codex", Prompt: "invalid membership", IdempotencyKey: "bad"})
	if e == nil {
		t.Fatal("missing collaboration accepted")
	}
	tasks, e := s.ListTasks(ctx)
	if e != nil || len(tasks) != 3 {
		t.Fatal("failed membership left orphan task", tasks, e)
	}
}
