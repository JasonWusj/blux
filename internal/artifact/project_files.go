package artifact

import (
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (s Service) saveBlob(b []byte) (string, error) {
	sum := sha256.Sum256(b)
	key := hex.EncodeToString(sum[:])
	path := s.Blob(key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return key, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".blob-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		if _, e := os.Stat(path); e != nil {
			return "", err
		}
	}
	return key, nil
}

func projectArtifactID(snapshot, path string) string {
	sum := sha256.Sum256([]byte(snapshot + "\x00project\x00" + path))
	return "artifact_" + hex.EncodeToString(sum[:16])
}

// Keep project paths together so HTML relative assets resolve in the same
// immutable version. Dedicated artifacts retain their existing paths and IDs.
func projectPrefix(v store.Snapshot) string {
	for n := 1; ; n++ {
		prefix := "project"
		if n > 1 {
			prefix = fmt.Sprintf("project-%d", n)
		}
		conflict := false
		for _, a := range v.Artifacts {
			if a.Path == prefix {
				conflict = true
				break
			}
			if strings.HasPrefix(a.Path, prefix+"/") {
				path := strings.TrimPrefix(a.Path, prefix+"/")
				if a.ID != projectArtifactID(v.ID, path) {
					conflict = true
					break
				}
			}
		}
		if !conflict {
			return prefix
		}
	}
}

// Read blobs from the saved Git commit, never from the mutable live worktree.
func (s Service) projectArtifacts(ctx context.Context, repo, base string, v store.Snapshot) ([]store.ArtifactVersion, error) {
	files, err := gitBytes(ctx, repo, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "--diff-filter=ACMT", "-z", base, v.Commit, "--")
	if err != nil {
		return nil, err
	}
	out := []store.ArtifactVersion{}
	prefix := projectPrefix(v)
	for _, raw := range bytes.Split(files, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := string(raw)
		if name == ".agent" || strings.HasPrefix(name, ".agent/") || name == ".git" || strings.HasPrefix(name, ".git/") {
			continue
		}
		record, err := gitBytes(ctx, repo, "ls-tree", "-z", v.Commit, "--", ":(literal)"+name)
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(strings.SplitN(string(record), "\t", 2)[0])
		if len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" {
			continue
		}
		sizeText, err := git(ctx, repo, "cat-file", "-s", fields[2])
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseInt(sizeText, 10, 64)
		if err != nil {
			return nil, err
		}
		if size > 64<<20 {
			return nil, &core.ExecutionError{Code: "artifact_too_large", Message: "项目产物超过 64 MiB，未保存产物快照。"}
		}
		b, err := gitBytes(ctx, repo, "cat-file", "blob", fields[2])
		if err != nil {
			return nil, err
		}
		if int64(len(b)) != size {
			return nil, fmt.Errorf("project blob size mismatch")
		}
		hash, err := s.saveBlob(b)
		if err != nil {
			return nil, err
		}
		out = append(out, store.ArtifactVersion{ID: projectArtifactID(v.ID, name), Path: prefix + "/" + name, Hash: hash, Size: size, MIME: mime.TypeByExtension(filepath.Ext(name))})
	}
	return out, nil
}

// BackfillProjectArtifacts repairs the derived artifact index of completed
// snapshots. Commit/diff, existing artifact versions and frozen inputs stay fixed.
func (s Service) BackfillProjectArtifacts(ctx context.Context, taskID string) (int, error) {
	d, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return 0, err
	}
	versions, err := s.Store.Snapshots(ctx, taskID)
	if err != nil {
		return 0, err
	}
	completed := map[string]bool{}
	for _, turn := range d.Turns {
		completed[turn.ID] = turn.Status == "completed"
	}
	repo, err := (workspace.Manager{DataDir: s.DataDir}).Resolve(ctx, d.Task.RepositoryPath, d.Task.BaseCommit)
	if err != nil {
		return 0, err
	}
	if d.Session.Runtime.WorktreePath != "" {
		if _, e := git(ctx, d.Session.Runtime.WorktreePath, "rev-parse", "--git-dir"); e == nil {
			repo.Root = d.Session.Runtime.WorktreePath
		}
	}
	count := 0
	for _, v := range versions {
		if v.Commit == "" || !completed[v.TurnID] {
			continue
		}
		files, err := s.projectArtifacts(ctx, repo.Root, d.Task.BaseCommit, v)
		if err != nil {
			return count, err
		}
		n, err := s.Store.AddSnapshotArtifacts(ctx, v, files)
		if err != nil {
			return count, err
		}
		count += n
	}
	return count, nil
}
