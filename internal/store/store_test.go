package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"blux/internal/core"
	"blux/internal/store"
)

func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blux.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func createTask(t *testing.T, s *store.Store, key string) core.TaskDetail {
	t.Helper()
	d, duplicate, err := s.CreateTask(context.Background(), core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Title: "example", Prompt: "make an artifact", IdempotencyKey: key})
	if err != nil || duplicate {
		t.Fatalf("create: duplicate=%v err=%v", duplicate, err)
	}
	return d
}

func eventFor(j *core.Job, kind string, payload any) core.EventInput {
	raw, _ := json.Marshal(payload)
	return core.EventInput{ID: core.NewID("event"), TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: kind, Payload: raw}
}

func appendOK(t *testing.T, s *store.Store, event core.EventInput) core.Event {
	t.Helper()
	e, duplicate, err := s.AppendEvent(context.Background(), event)
	if err != nil || duplicate {
		t.Fatalf("append %s: duplicate=%v err=%v", event.Type, duplicate, err)
	}
	return e
}

func claim(t *testing.T, s *store.Store) *core.Job {
	t.Helper()
	j, err := s.ClaimNext(context.Background(), "worker-test")
	if err != nil || j == nil {
		t.Fatalf("claim: job=%v err=%v", j, err)
	}
	return j
}

func TestIdempotencyAndSingleActiveTurn(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	d := createTask(t, s, "task-request")
	if d.Task.Adapter != "codex" || d.Task.Status != core.StatusQueued || len(d.Turns) != 1 || len(d.Messages) != 2 || d.Task.LastEventSeq != 2 {
		t.Fatalf("unexpected initial detail: %+v", d)
	}
	again, replayed, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Title: "example", Prompt: "make an artifact", IdempotencyKey: "task-request"})
	if err != nil || !replayed || again.Task.ID != d.Task.ID {
		t.Fatalf("replay: %+v %v %v", again, replayed, err)
	}
	_, _, err = s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Title: "example", Prompt: "different", IdempotencyKey: "task-request"})
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("expected key conflict, got %v", err)
	}
	_, _, err = s.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "continue", IdempotencyKey: "next"})
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("expected active turn conflict, got %v", err)
	}
	j := claim(t, s)
	appendOK(t, s, eventFor(j, "turn.completed", map[string]string{"summary": "finished"}))
	next, replayed, err := s.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "continue", IdempotencyKey: "next"})
	if err != nil || replayed || len(next.Turns) != 2 || next.Task.Status != core.StatusQueued {
		t.Fatalf("next turn: %+v %v %v", next, replayed, err)
	}
	_, replayed, err = s.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "continue", IdempotencyKey: "next"})
	if err != nil || !replayed {
		t.Fatalf("turn replay: %v %v", replayed, err)
	}
	_, _, err = s.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "changed", IdempotencyKey: "next"})
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("turn key mismatch: %v", err)
	}
	tasks, err := s.ListTasks(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("task list: %v %v", tasks, err)
	}
}

func TestIdempotencyReceiptKeepsOriginalTurnAfterLaterTurns(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	first := createTask(t, s, "original-task")
	firstTurnID := first.Task.CurrentTurnID
	if first.SubmittedTurnID != firstTurnID {
		t.Fatalf("create receipt=%q, want %q", first.SubmittedTurnID, firstTurnID)
	}
	appendOK(t, s, eventFor(claim(t, s), "turn.completed", map[string]string{}))
	second, _, err := s.CreateTurn(ctx, first.Task.ID, core.CreateTurnInput{Prompt: "second", IdempotencyKey: "second-request"})
	if err != nil {
		t.Fatal(err)
	}
	secondTurnID := second.Task.CurrentTurnID
	if second.SubmittedTurnID != secondTurnID {
		t.Fatalf("second receipt=%q, want %q", second.SubmittedTurnID, secondTurnID)
	}
	appendOK(t, s, eventFor(claim(t, s), "turn.completed", map[string]string{}))
	third, _, err := s.CreateTurn(ctx, first.Task.ID, core.CreateTurnInput{Prompt: "third", IdempotencyKey: "third-request"})
	if err != nil {
		t.Fatal(err)
	}
	thirdTurnID := third.Task.CurrentTurnID
	secondReplay, replayed, err := s.CreateTurn(ctx, first.Task.ID, core.CreateTurnInput{Prompt: "second", IdempotencyKey: "second-request"})
	if err != nil || !replayed {
		t.Fatalf("second replay: replayed=%v err=%v", replayed, err)
	}
	if secondReplay.SubmittedTurnID != secondTurnID || secondReplay.Task.CurrentTurnID != thirdTurnID || len(secondReplay.Turns) != 3 {
		t.Fatalf("original receipt and current state were conflated: %+v", secondReplay)
	}
	firstReplay, replayed, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Title: "example", Prompt: "make an artifact", IdempotencyKey: "original-task"})
	if err != nil || !replayed {
		t.Fatalf("first replay: replayed=%v err=%v", replayed, err)
	}
	if firstReplay.SubmittedTurnID != firstTurnID || firstReplay.Task.CurrentTurnID != thirdTurnID || len(firstReplay.Turns) != 3 {
		t.Fatalf("original task receipt lost: %+v", firstReplay)
	}
	latest, err := s.GetTask(ctx, first.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.SubmittedTurnID != "" {
		t.Fatalf("GET unexpectedly contains a submission receipt: %+v", latest)
	}
	encoded, err := json.Marshal(latest)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["submitted_turn_id"]; ok {
		t.Fatal("GET JSON contains submitted_turn_id")
	}
	secondReplay.SubmittedTurnID = ""
	firstReplay.SubmittedTurnID = ""
	if !reflect.DeepEqual(latest, secondReplay) || !reflect.DeepEqual(latest, firstReplay) {
		t.Fatal("replayed request should return current task detail alongside its original receipt")
	}
}

