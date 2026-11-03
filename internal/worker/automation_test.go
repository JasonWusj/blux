package worker

import (
	"blux/internal/agent"
	"blux/internal/automation"
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
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

func automationFixture(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	data := t.TempDir()
	db, err := store.Open(filepath.Join(data, "blux.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@local", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"}} {
		if out, e := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); e != nil {
			t.Fatal(string(out), e)
		}
	}
	return db, data, repo
}

// recordingAdapter finishes immediately and remembers how it was configured.
type recordingAdapter struct {
	mu      sync.Mutex
	configs []agent.SessionConfig
}

func (*recordingAdapter) Name() string                     { return "codex" }
func (*recordingAdapter) Capabilities() agent.Capabilities { return agent.Capabilities{Resume: true} }
func (a *recordingAdapter) Start(_ context.Context, c agent.SessionConfig) (agent.Session, error) {
	a.mu.Lock()
	a.configs = append(a.configs, c)
	a.mu.Unlock()
	return instructionSession{}, nil
}
func (a *recordingAdapter) Resume(ctx context.Context, c agent.SessionConfig) (agent.Session, error) {
	return a.Start(ctx, c)
}
func (a *recordingAdapter) seen() []agent.SessionConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]agent.SessionConfig(nil), a.configs...)
}

