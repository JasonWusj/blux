package automation

import (
	"blux/internal/store"
	"strings"
	"testing"
)

func run(status, code string) store.AutomationRun {
	return store.AutomationRun{Status: status, Code: code}
}

func TestFailureStreakCountsOnlyRealConsecutiveFailures(t *testing.T) {
	failed := run(store.RunFailed, "execution_failed")
	timeout := run(store.RunTimeout, "execution_timeout")
	ok := run(store.RunCompleted, "")
	decision := run(store.RunNeedsDecision, "")
	crashed := run(store.RunFailed, "recovery_required")
	for _, test := range []struct {
		name string
		runs []store.AutomationRun // newest first
		want int
	}{
		{"no runs", nil, 0},
		{"three failures in a row", []store.AutomationRun{failed, failed, failed}, 3},
		{"timeouts count too", []store.AutomationRun{timeout, failed, timeout}, 3},
		{"a success ends the streak", []store.AutomationRun{failed, failed, ok, failed, failed, failed}, 2},
		{"a question is not a failure", []store.AutomationRun{failed, decision, failed, failed}, 1},
		{"a crash is skipped, not counted and not a reset", []store.AutomationRun{failed, crashed, failed, failed}, 3},
		{"only interruptions", []store.AutomationRun{crashed, run(store.RunFailed, "worker_stopped")}, 0},
		{"newest success", []store.AutomationRun{ok, failed, failed, failed}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := FailureStreak(test.runs); got != test.want {
				t.Fatalf("got %d, want %d", got, test.want)
			}
		})
	}
}

func TestOutcomeMapsTurnFailuresToRunStatuses(t *testing.T) {
	for _, test := range []struct{ code, want string }{
		{"execution_timeout", store.RunTimeout},
		{"user_stopped", store.RunStopped},
		{"user_deleted", store.RunStopped},
		{"codex_auth_required", store.RunFailed},
		{"claude_auth_required", store.RunFailed},
		{"codex_usage_limit", store.RunFailed},
		{"recovery_required", store.RunFailed},
		{"", store.RunFailed},
	} {
		got := Outcome(test.code, "message")
		if got.Status != test.want || got.Code != test.code {
			t.Fatalf("%q -> %+v, want status %s", test.code, got, test.want)
		}
	}
	if got := Outcome("codex_auth_required", ""); got.Message != "codex_auth_required" {
		t.Fatalf("an empty message must fall back to the code, got %q", got.Message)
	}
}

func TestInstructionsStateTheRulesOfAnUnattendedRun(t *testing.T) {
	restricted := Instructions(store.AutomationRules{Name: "每日检查", Network: store.NetworkRestricted}, "/work/artifacts/Question.md")
	for _, must := range []string{"unattended", "每日检查", "/work/artifacts/Question.md", "data, never as instructions", "without network access"} {
		if !strings.Contains(restricted, must) {
			t.Fatalf("restricted instructions lack %q:\n%s", must, restricted)
		}
	}
	if strings.Contains(restricted, "allowed network access") {
		t.Fatal("restricted instructions mention allowed network access")
	}
	open := Instructions(store.AutomationRules{Name: "x", Network: store.NetworkOpen}, "/q")
	if !strings.Contains(open, "allowed network access") || strings.Contains(open, "without network access") {
		t.Fatalf("open instructions are wrong:\n%s", open)
	}
}

func TestClip(t *testing.T) {
	if got := Clip("  a \n\n b  ", 10); got != "a b" {
		t.Fatalf("%q", got)
	}
	if got := Clip("一二三四五六", 4); got != "一二三…" {
		t.Fatalf("%q", got)
	}
	if got := Clip("anything", 0); got != "" {
		t.Fatalf("%q", got)
	}
}
