package httpapi

import (
	"blux/internal/agent"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type modelCacheEntry struct {
	at      time.Time
	catalog agent.ModelCatalog
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agent")
	binary := ""
	switch id {
	case "codex":
		binary = s.CodexBinary
	case "claude":
		binary = s.ClaudeBinary
	case "deepseek":
		binary = s.DeepSeekBinary
	case "gemini":
		binary = s.GeminiBinary
	default:
		writeError(w, 404, "unknown_agent", "未知 Agent。")
		return
	}
	project := r.URL.Query().Get("project")
	if project != "" {
		p, e := filepath.Abs(project)
		if e != nil {
			writeError(w, 400, "invalid_project", "项目目录无效。")
			return
		}
		info, e := os.Stat(p)
		if e != nil || !info.IsDir() {
			writeError(w, 400, "invalid_project", "项目目录不存在。")
			return
		}
		project = p
	} else {
		var e error
		project, e = agent.CatalogWorkspace(s.DataDir)
		if e != nil {
			s.storeError(w, e)
			return
		}
	}
	key := id + "\x00" + binary + "\x00" + project
	s.modelMu.Lock()

	if c, ok := s.modelCache[key]; ok && time.Since(c.at) < 30*time.Second {
		s.modelMu.Unlock()
		writeJSON(w, 200, c.catalog)
		return
	}
	s.modelMu.Unlock()
	c, e := agent.ListModels(r.Context(), id, binary, project)
	if e != nil {
		c = agent.ModelCatalog{Models: []agent.ModelInfo{}, Source: id, Reason: "无法读取 CLI 模型列表，请检查本机 CLI 配置。"}
	}
	s.modelMu.Lock()
	if s.modelCache == nil || len(s.modelCache) > 64 {
		s.modelCache = map[string]modelCacheEntry{}
	}
	if r.Context().Err() == nil && e == nil {
		s.modelCache[key] = modelCacheEntry{time.Now(), c}
	}
	s.modelMu.Unlock()
	writeJSON(w, 200, c)
}
