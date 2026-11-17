//go:build linux

package discovery

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
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

// parseHexAddr 解析 /proc/net/tcp 的 "0100007F:1F90"。
//
// IPv4 是小端 hex：0100007F -> 127.0.0.1
// IPv6 是 4 组小端 32 位 word。
func parseHexAddr(s string) (string, int, bool) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", 0, false
	}
	hexIP, hexPort := s[:idx], s[idx+1:]

	port, err := strconv.ParseUint(hexPort, 16, 32)
	if err != nil || port == 0 {
		return "", 0, false
	}

	ipBytes, err := hex.DecodeString(hexIP)
	if err != nil || len(ipBytes) != net.IPv6len {
		return "", 0, false
	}

	// /proc/net/tcp 用的是网络字节序的十六进制视图，
	// 直接按 4 字节一组反转即可还原。
	if len(ipBytes) == 4 {
		ipBytes = []byte{ipBytes[3], ipBytes[2], ipBytes[1], ipBytes[0]}
		return net.IP(ipBytes).String(), int(port), true
	}

	// IPv6：4 个 32 位 word，每个 word 内部小端
	var ipv6 [16]byte
	for w := 0; w < 4; w++ {
		word := ipBytes[w*4 : w*4+4]
		ipv6[w*4+0] = word[3]
		ipv6[w*4+1] = word[2]
		ipv6[w*4+2] = word[1]
		ipv6[w*4+3] = word[0]
	}
	return net.IP(ipv6[:]).String(), int(port), true
}

// unusedBinary 保留 binary 引用（供将来解析二进制格式时用）。
var _ = binary.LittleEndian
