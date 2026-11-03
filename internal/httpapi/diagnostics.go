package httpapi

import (
	"net/http"
	"runtime"
)

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Store.ListTasks(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	counts := map[string]int{}
	for _, t := range tasks {
		counts[t.Status]++
	}
	worker, err := s.Store.GetWorkerStatus(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	info, e := s.codexStatus(r.Context())
	var reason string
	if e != nil {
		reason = e.Error()
	}
	writeJSON(w, 200, map[string]any{"platform": runtime.GOOS, "arch": runtime.GOARCH, "data_dir": s.DataDir, "tasks": counts, "worker": worker, "codex": info, "codex_error": reason, "recovery": "interrupted turns require an explicit continuation; external effects are not automatically replayed"})
}
