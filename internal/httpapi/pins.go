package httpapi

import "net/http"

func (s *Server) pinTask(w http.ResponseWriter, r *http.Request) {
	s.setPin(w, r, "task", r.PathValue("task"))
}
func (s *Server) pinCollaboration(w http.ResponseWriter, r *http.Request) {
	s.setPin(w, r, "collaboration", r.PathValue("collaboration"))
}
func (s *Server) setPin(w http.ResponseWriter, r *http.Request, kind, id string) {
	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Pinned == nil {
		writeError(w, 400, "invalid_request", "请指定是否置顶。")
		return
	}
	v, err := s.Store.SetPin(r.Context(), kind, id, *body.Pinned)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
