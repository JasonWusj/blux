package platform

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"
)

func ConfigureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func TerminateTree(cmd *exec.Cmd, force bool) error {
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), args...).Run()
}
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	overlap := new(windows.Overlapped)
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlap); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlap); _ = f.Close() }, nil
}

func AttachProcessTree(cmd *exec.Cmd) (func(), error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	defer windows.CloseHandle(process)
	if e = windows.AssignProcessToJobObject(job, process); e != nil {
		windows.CloseHandle(job)
		return nil, e
	}
	return func() { _ = windows.CloseHandle(job) }, nil
}
func BeginTermination(cmd *exec.Cmd) func() {
	stamp, _ := processStamp(cmd.Process.Pid)
	_ = TerminateTree(cmd, false)
	return func() {
		if now, e := processStamp(cmd.Process.Pid); e == nil && now == stamp {
			_ = TerminateTree(cmd, true)
		}
	}
}
