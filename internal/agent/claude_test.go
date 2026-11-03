package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeClaude() {
	for _, arg := range os.Args {
		if arg == "--version" {
			fmt.Println("2.1.301 (Claude Code)")
			return
		}
		if arg == "--help" {
			fmt.Println("--restricted --permission-prompts --system-prompt-snapshot")
			return
		}
	}
	// Validate the actual subprocess contract, not only translated output.
	required := map[string]string{"--permission-mode": "dontAsk", "--permission-prompts": "none", "--system-prompt-snapshot": "off", "--mcp-config": `{"mcpServers":{}}`}
	flags := map[string]string{}
	for i, arg := range os.Args {
		if i+1 < len(os.Args) {
			flags[arg] = os.Args[i+1]
		}
	}
	if expected := os.Getenv("BLUX_FAKE_EFFORT"); expected != "" && flags["--effort"] != expected {
		os.Exit(24)
	}
	for flag, value := range required {
		if flags[flag] != value {
			os.Exit(20)
		}
	}
	var settings struct {
		DisableAllHooks bool `json:"disableAllHooks"`
		Sandbox         struct {
			Enabled     bool `json:"enabled"`
			Fail        bool `json:"failIfUnavailable"`
			Unsandboxed bool `json:"allowUnsandboxedCommands"`
			Network     struct {
				Domains []string `json:"allowedDomains"`
				Strict  bool     `json:"strictAllowlist"`
			} `json:"network"`
		} `json:"sandbox"`
	}
	if json.Unmarshal([]byte(flags["--settings"]), &settings) != nil || !settings.DisableAllHooks || !settings.Sandbox.Enabled || !settings.Sandbox.Fail || settings.Sandbox.Unsandboxed {
		os.Exit(21)
	}
	// BLUX_FAKE_NETWORK pins the exact tool list and domain allowlist the
	// adapter must pass: "open" is the long-standing default, "restricted"
	// removes web tools and every allowed domain.
	switch os.Getenv("BLUX_FAKE_NETWORK") {
	case "open":
		if flags["--tools"] != "Read,Edit,Write,Bash,Glob,Grep,WebFetch,WebSearch" || flags["--allowedTools"] != flags["--tools"] || len(settings.Sandbox.Network.Domains) != 1 || settings.Sandbox.Network.Domains[0] != "*" || !settings.Sandbox.Network.Strict {
			os.Exit(29)
		}
	case "restricted":
		if flags["--tools"] != "Read,Edit,Write,Bash,Glob,Grep" || flags["--allowedTools"] != flags["--tools"] || len(settings.Sandbox.Network.Domains) != 0 || !settings.Sandbox.Network.Strict {
			os.Exit(30)
		}
	}
	id := ""
	for i, arg := range os.Args {
		if (arg == "--session-id" || arg == "--resume") && i+1 < len(os.Args) {
			id = os.Args[i+1]
		}
	}
	io.ReadAll(os.Stdin)
	send := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	send(map[string]any{"type": "system", "subtype": "init", "session_id": id, "model": "test-model", "permissionMode": "dontAsk"})
	if os.Getenv("BLUX_FAKE_MODE") == "failed" {
		send(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "session_id": id, "errors": []string{"API key unavailable"}})
		return
	}
	send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]string{"id": "message-1"}}})
	send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "hello"}}})
	send(map[string]any{"type": "assistant", "message": map[string]any{"id": "message-1", "content": []any{map[string]string{"type": "text", "text": "hello world"}}}})
	send(map[string]any{"type": "result", "subtype": "success", "session_id": id, "result": "hello world", "usage": map[string]int{"input_tokens": 4, "output_tokens": 3, "cache_read_input_tokens": 2}})
}
func TestClaudePrintProtocolAndResume(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("BLUX_FAKE_CLAUDE", "1")
	t.Setenv("BLUX_FAKE_MODE", "ok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	for _, n := range []string{"code", "artifacts", "agent"} {
		os.Mkdir(filepath.Join(root, n), 0700)
	}
	config := SessionConfig{Workspace: filepath.Join(root, "code"), ArtifactsPath: filepath.Join(root, "artifacts"), AgentPath: filepath.Join(root, "agent")}
	for i := 0; i < 2; i++ {
		config.ReasoningEffort = []string{"high", "low"}[i]
		t.Setenv("BLUX_FAKE_EFFORT", config.ReasoningEffort)
		var s Session
		var err error
		if i == 0 {
			s, err = (Claude{Binary: exe}).Start(ctx, config)
		} else {
			s, err = (Claude{Binary: exe}).Resume(ctx, config)
		}
		if err != nil {
			t.Fatal(err)
		}
		updates, err := s.Send(ctx, TurnRequest{TurnID: fmt.Sprint(i), Prompt: "test"})
		if err != nil {
			t.Fatal(err)
		}
		snapshots := 0
		for update := range updates {
			if update.Err != nil {
				t.Fatal(update.Err)
			}
			if update.Type == "agent.session.ready" {
				var rt struct {
					ID string `json:"native_thread_id"`
				}
				json.Unmarshal(update.Payload, &rt)
				if i == 1 && config.ThreadID != rt.ID {
					t.Fatal("thread changed")
				}
				config.ThreadID = rt.ID
			}
			if update.Type == "message.snapshot" {
				snapshots++
			}
		}
		if err = s.Close(); err != nil || snapshots != 1 {
			t.Fatal(err, snapshots)
		}
	}
}

