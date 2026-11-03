package workspace

import (
	"blux/internal/core"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	g("init")
	g("config", "user.name", "Blux Test")
	g("config", "user.email", "blux@localhost")
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("base\n"), 0600)
	g("add", ".")
	g("commit", "-m", "base")
	return dir
}
func TestIsolatedFrozenWorktree(t *testing.T) {
	ctx := context.Background()
	src := repository(t)
	r, err := Inspect(ctx, src, true)
	if err != nil {
		t.Fatal(err)
	}
	m := Manager{DataDir: t.TempDir()}
	j := core.Job{TaskID: "task_isolation", Adapter: "codex", RepositoryPath: src, BaseCommit: r.BaseCommit}
	rt, err := m.Prepare(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(rt.WorktreePath, "hello.txt"), []byte("changed\n"), 0600)
	original, _ := os.ReadFile(filepath.Join(src, "hello.txt"))
	if string(original) != "base\n" {
		t.Fatal("source changed")
	}
	j.Runtime = rt
	again, err := m.Prepare(ctx, j)
	if err != nil || again.WorktreePath != rt.WorktreePath {
		t.Fatalf("resume %v", err)
	}
	changed, _ := os.ReadFile(filepath.Join(rt.WorktreePath, "hello.txt"))
	if string(changed) != "changed\n" {
		t.Fatal("resume erased changes")
	}
	os.WriteFile(filepath.Join(src, "untracked"), []byte("x"), 0600)
	_, err = Inspect(ctx, src, true)
	var e *core.ExecutionError
	if !errors.As(err, &e) || e.Code != "repository_dirty" {
		t.Fatalf("dirty: %v", err)
	}
}
func TestRejectMissingBaselineAndUnsafeIdentity(t *testing.T) {
	m := Manager{DataDir: t.TempDir()}
	for _, j := range []core.Job{{TaskID: "../../elsewhere"}, {TaskID: "task_x", Adapter: "codex", BaseCommit: "HEAD"}} {
		if _, err := m.Prepare(context.Background(), j); err == nil {
			t.Fatal("accepted invalid workspace")
		}
	}
}
