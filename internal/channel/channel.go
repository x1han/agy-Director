// Package channel 定义"通道"模型与本地代理端口探测。
//
// 设计前提（已实证）：Antigravity 的 Go 后端只从环境变量解析代理，
// 支持 http / socks5 两种 scheme。因此"通道"被抽象为
// 一个本地代理端点 + 一种协议，而不是绑定某个特定客户端。
package channel

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Scheme 是本地代理端点支持的协议。
type Scheme string

const (
	// SchemeHTTP 是 HTTP 代理（支持 CONNECT 隧道）。
	SchemeHTTP Scheme = "http"
	// SchemeSOCKS5 是 SOCKS5 代理。
	SchemeSOCKS5 Scheme = "socks5"
)

// Channel 是一个本地代理端点。
type Channel struct {
	// Name 是人类可读名称，如 "v2rayN"。
	Name string `json:"name"`
	// Host 是代理主机，通常是 127.0.0.1。
	Host string `json:"host"`
	// Port 是代理端口。
	Port int `json:"port"`
	// Scheme 是协议。空值表示"未确定，由探测得出"。
	Scheme Scheme `json:"scheme,omitempty"`
	// Protocols 是该端点实测支持的全部协议（探测得到，不持久化）。
	// mixed inbound 会同时包含 http 与 socks5。
	Protocols []Scheme `json:"-"`
	// LAN 为 true 表示该端点监听在非回环地址（0.0.0.0），
	// 探测流量可能被同网段看到，默认应跳过。
	LAN bool `json:"lan,omitempty"`
	// Preferred 为 true 表示用户显式指定，探测时不做 auto 淘汰。
	Preferred bool `json:"preferred,omitempty"`
}

// Addr 返回 host:port 形式。
func (c Channel) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// URL 返回可注入到 HTTP_PROXY / HTTPS_PROXY 的完整 URL。
// socks5 通道同样用它，因为 Go 的 net/http.Transport 原生支持
// "socks5://" scheme（见 x/net/http/httpproxy 与 x/net/proxy）。
func (c Channel) URL() string {
	u := url.URL{
		Scheme: string(c.Scheme),
		Host:   c.Addr(),
	}
	return u.String()
}

// String 用于 CLI 输出。
func (c Channel) String() string {
	name := c.Name
	if name == "" {
		name = c.Addr()
	}
	return fmt.Sprintf("%s (%s://%s)", name, c.Scheme, c.Addr())
}

// Equal 比较两个通道是否指向同一个端点。
func (c Channel) Equal(o Channel) bool {
	return c.Host == o.Host && c.Port == o.Port && c.Scheme == o.Scheme
}

// Validate 检查通道字段是否自洽。
func (c Channel) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("channel %q: host 不能为空", c.Name)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("channel %q: port %d 越界", c.Name, c.Port)
	}
	switch c.Scheme {
	case "", SchemeHTTP, SchemeSOCKS5:
		return nil
	default:
		return fmt.Errorf("channel %q: 不支持的 scheme %q（仅 http / socks5）", c.Name, c.Scheme)
	}
}

// ParseTarget 把 "host:port" 解析成 host 与端口。
// IPv6 的 "[::1]:1080" 形式由 net.SplitHostPort 处理。
func ParseTarget(target string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(target)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("端口不是数字: %q", p)
	}
	return h, n, nil
}

// IsLoopback 判断 host 是否指向本机回环。
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// JoinNoProxy 把已有的 NO_PROXY 值与默认回环项合并，
// 去重且保持原有顺序（用户自己的项优先，不丢失）。
func JoinNoProxy(existing string) string {
	seen := map[string]bool{}
	var out []string

	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}

	for _, part := range strings.Split(existing, ",") {
		add(part)
	}
	// 回环地址永远不进代理。
	add("localhost")
	add("127.0.0.1")
	add("::1")

	return strings.Join(out, ",")
}

// EffectiveNoProxy 读取宿主机现有的 NO_PROXY，兼容大小写两种写法，
// 再合并回环地址。
//
// 读取规则：
//
//   - 只设了小写 → 用小写的值。
//   - 只设了大写 → 用大写的值。
//   - **两个都设了** → 两个都并进来（大写在前）。
//
// 为什么不能只看大写：用户的 rc 文件里常写的是小写
// `export no_proxy=...`。Windows 上 os.Getenv 大小写不敏感，
// 两个名字其实是同一个条目；Unix 上它们是两个独立条目，
// 只读大写会把用户的 bypass 列表整段丢掉——那等于让用户的
// 内网服务全部被代理。
//
// ⚠️ 关于"两个都设了"：Go 的 httpproxy 只读大写
// （getEnvAny("NO_PROXY", "no_proxy")），所以**大写决定**实际行为。
// 这里仍把小写并进来，理由是：我们注入的 NO_PROXY 会被
// Antigravity 的登录 shell 环境逐键覆盖，值更大意味着
// 用户的 bypass 意图被完整保留——而不是因为小写会影响 Antigravity。
// 合并只增不减，不会让本该走代理的流量变成绕过代理。
//
// 返回值已经过 JoinNoProxy，含 localhost,127.0.0.1,::1。
func EffectiveNoProxy() string {
	return JoinNoProxy(MergeNoProxy(os.Getenv("NO_PROXY"), os.Getenv("no_proxy")))
}

// MergeNoProxy 合并大写与小写的 NO_PROXY 值。
//
// 规则：
//
//   - 只设了小写 → 返回小写的值。
//   - 只设了大写 → 返回大写的值。
//   - **两个都设了** → 两个都并进来（大写在前）。
//
// 抽成纯函数是为了能测：Windows 上 os.Getenv 大小写不敏感，
// NO_PROXY 与 no_proxy 是**同一个**环境条目，
// 无法在 Windows 上构造"两个都设"的场景。
//
// ⚠️ 合并只增不减，所以本该走代理的流量不会因此变成绕过代理。
func MergeNoProxy(upper, lower string) string {
	switch {
	case upper == "":
		return lower
	case lower == "" || lower == upper:
		return upper
	default:
		return upper + "," + lower
	}
}

// HostNoProxy 返回宿主机现有的 NO_PROXY 原始值（不合并回环项），
// 兼容大小写两种写法，大写优先。
//
// 用于"和宿主机现状比对"的场景（比如 doctor）：
// 这种比对要拿**原始值**当基准，合并过回环项会产生假冲突。
func HostNoProxy() string {
	if v := os.Getenv("NO_PROXY"); v != "" {
		return v
	}
	return os.Getenv("no_proxy")
}

// DefaultTimeout 是探测各阶段的默认超时。
const DefaultTimeout = 3 * time.Second
