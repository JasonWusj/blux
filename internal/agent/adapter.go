// Package agent defines the execution boundary shared by local CLI adapters.
package agent

import (
	"context"
	"encoding/json"
	"errors"
)

var ErrUnsupported = errors.New("adapter capability is not available")

type Capabilities struct {
	Streaming bool `json:"streaming"`
	Resume    bool `json:"resume"`
	Steer     bool `json:"steer"`
	Interrupt bool `json:"interrupt"`
}

type SessionConfig struct {
	ReasoningEffort        string
	Manager                bool
	MCPCommand             string
	MCPArgs                []string
	AdditionalInstructions string
	SessionID              string
	Workspace              string
	ThreadID               string
	Model                  string
	ModelProvider          string
	ArtifactsPath          string
	AgentPath              string
	// RestrictNetwork denies network access to commands the agent runs. The
	// agent's own calls to its model provider are not affected.
	RestrictNetwork bool
}

type TurnRequest struct {
	TurnID     string
	Generation int64
	Prompt     string
}

type Update struct {
	Type    string
	Payload json.RawMessage
	Err     error
}

type AgentAdapter interface {
	Name() string
	Capabilities() Capabilities
	Start(context.Context, SessionConfig) (Session, error)
	Resume(context.Context, SessionConfig) (Session, error)
}

type Session interface {
	Send(context.Context, TurnRequest) (<-chan Update, error)
	Steer(context.Context, string, string) error
	Interrupt(context.Context, string) error
	Close() error
}
