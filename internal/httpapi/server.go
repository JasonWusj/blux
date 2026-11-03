// Package httpapi exposes the local, account-free Blux HTTP and event API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"blux/internal/agent"
	"blux/internal/core"
	"blux/internal/store"
	"blux/internal/workspace"
)

type Server struct {
	Store           *store.Store
	DataDir         string
	WebDir          string
	Logger          *slog.Logger
	CodexBinary     string
	ClaudeBinary    string
	DeepSeekBinary  string
	GeminiBinary    string
	modelMu         sync.Mutex
	modelCache      map[string]modelCacheEntry
	probeMu         sync.Mutex
	probeAt         time.Time
	probeInfo       agent.CodexInfo
	probeErr        error
	DirectoryPicker func(context.Context) (string, error)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/agents", s.agents)
	mux.HandleFunc("GET /api/v1/agents/{agent}/models", s.models)
	mux.HandleFunc("GET /api/v1/collaborations", s.collaborations)
	mux.HandleFunc("POST /api/v1/collaborations", s.createCollaboration)
	mux.HandleFunc("GET /api/v1/collaborations/{collaboration}", s.collaboration)
	mux.HandleFunc("DELETE /api/v1/collaborations/{collaboration}", s.deleteCollaboration)
	mux.HandleFunc("PUT /api/v1/collaborations/{collaboration}/pin", s.pinCollaboration)
	mux.HandleFunc("POST /api/v1/collaborations/{collaboration}/tasks", s.addAgentTask)
	mux.HandleFunc("GET /api/v1/collaborations/{collaboration}/artifacts", s.sharedArtifacts)
	mux.HandleFunc("GET /api/v1/manager", s.managerTask)
	mux.HandleFunc("GET /api/v1/previews/{snapshot}/{path...}", s.preview)
	mux.HandleFunc("POST /api/v1/manager/turns", s.managerTurn)
	mux.HandleFunc("GET /api/v1/diagnostics", s.diagnostics)
	mux.HandleFunc("GET /api/v1/modes", s.modes)
	mux.HandleFunc("GET /api/v1/flow-templates", s.templates)
	mux.HandleFunc("POST /api/v1/flow-templates", s.saveTemplate)
	mux.HandleFunc("GET /api/v1/deliveries", s.deliveries)
	mux.HandleFunc("POST /api/v1/deliveries", s.createDelivery)
	mux.HandleFunc("GET /api/v1/deliveries/{delivery}", s.delivery)
	mux.HandleFunc("POST /api/v1/deliveries/{delivery}/actions", s.flowAction)
	mux.HandleFunc("POST /api/v1/deliveries/{delivery}/accept", s.acceptDelivery)
	mux.HandleFunc("GET /api/v1/context", s.contextEntries)
	mux.HandleFunc("POST /api/v1/context", s.saveContext)
	mux.HandleFunc("POST /api/v1/context/import", s.importContext)
	mux.HandleFunc("GET /api/v1/tasks/{task}/context", s.taskContext)
	mux.HandleFunc("GET /api/v1/tasks", s.listTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	mux.HandleFunc("POST /api/v1/projects/select", s.selectDirectory)
	mux.HandleFunc("GET /api/v1/projects/info", s.projectInfo)
	mux.HandleFunc("GET /api/v1/tasks/{task}", s.getTask)
	mux.HandleFunc("DELETE /api/v1/tasks/{task}", s.deleteTask)
	mux.HandleFunc("PUT /api/v1/tasks/{task}/pin", s.pinTask)
	mux.HandleFunc("GET /api/v1/tasks/{task}/snapshots", s.snapshots)
	mux.HandleFunc("GET /api/v1/tasks/{task}/files", s.workspaceFiles)
	mux.HandleFunc("GET /api/v1/tasks/{task}/file", s.workspaceFile)
	mux.HandleFunc("GET /api/v1/tasks/{task}/previews/{version}/{path...}", s.workspacePreview)
	mux.HandleFunc("GET /api/v1/artifacts/{artifact}/content", s.artifactContent)
	mux.HandleFunc("GET /api/v1/tasks/{task}/inputs/{artifact}/content", s.taskInputContent)
	mux.HandleFunc("POST /api/v1/snapshots/{snapshot}/accept", s.acceptSnapshot)
	mux.HandleFunc("POST /api/v1/tasks/{task}/turns", s.createTurn)
	mux.HandleFunc("POST /api/v1/tasks/{task}/controls", s.control)
	mux.HandleFunc("GET /api/v1/tasks/{task}/controls/{key}", s.getControl)
	mux.HandleFunc("GET /api/v1/tasks/{task}/events", s.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "接口不存在。")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.frontend(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	worker, err := s.Store.GetWorkerStatus(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	if worker == nil {
		worker = &core.WorkerStatus{}
	}
	info, probeErr := s.codexStatus(r.Context())
	codex := map[string]any{"available": info.Available, "version": info.Version}
	if probeErr != nil {
		codex["error"] = probeErr.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "phase": 7, "adapter": "codex", "codex": codex,
		"data_dir": s.DataDir, "worker": worker,
	})
}

func (s *Server) codexStatus(ctx context.Context) (agent.CodexInfo, error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if !s.probeAt.IsZero() && time.Since(s.probeAt) < 30*time.Second {
		return s.probeInfo, s.probeErr
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.probeInfo, s.probeErr = agent.ProbeCodex(ctx, s.CodexBinary)
	s.probeAt = time.Now()
	return s.probeInfo, s.probeErr
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Store.ListTasks(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	detail, err := s.Store.GetTask(r.Context(), r.PathValue("task"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	detail.Manager, err = s.Store.IsManager(r.Context(), detail.Task.ID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	detail.DeliveryID, err = s.Store.WorkflowDelivery(r.Context(), detail.Task.ID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		Title           string   `json:"title"`
		Prompt          string   `json:"prompt"`
		Adapter         string   `json:"adapter"`
		RepositoryPath  string   `json:"repository_path"`
		Model           string   `json:"model"`
		ModelProvider   string   `json:"model_provider"`
		ReasoningEffort string   `json:"reasoning_effort"`
		Mode            string   `json:"mode"`
		ContextIDs      []string `json:"context_ids"`
	}
	if !readJSON(w, r, &body) || !validPrompt(w, body.Prompt) {
		return
	}
	if utf8.RuneCountInString(strings.TrimSpace(body.Title)) > 120 {
		writeError(w, http.StatusBadRequest, "invalid_request", "任务标题不能超过 120 个字符。")
		return
	}
	adapter := strings.TrimSpace(body.Adapter)
	if adapter == "" {
		adapter = "codex"
	}
	if adapter != "codex" && adapter != "claude" && adapter != "deepseek" && adapter != "gemini" {
		writeError(w, http.StatusBadRequest, "adapter_unsupported", "请选择已支持的 Agent。")
		return
	}
	repositoryPath := strings.TrimSpace(body.RepositoryPath)
	if repositoryPath != "" {
		if strings.HasPrefix(repositoryPath, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				s.storeError(w, err)
				return
			}
			repositoryPath = filepath.Join(home, repositoryPath[2:])
		}
		var err error
		repositoryPath, err = filepath.Abs(filepath.Clean(repositoryPath))
		if err != nil {
			writeError(w, http.StatusBadRequest, "repository_invalid", "项目目录无法解析。")
			return
		}
	}
	if repositoryPath == "" {
		writeError(w, http.StatusBadRequest, "repository_required", "请选择本地项目文件夹。")
		return
	}
	in := core.CreateTaskInput{Mode: body.Mode, ContextIDs: body.ContextIDs, Title: body.Title, Prompt: body.Prompt, IdempotencyKey: key,
		Adapter: adapter, RepositoryPath: repositoryPath, Model: strings.TrimSpace(body.Model), ReasoningEffort: body.ReasoningEffort, ModelProvider: strings.TrimSpace(body.ModelProvider)}
	// Resolve idempotency before looking at mutable source repository state.
	// A retry must still return its original task after HEAD or dirty state changes.
	detail, replayed, err := s.Store.LookupTaskCreation(r.Context(), in)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if !replayed {
		{
			if err := s.checkAdapter(r.Context(), adapter); err != nil {
				s.storeError(w, err)
				return
			}
			repo, err := (workspace.Manager{DataDir: s.DataDir}).Freeze(r.Context(), repositoryPath)
			if err != nil {
				s.storeError(w, err)
				return
			}
			in.BaseCommit = repo.BaseCommit
		}
		detail, replayed, err = s.Store.CreateTask(r.Context(), in)
	}
	if err != nil {
		s.storeError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/tasks/"+detail.Task.ID)
	created(w, detail, replayed)
}

func (s *Server) createTurn(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		Model           string   `json:"model"`
		ModelProvider   string   `json:"model_provider"`
		ReasoningEffort string   `json:"reasoning_effort"`
		Prompt          string   `json:"prompt"`
		ContextIDs      []string `json:"context_ids"`
	}
	if !readJSON(w, r, &body) || !validPrompt(w, body.Prompt) {
		return
	}
	detail, replayed, err := s.Store.CreateTurn(r.Context(), r.PathValue("task"), core.CreateTurnInput{
		Prompt: body.Prompt, IdempotencyKey: key, ContextIDs: body.ContextIDs, Model: strings.TrimSpace(body.Model), ReasoningEffort: body.ReasoningEffort, ModelProvider: strings.TrimSpace(body.ModelProvider),
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	created(w, detail, replayed)
}

func created(w http.ResponseWriter, detail core.TaskDetail, replayed bool) {
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, status, detail)
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请提供有效的 JSON 请求。")
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求只能包含一个 JSON 对象。")
		return false
	}
	return true
}

func validPrompt(w http.ResponseWriter, prompt string) bool {
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请填写任务目标。")
		return false
	}
	if utf8.RuneCountInString(prompt) > 16000 {
		writeError(w, http.StatusBadRequest, "invalid_request", "任务目标不能超过 16000 个字符。")
		return false
	}
	return true
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "请提供不超过 200 字节的 Idempotency-Key 请求头。")
		return "", false
	}
	return key, true
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	var executionError *core.ExecutionError
	if errors.As(err, &executionError) {
		status := http.StatusBadRequest
		if (strings.HasPrefix(executionError.Code, "codex_") || strings.HasPrefix(executionError.Code, "claude_") || strings.HasPrefix(executionError.Code, "deepseek_") || strings.HasPrefix(executionError.Code, "gemini_")) || executionError.Code == "git_not_found" {
			status = http.StatusServiceUnavailable
		}
		if executionError.Code == "repository_dirty" {
			status = http.StatusConflict
		}
		writeError(w, status, executionError.Code, executionError.Message)
		return
	}
	switch {
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "没有找到该任务。")
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "当前会话已有待执行回合，或同一请求标识对应了不同内容。请刷新后重试。")
	case errors.Is(err, core.ErrStaleGeneration):
		writeError(w, http.StatusConflict, "stale_generation", "该执行结果已过期。")
	case errors.Is(err, core.ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "当前任务状态不支持此操作。")
	default:
		s.Logger.Error("database operation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "本地数据操作失败，请查看服务日志。")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	for _, raw := range []string{r.URL.Query().Get("after"), r.Header.Get("Last-Event-ID")} {
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "事件游标必须为非负整数。")
			return
		}
		after = max(after, value)
	}
	detail, err := s.Store.GetTask(r.Context(), r.PathValue("task"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	if after > detail.Task.LastEventSeq {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "事件游标超出了当前任务记录。")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unavailable", "当前连接不支持事件流。")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(value string) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.WriteString(w, value); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !write(": connected\nretry: 1000\n\n") {
		return
	}
	poll := time.NewTicker(150 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		events, err := s.Store.Events(r.Context(), detail.Task.ID, after, 250)
		if err != nil {
			if r.Context().Err() == nil {
				s.Logger.Error("event stream read failed", "task_id", detail.Task.ID, "error", err)
			}
			return
		}
		for _, event := range events {
			data, err := json.Marshal(event)
			if err != nil || !write(fmt.Sprintf("id: %d\nevent: blux.event\ndata: %s\n\n", event.Seq, data)) {
				return
			}
			after = event.Seq
		}
		if len(events) == 250 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if !write(": heartbeat\n\n") {
				return
			}
		}
	}
}

func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	index := filepath.Join(s.WebDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.Error(w, "Blux 前端尚未构建。请在项目目录运行 pnpm --dir web install && pnpm --dir web build，或使用 make dev。", http.StatusServiceUnavailable)
		return
	}
	cleanPath := path.Clean("/" + r.URL.Path)
	asset := filepath.Join(s.WebDir, filepath.FromSlash(cleanPath))
	if info, err := os.Stat(asset); err == nil && !info.IsDir() {
		if strings.HasPrefix(cleanPath, "/assets/") || strings.HasPrefix(cleanPath, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFile(w, r, asset)
		return
	}
	if path.Ext(cleanPath) != "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, index)
}
