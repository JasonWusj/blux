package platform

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

var ErrCanceled = errors.New("directory selection canceled")

// PickDirectory opens a native chooser on the machine running the local API.
// Scripts are fixed; project names and user text are never interpolated.
func PickDirectory(ctx context.Context) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "/usr/bin/osascript", "-e", `POSIX path of (choose folder with prompt "选择项目目录")`)
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-Command", `[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = '选择项目目录'; $dialog.ShowNewFolderButton = $false; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($dialog.SelectedPath) }; $dialog.Dispose()`)
	default:
		return "", errors.New("native directory selection is supported on macOS and Windows")
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "-128") {
			return "", ErrCanceled
		}
		return "", err
	}
	path := strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff"))
	if path == "" {
		return "", ErrCanceled
	}
	return path, nil
}

func PickFile(ctx context.Context) (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "/usr/bin/osascript", "-e", `POSIX path of (choose file with prompt "选择项目资料或技能文件")`)
	case "windows":
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-Command", `[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); Add-Type -AssemblyName System.Windows.Forms; $d=New-Object System.Windows.Forms.OpenFileDialog; $d.Title='选择项目资料或技能文件'; $d.Multiselect=$false; if($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK){[Console]::Write($d.FileName)}; $d.Dispose()`)
	default:
		return "", errors.New("unsupported desktop")
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "-128") {
			return "", ErrCanceled
		}
		return "", err
	}
	p := strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff"))
	if p == "" {
		return "", ErrCanceled
	}
	return p, nil
}
