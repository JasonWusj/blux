package flow

import (
	"blux/internal/platform"
	"path/filepath"
)

func platformAcceptLock(data string) (func(), error) {
	return platform.Lock(filepath.Join(data, "locks", "accept.lock"))
}
