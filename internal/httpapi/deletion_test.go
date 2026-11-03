package httpapi

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"net/http"
	"testing"
)

func TestDeleteHTTPAndHiddenTaskEndpoints(t *testing.T) {
	server, db, _ := testServer(t)
	ctx := context.Background()
	d, _, err := db.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/repo", BaseCommit: "head", Prompt: "queued", IdempotencyKey: "delete-http"})
	if err != nil {
		t.Fatal(err)
	}
	check := func(method, path string, code int) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+"/api/v1"+path, nil)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != code {
			t.Fatalf("%s %s: %d", method, path, res.StatusCode)
		}
	}
	check("DELETE", "/tasks/"+d.Task.ID, 200)
	check("DELETE", "/tasks/"+d.Task.ID, 200)
	for _, tail := range []string{"", "/snapshots", "/events", "/files", "/file?path=README.md", "/context"} {
		check("GET", "/tasks/"+d.Task.ID+tail, 404)
	}
	check("DELETE", "/tasks/missing", 404)
	g, _, err := db.CreateCollaboration(ctx, store.CreateCollaborationInput{Goal: "group", RepositoryPath: "/repo"}, "group-delete-http")
	if err != nil {
		t.Fatal(err)
	}
	check("DELETE", "/collaborations/"+g.ID, 200)
	check("DELETE", "/collaborations/"+g.ID, 200)
	check("GET", "/collaborations/"+g.ID, 404)
	check("GET", "/collaborations/"+g.ID+"/artifacts", 404)
}
