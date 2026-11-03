//go:build !windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func ConfigureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func TerminateTree(cmd *exec.Cmd, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	return syscall.Kill(-cmd.Process.Pid, signal)
}
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func AttachProcessTree(cmd *exec.Cmd) (func(), error) { return func() {}, nil }

// Capture descendants before termination, including children in their own PTY
// process groups. Validate start identities again before force cleanup.
func BeginTermination(cmd *exec.Cmd) func() {
	root := cmd.Process.Pid
	output, _ := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	parents := map[int]int{}
	for _, line := range strings.Split(string(output), "\n") {
		var pid, ppid int
		if _, e := fmt.Sscan(line, &pid, &ppid); e == nil {
			parents[pid] = ppid
		}
	}
	ids := map[int]string{}
	for pid := range parents {
		current := pid
		for depth := 0; depth < 128; depth++ {
			parent, ok := parents[current]
			if !ok || parent <= 1 {
				break
			}
			if parent == root {
				if stamp, e := processStamp(pid); e == nil {
					ids[pid] = stamp
				}
				break
			}
			current = parent
		}
	}
	rootStamp, _ := processStamp(root)
	_ = TerminateTree(cmd, false)
	for pid, stamp := range ids {
		if now, e := processStamp(pid); e == nil && now == stamp {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	return func() {
		for pid, stamp := range ids {
			if now, e := processStamp(pid); e == nil && now == stamp {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if now, e := processStamp(root); e == nil && now == rootStamp {
			_ = TerminateTree(cmd, true)
		}
	}
}
