package channel

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProbeResult 是单个端口的协议探测结果。
type ProbeResult struct {
	Channel Channel
	// HTTPProxy 为 true 表示该端口接受 HTTP CONNECT。
	HTTPProxy bool
	// SOCKS5 为 true 表示该端口接受 SOCKS5 握手。
	SOCKS5 bool
	// LAN 为 true 表示端口监听在非回环地址。
	LAN bool
	// Err 记录失败原因（全部失败时有值）。
	Err error
}

// Mixed 为 true 表示该端口同时支持 HTTP 与 SOCKS5（mixed inbound）。
func (r ProbeResult) Mixed() bool { return r.HTTPProxy && r.SOCKS5 }

// Schemes 返回该端口支持的协议列表，HTTP 优先（net/http 对其支持最完整）。
func (r ProbeResult) Schemes() []Scheme {
	var out []Scheme
	if r.HTTPProxy {
		out = append(out, SchemeHTTP)
	}
	if r.SOCKS5 {
		out = append(out, SchemeSOCKS5)
	}
	return out
}

// Best 返回该端口最优的 scheme，HTTP 优先。
func (r ProbeResult) Best() Scheme {
	s := r.Schemes()
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// ProbeTarget 对一个 host:port 做协议探测。
// 不做任何外部请求——只验证端点是否讲 HTTP CONNECT / SOCKS5。
func ProbeTarget(host string, port int, timeout time.Duration) ProbeResult {
	res := ProbeResult{Channel: Channel{Host: host, Port: port}}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// TCP 可达性先行：不通就没什么好谈的。
	if err := dialTimeout(host, port, timeout); err != nil {
		res.Err = err
		return res
	}
	res.LAN = !IsLoopback(host)

	res.HTTPProxy = probeHTTPConnect(host, port, timeout)
	res.SOCKS5 = probeSOCKS5(host, port, timeout)

	if !res.HTTPProxy && !res.SOCKS5 {
		res.Err = fmt.Errorf("端口开放但既不是 HTTP 代理也不是 SOCKS5 端点")
	}
	return res
}

func dialTimeout(host string, port int, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// probeHTTPConnect 发一个 CONNECT 请求，看端点是否回 200。
// 注意：只看首个状态行，且必须以 "HTTP/" 开头——
// 否则说明对端不是 HTTP 代理（例如某些服务会回二进制或自定义协议）。
func probeHTTPConnect(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	_, _ = conn.Write([]byte("CONNECT generativelanguage.googleapis.com:443 HTTP/1.1\r\n" +
		"Host: generativelanguage.googleapis.com:443\r\n\r\n"))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "HTTP/") {
		return false
	}
	// "HTTP/1.1 200 Connection established"
	return strings.Contains(line, " 200")
}

// probeSOCKS5 做 SOCKS5 协商握手。
//
// 只做 greeting（05 01 00 -> 05 00），不做 CONNECT：
// greeting 足以确认"对端讲 SOCKS5"，而 CONNECT 需要真正连出去，
// 在扫描阶段会拖慢并可能超时（实测 1.2s 预算下会漏判 socks5）。
// 真正的功能验证交给 CheckReachability —— Go 的 http.Transport
// 原生支持 socks5:// scheme，能直接验证通道可用性。
func probeSOCKS5(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	// 无认证协商
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return false
	}
	resp := make([]byte, 2)
	if _, err := conn.Read(resp); err != nil {
		return false
	}
	// VER=5 且方法=0（NO AUTH）
	return resp[0] == 0x05 && resp[1] == 0x00
}

// Reachability 用通道真发一个 HTTPS 请求，验证它能到目标站点。
// 这是"端口在监听"和"通道真的能用"之间的区别——
// 后者才决定能不能启动 Antigravity。
type Reachability struct {
	URL    string
	Status int
	// FromGoogle 为 true 表示响应头里的 Server 来自 Google，
	// 用来排除"被代理侧/中间设备拦截并返回假响应"的情况。
	FromGoogle bool
	Latency    time.Duration
	Err        error
}

// ProbeURLs 是 Antigravity 后端真实会访问的端点。
// 两个都要测：只测一个可能漏掉"某域名被墙/被限"的情况。
var ProbeURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://generativelanguage.googleapis.com/v1beta/models",
}

// CheckReachability 通过通道访问目标 URL，判断通道是否真的可用。
func CheckReachability(ch Channel, targetURL string, timeout time.Duration) Reachability {
	r := Reachability{URL: targetURL}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	proxyURL, err := url.Parse(ch.URL())
	if err != nil {
		r.Err = err
		return r
	}

	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		TLSHandshakeTimeout: timeout,
		DisableKeepAlives: true,
	}
	// Go 的 Transport 原生支持 socks5:// —— 无需额外代码。
	client := &http.Client{Transport: transport, Timeout: timeout}

	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		r.Err = err
		return r
	}
	resp, err := client.Do(req)
	r.Latency = time.Since(start)
	if err != nil {
		r.Err = err
		return r
	}
	defer resp.Body.Close()

	r.Status = resp.StatusCode
	r.FromGoogle = isGoogleResponse(resp.Header)
	return r
}

// isGoogleResponse 判断响应是否来自 Google 的服务端。
// Google 的 API 网关会回 Server: ESF 或 Server: scaffolding on HTTPServer2。
func isGoogleResponse(h http.Header) bool {
	for _, v := range h.Values("Server") {
		v = strings.TrimSpace(v)
		if v == "ESF" || strings.HasPrefix(v, "scaffolding") {
			return true
		}
	}
	return false
}

// VerifyChannel 对一个通道做完整的可用性判定：
// 1) 协议探测（端点是否讲 http/socks5）
// 2) 真实请求（能否到达 Antigravity 的后端端点）
// 返回的 ok 为 true 表示这个通道可以放心用来启动 Antigravity。
func VerifyChannel(ch Channel, timeout time.Duration) (ok bool, detail string) {
	if err := ch.Validate(); err != nil {
		return false, err.Error()
	}

	probe := ProbeTarget(ch.Host, ch.Port, timeout)
	if probe.Err != nil {
		return false, fmt.Sprintf("端点不可用: %v", probe.Err)
	}
	if !probe.HTTPProxy && !probe.SOCKS5 {
		return false, "端点不是 http/socks5 代理"
	}

	// 用探测出的最优 scheme
	ch.Scheme = probe.Best()

	for _, u := range ProbeURLs {
		rr := CheckReachability(ch, u, timeout)
		if rr.Err != nil {
			return false, fmt.Sprintf("%s 请求失败: %v", u, rr.Err)
		}
		// 5xx 与 407 是明确的失败；403 是"缺 API key 但链路通"，
		// 对探测而言是可接受的（说明真的连到了 Google）。
		if rr.Status >= 500 || rr.Status == 407 {
			return false, fmt.Sprintf("%s 返回 HTTP %d", u, rr.Status)
		}
		if !rr.FromGoogle {
			return false, fmt.Sprintf("%s 响应不是来自 Google（可能被拦截）", u)
		}
	}
	return true, fmt.Sprintf("%s 可用", ch)
}
