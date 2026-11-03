package store

import (
	"blux/internal/core"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStopQueuedAndRecoverWithoutRedispatch(t *testing.T) {
	ctx := context.Background()
	db, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d, _, e := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "cancel", IdempotencyKey: "a"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := db.RequestControl(ctx, d.Task.ID, d.Task.CurrentTurnID, "stop", "stop", "")
	if e != nil || c.Status != "applied" {
		t.Fatal(c, e)
	}
	c, e = db.RequestControl(ctx, d.Task.ID, d.Task.CurrentTurnID, "stop", "stop", "")
	if e != nil || c.Status != "applied" {
		t.Fatal(c, e)
	}
	if j, e := db.ClaimNext(ctx, "w"); e != nil || j != nil {
		t.Fatal(j, e)
	}
	d, _, e = db.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "continue", IdempotencyKey: "b"})
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.ClaimNext(ctx, "w")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.RecoverRunning(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := db.GetTask(ctx, d.Task.ID)
	if e != nil || got.Turns[1].ErrorCode != "recovery_required" {
		t.Fatal(got, e)
	}
	if _, _, e = db.AppendEvent(ctx, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.completed", Payload: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("accepted late completion")
	}
	if j, e := db.ClaimNext(ctx, "w2"); e != nil || j != nil {
		t.Fatal("redispatched", j, e)
	}
	if e = db.Replay(ctx, d.Task.ID); e != nil {
		t.Fatal(e)
	}
}
