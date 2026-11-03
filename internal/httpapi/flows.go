package httpapi

import (
	"blux/internal/flow"
	"blux/internal/flowmodel"
	"blux/internal/workspace"
	"net/http"
	"path/filepath"
	"strings"
)

func (s *Server) flowService() flow.Service { return flow.Service{Store: s.Store, DataDir: s.DataDir} }
func (s *Server) templates(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Templates(r.Context())
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"templates": v})
}
func (s *Server) saveTemplate(w http.ResponseWriter, r *http.Request) {
	var t flowmodel.Template
	if !readJSON(w, r, &t) {
		return
	}
	if e := s.Store.SaveTemplate(r.Context(), t); e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 201, t)
}
func (s *Server) deliveries(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Deliveries(r.Context())
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"deliveries": v})
}
func (s *Server) delivery(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Delivery(r.Context(), r.PathValue("delivery"))
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) createDelivery(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var in flow.CreateInput
	if !readJSON(w, r, &in) || !validPrompt(w, in.Goal) {
		return
	}
	if in.Adapter == "" {
		in.Adapter = "codex"
	}
	in.RepositoryPath = strings.TrimSpace(in.RepositoryPath)
	if in.RepositoryPath != "" {
		p, e := filepath.Abs(filepath.Clean(in.RepositoryPath))
		if e != nil {
			s.storeError(w, e)
			return
		}
		in.RepositoryPath = p
	}
	if prior, found, e := s.flowService().LookupCreate(r.Context(), in, key); e != nil {
		s.storeError(w, e)
		return
	} else if found {
		writeJSON(w, 200, prior)
		return
	}
	if in.Adapter == "codex" {
		if _, e := s.codexStatus(r.Context()); e != nil {
			s.storeError(w, e)
			return
		}
		repo, e := (workspace.Manager{DataDir: s.DataDir}).Freeze(r.Context(), in.RepositoryPath)
		if e != nil {
			s.storeError(w, e)
			return
		}
		in.RepositoryPath = repo.Path
		in.BaseCommit = repo.BaseCommit
	}
	d, e := s.flowService().Create(r.Context(), in, key)
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 201, d)
}
func (s *Server) flowAction(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var a flow.Action
	if !readJSON(w, r, &a) {
		return
	}
	d, e := s.flowService().Action(r.Context(), r.PathValue("delivery"), key, a)
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, d)
}
func (s *Server) acceptDelivery(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var in struct {
		Generation int64 `json:"generation"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	d, e := s.flowService().Accept(r.Context(), r.PathValue("delivery"), key, in.Generation)
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, d)
}
