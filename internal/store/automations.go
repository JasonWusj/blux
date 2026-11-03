package store

import (
	"blux/internal/core"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Run statuses. starting and active are in progress; the rest are final.
const (
	RunStarting      = "starting"
	RunActive        = "active"
	RunCompleted     = "completed"
	RunNeedsDecision = "needs_decision"
	RunFailed        = "failed"
	RunTimeout       = "timeout"
	RunStopped       = "stopped"
	RunSkipped       = "skipped"
)

const (
	TriggerSchedule   = "schedule"
	TriggerManual     = "manual"
	NetworkRestricted = "restricted"
	NetworkOpen       = "open"
)

const (
	DefaultAutomationTimeoutSeconds = 30 * 60
	DefaultAutomationDailyLimit     = 5
	DefaultAutomationPauseFailures  = 3
)

// Automation is a saved prompt that Blux runs on a schedule or on demand.
type Automation struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Prompt             string `json:"prompt"`
	RepositoryPath     string `json:"repository_path"`
	Adapter            string `json:"adapter"`
	ScheduleTime       string `json:"schedule_time"`
	Network            string `json:"network"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	DailyLimit         int    `json:"daily_limit"`
	PauseAfterFailures int    `json:"pause_after_failures"`
	Paused             bool   `json:"paused"`
	PausedAt           string `json:"paused_at,omitempty"`
	PauseReason        string `json:"pause_reason,omitempty"`
	AnchorAt           string `json:"anchor_at"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

// AutomationInput holds the user-editable fields. Zero numeric fields take
// their defaults. RepositoryPath is fixed once the automation exists.
type AutomationInput struct {
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

// AutomationRules is the configuration frozen into one run, so editing an
// automation never changes a run that has already started.
type AutomationRules struct {
	Name           string `json:"name"`
	Prompt         string `json:"prompt"`
	Adapter        string `json:"adapter"`
	RepositoryPath string `json:"repository_path"`
	Network        string `json:"network"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type AutomationRun struct {
	ID             string          `json:"id"`
	AutomationID   string          `json:"automation_id"`
	AutomationName string          `json:"automation_name"`
	Adapter        string          `json:"adapter"`
	Trigger        string          `json:"trigger"`
	PlannedAt      string          `json:"planned_at,omitempty"`
	Status         string          `json:"status"`
	TaskID         string          `json:"task_id"`
	TurnID         string          `json:"turn_id,omitempty"`
	TaskStatus     string          `json:"task_status,omitempty"`
	Code           string          `json:"code,omitempty"`
	Message        string          `json:"message,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	CreatedAt      string          `json:"created_at"`
	FinishedAt     string          `json:"finished_at,omitempty"`
	SeenAt         string          `json:"seen_at,omitempty"`
	NotifiedAt     string          `json:"-"`
	DismissedAt    string          `json:"-"`
	Rules          AutomationRules `json:"-"`
}

type NewAutomationRun struct {
	AutomationID string
	Trigger      string
	DedupeKey    string
	PlannedAt    string
	Status       string
	Code         string
	Message      string
	Rules        AutomationRules
}

type AutomationOutcome struct {
	Status  string
	Code    string
	Message string
	Summary string
}

type TurnOutcome struct {
	Status  string
	Code    string
	Message string
}

func invalidAutomation(message string) error {
	return &core.ExecutionError{Code: "invalid_request", Message: message}
}

// NormalizeAutomation trims, defaults, and validates user input. The returned
// error carries a user-facing message and maps to HTTP 400.
func NormalizeAutomation(in AutomationInput) (AutomationInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.RepositoryPath = strings.TrimSpace(in.RepositoryPath)
	in.Adapter = strings.TrimSpace(in.Adapter)
	in.ScheduleTime = strings.TrimSpace(in.ScheduleTime)
	in.Network = strings.TrimSpace(in.Network)
	if in.Name == "" {
		line := strings.SplitN(in.Prompt, "\n", 2)[0]
		in.Name = string([]rune(line)[:min(40, utf8.RuneCountInString(line))])
	}
	if in.Name == "" {
		return in, invalidAutomation("请填写自动化名称或提示词。")
	}
	if utf8.RuneCountInString(in.Name) > 120 {
		return in, invalidAutomation("自动化名称不能超过 120 个字符。")
	}
	if in.Prompt == "" {
		return in, invalidAutomation("请填写自动化要执行的提示词。")
	}
	if utf8.RuneCountInString(in.Prompt) > 16000 {
		return in, invalidAutomation("提示词不能超过 16000 个字符。")
	}
	if in.Adapter == "" {
		in.Adapter = "codex"
	}
	if in.Adapter != "codex" && in.Adapter != "claude" {
		return in, &core.ExecutionError{Code: "adapter_unsupported", Message: "自动化目前只支持 Codex 和 Claude Code。"}
	}
	if in.ScheduleTime != "" {
		if _, _, ok := core.ParseClock(in.ScheduleTime); !ok {
			return in, invalidAutomation("运行时间需要使用 24 小时制 HH:MM。")
		}
	}
	if in.Network == "" {
		in.Network = NetworkRestricted
	}
	if in.Network != NetworkRestricted && in.Network != NetworkOpen {
		return in, invalidAutomation("命令联网只能是 restricted 或 open。")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = DefaultAutomationTimeoutSeconds
	}
	if in.TimeoutSeconds < 60 || in.TimeoutSeconds > 4*60*60 {
		return in, invalidAutomation("单次运行时限需要在 1 分钟到 4 小时之间。")
	}
	if in.DailyLimit == 0 {
		in.DailyLimit = DefaultAutomationDailyLimit
	}
	if in.DailyLimit < 1 || in.DailyLimit > 50 {
		return in, invalidAutomation("每日运行次数上限需要在 1 到 50 之间。")
	}
	if in.PauseAfterFailures == 0 {
		in.PauseAfterFailures = DefaultAutomationPauseFailures
	}
	if in.PauseAfterFailures < 1 || in.PauseAfterFailures > 20 {
		return in, invalidAutomation("连续失败暂停次数需要在 1 到 20 之间。")
	}
	return in, nil
}

const automationColumns = "id,name,prompt,repository_path,adapter,schedule_time,network,timeout_seconds,daily_limit,pause_after_failures,anchor_at,paused_at,pause_reason,created_at,updated_at"

func scanAutomation(row scanner) (Automation, error) {
	var a Automation
	err := row.Scan(&a.ID, &a.Name, &a.Prompt, &a.RepositoryPath, &a.Adapter, &a.ScheduleTime, &a.Network, &a.TimeoutSeconds, &a.DailyLimit, &a.PauseAfterFailures, &a.AnchorAt, &a.PausedAt, &a.PauseReason, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	a.Paused = a.PausedAt != ""
	return a, err
}

func automationVisible(ctx context.Context, q querier, id string) error {
	var found int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM automations WHERE id=? AND deleted_at=''", id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ErrNotFound
	}
	return err
}

func (s *Store) CreateAutomation(ctx context.Context, in AutomationInput, key string) (Automation, bool, error) {
	in, err := NormalizeAutomation(in)
	if err != nil {
		return Automation{}, false, err
	}
	if in.RepositoryPath == "" {
		return Automation{}, false, &core.ExecutionError{Code: "repository_required", Message: "请选择本地项目文件夹。"}
	}
	if key == "" {
		return Automation{}, false, core.ErrInvalidState
	}
	hash := digest(in)
	var id string
	var replayed bool
	err = s.transaction(ctx, func(q querier) error {
		var old string
		e := q.QueryRowContext(ctx, "SELECT digest,entity_id FROM automation_receipts WHERE scope='create' AND key=?", key).Scan(&old, &id)
		if e == nil {
			if old != hash {
				return core.ErrConflict
			}
			replayed = true
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		id = core.NewID("automation")
		now := core.Now()
		if _, e = q.ExecContext(ctx, "INSERT INTO automations("+automationColumns+",deleted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'')", id, in.Name, in.Prompt, in.RepositoryPath, in.Adapter, in.ScheduleTime, in.Network, in.TimeoutSeconds, in.DailyLimit, in.PauseAfterFailures, now, "", "", now, now); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, "INSERT INTO automation_receipts(scope,key,digest,entity_id) VALUES('create',?,?,?)", key, hash, id)
		return e
	})
	if err != nil {
		return Automation{}, false, err
	}
	a, err := s.Automation(ctx, id)
	return a, replayed, err
}

// UpdateAutomation replaces the editable fields. Changing the schedule moves the
// anchor to now, so the new time never triggers a catch-up for a slot that has
// already passed.
func (s *Store) UpdateAutomation(ctx context.Context, id string, in AutomationInput) (Automation, error) {
	in, err := NormalizeAutomation(in)
	if err != nil {
		return Automation{}, err
	}
	err = s.transaction(ctx, func(q querier) error {
		current, e := scanAutomation(q.QueryRowContext(ctx, "SELECT "+automationColumns+" FROM automations WHERE id=? AND deleted_at=''", id))
		if e != nil {
			return e
		}
		anchor, now := current.AnchorAt, core.Now()
		if in.ScheduleTime != current.ScheduleTime {
			anchor = now
		}
		_, e = q.ExecContext(ctx, "UPDATE automations SET name=?,prompt=?,adapter=?,schedule_time=?,network=?,timeout_seconds=?,daily_limit=?,pause_after_failures=?,anchor_at=?,updated_at=? WHERE id=?", in.Name, in.Prompt, in.Adapter, in.ScheduleTime, in.Network, in.TimeoutSeconds, in.DailyLimit, in.PauseAfterFailures, anchor, now, id)
		return e
	})
	if err != nil {
		return Automation{}, err
	}
	return s.Automation(ctx, id)
}

func (s *Store) Automation(ctx context.Context, id string) (Automation, error) {
	return scanAutomation(s.db.QueryRowContext(ctx, "SELECT "+automationColumns+" FROM automations WHERE id=? AND deleted_at=''", id))
}

func (s *Store) Automations(ctx context.Context) ([]Automation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+automationColumns+" FROM automations WHERE deleted_at='' ORDER BY created_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Automation{}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAutomation stops future runs. Runs already recorded stay in the inbox.
func (s *Store) DeleteAutomation(ctx context.Context, id string) error {
	return s.transaction(ctx, func(q querier) error {
		r, err := q.ExecContext(ctx, "UPDATE automations SET deleted_at=?,updated_at=? WHERE id=? AND deleted_at=''", core.Now(), core.Now(), id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return core.ErrNotFound
		}
		return nil
	})
}

// SetAutomationPaused pauses or resumes. Pausing an already paused automation
// keeps the original reason. Resuming moves the anchor to now so the time spent
// paused is never replayed.
func (s *Store) SetAutomationPaused(ctx context.Context, id string, paused bool, reason string) (Automation, error) {
	err := s.transaction(ctx, func(q querier) error {
		if err := automationVisible(ctx, q, id); err != nil {
			return err
		}
		now := core.Now()
		if paused {
			_, err := q.ExecContext(ctx, "UPDATE automations SET paused_at=?,pause_reason=?,updated_at=? WHERE id=? AND paused_at=''", now, reason, now, id)
			return err
		}
		_, err := q.ExecContext(ctx, "UPDATE automations SET paused_at='',pause_reason='',anchor_at=?,updated_at=? WHERE id=?", now, now, id)
		return err
	})
	if err != nil {
		return Automation{}, err
	}
	return s.Automation(ctx, id)
}

const runColumns = "r.id,r.automation_id,a.name,a.adapter,r.trigger_kind,r.planned_at,r.status,r.task_id,r.turn_id,COALESCE(t.status,''),r.code,r.message,r.summary,r.created_at,r.finished_at,r.seen_at,r.notified_at,r.dismissed_at,r.rules_json"
const runFrom = " FROM automation_runs r JOIN automations a ON a.id=r.automation_id LEFT JOIN tasks t ON t.id=r.task_id "

func scanRun(row scanner) (AutomationRun, error) {
	var run AutomationRun
	var rules string
	err := row.Scan(&run.ID, &run.AutomationID, &run.AutomationName, &run.Adapter, &run.Trigger, &run.PlannedAt, &run.Status, &run.TaskID, &run.TurnID, &run.TaskStatus, &run.Code, &run.Message, &run.Summary, &run.CreatedAt, &run.FinishedAt, &run.SeenAt, &run.NotifiedAt, &run.DismissedAt, &rules)
	if errors.Is(err, sql.ErrNoRows) {
		return run, core.ErrNotFound
	}
	if err != nil {
		return run, err
	}
	return run, json.Unmarshal([]byte(rules), &run.Rules)
}

func queryRuns(ctx context.Context, q querier, where string, args ...any) ([]AutomationRun, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+runColumns+runFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutomationRun{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// InsertAutomationRun records a run under its dedupe key. It reports whether
// this call created the row; an existing row for the key is returned unchanged.
func (s *Store) InsertAutomationRun(ctx context.Context, in NewAutomationRun) (AutomationRun, bool, error) {
	rules, err := json.Marshal(in.Rules)
	if err != nil {
		return AutomationRun{}, false, err
	}
	finished := ""
	now := core.Now()
	if in.Status != RunStarting && in.Status != RunActive {
		finished = now
	}
	var inserted bool
	err = s.transaction(ctx, func(q querier) error {
		if err := automationVisible(ctx, q, in.AutomationID); err != nil {
			return err
		}
		r, e := q.ExecContext(ctx, "INSERT INTO automation_runs(id,automation_id,trigger_kind,dedupe_key,planned_at,status,rules_json,code,message,created_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING", core.NewID("run"), in.AutomationID, in.Trigger, in.DedupeKey, in.PlannedAt, in.Status, string(rules), in.Code, in.Message, now, finished)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		inserted = n == 1
		return nil
	})
	if err != nil {
		return AutomationRun{}, false, err
	}
	run, found, err := s.AutomationRunByKey(ctx, in.DedupeKey)
	if err == nil && !found {
		err = core.ErrNotFound
	}
	return run, inserted, err
}

func (s *Store) AutomationRun(ctx context.Context, id string) (AutomationRun, error) {
	return scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+runFrom+"WHERE r.id=?", id))
}

func (s *Store) AutomationRunByKey(ctx context.Context, key string) (AutomationRun, bool, error) {
	run, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+runFrom+"WHERE r.dedupe_key=?", key))
	if errors.Is(err, core.ErrNotFound) {
		return AutomationRun{}, false, nil
	}
	return run, err == nil, err
}

// AttachAutomationRunTask links the created task and marks the run active. It is
// safe to repeat with the same task.
func (s *Store) AttachAutomationRunTask(ctx context.Context, runID, taskID, turnID string) error {
	return s.transaction(ctx, func(q querier) error {
		r, err := q.ExecContext(ctx, "UPDATE automation_runs SET task_id=?,turn_id=?,status=? WHERE id=? AND status=?", taskID, turnID, RunActive, runID, RunStarting)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 1 {
			return nil
		}
		var current string
		if err = q.QueryRowContext(ctx, "SELECT task_id FROM automation_runs WHERE id=?", runID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return core.ErrNotFound
			}
			return err
		}
		if current == taskID {
			return nil
		}
		return core.ErrInvalidState
	})
}

