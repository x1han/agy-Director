package channel

import (
	"testing"
	"time"
)

func TestJoinNoProxy(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "localhost,127.0.0.1,::1"},
		{"localhost", "localhost,127.0.0.1,::1"},
		{"xapi.example.com,localhost", "xapi.example.com,localhost,127.0.0.1,::1"},
		{"a,b,a", "a,b,localhost,127.0.0.1,::1"},
		// 用户已有的值必须保留，不能被覆盖
		{"api.example.com,.example.com,localhost,127.0.0.1",
			"api.example.com,.example.com,localhost,127.0.0.1,::1"},
	}
	for _, c := range cases {
		got := JoinNoProxy(c.in)
		if got != c.want {
			t.Errorf("JoinNoProxy(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTarget(t *testing.T) {
	h, p, err := ParseTarget("127.0.0.1:10808")
	if err != nil || h != "127.0.0.1" || p != 10808 {
		t.Fatalf("ParseTarget = %q,%d,%v", h, p, err)
	}
	h, p, err = ParseTarget("[::1]:7890")
	if err != nil || h != "::1" || p != 7890 {
		t.Fatalf("ParseTarget ipv6 = %q,%d,%v", h, p, err)
	}
	if _, _, err := ParseTarget("no-port"); err == nil {
		t.Fatal("expected error for missing port")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true,
		"localhost":  true,
		"::1":        true,
		"0.0.0.0":    false,
		"192.168.1.5": false,
	}
	for host, want := range cases {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestChannelURL(t *testing.T) {
	ch := Channel{Host: "127.0.0.1", Port: 10808, Scheme: SchemeHTTP}
	if got := ch.URL(); got != "http://127.0.0.1:10808" {
		t.Errorf("URL = %q", got)
	}
	ch.Scheme = SchemeSOCKS5
	if got := ch.URL(); got != "socks5://127.0.0.1:10808" {
		t.Errorf("URL = %q", got)
	}
}

func TestValidate(t *testing.T) {
	if err := (Channel{Host: "127.0.0.1", Port: 10808}).Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := (Channel{Host: "", Port: 10808}).Validate(); err == nil {
		t.Error("expected error for empty host")
	}
	if err := (Channel{Host: "h", Port: 70000}).Validate(); err == nil {
		t.Error("expected error for out-of-range port")
	}
	if err := (Channel{Host: "h", Port: 80, Scheme: "ftp"}).Validate(); err == nil {
		t.Error("expected error for unsupported scheme")
	}
}

// TestProbeAgainstLiveProxy 在本机真实代理端口上跑探测。
// 没有可用代理时跳过（CI/无代理环境下不该失败）。
func TestProbeAgainstLiveProxy(t *testing.T) {
	const port = 10808
	ch := Channel{Host: "127.0.0.1", Port: port}

	probe := ProbeTarget(ch.Host, ch.Port, 2*time.Second)
	if probe.Err != nil {
		t.Skipf("no proxy on %d, skipping: %v", port, probe.Err)
	}
	if !probe.HTTPProxy && !probe.SOCKS5 {
		t.Fatalf("port %d speaks neither http nor socks5", port)
	}
	t.Logf("probe: http=%v socks5=%v lan=%v", probe.HTTPProxy, probe.SOCKS5, probe.LAN)

	// 至少一种协议可用时，Best() 必须给出非空 scheme
	if probe.Best() == "" {
		t.Fatal("Best() returned empty scheme despite a working protocol")
	}
}
