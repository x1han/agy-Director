package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/user/agy-untun/internal/channel"
	"github.com/user/agy-untun/internal/discovery"
)

// runChannels 是 channels 子命令的入口。
func runChannels(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		return channelsList()
	case "scan":
		return channelsScan()
	case "add":
		return channelsAdd(args[1:])
	case "remove", "rm":
		return channelsRemove(args[1:])
	case "test":
		return channelsTest(args[1:])
	default:
		return fmt.Errorf("未知子命令: %s", args[0])
	}
}

// ─────────────────────────────────────────────────────────────────
// 配置存储
// ─────────────────────────────────────────────────────────────────

// configFile 返回通道配置文件的路径。
// 采用 XDG 规范：Windows 用 %APPDATA%，其余用 $XDG_CONFIG_HOME 或 ~/.config。
func configFile() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		dir := filepath.Join(appdata, "agy-untun")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(dir, "channels.json"), nil
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".config")
		}
		dir := filepath.Join(base, "agy-untun")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(dir, "channels.json"), nil
	}
}

// loadConfig 读取通道配置。文件不存在时返回空配置，不算错误。
func loadConfig() (*channelConfig, error) {
	path, err := configFile()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &channelConfig{}, nil
		}
		return nil, err
	}
	cfg := &channelConfig{}
	if err := decodeJSON(b, cfg); err != nil {
		return nil, fmt.Errorf("配置文件 %s 解析失败: %w", path, err)
	}
	return cfg, nil
}

// saveConfig 写回通道配置。
func saveConfig(cfg *channelConfig) error {
	path, err := configFile()
	if err != nil {
		return err
	}
	b, err := encodeJSON(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// channelConfig 是配置文件结构。
type channelConfig struct {
	Channels []channel.Channel `json:"channels"`
	// Preferred 是默认通道名。
	Preferred string `json:"preferred,omitempty"`
}

// ─────────────────────────────────────────────────────────────────
// 子命令实现
// ─────────────────────────────────────────────────────────────────

func channelsList() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Channels) == 0 {
		path, _ := configFile()
		fmt.Printf("还没有配置任何通道。\n配置文件: %s\n\n", path)
		fmt.Println("用法:")
		fmt.Println("  agy-untun channels add v2rayN http://127.0.0.1:10808")
		fmt.Println("  agy-untun channels scan")
		return nil
	}
	fmt.Println("已配置的通道:")
	for _, c := range cfg.Channels {
		mark := " "
		if c.Name == cfg.Preferred {
			mark = "*"
		}
		fmt.Printf(" %s %-16s %s", mark, c.Name, c.Addr())
		if c.Scheme != "" {
			fmt.Printf("  (%s)", c.Scheme)
		}
		if c.LAN {
			fmt.Printf("  [局域网监听]")
		}
		fmt.Println()
	}
	return nil
}

func channelsAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("用法: agy-untun channels add <名> <URL>")
	}
	name := args[0]
	ch, err := channelFromURL(args[1])
	if err != nil {
		return err
	}
	ch.Name = name

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	replaced := false
	for i := range cfg.Channels {
		if cfg.Channels[i].Name == name {
			cfg.Channels[i] = ch
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.Channels = append(cfg.Channels, ch)
	}
	if cfg.Preferred == "" {
		cfg.Preferred = name
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("已添加通道 %s -> %s\n", name, ch.URL())
	return nil
}

func channelsRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: agy-untun channels remove <名>")
	}
	name := args[0]
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	out := cfg.Channels[:0]
	removed := false
	for _, c := range cfg.Channels {
		if c.Name == name {
			removed = true
			continue
		}
		out = append(out, c)
	}
	cfg.Channels = out
	if cfg.Preferred == name {
		cfg.Preferred = ""
		if len(cfg.Channels) > 0 {
			cfg.Preferred = cfg.Channels[0].Name
		}
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("没有名为 %q 的通道", name)
	}
	fmt.Printf("已移除通道 %s\n", name)
	return nil
}

