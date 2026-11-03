// Package workspace prepares persistent task directories and isolated Git
// worktrees. Git metadata is managed here, outside the agent's write sandbox.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"blux/internal/core"
)

type Repository struct {
	Path       string
	Root       string
	BaseCommit string
	Branch     string
}

// Inspect validates a directory and reads optional Git metadata without writes.
// clean is retained for callers that explicitly require a clean Git source.
func Inspect(ctx context.Context, path string, clean bool) (Repository, error) {
	if strings.TrimSpace(path) == "" {
		return Repository{}, failure("repository_required", "请选择本地项目文件夹。", nil)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return Repository{}, failure("repository_invalid", "无法解析项目目录。", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return Repository{}, failure("repository_not_found", "项目目录不存在或不是文件夹。", err)
	}
	root, err := git(ctx, absolute, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repository{Path: absolute, Root: absolute}, nil
	}
	branch, _ := git(ctx, absolute, "symbolic-ref", "--short", "HEAD")
	commit, _ := git(ctx, absolute, "rev-parse", "--verify", "HEAD^{commit}")
	if branch == "" && commit != "" {
		branch = "HEAD (" + commit[:min(8, len(commit))] + ")"
	}
	if clean {
		status, err := git(ctx, absolute, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			return Repository{}, failure("repository_unreadable", "无法检查项目的 Git 状态。", err)
		}
		if status != "" {
			return Repository{}, failure("repository_dirty", "项目存在未提交的变更，请先提交或处理后再创建任务。", nil)
		}
	}
	return Repository{Path: absolute, Root: root, BaseCommit: commit, Branch: branch}, nil
}

type Manager struct{ DataDir string }

var taskPattern = regexp.MustCompile(`^task_[a-zA-Z0-9_-]+$`)
var commitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

func (m Manager) Prepare(ctx context.Context, job core.Job) (core.RuntimeInfo, error) {
	if !taskPattern.MatchString(job.TaskID) {
		return core.RuntimeInfo{}, failure("workspace_invalid", "任务工作区标识无效。", nil)
	}
	root, err := filepath.Abs(filepath.Join(m.DataDir, "workspaces", job.TaskID))
	if err != nil {
		return core.RuntimeInfo{}, err
	}
	for _, name := range []string{"repos", "artifacts", ".agent"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			return core.RuntimeInfo{}, failure("workspace_create_failed", "无法创建任务工作区。", err)
		}
	}
	// macOS /tmp and /var are symlink aliases. Persist canonical roots so the
	// Codex response and our frozen worktree identity refer to the same paths.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return core.RuntimeInfo{}, failure("workspace_unreadable", "无法解析任务工作区。", err)
	}
	if job.Manager {
		return core.RuntimeInfo{WorkspacePath: root}, nil
	}
	if !commitPattern.MatchString(job.BaseCommit) {
		return core.RuntimeInfo{}, failure("repository_baseline_invalid", "任务缺少有效的仓库基线提交。请重新创建任务。", nil)
	}
	repo, err := m.Resolve(ctx, job.RepositoryPath, job.BaseCommit)
	if err != nil {
		return core.RuntimeInfo{}, err
	}
	// HEAD may have advanced while queued. Always use the frozen commit, never
	// silently replace it with the source repository's latest revision.
	commit, err := git(ctx, repo.Root, "rev-parse", "--verify", job.BaseCommit+"^{commit}")
	if err != nil || commit != job.BaseCommit {
		return core.RuntimeInfo{}, failure("repository_baseline_missing", "任务的基线提交已不可用，无法准备工作区。", err)
	}
	code := filepath.Join(root, "repos", "code")
	branch := "feat/blux-" + job.TaskID
	if info, statErr := os.Lstat(code); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return core.RuntimeInfo{}, failure("workspace_conflict", "任务工作区路径已被其他文件占用。", nil)
		}
		currentBranch, branchErr := git(ctx, code, "symbolic-ref", "--short", "HEAD")
		currentCommit, commitErr := git(ctx, code, "rev-parse", "HEAD")
		common, commonErr := git(ctx, code, "rev-parse", "--path-format=absolute", "--git-common-dir")
		sourceCommon, sourceErr := git(ctx, repo.Root, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if branchErr != nil || commitErr != nil || commonErr != nil || sourceErr != nil || currentBranch != branch || !isDescendant(ctx, code, job.BaseCommit, currentCommit) || common != sourceCommon {
			return core.RuntimeInfo{}, failure("workspace_conflict", "已有任务工作区与冻结的仓库或分支不一致。", nil)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return core.RuntimeInfo{}, failure("workspace_unreadable", "无法读取任务工作区。", statErr)
	} else {
		if job.Runtime.WorktreePath != "" || job.Runtime.NativeThreadID != "" {
			return core.RuntimeInfo{}, failure("workspace_missing", "任务工作区已丢失，无法继续原会话。", nil)
		}
		if _, err := git(ctx, repo.Root, "worktree", "add", "-b", branch, code, job.BaseCommit); err != nil {
			return core.RuntimeInfo{}, failure("worktree_create_failed", "创建独立 Git worktree 失败。请检查 Git 分支与工作区状态。", err)
		}
	}
	if job.Runtime.WorktreePath != "" && filepath.Clean(job.Runtime.WorktreePath) != code {
		return core.RuntimeInfo{}, failure("workspace_conflict", "任务工作区位置与已保存记录不一致。", nil)
	}
	runtime := job.Runtime
	runtime.WorkspacePath, runtime.WorktreePath = root, code
	runtime.BranchName, runtime.BaseCommit = branch, job.BaseCommit
	return runtime, nil
}

func git(ctx context.Context, directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args = append([]string{"-c", "core.hooksPath=" + os.DevNull, "-C", directory}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_TRACE2_EVENT=0")
	out, err := cmd.Output()
	if err != nil {
		// Caller-facing failures are stable codes; don't expose arbitrary Git
		// stderr (which may contain remote URLs or user-specific configuration).
		return "", fmt.Errorf("git operation failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func failure(code, message string, err error) error {
	return &core.ExecutionError{Code: code, Message: message, Err: err}
}

func isDescendant(ctx context.Context, dir, base, head string) bool {
	_, err := git(ctx, dir, "merge-base", "--is-ancestor", base, head)
	return err == nil
}
