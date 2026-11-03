package httpapi

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type workspaceEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size,omitempty"`
	Change   string `json:"change,omitempty"`
	Previous string `json:"previous_path,omitempty"`
}
type workspaceSource struct {
	detail   core.TaskDetail
	snapshot *store.Snapshot
	root     string
}

func validWorkspacePath(name string) bool {
	return name == "" || fs.ValidPath(name) && !strings.Contains(name, "\\") && !strings.ContainsRune(name, 0) && !strings.Contains("/"+name+"/", "/.git/")
}
func (s *Server) workspaceSource(r *http.Request) (workspaceSource, error) {
	var source workspaceSource
	d, e := s.Store.GetTask(r.Context(), r.PathValue("task"))
	if e != nil {
		return source, e
	}
	source.detail = d
	source.root = d.Session.Runtime.WorktreePath
	if id := r.URL.Query().Get("snapshot"); id != "" {
		v, e := s.Store.Snapshot(r.Context(), id)
		if e != nil {
			return source, e
		}
		if v.TaskID != d.Task.ID {
			return source, core.ErrNotFound
		}
		source.snapshot = &v
		source.root = d.Task.RepositoryPath
		if v.Commit == "" {
			source.root = ""
		} else {
			// A selected project may be a subdirectory; the task worktree is rooted
			// at the repository, and historical trees must use that same root.
			root, _, err := workspaceGit(r.Context(), source.root, 64<<10, "rev-parse", "--show-toplevel")
			if err != nil {
				return source, core.ErrNotFound
			}
			source.root = strings.TrimSpace(string(root))
		}
	}
	return source, nil
}
func workspaceGit(ctx context.Context, root string, limit int, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "--no-replace-objects", "--literal-pathspecs", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	cmd.Stderr = io.Discard
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, false, e
	}
	if e = cmd.Start(); e != nil {
		return nil, false, e
	}
	b, readErr := io.ReadAll(io.LimitReader(out, int64(limit)+1))
	truncated := len(b) > limit
	if truncated {
		b = b[:limit]
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, false, readErr
	}
	if waitErr != nil && !truncated {
		return nil, false, waitErr
	}
	return b, truncated, nil
}
func (s *Server) workspaceFiles(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if !validWorkspacePath(name) {
		writeError(w, 400, "invalid_path", "文件路径无效。")
		return
	}
	source, e := s.workspaceSource(r)
	if e != nil {
		s.storeError(w, e)
		return
	}
	entries := []workspaceEntry{}
	truncated := false
	if source.root == "" {
		writeJSON(w, 200, map[string]any{"entries": entries, "pending": true})
		return
	}
	if r.URL.Query().Get("scope") == "changes" {
		entries, e = workspaceChanges(r.Context(), source)
	} else if source.snapshot != nil {
		revision := source.snapshot.Commit
		if name != "" {
			revision += ":" + name
		}
		var b []byte
		b, truncated, e = workspaceGit(r.Context(), source.root, 2<<20, "ls-tree", "-z", revision)
		if e == nil {
			for _, record := range strings.Split(string(b), "\x00") {
				meta, filename, ok := strings.Cut(record, "\t")
				fields := strings.Fields(meta)
				if !ok || len(fields) != 3 || filename == ".git" {
					continue
				}
				kind := "file"
				if fields[1] == "tree" {
					kind = "directory"
				}
				if fields[0] == "120000" || fields[0] == "160000" {
					kind = "link"
				}
				entries = append(entries, workspaceEntry{Name: filename, Path: path.Join(name, filename), Kind: kind})
				if len(entries) >= 500 {
					truncated = true
					break
				}
			}
		}
	} else {
		var root *os.Root
		root, e = os.OpenRoot(source.root)
		if e == nil {
			defer root.Close()
			var directory *os.File
			dir := name
			if dir == "" {
				dir = "."
			}
			var info fs.FileInfo
			info, e = root.Stat(dir)
			if e == nil && !info.IsDir() {
				e = errors.New("not a directory")
			}
			if e == nil {
				directory, e = root.Open(dir)
			}
			if e == nil {
				defer directory.Close()
				var children []fs.DirEntry
				children, e = directory.ReadDir(501)
				if errors.Is(e, io.EOF) {
					e = nil
				}
				if len(children) > 500 {
					truncated = true
					children = children[:500]
				}
				for _, child := range children {
					if child.Name() == ".git" {
						continue
					}
					kind := "file"
					if child.IsDir() {
						kind = "directory"
					}
					if child.Type()&os.ModeSymlink != 0 || !child.IsDir() && !child.Type().IsRegular() {
						kind = "link"
					}
					var size int64
					if info, err := child.Info(); err == nil {
						size = info.Size()
					}
					entries = append(entries, workspaceEntry{Name: child.Name(), Path: path.Join(name, child.Name()), Kind: kind, Size: size})
				}
			}
		}
	}
	if e != nil {
		writeError(w, 404, "workspace_unavailable", "无法读取此工作区目录或版本。")
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Kind == "directory") != (entries[j].Kind == "directory") {
			return entries[i].Kind == "directory"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	writeJSON(w, 200, map[string]any{"entries": entries, "truncated": truncated})
}
func workspaceChanges(ctx context.Context, source workspaceSource) ([]workspaceEntry, error) {
	entries := []workspaceEntry{}
	var args []string
	if source.snapshot != nil {
		args = []string{"diff", "--no-ext-diff", "--no-textconv", "--name-status", "-z", "--find-renames", source.detail.Task.BaseCommit, source.snapshot.Commit}
	} else {
		args = []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}
	}
	b, truncated, e := workspaceGit(ctx, source.root, 2<<20, args...)
	if e != nil {
		return nil, e
	}
	if truncated {
		return nil, errors.New("too many changes")
	}
	records := strings.Split(string(b), "\x00")
	for i := 0; i < len(records); i++ {
		if records[i] == "" {
			continue
		}
		status, filename := "", ""
		previous := ""
		if source.snapshot != nil {
			status = records[i]
			i++
			if i >= len(records) {
				break
			}
			filename = records[i]
			if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
				previous = filename
				i++
				if i >= len(records) {
					break
				}
				filename = records[i]
			}
		}
		if source.snapshot == nil {
			if len(records[i]) < 4 {
				continue
			}
			status = strings.TrimSpace(records[i][:2])
			filename = records[i][3:]
			if strings.Contains(status, "R") || strings.Contains(status, "C") {
				i++
				if i < len(records) {
					previous = records[i]
				}
			}
		}
		if !validWorkspacePath(filename) {
			continue
		}
		kind := "modified"
		if strings.Contains(status, "D") {
			kind = "deleted"
		} else if strings.Contains(status, "A") || status == "??" {
			kind = "added"
		} else if strings.HasPrefix(status, "R") {
			kind = "renamed"
		}
		entries = append(entries, workspaceEntry{Name: path.Base(filename), Path: filename, Kind: "file", Change: kind, Previous: previous})
	}
	return entries, nil
}

