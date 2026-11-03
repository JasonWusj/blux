package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"blux/internal/core"
)

func TestPragmasSurviveConnectionReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a ? # database.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	// Force database/sql to close idle connections; every following query must
	// establish a new one rather than inherit Open's initialization connection.
	s.db.SetMaxIdleConns(0)
	for _, test := range []struct {
		pragma string
		want   int
	}{{"foreign_keys", 1}, {"busy_timeout", 5000}} {
		var got int
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+test.pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("%s=%d, want %d", test.pragma, got, test.want)
		}
	}
	if _, _, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Title: "special path", Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tasks, err := other.ListTasks(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("special-character path persistence: %v %v", tasks, err)
	}
}

func TestConcurrentOpenFreshDatabase(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fresh.db")
			const count = 8
			start := make(chan struct{})
			failures := make(chan error, count)
			var wg sync.WaitGroup
			for i := 0; i < count; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					s, err := Open(path)
					if err != nil {
						failures <- fmt.Errorf("open %d: %w", i, err)
						return
					}
					defer s.Close()
					ctx := context.Background()
					detail, _, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: fmt.Sprintf("task %d", i)})
					if err != nil {
						failures <- fmt.Errorf("create %d: %w", i, err)
						return
					}
					persisted, err := s.GetTask(ctx, detail.Task.ID)
					if err != nil || persisted.Task.ID != detail.Task.ID || persisted.Task.LastEventSeq != 2 {
						failures <- fmt.Errorf("read %d: detail=%+v err=%v", i, persisted, err)
					}
				}(i)
			}
			close(start)
			wg.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tasks, err := s.ListTasks(context.Background())
			if err != nil || len(tasks) != count {
				t.Fatalf("final task count=%d, want %d; err=%v", len(tasks), count, err)
			}
		})
	}
}

func TestConcurrentOpenProcessesFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh-processes.db")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	failures := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			command := exec.Command(testBinary, "-test.run=^TestOpenProcessHelper$")
			command.Env = append(os.Environ(), "BLUX_STORE_TEST_DATABASE="+path)
			if output, err := command.CombinedOutput(); err != nil {
				failures <- fmt.Errorf("subprocess: %w: %s", err, output)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tasks, err := s.ListTasks(context.Background())
	if err != nil || len(tasks) != 4 {
		t.Fatalf("subprocess task count=%d, want 4; err=%v", len(tasks), err)
	}
}

func TestOpenProcessHelper(t *testing.T) {
	path := os.Getenv("BLUX_STORE_TEST_DATABASE")
	if path == "" {
		t.Skip("subprocess helper")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	detail, _, err := s.CreateTask(ctx, core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: fmt.Sprintf("process %d", os.Getpid())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTask(ctx, detail.Task.ID); err != nil {
		t.Fatal(err)
	}
}

type initializationTestError int

func (e initializationTestError) Error() string { return fmt.Sprintf("SQLite test code %d", int(e)) }
func (e initializationTestError) Code() int     { return int(e) }

func TestInitializationRetriesOnlyContentionAndHonorsDeadline(t *testing.T) {
	attempts := 0
	err := retrySQLiteContention(context.Background(), func() error {
		attempts++
		if attempts == 1 {
			return initializationTestError(5 + 256)
		}
		if attempts == 2 {
			return initializationTestError(6)
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("contention retries: attempts=%d err=%v", attempts, err)
	}
	attempts = 0
	fatal := initializationTestError(11) // SQLITE_CORRUPT must not be retried.
	err = retrySQLiteContention(context.Background(), func() error { attempts++; return fatal })
	if err != fatal || attempts != 1 {
		t.Fatalf("non-contention retries: attempts=%d err=%v", attempts, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = retrySQLiteContention(ctx, func() error { return initializationTestError(5) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry did not stop on deadline: %v", err)
	}
}

func TestInMemoryDatabase(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.CreateTask(context.Background(), core.CreateTaskInput{RepositoryPath: "/source/repo", BaseCommit: "frozen-head", Prompt: "memory"}); err != nil {
		t.Fatal(err)
	}
}
