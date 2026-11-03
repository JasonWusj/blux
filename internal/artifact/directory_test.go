package artifact

import (
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySnapshotCaptureAndAcceptance(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "accept", true: "conflict"}[conflict], func(t *testing.T) {
			ctx := context.Background()
			src := t.TempDir()
			data := t.TempDir()
			m := workspace.Manager{DataDir: data}
			os.WriteFile(filepath.Join(src, "code.txt"), []byte("base\n"), 0600)
			base, e := m.Freeze(ctx, src)
			if e != nil {
				t.Fatal(e)
			}
			db, e := store.Open(filepath.Join(data, "blux.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			d, _, e := db.CreateTask(ctx, core.CreateTaskInput{Prompt: "edit", RepositoryPath: src, BaseCommit: base.BaseCommit, IdempotencyKey: "create"})
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
			os.WriteFile(filepath.Join(rt.WorktreePath, "code.txt"), []byte("Agent\n"), 0600)
			svc := Service{Store: db, DataDir: data}
			if e = svc.Capture(ctx, *j, rt); e != nil {
				t.Fatal(e)
			}
			if _, _, e = db.AppendEvent(ctx, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.completed"}); e != nil {
				t.Fatal(e)
			}
			versions, e := db.Snapshots(ctx, d.Task.ID)
			if e != nil || len(versions) != 1 {
				t.Fatal(versions, e)
			}
			source, _ := os.ReadFile(filepath.Join(src, "code.txt"))
			if string(source) != "base\n" {
				t.Fatal("source changed before acceptance")
			}
			want := "accepted"
			if conflict {
				want = "blocked"
				os.WriteFile(filepath.Join(src, "code.txt"), []byte("local\n"), 0600)
			}
			a, e := svc.Accept(ctx, versions[0].ID, "accept")
			if e != nil || a.Status != want {
				t.Fatal(a, e)
			}
			again, e := svc.Accept(ctx, versions[0].ID, "accept")
			if e != nil || again.Status != want {
				t.Fatal(again, e)
			}
			if _, e = os.Stat(filepath.Join(src, ".git")); !os.IsNotExist(e) {
				t.Fatal("source Git initialized")
			}
			got, _ := os.ReadFile(filepath.Join(src, "code.txt"))
			expected := "Agent\n"
			if conflict {
				expected = "local\n"
			}
			if string(got) != expected {
				t.Fatal(string(got))
			}
		})
	}
}
