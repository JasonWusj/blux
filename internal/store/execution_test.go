package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"blux/internal/core"
)

func executionStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "execution.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func executionJob(t *testing.T, s *Store) *core.Job {
	t.Helper()
	job, err := s.ClaimNext(context.Background(), "execution-test")
	if err != nil || job == nil {
		t.Fatalf("claim job: %+v %v", job, err)
	}
	return job
}

func executionInput(job *core.Job, kind string, payload any) core.EventInput {
	raw, _ := json.Marshal(payload)
	return core.EventInput{ID: core.NewID("event"), TaskID: job.TaskID, SessionID: job.SessionID, TurnID: job.TurnID, Generation: job.Generation, Type: kind, Payload: raw}
}

func executionEvent(t *testing.T, s *Store, job *core.Job, kind string, payload any) core.Event {
	t.Helper()
	event, duplicate, err := s.AppendEvent(context.Background(), executionInput(job, kind, payload))
	if err != nil || duplicate {
		t.Fatalf("append %s: duplicate=%v err=%v", kind, duplicate, err)
	}
	return event
}

func TestExecutionMetadataProjectionAndResume(t *testing.T) {
	s := executionStore(t)
	ctx := context.Background()
	in := core.CreateTaskInput{Title: "Codex", Prompt: "edit code", IdempotencyKey: "create", Adapter: "codex", RepositoryPath: "/source/repo", BaseCommit: "frozen-head"}
	detail, _, err := s.CreateTask(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	job := executionJob(t, s)
	if job.Adapter != "codex" || job.RepositoryPath != in.RepositoryPath || job.BaseCommit != in.BaseCommit || job.Runtime.NativeThreadID != "" {
		t.Fatalf("first job: %+v", job)
	}
	workspace := core.RuntimeInfo{WorkspacePath: "/tasks/task-root", WorktreePath: "/tasks/task-root/repos/repo", BranchName: "blux/task", BaseCommit: in.BaseCommit}
	wrong := workspace
	wrong.BaseCommit = "different-head"
	if _, _, err = s.AppendEvent(ctx, executionInput(job, "workspace.prepared", wrong)); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("workspace commit mismatch: %v", err)
	}
	executionEvent(t, s, job, "workspace.prepared", workspace)
	ready := core.RuntimeInfo{NativeThreadID: "thread-1", Model: "resolved-model", ModelProvider: "configured-provider", CLIVersion: "codex test"}
	executionEvent(t, s, job, "agent.session.ready", ready)
	executionEvent(t, s, job, "agent.turn.started", map[string]string{"native_turn_id": "native-turn-1"})
	executionEvent(t, s, job, "agent.warning", map[string]string{"code": "nonfatal", "message": "informational"})
	before, err := s.GetTask(ctx, job.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Task.Status != core.StatusRunning || before.Task.Model != "" || before.Task.ModelProvider != "" || before.Session.Runtime.NativeThreadID != "thread-1" || before.Session.Runtime.Model != "resolved-model" || before.Turns[0].NativeTurnID != "native-turn-1" {
		t.Fatalf("runtime projection: %+v", before)
	}
	executionEvent(t, s, job, "turn.completed", map[string]string{"summary": "done"})
	second, _, err := s.CreateTurn(ctx, job.TaskID, core.CreateTurnInput{Prompt: "follow up", IdempotencyKey: "next"})
	if err != nil {
		t.Fatal(err)
	}
	next := executionJob(t, s)
	if next.TurnID != second.SubmittedTurnID || next.Runtime.NativeThreadID != "thread-1" || next.Runtime.WorktreePath != workspace.WorktreePath || next.Runtime.Model != "resolved-model" || next.Model != "resolved-model" || next.Runtime.ModelProvider != "configured-provider" {
		t.Fatalf("resumed job did not carry frozen execution context: %+v", next)
	}
	// Repreparing the same workspace must retain the native thread for resume.
	executionEvent(t, s, next, "workspace.prepared", workspace)
	executionEvent(t, s, next, "agent.session.ready", ready)
	executionEvent(t, s, next, "agent.turn.started", map[string]string{"native_turn_id": "native-turn-2"})
	executionEvent(t, s, next, "turn.completed", map[string]string{"summary": "second result"})
	before, err = s.GetTask(ctx, job.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt projection-only metadata to prove replay overwrites it from facts.
	if _, err = s.db.ExecContext(ctx, "UPDATE tasks SET repository_path='bad',base_commit='bad',model='bad',model_provider='bad' WHERE id=?", job.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE sessions SET runtime_json=? WHERE id=?", `{"native_thread_id":"bad"}`, job.SessionID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Replay(ctx, job.TaskID); err != nil {
			t.Fatal(err)
		}
		after, err := s.GetTask(ctx, job.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("execution replay changed projection\nbefore=%+v\nafter=%+v", before, after)
		}
	}
	late := in
	late.BaseCommit = "source-head-moved"
	replayed, exists, err := s.LookupTaskCreation(ctx, late)
	if err != nil || !exists || replayed.SubmittedTurnID != detail.SubmittedTurnID || replayed.Task.CurrentTurnID != second.SubmittedTurnID || replayed.Task.BaseCommit != in.BaseCommit {
		t.Fatalf("late lookup: %+v exists=%v err=%v", replayed, exists, err)
	}
	late.Model = "different-model"
	if _, _, err = s.LookupTaskCreation(ctx, late); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("changed identity should conflict: %v", err)
	}
}

