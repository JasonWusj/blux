package automation

import (
	"blux/internal/artifact"
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// harness wires a real store, a real git project, and a clock the test moves.
type harness struct {
	t      *testing.T
	ctx    context.Context
	db     *store.Store
	data   string
	dbPath string
	svc    *Service
	repo   string
	clock  time.Time
	sent   []string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Blux Test", "-c", "user.email=test@local", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	data := t.TempDir()
	dbPath := filepath.Join(data, "blux.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo := t.TempDir()
	git(t, repo, "init")
	if err = os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "base")
	h := &harness{t: t, ctx: context.Background(), db: db, data: data, dbPath: dbPath, repo: repo}
	// Automations are anchored at the real creation time, so the test clock runs
	// two days ahead; every slot it then sees is after the anchor.
	h.clock = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for h.clock.Before(time.Now().Add(48 * time.Hour)) {
		h.clock = h.clock.AddDate(0, 0, 30)
	}
	h.svc = h.newService()
	return h
}

func (h *harness) newService() *Service {
	return &Service{Store: h.db, DataDir: h.data, Now: func() time.Time { return h.clock }, Location: time.UTC,
		Notify: func(title, body string) { h.sent = append(h.sent, title+"|"+body) }}
}

func (h *harness) create(input store.AutomationInput) store.Automation {
	h.t.Helper()
	if input.RepositoryPath == "" {
		input.RepositoryPath = h.repo
	}
	if input.Prompt == "" {
		input.Prompt = "check the project"
	}
	a, _, err := h.db.CreateAutomation(h.ctx, input, core.NewID("key"))
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

func (h *harness) tick() {
	h.t.Helper()
	if err := h.svc.Tick(h.ctx); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) runs(a store.Automation) []store.AutomationRun {
	h.t.Helper()
	runs, err := h.db.AutomationRuns(h.ctx, a.ID, 50)
	if err != nil {
		h.t.Fatal(err)
	}
	return runs
}

func (h *harness) start(a store.Automation, key string) store.AutomationRun {
	h.t.Helper()
	run, err := h.svc.Start(h.ctx, a, StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:" + key})
	if err != nil {
		h.t.Fatal(err)
	}
	return run
}

// work claims the next job like the worker would and returns what it needs to
// finish the turn.
type work struct {
	job *core.Job
	rt  core.RuntimeInfo
}

func (h *harness) claim() work {
	h.t.Helper()
	job, err := h.db.ClaimNext(h.ctx, "test-worker")
	if err != nil || job == nil {
		h.t.Fatalf("no job to claim: %v %v", job, err)
	}
	rt, err := (workspace.Manager{DataDir: h.svc.DataDir}).Prepare(h.ctx, *job)
	if err != nil {
		h.t.Fatal(err)
	}
	return work{job: job, rt: rt}
}

func (h *harness) event(w work, kind string, payload any) {
	h.t.Helper()
	raw, _ := json.Marshal(payload)
	if _, _, err := h.db.AppendEvent(h.ctx, core.EventInput{TaskID: w.job.TaskID, SessionID: w.job.SessionID, TurnID: w.job.TurnID, Generation: w.job.Generation, Type: kind, Payload: raw}); err != nil {
		h.t.Fatal(err)
	}
}

// succeed finishes a turn as an agent would: it writes files, says something,
// captures the snapshot, and completes.
func (h *harness) succeed(w work, say string, files map[string]string) {
	h.t.Helper()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(w.rt.WorkspacePath, "artifacts", name), []byte(text), 0600); err != nil {
			h.t.Fatal(err)
		}
	}
	h.event(w, "message.snapshot", map[string]string{"message_id": "msg_" + w.job.TurnID, "item_id": "item-1", "text": say})
	if err := (artifact.Service{Store: h.db, DataDir: h.svc.DataDir}).Capture(h.ctx, *w.job, w.rt); err != nil {
		h.t.Fatal(err)
	}
	h.event(w, "turn.completed", map[string]string{})
}

func (h *harness) fail(w work, code, message string) {
	h.t.Helper()
	h.event(w, "turn.failed", map[string]string{"code": code, "message": message})
}

func status(t *testing.T, db *store.Store, id string) string {
	t.Helper()
	run, err := db.AutomationRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run.Status
}

func TestScheduledSlotStartsExactlyOnceEvenAcrossRestarts(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily", ScheduleTime: "09:00"})
	h.tick()
	runs := h.runs(a)
	if len(runs) != 1 || runs[0].Trigger != store.TriggerSchedule || runs[0].Status != store.RunActive || runs[0].TaskID == "" || runs[0].PlannedAt == "" {
		t.Fatalf("first tick: %+v", runs)
	}
	h.tick()
	h.svc = h.newService() // a restarted worker
	h.tick()
	h.tick()
	if got := h.runs(a); len(got) != 1 || got[0].ID != runs[0].ID {
		t.Fatalf("slot started again: %+v", got)
	}
	tasks, err := h.db.ListTasks(h.ctx)
	if err != nil || len(tasks) != 1 || tasks[0].AutomationID != a.ID {
		t.Fatalf("expected one task tagged with the automation: %+v %v", tasks, err)
	}
}

// After days offline only the newest missed slot runs, and it runs once.
func TestCatchUpRunsOnlyTheLatestMissedSlot(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily", ScheduleTime: "09:00"})
	h.clock = h.clock.AddDate(0, 0, 5) // five slots passed while nothing ran
	h.tick()
	h.tick()
	runs := h.runs(a)
	if len(runs) != 1 {
		t.Fatalf("catch-up started %d runs: %+v", len(runs), runs)
	}
	latest, _ := LatestSlot("09:00", h.clock, time.UTC)
	if _, found, _ := h.db.AutomationRunByKey(h.ctx, SlotKey(a.ID, latest)); !found {
		t.Fatal("the latest slot was not the one that ran")
	}
	if _, found, _ := h.db.AutomationRunByKey(h.ctx, SlotKey(a.ID, latest.AddDate(0, 0, -1))); found {
		t.Fatal("an older slot was replayed")
	}
}

func TestPausedAutomationDoesNotRunAndResumeDoesNotReplayTheGap(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily", ScheduleTime: "09:00"})
	if _, err := h.db.SetAutomationPaused(h.ctx, a.ID, true, "manual"); err != nil {
		t.Fatal(err)
	}
	h.clock = h.clock.AddDate(0, 0, 3)
	h.tick()
	if len(h.runs(a)) != 0 {
		t.Fatal("a paused automation ran")
	}
	// Resume moves the anchor to the real now, which is before the test clock, so
	// the already-passed slot of today is still due: that is the one allowed run.
	if _, err := h.db.SetAutomationPaused(h.ctx, a.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	h.tick()
	h.tick()
	if got := len(h.runs(a)); got != 1 {
		t.Fatalf("resume started %d runs, want 1 for the newest slot", got)
	}
}

func TestChangingTheScheduleDoesNotRunAPassedSlot(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily", ScheduleTime: "09:00"})
	// Move the clock so a new 08:00 slot is long past, then edit to that time.
	h.clock = h.clock.AddDate(0, 0, 3)
	updated, err := h.db.UpdateAutomation(h.ctx, a.ID, store.AutomationInput{Name: "daily", Prompt: a.Prompt, Adapter: a.Adapter, ScheduleTime: "08:00", Network: a.Network})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AnchorAt == a.AnchorAt {
		t.Fatal("schedule change kept the old anchor")
	}
	// The anchor is the real now; the test clock is days ahead, so a slot exists
	// after it. Pin the clock to just before the anchor to assert no catch-up.
	anchor, _ := time.Parse(time.RFC3339Nano, updated.AnchorAt)
	h.clock = anchor.Add(-time.Minute)
	h.tick()
	if len(h.runs(a)) != 0 {
		t.Fatal("editing the time triggered a run for a slot that had already passed")
	}
	// Editing something other than the time keeps the anchor.
	same, err := h.db.UpdateAutomation(h.ctx, a.ID, store.AutomationInput{Name: "renamed", Prompt: a.Prompt, Adapter: a.Adapter, ScheduleTime: "08:00", Network: a.Network})
	if err != nil || same.AnchorAt != updated.AnchorAt {
		t.Fatal("a non-schedule edit moved the anchor", err)
	}
}

func TestRunFinishesAsCompletedWithSummaryAndNotifiesOnce(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "report"})
	run := h.start(a, "k1")
	if run.Status != store.RunActive || run.TurnID == "" {
		t.Fatalf("%+v", run)
	}
	w := h.claim()
	h.tick()
	if status(t, h.db, run.ID) != store.RunActive {
		t.Fatal("run finished while its turn was still running")
	}
	h.succeed(w, "All 12 checks passed.\n\nNothing to fix.", map[string]string{"Report.md": "# ok"})
	h.tick()
	h.tick()
	h.tick()
	got, err := h.db.AutomationRun(h.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.RunCompleted || got.Summary != "All 12 checks passed. Nothing to fix." || got.FinishedAt == "" {
		t.Fatalf("%+v", got)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "自动化完成") || !strings.Contains(h.sent[0], "report") {
		t.Fatalf("notifications: %q", h.sent)
	}
	unread, _ := h.db.UnreadAutomationRuns(h.ctx)
	if unread != 1 {
		t.Fatalf("unread = %d", unread)
	}
	if err = h.db.MarkAutomationRunSeen(h.ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if unread, _ = h.db.UnreadAutomationRuns(h.ctx); unread != 0 {
		t.Fatalf("unread after seen = %d", unread)
	}
}

// A finished run must leave the user's own project exactly as it was.
func TestARunNeverChangesTheSourceProject(t *testing.T) {
	h := newHarness(t)
	headBefore := git(t, h.repo, "rev-parse", "HEAD")
	a := h.create(store.AutomationInput{Name: "edit things"})
	run := h.start(a, "k1")
	w := h.claim()
	if w.rt.WorktreePath == "" {
		t.Fatal("no isolated worktree")
	}
	if err := os.WriteFile(filepath.Join(w.rt.WorktreePath, "hello.txt"), []byte("changed by the agent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	h.succeed(w, "edited hello.txt", nil)
	h.tick()
	if status(t, h.db, run.ID) != store.RunCompleted {
		t.Fatal("run did not complete")
	}
	if got := git(t, h.repo, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("source HEAD moved: %s -> %s", headBefore, got)
	}
	if out := git(t, h.repo, "status", "--porcelain"); out != "" {
		t.Fatalf("source working tree changed:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(h.repo, "hello.txt")); string(data) != "source\n" {
		t.Fatalf("source file content changed: %q", data)
	}
}

func TestQuestionFileTurnsACompletedRunIntoNeedsDecision(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "migrate"})
	run := h.start(a, "k1")
	w := h.claim()
	h.succeed(w, "I stopped.", map[string]string{QuestionFile: "Which database should I migrate first?\n\n- users\n- orders"})
	h.tick()
	got, _ := h.db.AutomationRun(h.ctx, run.ID)
	if got.Status != store.RunNeedsDecision || !strings.Contains(got.Summary, "Which database") {
		t.Fatalf("%+v", got)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "需要你的决定") {
		t.Fatalf("notifications: %q", h.sent)
	}
	// A needs-decision run is not a failure.
	if streak := FailureStreak([]store.AutomationRun{got}); streak != 0 {
		t.Fatal(streak)
	}
}

func TestLoginFailureKeepsItsSpecificCodeAndMessage(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	run := h.start(a, "k1")
	h.fail(h.claim(), "codex_auth_required", "Codex 凭据不可用，请先在现有 Codex CLI 中完成配置。")
	h.tick()
	got, _ := h.db.AutomationRun(h.ctx, run.ID)
	if got.Status != store.RunFailed || got.Code != "codex_auth_required" || !strings.Contains(got.Message, "凭据不可用") {
		t.Fatalf("%+v", got)
	}
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "自动化失败") || !strings.Contains(h.sent[0], "凭据不可用") {
		t.Fatalf("notifications: %q", h.sent)
	}
}

func TestThreeConsecutiveFailuresPauseAndPausedAutomationStopsScheduling(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "flaky"})
	for i := 1; i <= 3; i++ {
		h.start(a, string(rune('a'+i)))
		h.fail(h.claim(), "execution_failed", "boom "+string(rune('0'+i)))
		h.tick()
		current, _ := h.db.Automation(h.ctx, a.ID)
		if wantPaused := i == 3; current.Paused != wantPaused {
			t.Fatalf("after %d failures paused=%v", i, current.Paused)
		}
	}
	paused, _ := h.db.Automation(h.ctx, a.ID)
	if !strings.Contains(paused.PauseReason, "连续 3 次") || !strings.Contains(paused.PauseReason, "boom 3") {
		t.Fatalf("pause reason: %q", paused.PauseReason)
	}
	if last := h.sent[len(h.sent)-1]; !strings.Contains(last, "已暂停") {
		t.Fatalf("the final notification should mention the pause: %q", last)
	}
	// Give the paused automation a schedule: it must still not run.
	if _, err := h.db.UpdateAutomation(h.ctx, a.ID, store.AutomationInput{Name: a.Name, Prompt: a.Prompt, Adapter: a.Adapter, ScheduleTime: "09:00", Network: a.Network}); err != nil {
		t.Fatal(err)
	}
	before := len(h.runs(a))
	h.clock = h.clock.AddDate(0, 0, 2)
	h.tick()
	if len(h.runs(a)) != before {
		t.Fatal("a paused automation scheduled a run")
	}
}

