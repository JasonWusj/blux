package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve npm's Windows .cmd shim to its Node entrypoint instead of passing
// user-controlled paths through cmd.exe quoting or shell expansion.
func CodexCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(binary), ".cmd") || strings.EqualFold(filepath.Ext(binary), ".bat")) {
		script := filepath.Join(filepath.Dir(binary), "node_modules", "@openai", "codex", "bin", "codex.js")
		if _, err := os.Stat(script); err != nil {
			return nil, fmt.Errorf("Codex npm entrypoint unavailable: %w", err)
		}
		node, err := exec.LookPath("node.exe")
		if err != nil {
			return nil, err
		}
		binary = node
		args = append([]string{script}, args...)
	}
	if ctx == nil {
		return exec.Command(binary, args...), nil
	}
	return exec.CommandContext(ctx, binary, args...), nil
}