func channelsTest(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: agy-untun channels test <名|URL>")
	}
	target := args[0]

	ch, ok := findConfiguredChannel(target)
	if !ok {
		var err error
		ch, err = channelFromURL(target)
		if err != nil {
			return err
		}
	}
	return testChannel(ch)
}

// testChannel 对单个通道做完整验证并输出细节。
func testChannel(ch channel.Channel) error {
	fmt.Printf("测试 %s (%s)\n", ch.Addr(), ch.Scheme)

	probe := channel.ProbeTarget(ch.Host, ch.Port, 3*time.Second)
	if probe.Err != nil {
		return fmt.Errorf("端点不可用: %w", probe.Err)
	}
	fmt.Printf("  协议  http=%v  socks5=%v", probe.HTTPProxy, probe.SOCKS5)
	if probe.LAN {
		fmt.Printf("  [注意: 非回环监听]")
	}
	fmt.Println()

	ch.Scheme = probe.Best()
	for _, u := range channel.ProbeURLs {
		rr := channel.CheckReachability(ch, u, 10*time.Second)
		if rr.Err != nil {
			fmt.Printf("  ✗ %s\n     %v\n", u, rr.Err)
			continue
		}
		origin := "来源未知"
		if rr.FromGoogle {
			origin = "来自 Google"
		}
		fmt.Printf("  %s %s\n     HTTP %d  %s  %dms\n",
			mark(rr.Err == nil && rr.Status < 500 && rr.Status != 407),
			u, rr.Status, origin, rr.Latency.Milliseconds())
	}
	return nil
}

func channelsScan() error {
	fmt.Println("扫描本机监听端口，寻找 http/socks5 代理端点...")
	candidates, err := discoverChannels()
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("没有发现任何代理端点。")
		return nil
	}
	fmt.Printf("\n发现 %d 个候选:\n", len(candidates))
	for _, c := range candidates {
		fmt.Printf("  %-22s %s", c.Addr(), schemeList(c))
		if c.LAN {
			fmt.Printf("  [局域网监听，建议跳过]")
		}
		fmt.Println()
	}
	return nil
}

// schemeList 列出通道支持的全部协议。
func schemeList(c channel.Channel) string {
	if len(c.Protocols) == 0 {
		if c.Scheme == "" {
			return "?"
		}
		return string(c.Scheme)
	}
	parts := make([]string, 0, len(c.Protocols))
	for _, s := range c.Protocols {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, "+")
}

