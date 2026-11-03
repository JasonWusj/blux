package artifact

import (
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectFilesPublishAfterCompletionAndPreserveExplicitArtifacts(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	data := t.TempDir()
	m := workspace.Manager{DataDir: data}
	os.WriteFile(filepath.Join(src, "baseline.md"), []byte("not a new output"), 0600)
	base, e := m.Freeze(ctx, src)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(data, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	group, _, e := db.CreateCollaboration(ctx, store.CreateCollaborationInput{Goal: "poems", RepositoryPath: src}, "group")
	if e != nil {
		t.Fatal(e)
	}
	svc := Service{Store: db, DataDir: data}
	for _, adapter := range []string{"codex", "claude"} {
		d, _, e := db.CreateTask(ctx, core.CreateTaskInput{Prompt: "write poem in current directory", Adapter: adapter, RepositoryPath: src, BaseCommit: base.BaseCommit, CollaborationID: group.ID, IdempotencyKey: adapter})
		if e != nil {
			t.Fatal(e)
		}
		j, e := db.ClaimNext(ctx, "test")
		if e != nil {
			t.Fatal(e)
		}
		rt, e := m.Prepare(ctx, *j)
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(rt.WorktreePath, "小诗.md"), []byte("# 诗\n"+adapter+"\n"), 0600)
		os.WriteFile(filepath.Join(rt.WorktreePath, " space\n.txt"), []byte("filename preserved"), 0600)
		os.WriteFile(filepath.Join(rt.WorktreePath, "image.png"), []byte{0, 1, 255, 0}, 0600)
		os.Symlink(filepath.Join(src, "baseline.md"), filepath.Join(rt.WorktreePath, "link.md"))
		os.Mkdir(filepath.Join(rt.WorkspacePath, "artifacts", "project"), 0700)
		os.WriteFile(filepath.Join(rt.WorkspacePath, "artifacts", "project", "小诗.md"), []byte("explicit output"), 0600)
		if e = svc.Capture(ctx, *j, rt); e != nil {
			t.Fatal(e)
		}
		versions, e := db.Snapshots(ctx, d.Task.ID)
		if e != nil || len(versions) != 1 {
			t.Fatal(versions, e)
		}
		v := versions[0]
		if len(v.Artifacts) != 4 {
			t.Fatal("missing project outputs", v.Artifacts)
		}
		seen := map[string]string{}
		for _, a := range v.Artifacts {
			b, e := os.ReadFile(svc.Blob(a.Hash))
			if e != nil {
				t.Fatal(e)
			}
			seen[a.Path] = string(b)
		}
		if seen["project/小诗.md"] != "explicit output" || seen["project-2/小诗.md"] != "# 诗\n"+adapter+"\n" || seen["project-2/ space\n.txt"] != "filename preserved" {
			t.Fatal(seen)
		}
		shared, e := db.SharedSnapshots(ctx, group.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range shared {
			if v.TaskID == d.Task.ID {
				t.Fatal("running files published")
			}
		}
		if _, _, e = db.AppendEvent(ctx, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.completed"}); e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(rt.WorktreePath, "小诗.md"), []byte("live modified"), 0600)
		if n, e := svc.BackfillProjectArtifacts(ctx, d.Task.ID); e != nil || n != 0 {
			t.Fatal("backfill duplicated outputs", n, e)
		}
		if _, e = os.Stat(filepath.Join(src, "小诗.md")); !os.IsNotExist(e) {
			t.Fatal("source modified")
		}
	}
	c, e := db.Collaboration(ctx, group.ID)
	if e != nil || c.ArtifactCount != 8 {
		t.Fatal(c.ArtifactCount, e)
	}
	shared, e := db.SharedSnapshots(ctx, group.ID)
	if e != nil || len(shared) != 2 {
		t.Fatal(shared, e)
	}
}

func TestBackfillUsesHistoricalCommitAndNeverReplacesVersions(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	data := t.TempDir()
	m := workspace.Manager{DataDir: data}
	base, e := m.Freeze(ctx, src)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(data, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	d, _, e := db.CreateTask(ctx, core.CreateTaskInput{Prompt: "poem", RepositoryPath: src, BaseCommit: base.BaseCommit, IdempotencyKey: "old"})
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.ClaimNext(ctx, "test")
	if e != nil {
		t.Fatal(e)
	}
	rt, e := m.Prepare(ctx, *j)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(rt.WorktreePath, "poem.md"), []byte("# Historical poem\n"), 0600)
	if _, e = git(ctx, rt.WorktreePath, "add", "-A"); e != nil {
		t.Fatal(e)
	}
	if _, e = git(ctx, rt.WorktreePath, "commit", "-m", "saved output"); e != nil {
		t.Fatal(e)
	}
	commit, e := git(ctx, rt.WorktreePath, "rev-parse", "HEAD")
	if e != nil {
		t.Fatal(e)
	}
	v := store.Snapshot{ID: core.NewID("snapshot"), TaskID: d.Task.ID, TurnID: j.TurnID, Generation: j.Generation, Commit: commit, Diff: "unchanged original diff", CreatedAt: core.Now()}
	if e = db.SaveSnapshot(ctx, v); e != nil {
		t.Fatal(e)
	}
	svc := Service{Store: db, DataDir: data}
	if n, e := svc.BackfillProjectArtifacts(ctx, d.Task.ID); e != nil || n != 0 {
		t.Fatal("running backfill", n, e)
	}
	if _, _, e = db.AppendEvent(ctx, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.completed"}); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(rt.WorktreePath, "poem.md"), []byte("wrong live content"), 0600)
	if n, e := svc.BackfillProjectArtifacts(ctx, d.Task.ID); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if n, e := svc.BackfillProjectArtifacts(ctx, d.Task.ID); e != nil || n != 0 {
		t.Fatal("duplicate backfill", n, e)
	}
	saved, e := db.Snapshot(ctx, v.ID)
	if e != nil || len(saved.Artifacts) != 1 || saved.Diff != v.Diff || saved.Commit != v.Commit {
		t.Fatal(saved, e)
	}
	b, e := os.ReadFile(svc.Blob(saved.Artifacts[0].Hash))
	if e != nil || !strings.Contains(string(b), "Historical poem") {
		t.Fatal(string(b), e)
	}
	altered := saved.Artifacts[0]
	altered.Hash = "different"
	if _, e = db.AddSnapshotArtifacts(ctx, v, []store.ArtifactVersion{altered}); e != core.ErrConflict {
		t.Fatal("overwrote immutable artifact", e)
	}
	if _, e = db.DeleteTask(ctx, d.Task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.BackfillProjectArtifacts(ctx, d.Task.ID); e != core.ErrNotFound {
		t.Fatal("deleted task repaired", e)
	}
}
