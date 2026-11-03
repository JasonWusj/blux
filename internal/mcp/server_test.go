package mcp

import (
	"blux/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolAndHumanGateBoundary(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	repo := t.TempDir()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Blux Test", "-c", "user.email=test@local", "commit", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(string(out), e)
		}
	}
	s := Server{Store: db, DataDir: t.TempDir(), Project: repo}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"start_delivery","arguments":{"goal":"codex","adapter":"codex","idempotency_key":"one"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"approve","arguments":{}}}
`
	var out bytes.Buffer
	if e = s.Run(context.Background(), strings.NewReader(input), &out); e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatal(out.String())
	}
	for _, line := range lines {
		var v map[string]any
		if e = json.Unmarshal([]byte(line), &v); e != nil {
			t.Fatal(e)
		}
	}
	var denied struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[3]), &denied)
	if !denied.Result.IsError {
		t.Fatal("manager approved a gate")
	}
	if _, e = s.call(context.Background(), "start_delivery", json.RawMessage(`{"goal":"codex","adapter":"codex","idempotency_key":"one"}`)); e != nil {
		t.Fatal(e)
	}
	list, e := db.Deliveries(context.Background())
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	for _, tool := range tools() {
		if strings.Contains(tool.Name, "approve") || strings.Contains(tool.Name, "accept") {
			t.Fatal("privileged tool")
		}
	}
}
