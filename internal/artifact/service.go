package artifact

import (
	"blux/internal/core"
	"blux/internal/platform"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"errors"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Service struct {
	Store   *store.Store
	DataDir string
}

func gitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prefix := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "core.fsmonitor=false", "-c", "user.name=Blux", "-c", "user.email=blux@localhost", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TRACE2_EVENT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	return cmd.Output()
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	b, err := gitBytes(ctx, dir, args...)
	return strings.TrimSpace(string(b)), err
}
func (s Service) Blob(hash string) string {
	return filepath.Join(s.DataDir, "artifacts", "blobs", hash)
}
func (s Service) Capture(ctx context.Context, j core.Job, rt core.RuntimeInfo) error {
	v := store.Snapshot{ID: core.NewID("snapshot"), TaskID: j.TaskID, TurnID: j.TurnID, Generation: j.Generation, CreatedAt: core.Now(), Artifacts: []store.ArtifactVersion{}}
	if rt.WorktreePath != "" {
		if _, err := git(ctx, rt.WorktreePath, "add", "-A"); err != nil {
			return err
		}
		dirty, err := git(ctx, rt.WorktreePath, "diff", "--cached", "--name-only")
		if err != nil {
			return err
		}
		if dirty != "" {
			if _, err = git(ctx, rt.WorktreePath, "commit", "-m", "Blux task "+j.TaskID+" turn "+j.TurnID); err != nil {
				return err
			}
		}
		v.Commit, err = git(ctx, rt.WorktreePath, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		v.Diff, err = git(ctx, rt.WorktreePath, "diff", "--no-ext-diff", "--no-textconv", "--binary", j.BaseCommit, v.Commit)
		if err != nil {
			return err
		}
	}
	root := filepath.Join(rt.WorkspacePath, "artifacts")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > 64<<20 {
			return &core.ExecutionError{Code: "artifact_too_large", Message: "单个产物超过 64 MiB，未保存快照。"}
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(f, (64<<20)+1))
		f.Close()
		if e != nil {
			return e
		}
		if len(b) > 64<<20 {
			return errors.New("artifact changed while reading")
		}
		key, e := s.saveBlob(b)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, path)
		v.Artifacts = append(v.Artifacts, store.ArtifactVersion{ID: core.NewID("artifact"), Path: filepath.ToSlash(rel), Hash: key, Size: int64(len(b)), MIME: mime.TypeByExtension(filepath.Ext(path))})
		return nil
	})
	if err != nil {
		return err
	}
	if rt.WorktreePath != "" {
		files, err := s.projectArtifacts(ctx, rt.WorktreePath, j.BaseCommit, v)
		if err != nil {
			return err
		}
		v.Artifacts = append(v.Artifacts, files...)
	}
	return s.Store.SaveSnapshot(ctx, v)
}

