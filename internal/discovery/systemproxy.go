package discovery

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// SystemProxy 是从操作系统设置里读到的系统代理。
type SystemProxy struct {
	// Enabled 为 true 表示系统代理处于开启状态。
	Enabled bool
	// Host 与 Port 是 HTTP 代理地址（无 scheme）。
	Host string
	Port int
	// Scheme 是 "http" 或 "https"（系统代理允许 https 代理）。
	Scheme string
	// Bypass 是 ProxyOverride / bypass list。
	Bypass []string
	// Source 说明来源，用于 CLI 输出。
	Source string
}

// Addr 返回 host:port。
func (s SystemProxy) Addr() string {
	if s.Host == "" || s.Port == 0 {
		return ""
	}
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

// ReadSystemProxy 读取当前操作系统的代理设置。
//
// 平台实现：
//   - Windows: 注册表 HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
//   - macOS:   scutil --proxy
//   - Linux:   环境变量 + gsettings（GNOME）
//
// 这是候选来源里最准的一个：绝大多数用 v2rayN / Clash 的用户
// 都开着系统代理，端口直接写在系统设置里，不需要猜。
func ReadSystemProxy() (SystemProxy, error) {
	switch runtime.GOOS {
	case "windows":
		return readWindowsSystemProxy()
	case "darwin":
		return readDarwinSystemProxy()
	case "linux":
		return readLinuxSystemProxy()
	default:
		return SystemProxy{}, fmt.Errorf("unsupported os: %s", runtime.GOOS)
	}
}

// ── Windows ────────────────────────────────────────────────────────────

// readWindowsSystemProxy 读注册表 Internet Settings。
//
// 注意：只读 ProxyEnable/ProxyServer/ProxyOverride，不写任何东西。
// 严禁修改系统代理设置——那会破坏用户的其他应用。
func readWindowsSystemProxy() (SystemProxy, error) {
	const key = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

	out, err := exec.Command("reg", "query", key, "/v", "ProxyEnable").Output()
	if err != nil {
		return SystemProxy{}, fmt.Errorf("读注册表失败: %w", err)
	}
	enabled := regValue(string(out), "ProxyEnable") == "0x1"

	server := ""
	if out, err := exec.Command("reg", "query", key, "/v", "ProxyServer").Output(); err == nil {
		server = regValue(string(out), "ProxyServer")
	}

	override := ""
	if out, err := exec.Command("reg", "query", key, "/v", "ProxyOverride").Output(); err == nil {
		override = regValue(string(out), "ProxyOverride")
	}

	sp := SystemProxy{
		Enabled: enabled,
		Source:  "Windows 系统代理（注册表）",
		Bypass:  splitOverride(override),
	}
	if host, port, scheme, ok := parseProxyServer(server); ok {
		sp.Host, sp.Port, sp.Scheme = host, port, scheme
	}
	return sp, nil
}

// regValue 从 reg query 的输出里取 "NAME    TYPE    VALUE" 里的 VALUE。
func regValue(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.EqualFold(fields[0], name) {
			return fields[len(fields)-1]
		}
	}
	return ""
}

// parseProxyServer 解析 ProxyServer 的值。
//
// 格式有两种：
//   - 单地址:  "127.0.0.1:10808"          （http 与 https 共用）
//   - 分协议:  "http=127.0.0.1:1080;https=127.0.0.1:1081"
//
// 分协议时优先取 https 的（Antigravity 后端主要走 https）。
func parseProxyServer(server string) (host string, port int, scheme string, ok bool) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", 0, "", false
	}

	if strings.Contains(server, "=") {
		byProto := map[string]string{}
		for _, part := range strings.Split(server, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			byProto[strings.ToLower(strings.TrimSpace(kv[0]))] = strings.TrimSpace(kv[1])
		}
		for _, proto := range []string{"https", "http"} {
			if addr, ok := byProto[proto]; ok {
				if h, p, _, ok2 := splitHostPort(addr); ok2 {
					return h, p, proto, true
				}
			}
		}
		return "", 0, "", false
	}

	if h, p, _, ok := splitHostPort(server); ok {
		return h, p, "http", true
	}
	return "", 0, "", false
}

