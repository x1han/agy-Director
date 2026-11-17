//go:build !windows && !linux && !darwin

package discovery

import (
	"fmt"
	"runtime"
)

// listenPorts 在不支持的平台上返回一个明确的错误。
//
// 刻意不提供全端口扫描兜底——那是本项目明令禁止的行为。
func listenPorts() ([]ListenPort, error) {
	return nil, fmt.Errorf("unsupported os: %s", runtime.GOOS)
}
