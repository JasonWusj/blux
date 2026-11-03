package httpapi

import (
	"blux/internal/artifact"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Snapshot(r.Context(), r.PathValue("snapshot"))
	if e != nil {
		s.storeError(w, e)
		return
	}
	name := r.PathValue("path")
	for _, a := range v.Artifacts {
		if a.Path != name {
			continue
		}
		f, e := os.Open((artifact.Service{DataDir: s.DataDir}).Blob(a.Hash))
		if e != nil {
			writeError(w, 404, "artifact_missing", "产物文件已丢失。")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			s.storeError(w, e)
			return
		}
		typ := mime.TypeByExtension(filepath.Ext(name))
		if typ == "" {
			typ = "application/octet-stream"
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'none'; form-action 'none'")
		http.ServeContent(w, r, name, info.ModTime(), f)
		return
	}
	writeError(w, 404, "not_found", "快照中不存在此产物。")
}
