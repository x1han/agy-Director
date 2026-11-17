//go:build darwin

package discovery

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// darwinListenPorts 用 sysctl 读 macOS 的 TCP 监听表。
//
// macOS 没有 /proc/net/tcp，sysctl net.tcp.* 输出的格式随版本变，
// 因此这里用 lsof（系统自带）拿结构化的输出，不去解析 sysctl 的表文本。
//
// lsof 输出示例：
//
//	COMMAND  PID USER   FD   TYPE DEVICE SIZE/OFF NODE NAME
//	v2rayN  1234 hxsci  12u  IPv4 0x...      0t0   TCP 127.0.0.1:10808 (LISTEN)
//
// 最后一列形如 "127.0.0.1:10808 (LISTEN)" 或 "*:10808 (LISTEN)"。
func listenPorts() ([]ListenPort, error) {
	out, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN").Output()
	if err != nil {
		return nil, fmt.Errorf("lsof 读取监听表失败: %w", err)
	}

	var outPorts []ListenPort
	seen := map[string]bool{}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// 表头行
		if fields[0] == "COMMAND" {
			continue
		}
		// 找 "(LISTEN)" 所在的位置，取它前面的地址部分
		listenIdx := -1
		for i, f := range fields {
			if f == "(LISTEN)" {
				listenIdx = i
				break
			}
		}
		if listenIdx < 1 {
			continue
		}
		addr := fields[listenIdx-1]

		host, portStr, ok := splitListenAddr(addr)
		if !ok {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			continue
		}

		normHost, wildcard := normalizeListenHost(host)
		key := fmt.Sprintf("%s:%d", normHost, port)
		if seen[key] {
			continue
		}
		seen[key] = true
		outPorts = append(outPorts, ListenPort{Host: normHost, Port: port, Wildcard: wildcard})
	}

	if len(outPorts) == 0 {
		return nil, fmt.Errorf("lsof 没有读到任何监听端口")
	}
	return outPorts, nil
}

// splitListenAddr 见 listenaddr.go（无平台依赖，便于跨平台单测）。
