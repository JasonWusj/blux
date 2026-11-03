package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"blux/internal/agent"
	"blux/internal/core"
	"blux/internal/store"
	"os/exec"
)

func testServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "blux.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := filepath.Join(dir, "repo")
	os.Mkdir(repo, 0700)
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Blux Test", "-c", "user.email=test@local", "commit", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(string(out), e)
		}
	}
	api := &Server{probeAt: time.Now(), probeInfo: agent.CodexInfo{Available: true}, Store: db, DataDir: dir, WebDir: filepath.Join(dir, "web"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return server, db, repo
}

func post(t *testing.T, server *httptest.Server, path, key, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestHTTPSubmissionContract(t *testing.T) {
	server, _, repo := testServer(t)
	for _, test := range []struct{ name, key, body string }{
		{"missing idempotency key", "", `{"prompt":"目标"}`},
		{"empty prompt", "empty", `{"prompt":"  "}`},
		{"multiple JSON values", "multiple", `{"prompt":"目标"} {}`},
		{"unknown field", "unknown", `{"prompt":"目标","adapter":"codex","unexpected":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := post(t, server, "/api/v1/tasks", test.key, test.body)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
	payload := func(prompt string) string {
		b, _ := json.Marshal(map[string]string{"prompt": prompt, "adapter": "codex", "repository_path": repo})
		return string(b)
	}
	first := post(t, server, "/api/v1/tasks", "create-once", payload("测试持久化目标"))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", first.StatusCode)
	}
	var created core.TaskDetail
	if err := json.NewDecoder(first.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	retry := post(t, server, "/api/v1/tasks", "create-once", payload("测试持久化目标"))
	if retry.StatusCode != http.StatusOK || retry.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("retry = %d %v", retry.StatusCode, retry.Header)
	}
	var repeated core.TaskDetail
	if err := json.NewDecoder(retry.Body).Decode(&repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.Task.ID != created.Task.ID || repeated.SubmittedTurnID != created.SubmittedTurnID {
		t.Fatal("idempotent retry changed task or submitted turn identity")
	}
	conflict := post(t, server, "/api/v1/tasks", "create-once", payload("不同内容"))
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d", conflict.StatusCode)
	}
	active := post(t, server, "/api/v1/tasks/"+created.Task.ID+"/turns", "new-turn", `{"prompt":"不能并行"}`)
	if active.StatusCode != http.StatusConflict {
		t.Fatalf("active turn status = %d", active.StatusCode)
	}
}

func TestSSECursorAndWireFormat(t *testing.T) {
	server, db, _ := testServer(t)
	detail, _, err := db.CreateTask(context.Background(), core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "事件续收", IdempotencyKey: "sse"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/tasks/"+detail.Task.ID+"/events?after=0", nil)
	req.Header.Set("Last-Event-ID", "1")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("invalid SSE response: %d %v", response.StatusCode, response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	var id, eventName string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var event core.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			if id != "2" || eventName != "blux.event" || event.Seq != 2 || event.Type != "turn.queued" || event.TaskID != detail.Task.ID {
				t.Fatalf("incorrect resumed event: %s %s %+v", id, eventName, event)
			}
			return
		}
	}
	t.Fatalf("no event received: %v", scanner.Err())
}

func TestAPIErrorShapeAndCursorValidation(t *testing.T) {
	server, db, _ := testServer(t)
	detail, _, err := db.CreateTask(context.Background(), core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "游标", IdempotencyKey: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		code int
	}{
		{"/api/v1/tasks/missing", 404},
		{"/api/v1/unknown", 404},
		{"/api/v1/tasks/" + detail.Task.ID + "/events?after=-1", 400},
		{"/api/v1/tasks/" + detail.Task.ID + "/events?after=999", 400},
		{"/api/v1/tasks/" + detail.Task.ID + "/events?after=hello", 400},
	} {
		response, err := server.Client().Get(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != test.code || !bytes.Contains(data, []byte(`"error"`)) {
			t.Fatalf("%s returned %d %s", test.path, response.StatusCode, data)
		}
	}
}

func TestSvelteKitStaticDeployment(t *testing.T) {
	dir := t.TempDir()
	webDir := filepath.Join(dir, "dist")
	files := map[string]string{
		"index.html":                     "<!doctype html><html><title>Blux · Open WebUI</title></html>",
		"_app/immutable/chunks/chat.js":  "export const version = 1;",
		"_app/immutable/assets/chat.css": "body { color: black; }",
		"_app/version.json":              `{"version":"phase-1"}`,
		"licenses/open-webui/LICENSE":    "Open WebUI License",
	}
	for name, content := range files {
		file := filepath.Join(webDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(filepath.Join(dir, "blux.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	api := &Server{Store: db, DataDir: dir, WebDir: webDir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	for _, test := range []struct {
		path, body, contentType, cache string
		status                         int
	}{
		{"/", "Open WebUI", "text/html", "no-cache", 200},
		{"/?task=task_example", "Open WebUI", "text/html", "no-cache", 200},
		{"/tasks/task_example", "Open WebUI", "text/html", "no-cache", 200},
		{"/_app/immutable/chunks/chat.js", "export const", "javascript", "immutable", 200},
		{"/_app/immutable/assets/chat.css", "body", "text/css", "immutable", 200},
		{"/_app/version.json", "phase-1", "application/json", "no-cache", 200},
		{"/licenses/open-webui/LICENSE", "Open WebUI License", "text/plain", "no-cache", 200},
		{"/_app/immutable/chunks/missing.js", "404", "text/plain", "", 404},
		{"/api/v1/missing", `"error"`, "application/json", "no-store", 404},
	} {
		t.Run(test.path, func(t *testing.T) {
			response, err := server.Client().Get(server.URL + test.path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != test.status || !strings.Contains(string(body), test.body) {
				t.Fatalf("status %d, body %s", response.StatusCode, body)
			}
			if !strings.Contains(response.Header.Get("Content-Type"), test.contentType) || !strings.Contains(response.Header.Get("Cache-Control"), test.cache) {
				t.Fatalf("incorrect static headers: %v", response.Header)
			}
		})
	}
}

func TestMissingAdditionalCLIsAreRejectedBeforeQueueing(t *testing.T) {
	dir := t.TempDir()
	db, e := store.Open(filepath.Join(dir, "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	api := &Server{Store: db, DataDir: dir, DeepSeekBinary: filepath.Join(dir, "missing-dsh"), GeminiBinary: filepath.Join(dir, "missing-gemini"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	for _, id := range []string{"deepseek", "gemini"} {
		response := post(t, server, "/api/v1/tasks", id, `{"adapter":"`+id+`","prompt":"task","repository_path":"/not-inspected"}`)
		var result struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		json.NewDecoder(response.Body).Decode(&result)
		if response.StatusCode != 503 || result.Error.Code != id+"_not_found" {
			t.Fatal(id, response.StatusCode, result.Error.Code)
		}
	}
	tasks, e := db.ListTasks(context.Background())
	if e != nil || len(tasks) > 0 {
		t.Fatal("unavailable CLI left queued tasks", tasks, e)
	}
}