func TestInterruptionsNeverPauseAnAutomation(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	for i, code := range []string{"user_stopped", "recovery_required", "worker_stopped", "user_stopped"} {
		h.start(a, string(rune('a'+i)))
		h.fail(h.claim(), code, "interrupted")
		h.tick()
	}
	current, _ := h.db.Automation(h.ctx, a.ID)
	if current.Paused {
		t.Fatalf("interruptions paused the automation: %q", current.PauseReason)
	}
	stopped := 0
	for _, run := range h.runs(a) {
		if run.Status == store.RunStopped {
			stopped++
		}
	}
	if stopped != 2 {
		t.Fatalf("user_stopped runs should be recorded as stopped, got %d", stopped)
	}
}

func TestTimeoutIsItsOwnStatusAndCountsAsAFailure(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "slow", PauseAfterFailures: 1})
	run := h.start(a, "k1")
	h.fail(h.claim(), "execution_timeout", "本轮执行超过时间上限，已停止。")
	h.tick()
	got, _ := h.db.AutomationRun(h.ctx, run.ID)
	if got.Status != store.RunTimeout {
		t.Fatalf("%+v", got)
	}
	if current, _ := h.db.Automation(h.ctx, a.ID); !current.Paused {
		t.Fatal("a timeout should count toward pausing")
	}
}