// The default arguments must stay exactly as they were, and a restricted
// session must drop web tools and allow no domains. The fake binary exits with
// a distinct code when either contract is violated.
func TestClaudeRestrictNetworkChangesToolsAndDomains(t *testing.T) {
	exe, _ := os.Executable()
	for _, restrict := range []bool{false, true} {
		name, expect := "open", "open"
		if restrict {
			name, expect = "restricted", "restricted"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("BLUX_FAKE_CLAUDE", "1")
			t.Setenv("BLUX_FAKE_MODE", "ok")
			t.Setenv("BLUX_FAKE_NETWORK", expect)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			root := t.TempDir()
			for _, n := range []string{"code", "artifacts", "agent"} {
				os.Mkdir(filepath.Join(root, n), 0700)
			}
			s, err := (Claude{Binary: exe}).Start(ctx, SessionConfig{Workspace: filepath.Join(root, "code"), ArtifactsPath: filepath.Join(root, "artifacts"), AgentPath: filepath.Join(root, "agent"), RestrictNetwork: restrict})
			if err != nil {
				t.Fatal(err)
			}
			updates, err := s.Send(ctx, TurnRequest{TurnID: "turn-1", Prompt: "test"})
			if err != nil {
				t.Fatal(err)
			}
			snapshots := 0
			for update := range updates {
				if update.Err != nil {
					t.Fatal(update.Err)
				}
				if update.Type == "message.snapshot" {
					snapshots++
				}
			}
			if err = s.Close(); err != nil || snapshots != 1 {
				t.Fatal(err, snapshots)
			}
		})
	}
}

func TestClaudeStateRejectsUnconfirmedAndFailedResults(t *testing.T) {
	for _, raw := range []string{
		`{"type":"result","subtype":"success","result":"unconfirmed"}`,
		`{"type":"system","subtype":"init","session_id":"other","model":"model","permissionMode":"dontAsk"}`,
		`{"type":"system","subtype":"init","session_id":"native","model":"model","permissionMode":"bypassPermissions"}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API key unavailable"}`,
	} {
		s := newClaudeState("turn", "native", ClaudeInfo{}, SessionConfig{}, func(string, any) error { return nil })
		if e := s.consume([]byte(raw)); e == nil {
			t.Fatal("invalid result accepted", raw)
		}
	}
}
func TestClaudeReusesOnlyProviderConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	raw := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"fixture-secret","ANTHROPIC_BASE_URL":"https://fixture.invalid","PATH":"malicious-path"},"model":"fixture-model","hooks":{"SessionStart":["must-not-run"]}}`)
	os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0600)
	env, e := claudeEnvironment()
	if e != nil {
		t.Fatal(e)
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if values["ANTHROPIC_AUTH_TOKEN"] != "fixture-secret" || values["ANTHROPIC_BASE_URL"] != "https://fixture.invalid" || values["ANTHROPIC_MODEL"] != "fixture-model" || values["PATH"] == "malicious-path" {
		t.Fatal("provider config was not isolated")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(after) != string(raw) {
		t.Fatal("global settings changed")
	}
	t.Setenv("ANTHROPIC_MODEL", "explicit-model")
	env, e = claudeEnvironment()
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "ANTHROPIC_MODEL=") {
			count++
			if entry != "ANTHROPIC_MODEL=explicit-model" {
				t.Fatal("explicit environment ignored")
			}
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
}