// FinishAutomationRun records the final outcome once. It reports false when the
// run had already finished, which makes the caller's follow-up work run once.
func (s *Store) FinishAutomationRun(ctx context.Context, runID string, out AutomationOutcome) (bool, error) {
	var changed bool
	err := s.transaction(ctx, func(q querier) error {
		r, err := q.ExecContext(ctx, "UPDATE automation_runs SET status=?,code=?,message=?,summary=?,finished_at=? WHERE id=? AND status IN(?,?)", out.Status, out.Code, out.Message, out.Summary, core.Now(), runID, RunStarting, RunActive)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		changed = n == 1
		return nil
	})
	return changed, err
}

func (s *Store) ActiveAutomationRuns(ctx context.Context) ([]AutomationRun, error) {
	return queryRuns(ctx, s.db, "WHERE r.status IN('starting','active') ORDER BY r.created_at")
}

func (s *Store) AutomationHasActiveRun(ctx context.Context, automationID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM automation_runs WHERE automation_id=? AND status IN('starting','active')", automationID).Scan(&n)
	return n > 0, err
}

func (s *Store) AutomationRuns(ctx context.Context, automationID string, limit int) ([]AutomationRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return queryRuns(ctx, s.db, "WHERE r.automation_id=? ORDER BY r.created_at DESC,r.id LIMIT ?", automationID, limit)
}