// Accept validates an immutable snapshot in a separate integration worktree.
// The source working tree is touched only by the final explicit fast-forward.
func (s Service) Accept(ctx context.Context, snapshotID, key string) (a store.Acceptance, err error) {
	if key == "" {
		return a, core.ErrInvalidState
	}
	if err = os.MkdirAll(filepath.Join(s.DataDir, "locks"), 0700); err != nil {
		return
	}
	unlock, e := platform.Lock(filepath.Join(s.DataDir, "locks", "accept.lock"))
	if e != nil {
		return a, core.ErrConflict
	}
	defer unlock()
	v, err := s.Store.Snapshot(ctx, snapshotID)
	if err != nil {
		return
	}
	id := v.TaskID + ":" + key
	a, e = s.Store.GetAcceptance(ctx, id)
	if e == nil {
		if a.SnapshotID != snapshotID {
			return a, core.ErrConflict
		}
		if a.Status == "accepted" || a.Status == "blocked" {
			return a, nil
		}
	} else if !errors.Is(e, core.ErrNotFound) {
		return a, e
	}
	d, err := s.Store.GetTask(ctx, v.TaskID)
	if err != nil {
		return
	}
	if managed, e := s.Store.WorkflowDelivery(ctx, v.TaskID); e != nil {
		return a, e
	} else if managed != "" {
		return a, core.ErrConflict
	}
	if d.Task.CurrentTurnID != v.TurnID || d.Task.Status != "completed" || v.Commit == "" {
		return a, core.ErrConflict
	}
	m := workspace.Manager{DataDir: s.DataDir}
	repo, err := m.Resolve(ctx, d.Task.RepositoryPath, d.Task.BaseCommit)
	if err != nil {
		return a, err
	}
	if m.IsSnapshot(d.Task.RepositoryPath, repo.Root) {
		recovering := a.Status == "applying" && a.ResultCommit == v.Commit
		a = store.Acceptance{ID: id, SnapshotID: v.ID, TaskID: v.TaskID, Status: "applying", SourceHead: d.Task.BaseCommit, ResultCommit: v.Commit}
		if err = s.Store.SaveAcceptance(ctx, a); err != nil {
			return a, err
		}
		if err = m.ApplySnapshot(ctx, d.Task.RepositoryPath, d.Task.BaseCommit, v.Commit, recovering); err != nil {
			a.Status = "blocked"
			a.Reason = err.Error()
		} else {
			a.Status = "accepted"
		}
		return a, s.Store.SaveAcceptance(ctx, a)
	}
	src := d.Task.RepositoryPath
	if a.ResultCommit != "" {
		head, e := git(ctx, src, "rev-parse", "HEAD")
		if e == nil && head == a.ResultCommit {
			a.Status = "accepted"
			return a, s.Store.SaveAcceptance(ctx, a)
		}
		if e == nil && head == a.SourceHead && a.Status == "ready" {
			status, se := git(ctx, src, "status", "--porcelain=v1", "--untracked-files=all")
			if se == nil && status == "" {
				if _, e = git(ctx, src, "merge", "--ff-only", a.ResultCommit); e == nil {
					a.Status = "accepted"
					return a, s.Store.SaveAcceptance(ctx, a)
				}
			}
		}

	}
	a = store.Acceptance{ID: id, SnapshotID: v.ID, TaskID: v.TaskID, Status: "integrating", IntegrationPath: filepath.Join(s.DataDir, "integrations", v.ID)}
	block := func(reason string) (store.Acceptance, error) {
		a.Status = "blocked"
		a.Reason = reason
		return a, s.Store.SaveAcceptance(ctx, a)
	}
	status, e := git(ctx, src, "status", "--porcelain=v1", "--untracked-files=all")
	if e != nil {
		return a, e
	}
	if status != "" {
		return block("源项目存在未提交变更，请先处理，再使用新的接收请求。")
	}
	a.SourceHead, err = git(ctx, src, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	if err = s.Store.SaveAcceptance(ctx, a); err != nil {
		return
	}
	if _, e = os.Stat(a.IntegrationPath); os.IsNotExist(e) {
		if _, err = git(ctx, src, "worktree", "add", "--detach", a.IntegrationPath, a.SourceHead); err != nil {
			return block("无法创建临时整合 worktree。")
		}

		if _, err = git(ctx, a.IntegrationPath, "merge", "--no-ff", "--no-commit", v.Commit); err != nil {
			return block("合并冲突，已保留整合区。源项目未改变。")
		}
	} else {
		status, se := git(ctx, a.IntegrationPath, "status", "--porcelain=v1")
		_, ve := git(ctx, a.IntegrationPath, "merge-base", "--is-ancestor", v.Commit, "HEAD")
		_, be := git(ctx, a.IntegrationPath, "merge-base", "--is-ancestor", a.SourceHead, "HEAD")
		if se != nil || status != "" || ve != nil || be != nil {
			return block("已有整合区尚未解决或不包含当前版本，请检查文件和冲突后再操作。")
		}
	}
	status, err = git(ctx, a.IntegrationPath, "status", "--porcelain=v1")
	if err != nil {
		return
	}
	if _, e = git(ctx, a.IntegrationPath, "rev-parse", "--verify", "MERGE_HEAD"); e == nil {
		if _, err = git(ctx, a.IntegrationPath, "commit", "-m", "Accept Blux snapshot "+v.ID); err != nil {
			return block("无法保存整合提交。")
		}
	}
	a.ResultCommit, err = git(ctx, a.IntegrationPath, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	a.Status = "ready"
	if err = s.Store.SaveAcceptance(ctx, a); err != nil {
		return
	}
	head, e := git(ctx, src, "rev-parse", "HEAD")
	status, se := git(ctx, src, "status", "--porcelain=v1", "--untracked-files=all")
	if e != nil || se != nil || head != a.SourceHead || status != "" {
		return block("整合期间源项目发生变化，请检查后重新接收。")
	}
	if _, err = git(ctx, src, "merge", "--ff-only", a.ResultCommit); err != nil {
		return block("最终快进失败，整合区已保留。")
	}
	a.Status = "accepted"
	return a, s.Store.SaveAcceptance(ctx, a)
}
