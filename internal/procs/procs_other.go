//go:build !windows && !linux && !darwin

package procs

import (
	"fmt"
	"runtime"
)

// listUnsupported 在不支持的平台上返回明确的错误。
func list() ([]Proc, error) {
	return nil, fmt.Errorf("unsupported os: %s", runtime.GOOS)
}