func TestOnlyOneActiveRunPerAutomationAndManualRunsAreIdempotent(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	first := h.start(a, "same-click")
	again := h.start(a, "same-click")
	if again.ID != first.ID {
		t.Fatal("the same key started a second run")
	}
	if tasks, _ := h.db.ListTasks(h.ctx); len(tasks) != 1 {
		t.Fatalf("%d tasks for one logical run", len(tasks))
	}
	if _, err := h.svc.Start(h.ctx, a, StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:other-click"}); !errors.Is(err, ErrRunning) {
		t.Fatalf("a second concurrent run was allowed: %v", err)
	}
}

func TestDailyLimitBlocksFurtherManualRuns(t *testing.T) {
	h := newHarness(t)
	h.clock = time.Now() // runs are stamped with the real clock, so count against it
	a := h.create(store.AutomationInput{Name: "daily", DailyLimit: 2})
	for i := 0; i < 2; i++ {
		h.start(a, string(rune('a'+i)))
		h.succeed(h.claim(), "done", nil)
		h.tick()
	}
	if _, err := h.svc.Start(h.ctx, a, StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:third"}); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("manual run beyond the limit: %v", err)
	}
	// Skipped attempts do not use up the budget.
	if used, _ := h.db.CountAutomationRunsSince(h.ctx, a.ID, DayStart(h.clock, time.UTC)); used != 2 {
		t.Fatalf("budget used = %d", used)
	}
}