// splitHostPort 拆 "host:port"，支持 [::1]:port。
func splitHostPort(addr string) (host string, port int, scheme string, ok bool) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0, "", false
	}
	// 去掉可能的 scheme 前缀
	if i := strings.Index(addr, "://"); i >= 0 {
		scheme = strings.ToLower(addr[:i])
		addr = addr[i+3:]
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 {
		return "", 0, "", false
	}
	return h, n, scheme, true
}

// splitOverride 拆 ProxyOverride。
// 形如 "<local>;localhost;127.*;10.*;192.168.*"
func splitOverride(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ── macOS ──────────────────────────────────────────────────────────────

// readDarwinSystemProxy 用 scutil --proxy 读取。
//
// 输出形如：
//   <dictionary> { 
//     ExceptionsList : <array> { ... }
//     HTTPEnable : 1
//     HTTPPort : 7890
//     HTTPProxy : 127.0.0.1
//     HTTPSEnable : 1
//     HTTPSPort : 7890
//     HTTPSProxy : 127.0.0.1
//   }
func readDarwinSystemProxy() (SystemProxy, error) {
	out, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return SystemProxy{}, fmt.Errorf("scutil --proxy 失败: %w", err)
	}

	fields := parseScutil(string(out))
	sp := SystemProxy{Source: "macOS 系统代理（scutil）"}

	// 优先 HTTPS，其次 HTTP
	for _, proto := range []string{"HTTPS", "HTTP"} {
		if fields[proto+"Enable"] == "1" {
			host := fields[proto+"Proxy"]
			port, _ := strconv.Atoi(fields[proto+"Port"])
			if host != "" && port > 0 {
				sp.Enabled = true
				sp.Host = host
				sp.Port = port
				sp.Scheme = strings.ToLower(proto)
				break
			}
		}
	}
	if v := fields["ExceptionsList"]; v != "" {
		sp.Bypass = strings.Split(v, ",")
	}
	return sp, nil
}

// parseScutil 解析 scutil --proxy 的 key : value 行。
func parseScutil(out string) map[string]string {
	fields := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		if key == "" || val == "" {
			continue
		}
		fields[key] = val
	}
	return fields
}

// ── Linux ──────────────────────────────────────────────────────────────

// readLinuxSystemProxy 读 GNOME gsettings，回退到环境变量。
func readLinuxSystemProxy() (SystemProxy, error) {
	sp := SystemProxy{Source: "Linux 系统代理"}

	// gsettings（GNOME）
	if out, err := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output(); err == nil {
		mode := strings.TrimSpace(strings.Trim(string(out), "'"))
		if mode == "'manual'" {
			sp.Enabled = true
			host, _ := gsettingString("org.gnome.system.proxy.http", "host")
			port, _ := gsettingInt("org.gnome.system.proxy.http", "port")
			if host != "" && port > 0 {
				sp.Host, sp.Port, sp.Scheme = host, port, "http"
			}
			// 优先 https 的
			host, _ = gsettingString("org.gnome.system.proxy.https", "host")
			port, _ = gsettingInt("org.gnome.system.proxy.https", "port")
			if host != "" && port > 0 {
				sp.Host, sp.Port, sp.Scheme = host, port, "https"
			}
			if ignore, _ := gsettingString("org.gnome.system.proxy", "ignore-hosts"); ignore != "" {
				sp.Bypass = strings.Split(ignore, ",")
			}
			return sp, nil
		}
	}

	// 环境变量回退
	for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		v := os.Getenv(name)
		if v == "" {
			continue
		}
		if h, p, scheme, ok := parseProxyServer(v); ok {
			sp.Enabled = true
			sp.Host, sp.Port = h, p
			if scheme != "" {
				sp.Scheme = scheme
			} else if strings.HasPrefix(strings.ToLower(name), "https") {
				sp.Scheme = "https"
			}
			sp.Source = "Linux 环境变量 " + name
			return sp, nil
		}
	}
	return sp, nil
}

func gsettingString(schema, key string) (string, error) {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'"), nil
}

func gsettingInt(schema, key string) (int, error) {
	s, err := gsettingString(schema, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

// ── 通用辅助 ───────────────────────────────────────────────────────────

// readJSONFile 读一个 JSON 文件（用于解析 JSON 格式的配置文件）。
func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// homeDir 返回用户主目录。
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// executableExists 判断路径是否是可执行文件。
func executableExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// fileExists 判断路径是否存在且是普通文件。
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// dirExists 判断路径是否是目录。
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// unusedPathJoin 保留 filepath 引用。
var _ = filepath.Join
