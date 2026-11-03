package httpapi

import (
	"blux/internal/core"
	"net/http"
	"path/filepath"
	"strings"
)

func (s *Server) managerTask(w http.ResponseWriter, r *http.Request) {
	id, e := s.Store.ManagerTask(r.Context(), r.URL.Query().Get("project"))
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]string{"task_id": id})
}
func (s *Server) managerTurn(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var p struct {
		Model           string   `json:"model"`
		ModelProvider   string   `json:"model_provider"`
		ReasoningEffort string   `json:"reasoning_effort"`
		Prompt          string   `json:"prompt"`
		ContextIDs      []string `json:"context_ids"`
		RepositoryPath  string   `json:"repository_path"`
	}
	if !readJSON(w, r, &p) || !validPrompt(w, p.Prompt) {
		return
	}
	project, e := filepath.Abs(strings.TrimSpace(p.RepositoryPath))
	if e != nil || strings.TrimSpace(p.RepositoryPath) == "" {
		writeError(w, 400, "repository_required", "请先选择项目文件夹。")
		return
	}
	in := core.CreateTaskInput{Model: p.Model, ReasoningEffort: p.ReasoningEffort, ModelProvider: p.ModelProvider, ContextIDs: p.ContextIDs, Prompt: p.Prompt, Title: "管家 · " + filepath.Base(project), RepositoryPath: project, Adapter: "codex", Mode: "explore", Manager: true, IdempotencyKey: "manager:" + key}
	if d, found, e := s.Store.LookupTaskCreation(r.Context(), in); e != nil {
		s.storeError(w, e)
		return
	} else if found {
		writeJSON(w, 200, d)
		return
	}
	if _, e = s.codexStatus(r.Context()); e != nil {
		s.storeError(w, e)
		return
	}
	id, e := s.Store.ManagerTask(r.Context(), project)
	if e != nil {
		s.storeError(w, e)
		return
	}
	var d core.TaskDetail
	if id == "" {
		d, _, e = s.Store.CreateTask(r.Context(), in)
	} else {
		d, _, e = s.Store.CreateTurn(r.Context(), id, core.CreateTurnInput{Model: p.Model, ReasoningEffort: p.ReasoningEffort, ModelProvider: p.ModelProvider, ContextIDs: p.ContextIDs, Prompt: p.Prompt, IdempotencyKey: "manager:" + key})
	}
	if e != nil {
		s.storeError(w, e)
		return
	}
	writeJSON(w, 201, d)
}
