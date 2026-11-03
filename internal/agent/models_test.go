package agent

import (
	"blux/internal/core"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeContextCountersAreNotCumulativeUsage(t *testing.T) {
	var contexts []core.ContextUsage
	emit := func(kind string, v any) error {
		if kind == "context.updated" {
			raw, _ := json.Marshal(v)
			var c core.ContextUsage
			json.Unmarshal(raw, &c)
			contexts = append(contexts, c)
		}
		return nil
	}
	c := newClaudeState("turn", "id", ClaudeInfo{}, SessionConfig{}, emit)
	frames := []string{
		`{"type":"system","subtype":"init","session_id":"id","model":"native-model","permissionMode":"dontAsk"}`,
		`{"type":"assistant","message":{"id":"a","model":"native-model","usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":20,"output_tokens":5}}}`,
		`{"type":"result","subtype":"success","session_id":"id","usage":{"input_tokens":99999},"modelUsage":{"native-model":{"contextWindow":200000}}}`,
	}
	for _, f := range frames {
		if e := c.consume([]byte(f)); e != nil {
			t.Fatal(e)
		}
	}
	if len(contexts) != 2 || contexts[1].UsedTokens != 135 || contexts[1].WindowTokens != 200000 {
		t.Fatalf("wrong Claude context: %+v", contexts)
	}
	contexts = nil
	var n codexNotification
	if e := json.Unmarshal([]byte(`{"tokenUsage":{"total":{"totalTokens":90000},"last":{"totalTokens":1200},"modelContextWindow":10000}}`), &n); e != nil {
		t.Fatal(e)
	}
	state := codexTurnState{emit: emit, model: "native-model"}
	if e := state.handle("thread/tokenUsage/updated", n); e != nil {
		t.Fatal(e)
	}
	if len(contexts) != 1 || contexts[0].UsedTokens != 1200 || contexts[0].WindowTokens != 10000 {
		t.Fatalf("wrong Codex context: %+v", contexts)
	}
	contexts = nil
	n.TokenUsage.Last = nil
	if e := state.handle("thread/tokenUsage/updated", n); e != nil {
		t.Fatal(e)
	}
	if len(contexts) != 0 {
		t.Fatal("missing context fabricated zero")
	}
}
func TestDeepSeekConfiguredCatalogDoesNotExposeCredentials(t *testing.T) {
	raw := []byte(`- id: model-entry
  name: '@deepseek-ai/dsh-agent-default-model'
  config: {provider: custom, model: native-a}
- id: adapter
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      custom:
        apiKey: secret-that-must-stay-local
        models:
          - {id: native-a, name: Model A}
          - {id: native-b, reasoningEfforts: {off: null, high: high}}
`)
	p, e := parseDeepSeekProfile(raw)
	if e != nil {
		t.Fatal(e)
	}
	if p.EntryID != "model-entry" || p.Catalog.DefaultModel != "native-a" || len(p.Catalog.Models) != 2 {
		t.Fatalf("wrong profile: %+v", p)
	}
	if len(p.Catalog.Models[1].ReasoningEfforts) != 2 || p.Catalog.Models[1].ReasoningEfforts[1].ID != "high" {
		t.Fatalf("missing native effort levels: %+v", p.Catalog.Models)
	}
	b, _ := json.Marshal(p.Catalog)
	if strings.Contains(string(b), "secret-that-must-stay-local") || strings.Contains(string(b), "apiKey") {
		t.Fatal("catalog exposed credentials")
	}
	var fields map[string]any
	if json.Unmarshal(b, &fields) != nil {
		t.Fatal("invalid catalog")
	}
	for _, m := range p.Catalog.Models {
		if m.Provider != "custom" || m.Model == "" {
			t.Fatalf("provider lost: %+v", m)
		}
	}
}
