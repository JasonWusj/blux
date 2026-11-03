package backup

import (
	"archive/zip"
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineExportContainsConsistentDatabase(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	db, e := store.Open(filepath.Join(data, "blux.db"))
	if e != nil {
		t.Fatal(e)
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Blux Test", "-c", "user.email=test@local", "commit", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(string(out), e)
		}
	}
	out, e := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if e != nil {
		t.Fatal(e)
	}
	task, _, e := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: repo, BaseCommit: strings.TrimSpace(string(out)), Prompt: "persistent", IdempotencyKey: "one"})
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	path := filepath.Join(t.TempDir(), "backup.zip")
	if e = Export(ctx, data, path); e != nil {
		t.Fatal(e)
	}
	z, e := zip.OpenReader(path)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name != "data/blux.db" {
			continue
		}
		in, _ := f.Open()
		b, _ := io.ReadAll(in)
		in.Close()
		restore := filepath.Join(t.TempDir(), "restored.db")
		os.WriteFile(restore, b, 0600)
		copy, e := store.Open(restore)
		if e != nil {
			t.Fatal(e)
		}
		got, e := copy.GetTask(ctx, task.Task.ID)
		copy.Close()
		if e != nil || got.Task.Prompt != "persistent" {
			t.Fatal(got, e)
		}
		found = true
	}
	if !found {
		t.Fatal("no database")
	}
	if e = Export(ctx, data, path); e == nil {
		t.Fatal("overwrote existing backup")
	}
}

func TestDirectoryProjectExportIncludesPrivateBaseline(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "file.txt"), []byte("base"), 0600)
	m := workspace.Manager{DataDir: data}
	base, e := m.Freeze(ctx, src)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(data, "blux.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = db.CreateTask(ctx, core.CreateTaskInput{Prompt: "directory", RepositoryPath: src, BaseCommit: base.BaseCommit, IdempotencyKey: "directory"})
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "backup.zip")
	if e = Export(ctx, data, target); e != nil {
		t.Fatal(e)
	}
	z, e := zip.OpenReader(target)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	rel, _ := filepath.Rel(data, base.Root)
	want := "data/" + filepath.ToSlash(rel) + "/HEAD"
	for _, f := range z.File {
		if f.Name == want {
			return
		}
	}
	t.Fatal("private baseline missing from backup")
}