// A due slot that finds the budget spent is recorded as skipped, with a reason,
// instead of being silently lost or run anyway.
func TestScheduledSlotIsSkippedWhenTheDailyBudgetIsSpent(t *testing.T) {
	h := newHarness(t)
	h.clock = time.Now().UTC()
	a := h.create(store.AutomationInput{Name: "daily", DailyLimit: 1})
	h.start(a, "manual")
	h.succeed(h.claim(), "done", nil)
	h.tick() // one run today: the budget of one is spent

	// A slot that passed a minute ago, on an automation whose anchor is older.
	slot := h.clock.Add(-time.Minute)
	clock := slot.Format("15:04")
	if _, err := h.db.UpdateAutomation(h.ctx, a.ID, store.AutomationInput{Name: a.Name, Prompt: a.Prompt, Adapter: a.Adapter, ScheduleTime: clock, Network: a.Network, DailyLimit: 1}); err != nil {
		t.Fatal(err)
	}
	h.backdateAnchor(a.ID, h.clock.Add(-2*time.Hour))
	h.tick()
	h.tick() // a second pass must not record the same skipped slot again

	var skipped []store.AutomationRun
	for _, run := range h.runs(a) {
		if run.Status == store.RunSkipped {
			skipped = append(skipped, run)
		}
	}
	if len(skipped) != 1 || skipped[0].Code != "daily_limit" || skipped[0].TaskID != "" || skipped[0].Trigger != store.TriggerSchedule {
		t.Fatalf("expected exactly one skipped scheduled run, got %+v", h.runs(a))
	}
	if inbox, _ := h.db.AutomationInbox(h.ctx, 50); len(inbox) != 1 {
		t.Fatalf("a skipped run must not appear in the inbox, got %d entries", len(inbox))
	}
	if tasks, _ := h.db.ListTasks(h.ctx); len(tasks) != 1 {
		t.Fatalf("a skipped slot created a task: %d tasks", len(tasks))
	}
}

