package store_test

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"errors"
	"testing"
)

func TestPinsPersistOrderAndDoNotChangeTaskExecution(t *testing.T) {
	s, file := openStore(t)
	ctx := context.Background()
	first := createTask(t, s, "pin-first")
	second := createTask(t, s, "pin-second")
	g1, _, err := s.CreateCollaboration(ctx, store.CreateCollaborationInput{Goal: "first"}, "pin-group-one")
	if err != nil {
		t.Fatal(err)
	}
	g2, _, err := s.CreateCollaboration(ctx, store.CreateCollaborationInput{Goal: "second"}, "pin-group-two")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.SetPin(ctx, "task", first.Task.ID, true)
	if err != nil || !p.Pinned || p.PinnedAt == "" {
		t.Fatal(p, err)
	}
	again, err := s.SetPin(ctx, "task", first.Task.ID, true)
	if err != nil || again.PinnedAt != p.PinnedAt {
		t.Fatal("repeated PUT changed order", again, err)
	}
	if _, err = s.SetPin(ctx, "collaboration", g1.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.Replay(ctx, first.Task.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetTask(ctx, first.Task.ID)
	if err != nil || d.Task.PinnedAt != p.PinnedAt || d.Task.LastEventSeq != first.Task.LastEventSeq || len(d.Turns) != 1 || d.Task.Status != core.StatusQueued {
		t.Fatal("pin changed execution or replay lost preference", d, err)
	}
	s.Close()
	s, err = store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tasks, err := s.ListTasks(ctx)
	if err != nil || len(tasks) != 2 || tasks[0].ID != first.Task.ID {
		t.Fatal(tasks, err)
	}
	groups, err := s.Collaborations(ctx)
	if err != nil || len(groups) != 2 || groups[0].ID != g1.ID {
		t.Fatal(groups, err)
	}
	if _, err = s.SetPin(ctx, "task", first.Task.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPin(ctx, "collaboration", g1.ID, false); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.ListTasks(ctx)
	if err != nil || tasks[0].ID != second.Task.ID || tasks[1].PinnedAt != "" {
		t.Fatal(tasks, err)
	}
	groups, err = s.Collaborations(ctx)
	if err != nil || groups[0].ID != g2.ID {
		t.Fatal(groups, err)
	}
	if _, err = s.DeleteTask(ctx, first.Task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPin(ctx, "task", first.Task.ID, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("pinned deleted task", err)
	}
	if _, err = s.DeleteCollaboration(ctx, g1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPin(ctx, "collaboration", g1.ID, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("pinned deleted collaboration", err)
	}
}
