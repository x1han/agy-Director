package discovery

import (
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// parseHexAddr 解析 /proc/net/tcp 的 "0100007F:1F90"。
//
// Linux 内核把地址写成**网络字节序**的十六进制文本：
//
//   - IPv4（8 个 hex 字符）：4 字节整体反序
//     0100007F -> 127.0.0.1，00000000 -> 0.0.0.0
//   - IPv6（32 个 hex 字符）：4 个 32 位 word，**每个 word 内部**反序
//     00000000000000000000000001000000 -> ::1
//
// 端口是 4 个 hex 字符的大端值（1F90 = 8080）。
//
// ⚠️ 长度必须**同时**接受 4（IPv4）和 16（IPv6）字节。
// 早期版本只接受 16，把所有 IPv4 行都丢掉了——Linux 上
// /proc/net/tcp 里绝大多数条目是 IPv4，结果等于整个表读不到。
//
// 这个函数没有平台依赖，所以放在无 build tag 的文件里，
// 这样 Windows 上也能跑单元测试（ports_linux_test.go）。
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
	if err != nil {
		return "", 0, false
	}

	switch len(ipBytes) {
	case net.IPv4len:
		ipBytes = []byte{ipBytes[3], ipBytes[2], ipBytes[1], ipBytes[0]}
		return net.IP(ipBytes).String(), int(port), true
	case net.IPv6len:
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
	return "", 0, false
}
