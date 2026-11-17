//go:build linux

package discovery

import (
	"fmt"
	"os"
)

// listenPorts 读 /proc/net/tcp 与 /proc/net/tcp6。
//
// 格式与解析见 procnet.go（无平台依赖，便于跨平台单测）；
// 这里只负责打开文件。
//
// 不调外部命令，不做字符串解析出 netstat 的表格。
func listenPorts() ([]ListenPort, error) {
	var all []ListenPort

	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		ports, err := parseProcNetTCPReader(f)
		f.Close()
		if err != nil {
			continue
		}
		all = append(all, ports...)
	}

	if len(all) == 0 {
		return nil, fmt.Errorf("无法从 /proc/net/tcp* 读到监听端口")
	}
	return dedupeListenPorts(all), nil
}