func TestExecutionMessageSnapshotsToolsFilesAndUsage(t *testing.T) {
	s := executionStore(t)
	ctx := context.Background()
	detail, _, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	job := executionJob(t, s)
	messageID := "msg_" + job.TurnID
	executionEvent(t, s, job, "message.delta", map[string]string{"message_id": messageID, "item_id": "text1", "text": "initial"})
	executionEvent(t, s, job, "tool.started", map[string]string{"message_id": messageID, "tool_call_id": "command1", "name": "shell", "input": "go test"})
	executionEvent(t, s, job, "tool.output", map[string]string{"message_id": messageID, "tool_call_id": "command1", "delta": "partial "})
	executionEvent(t, s, job, "tool.output", map[string]string{"message_id": messageID, "tool_call_id": "command1", "delta": "output"})
	streaming, err := s.GetTask(ctx, job.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if streaming.Messages[1].Segments[2].Output != "partial output" || streaming.Messages[1].Segments[1].Status != core.StatusRunning || streaming.Messages[1].Segments[2].Status != core.StatusRunning {
		t.Fatalf("tool delta projection: %+v", streaming.Messages[1])
	}
	executionEvent(t, s, job, "message.delta", map[string]string{"message_id": messageID, "item_id": "text2", "text": "another message"})
	executionEvent(t, s, job, "message.delta", map[string]string{"message_id": messageID, "item_id": "text1", "text": " continued"})
	executionEvent(t, s, job, "message.snapshot", map[string]string{"message_id": messageID, "item_id": "text1", "text": "correct final text"})
	executionEvent(t, s, job, "message.snapshot", map[string]string{"message_id": messageID, "item_id": "text3", "text": "snapshot without delta"})
	executionEvent(t, s, job, "tool.completed", map[string]string{"message_id": messageID, "tool_call_id": "command1", "name": "shell", "output": "authoritative final output"})
	for _, diff := range []string{"first diff", "correct final diff"} {
		executionEvent(t, s, job, "file.changed", map[string]any{"message_id": messageID, "item_id": "change1", "changes": []core.FileChange{{Path: "main.go", Kind: "update", Diff: diff}}})
	}
	usage := core.TokenUsage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 60, ReasoningOutputTokens: 5, TotalTokens: 120}
	usageInput := executionInput(job, "usage.updated", usage)
	if _, _, err = s.AppendEvent(ctx, usageInput); err != nil {
		t.Fatal(err)
	}
	if _, duplicate, err := s.AppendEvent(ctx, usageInput); err != nil || !duplicate {
		t.Fatalf("usage event dedupe: %v %v", duplicate, err)
	}
	// Native usage notifications can repeat the same totals under new event IDs.
	executionEvent(t, s, job, "usage.updated", usage)
	actual, err := s.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Turns[0].Usage != usage {
		t.Fatalf("usage accumulated twice: %+v", actual.Turns[0].Usage)
	}
	segments := actual.Messages[1].Segments
	if len(segments) != 6 || segments[0].Text != "correct final text" || segments[2].Type != "tool_result" || segments[2].Output != "authoritative final output" || segments[3].Text != "another message" || segments[4].Text != "snapshot without delta" || segments[5].Changes[0].Diff != "correct final diff" {
		t.Fatalf("unexpected final segments: %+v", segments)
	}
	if segments[1].Status != core.StatusCompleted || segments[2].Status != core.StatusCompleted {
		t.Fatalf("completed tool retains streaming status: %+v", segments)
	}
	if err = s.Replay(ctx, detail.Task.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, replayed) {
		t.Fatal("streamed execution projection changed after replay")
	}
	stale := executionInput(job, "usage.updated", usage)
	stale.Generation = 0
	if _, _, err = s.AppendEvent(ctx, stale); !errors.Is(err, core.ErrStaleGeneration) {
		t.Fatalf("new event bypassed generation check: %v", err)
	}
}