func TestConcurrentClaimAcrossStores(t *testing.T) {
	s, path := openStore(t)
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	const count = 18
	for i := 0; i < count; i++ {
		createTask(t, s, fmt.Sprint(i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	jobs := make(chan *core.Job, count)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for i, connection := range []*store.Store{s, other} {
		wg.Add(1)
		go func(worker int, connection *store.Store) {
			defer wg.Done()
			for {
				j, err := connection.ClaimNext(ctx, fmt.Sprintf("worker-%d", worker))
				if err != nil {
					failures <- err
					return
				}
				if j == nil {
					return
				}
				jobs <- j
			}
		}(i, connection)
	}
	wg.Wait()
	close(jobs)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := map[string]bool{}
	for j := range jobs {
		if seen[j.ID] {
			t.Errorf("claimed twice: %s", j.ID)
		}
		seen[j.ID] = true
	}
	if len(seen) != count {
		t.Fatalf("claimed %d jobs, want %d", len(seen), count)
	}
}

func TestEventsDeduplicationGenerationAndTerminalState(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	d := createTask(t, s, "request")
	j := claim(t, s)
	delta := eventFor(j, "message.delta", map[string]string{"message_id": "msg_" + j.TurnID, "text": "hello"})
	first := appendOK(t, s, delta)
	duplicate, replayed, err := s.AppendEvent(ctx, delta)
	if err != nil || !replayed || duplicate.Seq != first.Seq {
		t.Fatalf("duplicate event: %+v %v %v", duplicate, replayed, err)
	}
	different := delta
	different.Payload = json.RawMessage(`{"message_id":"msg_bad","text":"different"}`)
	if _, _, err = s.AppendEvent(ctx, different); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("event id content mismatch: %v", err)
	}
	stale := eventFor(j, "message.delta", map[string]string{"message_id": "msg_" + j.TurnID, "text": "stale"})
	stale.Generation = 0
	if _, _, err = s.AppendEvent(ctx, stale); !errors.Is(err, core.ErrStaleGeneration) {
		t.Fatalf("stale generation: %v", err)
	}
	wrong := eventFor(j, "message.delta", map[string]string{"message_id": "msg_" + j.TurnID, "text": "wrong"})
	wrong.SessionID = "other"
	if _, _, err = s.AppendEvent(ctx, wrong); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("wrong identity: %v", err)
	}
	appendOK(t, s, eventFor(j, "turn.completed", map[string]string{}))
	if _, _, err = s.AppendEvent(ctx, eventFor(j, "turn.failed", map[string]string{"code": "late", "message": "late failure"})); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("terminal overwrite: %v", err)
	}
	if _, replayed, err = s.AppendEvent(ctx, delta); err != nil || !replayed {
		t.Fatalf("terminal duplicate should replay: %v %v", replayed, err)
	}
	detail, err := s.GetTask(ctx, d.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Status != core.StatusCompleted || detail.Messages[1].Segments[0].Text != "hello" {
		t.Fatalf("projection: %+v", detail)
	}
	events, err := s.Events(ctx, d.Task.ID, first.Seq, 100)
	if err != nil || len(events) != 1 || events[0].Type != "turn.completed" {
		t.Fatalf("incremental events: %+v %v", events, err)
	}
	for i, e := range mustEvents(t, s, d.Task.ID) {
		if e.Seq != int64(i+1) {
			t.Errorf("event sequence gap: %+v", e)
		}
	}
}

func mustEvents(t *testing.T, s *store.Store, taskID string) []core.Event {
	t.Helper()
	events, err := s.Events(context.Background(), taskID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestConcurrentTerminalHasOneWinner(t *testing.T) {
	s, path := openStore(t)
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	d := createTask(t, s, "request")
	j := claim(t, s)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i, connection := range []*store.Store{s, other} {
		wg.Add(1)
		go func(i int, connection *store.Store) {
			defer wg.Done()
			kind := "turn.completed"
			if i == 1 {
				kind = "turn.failed"
			}
			_, _, err := connection.AppendEvent(context.Background(), eventFor(j, kind, map[string]string{}))
			errorsCh <- err
		}(i, connection)
	}
	wg.Wait()
	close(errorsCh)
	winners, losers := 0, 0
	for err := range errorsCh {
		if err == nil {
			winners++
		} else if errors.Is(err, core.ErrInvalidState) {
			losers++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d", winners, losers)
	}
	events := mustEvents(t, s, d.Task.ID)
	if len(events) != 4 {
		t.Fatalf("unexpected events after terminal race: %+v", events)
	}
}

func TestPersistenceAndReplay(t *testing.T) {
	s, path := openStore(t)
	ctx := context.Background()
	d := createTask(t, s, "request")
	j := claim(t, s)
	messageID := "msg_" + j.TurnID
	appendOK(t, s, eventFor(j, "message.delta", map[string]string{"message_id": messageID, "text": "before "}))
	appendOK(t, s, eventFor(j, "message.delta", map[string]string{"message_id": messageID, "text": "tool"}))
	appendOK(t, s, eventFor(j, "tool.started", map[string]string{"message_id": messageID, "tool_call_id": "tool1", "name": "shell", "input": "pwd"}))
	appendOK(t, s, eventFor(j, "tool.completed", map[string]string{"message_id": messageID, "tool_call_id": "tool1", "name": "shell", "output": "/workspace"}))
	appendOK(t, s, eventFor(j, "message.delta", map[string]string{"message_id": messageID, "text": "after tool"}))
	appendOK(t, s, eventFor(j, "turn.completed", map[string]string{"summary": "already streamed"}))
	if _, _, err := s.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "follow up", IdempotencyKey: "follow"}); err != nil {
		t.Fatal(err)
	}
	j = claim(t, s)
	appendOK(t, s, eventFor(j, "turn.failed", map[string]string{"code": "fixture_failure", "message": "deliberate failure"}))
	before, err := s.GetTask(ctx, d.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Messages[1].Segments) != 4 || before.Messages[1].Segments[0].Text != "before tool" {
		t.Fatalf("segments: %+v", before.Messages[1].Segments)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := reopened.GetTask(ctx, d.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, persisted) {
		t.Fatalf("persisted projection differs\nbefore=%+v\nafter=%+v", before, persisted)
	}
	for i := 0; i < 2; i++ {
		if err = reopened.Replay(ctx, d.Task.ID); err != nil {
			t.Fatal(err)
		}
		after, err := reopened.GetTask(ctx, d.Task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("replayed projection differs\nbefore=%+v\nafter=%+v", before, after)
		}
	}
	if j, err := reopened.ClaimNext(ctx, "another"); err != nil || j != nil {
		t.Fatalf("completed jobs requeued: %+v %v", j, err)
	}
}

func TestInvalidProjectionRollsBackEvent(t *testing.T) {
	s, _ := openStore(t)
	d := createTask(t, s, "request")
	j := claim(t, s)
	invalid := eventFor(j, "tool.completed", map[string]string{"message_id": "msg_" + j.TurnID, "tool_call_id": "missing", "output": "invalid"})
	if _, _, err := s.AppendEvent(context.Background(), invalid); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("expected invalid state, got %v", err)
	}
	if events := mustEvents(t, s, d.Task.ID); len(events) != 3 {
		t.Fatalf("invalid projection left persisted event: %+v", events)
	}
}

func TestNotFoundAndHeartbeat(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	if _, err := s.GetTask(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Events(ctx, "missing", 0, 10); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Replay(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	if w, err := s.GetWorkerStatus(ctx); err != nil || w != nil {
		t.Fatalf("initial worker: %+v %v", w, err)
	}
	if err := s.WorkerHeartbeat(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	if w, err := s.GetWorkerStatus(ctx); err != nil || w == nil || w.ID != "worker" || !w.Online {
		t.Fatalf("worker: %+v %v", w, err)
	}
}
