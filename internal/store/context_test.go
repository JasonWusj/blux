package store

import (
	"blux/internal/core"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestContextVersionFreezingAndSearch(t *testing.T) {
	ctx := context.Background()
	db, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	first, e := db.SaveContext(ctx, ContextEntry{ProjectPath: "/project", Kind: "memory", Title: "project facts", Content: "first constraint"})
	if e != nil {
		t.Fatal(e)
	}
	d, _, e := db.CreateTask(ctx, core.CreateTaskInput{BaseCommit: "frozen-head", Prompt: "spec", RepositoryPath: "/project", Mode: "spec", ContextIDs: []string{first.ID}, IdempotencyKey: "task"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.SaveContext(ctx, ContextEntry{ProjectPath: "/project", Kind: "memory", Title: "project facts", Content: "second constraint", PreviousID: first.ID})
	if e != nil {
		t.Fatal(e)
	}
	v, e := db.TaskContext(ctx, d.Task.ID)
	if e != nil || len(v.Entries) != 1 || v.Entries[0].Content != "first constraint" || v.Mode.ID != "spec" {
		t.Fatal(v, e)
	}
	entries, e := db.ContextEntries(ctx, "/project", "second")
	if e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
	entries, e = db.ContextEntries(ctx, "/project", "first")
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
}

func TestContinuationSkillsAreFrozenScopedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	save := func(kind, title, text, previous, project string) ContextEntry {
		t.Helper()
		e, err := db.SaveContext(ctx, ContextEntry{ProjectPath: project, Kind: kind, Title: title, Content: text, PreviousID: previous})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	memory := save("memory", "preferences", "keep it simple", "", "/project")
	skill1 := save("skill", "review", "version one", "", "/project")
	detail, _, err := db.CreateTask(ctx, core.CreateTaskInput{BaseCommit: "frozen-head", Prompt: "first", RepositoryPath: "/project", ContextIDs: []string{memory.ID, skill1.ID}, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	finish := func() {
		t.Helper()
		job, e := db.ClaimNext(ctx, "worker")
		if e != nil || job == nil {
			t.Fatal(job, e)
		}
		if _, _, e = db.AppendEvent(ctx, core.EventInput{TaskID: job.TaskID, SessionID: job.SessionID, TurnID: job.TurnID, Generation: job.Generation, Type: "turn.completed"}); e != nil {
			t.Fatal(e)
		}
	}
	finish()
	skill2 := save("skill", "review", "version two", skill1.ID, "/project")
	in := core.CreateTurnInput{Prompt: "next", ContextIDs: []string{skill2.ID}, IdempotencyKey: "next"}
	next, _, err := db.CreateTurn(ctx, detail.Task.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	skill3 := save("skill", "review", "version three", skill2.ID, "/project")
	repeated, replayed, err := db.CreateTurn(ctx, detail.Task.ID, in)
	if err != nil || !replayed || repeated.SubmittedTurnID != next.SubmittedTurnID {
		t.Fatal(repeated, replayed, err)
	}
	changed := in
	changed.ContextIDs = []string{skill3.ID}
	if _, _, e := db.CreateTurn(ctx, detail.Task.ID, changed); !errors.Is(e, core.ErrConflict) {
		t.Fatal("changed selection reused key", e)
	}
	assertContext := func() {
		t.Helper()
		v, e := db.ExecutionContext(ctx, detail.Task.ID, next.SubmittedTurnID)
		if e != nil || len(v.Entries) != 2 || v.Entries[0].ID != memory.ID || v.Entries[1].Content != "version two" {
			t.Fatal(v, e)
		}
		original, e := db.ExecutionContext(ctx, detail.Task.ID, detail.SubmittedTurnID)
		if e != nil || original.Entries[1].Content != "version one" {
			t.Fatal(original, e)
		}
	}
	assertContext()
	finish()
	if err = db.Replay(ctx, detail.Task.ID); err != nil {
		t.Fatal(err)
	}
	assertContext()
	other := save("skill", "other", "foreign project", "", "/other")
	before, _ := db.GetTask(ctx, detail.Task.ID)
	if _, _, err = db.CreateTurn(ctx, detail.Task.ID, core.CreateTurnInput{Prompt: "invalid", ContextIDs: []string{other.ID}, IdempotencyKey: "bad"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	after, _ := db.GetTask(ctx, detail.Task.ID)
	if len(before.Turns) != len(after.Turns) {
		t.Fatal("invalid context left a queued turn")
	}
}
