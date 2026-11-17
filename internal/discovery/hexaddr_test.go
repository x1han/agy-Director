package discovery

import "testing"

// TestParseHexAddr 验证 /proc/net/tcp 的地址解析。
//
// 这些向量取自内核的 /proc/net/tcp 格式：地址是**网络字节序**
// 的十六进制文本（IPv4 按 4 字节反序，IPv6 按 32 位 word 反序）。
//
// ⚠️ 早期版本只接受 16 字节，把所有 IPv4 行都丢了——
// Linux 上绝大多数条目是 IPv4，整个表因此读不出来。
//
// 这个测试没有 build tag，因此在 Windows 上也能跑：
// 解析逻辑与平台无关（见 hexaddr.go）。
func TestParseHexAddr(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantHost string
		wantPort int
		wantOK   bool
	}{
		{
			name:     "IPv4 回环",
			in:       "0100007F:1F90",
			wantHost: "127.0.0.1",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:     "IPv4 通配",
			in:       "00000000:1F90",
			wantHost: "0.0.0.0",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			// 10.0.0.1 的网络字节序是 0A 00 00 01，
			// 内核用 %08X 打印成小端视图 = 0100000A。
			name:     "IPv4 具体地址",
			in:       "0100000A:2A44",
			wantHost: "10.0.0.1",
			wantPort: 10820,
			wantOK:   true,
		},
		{
			// IPv6 回环：00000000000000000000000001000000
			// 按 32 位 word 反序后 ::1
			name:     "IPv6 回环",
			in:       "00000000000000000000000001000000:1F90",
			wantHost: "::1",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:     "IPv6 通配",
			in:       "00000000000000000000000000000000:1F90",
			wantHost: "::",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:   "端口为 0 视为无效",
			in:     "0100007F:0000",
			wantOK: false,
		},
		{
			name:   "长度不对",
			in:     "0100007F1F:1F90",
			wantOK: false,
		},
		{
			name:   "缺少冒号",
			in:     "0100007F",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, ok := parseHexAddr(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (host=%q port=%d)", ok, tt.wantOK, host, port)
			}
			if !ok {
				return
			}
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
		})
	}
}
