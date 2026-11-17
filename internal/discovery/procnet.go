package discovery

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// parseProcNetTCPReader 解析 /proc/net/tcp（与 tcp6）的内容。
//
// 格式：每行一个 socket，表头之后：
//
//	sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt ...
//	   1        2            3    4        5:6           7:8    9    10
//
//	st 是**第 4 列**（下标 3），"0A" = LISTEN；
//	local_address 是**第 2 列**（下标 1），形如 "0100007F:1F90"。
//
// 早期版本误把 st 当成第 3 列（下标 2，那是 rem_address），
// 于是把每一行都判成非 LISTEN，表读出来是空的。
//
// 这个函数**没有平台依赖**（只吃 io.Reader），所以放在无 build tag 的
// 文件里 —— 否则 Linux 专用的解析逻辑在 Windows 开发机上
// 永远跑不到单测。ports_linux.go 只负责打开文件。
func parseProcNetTCPReader(r io.Reader) ([]ListenPort, error) {
	var out []ListenPort

	scanner := bufio.NewScanner(r)
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
		if fields[3] != "0A" {
			continue // 只取 LISTEN
		}

		host, port, wildcard, ok := parseProcListenAddr(fields[1])
		if !ok {
			continue
		}
		out = append(out, ListenPort{Host: host, Port: port, Wildcard: wildcard})
	}
	return out, scanner.Err()
}

// parseProcListenAddr 解析 /proc/net/tcp 的 local_address 字段，
// 并**归一化**通配监听地址。
//
// 归一化这一步不能省：Linux 上双栈监听（IPV6_V6ONLY=0，Go/Java
// 代理客户端的默认）只出现在 /proc/net/tcp6 里，且地址是 IPv4-mapped
// 形式：
//
//	::ffff:127.0.0.1  ->  词 00000000 00000000 FFFF0000 0100007F
//	::ffff:0.0.0.0    ->  词 00000000 00000000 FFFF0000 00000000
//
// 按词内字节反序后是 16 字节，Go 的 net.IP.String() 会识别出
// v4InV6Prefix：
//
//	::ffff:127.0.0.1  ->  "127.0.0.1"
//	::ffff:0.0.0.0    ->  "0.0.0.0"   ← 通配监听！
//
// 所以归一化之后才是 127.0.0.1 / Wildcard=true，
// 否则 "0.0.0.0" 会带着 Wildcard=false 漏网。
//
// 这个函数没有平台依赖，便于跨平台单测（procnet_test.go）。
func parseProcListenAddr(s string) (host string, port int, wildcard bool, ok bool) {
	raw, port, ok := parseHexAddr(s)
	if !ok {
		return "", 0, false, false
	}
	normHost, wild := normalizeListenHost(raw)
	return normHost, port, wild, true
}

// dedupeListenPorts 按 host:port 去重。
//
// 同一条监听可能同时以通配形式和具体形式出现（例如 IPv6 的
// ::ffff:0.0.0.0 与 IPv4 的 0.0.0.0）。此时**保留通配的那条**，
// 因为"局域网可见"是更重要的信息。
func dedupeListenPorts(list []ListenPort) []ListenPort {
	best := map[string]ListenPort{}
	var order []string

	for _, p := range list {
		key := fmt.Sprintf("%s:%d", p.Host, p.Port)
		prev, seen := best[key]
		if !seen {
			order = append(order, key)
			best[key] = p
			continue
		}
		if p.Wildcard && !prev.Wildcard {
			best[key] = p
		}
	}

	out := make([]ListenPort, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	return out
}
