package flow

import (
	"blux/internal/flowmodel"
	"blux/internal/workspace"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryDeliveryIntegratesAndAccepts(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "code.txt"), []byte("base\n"), 0600)
	base, e := (workspace.Manager{DataDir: s.DataDir}).Freeze(ctx, src)
	if e != nil {
		t.Fatal(e)
	}
	template := flowmodel.Template{ID: "directory", Version: 1, Phases: []flowmodel.PhaseTemplate{{ID: "code", Mode: "development", Units: []flowmodel.UnitTemplate{{ID: "a", Prompt: "edit"}}}}}
	d, e := s.Create(ctx, CreateInput{Goal: "edit", Adapter: "codex", RepositoryPath: src, BaseCommit: base.BaseCommit, Template: template}, "create")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	finishJobs(t, s, false, "result ")
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	d, e = s.Store.Delivery(ctx, d.ID)
	if e != nil || d.Status != "completed" {
		t.Fatal(d, e)
	}
	b, _ := os.ReadFile(filepath.Join(src, "code.txt"))
	if string(b) != "base\n" {
		t.Fatal("source changed before acceptance")
	}
	d, e = s.Accept(ctx, d.ID, "accept", 1)
	if e != nil || d.Status != "accepted" {
		t.Fatal(d, e)
	}
	b, _ = os.ReadFile(filepath.Join(src, "code.txt"))
	if string(b) == "base\n" {
		t.Fatal("source unchanged after acceptance")
	}
	if _, e = os.Stat(filepath.Join(src, ".git")); !os.IsNotExist(e) {
		t.Fatal("source Git initialized")
	}
}