func waitFor(t *testing.T, what string, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole chain, in a real worker: an automation starts, the agent is
// configured for unattended work, the run is closed and the user notified, and
// a follow-up turn is configured as ordinary interactive work.
func TestWorkerRunsAnAutomationUnattendedThenAFollowUpInteractively(t *testing.T) {
	db, data, repo := automationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, _, err := db.CreateAutomation(ctx, store.AutomationInput{Name: "nightly", Prompt: "check the project", RepositoryPath: repo}, "create")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{}
	var notifyMu sync.Mutex
	var notices []string
	w := Worker{Store: db, ID: "w", DataDir: data, Adapter: adapter, Concurrency: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notify: func(title, body string) { notifyMu.Lock(); notices = append(notices, title); notifyMu.Unlock() }}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	run, err := (&automation.Service{Store: db, DataDir: data}).Start(ctx, a, automation.StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:go"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to finish", 15*time.Second, func() bool {
		got, e := db.AutomationRun(ctx, run.ID)
		return e == nil && got.Status == store.RunCompleted
	})
	waitFor(t, "the notification", 10*time.Second, func() bool { notifyMu.Lock(); defer notifyMu.Unlock(); return len(notices) == 1 })

	configs := adapter.seen()
	if len(configs) != 1 {
		t.Fatalf("expected one session, got %d", len(configs))
	}
	first := configs[0]
	if !first.RestrictNetwork {
		t.Fatal("an unattended run must restrict network access for commands by default")
	}
	for _, must := range []string{"unattended", "nightly", filepath.Join("artifacts", automation.QuestionFile), "without network access"} {
		if !strings.Contains(first.AdditionalInstructions, must) {
			t.Fatalf("the agent was not told %q:\n%s", must, first.AdditionalInstructions)
		}
	}

	// The user follows up: that turn is interactive, with the normal sandbox.
	detail, _, err := db.CreateTurn(ctx, run.TaskID, core.CreateTurnInput{Prompt: "also run the linter", IdempotencyKey: "follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the follow-up turn", 15*time.Second, func() bool {
		d, e := db.GetTask(ctx, run.TaskID)
		return e == nil && len(d.Turns) == 2 && d.Turns[1].Status == core.StatusCompleted && d.Turns[1].ID == detail.SubmittedTurnID
	})
	second := adapter.seen()[1]
	if second.RestrictNetwork || strings.Contains(second.AdditionalInstructions, "unattended") {
		t.Fatalf("a follow-up must not inherit the unattended rules: restrict=%v", second.RestrictNetwork)
	}
	// Closing the first turn was already final; the follow-up must not reopen it.
	if got, _ := db.AutomationRun(ctx, run.ID); got.Status != store.RunCompleted {
		t.Fatalf("run status changed to %s after a follow-up", got.Status)
	}

	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

// With the network switch on, the agent is configured with network access and
// told so.
func TestWorkerAllowsNetworkOnlyWhenTheAutomationAsksForIt(t *testing.T) {
	db, data, repo := automationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, _, err := db.CreateAutomation(ctx, store.AutomationInput{Name: "installer", Prompt: "install and test", RepositoryPath: repo, Network: store.NetworkOpen}, "create")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &recordingAdapter{}
	w := Worker{Store: db, ID: "w", DataDir: data, Adapter: adapter, Concurrency: 2, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	run, err := (&automation.Service{Store: db, DataDir: data}).Start(ctx, a, automation.StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:go"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to finish", 15*time.Second, func() bool {
		got, e := db.AutomationRun(ctx, run.ID)
		return e == nil && got.Status == store.RunCompleted
	})
	config := adapter.seen()[0]
	if config.RestrictNetwork || !strings.Contains(config.AdditionalInstructions, "allowed network access") {
		t.Fatalf("restrict=%v instructions:\n%s", config.RestrictNetwork, config.AdditionalInstructions)
	}
	cancel()
	<-done
}

// holdingAdapter keeps every session open until released, so the test can see
// how many automation runs the worker lets execute at the same time.
type holdingAdapter struct {
	mu      sync.Mutex
	running int
	peak    int
	release chan struct{}
}

func (*holdingAdapter) Name() string                     { return "codex" }
func (*holdingAdapter) Capabilities() agent.Capabilities { return agent.Capabilities{Interrupt: true} }
func (a *holdingAdapter) Start(context.Context, agent.SessionConfig) (agent.Session, error) {
	a.mu.Lock()
	a.running++
	a.peak = max(a.peak, a.running)
	a.mu.Unlock()
	return holdingSession{a}, nil
}
func (a *holdingAdapter) Resume(ctx context.Context, c agent.SessionConfig) (agent.Session, error) {
	return a.Start(ctx, c)
}

type holdingSession struct{ a *holdingAdapter }

func (s holdingSession) Send(ctx context.Context, _ agent.TurnRequest) (<-chan agent.Update, error) {
	out := make(chan agent.Update)
	go func() {
		defer close(out)
		select {
		case <-s.a.release:
		case <-ctx.Done():
		}
	}()
	return out, nil
}
func (s holdingSession) Steer(context.Context, string, string) error { return agent.ErrUnsupported }
func (s holdingSession) Interrupt(context.Context, string) error     { return nil }
func (s holdingSession) Close() error {
	s.a.mu.Lock()
	s.a.running--
	s.a.mu.Unlock()
	return nil
}

// Three worker slots, one background slot: of two automations only one runs, and
// an interactive task still gets a slot while the automation is busy.
func TestWorkerKeepsASlotFreeForInteractiveWorkWhileAutomationsRun(t *testing.T) {
	db, data, repo := automationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := &holdingAdapter{release: make(chan struct{})}
	w := Worker{Store: db, ID: "w", DataDir: data, Adapter: adapter, Concurrency: 3, BackgroundConcurrency: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	svc := &automation.Service{Store: db, DataDir: data}
	for _, name := range []string{"first", "second"} {
		a, _, err := db.CreateAutomation(ctx, store.AutomationInput{Name: name, Prompt: name, RepositoryPath: repo}, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.Start(ctx, a, automation.StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:go"}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "an automation to be running", 10*time.Second, func() bool { adapter.mu.Lock(); defer adapter.mu.Unlock(); return adapter.running == 1 })

	// An interactive task arrives while one automation runs and one waits.
	repoInfo, err := (workspace.Manager{DataDir: data}).Freeze(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.CreateTask(ctx, core.CreateTaskInput{Prompt: "interactive", Adapter: "codex", RepositoryPath: repo, BaseCommit: repoInfo.BaseCommit, IdempotencyKey: "interactive"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the interactive task to start beside the automation", 10*time.Second, func() bool { adapter.mu.Lock(); defer adapter.mu.Unlock(); return adapter.running == 2 })
	time.Sleep(400 * time.Millisecond) // give a wrongly admitted second automation time to show up
	adapter.mu.Lock()
	running, peak := adapter.running, adapter.peak
	adapter.mu.Unlock()
	if running != 2 || peak > 2 {
		t.Fatalf("expected one automation and one interactive task running, got running=%d peak=%d", running, peak)
	}
	queued, err := db.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waiting := 0
	for _, task := range queued {
		if task.Status == core.StatusQueued {
			waiting++
		}
	}
	if waiting != 1 {
		t.Fatalf("the second automation should still be waiting, queued tasks = %d", waiting)
	}
	close(adapter.release)
	cancel()
	<-done
}

// An automation's own time limit replaces the worker-wide one, but only for the
// unattended turn; everything else keeps the worker's limit or the 2 hour default.
func TestTurnTimeoutPrefersTheAutomationLimitOnlyForUnattendedTurns(t *testing.T) {
	rules := store.AutomationRules{TimeoutSeconds: 600}
	for _, test := range []struct {
		name       string
		worker     time.Duration
		rules      store.AutomationRules
		unattended bool
		want       time.Duration
	}{
		{"automation limit wins over a long worker limit", 6 * time.Hour, rules, true, 10 * time.Minute},
		{"automation limit wins over the default", 0, rules, true, 10 * time.Minute},
		{"a follow-up keeps the worker limit", 6 * time.Hour, rules, false, 6 * time.Hour},
		{"an ordinary task keeps the default", 0, store.AutomationRules{}, false, 2 * time.Hour},
		{"an unattended turn without a limit falls back", 3 * time.Hour, store.AutomationRules{}, true, 3 * time.Hour},
		{"a negative worker limit is replaced by the default", -1, store.AutomationRules{}, false, 2 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := turnTimeout(test.worker, test.rules, test.unattended); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

// The limit reaches the worker through the run's frozen rules and not through
// the automation, so editing the automation mid-run cannot extend a running turn.
func TestWorkerReadsTheTimeLimitFromTheFrozenRun(t *testing.T) {
	db, data, repo := automationFixture(t)
	ctx := context.Background()
	a, _, err := db.CreateAutomation(ctx, store.AutomationInput{Name: "short", Prompt: "slow", RepositoryPath: repo, TimeoutSeconds: 90}, "create")
	if err != nil {
		t.Fatal(err)
	}
	run, err := (&automation.Service{Store: db, DataDir: data}).Start(ctx, a, automation.StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateAutomation(ctx, a.ID, store.AutomationInput{Name: "short", Prompt: "slow", Adapter: "codex", TimeoutSeconds: 14400}); err != nil {
		t.Fatal(err)
	}
	rules, ok, err := db.AutomationRulesForTurn(ctx, run.TurnID)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := turnTimeout(2*time.Hour, rules, ok); got != 90*time.Second {
		t.Fatalf("the running turn would get %v after the automation was edited", got)
	}
}
