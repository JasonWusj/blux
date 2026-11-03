package flow

import (
	"blux/internal/artifact"
	"blux/internal/core"
	"blux/internal/flowmodel"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) Service {
	t.Helper()
	data := t.TempDir()
	db, e := store.Open(filepath.Join(data, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return Service{Store: db, DataDir: data}
}
func finishJobs(t *testing.T, s Service, failedVerification bool, code string) {
	t.Helper()
	ctx := context.Background()
	for {
		j, e := s.Store.ClaimNext(ctx, "test")
		if e != nil {
			t.Fatal(e)
		}
		if j == nil {
			return
		}
		rt, e := (workspace.Manager{DataDir: s.DataDir}).Prepare(ctx, *j)
		if e != nil {
			t.Fatal(e)
		}
		pack, e := s.Store.TaskContext(ctx, j.TaskID)
		if e != nil {
			t.Fatal(e)
		}
		if rt.WorktreePath != "" {
			if e = os.WriteFile(filepath.Join(rt.WorktreePath, "code.txt"), []byte(code+j.TaskID+"\n"), 0600); e != nil {
				t.Fatal(e)
			}
		}
		name := "Spec.md"
		text := "exact version"
		if pack.Mode.ID != "spec" {
			name = "Implementation.md"
		}
		os.WriteFile(filepath.Join(rt.WorkspacePath, "artifacts", name), []byte(text), 0600)
		if failedVerification && pack.Mode.ID == "development" {
			os.WriteFile(filepath.Join(rt.WorkspacePath, "artifacts", "Verification.json"), []byte(`{"passed":false,"feedback":"test failed"}`), 0600)
		}
		if e = (artifact.Service{Store: s.Store, DataDir: s.DataDir}).Capture(ctx, *j, rt); e != nil {
			t.Fatal(e)
		}
		if _, _, e = s.Store.AppendEvent(ctx, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.completed"}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestGatesExactVersionsRollbackAndIdempotency(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	repo, base := flowFixtureRepo(t)
	d, e := s.Create(ctx, CreateInput{Goal: "ship", Adapter: "codex", RepositoryPath: repo, BaseCommit: base}, "create")
	if e != nil {
		t.Fatal(e)
	}
	tick := func() {
		t.Helper()
		if e = s.Tick(ctx); e != nil {
			t.Fatal(e)
		}
		d, e = s.Store.Delivery(ctx, d.ID)
		if e != nil {
			t.Fatal(e)
		}
	}
	tick()
	finishJobs(t, s, false, "")
	tick()
	if d.Run.Phases[0].Status != "awaiting_review" || d.Run.Phases[1].Attempt.Units[0].TaskID != "" {
		t.Fatal("gate advanced", d)
	}
	if _, e = s.Action(ctx, d.ID, "bad", Action{Kind: "approve", PhaseID: "design", Generation: 1, SnapshotIDs: []string{"wrong"}}); e == nil {
		t.Fatal("accepted wrong version")
	}
	approve := Action{Kind: "approve", PhaseID: "design", Generation: 1, SnapshotIDs: phaseVersions(d.Run.Phases[0])}
	if _, e = s.Action(ctx, d.ID, "approve", approve); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Action(ctx, d.ID, "approve", approve); e != nil {
		t.Fatal(e)
	}
	tick()
	inputs, e := s.Store.TaskInputs(ctx, d.Run.Phases[1].Attempt.Units[0].TaskID)
	design, err := s.Store.Snapshot(ctx, d.Run.Phases[0].Attempt.Units[0].SnapshotID)
	if e != nil || err != nil || len(inputs) != len(design.Artifacts) || len(inputs) == 0 {
		t.Fatal("no exact handoff", inputs, e)
	}
	byID := map[string]store.ArtifactVersion{}
	for _, a := range inputs {
		byID[a.ID] = a
	}
	for _, a := range design.Artifacts {
		if got, ok := byID[a.ID]; !ok || got.Hash != a.Hash || got.Path != a.Path {
			t.Fatal("handoff changed an artifact version", a, got)
		}
	}
	finishJobs(t, s, false, "")
	tick()
	finishJobs(t, s, false, "")
	tick()
	if d.Run.Phases[2].Status != "awaiting_review" {
		t.Fatal(d)
	}
	_, e = s.Action(ctx, d.ID, "rollback", Action{Kind: "rollback", PhaseID: "develop", Generation: 1, Feedback: "revise"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Action(ctx, d.ID, "old", Action{Kind: "approve", PhaseID: "verify", Generation: 1, SnapshotIDs: phaseVersions(d.Run.Phases[2])}); e == nil {
		t.Fatal("old generation promoted")
	}
	tick()
	before, _ := s.Store.ListTasks(ctx)
	tick()
	after, _ := s.Store.ListTasks(ctx)
	if len(before) != len(after) {
		t.Fatal("duplicate dispatch")
	}
	if d.Run.Generation != 2 || d.Run.Phases[0].Status != "accepted" || len(d.History) != 1 {
		t.Fatal(d)
	}
}
func TestParallelConflictRetainedAndResolved(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	src := t.TempDir()
	g := func(dir string, args ...string) string {
		t.Helper()
		out, e := git(ctx, dir, args...)
		if e != nil {
			t.Fatal(args, e)
		}
		return out
	}
	g(src, "init")
	os.WriteFile(filepath.Join(src, "code.txt"), []byte("base\n"), 0600)
	g(src, "add", ".")
	g(src, "commit", "-m", "base")
	base := g(src, "rev-parse", "HEAD")
	template := flowmodel.Template{ID: "parallel", Version: 1, Phases: []flowmodel.PhaseTemplate{{ID: "code", Mode: "development", Units: []flowmodel.UnitTemplate{{ID: "a", Prompt: "a"}, {ID: "b", Prompt: "b"}}}}}
	d, e := s.Create(ctx, CreateInput{Goal: "parallel", Adapter: "codex", RepositoryPath: src, BaseCommit: base, Template: template}, "create")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	finishJobs(t, s, false, "changed ")
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	d, e = s.Store.Delivery(ctx, d.ID)
	if e != nil || d.Status != "blocked" {
		t.Fatal(d, e)
	}
	if got := g(src, "rev-parse", "HEAD"); got != base {
		t.Fatal("source changed")
	}
	os.WriteFile(filepath.Join(d.Worktree, "code.txt"), []byte("resolved\n"), 0600)
	g(d.Worktree, "add", ".")
	g(d.Worktree, "commit", "-m", "resolve both units")
	if _, e = s.Action(ctx, d.ID, "resume", Action{Kind: "resume", Generation: 1}); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	d, _ = s.Store.Delivery(ctx, d.ID)
	if d.Status != "completed" {
		t.Fatal(d)
	}
	d, e = s.Accept(ctx, d.ID, "accept", 1)
	if e != nil || d.Status != "accepted" {
		t.Fatal(d, e)
	}
	d, e = s.Accept(ctx, d.ID, "accept", 1)
	if e != nil || d.Status != "accepted" {
		t.Fatal(d, e)
	}
	b, _ := os.ReadFile(filepath.Join(src, "code.txt"))
	if string(b) != "resolved\n" {
		t.Fatal(string(b))
	}
}
func TestAutomaticReworkStopsAtThree(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	repo, base := flowFixtureRepo(t)
	d, e := s.Create(ctx, CreateInput{Goal: "rework", Adapter: "codex", RepositoryPath: repo, BaseCommit: base}, "create")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 30; i++ {
		if e = s.Tick(ctx); e != nil {
			t.Fatal(e)
		}
		d, _ = s.Store.Delivery(ctx, d.ID)
		if d.Status == "awaiting_rework" {
			break
		}
		for _, p := range d.Run.Phases {
			if p.Status == "awaiting_review" {
				if _, e = s.Action(ctx, d.ID, core.NewID("approve"), Action{Kind: "approve", PhaseID: p.ID, Generation: d.Run.Generation, SnapshotIDs: phaseVersions(p)}); e != nil {
					t.Fatal(e)
				}
			}
		}
		finishJobs(t, s, true, "")
	}
	d, _ = s.Store.Delivery(ctx, d.ID)
	if d.Status != "awaiting_rework" || d.Reworks != 3 {
		t.Fatal(d.Status, d.Reworks)
	}
}

func flowFixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{{"init"}, {"commit", "--allow-empty", "-m", "base"}} {
		if out, e := git(ctx, repo, args...); e != nil {
			t.Fatal(out, e)
		}
	}
	base, e := git(ctx, repo, "rev-parse", "HEAD")
	if e != nil {
		t.Fatal(e)
	}
	return repo, base
}

func TestDeletedUnitBlocksDeliveryWithoutRedispatch(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	repo, base := flowFixtureRepo(t)
	d, e := s.Create(ctx, CreateInput{Goal: "ship", Adapter: "codex", RepositoryPath: repo, BaseCommit: base}, "delete-unit")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	tasks, e := s.Store.ListTasks(ctx)
	if e != nil || len(tasks) == 0 {
		t.Fatal(tasks, e)
	}
	if _, e = s.Store.DeleteTask(ctx, tasks[0].ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	d, e = s.Store.Delivery(ctx, d.ID)
	if e != nil || d.Status != "blocked" {
		t.Fatal(d, e)
	}
	if j, e := s.Store.ClaimNext(ctx, "worker"); e != nil || j != nil {
		t.Fatal(j, e)
	}
}
