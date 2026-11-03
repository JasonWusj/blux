package worker

import (
	"blux/internal/agent"
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type deletionAdapter struct {
	started    chan *deletionSession
	blockStart bool
}

func (*deletionAdapter) Name() string                     { return "codex" }
func (*deletionAdapter) Capabilities() agent.Capabilities { return agent.Capabilities{} }
func (a *deletionAdapter) Start(ctx context.Context, _ agent.SessionConfig) (agent.Session, error) {
	s := &deletionSession{closed: make(chan struct{})}
	a.started <- s
	if a.blockStart {
		<-ctx.Done()
		s.Close()
		return nil, ctx.Err()
	}
	return s, nil
}
func (a *deletionAdapter) Resume(ctx context.Context, c agent.SessionConfig) (agent.Session, error) {
	return a.Start(ctx, c)
}

type deletionSession struct {
	waitingSession
	closed chan struct{}
	once   sync.Once
}

func (s *deletionSession) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

func TestDeleteCancelsStartupAndStreamingWithoutStoppingWorker(t *testing.T) {
	for _, startup := range []bool{true, false} {
		t.Run(map[bool]string{true: "startup", false: "streaming"}[startup], func(t *testing.T) {
			data := t.TempDir()
			db, e := store.Open(filepath.Join(data, "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			repo := t.TempDir()
			for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@local", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"}} {
				if b, e := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); e != nil {
					t.Fatal(string(b), e)
				}
			}
			base, e := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &deletionAdapter{started: make(chan *deletionSession, 2), blockStart: startup}
			w := Worker{Store: db, ID: "delete-test", DataDir: data, Adapter: a, Concurrency: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			for _, key := range []string{"first", "second"} {
				d, _, e := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: repo, BaseCommit: strings.TrimSpace(string(base)), Prompt: key, IdempotencyKey: key})
				if e != nil {
					t.Fatal(e)
				}
				var session *deletionSession
				select {
				case session = <-a.started:
				case <-time.After(4 * time.Second):
					t.Fatal("worker did not start the next task")
				}
				if _, e = db.DeleteTask(ctx, d.Task.ID); e != nil {
					t.Fatal(e)
				}
				select {
				case <-session.closed:
				case <-time.After(3 * time.Second):
					t.Fatal("deleted task execution was not closed")
				}
			}
			cancel()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("worker did not stop")
			}
		})
	}
}
