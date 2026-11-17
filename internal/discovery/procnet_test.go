package discovery

import (
	"strconv"
	"strings"
	"testing"
)

// TestParseProcListenAddr 验证 /proc/net/tcp 的 local_address 解析与归一化。
//
// ⚠️ 这些用例覆盖两个**曾经出过事**的地方：
//
//  1. 通配监听地址必须归一化并标 Wildcard —— 早期版本漏了
//     normalizeListenHost，"0.0.0.0" 原样复活，Wildcard 永远 false。
//
//  2. IPv4-mapped IPv6（::ffff:a.b.c.d）—— 双栈监听（IPV6_V6ONLY=0）
//     只出现在 /proc/net/tcp6 里。Go 的 net.IP.String() 会把
//     ::ffff:127.0.0.1 打印成 "127.0.0.1"、把 ::ffff:0.0.0.0 打印成
//     "0.0.0.0"，所以归一化必须在解析**之后**做，否则后者会以
//     Wildcard=false 的 "0.0.0.0" 漏网。
//
// 没有 build tag —— 解析逻辑与平台无关（procnet.go），
// 所以 Windows 开发机上也能跑。
func TestParseProcListenAddr(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantHost     string
		wantPort     int
		wantWildcard bool
		wantOK       bool
	}{
		{
			name:     "IPv4 回环",
			in:       "0100007F:1F90",
			wantHost: "127.0.0.1",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			name:         "IPv4 通配",
			in:           "00000000:1F90",
			wantHost:     "127.0.0.1",
			wantPort:     8080,
			wantWildcard: true,
			wantOK:       true,
		},
		{
			name:         "IPv6 通配",
			in:           "00000000000000000000000000000000:1F90",
			wantHost:     "::1",
			wantPort:     8080,
			wantWildcard: true,
			wantOK:       true,
		},
		{
			name:     "IPv6 回环",
			in:       "00000000000000000000000001000000:1F90",
			wantHost: "::1",
			wantPort: 8080,
			wantOK:   true,
		},
		{
			// IPv4-mapped IPv6 的具体地址。
			// 词 00000000 00000000 FFFF0000 0100007F
			// → 反序后 00*10 FF FF 7F 00 00 01
			// → net.IP.String() 认出 v4InV6Prefix → "127.0.0.1"
			name:     "IPv4-mapped IPv6 回环",
			in:       "0000000000000000FFFF00000100007F:2A38",
			wantHost: "127.0.0.1",
			wantPort: 10808,
			wantOK:   true,
		},
		{
			// IPv4-mapped IPv6 的通配地址。
			// 词 00000000 00000000 FFFF0000 00000000
			// → 反序后 00*10 FF FF 00 00 00 00
			// → net.IP.String() → "0.0.0.0" ← 必须再归一化成 127.0.0.1
			name:         "IPv4-mapped IPv6 通配",
			in:           "0000000000000000FFFF000000000000:2A38",
			wantHost:     "127.0.0.1",
			wantPort:     10808,
			wantWildcard: true,
			wantOK:       true,
		},
		{
			name:   "端口为 0",
			in:     "0100007F:0000",
			wantOK: false,
		},
		{
			name:   "长度不对",
			in:     "0100007F1F:1F90",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, wildcard, ok := parseProcListenAddr(tt.in)
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
			if wildcard != tt.wantWildcard {
				t.Errorf("wildcard = %v, want %v", wildcard, tt.wantWildcard)
			}
		})
	}
}

// procNetSample 是一段真实的 /proc/net/tcp 格式文本。
//
// 列：sl local_address rem_address st tx_queue:rx_queue ...
const procNetSample = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0A000001:2A38 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 100 0 0 10 0
   3: 0100007F:2710 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12348 1 0000000000000000 100 0 0 10 0
`

// TestParseProcNetTCPReader 验证整行解析：状态过滤 + 归一化。
//
// ⚠️ 第 3 行是 ESTABLISHED（st = 01），必须被滤掉。
// 第 1 行是通配监听，必须归一化成 127.0.0.1 且 Wildcard=true。
func TestParseProcNetTCPReader(t *testing.T) {
	got, err := parseProcNetTCPReader(strings.NewReader(procNetSample))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("解析出 %d 条，want 3（ESTABLISHED 那行要被滤掉）: %+v", len(got), got)
	}

	// 第 1 条：0100007F:1F90 → 127.0.0.1:8080
	if got[0].Host != "127.0.0.1" || got[0].Port != 8080 || got[0].Wildcard {
		t.Errorf("第 1 条 = %+v，want {127.0.0.1 8080 false}", got[0])
	}
	// 第 2 条：00000000:1F90 → 127.0.0.1:8080 且 Wildcard
	if got[1].Host != "127.0.0.1" || got[1].Port != 8080 || !got[1].Wildcard {
		t.Errorf("第 2 条 = %+v，want {127.0.0.1 8080 true}", got[1])
	}
	// 第 3 条：0100007F:2710 → 127.0.0.1:10000
	if got[2].Host != "127.0.0.1" || got[2].Port != 10000 || got[2].Wildcard {
		t.Errorf("第 3 条 = %+v，want {127.0.0.1 10000 false}", got[2])
	}
}

// TestDedupeListenPorts 验证去重时保留通配标记。
//
// 同一条监听可能同时以通配形式和具体形式出现
// （IPv6 的 ::ffff:0.0.0.0 与 IPv4 的 0.0.0.0），
// 此时"局域网可见"这条信息不能丢。
func TestDedupeListenPorts(t *testing.T) {
	list := []ListenPort{
		{Host: "127.0.0.1", Port: 8080},                  // 具体在前
		{Host: "127.0.0.1", Port: 8080, Wildcard: true},  // 通配在后
		{Host: "127.0.0.1", Port: 9090, Wildcard: true},  // 只有通配
		{Host: "127.0.0.1", Port: 9090, Wildcard: false}, // 具体在后
		{Host: "::1", Port: 8080, Wildcard: true},        // 不同地址
	}

	got := dedupeListenPorts(list)

	if len(got) != 3 {
		t.Fatalf("去重后 %d 条，want 3: %+v", len(got), got)
	}
	byKey := map[string]ListenPort{}
	for _, p := range got {
		byKey[p.Host+":"+strconv.Itoa(p.Port)] = p
	}
	if p := byKey["127.0.0.1:8080"]; !p.Wildcard {
		t.Errorf("127.0.0.1:8080 的通配标记丢了: %+v", p)
	}
	if p := byKey["127.0.0.1:9090"]; !p.Wildcard {
		t.Errorf("127.0.0.1:9090 的通配标记丢了: %+v", p)
	}
	if p := byKey["::1:8080"]; !p.Wildcard {
		t.Errorf("::1:8080 的通配标记丢了: %+v", p)
	}
}
