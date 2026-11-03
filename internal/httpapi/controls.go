package httpapi

import "net/http"

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		TurnID string `json:"turn_id"`
		Kind   string `json:"kind"`
		Prompt string `json:"prompt"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Kind == "steer" && !validPrompt(w, body.Prompt) {
		return
	}
	c, err := s.Store.RequestControl(r.Context(), r.PathValue("task"), body.TurnID, key, body.Kind, body.Prompt)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 202, c)
}
func (s *Server) getControl(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetControl(r.Context(), r.PathValue("task"), r.PathValue("key"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, c)
}
