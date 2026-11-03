package agent

import (
	"blux/internal/core"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeHeadless() {
	id := os.Getenv("BLUX_FAKE_HEADLESS")
	for _, a := range os.Args {
		if a == "--dump-config" {
			fmt.Println(`- id: native-model-entry
  name: '@deepseek-ai/dsh-agent-default-model'
  config: {provider: fixture-provider, model: test-model}`)
			return
		}
		if a == "--version" {
			fmt.Println("0.99.0-fixture")
			return
		}
		if a == "--help" {
			fmt.Println("--json --session-id --permission-mode --approval-policy --add-dir --output-format stream-json --resume --sandbox --approval-mode --include-directories")
			return
		}
	}
	flags := map[string][]string{}
	for i, a := range os.Args {
		if strings.HasPrefix(a, "--") && i+1 < len(os.Args) {
			flags[a] = append(flags[a], os.Args[i+1])
		}
	}
	flag := func(k string) string {
		if len(flags[k]) > 0 {
			return flags[k][0]
		}
		return ""
	}
	if id == "deepseek" {
		if expected := os.Getenv("BLUX_FAKE_MODEL"); expected != "" {
			raw, e := os.ReadFile(flag("--patch"))
			var patch []struct {
				ID     string `json:"id"`
				Config struct {
					Provider string `json:"provider"`
					Model    string `json:"model"`
				} `json:"config"`
			}
			if e != nil || json.Unmarshal(raw, &patch) != nil || len(patch) != 1 || patch[0].ID != "native-model-entry" || patch[0].Config.Model != expected || patch[0].Config.Provider != "fixture-provider" || flag("--profile") != "headless" {
				os.Exit(23)
			}
		}
		if flag("--permission-mode") != "workspace-write" || flag("--approval-policy") != "never" || len(flags["--add-dir"]) != 2 {
			os.Exit(20)
		}
	} else {
		if flag("--approval-mode") != "yolo" || flag("--output-format") != "stream-json" || len(flags["--include-directories"]) != 2 || os.Getenv("GEMINI_SANDBOX") != "sandbox-exec" || os.Getenv("SANDBOX") != "" {
			os.Exit(21)
		}
	}
	prompt, _ := io.ReadAll(os.Stdin)
	if !strings.Contains(string(prompt), "test task") {
		os.Exit(22)
	}
	cwd, _ := os.Getwd()
	session := "session-fixture"
	if v := flag("--session-id"); v != "" {
		session = v
	}
	if v := flag("--resume"); v != "" {
		session = v
	}
	send := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	if id == "deepseek" {
		send(map[string]any{"type": "session", "sessionId": session, "cwd": cwd})
	} else {
		send(map[string]any{"type": "init", "session_id": session, "model": "test-model"})
	}
	if os.Getenv("BLUX_FAKE_MODE") == "hang" {
		time.Sleep(time.Minute)
		return
	}
	if os.Getenv("BLUX_FAKE_MODE") == "eof" {
		return
	}
	if os.Getenv("BLUX_FAKE_MODE") == "failed" {
		send(map[string]any{"type": "error", "severity": "error", "message": "fixture quota unavailable"})
		return
	}
	if id == "deepseek" {
		send(map[string]any{"type": "status", "phase": "step_start"})
		send(map[string]any{"type": "text", "text": "hello"})
		send(map[string]any{"type": "tool_call", "callId": "tool-1", "tool": "write_file", "input": map[string]string{"file_path": "example.txt", "content": "hello"}})
		send(map[string]any{"type": "tool_result", "callId": "tool-1", "status": "completed", "result": "saved"})
		send(map[string]any{"type": "final", "sessionId": session, "reason": map[string]string{"kind": "completed"}, "text": "hello"})
	} else {
		send(map[string]any{"type": "message", "role": "assistant", "content": "hello", "delta": true})
		send(map[string]any{"type": "tool_use", "tool_id": "tool-1", "tool_name": "write_file", "parameters": map[string]string{"file_path": "example.txt", "content": "hello"}})
		send(map[string]any{"type": "tool_result", "tool_id": "tool-1", "status": "success", "output": "saved"})
		send(map[string]any{"type": "result", "status": "success", "stats": map[string]int{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}})
	}
}
func headlessConfig(t *testing.T) SessionConfig {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"code", "artifacts", "agent"} {
		if e := os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	return SessionConfig{Workspace: filepath.Join(root, "code"), ArtifactsPath: filepath.Join(root, "artifacts"), AgentPath: filepath.Join(root, "agent")}
}
func TestHeadlessProcessProtocolsResumeAndStrictCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native CLI launch not enabled on Windows")
	}
	exe, _ := os.Executable()
	for _, id := range []string{"deepseek", "gemini"} {
		t.Run(id, func(t *testing.T) {
			if id == "gemini" && runtime.GOOS != "darwin" {
				t.Skip("macOS native sandbox")
			}
			t.Setenv("BLUX_FAKE_HEADLESS", id)
			t.Setenv("SANDBOX", "inherited-marker-must-not-bypass-sandbox")
			adapter := Headless{ID: id, Binary: exe}
			config := headlessConfig(t)
			for _, mode := range []string{"ok", "ok", "failed", "eof"} {
				t.Setenv("BLUX_FAKE_MODE", mode)
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				var session Session
				var e error
				if config.ThreadID == "" {
					session, e = adapter.Start(ctx, config)
				} else {
					session, e = adapter.Resume(ctx, config)
				}
				if e != nil {
					cancel()
					t.Fatal(e)
				}
				updates, e := session.Send(ctx, TurnRequest{TurnID: core.NewID("turn"), Prompt: "test task"})
				if e != nil {
					cancel()
					t.Fatal(e)
				}
				failed := false
				kinds := map[string]int{}
				for update := range updates {
					if update.Err != nil {
						failed = true
					}
					kinds[update.Type]++
					if update.Type == "agent.session.ready" {
						var ready struct {
							ID string `json:"native_thread_id"`
						}
						json.Unmarshal(update.Payload, &ready)
						if config.ThreadID != "" && config.ThreadID != ready.ID {
							t.Fatal("resume changed identity")
						}
						config.ThreadID = ready.ID
					}
				}
				if e = session.Close(); e != nil {
					t.Fatal(e)
				}
				cancel()
				if failed != (mode != "ok") {
					t.Fatal(mode, failed)
				}
				if mode == "ok" && (kinds["tool.completed"] != 1 || kinds["file.changed"] != 1) {
					t.Fatal(kinds)
				}
			}
		})
	}
}
func TestHeadlessCancellationTerminatesProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native launch unsupported")
	}
	t.Setenv("BLUX_FAKE_HEADLESS", "deepseek")
	t.Setenv("BLUX_FAKE_MODE", "hang")
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, e := (Headless{ID: "deepseek", Binary: exe}).Start(ctx, headlessConfig(t))
	if e != nil {
		t.Fatal(e)
	}
	updates, e := s.Send(ctx, TurnRequest{TurnID: "turn", Prompt: "test task"})
	if e != nil {
		t.Fatal(e)
	}
	for u := range updates {
		if u.Type == "agent.session.ready" {
			break
		}
	}
	before := time.Now()
	if e = s.Interrupt(ctx, ""); e != nil {
		t.Fatal(e)
	}
	for range updates {
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if time.Since(before) > 3*time.Second {
		t.Fatal("stop exceeded cleanup bound")
	}
}
func TestHeadlessRejectsWrongIdentityAndFalseCompletion(t *testing.T) {
	for _, raw := range []string{`{"type":"session","sessionId":"wrong","cwd":"/tmp"}`, `{"type":"final","sessionId":"original","reason":{"kind":"completed"}}`, `{"type":"result","status":"success"}`, `not-json`} {
		state := newHeadlessState(Headless{ID: "deepseek"}, SessionConfig{ThreadID: "original", Workspace: "/tmp"}, CLIInfo{}, "turn", func(string, any) error { return nil })
		if state.consume([]byte(raw)) == nil {
			t.Fatal("unconfirmed frame accepted", raw)
		}
	}
}

func TestDeepSeekSelectedModelUsesInvocationPatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("headless adapter not enabled on Windows")
	}
	exe, _ := os.Executable()
	t.Setenv("BLUX_FAKE_HEADLESS", "deepseek")
	t.Setenv("BLUX_FAKE_MODEL", "selected-native-model")
	t.Setenv("BLUX_FAKE_MODE", "ok")
	c := headlessConfig(t)
	c.Model = "selected-native-model"
	c.ModelProvider = "fixture-provider"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, e := (Headless{ID: "deepseek", Binary: exe}).Start(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	stream, e := session.Send(ctx, TurnRequest{TurnID: "model-test", Prompt: "test task"})
	if e != nil {
		t.Fatal(e)
	}
	confirmed := false
	for update := range stream {
		if update.Err != nil {
			t.Fatal(update.Err)
		}
		if update.Type == "agent.session.ready" {
			var rt core.RuntimeInfo
			json.Unmarshal(update.Payload, &rt)
			confirmed = rt.Model == c.Model && rt.ModelProvider == c.ModelProvider
		}
	}
	if !confirmed {
		t.Fatal("selected model was not applied to native invocation")
	}
}
