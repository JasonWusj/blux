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
	"testing"
	"time"
)

func TestConcurrentStopAndTimeout(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	base := strings.TrimSpace(string(out))
	var tasks []core.TaskDetail
	for _, key := range []string{"one", "two", "three"} {
		d, _, err := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: repo, BaseCommit: base, Prompt: key, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, d)
	}
	w := Worker{Store: db, ID: "test", DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Adapter: waitingAdapter{}, Concurrency: 2, Timeout: 800 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	stopped := false
	for time.Now().Before(deadline) {
		list, e := db.ListTasks(ctx)
		if e != nil {
			t.Fatal(e)
		}
		running := 0
		terminal := 0
		for _, d := range list {
			if d.Status == "running" {
				running++
			}
			if d.Status == "failed" {
				terminal++
			}
		}
		if running > 2 {
			t.Fatal("exceeded concurrency")
		}
		if running == 2 && !stopped {
			_, err := db.RequestControl(ctx, tasks[0].Task.ID, tasks[0].Task.CurrentTurnID, "stop", "stop", "")
			if err != nil {
				t.Fatal(err)
			}
			stopped = true
		}
		if terminal == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	d, e := db.GetTask(context.Background(), tasks[0].Task.ID)
	if e != nil || d.Turns[0].ErrorCode != "user_stopped" {
		t.Fatal(d, e)
	}
	d, e = db.GetTask(context.Background(), tasks[2].Task.ID)
	if e != nil || d.Turns[0].ErrorCode != "execution_timeout" {
		t.Fatal(d, e)
	}
}

// Test-only blocked session exercises real Worker cancellation and concurrency.
type waitingAdapter struct{}

func (waitingAdapter) Name() string                     { return "codex" }
func (waitingAdapter) Capabilities() agent.Capabilities { return agent.Capabilities{Interrupt: true} }
func (waitingAdapter) Start(context.Context, agent.SessionConfig) (agent.Session, error) {
	return waitingSession{}, nil
}
func (waitingAdapter) Resume(context.Context, agent.SessionConfig) (agent.Session, error) {
	return waitingSession{}, nil
}

type waitingSession struct{}

func (waitingSession) Send(ctx context.Context, _ agent.TurnRequest) (<-chan agent.Update, error) {
	out := make(chan agent.Update)
	go func() { defer close(out); <-ctx.Done() }()
	return out, nil
}
func (waitingSession) Steer(context.Context, string, string) error { return agent.ErrUnsupported }
func (waitingSession) Interrupt(context.Context, string) error     { return nil }
func (waitingSession) Close() error                                { return nil }