func TestMigrateVersionOneRetiresLegacySimulator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err = legacy.Exec(initialMigration); err != nil {
		t.Fatal(err)
	}
	stamp := "2026-11-02T00:00:00Z"
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tasks(id,title,prompt,status,adapter,session_id,current_turn_id,created_at,updated_at,last_event_seq) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"task_old", "legacy", "legacy prompt", "completed", "demo", "session_old", "turn_old", stamp, stamp, 5}},
		{`INSERT INTO sessions(id,task_id,created_at) VALUES(?,?,?)`, []any{"session_old", "task_old", stamp}},
		{`INSERT INTO turns(id,task_id,session_id,prompt,status,generation,created_at,started_at,ended_at,created_seq) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"turn_old", "task_old", "session_old", "legacy prompt", "completed", 1, stamp, stamp, stamp, 2}},
		{`INSERT INTO messages(id,session_id,turn_id,role,status,segments,created_seq,position) VALUES(?,?,?,?,?,?,?,?)`, []any{"usr_turn_old", "session_old", "turn_old", "user", "completed", `[{"type":"text","text":"legacy prompt"}]`, 2, 0}},
		{`INSERT INTO messages(id,session_id,turn_id,role,status,segments,created_seq,position) VALUES(?,?,?,?,?,?,?,?)`, []any{"msg_turn_old", "session_old", "turn_old", "assistant", "completed", `[{"type":"text","text":"legacy result"}]`, 2, 1}},
		{`INSERT INTO jobs(id,task_id,session_id,turn_id,generation,prompt,status,created_at) VALUES(?,?,?,?,?,?,?,?)`, []any{"job_old", "task_old", "session_old", "turn_old", 1, "legacy prompt", "completed", stamp}},
		{`INSERT INTO idempotency_keys(scope,key,digest,task_id,turn_id) VALUES(?,?,?,?,?)`, []any{"task", "legacy-key", digest(struct{ Title, Prompt string }{"legacy", "legacy prompt"}), "task_old", "turn_old"}},
	} {
		if _, err = legacy.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for index, event := range []struct{ kind, payload string }{
		{"task.created", `{"title":"legacy","prompt":"legacy prompt","adapter":"demo"}`},
		{"turn.queued", `{"prompt":"legacy prompt"}`},
		{"turn.started", `{}`},
		{"message.delta", `{"message_id":"msg_turn_old","text":"legacy result"}`},
		{"turn.completed", `{}`},
	} {
		if _, err = legacy.Exec(`INSERT INTO events(id,schema_version,task_id,session_id,turn_id,generation,seq,type,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("legacy_%d", index), 1, "task_old", "session_old", "turn_old", 1, index+1, event.kind, event.payload, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = legacy.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err = s.GetTask(ctx, "task_old"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("simulated task survived migration: %v", err)
	}
	for _, table := range []string{"tasks", "sessions", "turns", "events", "messages", "jobs", "idempotency_keys"} {
		var n int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("legacy rows remain in %s: %d %v", table, n, err)
		}
	}
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal(version, err)
	}

}

func TestTaskLookupIdentityIncludesExecutionChoices(t *testing.T) {
	s := executionStore(t)
	ctx := context.Background()
	base := core.CreateTaskInput{Prompt: "same prompt", IdempotencyKey: "key", Adapter: "codex", RepositoryPath: "/repo", BaseCommit: "head", Model: "model", ModelProvider: "provider"}
	if _, exists, err := s.LookupTaskCreation(ctx, base); err != nil || exists {
		t.Fatalf("missing lookup: %v %v", exists, err)
	}
	if _, _, err := s.CreateTask(ctx, base); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"adapter", "repository", "model", "provider"} {
		changed := base
		switch field {
		case "adapter":
			changed.Adapter = "claude"
		case "repository":
			changed.RepositoryPath = "/other"
		case "model":
			changed.Model = "other"
		case "provider":
			changed.ModelProvider = "other"
		}
		if _, _, err := s.LookupTaskCreation(ctx, changed); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("%s excluded from identity: %v", field, err)
		}
	}
}

