package store

import (
	"blux/internal/core"
	"context"
)

type Pin struct {
	Pinned   bool   `json:"pinned"`
	PinnedAt string `json:"pinned_at"`
}

// PUT semantics: repeating the current value preserves the original pin order.
func (s *Store) SetPin(ctx context.Context, kind, id string, pinned bool) (result Pin, err error) {
	err = s.transaction(ctx, func(q querier) error {
		switch kind {
		case "task":
			if err := taskVisible(ctx, q, id); err != nil {
				return err
			}
		case "collaboration":
			if err := collaborationVisible(ctx, q, id); err != nil {
				return err
			}
		default:
			return core.ErrInvalidState
		}
		if pinned {
			if _, err := q.ExecContext(ctx, "INSERT OR IGNORE INTO pins(kind,entity_id,pinned_at) VALUES(?,?,?)", kind, id, core.Now()); err != nil {
				return err
			}
			if err := q.QueryRowContext(ctx, "SELECT pinned_at FROM pins WHERE kind=? AND entity_id=?", kind, id).Scan(&result.PinnedAt); err != nil {
				return err
			}
		} else if _, err := q.ExecContext(ctx, "DELETE FROM pins WHERE kind=? AND entity_id=?", kind, id); err != nil {
			return err
		}
		result.Pinned = pinned
		return nil
	})
	return
}
