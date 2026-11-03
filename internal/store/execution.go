package store

import (
	"context"
	"encoding/json"
	"fmt"

	"blux/internal/core"
)

func projectRuntime(ctx context.Context, q querier, event core.Event) error {
	var raw, baseCommit string
	if err := q.QueryRowContext(ctx, "SELECT s.runtime_json,t.base_commit FROM sessions s JOIN tasks t ON t.id=s.task_id WHERE s.id=?", event.SessionID).Scan(&raw, &baseCommit); err != nil {
		return err
	}
	var current, incoming core.RuntimeInfo
	if err := json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if err := json.Unmarshal(event.Payload, &incoming); err != nil {
		return err
	}
	if event.Type == "workspace.prepared" {
		if incoming.BaseCommit != baseCommit {
			return fmt.Errorf("workspace does not match frozen commit: %w", core.ErrConflict)
		}
		for _, field := range []struct{ old, new string }{{current.WorkspacePath, incoming.WorkspacePath}, {current.WorktreePath, incoming.WorktreePath}, {current.BranchName, incoming.BranchName}, {current.BaseCommit, incoming.BaseCommit}} {
			if field.old != "" && field.old != field.new {
				return fmt.Errorf("workspace identity changed: %w", core.ErrConflict)
			}
		}
		current.WorkspacePath = incoming.WorkspacePath
		current.WorktreePath = incoming.WorktreePath
		current.BranchName = incoming.BranchName
		current.BaseCommit = incoming.BaseCommit
	} else {
		if incoming.NativeThreadID == "" {
			return fmt.Errorf("native thread identity required: %w", core.ErrInvalidState)
		}
		var requestedModel, requestedProvider string
		if err := q.QueryRowContext(ctx, "SELECT model,model_provider FROM turns WHERE id=?", event.TurnID).Scan(&requestedModel, &requestedProvider); err != nil {
			return err
		}
		if current.Model != "" && incoming.Model != "" && current.Model != incoming.Model {
			if requestedModel == "" || requestedModel != incoming.Model {
				return fmt.Errorf("unexpected model switch: %w", core.ErrConflict)
			}
			current.ContextUsage = nil
		}
		if current.ModelProvider != "" && incoming.ModelProvider != "" && current.ModelProvider != incoming.ModelProvider {
			if requestedProvider != incoming.ModelProvider {
				return fmt.Errorf("unexpected provider switch: %w", core.ErrConflict)
			}
			current.ContextUsage = nil
		}
		for _, field := range []struct{ old, new string }{{current.NativeThreadID, incoming.NativeThreadID}} {
			if field.old != "" && field.new != "" && field.old != field.new {
				return fmt.Errorf("agent session configuration changed: %w", core.ErrConflict)
			}
		}
		current.NativeThreadID = incoming.NativeThreadID
		current.ReasoningEffort = incoming.ReasoningEffort
		if incoming.ReasoningEffort != "" {
			if _, err := q.ExecContext(ctx, "UPDATE turns SET reasoning_effort=? WHERE id=? AND reasoning_effort=''", incoming.ReasoningEffort, event.TurnID); err != nil {
				return err
			}
		}
		if incoming.Model != "" {
			current.Model = incoming.Model
		}
		if incoming.ModelProvider != "" {
			current.ModelProvider = incoming.ModelProvider
		}
		if incoming.CLIVersion != "" {
			current.CLIVersion = incoming.CLIVersion
		}
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE sessions SET runtime_json=? WHERE id=?", string(encoded), event.SessionID)
	return err
}