func TestToolFailureStatusesSurviveProjectionReplay(t *testing.T) {
	for _, outcome := range []string{"failed", "declined", "turn.failed"} {
		t.Run(outcome, func(t *testing.T) {
			s := executionStore(t)
			ctx := context.Background()
			if _, _, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "tool failure"}); err != nil {
				t.Fatal(err)
			}
			job := executionJob(t, s)
			messageID := "msg_" + job.TurnID
			executionEvent(t, s, job, "tool.started", map[string]string{"message_id": messageID, "tool_call_id": "command", "name": "shell", "input": "test"})
			executionEvent(t, s, job, "tool.output", map[string]string{"message_id": messageID, "tool_call_id": "command", "delta": "partial result"})
			if outcome == "turn.failed" {
				executionEvent(t, s, job, "turn.failed", map[string]string{"code": "process_failed", "message": "process stopped"})
			} else {
				executionEvent(t, s, job, "tool.completed", map[string]string{"message_id": messageID, "tool_call_id": "command", "output": "final result", "status": outcome})
			}
			before, err := s.GetTask(ctx, job.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			for _, segment := range before.Messages[1].Segments {
				if segment.Status != core.StatusFailed {
					t.Fatalf("%s did not mark tool failed: %+v", outcome, segment)
				}
			}
			if err := s.Replay(ctx, job.TaskID); err != nil {
				t.Fatal(err)
			}
			after, err := s.GetTask(ctx, job.TaskID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("tool status replay differs: err=%v before=%+v after=%+v", err, before, after)
			}
		})
	}
}

