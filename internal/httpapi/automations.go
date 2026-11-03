package httpapi

import (
	"blux/internal/automation"
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// automationView adds the values a client needs but the database does not keep.
type automationView struct {
	store.Automation
	NextRunAt string               `json:"next_run_at,omitempty"`
	RunsToday int                  `json:"runs_today"`
	LastRun   *store.AutomationRun `json:"last_run,omitempty"`
}

func (s *Server) automationService() *automation.Service {
	return &automation.Service{Store: s.Store, DataDir: s.DataDir, Logger: s.Logger}
}

func (s *Server) describeAutomation(ctx context.Context, a store.Automation) (automationView, error) {
	view := automationView{Automation: a}
	now := time.Now()
	if !a.Paused && a.ScheduleTime != "" {
		if next, ok := automation.NextSlot(a.ScheduleTime, now, time.Local); ok {
			view.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	}
	used, err := s.Store.CountAutomationRunsSince(ctx, a.ID, automation.DayStart(now, time.Local))
	if err != nil {
		return view, err
	}
	view.RunsToday = used
	runs, err := s.Store.AutomationRuns(ctx, a.ID, 1)
	if err != nil {
		return view, err
	}
	if len(runs) > 0 {
		view.LastRun = &runs[0]
	}
	return view, nil
}

// automationError turns a missing automation into a specific message and leaves
// every other error to the shared mapping.
func (s *Server) automationError(w http.ResponseWriter, err error) {
	if errors.Is(err, core.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "没有找到该自动化。")
		return
	}
	s.storeError(w, err)
}

// cleanProject makes a user-supplied project path absolute and checks that it
// is a directory Blux can snapshot.
func (s *Server) cleanProject(ctx context.Context, raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	repo, err := workspace.Inspect(ctx, path, false)
	if err != nil {
		return "", err
	}
	return repo.Path, nil
}

type automationBody struct {
	Name               string `json:"name"`
	Prompt             string `json:"prompt"`
	RepositoryPath     string `json:"repository_path"`
	Adapter            string `json:"adapter"`
	ScheduleTime       string `json:"schedule_time"`
	Network            string `json:"network"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	DailyLimit         int    `json:"daily_limit"`
	PauseAfterFailures int    `json:"pause_after_failures"`
}

func (b automationBody) input() store.AutomationInput {
	return store.AutomationInput{Name: b.Name, Prompt: b.Prompt, RepositoryPath: b.RepositoryPath, Adapter: b.Adapter, ScheduleTime: b.ScheduleTime, Network: b.Network, TimeoutSeconds: b.TimeoutSeconds, DailyLimit: b.DailyLimit, PauseAfterFailures: b.PauseAfterFailures}
}

func (s *Server) automations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Automations(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	views := make([]automationView, 0, len(list))
	for _, a := range list {
		view, err := s.describeAutomation(r.Context(), a)
		if err != nil {
			s.storeError(w, err)
			return
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"automations": views})
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body automationBody
	if !readJSON(w, r, &body) {
		return
	}
	in := body.input()
	// Validate before touching the file system so mistakes get the clearest message.
	if _, err := store.NormalizeAutomation(in); err != nil {
		s.storeError(w, err)
		return
	}
	if strings.TrimSpace(in.RepositoryPath) == "" {
		writeError(w, http.StatusBadRequest, "repository_required", "请选择本地项目文件夹。")
		return
	}
	path, err := s.cleanProject(r.Context(), in.RepositoryPath)
	if err != nil {
		s.storeError(w, err)
		return
	}
	in.RepositoryPath = path
	a, replayed, err := s.Store.CreateAutomation(r.Context(), in, key)
	if err != nil {
		s.automationError(w, err)
		return
	}
	view, err := s.describeAutomation(r.Context(), a)
	if err != nil {
		s.storeError(w, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, view)
}

func (s *Server) automation(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.Automation(r.Context(), r.PathValue("automation"))
	if err != nil {
		s.automationError(w, err)
		return
	}
	view, err := s.describeAutomation(r.Context(), a)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("automation")
	var body automationBody
	if !readJSON(w, r, &body) {
		return
	}
	// The project is fixed once an automation exists, so a request cannot move it.
	current, err := s.Store.Automation(r.Context(), id)
	if err != nil {
		s.automationError(w, err)
		return
	}
	in := body.input()
	if strings.TrimSpace(in.RepositoryPath) != "" && strings.TrimSpace(in.RepositoryPath) != current.RepositoryPath {
		writeError(w, http.StatusBadRequest, "invalid_request", "自动化创建后不能更换项目，请新建一个自动化。")
		return
	}
	in.RepositoryPath = current.RepositoryPath
	a, err := s.Store.UpdateAutomation(r.Context(), id, in)
	if err != nil {
		s.automationError(w, err)
		return
	}
	view, err := s.describeAutomation(r.Context(), a)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteAutomation(r.Context(), r.PathValue("automation")); err != nil {
		s.automationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) setAutomationPaused(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused *bool `json:"paused"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Paused == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请指定是否暂停。")
		return
	}
	a, err := s.Store.SetAutomationPaused(r.Context(), r.PathValue("automation"), *body.Paused, "已手动暂停。")
	if err != nil {
		s.automationError(w, err)
		return
	}
	view, err := s.describeAutomation(r.Context(), a)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// runAutomation starts one run now. Creating the task snapshots the project,
// which can outlast the browser's patience, so it continues if the client goes
// away and a retry with the same key finds the same run.
func (s *Server) runAutomation(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	a, err := s.Store.Automation(r.Context(), r.PathValue("automation"))
	if err != nil {
		s.automationError(w, err)
		return
	}
	dedupe := "automation:" + a.ID + ":manual:" + key
	if run, found, err := s.Store.AutomationRunByKey(r.Context(), dedupe); err != nil {
		s.storeError(w, err)
		return
	} else if found && run.Status != store.RunStarting {
		w.Header().Set("Idempotency-Replayed", "true")
		writeJSON(w, http.StatusOK, run)
		return
	}
	if err = s.checkAdapter(r.Context(), a.Adapter); err != nil {
		s.storeError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), automation.StartTimeout)
	defer cancel()
	run, err := s.automationService().Start(ctx, a, automation.StartRequest{Trigger: store.TriggerManual, Key: dedupe})
	switch {
	case errors.Is(err, automation.ErrRunning):
		writeError(w, http.StatusConflict, "automation_running", "上一次运行还没有结束，请稍后再试。")
	case errors.Is(err, automation.ErrDailyLimit):
		writeError(w, http.StatusTooManyRequests, "automation_daily_limit", fmt.Sprintf("今天已运行 %d 次，达到每日上限。可以明天再试，或在自动化设置里调高上限。", a.DailyLimit))
	case err != nil:
		s.storeError(w, err)
	default:
		writeJSON(w, http.StatusCreated, run)
	}
}

func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.AutomationInbox(r.Context(), 100)
	if err != nil {
		s.storeError(w, err)
		return
	}
	unread, err := s.Store.UnreadAutomationRuns(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "unread": unread})
}

func (s *Server) inboxUnread(w http.ResponseWriter, r *http.Request) {
	unread, err := s.Store.UnreadAutomationRuns(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread})
}

func (s *Server) seenRun(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.MarkAutomationRunSeen(r.Context(), r.PathValue("run")); err != nil {
		s.runError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seen": true})
}

func (s *Server) dismissRun(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DismissAutomationRun(r.Context(), r.PathValue("run")); err != nil {
		s.runError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": true})
}

func (s *Server) runError(w http.ResponseWriter, err error) {
	if errors.Is(err, core.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "没有找到该运行记录。")
		return
	}
	s.storeError(w, err)
}
