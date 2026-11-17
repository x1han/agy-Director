package channel

import (
	"bufio"
	"fmt"
	"io"
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
	// LAN 为 true 表示端口原始监听在非回环地址（0.0.0.0 / ::）。
	LAN bool
	// Err 记录失败原因（全部失败时有值）。
	Err error
}

// Mixed 为 true 表示该端口同时支持 HTTP 与 SOCKS5（mixed inbound）。
func (r ProbeResult) Mixed() bool { return r.HTTPProxy && r.SOCKS5 }

// Schemes 返回该端口支持的协议列表，HTTP 优先。
//
// 优先 HTTP 的理由：net/http.Transport 对 HTTP 代理的支持路径最完整
// （CONNECT 隧道 + 绝对 URI 两种形式都支持），而 socks5:// 走的是
// x/net/proxy 的 SOCKS5 dialer，某些边缘场景（如需要 HTTP/1.0 代理、
// 或代理侧要求 Basic 认证）行为不如 HTTP 代理一致。
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
//
// host 必须是**连接目标**地址，不能是监听通配地址。
// 监听表里的 "0.0.0.0" / "::" 必须先经 NormalizeTarget 归一化成
// 127.0.0.1 / ::1，否则会写出 HTTP_PROXY=http://0.0.0.0:10810 这种
// 在部分平台属非法目标地址的值。
func ProbeTarget(host string, port int, timeout time.Duration) ProbeResult {
	host = NormalizeTarget(host)
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

// NormalizeTarget 把监听通配地址归一化成可连接的本地地址。
//
// "0.0.0.0" / "" / "*" -> "127.0.0.1"
// "::"                 -> "::1"
// 其余原样返回。
//
// 背景：服务端用 INADDR_ANY 监听时，监听表里记的是 "0.0.0.0:port"。
// 把这个值直接当作代理目标写进 HTTP_PROXY 是错的——
// 0.0.0.0 是「本机所有接口」的监听语义，不是合法的连接目标。
func NormalizeTarget(host string) string {
	switch strings.TrimSpace(host) {
	case "", "*", "0.0.0.0":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	}
	return host
}

func dialTimeout(host string, port int, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// probeHTTPConnect 发一个 CONNECT 请求，用标准库解析响应。
//
// 不能手写字符串匹配：TCP 是字节流，没有消息边界；
// 而且 "HTTP/1.1 200 Connection established" 这类首行的
// reason phrase 里也可能出现 " 200"，用 strings.Contains 会误判。
// 用 http.ReadResponse 解析出 StatusCode 才可靠。
func probeHTTPConnect(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: "generativelanguage.googleapis.com:443"},
		Host:   "generativelanguage.googleapis.com:443",
		Header: http.Header{},
	}
	if err := req.Write(conn); err != nil {
		return false
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// probeSOCKS5 做 SOCKS5 协商握手。
//
// 只做 greeting（05 01 00 -> 05 00），不做 CONNECT：
// greeting 足以确认"对端讲 SOCKS5"，而 CONNECT 需要真正连出去，
// 在扫描阶段会拖慢并可能超时（实测 1.2 s 预算下会漏判 socks5）。
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
	// 必须用 io.ReadFull：TCP 无消息边界，一次 Read 可能只返回 1 字节。
	// 用 conn.Read 会拿到 n=1, err=nil，而 resp[1] 是零值——
	// 那种情况恰好蒙混过关，换个校验就会静默判错。
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
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
		Proxy:               http.ProxyURL(proxyURL),
		TLSHandshakeTimeout: timeout,
		DisableKeepAlives:   true,
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
