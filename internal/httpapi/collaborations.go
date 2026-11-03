package httpapi

import (
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
	"net/http"
	"path/filepath"
)

func (s *Server) collaborations(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Collaborations(r.Context())
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"collaborations": v})
}
func (s *Server) createCollaboration(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var in store.CreateCollaborationInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.RepositoryPath != "" {
		p, e := filepath.Abs(filepath.Clean(in.RepositoryPath))
		if e != nil {
			s.storeError(w, e)
			return
		}
		in.RepositoryPath = p
	}
	v, replayed, e := s.Store.CreateCollaboration(r.Context(), in, key)
	if e != nil {
		s.storeError(w, e)
		return
	}
	code := 201
	if replayed {
		code = 200
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, code, v)
}
func (s *Server) collaboration(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.Collaboration(r.Context(), r.PathValue("collaboration"))
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) sharedArtifacts(w http.ResponseWriter, r *http.Request) {
	v, e := s.Store.SharedSnapshots(r.Context(), r.PathValue("collaboration"))
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"snapshots": v})
}
func (s *Server) addAgentTask(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		Model           string   `json:"model"`
		ModelProvider   string   `json:"model_provider"`
		ReasoningEffort string   `json:"reasoning_effort"`
		Title           string   `json:"title"`
		Prompt          string   `json:"prompt"`
		Adapter         string   `json:"adapter"`
		Mode            string   `json:"mode"`
		ContextIDs      []string `json:"context_ids"`
	}
	if !readJSON(w, r, &body) || !validPrompt(w, body.Prompt) {
		return
	}
	group, e := s.Store.Collaboration(r.Context(), r.PathValue("collaboration"))
	if e != nil {
		s.storeError(w, e)
		return
	}
	if body.Adapter == "" {
		body.Adapter = "codex"
	}
	if body.Adapter != "codex" && body.Adapter != "claude" && body.Adapter != "deepseek" && body.Adapter != "gemini" {
		writeError(w, 400, "adapter_unsupported", "请选择支持的 Agent。")
		return
	}
	in := core.CreateTaskInput{CollaborationID: group.ID, Model: body.Model, ReasoningEffort: body.ReasoningEffort, ModelProvider: body.ModelProvider, Title: body.Title, Prompt: body.Prompt, RepositoryPath: group.RepositoryPath, Adapter: body.Adapter, Mode: body.Mode, ContextIDs: append(append([]string{}, group.ContextIDs...), body.ContextIDs...), IdempotencyKey: "collaboration:" + group.ID + ":" + key}
	if d, found, e := s.Store.LookupTaskCreation(r.Context(), in); e != nil {
		s.storeError(w, e)
		return
	} else if found {
		created(w, d, true)
		return
	}
	if e = s.checkAdapter(r.Context(), body.Adapter); e != nil {
		s.storeError(w, e)
		return
	}
	{
		repo, e := (workspace.Manager{DataDir: s.DataDir}).Freeze(r.Context(), group.RepositoryPath)
		if e != nil {
			s.storeError(w, e)
			return
		}
		in.BaseCommit = repo.BaseCommit
	}
	versions, e := s.Store.SharedSnapshots(r.Context(), group.ID)
	if e != nil {
		s.storeError(w, e)
		return
	}
	latest := map[string]store.SharedSnapshot{}
	for _, v := range versions {
		latest[v.TaskID] = v
	}
	for _, task := range group.Tasks {
		if v, ok := latest[task.ID]; ok {
			for _, a := range v.Artifacts {
				in.InputArtifacts = append(in.InputArtifacts, a.ID)
			}
		}
	}
	detail, replayed, e := s.Store.CreateTask(r.Context(), in)
	if e != nil {
		s.storeError(w, e)
		return
	}
	created(w, detail, replayed)
}