// visibleRuns hides runs whose task the user deleted.
const visibleRuns = "(r.task_id='' OR r.task_id NOT IN(SELECT task_id FROM deleted_tasks))"

// AutomationInbox lists runs the user has not dismissed, newest first. Skipped
// and stopped runs stay in the history but do not need attention.
func (s *Store) AutomationInbox(ctx context.Context, limit int) ([]AutomationRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return queryRuns(ctx, s.db, "WHERE r.dismissed_at='' AND r.status NOT IN('skipped','stopped') AND "+visibleRuns+" ORDER BY r.created_at DESC,r.id LIMIT ?", limit)
}

func (s *Store) UnreadAutomationRuns(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM automation_runs r WHERE r.dismissed_at='' AND r.seen_at='' AND r.status IN('completed','needs_decision','failed','timeout') AND "+visibleRuns).Scan(&n)
	return n, err
}

func (s *Store) MarkAutomationRunSeen(ctx context.Context, id string) error {
	return s.markRun(ctx, "UPDATE automation_runs SET seen_at=CASE WHEN seen_at='' THEN ? ELSE seen_at END WHERE id=?", id)
}

func (s *Store) DismissAutomationRun(ctx context.Context, id string) error {
	return s.markRun(ctx, "UPDATE automation_runs SET dismissed_at=CASE WHEN dismissed_at='' THEN ? ELSE dismissed_at END WHERE id=?", id)
}

