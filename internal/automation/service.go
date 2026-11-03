package automation

import (
	"blux/internal/artifact"
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

var (
	// ErrRunning means the automation already has a run in progress.
	ErrRunning = errors.New("automation already has a run in progress")
	// ErrDailyLimit means today's run budget for the automation is used up.
	ErrDailyLimit = errors.New("automation reached its daily run limit")
)

const (
	tickInterval  = 2 * time.Second
	startingGrace = time.Minute
	summaryLimit  = 280
	// StartTimeout bounds creating a run's task, which snapshots the project.
	StartTimeout = 3 * time.Minute
)

// Service starts automation runs and follows them to a final outcome. One
// Service in the worker process drives the schedule; the web server uses
// another only to start manual runs.
type Service struct {
	Store   *store.Store
	DataDir string
	Logger  *slog.Logger
	// Notify shows a desktop notification when a run finishes. It may be nil.
	Notify func(title, body string)
	// Now and Location exist for tests. They default to the wall clock and the
	// machine's time zone.
	Now      func() time.Time
	Location *time.Location
}

// StartRequest describes why a run starts. Key makes the start idempotent: the
// same key never creates a second run.
type StartRequest struct {
	Trigger   string
	PlannedAt time.Time
	Key       string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) loc() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	return time.Local
}

func (s *Service) logError(message string, args ...any) {
	if s.Logger != nil {
		s.Logger.Error(message, args...)
	}
}

