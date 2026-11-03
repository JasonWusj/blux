package httpapi

import (
	"blux/internal/contextpack"
	"blux/internal/platform"
	"blux/internal/store"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Server) modes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"modes": contextpack.List()})
}
func (s *Server) contextEntries(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.ContextEntries(r.Context(), r.URL.Query().Get("project"), r.URL.Query().Get("q"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"entries": v})
}
func (s *Server) saveContext(w http.ResponseWriter, r *http.Request) {
	var in store.ContextEntry
	if !readJSON(w, r, &in) {
		return
	}
	v, err := s.Store.SaveContext(r.Context(), in)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) taskContext(w http.ResponseWriter, r *http.Request) {
	detail, err := s.Store.GetTask(r.Context(), r.PathValue("task"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	turnID := r.URL.Query().Get("turn")
	if turnID == "" {
		turnID = detail.Task.CurrentTurnID
	}
	v, err := s.Store.ExecutionContext(r.Context(), detail.Task.ID, turnID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	inputs, err := s.Store.TaskInputs(r.Context(), detail.Task.ID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mode": v.Mode, "entries": v.Entries, "frozen_at": v.FrozenAt, "input_artifacts": inputs})
}
func (s *Server) importContext(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectPath string `json:"project_path"`
		Kind        string `json:"kind"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	select {
	case pickerGate <- struct{}{}:
		defer func() { <-pickerGate }()
	default:
		writeError(w, 409, "picker_busy", "文件选择窗口已打开。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	path, err := platform.PickFile(ctx)
	if errors.Is(err, platform.ErrCanceled) {
		writeJSON(w, 200, map[string]any{"canceled": true})
		return
	}
	if err != nil {
		writeError(w, 503, "picker_unavailable", "无法打开系统文件选择窗口。")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.storeError(w, err)
		return
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	if err != nil {
		s.storeError(w, err)
		return
	}
	if len(b) > 1<<20 || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		writeError(w, 400, "document_unsupported", "请选择 1 MiB 以内的 UTF-8 文本、Markdown、CSV 或代码文件。")
		return
	}
	e, err := s.Store.SaveContext(r.Context(), store.ContextEntry{ProjectPath: in.ProjectPath, Kind: in.Kind, Title: filepath.Base(path), Content: string(b)})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 201, e)
}