// discoverChannels 扫描本机，识别出所有像代理的端点。
//
// 三级来源（按置信度从高到低）：
//
//  1. **系统代理设置** —— Windows 注册表 / macOS scutil / Linux gsettings。
//     这是最准的来源：绝大多数用 v2rayN / Clash 的用户都开着系统代理，
//     端口就写在系统设置里，不需要猜。只读，绝不写。
//  2. **本机监听表** —— 用系统原生接口读（Windows GetExtendedTcpTable、
//     Linux /proc/net/tcp、macOS lsof），不做任何端口扫描。
//  3. **常见代理端口白名单** —— 前两条都读不到时的兜底，
//     最多 19 个端口，逐个做协议探测。
//
// 排序规则：回环地址优先于局域网地址。
// 局域网监听的端口（如 v2rayN 的 10810，listen=0.0.0.0）意味着
// 探测流量会经过非回环接口，既慢又可能被同网段观察到，
// 因此只作为候选列出，不优先使用。
func discoverChannels() ([]channel.Channel, error) {
	var out []channel.Channel
	seen := map[string]bool{}

	add := func(c channel.Channel, src string) {
		key := fmt.Sprintf("%s:%d", c.Host, c.Port)
		if seen[key] {
			return
		}
		seen[key] = true
		c.Name = src
		out = append(out, c)
	}

	// ── 来源 1：系统代理 ────────────────────────────────────────────
	if sp, err := discovery.ReadSystemProxy(); err == nil && sp.Enabled && sp.Port > 0 {
		ch := channel.Channel{
			Host:   sp.Host,
			Port:   sp.Port,
			Scheme: channel.Scheme(sp.Scheme),
		}
		if ch.Scheme != channel.SchemeHTTP && ch.Scheme != channel.SchemeSOCKS5 {
			ch.Scheme = channel.SchemeHTTP
		}
		add(ch, sp.Source)
	}

	// ── 来源 2：本机监听表 ──────────────────────────────────────────
	if ports, err := discovery.ListenPorts(); err == nil {
		for _, p := range ports {
			// 跳过明显的系统端口，降低无谓探测。
			if isSystemPort(p.Port) {
				continue
			}
			probe := channel.ProbeTarget(p.Host, p.Port, 1200*time.Millisecond)
			if !probe.HTTPProxy && !probe.SOCKS5 {
				continue
			}
			add(channel.Channel{
				Host:      p.Host,
				Port:      p.Port,
				Scheme:    probe.Best(),
				Protocols: probe.Schemes(),
				LAN:       probe.LAN,
			}, "本机监听表")
		}
	}

	// ── 来源 3：常见端口白名单（兜底）──────────────────────────────
	// 只有前两个来源一个都没产出时才有意义。
	if len(out) == 0 {
		for _, port := range discovery.CommonProxyPorts {
			probe := channel.ProbeTarget("127.0.0.1", port, 1200*time.Millisecond)
			if !probe.HTTPProxy && !probe.SOCKS5 {
				continue
			}
			add(channel.Channel{
				Host:      "127.0.0.1",
				Port:      port,
				Scheme:    probe.Best(),
				Protocols: probe.Schemes(),
			}, "常见代理端口")
		}
	}

	// 回环优先
	sort.SliceStable(out, func(i, j int) bool {
		return !out[i].LAN && out[j].LAN
	})
	return out, nil
}

// isSystemPort 判断是否属于常见的系统/开发服务端口。
func isSystemPort(port int) bool {
	switch {
	case port < 1024:
		return true
	case port >= 49152 && port <= 65535:
		// 动态/临时端口区，多半是本机的客户端连接。
		return true
	}
	switch port {
	case 5353, 8080, 8081, 8888, 9090, 9418, 4040, 2222, 6379, 27017:
		return true
	}
	return false
}

// findConfiguredChannel 按名字或 URL 找配置里的通道。
func findConfiguredChannel(name string) (channel.Channel, bool) {
	cfg, err := loadConfig()
	if err != nil {
		return channel.Channel{}, false
	}
	for _, c := range cfg.Channels {
		if c.Name == name || c.Addr() == name {
			return c, true
		}
	}
	// 没给名字时用首选通道。
	if name == "" && cfg.Preferred != "" {
		for _, c := range cfg.Channels {
			if c.Name == cfg.Preferred {
				return c, true
			}
		}
	}
	return channel.Channel{}, false
}

// mark 返回对勾或叉。
func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

// ─────────────────────────────────────────────────────────────────
// 交互辅助
// ─────────────────────────────────────────────────────────────────

// confirm 问一个 yes/no 问题，默认是。
func confirm(question string) bool {
	fmt.Printf("%s [Y/n] ", question)
	var answer string
	_, _ = fmt.Fscanln(os.Stdin, &answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "" || answer == "y" || answer == "yes"
}

// promptIndex 让用户从 1..n 里选一个，返回 0-based 索引。
func promptIndex(n int, question string) (int, error) {
	fmt.Printf("%s [1-%d] ", question, n)
	var answer string
	if _, err := fmt.Fscanln(os.Stdin, &answer); err != nil {
		return 0, fmt.Errorf("读取输入失败: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return 0, nil
	}
	var idx int
	if _, err := fmt.Sscanf(answer, "%d", &idx); err != nil || idx < 1 || idx > n {
		return 0, fmt.Errorf("无效的选择: %q", answer)
	}
	return idx - 1, nil
}

// isLinux 判断当前平台。
func isLinux() bool { return runtime.GOOS == "linux" }
