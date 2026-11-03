package httpapi

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestPinAPIContract(t *testing.T) {
	s, db, _ := testServer(t)
	ctx := context.Background()
	d, _, err := db.CreateTask(ctx, core.CreateTaskInput{Prompt: "pin", RepositoryPath: "/repo", BaseCommit: "head", IdempotencyKey: "pin-http"})
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := db.CreateCollaboration(ctx, store.CreateCollaborationInput{Goal: "pin"}, "pin-group-http")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/tasks/" + d.Task.ID + "/pin", "/collaborations/" + g.ID + "/pin"} {
		for _, test := range []struct {
			body string
			code int
		}{{`{}`, 400}, {`{"pinned":null}`, 400}, {`{"pinned":"yes"}`, 400}, {`{"pinned":true}`, 200}, {`{"pinned":true}`, 200}, {`{"pinned":false}`, 200}} {
			req, _ := http.NewRequest("PUT", s.URL+"/api/v1"+path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			res, e := s.Client().Do(req)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			if res.StatusCode != test.code {
				t.Fatal(path, test.body, res.StatusCode)
			}
		}
	}
}
