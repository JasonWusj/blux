package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrStaleGeneration = errors.New("stale generation")
	ErrInvalidState    = errors.New("invalid state")
)

type ExecutionError struct {
	Code    string
	Message string
	Err     error
}

func (e *ExecutionError) Error() string { return e.Message }
func (e *ExecutionError) Unwrap() error { return e.Err }

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type CreateTaskInput struct {
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	CollaborationID string   `json:"collaboration_id,omitempty"`
	Manager         bool     `json:"manager"`
	DeliveryID      string   `json:"delivery_id"`
	FlowGeneration  int64    `json:"flow_generation"`
	InputArtifacts  []string `json:"input_artifacts"`
	Mode            string   `json:"mode"`
	ContextIDs      []string `json:"context_ids"`
	Title           string   `json:"title"`
	Prompt          string   `json:"prompt"`
	IdempotencyKey  string   `json:"idempotency_key"`
	Adapter         string   `json:"adapter"`
	RepositoryPath  string   `json:"repository_path"`
	BaseCommit      string   `json:"base_commit"`
	Model           string   `json:"model"`
	ModelProvider   string   `json:"model_provider"`
}

type CreateTurnInput struct {
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	Model           string   `json:"model,omitempty"`
	ModelProvider   string   `json:"model_provider,omitempty"`
	ContextIDs      []string `json:"context_ids,omitempty"`
	Prompt          string   `json:"prompt"`
	IdempotencyKey  string   `json:"idempotency_key"`
}

type Task struct {
	PinnedAt        string `json:"pinned_at,omitempty"`
	CollaborationID string `json:"collaboration_id,omitempty"`
	ID              string `json:"id"`
	Title           string `json:"title"`
	Prompt          string `json:"prompt"`
	Status          string `json:"status"`
	Adapter         string `json:"adapter"`
	SessionID       string `json:"session_id"`
	CurrentTurnID   string `json:"current_turn_id"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	LastEventSeq    int64  `json:"last_event_seq"`
	RepositoryPath  string `json:"repository_path"`
	BaseCommit      string `json:"base_commit"`
	Model           string `json:"model"`
	ModelProvider   string `json:"model_provider"`
}

type ContextUsage struct {
	UsedTokens   int64  `json:"used_tokens"`
	WindowTokens int64  `json:"window_tokens,omitempty"`
	Model        string `json:"model,omitempty"`
	Source       string `json:"source"`
	ObservedAt   string `json:"observed_at"`
}

type RuntimeInfo struct {
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	ContextUsage    *ContextUsage `json:"context_usage,omitempty"`
	WorkspacePath   string        `json:"workspace_path"`
	WorktreePath    string        `json:"worktree_path"`
	BranchName      string        `json:"branch_name"`
	BaseCommit      string        `json:"base_commit"`
	NativeThreadID  string        `json:"native_thread_id"`
	Model           string        `json:"model"`
	ModelProvider   string        `json:"model_provider"`
	CLIVersion      string        `json:"cli_version"`
}

type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Diff string `json:"diff"`
}

type Session struct {
	ID        string      `json:"id"`
	TaskID    string      `json:"task_id"`
	CreatedAt string      `json:"created_at"`
	Runtime   RuntimeInfo `json:"runtime"`
}

type Turn struct {
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	Model           string     `json:"model,omitempty"`
	ModelProvider   string     `json:"model_provider,omitempty"`
	ID              string     `json:"id"`
	TaskID          string     `json:"task_id"`
	SessionID       string     `json:"session_id"`
	Prompt          string     `json:"prompt"`
	Status          string     `json:"status"`
	Generation      int64      `json:"generation"`
	CreatedAt       string     `json:"created_at"`
	StartedAt       string     `json:"started_at,omitempty"`
	EndedAt         string     `json:"ended_at,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	Error           string     `json:"error,omitempty"`
	NativeTurnID    string     `json:"native_turn_id,omitempty"`
	Usage           TokenUsage `json:"usage"`
}

type Segment struct {
	Type       string       `json:"type"`
	Status     string       `json:"status,omitempty"`
	Text       string       `json:"text,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
	Input      string       `json:"input,omitempty"`
	Output     string       `json:"output,omitempty"`
	ItemID     string       `json:"item_id,omitempty"`
	Changes    []FileChange `json:"changes,omitempty"`
}

type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	Segments  []Segment `json:"segments"`
}

type TaskDetail struct {
	CollaborationID string    `json:"collaboration_id,omitempty"`
	Manager         bool      `json:"manager"`
	DeliveryID      string    `json:"delivery_id,omitempty"`
	Task            Task      `json:"task"`
	Session         Session   `json:"session"`
	Turns           []Turn    `json:"turns"`
	Messages        []Message `json:"messages"`
	SubmittedTurnID string    `json:"submitted_turn_id,omitempty"`
}

type Event struct {
	ID            string          `json:"id"`
	SchemaVersion int             `json:"schema_version"`
	TaskID        string          `json:"task_id"`
	SessionID     string          `json:"session_id"`
	TurnID        string          `json:"turn_id"`
	Generation    int64           `json:"generation"`
	Seq           int64           `json:"seq"`
	Type          string          `json:"type"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
}

type EventInput struct {
	ID         string          `json:"id,omitempty"`
	TaskID     string          `json:"task_id"`
	SessionID  string          `json:"session_id"`
	TurnID     string          `json:"turn_id"`
	Generation int64           `json:"generation"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
}

type Job struct {
	ReasoningEffort string      `json:"reasoning_effort,omitempty"`
	Manager         bool        `json:"manager"`
	ID              string      `json:"id"`
	TaskID          string      `json:"task_id"`
	SessionID       string      `json:"session_id"`
	TurnID          string      `json:"turn_id"`
	Generation      int64       `json:"generation"`
	Prompt          string      `json:"prompt"`
	Adapter         string      `json:"adapter"`
	RepositoryPath  string      `json:"repository_path"`
	BaseCommit      string      `json:"base_commit"`
	Model           string      `json:"model"`
	ModelProvider   string      `json:"model_provider"`
	Runtime         RuntimeInfo `json:"runtime"`
}

type WorkerStatus struct {
	ID         string `json:"id"`
	LastSeenAt string `json:"last_seen_at"`
	Online     bool   `json:"online"`
}

func NewID(prefix string) string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("generate random identifier: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(data[:])
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func IsTerminal(status string) bool {
	return status == StatusCompleted || status == StatusFailed
}

// OutputTail keeps projections bounded while the event log retains source deltas.
func OutputTail(s string) string {
	const limit = 64 << 10
	if len(s) <= limit {
		return s
	}
	return "[输出过长，仅显示末尾 64 KiB]\n" + strings.ToValidUTF8(s[len(s)-limit:], "�")
}