// Keep the version in the URL path so relative HTML assets use the same tree.
func (s *Server) workspacePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("path", r.PathValue("path"))
	q.Set("raw", "1")
	q.Del("diff")
	q.Del("download")
	q.Del("snapshot")
	if version := r.PathValue("version"); version != "live" {
		q.Set("snapshot", version)
	}
	r.URL.RawQuery = q.Encode()
	s.workspaceFile(w, r)
}

func (s *Server) workspaceFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if name == "" || !validWorkspacePath(name) {
		writeError(w, 400, "invalid_path", "文件路径无效。")
		return
	}
	source, e := s.workspaceSource(r)
	if e != nil {
		s.storeError(w, e)
		return
	}
	if source.root == "" {
		writeError(w, 404, "workspace_pending", "工作区尚未准备完成。")
		return
	}
	raw := r.URL.Query().Get("raw") == "1"
	limit := 1 << 20
	if raw {
		limit = 64 << 20
	}
	diff := r.URL.Query().Get("diff") == "1"
	var b []byte
	var truncated bool
	var size int64
	if diff {
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames"}
		if source.snapshot != nil {
			args = append(args, source.detail.Task.BaseCommit, source.snapshot.Commit)
		} else {
			args = append(args, "HEAD")
		}
		args = append(args, "--", name)
		if previous := r.URL.Query().Get("previous_path"); previous != "" {
			if !validWorkspacePath(previous) {
				writeError(w, 400, "invalid_path", "文件路径无效。")
				return
			}
			args = append(args, previous)
		}
		b, truncated, e = workspaceGit(r.Context(), source.root, limit, args...)
		// Untracked files have no Git diff; their actual contents form the addition.
		if e == nil && len(b) == 0 && source.snapshot == nil {
			tracked, _, err := workspaceGit(r.Context(), source.root, 1024, "ls-files", "-z", "--", name)
			if err == nil && len(tracked) == 0 {
				if content, _, cut, err := readWorkspaceFile(r.Context(), source, name, limit); err == nil {
					b = []byte("--- /dev/null\n+++ b/" + name + "\n+" + strings.ReplaceAll(string(content), "\n", "\n+"))
					truncated = cut
					if len(b) > limit {
						b = b[:limit]
						truncated = true
					}
				}
			}
		}
		size = int64(len(b))
	} else {
		b, size, truncated, e = readWorkspaceFile(r.Context(), source, name, limit)
	}
	if e != nil {
		writeError(w, 404, "file_unavailable", "无法读取此文件，它可能已删除或不在任务工作区内。")
		return
	}
	if raw {
		if truncated {
			writeError(w, 413, "file_too_large", "文件超过 64 MiB，无法下载。")
			return
		}
		typ := mime.TypeByExtension(filepath.Ext(name))
		if typ == "" {
			typ = "application/octet-stream"
		}
		w.Header().Set("Content-Type", typ)
		disposition := "inline"
		if r.URL.Query().Get("download") == "1" {
			disposition = "attachment"
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": path.Base(name)}))
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'none'; form-action 'none'")
		w.Write(b)
		return
	}
	if truncated {
		for n := 0; n < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); n++ {
			b = b[:len(b)-1]
		}
	}
	binary := !utf8.Valid(b) || strings.ContainsRune(string(b), 0)
	content := ""
	if !binary {
		content = string(b)
	}
	writeJSON(w, 200, map[string]any{"path": name, "content": content, "size": size, "binary": binary, "truncated": truncated})
}
func readWorkspaceFile(ctx context.Context, source workspaceSource, name string, limit int) ([]byte, int64, bool, error) {
	if source.snapshot != nil {
		object := source.snapshot.Commit + ":" + name
		meta, _, e := workspaceGit(ctx, source.root, 128, "cat-file", "-s", object)
		if e != nil {
			return nil, 0, false, e
		}
		size, e := strconv.ParseInt(strings.TrimSpace(string(meta)), 10, 64)
		if e != nil {
			return nil, 0, false, e
		}
		b, truncated, e := workspaceGit(ctx, source.root, limit, "show", object)
		return b, size, truncated, e
	}
	root, e := os.OpenRoot(source.root)
	if e != nil {
		return nil, 0, false, e
	}
	defer root.Close()
	if info, err := root.Lstat(name); err != nil || !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("not a regular file")
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, 0, false, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("not a regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	truncated := len(b) > limit
	if truncated {
		b = b[:limit]
	}
	return b, info.Size(), truncated, e
}