func (s *Store) MarkAutomationRunNotified(ctx context.Context, id string) error {
	return s.markRun(ctx, "UPDATE automation_runs SET notified_at=CASE WHEN notified_at='' THEN ? ELSE notified_at END WHERE id=?", id)
}

func (s *Store) markRun(ctx context.Context, statement, id string) error {
	return s.transaction(ctx, func(q querier) error {
		r, err := q.ExecContext(ctx, statement, core.Now(), id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return core.ErrNotFound
		}
		return nil
	})
}

// RunsAwaitingNotification returns finished runs the user has not been told
// about yet.
func (s *Store) RunsAwaitingNotification(ctx context.Context) ([]AutomationRun, error) {
	return queryRuns(ctx, s.db, "WHERE r.notified_at='' AND r.dismissed_at='' AND r.status IN('completed','needs_decision','failed','timeout') AND "+visibleRuns+" ORDER BY r.finished_at")
}

// CountAutomationRunsSince counts runs that really started, ignoring skipped
// ones. since must be a fixed-width UTC timestamp.
func (s *Store) CountAutomationRunsSince(ctx context.Context, automationID, since string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM automation_runs WHERE automation_id=? AND status<>'skipped' AND created_at>=?", automationID, since).Scan(&n)
	return n, err
}

// RecentAutomationOutcomes returns the newest runs that reached a verdict, which
// is what the consecutive-failure rule inspects.
func (s *Store) RecentAutomationOutcomes(ctx context.Context, automationID string, limit int) ([]AutomationRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return queryRuns(ctx, s.db, "WHERE r.automation_id=? AND r.status IN('completed','needs_decision','failed','timeout') ORDER BY r.created_at DESC,r.id LIMIT ?", automationID, limit)
}

