package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("BLUX_FAKE_HEADLESS") != "" {
		fakeHeadless()
		os.Exit(0)
	}
	if os.Getenv("BLUX_FAKE_CLAUDE") == "1" {
		fakeClaude()
		os.Exit(0)
	}
	if os.Getenv("BLUX_FAKE_CODEX") == "1" {
		fakeCodex()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fakeCodex() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("codex-cli 0.163.0")
		return
	}
	if len(os.Args) > 2 && os.Args[2] == "--help" {
		fmt.Println("--listen stdio://")
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	send := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	notify := func(method string, p any) { send(map[string]any{"method": method, "params": p}) }
	var thread map[string]any
	for sc.Scan() {
		var in struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		json.Unmarshal(sc.Bytes(), &in)
		if in.ID == nil {
			continue
		}
		var result any = map[string]any{}
		switch in.Method {
		case "thread/start", "thread/resume":
			cfg := in.Params["config"].(map[string]any)
			if expected := os.Getenv("BLUX_FAKE_EFFORT"); expected != "" && cfg["model_reasoning_effort"] != expected {
				os.Exit(25)
			}
			sandbox := "workspaceWrite"
			if os.Getenv("BLUX_FAKE_MODE") == "unsafe" {
				sandbox = "dangerFullAccess"
			}
			thread = map[string]any{"thread": map[string]any{"id": "native-thread", "status": map[string]string{"type": "idle"}}, "model": "test-model", "modelProvider": "test", "reasoningEffort": cfg["model_reasoning_effort"], "cwd": in.Params["cwd"], "approvalPolicy": in.Params["approvalPolicy"], "sandbox": map[string]any{"type": sandbox, "networkAccess": true, "writableRoots": cfg["sandbox_workspace_write.writable_roots"]}}
			result = thread
		case "turn/start":
			if expected := os.Getenv("BLUX_FAKE_EFFORT"); expected != "" && in.Params["effort"] != expected {
				os.Exit(26)
			}
			result = map[string]any{"turn": map[string]string{"id": "native-turn", "status": "inProgress"}}
			send(map[string]any{"id": in.ID, "result": result})
			p := map[string]any{"threadId": "native-thread", "turnId": "native-turn", "itemId": "item-1", "delta": "hello"}
			notify("item/agentMessage/delta", p)
			p["item"] = map[string]string{"id": "item-1", "type": "agentMessage", "text": "hello world"}
			notify("item/completed", p)
			status := "completed"
			var e any
			if os.Getenv("BLUX_FAKE_MODE") == "failed" {
				status = "failed"
				e = map[string]string{"message": "test quota", "codexErrorInfo": "usageLimitExceeded"}
			}
			notify("turn/completed", map[string]any{"threadId": "native-thread", "turn": map[string]any{"id": "native-turn", "status": status, "error": e}})
			continue
		}
		send(map[string]any{"id": in.ID, "result": result})
	}
}
func TestCodexWireAndFailure(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("BLUX_FAKE_CODEX", "1")
	for _, mode := range []string{"ok", "unsafe", "failed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BLUX_FAKE_MODE", mode)
			root := t.TempDir()
			for _, n := range []string{"code", "artifacts", "agent"} {
				os.Mkdir(filepath.Join(root, n), 0700)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := (Codex{Binary: exe}).Start(ctx, SessionConfig{Workspace: filepath.Join(root, "code"), ArtifactsPath: filepath.Join(root, "artifacts"), AgentPath: filepath.Join(root, "agent")})
			if mode == "unsafe" {
				if err == nil {
					defer s.Close()
					t.Fatal("accepted unsafe sandbox")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			updates, err := s.Send(ctx, TurnRequest{TurnID: "turn-1", Prompt: "test"})
			if err != nil {
				t.Fatal(err)
			}
			var kinds []string
			var turnErr error
			for u := range updates {
				if u.Err != nil {
					turnErr = u.Err
				}
				kinds = append(kinds, u.Type)
			}
			if mode == "failed" {
				if turnErr == nil || !strings.Contains(turnErr.Error(), "quota") {
					t.Fatal(turnErr)
				}
			} else if turnErr != nil || !strings.Contains(strings.Join(kinds, ","), "message.snapshot") {
				t.Fatalf("%v %v", kinds, turnErr)
			}
		})
	}
}

func TestCodexReasoningEffortStartAndResume(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("BLUX_FAKE_CODEX", "1")
	t.Setenv("BLUX_FAKE_MODE", "ok")
	root := t.TempDir()
	for _, name := range []string{"code", "artifacts", "agent"} {
		os.Mkdir(filepath.Join(root, name), 0700)
	}
	config := SessionConfig{Workspace: filepath.Join(root, "code"), ArtifactsPath: filepath.Join(root, "artifacts"), AgentPath: filepath.Join(root, "agent")}
	for i, effort := range []string{"high", "low"} {
		t.Setenv("BLUX_FAKE_EFFORT", effort)
		config.ReasoningEffort = effort
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var session Session
		var e error
		if i == 0 {
			session, e = (Codex{Binary: exe}).Start(ctx, config)
		} else {
			config.ThreadID = "native-thread"
			session, e = (Codex{Binary: exe}).Resume(ctx, config)
		}
		if e != nil {
			cancel()
			t.Fatal(e)
		}
		stream, e := session.Send(ctx, TurnRequest{TurnID: fmt.Sprint(i), Prompt: "test"})
		if e != nil {
			t.Fatal(e)
		}
		confirmed := false
		for u := range stream {
			if u.Err != nil {
				t.Fatal(u.Err)
			}
			if u.Type == "agent.session.ready" {
				var rt struct {
					Effort string `json:"reasoning_effort"`
				}
				json.Unmarshal(u.Payload, &rt)
				confirmed = rt.Effort == effort
			}
		}
		session.Close()
		cancel()
		if !confirmed {
			t.Fatal("effort was not confirmed")
		}
	}
}
