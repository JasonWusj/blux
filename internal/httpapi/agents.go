package httpapi

import (
	"blux/internal/agent"
	"blux/internal/core"
	"context"
	"net/http"
	"time"
)

func (s *Server) checkAdapter(ctx context.Context, id string) error {
	switch id {
	case "codex":
		_, e := s.codexStatus(ctx)
		return e
	case "deepseek":
		_, e := (agent.Headless{ID: id, Binary: s.DeepSeekBinary}).Probe(ctx)
		return e
	case "gemini":
		_, e := (agent.Headless{ID: id, Binary: s.GeminiBinary}).Probe(ctx)
		return e
	case "claude":
		_, e := agent.ProbeClaude(ctx, s.ClaudeBinary)
		return e
	}
	return core.ErrInvalidState
}

func (s *Server) agents(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		ID           string             `json:"id"`
		Name         string             `json:"name"`
		Available    bool               `json:"available"`
		Version      string             `json:"version"`
		Reason       string             `json:"reason,omitempty"`
		Capabilities agent.Capabilities `json:"capabilities"`
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	codex, ce := s.codexStatus(ctx)
	claude, ae := agent.ProbeClaude(ctx, s.ClaudeBinary)
	out := []entry{{ID: "codex", Name: "Codex", Available: codex.Available, Version: codex.Version, Capabilities: (agent.Codex{}).Capabilities()}, {ID: "claude", Name: "Claude Code", Available: claude.Available, Version: claude.Version, Capabilities: (agent.Claude{}).Capabilities()}}
	if ce != nil {
		out[0].Reason = ce.Error()
	}
	if ae != nil {
		out[1].Reason = ae.Error()
	}
	for _, v := range []struct{ id, name, binary string }{{"deepseek", "DeepSeek Harness", s.DeepSeekBinary}, {"gemini", "Gemini CLI", s.GeminiBinary}} {
		adapter := agent.Headless{ID: v.id, Binary: v.binary}
		info, e := adapter.Probe(ctx)
		item := entry{ID: v.id, Name: v.name, Available: info.Available, Version: info.Version, Capabilities: adapter.Capabilities()}
		if e != nil {
			item.Reason = e.Error()
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"agents": out})
}
