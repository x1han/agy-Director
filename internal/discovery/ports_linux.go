//go:build linux

package discovery

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// linuxListenPorts 读 /proc/net/tcp 与 /proc/net/tcp6。
//
// 格式：每行一个 socket，表头之后第 4 列是 local_address，
// 形如 "0100007F:1F90"（hex 的 IP:PORT，小端十六进制）。
// 第 3 列是状态，"0A" = LISTEN。
//
// 不调外部命令，不做字符串解析出 netstat 的表格。
func listenPorts() ([]ListenPort, error) {
	var out []ListenPort
	seen := map[string]bool{}

	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		ports, err := parseProcNetTCP(path)
		if err != nil {
			continue
		}
		for _, p := range ports {
			key := fmt.Sprintf("%s:%d", p.Host, p.Port)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("无法从 /proc/net/tcp* 读到监听端口")
	}
	return out, nil
}

// parseProcNetTCP 解析 /proc/net/tcp 的内容。
func parseProcNetTCP(path string) ([]ListenPort, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []ListenPort
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			continue // 表头
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// state == "0A" 表示 LISTEN
		//
		// ⚠️ 列号：/proc/net/tcp 的列是
		//	sl local_address rem_address st tx_queue:rx_queue ...
		//	st 是**第 4 列**（下标 3），local_address 是**第 2 列**（下标 1）。
		// 早期版本误把 st 当成第 3 列（下标 2，那是 rem_address），
		// 于是把每一行都判成非 LISTEN，表读出来是空的。
		if fields[3] != "0A" {
			continue
		}
		host, port, ok := parseHexAddr(fields[1])
		if !ok {
			continue
		}
		out = append(out, ListenPort{Host: host, Port: port})
	}
	return out, scanner.Err()
}

// parseHexAddr 见 hexaddr.go（无平台依赖，便于跨平台单测）。
