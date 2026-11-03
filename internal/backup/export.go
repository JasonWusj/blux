package backup

import (
	"archive/zip"
	"blux/internal/platform"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Repository struct {
	Source string `json:"source"`
	Bundle string `json:"bundle"`
}
type Manifest struct {
	Version      int          `json:"version"`
	CreatedAt    string       `json:"created_at"`
	DataDir      string       `json:"data_dir"`
	Repositories []Repository `json:"repositories"`
}

func Export(ctx context.Context, data, destination string) error {
	data, e := filepath.Abs(data)
	if e != nil {
		return e
	}
	destination, e = filepath.Abs(destination)
	if e != nil {
		return e
	}
	canonicalData, e := filepath.EvalSymlinks(data)
	if e != nil {
		return e
	}
	canonicalParent, e := filepath.EvalSymlinks(filepath.Dir(destination))
	if e != nil {
		return e
	}
	resolvedDestination := filepath.Join(canonicalParent, filepath.Base(destination))
	relative, e := filepath.Rel(canonicalData, resolvedDestination)
	if e == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("备份文件必须位于数据目录外")
	}

	worker, e := platform.Lock(filepath.Join(data, "worker.lock"))
	if e != nil {
		return errors.New("请先停止 Worker 再备份")
	}
	defer worker()
	server, e := platform.Lock(filepath.Join(data, "serve.lock"))
	if e != nil {
		return errors.New("请先停止网页服务再备份")
	}
	defer server()
	temp, e := os.MkdirTemp("", "blux-export-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	db, e := store.Open(filepath.Join(data, "blux.db"))
	if e != nil {
		return e
	}
	defer db.Close()
	snapshot := filepath.Join(temp, "blux.db")
	if e = db.ExportDatabase(ctx, snapshot); e != nil {
		return e
	}
	manifest := Manifest{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), DataDir: data, Repositories: []Repository{}}
	tasks, e := db.ListTasks(ctx)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		src := task.RepositoryPath
		if src == "" || task.Adapter != "codex" || seen[src] {
			continue
		}
		manager, e := db.IsManager(ctx, task.ID)
		if e != nil {
			return e
		}
		if manager {
			continue
		}
		repo, e := (workspace.Manager{DataDir: data}).Resolve(ctx, src, task.BaseCommit)
		if e != nil {
			return e
		}
		if (workspace.Manager{DataDir: data}).IsSnapshot(src, repo.Root) {
			continue
		} // Included below with the data directory.
		seen[src] = true
		name := fmt.Sprintf("repositories/repo-%d.bundle", len(manifest.Repositories)+1)
		path := filepath.Join(temp, filepath.FromSlash(name))
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		cmd := exec.CommandContext(ctx, "git", "-c", "core.hooksPath="+os.DevNull, "-C", src, "bundle", "create", path, "--all")
		if e = cmd.Run(); e != nil {
			return fmt.Errorf("无法备份仓库 %s: %w", src, e)
		}
		manifest.Repositories = append(manifest.Repositories, Repository{src, name})
	}
	file, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(destination)
		}
	}()
	z := zip.NewWriter(file)
	add := func(name, path string) error {
		entry, e := z.Create(name)
		if e != nil {
			return e
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		_, e = io.Copy(entry, in)
		return e
	}
	if e = add("data/blux.db", snapshot); e != nil {
		return e
	}
	e = filepath.WalkDir(data, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(data, path)
		slash := filepath.ToSlash(rel)
		if d.IsDir() {
			if slash == "logs" || slash == "runtime" || slash == "locks" {
				return filepath.SkipDir
			}
			return nil
		}
		if slash == "blux.db" || slash == "blux.db-wal" || slash == "blux.db-shm" || strings.HasSuffix(slash, ".lock") || filepath.Base(path) == "app-server-process.json" {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return add("data/"+slash, path)
	})
	if e != nil {
		return e
	}
	for _, repo := range manifest.Repositories {
		if e = add(repo.Bundle, filepath.Join(temp, filepath.FromSlash(repo.Bundle))); e != nil {
			return e
		}
	}
	entry, e := z.Create("manifest.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(entry).Encode(manifest); e != nil {
		return e
	}
	if e = z.Close(); e != nil {
		return e
	}
	if e = file.Sync(); e != nil {
		return e
	}
	success = true
	return nil
}
