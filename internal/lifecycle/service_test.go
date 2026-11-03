package lifecycle

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func fakeEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestPlanBuildsOneJobPerProcessWithSafeEnvironment(t *testing.T) {
	plan, err := buildPlan("/opt/blux/bin/blux", "/Users/me/.blux", "/opt/blux/web/dist", "127.0.0.1:8787", "/Users/me", fakeEnv(map[string]string{
		"PATH": "/opt/homebrew/bin:/usr/bin", "BLUX_CODEX_BIN": "/opt/homebrew/bin/codex", "CODEX_HOME": "/Users/me/.codex",
		// Secrets and unrelated variables must never be copied into the service.
		"ANTHROPIC_API_KEY": "sk-secret", "OPENAI_API_KEY": "sk-secret-2", "HOME": "/Users/me", "GITHUB_TOKEN": "ghp_x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Jobs) != 2 || plan.Jobs[0].Label != "local.blux.serve" || plan.Jobs[1].Label != "local.blux.worker" {
		t.Fatalf("%+v", plan.Jobs)
	}
	serve, worker := plan.Jobs[0], plan.Jobs[1]
	want := []string{"/opt/blux/bin/blux", "serve", "--data-dir", "/Users/me/.blux", "--addr", "127.0.0.1:8787", "--web-dir", "/opt/blux/web/dist"}
	if strings.Join(serve.Arguments, "|") != strings.Join(want, "|") {
		t.Fatalf("serve arguments: %v", serve.Arguments)
	}
	if strings.Join(worker.Arguments, "|") != "/opt/blux/bin/blux|worker|--data-dir|/Users/me/.blux" {
		t.Fatalf("worker arguments: %v", worker.Arguments)
	}
	for key, value := range map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin", "BLUX_CODEX_BIN": "/opt/homebrew/bin/codex", "CODEX_HOME": "/Users/me/.codex", "BLUX_DATA_DIR": "/Users/me/.blux"} {
		if serve.Env[key] != value || worker.Env[key] != value {
			t.Fatalf("%s = %q / %q", key, serve.Env[key], worker.Env[key])
		}
	}
	for _, secret := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GITHUB_TOKEN", "HOME"} {
		if _, leaked := worker.Env[secret]; leaked {
			t.Fatalf("%s was copied into the service environment", secret)
		}
	}
	if plan.Paths[0] != filepath.Join("/Users/me", "Library", "LaunchAgents", "local.blux.serve.plist") {
		t.Fatalf("%v", plan.Paths)
	}
	if serve.LogPath != filepath.Join("/Users/me/.blux", "logs", "serve.log") {
		t.Fatalf("%q", serve.LogPath)
	}
}

func TestPlistRestartsOnlyAfterACrashAndEscapesEverything(t *testing.T) {
	job := serviceJob{
		Label:     "local.blux.worker",
		Arguments: []string{"/Applications/R&D <tools>/blux", "worker", "--data-dir", `/Users/o"brien/.blux`},
		Env:       map[string]string{"PATH": "/a:/b&c", "BLUX_DATA_DIR": "/d"},
		LogPath:   "/logs/worker.log", WorkDir: "/d",
	}
	text, err := job.plist()
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"<key>Label</key>\n\t<string>local.blux.worker</string>",
		"<string>/Applications/R&amp;D &lt;tools&gt;/blux</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		// A clean exit (launchctl bootout, SIGTERM) must not respawn the job.
		"<key>SuccessfulExit</key>\n\t\t<false/>",
		"<string>/a:/b&amp;c</string>",
		"<key>StandardErrorPath</key>",
	} {
		if !strings.Contains(text, must) {
			t.Fatalf("missing %q in:\n%s", must, text)
		}
	}
	if strings.Contains(text, "R&D <tools>") || strings.Contains(text, "b&c<") {
		t.Fatal("a value was written without escaping")
	}
	// Environment keys are sorted so the file is stable between installs.
	if strings.Index(text, "BLUX_DATA_DIR") > strings.Index(text, "<key>PATH</key>") {
		t.Fatal("environment is not sorted")
	}
}

func TestPrintOutputParsing(t *testing.T) {
	out := "local.blux.worker = {\n\tactive count = 1\n\tstate = running\n\tprogram = /opt/blux\n\tpid = 4242\n\tlast exit code = (never exited)\n}"
	state, pid := parsePrint(out)
	if state != "running" || pid != "4242" {
		t.Fatal(state, pid)
	}
	if state, pid = parsePrint("nothing useful"); state != "unknown" || pid != "-" {
		t.Fatal(state, pid)
	}
}

func TestNotLoadedRecognisesTheNormalUninstallCase(t *testing.T) {
	for _, output := range []string{"Boot-out failed: 3: No such process", "Could not find service \"local.blux.serve\" in domain", "service not found"} {
		if !notLoaded(output) {
			t.Fatalf("%q should count as already unloaded", output)
		}
	}
	if notLoaded("Operation not permitted") {
		t.Fatal("a real failure was treated as already unloaded")
	}
}

func TestServiceCommandRejectsBadInputBeforeTouchingLaunchd(t *testing.T) {
	previous := launchctl
	t.Cleanup(func() { launchctl = previous })
	launchctl = func(...string) (string, error) {
		t.Fatal("launchctl must not run for invalid input or on this platform")
		return "", errors.New("unreachable")
	}
	for _, args := range [][]string{{}, {"bogus"}, {"install", "extra"}, {"install", "--nope"}} {
		if err := RunService(args, t.TempDir()); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	// Valid commands are refused off macOS without running anything.
	if err := RunService([]string{"status"}, t.TempDir()); err == nil {
		t.Skip("running on macOS: the platform guard is not exercised here")
	}
}
