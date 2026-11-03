package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CleanupRetiredData resumes filesystem work recorded atomically by migrations.
// Only generated paths under the data directory can be removed, never a source repo.
func (s *Store) CleanupRetiredData(ctx context.Context, dataDir string) error {
	canonicalData, e := filepath.EvalSymlinks(dataDir)
	if e != nil {
		return e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT kind,entry_id FROM pending_cleanup")
	if e != nil {
		return e
	}
	type entry struct{ kind, id string }
	var entries []entry
	for rows.Next() {
		var v entry
		if e = rows.Scan(&v.kind, &v.id); e != nil {
			rows.Close()
			return e
		}
		entries = append(entries, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	for _, v := range entries {
		if !valid.MatchString(v.id) {
			return fmt.Errorf("invalid retired data identifier")
		}
		var root string
		switch v.kind {
		case "workspaces", "deliveries", "integrations":
			root = filepath.Join(dataDir, v.kind)
		case "blobs":
			root = filepath.Join(dataDir, "artifacts", "blobs")
		default:
			return fmt.Errorf("unknown cleanup kind")
		}
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			rel, err := filepath.Rel(canonicalData, canonicalRoot)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("retired data path leaves the data directory")
			}
		}
		remove := true
		if v.kind == "blobs" {
			var n int
			if e = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifact_versions WHERE hash=?", v.id).Scan(&n); e != nil {
				return e
			}
			remove = n == 0
		}
		if remove {
			if e = os.RemoveAll(filepath.Join(root, v.id)); e != nil {
				return e
			}
		}
		if _, e = s.db.ExecContext(ctx, "DELETE FROM pending_cleanup WHERE kind=? AND entry_id=?", v.kind, v.id); e != nil {
			return e
		}
	}
	return nil
}
