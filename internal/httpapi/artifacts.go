package httpapi

import (
	"blux/internal/artifact"
	"blux/internal/store"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.GetTask(r.Context(), r.PathValue("task")); err != nil {
		s.storeError(w, err)
		return
	}
	v, err := s.Store.Snapshots(r.Context(), r.PathValue("task"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"snapshots": v})
}
func (s *Server) artifactContent(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.Artifact(r.Context(), r.PathValue("artifact"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.serveArtifact(w, r, a)
}
func (s *Server) taskInputContent(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.GetTask(r.Context(), r.PathValue("task")); err != nil {
		s.storeError(w, err)
		return
	}
	inputs, err := s.Store.TaskInputs(r.Context(), r.PathValue("task"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	for _, a := range inputs {
		if a.ID == r.PathValue("artifact") {
			s.serveArtifact(w, r, a)
			return
		}
	}
	writeError(w, 404, "not_found", "此任务未引用该产物版本。")
}
func (s *Server) serveArtifact(w http.ResponseWriter, r *http.Request, a store.ArtifactVersion) {
	file := (artifact.Service{Store: s.Store, DataDir: s.DataDir}).Blob(a.Hash)
	f, err := os.Open(file)
	if err != nil {
		writeError(w, 404, "artifact_missing", "产物文件已丢失。")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.storeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(a.Path)}))
	http.ServeContent(w, r, a.Path, info.ModTime(), f)
}
func (s *Server) acceptSnapshot(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	a, err := (artifact.Service{Store: s.Store, DataDir: s.DataDir}).Accept(r.Context(), r.PathValue("snapshot"), key)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, a)
}