// AutomationRulesForTurn reports whether a turn is the first turn of an
// automation run and returns the rules frozen for it. Later turns, such as the
// user's follow-ups, are ordinary interactive turns.
func (s *Store) AutomationRulesForTurn(ctx context.Context, turnID string) (AutomationRules, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT rules_json FROM automation_runs WHERE turn_id=? AND turn_id<>''", turnID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationRules{}, false, nil
	}
	if err != nil {
		return AutomationRules{}, false, err
	}
	var rules AutomationRules
	if err = json.Unmarshal([]byte(raw), &rules); err != nil {
		return AutomationRules{}, false, err
	}
	return rules, true, nil
}

func (s *Store) TurnOutcome(ctx context.Context, turnID string) (TurnOutcome, error) {
	var out TurnOutcome
	err := s.db.QueryRowContext(ctx, "SELECT status,error_code,error FROM turns WHERE id=?", turnID).Scan(&out.Status, &out.Code, &out.Message)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	return out, err
}

// TurnArtifact finds a deliverable of one turn by its path inside the
// artifacts directory.
func (s *Store) TurnArtifact(ctx context.Context, turnID, path string) (ArtifactVersion, bool, error) {
	var a ArtifactVersion
	err := s.db.QueryRowContext(ctx, "SELECT id,snapshot_id,task_id,turn_id,path,hash,size,mime,created_at FROM artifact_versions WHERE turn_id=? AND path=? ORDER BY created_at DESC LIMIT 1", turnID, path).Scan(&a.ID, &a.SnapshotID, &a.TaskID, &a.TurnID, &a.Path, &a.Hash, &a.Size, &a.MIME, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactVersion{}, false, nil
	}
	return a, err == nil, err
}

// LastAssistantText returns the final text block the agent wrote in a turn.
func (s *Store) LastAssistantText(ctx context.Context, turnID string) (string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT segments FROM messages WHERE turn_id=? AND role='assistant' ORDER BY position DESC", turnID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return "", err
		}
		var segments []core.Segment
		if err = json.Unmarshal([]byte(raw), &segments); err != nil {
			return "", err
		}
		for i := len(segments) - 1; i >= 0; i-- {
			if segments[i].Type == "text" && strings.TrimSpace(segments[i].Text) != "" {
				return segments[i].Text, nil
			}
		}
	}
	return "", rows.Err()
}
