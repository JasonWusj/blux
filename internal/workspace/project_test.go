package workspace

import (
	"blux/internal/core"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryBaselinesAndAcceptance(t *testing.T) {
	for _, kind := range []string{"empty", "plain", "unborn", "dirty", "subdirectory"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			src := t.TempDir()
			m := Manager{DataDir: t.TempDir()}
			write := func(dir, path, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			g := func(dir string, args ...string) string {
				t.Helper()
				v, e := git(ctx, dir, args...)
				if e != nil {
					t.Fatal(args, e)
				}
				return v
			}
			if kind == "dirty" {
				src = repository(t)
			}
			if kind == "subdirectory" {
				parent := repository(t)
				src = filepath.Join(parent, "nested")
				if err := os.Mkdir(src, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "empty" {
				write(src, "hello.txt", "baseline\n")
			}
			if kind == "unborn" {
				g(src, "init")
				g(src, "add", "hello.txt")
			}
			beforeStatus := ""
			if kind == "unborn" || kind == "dirty" {
				beforeStatus = g(src, "status", "--porcelain=v1")
			}
			info, err := Inspect(ctx, src, false)
			if err != nil {
				t.Fatal(err)
			}
			if (kind == "empty" || kind == "plain") && info.Branch != "" {
				t.Fatal(info)
			}
			if kind == "unborn" && info.Branch == "" {
				t.Fatal("unborn branch missing")
			}
			base, err := m.Freeze(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			if !m.IsSnapshot(src, base.Root) || base.BaseCommit == "" {
				t.Fatal(base)
			}
			if kind == "unborn" || kind == "dirty" {
				if got := g(src, "status", "--porcelain=v1"); got != beforeStatus {
					t.Fatal("source index changed", got)
				}
			} else if kind != "subdirectory" {
				if _, err = os.Stat(filepath.Join(src, ".git")); !os.IsNotExist(err) {
					t.Fatal("source initialized")
				}
			}
			write(src, "later.txt", "created while queued\n")
			j := core.Job{TaskID: "task_directory", RepositoryPath: src, BaseCommit: base.BaseCommit}
			rt, err := m.Prepare(ctx, j)
			if err != nil {
				t.Fatal(err)
			}
			if _, e := os.Stat(filepath.Join(rt.WorktreePath, "later.txt")); !os.IsNotExist(e) {
				t.Fatal("baseline moved while queued")
			}
			if kind != "empty" {
				b, e := os.ReadFile(filepath.Join(rt.WorktreePath, "hello.txt"))
				if e != nil || string(b) != "baseline\n" {
					t.Fatal("baseline files missing", string(b), e)
				}
			}
			write(rt.WorktreePath, "hello.txt", "Agent result\n")
			if _, e := git(ctx, rt.WorktreePath, "add", "-A"); e != nil {
				t.Fatal(e)
			}
			if _, e := git(ctx, rt.WorktreePath, "-c", "user.name=Blux Test", "-c", "user.email=blux@localhost", "-c", "commit.gpgsign=false", "commit", "-m", "task result"); e != nil {
				t.Fatal(e)
			}
			head := g(rt.WorktreePath, "rev-parse", "HEAD")
			if err = m.ApplySnapshot(ctx, src, base.BaseCommit, head, false); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(filepath.Join(src, "hello.txt"))
			if string(got) != "Agent result\n" {
				t.Fatal("not applied", string(got))
			}
			if err = m.ApplySnapshot(ctx, src, base.BaseCommit, head, true); err != nil {
				t.Fatal("recovery", err)
			}
			j.Runtime = rt
			if _, err = m.Prepare(ctx, j); err != nil {
				t.Fatal("resume", err)
			}
		})
	}
}

func TestDirectoryPatchRejectsConflictsAndSymlinks(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "symlink"}[symlink], func(t *testing.T) {
			ctx := context.Background()
			src := t.TempDir()
			m := Manager{DataDir: t.TempDir()}
			os.Mkdir(filepath.Join(src, "sub"), 0700)
			os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("base\n"), 0600)
			base, err := m.Freeze(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := m.Prepare(ctx, core.Job{TaskID: "task_conflict", RepositoryPath: src, BaseCommit: base.BaseCommit})
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(rt.WorktreePath, "sub", "a.txt"), []byte("Agent\n"), 0600)
			os.WriteFile(filepath.Join(rt.WorktreePath, "new.txt"), []byte("new\n"), 0600)
			if _, err = git(ctx, rt.WorktreePath, "add", "-A"); err != nil {
				t.Fatal(err)
			}
			if _, err = git(ctx, rt.WorktreePath, "-c", "user.name=Blux Test", "-c", "user.email=blux@localhost", "-c", "commit.gpgsign=false", "commit", "-m", "result"); err != nil {
				t.Fatal(err)
			}
			head, err := git(ctx, rt.WorktreePath, "rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			target := src
			if symlink {
				target = t.TempDir()
				os.Mkdir(filepath.Join(target, "sub"), 0700)
				os.WriteFile(filepath.Join(target, "sub", "a.txt"), []byte("base\n"), 0600)
				os.RemoveAll(filepath.Join(src, "sub"))
				if err = os.Symlink(filepath.Join(target, "sub"), filepath.Join(src, "sub")); err != nil {
					t.Skip(err)
				}
			} else {
				os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("local\n"), 0600)
			}
			if err = m.ApplySnapshot(ctx, src, base.BaseCommit, head, false); err == nil {
				t.Fatal("accepted changed source")
			}
			if _, e := os.Stat(filepath.Join(src, "new.txt")); !os.IsNotExist(e) {
				t.Fatal("partially applied conflicting patch")
			}
			if symlink {
				b, _ := os.ReadFile(filepath.Join(target, "sub", "a.txt"))
				if string(b) != "base\n" {
					t.Fatal("wrote outside source")
				}
			}
		})
	}
}

func TestSnapshotKeepsIgnoredTrackedFilesAndExcludesOwnData(t *testing.T) {
	ctx := context.Background()
	src := repository(t)
	data := filepath.Join(src, ".blux-data")
	m := Manager{DataDir: data}
	os.WriteFile(filepath.Join(src, ".gitignore"), []byte("*.cfg\n"), 0600)
	os.WriteFile(filepath.Join(src, " tracked.cfg"), []byte("current content\n"), 0600)
	if _, e := git(ctx, src, "add", "-f", "--", " tracked.cfg"); e != nil {
		t.Fatal(e)
	}
	indexBefore, e := os.ReadFile(filepath.Join(src, ".git", "index"))
	if e != nil {
		t.Fatal(e)
	}
	base, e := m.Freeze(ctx, src)
	if e != nil {
		t.Fatal(e)
	}
	rt, e := m.Prepare(ctx, core.Job{TaskID: "task_ignored", RepositoryPath: src, BaseCommit: base.BaseCommit})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(rt.WorktreePath, " tracked.cfg"))
	if e != nil || string(b) != "current content\n" {
		t.Fatal("tracked ignored file missing", e)
	}
	indexAfter, e := os.ReadFile(filepath.Join(src, ".git", "index"))
	if e != nil || string(indexAfter) != string(indexBefore) {
		t.Fatal("source index changed", e)
	}
	if _, e = os.Stat(filepath.Join(rt.WorktreePath, ".blux-data")); !os.IsNotExist(e) {
		t.Fatal("Blux data included in baseline")
	}
}
