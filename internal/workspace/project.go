package workspace

import (
	"blux/internal/platform"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (m Manager) snapshotRepository(path string) string {
	absolute, _ := filepath.Abs(filepath.Clean(path))
	sum := sha256.Sum256([]byte(absolute))
	return filepath.Join(m.DataDir, "project-repos", fmt.Sprintf("%x.git", sum))
}

// Freeze accepts any directory. Clean committed repository roots retain native
// worktrees; all other directories get a private Git baseline without touching
// their Git configuration, index, branches or files.
func (m Manager) Freeze(ctx context.Context, path string) (Repository, error) {
	repo, err := Inspect(ctx, path, false)
	if err != nil {
		return repo, err
	}
	if _, err = exec.LookPath("git"); err != nil {
		return repo, failure("git_not_found", "未找到 Git，Blux 保存工作区快照需要 Git。", err)
	}
	canonical, _ := filepath.EvalSymlinks(repo.Path)
	if repo.BaseCommit != "" && canonical == repo.Root {
		status, e := git(ctx, repo.Path, "status", "--porcelain=v1", "--untracked-files=all")
		if e != nil {
			return repo, failure("repository_unreadable", "无法读取项目状态。", e)
		}
		if status == "" {
			return repo, nil
		}
	}
	root := m.snapshotRepository(repo.Path)
	if err = os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		return repo, err
	}
	unlock, err := platform.Lock(root + ".lock")
	if err != nil {
		return repo, failure("project_busy", "项目快照正在保存，请稍后重试。", err)
	}
	defer unlock()
	if _, err = os.Stat(filepath.Join(root, "HEAD")); os.IsNotExist(err) {
		if _, err = git(ctx, filepath.Dir(root), "init", "--bare", "--initial-branch=blux-snapshot", root); err != nil {
			return repo, err
		}
	} else if err != nil {
		return repo, err
	}
	args := []string{"add", "-A", "--", "."}
	data, _ := filepath.Abs(m.DataDir)
	if resolved, e := filepath.EvalSymlinks(data); e == nil {
		data = resolved
	}
	rel, e := filepath.Rel(canonical, data)
	if e == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		args = append(args, ":(exclude,literal)"+filepath.ToSlash(rel))
	}
	if rel == "." {
		for _, name := range []string{"project-repos", "workspaces", "deliveries", "integrations"} {
			args = append(args, ":(exclude,literal)"+name)
		}
	}
	if _, err = projectGit(ctx, root, repo.Path, nil, args...); err != nil {
		return repo, failure("project_snapshot_failed", "无法保存项目快照。", err)
	}
	// A file already tracked by the user's repository can also be ignored by
	// .gitignore. Include its current content without changing the user's index.
	if repo.Branch != "" {
		cmd := exec.CommandContext(ctx, "git", "-C", repo.Path, "ls-files", "--cached", "-z", "--", ".")
		cmd.Env = append(os.Environ(), "GIT_TRACE2_EVENT=0", "GIT_OPTIONAL_LOCKS=0")
		tracked, e := cmd.Output()
		if e != nil {
			return repo, e
		}
		var paths []byte
		for _, name := range bytes.Split(tracked, []byte{0}) {
			if len(name) == 0 {
				continue
			}
			if _, e := os.Lstat(filepath.Join(repo.Path, string(name))); e != nil {
				continue
			}
			if rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && (string(name) == filepath.ToSlash(rel) || strings.HasPrefix(string(name), filepath.ToSlash(rel)+"/")) {
				continue
			}
			paths = append(paths, name...)
			paths = append(paths, 0)
		}
		if len(paths) > 0 {
			if _, err = projectGit(ctx, root, repo.Path, paths, "add", "-f", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
				return repo, err
			}
		}
	}
	if _, err = projectGit(ctx, root, repo.Path, nil, "commit", "--allow-empty", "-m", "Blux project baseline"); err != nil {
		return repo, failure("project_snapshot_failed", "无法保存项目基线。", err)
	}
	repo.BaseCommit, err = git(ctx, root, "rev-parse", "HEAD")
	repo.Root = root
	return repo, err
}

// Resolve also supports pre-existing tasks that use the original repository.
func (m Manager) Resolve(ctx context.Context, path, base string) (Repository, error) {
	root := m.snapshotRepository(path)
	if _, err := os.Stat(filepath.Join(root, "HEAD")); err == nil {
		if commit, e := git(ctx, root, "rev-parse", "--verify", base+"^{commit}"); e == nil && commit == base {
			return Repository{Path: path, Root: root, BaseCommit: base}, nil
		}
	}
	return Inspect(ctx, path, false)
}

func (m Manager) IsSnapshot(path, root string) bool {
	return filepath.Clean(root) == filepath.Clean(m.snapshotRepository(path))
}

func projectGit(ctx context.Context, repo, source string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	prefix := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "core.fsmonitor=false", "-c", "user.name=Blux", "-c", "user.email=blux@localhost", "--git-dir=" + repo, "--work-tree=" + source, "-C", source}
	cmd := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_TRACE2_EVENT=0")
	cmd.Stdin = bytes.NewReader(input)
	return cmd.Output()
}

// ApplySnapshot changes only paths in the reviewed patch; source edits that
// conflict with it are rejected by git apply before any file is changed.
func (m Manager) ApplySnapshot(ctx context.Context, source, base, head string, recovering bool) error {
	repo, err := m.Resolve(ctx, source, base)
	if err != nil {
		return err
	}
	if !m.IsSnapshot(source, repo.Root) {
		return fmt.Errorf("not a directory snapshot")
	}
	patch, err := projectGit(ctx, repo.Root, source, nil, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--no-renames", base, head, "--")
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return nil
	}
	if recovering {
		if _, e := projectGit(ctx, repo.Root, source, patch, "apply", "--reverse", "--check", "--binary"); e == nil {
			return nil
		}
	}
	if _, err = projectGit(ctx, repo.Root, source, patch, "apply", "--check", "--binary"); err != nil {
		return failure("source_changed", "源目录存在冲突或文件发生变化，未接收；请检查后重试。", err)
	}
	_, err = projectGit(ctx, repo.Root, source, patch, "apply", "--binary")
	return err
}
