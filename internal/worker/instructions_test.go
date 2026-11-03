package worker

import (
	"blux/internal/agent"
	"blux/internal/contextpack"
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real Worker start/resume path. This session only records the
// configuration boundary; model behavior is checked separately with real Codex.
type instructionAdapter struct{ configs []agent.SessionConfig }

func (*instructionAdapter) Name() string                     { return "codex" }
func (*instructionAdapter) Capabilities() agent.Capabilities { return agent.Capabilities{Resume: true} }
func (a *instructionAdapter) Start(_ context.Context, c agent.SessionConfig) (agent.Session, error) {
	a.configs = append(a.configs, c)
	return instructionSession{}, nil
}
func (a *instructionAdapter) Resume(ctx context.Context, c agent.SessionConfig) (agent.Session, error) {
	return a.Start(ctx, c)
}

type instructionSession struct{ waitingSession }

func (instructionSession) Send(context.Context, agent.TurnRequest) (<-chan agent.Update, error) {
	out := make(chan agent.Update, 1)
	out <- agent.Update{Type: "agent.session.ready", Payload: json.RawMessage(`{"native_thread_id":"instruction-thread","model":"test-model"}`)}
	close(out)
	return out, nil
}
func (instructionSession) Close() error { return nil }

func TestCurrentTurnIntentSurvivesLegacyContextAndResume(t *testing.T) {
	for _, manager := range []bool{false, true} {
		t.Run(map[bool]string{false: "task", true: "manager"}[manager], func(t *testing.T) {
			ctx := context.Background()
			directory := t.TempDir()
			file := filepath.Join(directory, "blux.db")
			db, err := store.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			repo := t.TempDir()
			for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@local", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"}} {
				if b, e := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); e != nil {
					t.Fatal(string(b), e)
				}
			}
			base, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			baseCommit := strings.TrimSpace(string(base))
			if manager {
				baseCommit = ""
			}
			d, _, err := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: repo, BaseCommit: baseCommit, Prompt: "你好", IdempotencyKey: "first", Adapter: "codex", Manager: manager})
			if err != nil {
				t.Fatal(err)
			}
			legacy := store.ContextSnapshot{Mode: contextpack.Mode{ID: "explore", Version: 1, Instructions: "Always write Result.md after each message."}, Entries: []store.ContextEntry{}}
			raw, _ := json.Marshal(legacy)
			connection, err := sql.Open("sqlite", file)
			if err != nil {
				t.Fatal(err)
			}
			_, err = connection.ExecContext(ctx, "UPDATE task_context SET snapshot_json=? WHERE task_id=?", string(raw), d.Task.ID)
			connection.Close()
			if err != nil {
				t.Fatal(err)
			}
			a := &instructionAdapter{}
			w := Worker{Store: db, DataDir: directory, Adapter: a, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			turnCount := 2
			if manager {
				turnCount = 1
			}
			for i := 0; i < turnCount; i++ {
				if i == 1 {
					if _, _, err = db.CreateTurn(ctx, d.Task.ID, core.CreateTurnInput{Prompt: "谢谢", IdempotencyKey: "second"}); err != nil {
						t.Fatal(err)
					}
				}
				job, err := db.ClaimNext(ctx, "instructions-test")
				if err != nil || job == nil {
					t.Fatal(job, err)
				}
				if err = w.execute(ctx, *job); err != nil {
					t.Fatal(err)
				}
				config := a.configs[i]
				if !strings.HasSuffix(config.AdditionalInstructions, currentTurnIntent) {
					t.Fatal("current-turn policy was lost behind frozen mode or manager instructions")
				}
				if i == 0 && config.ThreadID != "" || i == 1 && config.ThreadID != "instruction-thread" {
					t.Fatalf("incorrect start/resume identity: %q", config.ThreadID)
				}
				if !manager && (!strings.Contains(config.AdditionalInstructions, legacy.Mode.Instructions) || strings.Contains(config.AdditionalInstructions, "Read .agent/inputs.json")) {
					t.Fatal("legacy context was rewritten or empty inputs require a read")
				}
				frozen, err := db.ExecutionContext(ctx, d.Task.ID, job.TurnID)
				if err != nil || frozen.Mode.Version != 1 {
					t.Fatalf("changed immutable input: %+v %v", frozen, err)
				}
			}
		})
	}
}

func TestTaskInstructionsKeepRelevantPinnedInputsAvailable(t *testing.T) {
	pack := store.ContextSnapshot{Entries: []store.ContextEntry{{ID: "selected-skill", Kind: "skill"}}}
	runtime := core.RuntimeInfo{WorkspacePath: "/task workspace"}
	text := taskInstructions(pack, runtime, 1)
	if !strings.Contains(text, "context.json") || !strings.Contains(text, "inputs.json") || !strings.Contains(text, "exact files") || !strings.Contains(text, "only when needed") {
		t.Fatal("task context or pinned versions are unavailable")
	}
	text = taskInstructions(store.ContextSnapshot{}, runtime, 0)
	if strings.Contains(text, "inputs.json") || !strings.Contains(text, "no need to read context.json") {
		t.Fatal("empty inputs still require initialization reads")
	}
}
