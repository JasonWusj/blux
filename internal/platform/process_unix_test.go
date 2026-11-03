//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCleanupIncludesDetachedChild(t *testing.T) {
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("python3 unavailable")
	}
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	script := `import subprocess,sys,time,pathlib
p=subprocess.Popen([sys.executable,'-c','import os,signal,time;os.setsid();signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(60)'])
pathlib.Path(sys.argv[1]).write_text(str(p.pid))
time.sleep(60)`
	cmd := exec.Command(python, "-c", script, pidfile)
	ConfigureProcess(cmd)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = TerminateTree(cmd, true) }()
	var child int
	for i := 0; i < 50; i++ {
		b, e := os.ReadFile(pidfile)
		if e == nil {
			child, _ = strconv.Atoi(string(b))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child < 1 {
		t.Fatal("no child")
	}
	defer syscall.Kill(child, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	force := BeginTermination(cmd)
	_ = cmd.Wait()
	force()
	for i := 0; i < 50; i++ {
		out, e := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(child)).Output()
		if e != nil || strings.Contains(string(out), "Z") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("detached child survived cleanup")
}
