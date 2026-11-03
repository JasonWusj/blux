package httpapi

import (
	"blux/internal/artifact"
	"blux/internal/core"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImmutablePreviewAndDownload(t *testing.T) {
	_, db, _ := testServer(t)
	ctx := context.Background()
	d, _, e := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "preview", IdempotencyKey: "preview"})
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.ClaimNext(ctx, "test")
	if e != nil {
		t.Fatal(e)
	}
	data := t.TempDir()
	root := filepath.Join(data, "workspace")
	os.MkdirAll(filepath.Join(root, "artifacts"), 0700)
	source := filepath.Join(root, "artifacts", "report.html")
	os.WriteFile(source, []byte("<h1>Original report</h1>"), 0600)
	if e = (artifact.Service{Store: db, DataDir: data}).Capture(ctx, *j, core.RuntimeInfo{WorkspacePath: root}); e != nil {
		t.Fatal(e)
	}
	v, e := db.Snapshots(ctx, d.Task.ID)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(source, []byte("changed"), 0600)
	api := &Server{Store: db, DataDir: data}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	response, e := server.Client().Get(server.URL + "/api/v1/previews/" + v[0].ID + "/report.html")
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Original report") || !strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(response.StatusCode, string(body), response.Header)
	}
	response, e = server.Client().Get(server.URL + "/api/v1/artifacts/" + v[0].Artifacts[0].ID + "/content")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") {
		t.Fatal(response.Header)
	}
	if _, _, e = db.AppendEvent(ctx, core.EventInput{TaskID: j.TaskID, SessionID: j.SessionID, TurnID: j.TurnID, Generation: j.Generation, Type: "turn.completed", Payload: []byte(`{}`)}); e != nil {
		t.Fatal(e)
	}
	consumer, _, e := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/consumer", BaseCommit: "head", Prompt: "consume exact input", IdempotencyKey: "consumer", InputArtifacts: []string{v[0].Artifacts[0].ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.DeleteTask(ctx, d.Task.ID); e != nil {
		t.Fatal(e)
	}
	response, e = server.Client().Get(server.URL + "/api/v1/artifacts/" + v[0].Artifacts[0].ID + "/content")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("deleted task artifact exposed", response.StatusCode)
	}
	response, e = server.Client().Get(server.URL + "/api/v1/tasks/" + consumer.Task.ID + "/inputs/" + v[0].Artifacts[0].ID + "/content")
	if e != nil {
		t.Fatal(e)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Original report") {
		t.Fatal("pinned input lost", response.StatusCode, string(body))
	}
}
