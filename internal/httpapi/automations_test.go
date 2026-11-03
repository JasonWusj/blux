package httpapi

import (
	"blux/internal/automation"
	"blux/internal/core"
	"blux/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func send(t *testing.T, server *httptest.Server, method, path, key, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
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
	defer response.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(response.Body).Decode(&out)
	return response.StatusCode, out
}

// finish lets the worker's automation service notice that turns have ended.
func finish(t *testing.T, db *store.Store) {
	t.Helper()
	if err := (&automation.Service{Store: db, DataDir: t.TempDir()}).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func errorCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		return code
	}
	return ""
}

func automationJSON(repo string, extra map[string]any) string {
	body := map[string]any{"name": "每日检查", "prompt": "run the tests and report", "repository_path": repo, "adapter": "codex", "schedule_time": "09:00"}
	for k, v := range extra {
		body[k] = v
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func TestCreateAutomationValidatesAndIsIdempotent(t *testing.T) {
	server, _, repo := testServer(t)
	for _, test := range []struct {
		name, key, body, wantCode string
		status                    int
	}{
		{"missing idempotency key", "", automationJSON(repo, nil), "idempotency_key_required", 400},
		{"unknown field", "k", `{"prompt":"x","repository_path":"` + repo + `","bogus":1}`, "invalid_request", 400},
		{"empty prompt", "k", automationJSON(repo, map[string]any{"prompt": "  "}), "invalid_request", 400},
		{"no project", "k", automationJSON("", nil), "repository_required", 400},
		{"missing project folder", "k", automationJSON(repo+"/missing", nil), "repository_not_found", 400},
		{"unsupported agent", "k", automationJSON(repo, map[string]any{"adapter": "gemini"}), "adapter_unsupported", 400},
		{"bad time", "k", automationJSON(repo, map[string]any{"schedule_time": "25:00"}), "invalid_request", 400},
		{"bad network value", "k", automationJSON(repo, map[string]any{"network": "everything"}), "invalid_request", 400},
		{"timeout too long", "k", automationJSON(repo, map[string]any{"timeout_seconds": 999999}), "invalid_request", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := send(t, server, "POST", "/api/v1/automations", test.key, test.body)
			if status != test.status || errorCode(body) != test.wantCode {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
	status, created := send(t, server, "POST", "/api/v1/automations", "once", automationJSON(repo, nil))
	if status != 201 || created["id"] == "" || created["network"] != "restricted" || created["timeout_seconds"] != float64(1800) || created["daily_limit"] != float64(5) || created["paused"] != false {
		t.Fatalf("defaults wrong: %d %v", status, created)
	}
	if created["next_run_at"] == nil {
		t.Fatal("a scheduled automation should report its next run")
	}
	status, again := send(t, server, "POST", "/api/v1/automations", "once", automationJSON(repo, nil))
	if status != 200 || again["id"] != created["id"] {
		t.Fatalf("replay: %d %v", status, again)
	}
	status, conflict := send(t, server, "POST", "/api/v1/automations", "once", automationJSON(repo, map[string]any{"name": "different"}))
	if status != 409 {
		t.Fatalf("a reused key with different content must conflict: %d %v", status, conflict)
	}
	_, list := send(t, server, "GET", "/api/v1/automations", "", "")
	if items, _ := list["automations"].([]any); len(items) != 1 {
		t.Fatalf("%v", list)
	}
}

func TestAutomationLifecycleUpdatePauseResumeDelete(t *testing.T) {
	server, _, repo := testServer(t)
	_, created := send(t, server, "POST", "/api/v1/automations", "c", automationJSON(repo, nil))
	id := created["id"].(string)
	base := "/api/v1/automations/" + id

	status, updated := send(t, server, "PUT", base, "", automationJSON(repo, map[string]any{"name": "改名", "schedule_time": "18:30", "network": "open"}))
	if status != 200 || updated["name"] != "改名" || updated["schedule_time"] != "18:30" || updated["network"] != "open" || updated["anchor_at"] == created["anchor_at"] {
		t.Fatalf("%d %v", status, updated)
	}
	if status, body := send(t, server, "PUT", base, "", automationJSON(repo+"/elsewhere", nil)); status != 400 || !strings.Contains(body["error"].(map[string]any)["message"].(string), "不能更换项目") {
		t.Fatalf("moving the project must be refused: %d %v", status, body)
	}
	if status, _ := send(t, server, "PUT", base+"/paused", "", `{}`); status != 400 {
		t.Fatalf("paused is required: %d", status)
	}
	status, paused := send(t, server, "PUT", base+"/paused", "", `{"paused":true}`)
	if status != 200 || paused["paused"] != true || paused["next_run_at"] != nil || paused["pause_reason"] == "" {
		t.Fatalf("%d %v", status, paused)
	}
	status, resumed := send(t, server, "PUT", base+"/paused", "", `{"paused":false}`)
	if status != 200 || resumed["paused"] != false || resumed["next_run_at"] == nil {
		t.Fatalf("%d %v", status, resumed)
	}
	if status, _ = send(t, server, "DELETE", base, "", ""); status != 200 {
		t.Fatalf("delete: %d", status)
	}
	for _, call := range [][2]string{{"GET", base}, {"PUT", base}, {"DELETE", base}, {"PUT", base + "/paused"}} {
		body := ""
		if call[0] == "PUT" {
			body = automationJSON(repo, nil)
			if strings.HasSuffix(call[1], "paused") {
				body = `{"paused":true}`
			}
		}
		status, out := send(t, server, call[0], call[1], "", body)
		if status != 404 || errorCode(out) != "not_found" || !strings.Contains(out["error"].(map[string]any)["message"].(string), "自动化") {
			t.Fatalf("%s %s after delete: %d %v", call[0], call[1], status, out)
		}
	}
	if status, _ = send(t, server, "POST", base+"/runs", "k", ""); status != 404 {
		t.Fatalf("running a deleted automation: %d", status)
	}
	_, list := send(t, server, "GET", "/api/v1/automations", "", "")
	if items, _ := list["automations"].([]any); len(items) != 0 {
		t.Fatalf("deleted automation still listed: %v", list)
	}
}

func TestManualRunCreatesATaggedTaskAndEnforcesItsRules(t *testing.T) {
	server, db, repo := testServer(t)
	_, created := send(t, server, "POST", "/api/v1/automations", "c", automationJSON(repo, map[string]any{"schedule_time": "", "daily_limit": 2}))
	id := created["id"].(string)
	if created["next_run_at"] != nil {
		t.Fatal("a manual-only automation has no next run")
	}
	runs := "/api/v1/automations/" + id + "/runs"

	if status, body := send(t, server, "POST", runs, "", ""); status != 400 || errorCode(body) != "idempotency_key_required" {
		t.Fatalf("%d %v", status, body)
	}
	status, run := send(t, server, "POST", runs, "click-1", "")
	if status != 201 || run["status"] != "active" || run["trigger"] != "manual" || run["task_id"] == "" || run["automation_name"] != "每日检查" {
		t.Fatalf("%d %v", status, run)
	}
	// The same click twice is one run.
	if status, again := send(t, server, "POST", runs, "click-1", ""); status != 200 || again["id"] != run["id"] {
		t.Fatalf("replay: %d %v", status, again)
	}
	// A different click while the first is still running is refused.
	if status, body := send(t, server, "POST", runs, "click-2", ""); status != 409 || errorCode(body) != "automation_running" {
		t.Fatalf("%d %v", status, body)
	}
	// The task is tagged, so the personal task list can tell it apart.
	tasks, err := db.ListTasks(t.Context())
	if err != nil || len(tasks) != 1 || tasks[0].AutomationID != id {
		t.Fatalf("%+v %v", tasks, err)
	}
	_, detail := send(t, server, "GET", "/api/v1/tasks/"+run["task_id"].(string), "", "")
	if task, _ := detail["task"].(map[string]any); task["automation_id"] != id {
		t.Fatalf("task detail does not carry the automation: %v", detail["task"])
	}

	// Inbox shows the running run; nothing is unread yet.
	_, inbox := send(t, server, "GET", "/api/v1/inbox", "", "")
	items, _ := inbox["runs"].([]any)
	if len(items) != 1 || inbox["unread"] != float64(0) {
		t.Fatalf("%v", inbox)
	}
}

func TestDailyLimitSurfacesAsAClearError(t *testing.T) {
	server, db, repo := testServer(t)
	_, created := send(t, server, "POST", "/api/v1/automations", "c", automationJSON(repo, map[string]any{"schedule_time": "", "daily_limit": 1}))
	runs := "/api/v1/automations/" + created["id"].(string) + "/runs"
	_, first := send(t, server, "POST", runs, "one", "")
	// Finish the first run so only the budget, not "already running", blocks the next.
	job, err := db.ClaimNext(t.Context(), "w")
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	if _, _, err = db.AppendEvent(t.Context(), core.EventInput{TaskID: job.TaskID, SessionID: job.SessionID, TurnID: job.TurnID, Generation: job.Generation, Type: "turn.failed", Payload: json.RawMessage(`{"code":"user_stopped","message":"stopped"}`)}); err != nil {
		t.Fatal(err)
	}
	finish(t, db)
	_ = first
	status, body := send(t, server, "POST", runs, "two", "")
	if status != 429 || errorCode(body) != "automation_daily_limit" || !strings.Contains(body["error"].(map[string]any)["message"].(string), "每日上限") {
		t.Fatalf("%d %v", status, body)
	}
}

func TestInboxSeenAndDismiss(t *testing.T) {
	server, db, repo := testServer(t)
	_, created := send(t, server, "POST", "/api/v1/automations", "c", automationJSON(repo, map[string]any{"schedule_time": ""}))
	_, run := send(t, server, "POST", "/api/v1/automations/"+created["id"].(string)+"/runs", "one", "")
	id := run["id"].(string)
	job, _ := db.ClaimNext(t.Context(), "w")
	if _, _, err := db.AppendEvent(t.Context(), core.EventInput{TaskID: job.TaskID, SessionID: job.SessionID, TurnID: job.TurnID, Generation: job.Generation, Type: "turn.failed", Payload: json.RawMessage(`{"code":"codex_auth_required","message":"Codex 凭据不可用"}`)}); err != nil {
		t.Fatal(err)
	}
	finish(t, db)
	_, unread := send(t, server, "GET", "/api/v1/inbox/unread", "", "")
	if unread["unread"] != float64(1) {
		t.Fatalf("%v", unread)
	}
	_, inbox := send(t, server, "GET", "/api/v1/inbox", "", "")
	item := inbox["runs"].([]any)[0].(map[string]any)
	if item["status"] != "failed" || item["code"] != "codex_auth_required" || item["message"] != "Codex 凭据不可用" {
		t.Fatalf("a login failure must keep its code and message: %v", item)
	}
	if status, _ := send(t, server, "PUT", "/api/v1/inbox/"+id+"/seen", "", ""); status != 200 {
		t.Fatalf("seen: %d", status)
	}
	if _, unread = send(t, server, "GET", "/api/v1/inbox/unread", "", ""); unread["unread"] != float64(0) {
		t.Fatalf("%v", unread)
	}
	if status, _ := send(t, server, "POST", "/api/v1/inbox/"+id+"/dismiss", "", ""); status != 200 {
		t.Fatalf("dismiss: %d", status)
	}
	if _, inbox = send(t, server, "GET", "/api/v1/inbox", "", ""); len(inbox["runs"].([]any)) != 0 {
		t.Fatalf("%v", inbox)
	}
	for _, path := range []string{"/api/v1/inbox/run_missing/seen", "/api/v1/inbox/run_missing/dismiss"} {
		method := "PUT"
		if strings.HasSuffix(path, "dismiss") {
			method = "POST"
		}
		if status, body := send(t, server, method, path, "", ""); status != 404 || errorCode(body) != "not_found" {
			t.Fatalf("%s: %d %v", path, status, body)
		}
	}
}
