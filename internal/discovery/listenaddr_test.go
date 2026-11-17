package discovery

import "testing"

// TestSplitListenAddr 验证 lsof 地址字段的拆分。
//
// 没有 build tag，所以在 Windows 上也能跑：
// 拆分逻辑与平台无关（见 listenaddr.go）。
func TestSplitListenAddr(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantHost string
		wantPort string
		wantOK   bool
	}{
		{
			name:     "IPv4 具体地址",
			in:       "127.0.0.1:10808",
			wantHost: "127.0.0.1",
			wantPort: "10808",
			wantOK:   true,
		},
		{
			name:     "IPv4 通配",
			in:       "*:10808",
			wantHost: "0.0.0.0",
			wantPort: "10808",
			wantOK:   true,
		},
		{
			name:     "IPv6 带方括号",
			in:       "[::1]:10808",
			wantHost: "::1",
			wantPort: "10808",
			wantOK:   true,
		},
		{
			// 裸 IPv6 有多个冒号，必须取最后一个。
			name:     "IPv6 裸形式",
			in:       "::1:10808",
			wantHost: "::1",
			wantPort: "10808",
			wantOK:   true,
		},
		{
			name:   "缺少冒号",
			in:     "127.0.0.1",
			wantOK: false,
		},
		{
			name:   "不是 IP",
			in:     "hostname:10808",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, ok := splitListenAddr(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (host=%q port=%q)", ok, tt.wantOK, host, port)
			}
			if !ok {
				return
			}
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("port = %q, want %q", port, tt.wantPort)
			}
		})
	}
}

// TestNormalizeListenHost 验证通配地址的归一化。
func TestNormalizeListenHost(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantWild bool
	}{
		{in: "0.0.0.0", wantHost: "127.0.0.1", wantWild: true},
		{in: "*", wantHost: "127.0.0.1", wantWild: true},
		{in: "", wantHost: "127.0.0.1", wantWild: true},
		{in: "::", wantHost: "::1", wantWild: true},
		{in: "[::]", wantHost: "::1", wantWild: true},
		{in: "127.0.0.1", wantHost: "127.0.0.1", wantWild: false},
		{in: "192.168.1.5", wantHost: "192.168.1.5", wantWild: false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			host, wild := normalizeListenHost(tt.in)
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
			if wild != tt.wantWild {
				t.Errorf("wildcard = %v, want %v", wild, tt.wantWild)
			}
		})
	}
}
