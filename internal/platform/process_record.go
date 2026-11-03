package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type processRecord struct {
	PID   int    `json:"pid"`
	Stamp string `json:"stamp"`
}

func processStamp(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf("(Get-Process -Id %d -ErrorAction Stop).StartTime.ToUniversalTime().Ticks", pid))
	} else {
		cmd = exec.CommandContext(ctx, "ps", "-o", "lstart=", "-o", "command=", "-p", strconv.Itoa(pid))
	}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
func RecordProcess(agentPath string, pid int) (func(), error) {
	stamp, err := processStamp(pid)
	if err != nil || stamp == "" {
		return nil, fmt.Errorf("cannot identify Codex process: %w", err)
	}
	path := filepath.Join(agentPath, "app-server-process.json")
	b, _ := json.Marshal(processRecord{pid, stamp})
	if err = os.WriteFile(path, b, 0600); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

// ReapRecorded only terminates process groups whose PID and start identity still
// match this data directory's owned app-server. Reused PIDs are never signaled.
func ReapRecorded(dataDir string) error {
	paths, err := filepath.Glob(filepath.Join(dataDir, "workspaces", "*", ".agent", "app-server-process.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var r processRecord
		if json.Unmarshal(b, &r) != nil || r.PID <= 1 || r.Stamp == "" {
			return fmt.Errorf("invalid process record: %s", path)
		}
		stamp, err := processStamp(r.PID)
		if err == nil && stamp == r.Stamp {
			p, e := os.FindProcess(r.PID)
			if e != nil {
				return e
			}
			cmd := &exec.Cmd{Process: p}
			force := BeginTermination(cmd)
			time.Sleep(100 * time.Millisecond)
			force()

		}
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func RecordedAlive(directory string) bool {
	b, e := os.ReadFile(filepath.Join(directory, "app-server-process.json"))
	if e != nil {
		return false
	}
	var r processRecord
	if json.Unmarshal(b, &r) != nil || r.PID <= 1 {
		return false
	}
	stamp, e := processStamp(r.PID)
	return e == nil && stamp == r.Stamp
}
func StopRecorded(directory string) error {
	path := filepath.Join(directory, "app-server-process.json")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var r processRecord
	if e = json.Unmarshal(b, &r); e != nil || r.PID <= 1 {
		return fmt.Errorf("invalid process record")
	}
	if stamp, e := processStamp(r.PID); e != nil || stamp != r.Stamp {
		_ = os.Remove(path)
		return nil
	}
	p, e := os.FindProcess(r.PID)
	if e != nil {
		return e
	}
	cmd := &exec.Cmd{Process: p}
	_ = TerminateTree(cmd, false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !RecordedAlive(directory) {
			_ = os.Remove(path)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = TerminateTree(cmd, true)
	_ = os.Remove(path)
	return nil
}
