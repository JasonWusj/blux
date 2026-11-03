package httpapi

import "net/http"

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.DeleteTask(r.Context(), r.PathValue("task"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	status := http.StatusOK
	if v.PendingStop {
		status = http.StatusAccepted
	}
	writeJSON(w, status, v)
}
func (s *Server) deleteCollaboration(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.DeleteCollaboration(r.Context(), r.PathValue("collaboration"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	status := http.StatusOK
	if v.PendingStop {
		status = http.StatusAccepted
	}
	writeJSON(w, status, v)
}