// backdateAnchor moves an automation's anchor into the past, as if it had been
// created or resumed earlier.
func (h *harness) backdateAnchor(id string, at time.Time) {
	h.t.Helper()
	conn, err := sql.Open("sqlite", h.dbPath)
	if err != nil {
		h.t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(h.ctx, "UPDATE automations SET anchor_at=? WHERE id=?", at.UTC().Format(time.RFC3339Nano), id); err != nil {
		h.t.Fatal(err)
	}
}

func mustAutomation(t *testing.T, h *harness, id string) store.Automation {
	t.Helper()
	a, err := h.db.Automation(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRulesAreFrozenAndOnlyTheFirstTurnIsUnattended(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily", Prompt: "original prompt", Network: store.NetworkRestricted, TimeoutSeconds: 600})
	run := h.start(a, "k1")
	if _, err := h.db.UpdateAutomation(h.ctx, a.ID, store.AutomationInput{Name: "daily", Prompt: "edited later", Adapter: "codex", Network: store.NetworkOpen, TimeoutSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	rules, ok, err := h.db.AutomationRulesForTurn(h.ctx, run.TurnID)
	if err != nil || !ok || rules.Prompt != "original prompt" || rules.Network != store.NetworkRestricted || rules.TimeoutSeconds != 600 {
		t.Fatalf("rules were not frozen: %+v ok=%v err=%v", rules, ok, err)
	}
	w := h.claim()
	if w.job.Prompt != "original prompt" {
		t.Fatalf("the task ran %q", w.job.Prompt)
	}
	h.succeed(w, "done", nil)
	detail, _, err := h.db.CreateTurn(h.ctx, run.TaskID, core.CreateTurnInput{Prompt: "now also fix the lint errors", IdempotencyKey: "follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ = h.db.AutomationRulesForTurn(h.ctx, detail.SubmittedTurnID); ok {
		t.Fatal("a follow-up turn must be interactive, not unattended")
	}
}

func TestStartingRunIsResumedAfterAnInterruption(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	key := "automation:" + a.ID + ":manual:crashed"
	run, inserted, err := h.db.InsertAutomationRun(h.ctx, newRun(a, StartRequest{Trigger: store.TriggerManual, Key: key}, store.RunStarting, "", ""))
	if err != nil || !inserted || run.Status != store.RunStarting {
		t.Fatalf("%+v %v", run, err)
	}
	// The store stamps rows with the real clock, so judge age against the real
	// clock here instead of the harness clock that runs two days ahead.
	h.clock = time.Now()
	h.tick() // too soon: the original starter may still be working
	if status(t, h.db, run.ID) != store.RunStarting {
		t.Fatal("a fresh starting run was taken over")
	}
	h.clock = time.Now().Add(2 * startingGrace)
	h.tick()
	got, _ := h.db.AutomationRun(h.ctx, run.ID)
	if got.Status != store.RunActive || got.TaskID == "" {
		t.Fatalf("the interrupted start was not completed: %+v", got)
	}
	if tasks, _ := h.db.ListTasks(h.ctx); len(tasks) != 1 {
		t.Fatalf("%d tasks", len(tasks))
	}
}

func TestUnusableProjectFailsTheRunWithAClearReason(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "gone"})
	if err := os.RemoveAll(h.repo); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.Start(h.ctx, a, StartRequest{Trigger: store.TriggerManual, Key: "automation:" + a.ID + ":manual:k"})
	if err == nil {
		t.Fatal("starting against a missing project succeeded")
	}
	runs := h.runs(a)
	if len(runs) != 1 || runs[0].Status != store.RunFailed || runs[0].Code != "repository_not_found" || runs[0].TaskID != "" {
		t.Fatalf("%+v", runs)
	}
	if h.db == nil {
		t.Fatal()
	}
}

func TestDeletingTheTaskHidesTheRunAndStopsItCleanly(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	run := h.start(a, "k1")
	if _, err := h.db.DeleteTask(h.ctx, run.TaskID); err != nil {
		t.Fatal(err)
	}
	h.tick()
	if status(t, h.db, run.ID) != store.RunStopped {
		t.Fatalf("deleting the task should close the run as stopped, got %s", status(t, h.db, run.ID))
	}
	if inbox, _ := h.db.AutomationInbox(h.ctx, 50); len(inbox) != 0 {
		t.Fatalf("a deleted task's run is still in the inbox: %+v", inbox)
	}
	if current, _ := h.db.Automation(h.ctx, a.ID); current.Paused {
		t.Fatal("deleting a task must not pause the automation")
	}
}

// Automation runs may use only the background share of the worker, so an
// interactive task is never stuck behind them.
func TestBackgroundLimitLeavesSlotsForInteractiveTasks(t *testing.T) {
	h := newHarness(t)
	first := h.create(store.AutomationInput{Name: "one"})
	second := h.create(store.AutomationInput{Name: "two"})
	h.start(first, "k")
	h.start(second, "k")
	interactive, _, err := h.db.CreateTask(h.ctx, core.CreateTaskInput{Prompt: "interactive", Adapter: "codex", RepositoryPath: h.repo, BaseCommit: git(t, h.repo, "rev-parse", "HEAD"), IdempotencyKey: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	claim := func() *core.Job {
		job, err := h.db.ClaimNextLimited(h.ctx, "w", 1)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	a := claim()
	if a == nil || a.TaskID == interactive.Task.ID {
		t.Fatalf("expected the oldest job (an automation) first: %+v", a)
	}
	b := claim()
	if b == nil || b.TaskID != interactive.Task.ID {
		t.Fatalf("the second automation should wait and the interactive task go next: %+v", b)
	}
	if c := claim(); c != nil {
		t.Fatalf("a second automation ran beside the first despite the limit of 1: %+v", c)
	}
	// Finishing the first frees the background slot.
	h.event(work{job: a}, "turn.completed", map[string]string{})
	if c := claim(); c == nil {
		t.Fatal("the waiting automation never got its turn")
	}
}

func TestZeroLimitDisablesTheBackgroundCap(t *testing.T) {
	h := newHarness(t)
	h.start(h.create(store.AutomationInput{Name: "one"}), "k")
	h.start(h.create(store.AutomationInput{Name: "two"}), "k")
	for i := 0; i < 2; i++ {
		if job, err := h.db.ClaimNextLimited(h.ctx, "w", 0); err != nil || job == nil {
			t.Fatalf("job %d: %v %v", i, job, err)
		}
	}
}

// A finished run whose task the user later deletes must leave the inbox and the
// unread count. The status filter alone would not hide it, so this checks the
// deleted-task rule on its own.
func TestFinishedRunDisappearsWhenItsTaskIsDeleted(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	run := h.start(a, "k1")
	h.succeed(h.claim(), "done", nil)
	h.tick()
	if inbox, _ := h.db.AutomationInbox(h.ctx, 50); len(inbox) != 1 || inbox[0].Status != store.RunCompleted {
		t.Fatalf("setup: %+v", inbox)
	}
	if unread, _ := h.db.UnreadAutomationRuns(h.ctx); unread != 1 {
		t.Fatalf("setup: unread=%d", unread)
	}
	if _, err := h.db.DeleteTask(h.ctx, run.TaskID); err != nil {
		t.Fatal(err)
	}
	if inbox, _ := h.db.AutomationInbox(h.ctx, 50); len(inbox) != 0 {
		t.Fatalf("a finished run of a deleted task is still listed: %+v", inbox)
	}
	if unread, _ := h.db.UnreadAutomationRuns(h.ctx); unread != 0 {
		t.Fatalf("a deleted task still counts as unread: %d", unread)
	}
}

// Dismissing removes a run from the inbox for good without touching its task.
func TestDismissedRunLeavesTheInboxButKeepsItsTask(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily"})
	run := h.start(a, "k1")
	h.succeed(h.claim(), "done", nil)
	h.tick()
	if err := h.db.DismissAutomationRun(h.ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if inbox, _ := h.db.AutomationInbox(h.ctx, 50); len(inbox) != 0 {
		t.Fatal("dismissed run is still in the inbox")
	}
	if unread, _ := h.db.UnreadAutomationRuns(h.ctx); unread != 0 {
		t.Fatalf("dismissed run is still unread: %d", unread)
	}
	if _, err := h.db.GetTask(h.ctx, run.TaskID); err != nil {
		t.Fatalf("dismissing a run must not delete its task: %v", err)
	}
	if len(h.runs(a)) != 1 {
		t.Fatal("dismissed run vanished from the history")
	}
	if err := h.db.DismissAutomationRun(h.ctx, "run_missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("dismissing an unknown run: %v", err)
	}
}

// Two services racing on the same slot (a worker and its replacement) must still
// produce one run, whichever one reaches the database first.
func TestTwoServicesOnTheSameSlotStartItOnce(t *testing.T) {
	h := newHarness(t)
	a := h.create(store.AutomationInput{Name: "daily", ScheduleTime: "09:00"})
	other := h.newService()
	for i := 0; i < 3; i++ {
		for _, svc := range []*Service{h.svc, other} {
			if err := svc.Tick(h.ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := h.runs(a); len(got) != 1 {
		t.Fatalf("%d runs for one slot", len(got))
	}
	if tasks, _ := h.db.ListTasks(h.ctx); len(tasks) != 1 {
		t.Fatalf("%d tasks for one slot", len(tasks))
	}
}
