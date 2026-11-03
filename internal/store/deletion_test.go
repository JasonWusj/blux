package store_test

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"errors"
	"testing"
)

func TestDeleteQueuedRunningAndReopen(t *testing.T) {
	s, file := openStore(t)
	ctx := context.Background()
	queued := createTask(t, s, "queued-delete")
	if _, err := s.DeleteTask(ctx, queued.Task.ID); err != nil {
		t.Fatal(err)
	}
	if j, err := s.ClaimNext(ctx, "worker"); err != nil || j != nil {
		t.Fatal(j, err)
	}
	running := createTask(t, s, "running-delete")
	job := claim(t, s)
	result, err := s.DeleteTask(ctx, running.Task.ID)
	if err != nil || !result.PendingStop {
		t.Fatal(result, err)
	}
	if _, err = s.DeleteTask(ctx, running.Task.ID); err != nil {
		t.Fatal("repeated delete", err)
	}
	if _, _, err = s.AppendEvent(ctx, eventFor(job, "message.delta", map[string]string{"text": "late"})); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("late event accepted", err)
	}
	if _, _, err = s.CreateTurn(ctx, running.Task.ID, core.CreateTurnInput{Prompt: "retry", IdempotencyKey: "retry"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("deleted task resumed", err)
	}
	if err = s.Replay(ctx, running.Task.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("deleted task replayed", err)
	}
	if _, _, err = s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Title: "example", Prompt: "make an artifact", IdempotencyKey: "running-delete"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("receipt recreated deleted task", err)
	}
	if err = s.RecoverRunning(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if tasks, err := s.ListTasks(ctx); err != nil || len(tasks) != 0 {
		t.Fatal(tasks, err)
	}
	if j, err := s.ClaimNext(ctx, "restarted"); err != nil || j != nil {
		t.Fatal("deleted job redispatched", j, err)
	}
}

func TestDeleteCollaborationAndPinnedInput(t *testing.T) {
	s, _ := openStore(t)
	ctx := context.Background()
	g, _, err := s.CreateCollaboration(ctx, store.CreateCollaborationInput{Title: "group", Goal: "work", RepositoryPath: "/project"}, "group")
	if err != nil {
		t.Fatal(err)
	}
	makeTask := func(key, group string, inputs ...string) core.TaskDetail {
		t.Helper()
		d, _, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "head", CollaborationID: group, Prompt: key, IdempotencyKey: key, InputArtifacts: inputs})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	producer := makeTask("producer", g.ID)
	job := claim(t, s)
	snapshot := store.Snapshot{ID: "snapshot-delete", TaskID: producer.Task.ID, TurnID: job.TurnID, Generation: job.Generation, CreatedAt: core.Now(), Artifacts: []store.ArtifactVersion{{ID: "pinned", TaskID: producer.Task.ID, TurnID: job.TurnID, Path: "report.md", Hash: "hash", Size: 4}}}
	if err = s.SaveSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	appendOK(t, s, eventFor(job, "turn.completed", map[string]string{}))
	consumer := makeTask("consumer", "", "pinned")
	if _, err = s.DeleteTask(ctx, producer.Task.ID); err != nil {
		t.Fatal(err)
	}
	if group, err := s.Collaboration(ctx, g.ID); err != nil || len(group.Tasks) != 0 || group.ArtifactCount != 0 {
		t.Fatal(group, err)
	}
	if versions, err := s.SharedSnapshots(ctx, g.ID); err != nil || len(versions) != 0 {
		t.Fatal(versions, err)
	}
	if _, err = s.GetTask(ctx, producer.Task.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.Snapshot(ctx, snapshot.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.Artifact(ctx, "pinned"); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	if inputs, err := s.TaskInputs(ctx, consumer.Task.ID); err != nil || len(inputs) != 1 || inputs[0].Hash != "hash" {
		t.Fatal("pinned version lost", inputs, err)
	}
	first := makeTask("child-a", g.ID)
	second := makeTask("child-b", g.ID)
	result, err := s.DeleteCollaboration(ctx, g.ID)
	if err != nil || len(result.TaskIDs) != 3 {
		t.Fatal(result, err)
	}
	for _, id := range []string{first.Task.ID, second.Task.ID} {
		if _, err = s.GetTask(ctx, id); !errors.Is(err, core.ErrNotFound) {
			t.Fatal(id, err)
		}
	}
	if _, err = s.DeleteCollaboration(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if groups, err := s.Collaborations(ctx); err != nil || len(groups) != 0 {
		t.Fatal(groups, err)
	}
	if tasks, err := s.ListTasks(ctx); err != nil || len(tasks) != 1 || tasks[0].ID != consumer.Task.ID {
		t.Fatal(tasks, err)
	}
	if _, _, err = s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/repo", BaseCommit: "head", CollaborationID: g.ID, Prompt: "late child", IdempotencyKey: "late"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("child added to deleted group", err)
	}
}