// Run drives the service until ctx ends. A failing tick is logged and never
// stops the loop, so one bad automation cannot take the worker down.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.logError("automation tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick performs one pass: finish runs whose task ended, retry half-started
// runs, start due scheduled runs, then deliver notifications.
func (s *Service) Tick(ctx context.Context) error {
	var errs []error
	for _, step := range []func(context.Context) error{s.reconcile, s.recoverStarting, s.schedule, s.notifyPending} {
		if err := step(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) schedule(ctx context.Context) error {
	list, err := s.Store.Automations(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	var errs []error
	for _, a := range list {
		if a.Paused || a.ScheduleTime == "" {
			continue
		}
		slot, ok := LatestSlot(a.ScheduleTime, now, s.loc())
		if !ok {
			continue
		}
		anchor, err := time.Parse(time.RFC3339Nano, a.AnchorAt)
		if err != nil {
			errs = append(errs, fmt.Errorf("automation %s has an invalid anchor: %w", a.ID, err))
			continue
		}
		if !slot.After(anchor) {
			continue
		}
		key := SlotKey(a.ID, slot)
		if _, found, err := s.Store.AutomationRunByKey(ctx, key); err != nil {
			errs = append(errs, err)
			continue
		} else if found {
			continue
		}
		if _, err = s.Start(ctx, a, StartRequest{Trigger: store.TriggerSchedule, PlannedAt: slot, Key: key}); err != nil {
			errs = append(errs, fmt.Errorf("start automation %s: %w", a.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Start creates a run and its task. The same Key always returns the same run,
// and an unfinished start with that key is resumed instead of repeated.
func (s *Service) Start(ctx context.Context, a store.Automation, req StartRequest) (store.AutomationRun, error) {
	if run, found, err := s.Store.AutomationRunByKey(ctx, req.Key); err != nil {
		return store.AutomationRun{}, err
	} else if found {
		if run.Status == store.RunStarting {
			return s.launch(ctx, run)
		}
		return run, nil
	}
	scheduled := req.Trigger == store.TriggerSchedule
	busy, err := s.Store.AutomationHasActiveRun(ctx, a.ID)
	if err != nil {
		return store.AutomationRun{}, err
	}
	if busy {
		if scheduled {
			return s.skip(ctx, a, req, "previous_running", "上一次运行尚未结束，已跳过本次定时运行。")
		}
		return store.AutomationRun{}, ErrRunning
	}
	used, err := s.Store.CountAutomationRunsSince(ctx, a.ID, DayStart(s.now(), s.loc()))
	if err != nil {
		return store.AutomationRun{}, err
	}
	if used >= a.DailyLimit {
		if scheduled {
			return s.skip(ctx, a, req, "daily_limit", fmt.Sprintf("今天已运行 %d 次，达到每日上限，已跳过本次定时运行。", used))
		}
		return store.AutomationRun{}, ErrDailyLimit
	}
	run, inserted, err := s.Store.InsertAutomationRun(ctx, newRun(a, req, store.RunStarting, "", ""))
	if err != nil {
		return store.AutomationRun{}, err
	}
	if !inserted && run.Status != store.RunStarting {
		return run, nil
	}
	return s.launch(ctx, run)
}

func newRun(a store.Automation, req StartRequest, status, code, message string) store.NewAutomationRun {
	planned := ""
	if !req.PlannedAt.IsZero() {
		planned = req.PlannedAt.UTC().Format(time.RFC3339Nano)
	}
	return store.NewAutomationRun{
		AutomationID: a.ID, Trigger: req.Trigger, DedupeKey: req.Key, PlannedAt: planned, Status: status, Code: code, Message: message,
		Rules: store.AutomationRules{Name: a.Name, Prompt: a.Prompt, Adapter: a.Adapter, RepositoryPath: a.RepositoryPath, Network: a.Network, TimeoutSeconds: a.TimeoutSeconds},
	}
}

func (s *Service) skip(ctx context.Context, a store.Automation, req StartRequest, code, message string) (store.AutomationRun, error) {
	run, _, err := s.Store.InsertAutomationRun(ctx, newRun(a, req, store.RunSkipped, code, message))
	return run, err
}

// launch turns a starting run into a queued task. Everything it does is
// repeatable, so a crash at any point is repaired by running it again.
func (s *Service) launch(ctx context.Context, run store.AutomationRun) (store.AutomationRun, error) {
	in := core.CreateTaskInput{
		Title: taskTitle(run, s.loc()), Prompt: run.Rules.Prompt, Adapter: run.Rules.Adapter,
		RepositoryPath: run.Rules.RepositoryPath, Mode: "explore", IdempotencyKey: "automation:" + run.ID,
	}
	detail, found, err := s.Store.LookupTaskCreation(ctx, in)
	if err != nil {
		return s.fail(ctx, run, err)
	}
	if !found {
		repo, err := (workspace.Manager{DataDir: s.DataDir}).Freeze(ctx, run.Rules.RepositoryPath)
		if err != nil {
			return s.fail(ctx, run, err)
		}
		in.BaseCommit = repo.BaseCommit
		if detail, _, err = s.Store.CreateTask(ctx, in); err != nil {
			return s.fail(ctx, run, err)
		}
	}
	turnID := detail.SubmittedTurnID
	if turnID == "" {
		turnID = detail.Task.CurrentTurnID
	}
	if err = s.Store.AttachAutomationRunTask(ctx, run.ID, detail.Task.ID, turnID); err != nil {
		return store.AutomationRun{}, err
	}
	return s.Store.AutomationRun(ctx, run.ID)
}

func taskTitle(run store.AutomationRun, loc *time.Location) string {
	name := Clip(run.Rules.Name, 80)
	created, err := time.Parse(time.RFC3339Nano, run.CreatedAt)
	if err != nil {
		return "自动化 · " + name
	}
	return "自动化 · " + name + " · " + created.In(loc).Format("01-02 15:04")
}

// fail records that a run could not even start. A canceled context is not a
// failure of the run: it stays "starting" and is retried later.
func (s *Service) fail(ctx context.Context, run store.AutomationRun, cause error) (store.AutomationRun, error) {
	if ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return run, cause
	}
	code, message := "start_failed", "无法创建自动化任务，请查看 Worker 日志。"
	var execErr *core.ExecutionError
	if errors.As(cause, &execErr) {
		code, message = execErr.Code, execErr.Message
	}
	record, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	changed, err := s.Store.FinishAutomationRun(record, run.ID, store.AutomationOutcome{Status: store.RunFailed, Code: code, Message: message})
	if err == nil && changed {
		s.evaluatePause(record, run.AutomationID)
	}
	final, loadErr := s.Store.AutomationRun(record, run.ID)
	if loadErr != nil {
		final = run
	}
	return final, cause
}

// recoverStarting retries runs that were recorded but never got a task, for
// example because the process stopped between those two steps.
func (s *Service) recoverStarting(ctx context.Context) error {
	runs, err := s.Store.ActiveAutomationRuns(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, run := range runs {
		if run.Status != store.RunStarting {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, run.CreatedAt)
		if err != nil || s.now().Sub(created) < startingGrace {
			continue
		}
		if _, err = s.launch(ctx, run); err != nil && ctx.Err() == nil {
			errs = append(errs, fmt.Errorf("resume run %s: %w", run.ID, err))
		}
	}
	return errors.Join(errs...)
}

// reconcile closes runs whose task has finished.
func (s *Service) reconcile(ctx context.Context) error {
	runs, err := s.Store.ActiveAutomationRuns(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, run := range runs {
		if run.Status != store.RunActive || run.TurnID == "" {
			continue
		}
		turn, err := s.Store.TurnOutcome(ctx, run.TurnID)
		if err != nil {
			errs = append(errs, fmt.Errorf("read turn of run %s: %w", run.ID, err))
			continue
		}
		var out store.AutomationOutcome
		switch turn.Status {
		case core.StatusCompleted:
			out = s.completedOutcome(ctx, run)
		case core.StatusFailed:
			out = Outcome(turn.Code, turn.Message)
		default:
			continue
		}
		changed, err := s.Store.FinishAutomationRun(ctx, run.ID, out)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if changed && (out.Status == store.RunFailed || out.Status == store.RunTimeout) {
			s.evaluatePause(ctx, run.AutomationID)
		}
	}
	return errors.Join(errs...)
}

// completedOutcome distinguishes a finished result from a run that stopped to
// ask the user something, and extracts a short summary for the inbox.
func (s *Service) completedOutcome(ctx context.Context, run store.AutomationRun) store.AutomationOutcome {
	out := store.AutomationOutcome{Status: store.RunCompleted}
	if text, err := s.Store.LastAssistantText(ctx, run.TurnID); err == nil {
		out.Summary = Clip(text, summaryLimit)
	}
	if question, found, err := s.Store.TurnArtifact(ctx, run.TurnID, QuestionFile); err == nil && found {
		out.Status = store.RunNeedsDecision
		if data, err := os.ReadFile((artifact.Service{DataDir: s.DataDir}).Blob(question.Hash)); err == nil {
			if text := Clip(string(data), summaryLimit); text != "" {
				out.Summary = text
			}
		}
	}
	if out.Summary == "" {
		out.Summary = "运行完成。"
	}
	return out
}

// evaluatePause pauses an automation after too many consecutive failures, so a
// broken setup does not keep burning runs every day.
func (s *Service) evaluatePause(ctx context.Context, automationID string) {
	a, err := s.Store.Automation(ctx, automationID)
	if err != nil || a.Paused {
		return
	}
	recent, err := s.Store.RecentAutomationOutcomes(ctx, automationID, 20)
	if err != nil {
		s.logError("read recent automation outcomes", "automation_id", automationID, "error", err)
		return
	}
	streak := FailureStreak(recent)
	if streak < a.PauseAfterFailures {
		return
	}
	last := ""
	for _, run := range recent {
		if (run.Status == store.RunFailed || run.Status == store.RunTimeout) && !Interrupted(run.Code) {
			last = run.Message
			break
		}
	}
	reason := fmt.Sprintf("连续 %d 次运行失败，已自动暂停。最近一次：%s", streak, Clip(last, 120))
	if _, err = s.Store.SetAutomationPaused(ctx, automationID, true, reason); err != nil {
		s.logError("pause automation", "automation_id", automationID, "error", err)
	}
}

func (s *Service) notifyPending(ctx context.Context) error {
	runs, err := s.Store.RunsAwaitingNotification(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, run := range runs {
		if s.Notify != nil {
			title, body := notificationText(run)
			if a, err := s.Store.Automation(ctx, run.AutomationID); err == nil && a.Paused && (run.Status == store.RunFailed || run.Status == store.RunTimeout) {
				body += "（该自动化已暂停）"
			}
			s.Notify(title, body)
		}
		if err := s.Store.MarkAutomationRunNotified(ctx, run.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func notificationText(run store.AutomationRun) (title, body string) {
	name := Clip(run.AutomationName, 40)
	switch run.Status {
	case store.RunNeedsDecision:
		return "需要你的决定 · " + name, run.Summary
	case store.RunFailed:
		return "自动化失败 · " + name, run.Message
	case store.RunTimeout:
		return "自动化超时 · " + name, "运行超过时间上限，已停止。"
	default:
		return "自动化完成 · " + name, run.Summary
	}
}
