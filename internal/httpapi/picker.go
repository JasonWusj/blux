package httpapi

import (
	"blux/internal/platform"
	"blux/internal/workspace"
	"context"
	"errors"
	"net/http"
	"time"
)

var pickerGate = make(chan struct{}, 1)

func (s *Server) selectDirectory(w http.ResponseWriter, r *http.Request) {
	select {
	case pickerGate <- struct{}{}:
		defer func() { <-pickerGate }()
	default:
		writeError(w, 409, "picker_busy", "项目选择窗口已打开。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	picker := s.DirectoryPicker
	if picker == nil {
		picker = platform.PickDirectory
	}
	path, err := picker(ctx)
	if errors.Is(err, platform.ErrCanceled) {
		writeJSON(w, 200, map[string]any{"canceled": true})
		return
	}
	if err != nil {
		writeError(w, 503, "picker_unavailable", "无法打开系统文件夹选择窗口，请确认服务运行在本机桌面会话中。")
		return
	}
	repo, err := workspace.Inspect(r.Context(), path, false)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"path": repo.Path, "branch": repo.Branch, "canceled": false})
}

func (s *Server) projectInfo(w http.ResponseWriter, r *http.Request) {
	repo, err := workspace.Inspect(r.Context(), r.URL.Query().Get("path"), false)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"path": repo.Path, "branch": repo.Branch, "has_commits": repo.BaseCommit != ""})
}
