package httpapi

import (
	"blux/internal/artifact"
	"blux/internal/core"
	"blux/internal/workspace"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceFilesVersionsAndChanges(t *testing.T) {
	server, db, repo := testServer(t)
	ctx := context.Background()
	git := func(root string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@local", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(root, name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "src/中文 文件.txt", "original\n")
	write(repo, "remove.txt", "delete me\n")
	write(repo, "rename.txt", strings.Repeat("rename content\n", 10))
	git(repo, "add", ".")
	git(repo, "commit", "-m", "baseline")
	base := git(repo, "rev-parse", "HEAD")
	d, _, err := db.CreateTask(ctx, core.CreateTaskInput{Prompt: "files", Adapter: "codex", RepositoryPath: filepath.Join(repo, "src"), BaseCommit: base, IdempotencyKey: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	j, err := db.ClaimNext(ctx, "workspace-test")
	if err != nil || j == nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	rt, err := (workspace.Manager{DataDir: data}).Prepare(ctx, *j)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(rt)
	if _, _, err = db.AppendEvent(ctx, core.EventInput{ID: core.NewID("event"), TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "workspace.prepared", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	root := rt.WorktreePath
	get := func(endpoint string, query url.Values, status int) []byte {
		t.Helper()
		response, err := server.Client().Get(server.URL + "/api/v1/tasks/" + d.Task.ID + "/" + endpoint + "?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s %v: status %d %s %v", endpoint, query, response.StatusCode, b, err)
		}
		return b
	}
	file := func(name, snapshot string, diff bool) map[string]any {
		t.Helper()
		q := url.Values{"path": {name}}
		if snapshot != "" {
			q.Set("snapshot", snapshot)
		}
		if diff {
			q.Set("diff", "1")
		}
		var v map[string]any
		if err := json.Unmarshal(get("file", q, 200), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	list := func(q url.Values) []workspaceEntry {
		t.Helper()
		var v struct {
			Entries []workspaceEntry `json:"entries"`
		}
		if err := json.Unmarshal(get("files", q, 200), &v); err != nil {
			t.Fatal(err)
		}
		return v.Entries
	}
	entries := list(nil)
	if len(entries) != 3 || entries[0].Path != "src" || entries[0].Kind != "directory" {
		t.Fatalf("root entries: %+v", entries)
	}
	if v := file("src/中文 文件.txt", "", false); v["content"] != "original\n" {
		t.Fatal(v)
	}
	if v := file("src/中文 文件.txt", "", true); v["content"] != "" {
		t.Fatalf("unchanged file became an addition: %v", v)
	}
	write(root, "src/中文 文件.txt", "snapshot\n")
	write(root, "new/fresh.txt", "untracked\n")
	write(root, "site/index.html", `<link rel="stylesheet" href="style.css"><h1>Hello</h1>`)
	write(root, "site/style.css", "h1 { color: blue; }")
	if err := os.Remove(filepath.Join(root, "remove.txt")); err != nil {
		t.Fatal(err)
	}
	git(root, "mv", "rename.txt", "renamed.txt")
	entries = list(url.Values{"scope": {"changes"}})
	changes := map[string]string{}
	for _, e := range entries {
		changes[e.Path] = e.Change
	}
	if changes["new/fresh.txt"] != "added" || changes["remove.txt"] != "deleted" || changes["renamed.txt"] != "renamed" || changes["src/中文 文件.txt"] != "modified" {
		t.Fatal(changes)
	}
	if v := file("new/fresh.txt", "", true); !strings.Contains(v["content"].(string), "+untracked") {
		t.Fatal(v)
	}
	if err := (artifact.Service{Store: db, DataDir: data}).Capture(ctx, *j, rt); err != nil {
		t.Fatal(err)
	}
	versions, err := db.Snapshots(ctx, d.Task.ID)
	if err != nil || len(versions) != 1 {
		t.Fatal(versions, err)
	}
	snapshot := versions[0].ID
	write(root, "site/style.css", "h1 { color: red; }")
	for _, version := range []string{"live", snapshot} {
		response, err := server.Client().Get(server.URL + "/api/v1/tasks/" + d.Task.ID + "/previews/" + version + "/site/style.css")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(response.Body)
		response.Body.Close()
		color := "blue"
		if version == "live" {
			color = "red"
		}
		if response.StatusCode != 200 || !strings.Contains(string(b), color) || !strings.Contains(response.Header.Get("Content-Type"), "text/css") || !strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") {
			t.Fatal(response.StatusCode, string(b), response.Header)
		}
	}
	write(root, "src/中文 文件.txt", "later live edits\n")
	if v := file("src/中文 文件.txt", snapshot, false); v["content"] != "snapshot\n" {
		t.Fatal(v)
	}
	if v := file("src/中文 文件.txt", "", false); v["content"] != "later live edits\n" {
		t.Fatal(v)
	}
	if v := file("src/中文 文件.txt", snapshot, true); !strings.Contains(v["content"].(string), "+snapshot") || strings.Contains(v["content"].(string), "later live") {
		t.Fatal(v)
	}
	entries = list(url.Values{"snapshot": {snapshot}, "path": {"new"}})
	if len(entries) != 1 || entries[0].Path != "new/fresh.txt" {
		t.Fatal(entries)
	}
	entries = list(url.Values{"snapshot": {snapshot}, "scope": {"changes"}})
	for _, e := range entries {
		if e.Path == "renamed.txt" && e.Previous != "rename.txt" {
			t.Fatal(e)
		}
	}
	q := url.Values{"snapshot": {snapshot}, "path": {"renamed.txt"}, "diff": {"1"}, "previous_path": {"rename.txt"}}
	if b := get("file", q, 200); !strings.Contains(string(b), "rename from") {
		t.Fatal(string(b))
	}
	if source, _ := os.ReadFile(filepath.Join(repo, "src/中文 文件.txt")); string(source) != "original\n" || git(repo, "rev-parse", "HEAD") != base {
		t.Fatal("viewing files changed source repository")
	}

	write(root, "binary.bin", "\x00\xff\x01")
	if v := file("binary.bin", "", false); v["binary"] != true || v["content"] != "" {
		t.Fatal(v)
	}
	write(root, "large.txt", strings.Repeat("中", (1<<20)/3+10))
	if v := file("large.txt", "", false); v["truncated"] != true || v["binary"] != false {
		t.Fatalf("large UTF-8 file: truncated=%v binary=%v", v["truncated"], v["binary"])
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	write(filepath.Dir(outside), "private.txt", "outside")
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../private.txt", "/etc/passwd", ".git", ".git/config", "a/../../private.txt"} {
		get("file", url.Values{"path": {name}}, 400)
	}
	get("file", url.Values{"path": {"outside-link"}}, 404)
	get("files", url.Values{"path": {"outside-link"}}, 404)
	get("files", url.Values{"path": {"binary.bin"}}, 404)
	other, _, err := db.CreateTask(ctx, core.CreateTaskInput{Prompt: "other", RepositoryPath: repo, BaseCommit: base, IdempotencyKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Get(server.URL + "/api/v1/tasks/" + other.Task.ID + "/files?snapshot=" + snapshot)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("cross-task snapshot status %d", response.StatusCode)
	}
}