func TestTurnModelSwitchContextAndReplay(t *testing.T) {
	s := executionStore(t)
	ctx := context.Background()
	d, _, e := s.CreateTask(ctx, core.CreateTaskInput{Prompt: "first", Adapter: "codex", RepositoryPath: "/source/repo", BaseCommit: "frozen", Model: "model-a", IdempotencyKey: "create"})
	if e != nil {
		t.Fatal(e)
	}
	j := executionJob(t, s)
	executionEvent(t, s, j, "agent.session.ready", core.RuntimeInfo{NativeThreadID: "thread", Model: "model-a", ModelProvider: "openai"})
	executionEvent(t, s, j, "context.updated", core.ContextUsage{UsedTokens: 1200, WindowTokens: 10000, Source: "codex", Model: "model-a"})
	executionEvent(t, s, j, "usage.updated", core.TokenUsage{TotalTokens: 50000})
	executionEvent(t, s, j, "turn.completed", map[string]string{})
	before, e := s.GetTask(ctx, d.Task.ID)
	if e != nil || before.Session.Runtime.ContextUsage.UsedTokens != 1200 {
		t.Fatalf("context must not use billed total: %+v %v", before, e)
	}
	input := core.CreateTurnInput{Prompt: "second", Model: "model-b", IdempotencyKey: "next"}
	d, _, e = s.CreateTurn(ctx, j.TaskID, input)
	if e != nil {
		t.Fatal(e)
	}
	replay, dup, e := s.CreateTurn(ctx, j.TaskID, input)
	if e != nil || !dup || replay.SubmittedTurnID != d.SubmittedTurnID {
		t.Fatal("model selection lost idempotency", e)
	}
	input.Model = "model-c"
	if _, _, e = s.CreateTurn(ctx, j.TaskID, input); !errors.Is(e, core.ErrConflict) {
		t.Fatal("different model reused receipt", e)
	}
	j = executionJob(t, s)
	if j.Model != "model-b" {
		t.Fatalf("selected model not dispatched: %+v", j)
	}
	executionEvent(t, s, j, "agent.session.ready", core.RuntimeInfo{NativeThreadID: "thread", Model: "model-b", ModelProvider: "openai"})
	switched, e := s.GetTask(ctx, j.TaskID)
	if e != nil || switched.Session.Runtime.ContextUsage != nil {
		t.Fatal("model switch kept stale context", e)
	}
	executionEvent(t, s, j, "context.updated", core.ContextUsage{UsedTokens: 400, Source: "codex", Model: "model-b"})
	executionEvent(t, s, j, "turn.completed", map[string]string{})
	before, e = s.GetTask(ctx, j.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Replay(ctx, j.TaskID); e != nil {
		t.Fatal(e)
	}
	after, e := s.GetTask(ctx, j.TaskID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("model/context replay differs: %v", e)
	}
	d, _, e = s.CreateTurn(ctx, j.TaskID, core.CreateTurnInput{Prompt: "third", IdempotencyKey: "third"})
	if e != nil {
		t.Fatal(e)
	}
	j = executionJob(t, s)
	if j.Model != "model-b" {
		t.Fatalf("next turn lost model selection: %+v", j)
	}
}

func TestReasoningEffortIsFrozenInheritedAndReplayed(t *testing.T) {
	s := executionStore(t)
	ctx := context.Background()
	in := core.CreateTaskInput{Adapter: "codex", RepositoryPath: "/source", BaseCommit: "head", Prompt: "first", ReasoningEffort: "high", IdempotencyKey: "first"}
	d, _, e := s.CreateTask(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if d.Turns[0].ReasoningEffort != "high" {
		t.Fatal("initial effort not frozen")
	}
	in.ReasoningEffort = "low"
	if _, _, e = s.CreateTask(ctx, in); !errors.Is(e, core.ErrConflict) {
		t.Fatal("effort excluded from task idempotency", e)
	}
	j := executionJob(t, s)
	if j.ReasoningEffort != "high" {
		t.Fatal("effort not dispatched")
	}
	executionEvent(t, s, j, "agent.session.ready", core.RuntimeInfo{NativeThreadID: "thread", Model: "model", ReasoningEffort: "high"})
	executionEvent(t, s, j, "turn.completed", map[string]string{})
	input := core.CreateTurnInput{Prompt: "second", ReasoningEffort: "low", IdempotencyKey: "next"}
	_, _, e = s.CreateTurn(ctx, j.TaskID, input)
	if e != nil {
		t.Fatal(e)
	}
	input.ReasoningEffort = "medium"
	if _, _, e = s.CreateTurn(ctx, j.TaskID, input); !errors.Is(e, core.ErrConflict) {
		t.Fatal("effort excluded from turn idempotency", e)
	}
	j = executionJob(t, s)
	if j.ReasoningEffort != "low" {
		t.Fatal("effort change was ignored")
	}
	executionEvent(t, s, j, "agent.session.ready", core.RuntimeInfo{NativeThreadID: "thread", Model: "model", ReasoningEffort: "low"})
	executionEvent(t, s, j, "turn.completed", map[string]string{})
	before, e := s.GetTask(ctx, j.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Replay(ctx, j.TaskID); e != nil {
		t.Fatal(e)
	}
	after, e := s.GetTask(ctx, j.TaskID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("effort replay mismatch", e)
	}
	if _, _, e = s.CreateTurn(ctx, j.TaskID, core.CreateTurnInput{Prompt: "third", IdempotencyKey: "third"}); e != nil {
		t.Fatal(e)
	}
	j = executionJob(t, s)
	if j.ReasoningEffort != "low" {
		t.Fatal("followup lost chosen effort")
	}
}

func TestNativeDefaultEffortIsCapturedWithoutChangingCreationReceipt(t *testing.T) {
	s := executionStore(t)
	ctx := context.Background()
	input := core.CreateTaskInput{Adapter: "codex", RepositoryPath: "/source", BaseCommit: "head", Prompt: "default effort", IdempotencyKey: "default"}
	d, _, e := s.CreateTask(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	j := executionJob(t, s)
	executionEvent(t, s, j, "agent.session.ready", core.RuntimeInfo{NativeThreadID: "thread", Model: "model", ReasoningEffort: "high"})
	actual, e := s.GetTask(ctx, d.Task.ID)
	if e != nil || actual.Turns[0].ReasoningEffort != "high" {
		t.Fatal("native default not captured", e)
	}
	retry, duplicate, e := s.CreateTask(ctx, input)
	if e != nil || !duplicate || retry.Task.ID != d.Task.ID {
		t.Fatal("native confirmation changed receipt", e)
	}
	if e = s.Replay(ctx, d.Task.ID); e != nil {
		t.Fatal(e)
	}
	actual, e = s.GetTask(ctx, d.Task.ID)
	if e != nil || actual.Turns[0].ReasoningEffort != "high" {
		t.Fatal("native default lost on replay", e)
	}
}
