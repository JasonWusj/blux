package platform

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
)

const (
	notifyTitleLimit = 80
	notifyBodyLimit  = 200
)

// Notify shows a desktop notification. It is best effort: on platforms without
// support, or when the notification fails, it does nothing.
func Notify(title, body string) {
	if runtime.GOOS != "darwin" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "/usr/bin/osascript", notifyArgs(title, body)...).Run()
}

// notifyArgs builds the osascript arguments. The text travels as separate
// arguments read by a fixed script, so it is never interpreted as AppleScript.
func notifyArgs(title, body string) []string {
	script := "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run"
	return []string{"-e", script, clipNotice(title, notifyTitleLimit), clipNotice(body, notifyBodyLimit)}
}

func clipNotice(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(text))
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}
