package discovery

import (
	"net"
	"strings"
)

// splitListenAddr 拆 lsof（macOS）的地址字段。
//
//	"127.0.0.1:10808" -> ("127.0.0.1", "10808", true)
//	"*:10808"        -> ("0.0.0.0", "10808", true)
//	"[::1]:10808"    -> ("::1",    "10808", true)
//
// ⚠️ 分隔符用**最后一个**冒号：IPv6 的裸形式 "::1:10808"
// 有多个冒号，取最后一个才对。
//
// 这个函数没有平台依赖，所以放在无 build tag 的文件里，
// 这样 Windows 上也能跑单元测试（listenaddr_test.go）。
func splitListenAddr(addr string) (string, string, bool) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", "", false
	}
	host := addr[:idx]
	port := addr[idx+1:]

	// lsof 的 IPv6 地址带方括号。
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	// lsof 在 IPv4 通配监听上打印 "*"。
	if host == "*" {
		host = "0.0.0.0"
	}
	if net.ParseIP(host) == nil {
		return "", "", false
	}
	return host, port, true
}
