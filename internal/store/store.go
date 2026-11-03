package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"blux/internal/contextpack"
	"blux/internal/core"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var initialMigration string

//go:embed migrations/002_execution.sql
var executionMigration string

//go:embed migrations/003_controls.sql
var controlsMigration string

//go:embed migrations/004_artifacts.sql
var artifactsMigration string

//go:embed migrations/005_context.sql
var contextMigration string

//go:embed migrations/006_flows.sql
var flowsMigration string

//go:embed migrations/007_manager.sql
var managerMigration string

//go:embed migrations/008_turn_context.sql
var turnContextMigration string

//go:embed migrations/009_collaborations.sql
var collaborationsMigration string

//go:embed migrations/010_turn_models.sql
var turnModelsMigration string

//go:embed migrations/011_remove_simulations.sql
var removeSimulationsMigration string

//go:embed migrations/012_reasoning_effort.sql
var reasoningEffortMigration string

//go:embed migrations/013_deletion.sql
var deletionMigration string

//go:embed migrations/014_pins.sql
var pinsMigration string

const SchemaVersion = 14

type Store struct{ db *sql.DB }

type querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("empty database path")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	dsn, err := databaseDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// BEGIN IMMEDIATE arbitrates writers across processes. Connection-local
	// pragmas live in the DSN so replacement connections receive them too.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	initializationCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Switching a fresh database to WAL can return SQLITE_BUSY immediately,
	// even with busy_timeout. Two independently starting processes must retry
	// that transition rather than require a particular startup order.
	if err = retrySQLiteContention(initializationCtx, func() error {
		_, err := db.ExecContext(initializationCtx, "PRAGMA journal_mode = WAL")
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	s := &Store{db: db}
	if err = retrySQLiteContention(initializationCtx, func() error {
		// The version check and DDL share the same write reservation, so another
		// process cannot observe schema version zero and run a second migration.
		return s.transaction(initializationCtx, func(q querier) error {
			var version int
			if err := q.QueryRowContext(initializationCtx, "PRAGMA user_version").Scan(&version); err != nil {
				return err
			}
			if version > SchemaVersion {
				return fmt.Errorf("database schema version %d is newer than supported version 9", version)
			}
			if version == 0 {
				if _, err := q.ExecContext(initializationCtx, initialMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 1"); err != nil {
					return err
				}
				version = 1
			}
			if version == 1 {
				if _, err := q.ExecContext(initializationCtx, executionMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 2"); err != nil {
					return err
				}
				version = 2
			}
			if version == 2 {
				if _, err := q.ExecContext(initializationCtx, controlsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 3"); err != nil {
					return err
				}
				version = 3
			}
			if version == 3 {
				if _, err := q.ExecContext(initializationCtx, artifactsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 4"); err != nil {
					return err
				}
			}
			if version < 5 {
				if _, err := q.ExecContext(initializationCtx, contextMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 5"); err != nil {
					return err
				}
			}
			if version < 6 {
				if _, err := q.ExecContext(initializationCtx, flowsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 6"); err != nil {
					return err
				}
			}
			if version < 7 {
				if _, err := q.ExecContext(initializationCtx, managerMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 7"); err != nil {
					return err
				}
			}
			if version < 8 {
				if _, err := q.ExecContext(initializationCtx, turnContextMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 8"); err != nil {
					return err
				}
			}
			if version < 9 {
				if _, err := q.ExecContext(initializationCtx, collaborationsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 9"); err != nil {
					return err
				}
			}
			if version < 10 {
				if _, err := q.ExecContext(initializationCtx, turnModelsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 10"); err != nil {
					return err
				}
			}
			if version < 11 {
				if _, err := q.ExecContext(initializationCtx, removeSimulationsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 11"); err != nil {
					return err
				}
			}
			if version < 12 {
				if _, err := q.ExecContext(initializationCtx, reasoningEffortMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 12"); err != nil {
					return err
				}
			}
			if version < 13 {
				if _, err := q.ExecContext(initializationCtx, deletionMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 13"); err != nil {
					return err
				}
			}
			if version < 14 {
				if _, err := q.ExecContext(initializationCtx, pinsMigration); err != nil {
					return err
				}
				if _, err := q.ExecContext(initializationCtx, "PRAGMA user_version = 14"); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return s, nil
}

func retrySQLiteContention(ctx context.Context, operation func() error) error {
	delay := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if err == nil {
			return nil
		}
		var sqliteError interface{ Code() int }
		if !errors.As(err, &sqliteError) {
			return err
		}
		// SQLite extended result codes retain the primary code in the low byte.
		code := sqliteError.Code() & 0xff
		if code != 5 && code != 6 { // SQLITE_BUSY / SQLITE_LOCKED
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		if delay < 100*time.Millisecond {
			delay *= 2
			if delay > 100*time.Millisecond {
				delay = 100 * time.Millisecond
			}
		}
	}
}

func databaseDSN(path string) (string, error) {
	var databaseURL *url.URL
	var err error
	switch {
	case path == ":memory:":
		databaseURL = &url.URL{Scheme: "file", Opaque: core.NewID("blux_memory")}
		query := databaseURL.Query()
		query.Set("mode", "memory")
		query.Set("cache", "shared")
		databaseURL.RawQuery = query.Encode()
	case strings.HasPrefix(path, "file:"):
		databaseURL, err = url.Parse(path)
		if err != nil {
			return "", fmt.Errorf("invalid SQLite URL: %w", err)
		}
	default:
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		databaseURL = &url.URL{Scheme: "file", Path: absolutePath}
	}
	query := databaseURL.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String(), nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) transaction(ctx context.Context, fn func(querier) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if err := fn(conn); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func digest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func idempotency(ctx context.Context, q querier, scope, key, hash string) (string, string, bool, error) {
	if key == "" {
		return "", "", false, nil
	}
	var existingHash, taskID, turnID string
	err := q.QueryRowContext(ctx, "SELECT digest, task_id, turn_id FROM idempotency_keys WHERE scope = ? AND key = ?", scope, key).Scan(&existingHash, &taskID, &turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if existingHash != hash {
		return "", "", false, fmt.Errorf("idempotency key reused with different content: %w", core.ErrConflict)
	}
	return taskID, turnID, true, nil
}

func rememberKey(ctx context.Context, q querier, scope, key, hash, taskID, turnID string) error {
	if key == "" {
		return nil
	}
	_, err := q.ExecContext(ctx, "INSERT INTO idempotency_keys(scope, key, digest, task_id, turn_id) VALUES (?, ?, ?, ?, ?)", scope, key, hash, taskID, turnID)
	return err
}

func normalizeTaskInput(in core.CreateTaskInput) (core.CreateTaskInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.Adapter = strings.TrimSpace(in.Adapter)
	if in.Adapter == "" {
		in.Adapter = "codex"
	}
	if in.Adapter != "codex" && in.Adapter != "claude" && in.Adapter != "deepseek" && in.Adapter != "gemini" {
		return in, fmt.Errorf("unknown adapter: %w", core.ErrInvalidState)
	}
	in.RepositoryPath = strings.TrimSpace(in.RepositoryPath)
	in.Model = strings.TrimSpace(in.Model)
	in.ModelProvider = strings.TrimSpace(in.ModelProvider)
	in.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	if !validEffort(in.ReasoningEffort) {
		return in, core.ErrInvalidState
	}
	if _, err := contextpack.Get(in.Mode); err != nil {
		return in, core.ErrInvalidState
	}
	if in.Prompt == "" {
		return in, fmt.Errorf("prompt is required: %w", core.ErrInvalidState)
	}
	if in.Title == "" {
		runes := []rune(strings.SplitN(in.Prompt, "\n", 2)[0])
		if len(runes) > 80 {
			runes = runes[:80]
		}
		in.Title = string(runes)
	}
	return in, nil
}

func taskCreationDigest(in core.CreateTaskInput) string {
	if in.ReasoningEffort != "" {
		effort := in.ReasoningEffort
		in.ReasoningEffort = ""
		return digest(struct{ Hash, Effort string }{taskCreationDigest(in), effort})
	}
	if in.CollaborationID != "" {
		id := in.CollaborationID
		in.CollaborationID = ""
		in.InputArtifacts = nil
		return digest(struct{ CollaborationID, Hash string }{id, taskCreationDigest(in)})
	}
	if in.Manager {
		in.Manager = false
		return digest(struct{ Kind, Hash string }{"manager", taskCreationDigest(in)})
	}
	if in.Mode != "" || len(in.ContextIDs) > 0 || len(in.InputArtifacts) > 0 || in.DeliveryID != "" || in.Manager {
		return digest(struct {
			Title, Prompt, Adapter, RepositoryPath, Model, ModelProvider, Mode, DeliveryID string
			ContextIDs, InputArtifacts                                                     []string
			Generation                                                                     int64
		}{in.Title, in.Prompt, in.Adapter, in.RepositoryPath, in.Model, in.ModelProvider, in.Mode, in.DeliveryID, in.ContextIDs, in.InputArtifacts, in.FlowGeneration})
	}

	return digest(struct{ Title, Prompt, Adapter, RepositoryPath, Model, ModelProvider string }{in.Title, in.Prompt, in.Adapter, in.RepositoryPath, in.Model, in.ModelProvider})
}

func (s *Store) LookupTaskCreation(ctx context.Context, in core.CreateTaskInput) (core.TaskDetail, bool, error) {
	in, err := normalizeTaskInput(in)
	if err != nil {
		return core.TaskDetail{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return core.TaskDetail{}, false, err
	}
	defer tx.Rollback()
	id, turnID, exists, err := idempotency(ctx, tx, "task", in.IdempotencyKey, taskCreationDigest(in))
	if err != nil || !exists {
		return core.TaskDetail{}, false, err
	}
	detail, err := getTask(ctx, tx, id)
	if err != nil {
		return core.TaskDetail{}, false, err
	}
	detail.SubmittedTurnID = turnID
	return detail, true, tx.Commit()
}

func (s *Store) CreateTask(ctx context.Context, in core.CreateTaskInput) (core.TaskDetail, bool, error) {
	in, normalizeErr := normalizeTaskInput(in)
	if normalizeErr != nil {
		return core.TaskDetail{}, false, normalizeErr
	}
	hash := taskCreationDigest(in)
	var result core.TaskDetail
	var replayed bool
	err := s.transaction(ctx, func(q querier) error {
		id, submittedTurnID, duplicate, err := idempotency(ctx, q, "task", in.IdempotencyKey, hash)
		if err != nil {
			return err
		}
		if duplicate {
			result, err = getTask(ctx, q, id)
			result.SubmittedTurnID = submittedTurnID
			replayed = true
			return err
		}
		if !in.Manager && (in.RepositoryPath == "" || in.BaseCommit == "") {
			return fmt.Errorf("Codex tasks require a frozen repository and commit: %w", core.ErrInvalidState)
		}
		taskID, sessionID, turnID, now := core.NewID("task"), core.NewID("session"), core.NewID("turn"), core.Now()
		_, err = q.ExecContext(ctx, `INSERT INTO tasks(id,title,prompt,status,adapter,session_id,current_turn_id,created_at,updated_at,repository_path,base_commit,model,model_provider) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, taskID, in.Title, in.Prompt, core.StatusQueued, in.Adapter, sessionID, turnID, now, now, in.RepositoryPath, in.BaseCommit, in.Model, in.ModelProvider)
		if err != nil {
			return err
		}
		if in.CollaborationID != "" {
			if err = collaborationVisible(ctx, q, in.CollaborationID); err != nil {
				return err
			}
			if _, err = q.ExecContext(ctx, "INSERT INTO collaboration_tasks(task_id,collaboration_id,created_at) VALUES(?,?,?)", taskID, in.CollaborationID, now); err != nil {
				return err
			}
		}
		if in.Manager {
			if _, err = q.ExecContext(ctx, "INSERT INTO manager_tasks(task_id,project_path) VALUES(?,?)", taskID, in.RepositoryPath); err != nil {
				return err
			}
		}
		for _, artifactID := range in.InputArtifacts {
			if _, err = q.ExecContext(ctx, "INSERT INTO task_inputs(task_id,artifact_id) VALUES(?,?)", taskID, artifactID); err != nil {
				return err
			}
		}
		if in.DeliveryID != "" {
			if _, err = q.ExecContext(ctx, "INSERT INTO workflow_tasks(task_id,delivery_id,generation) VALUES(?,?,?)", taskID, in.DeliveryID, in.FlowGeneration); err != nil {
				return err
			}
		}
		if err = freezeContext(ctx, q, taskID, in); err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, "INSERT INTO sessions(id,task_id,created_at) VALUES(?,?,?)", sessionID, taskID, now); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"title": in.Title, "prompt": in.Prompt, "adapter": in.Adapter, "repository_path": in.RepositoryPath, "base_commit": in.BaseCommit, "model": in.Model, "model_provider": in.ModelProvider})
		if _, err = appendEvent(ctx, q, core.EventInput{TaskID: taskID, SessionID: sessionID, TurnID: turnID, Generation: 1, Type: "task.created", Payload: payload}); err != nil {
			return err
		}
		if err = queueTurn(ctx, q, taskID, sessionID, turnID, in.Prompt, 1, in.Model, in.ModelProvider, in.ReasoningEffort); err != nil {
			return err
		}
		if err = rememberKey(ctx, q, "task", in.IdempotencyKey, hash, taskID, turnID); err != nil {
			return err
		}
		result, err = getTask(ctx, q, taskID)
		result.SubmittedTurnID = turnID
		return err
	})
	return result, replayed, err
}

func (s *Store) CreateTurn(ctx context.Context, taskID string, in core.CreateTurnInput) (core.TaskDetail, bool, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	if !validEffort(in.ReasoningEffort) {
		return core.TaskDetail{}, false, core.ErrInvalidState
	}
	if in.Prompt == "" {
		return core.TaskDetail{}, false, fmt.Errorf("prompt is required: %w", core.ErrInvalidState)
	}
	hash := digest(in.Prompt)
	if len(in.ContextIDs) > 0 {
		hash = digest(struct {
			Prompt     string
			ContextIDs []string
		}{in.Prompt, in.ContextIDs})
	}
	if in.Model != "" || in.ModelProvider != "" {
		hash = digest(struct{ Hash, Model, Provider string }{hash, in.Model, in.ModelProvider})
	}
	if in.ReasoningEffort != "" {
		hash = digest(struct{ Hash, Effort string }{hash, in.ReasoningEffort})
	}
	var result core.TaskDetail
	var replayed bool
	err := s.transaction(ctx, func(q querier) error {
		_, submittedTurnID, duplicate, err := idempotency(ctx, q, "turn:"+taskID, in.IdempotencyKey, hash)
		if err != nil {
			return err
		}
		if duplicate {
			result, err = getTask(ctx, q, taskID)
			result.SubmittedTurnID = submittedTurnID
			replayed = true
			return err
		}
		var sessionID string
		if err = q.QueryRowContext(ctx, "SELECT session_id FROM tasks WHERE id = ?", taskID).Scan(&sessionID); errors.Is(err, sql.ErrNoRows) {
			return core.ErrNotFound
		} else if err != nil {
			return err
		}
		var managed int
		if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_tasks WHERE task_id=?", taskID).Scan(&managed); err != nil {
			return err
		}
		if managed > 0 {
			return fmt.Errorf("managed by delivery flow: %w", core.ErrConflict)
		}
		var active int
		if err = q.QueryRowContext(ctx, "SELECT count(*) FROM turns WHERE session_id = ? AND status IN ('queued','running')", sessionID).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return fmt.Errorf("session already has an active turn: %w", core.ErrConflict)
		}
		turnID := core.NewID("turn")
		if err = queueTurn(ctx, q, taskID, sessionID, turnID, in.Prompt, 1, in.Model, in.ModelProvider, in.ReasoningEffort); err != nil {
			return err
		}
		if err = freezeTurnContext(ctx, q, taskID, turnID, in.ContextIDs); err != nil {
			return err
		}
		if err = rememberKey(ctx, q, "turn:"+taskID, in.IdempotencyKey, hash, taskID, turnID); err != nil {
			return err
		}
		result, err = getTask(ctx, q, taskID)
		result.SubmittedTurnID = turnID
		return err
	})
	return result, replayed, err
}

func queueTurn(ctx context.Context, q querier, taskID, sessionID, turnID, prompt string, generation int64, selection ...string) error {
	if err := taskVisible(ctx, q, taskID); err != nil {
		return err
	}
	var model, provider, runtimeJSON string
	if err := q.QueryRowContext(ctx, "SELECT t.model,t.model_provider,s.runtime_json FROM tasks t JOIN sessions s ON s.task_id=t.id WHERE t.id=?", taskID).Scan(&model, &provider, &runtimeJSON); err != nil {
		return err
	}
	var runtime core.RuntimeInfo
	if err := json.Unmarshal([]byte(runtimeJSON), &runtime); err != nil {
		return err
	}
	if runtime.Model != "" {
		model = runtime.Model
	}
	if runtime.ModelProvider != "" {
		provider = runtime.ModelProvider
	}
	if len(selection) > 0 && selection[0] != "" {
		model = strings.TrimSpace(selection[0])
	}
	if len(selection) > 1 && selection[1] != "" {
		provider = strings.TrimSpace(selection[1])
	}
	var effort string
	if err := q.QueryRowContext(ctx, "SELECT COALESCE((SELECT reasoning_effort FROM turns WHERE task_id=? ORDER BY created_seq DESC LIMIT 1),'')", taskID).Scan(&effort); err != nil {
		return err
	}
	if effort == "" {
		effort = runtime.ReasoningEffort
	}
	if len(selection) > 0 && selection[0] != "" && selection[0] != runtime.Model {
		effort = ""
	}
	if len(selection) > 1 && selection[1] != "" && selection[1] != runtime.ModelProvider {
		effort = ""
	}
	if len(selection) > 2 && selection[2] != "" {
		effort = selection[2]
	}
	payload, _ := json.Marshal(map[string]string{"prompt": prompt, "model": model, "model_provider": provider, "reasoning_effort": effort})
	if _, err := appendEvent(ctx, q, core.EventInput{TaskID: taskID, SessionID: sessionID, TurnID: turnID, Generation: generation, Type: "turn.queued", Payload: payload}); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO jobs(id,task_id,session_id,turn_id,generation,prompt,status,created_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("job"), taskID, sessionID, turnID, generation, prompt, core.StatusQueued, core.Now())
	return err
}

const taskColumns = "id,title,prompt,status,adapter,session_id,current_turn_id,created_at,updated_at,last_event_seq,repository_path,base_commit,model,model_provider,COALESCE((SELECT collaboration_id FROM collaboration_tasks ct WHERE ct.task_id=tasks.id),''),COALESCE((SELECT pinned_at FROM pins WHERE kind='task' AND entity_id=tasks.id),'')"

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (core.Task, error) {
	var t core.Task
	err := row.Scan(&t.ID, &t.Title, &t.Prompt, &t.Status, &t.Adapter, &t.SessionID, &t.CurrentTurnID, &t.CreatedAt, &t.UpdatedAt, &t.LastEventSeq, &t.RepositoryPath, &t.BaseCommit, &t.Model, &t.ModelProvider, &t.CollaborationID, &t.PinnedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	return t, err
}

func (s *Store) ListTasks(ctx context.Context) ([]core.Task, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id NOT IN(SELECT task_id FROM deleted_tasks) ORDER BY COALESCE((SELECT pinned_at FROM pins WHERE kind='task' AND entity_id=tasks.id),'') DESC,updated_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []core.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) GetTask(ctx context.Context, id string) (core.TaskDetail, error) {
	// A read transaction provides one coherent snapshot of all projections.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return core.TaskDetail{}, err
	}
	defer tx.Rollback()
	result, err := getTask(ctx, tx, id)
	if err != nil {
		return core.TaskDetail{}, err
	}
	return result, tx.Commit()
}

func getTask(ctx context.Context, q querier, id string) (core.TaskDetail, error) {
	d := core.TaskDetail{Turns: []core.Turn{}, Messages: []core.Message{}}
	if err := taskVisible(ctx, q, id); err != nil {
		return d, err
	}
	var err error
	d.Task, err = scanTask(q.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM tasks WHERE id = ?", id))
	if err != nil {
		return d, err
	}
	d.CollaborationID = d.Task.CollaborationID
	var runtimeJSON string
	if err = q.QueryRowContext(ctx, "SELECT id,task_id,created_at,runtime_json FROM sessions WHERE task_id = ?", id).Scan(&d.Session.ID, &d.Session.TaskID, &d.Session.CreatedAt, &runtimeJSON); err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(runtimeJSON), &d.Session.Runtime); err != nil {
		return d, err
	}
	rows, err := q.QueryContext(ctx, "SELECT id,task_id,session_id,prompt,status,generation,created_at,started_at,ended_at,error_code,error,native_turn_id,usage_json,model,model_provider,reasoning_effort FROM turns WHERE task_id = ? ORDER BY created_seq", id)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var t core.Turn
		var usageJSON string
		if err = rows.Scan(&t.ID, &t.TaskID, &t.SessionID, &t.Prompt, &t.Status, &t.Generation, &t.CreatedAt, &t.StartedAt, &t.EndedAt, &t.ErrorCode, &t.Error, &t.NativeTurnID, &usageJSON, &t.Model, &t.ModelProvider, &t.ReasoningEffort); err != nil {
			rows.Close()
			return d, err
		}
		if err = json.Unmarshal([]byte(usageJSON), &t.Usage); err != nil {
			rows.Close()
			return d, err
		}
		d.Turns = append(d.Turns, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return d, err
	}
	rows, err = q.QueryContext(ctx, "SELECT id,session_id,turn_id,role,status,segments FROM messages WHERE session_id = ? ORDER BY created_seq,position", d.Session.ID)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var m core.Message
		var segments string
		if err = rows.Scan(&m.ID, &m.SessionID, &m.TurnID, &m.Role, &m.Status, &segments); err != nil {
			return d, err
		}
		if err = json.Unmarshal([]byte(segments), &m.Segments); err != nil {
			return d, err
		}
		d.Messages = append(d.Messages, m)
	}
	return d, rows.Err()
}

func (s *Store) WorkerHeartbeat(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("worker id required: %w", core.ErrInvalidState)
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO workers(id,last_seen_at) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET last_seen_at=excluded.last_seen_at", id, core.Now())
	return err
}

func (s *Store) GetWorkerStatus(ctx context.Context) (*core.WorkerStatus, error) {
	var w core.WorkerStatus
	err := s.db.QueryRowContext(ctx, "SELECT id,last_seen_at FROM workers ORDER BY last_seen_at DESC LIMIT 1").Scan(&w.ID, &w.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lastSeen, err := time.Parse(time.RFC3339Nano, w.LastSeenAt)
	if err != nil {
		return nil, err
	}
	w.Online = time.Since(lastSeen) < 15*time.Second
	return &w, nil
}

func validEffort(value string) bool {
	if len(value) > 32 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
