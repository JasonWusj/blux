package httpapi

import (
	"blux/internal/core"
	"blux/internal/store"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProjectsAcceptDirectoriesAndReportBranches(t *testing.T) {
	for _, kind := range []string{"plain", "unborn", "dirty"} {
		t.Run(kind, func(t *testing.T) {
			server, db, _ := testServer(t)
			dir := t.TempDir()
			g := func(args ...string) string {
				t.Helper()
				b, e := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
				if e != nil {
					t.Fatal(args, e)
				}
				return string(b)
			}
			if kind != "plain" {
				g("init", "-b", "test-project")
			}
			if kind == "dirty" {
				g("-c", "user.name=Blux Test", "-c", "user.email=test@local", "commit", "--allow-empty", "-m", "base")
			}
			os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("source"), 0600)
			response, e := server.Client().Get(server.URL + "/api/v1/projects/info?path=" + dir)
			if e != nil {
				t.Fatal(e)
			}
			var info struct {
				Branch     string
				HasCommits bool `json:"has_commits"`
			}
			json.NewDecoder(response.Body).Decode(&info)
			response.Body.Close()
			if response.StatusCode != 200 || kind == "plain" && info.Branch != "" || kind != "plain" && info.Branch != "test-project" || info.HasCommits != (kind == "dirty") {
				t.Fatal(response.StatusCode, info)
			}
			payload, _ := json.Marshal(map[string]string{"prompt": "edit directory", "repository_path": dir})
			create := post(t, server, "/api/v1/tasks", "directory", string(payload))
			if create.StatusCode != 201 {
				b, _ := io.ReadAll(create.Body)
				t.Fatal(create.StatusCode, string(b))
			}
			var task core.TaskDetail
			json.NewDecoder(create.Body).Decode(&task)
			if task.Task.BaseCommit == "" {
				t.Fatal("baseline missing")
			}
			os.WriteFile(filepath.Join(dir, "after.txt"), []byte("later"), 0600)
			repeat := post(t, server, "/api/v1/tasks", "directory", string(payload))
			if repeat.StatusCode != 200 {
				t.Fatal(repeat.StatusCode)
			}
			var again core.TaskDetail
			json.NewDecoder(repeat.Body).Decode(&again)
			if again.Task.ID != task.Task.ID || again.Task.BaseCommit != task.Task.BaseCommit {
				t.Fatal("retry changed baseline")
			}
			body, _ := json.Marshal(map[string]string{"title": "directory group", "goal": "work", "repository_path": dir})
			groupResp := post(t, server, "/api/v1/collaborations", "directory-group", string(body))
			if groupResp.StatusCode != 201 {
				t.Fatal(groupResp.StatusCode)
			}
			var group store.Collaboration
			json.NewDecoder(groupResp.Body).Decode(&group)
			assigned := post(t, server, "/api/v1/collaborations/"+group.ID+"/tasks", "assigned", `{"prompt":"work"}`)
			if assigned.StatusCode != 201 {
				b, _ := io.ReadAll(assigned.Body)
				t.Fatal(assigned.StatusCode, string(b))
			}
			tasks, e := db.ListTasks(context.Background())
			if e != nil || len(tasks) != 2 {
				t.Fatal(tasks, e)
			}
			if kind == "plain" {
				if _, e = os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(e) {
					t.Fatal("source Git was initialized")
				}
			}
		})
	}
}

func TestProjectInfoRejectsFilesAndMissingPaths(t *testing.T) {
	server, _, _ := testServer(t)
	file := filepath.Join(t.TempDir(), "file.txt")
	os.WriteFile(file, []byte("x"), 0600)
	for _, path := range []string{file, file + "-missing", ""} {
		r, e := http.Get(server.URL + "/api/v1/projects/info?path=" + path)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal(path, r.StatusCode)
		}
	}
}
