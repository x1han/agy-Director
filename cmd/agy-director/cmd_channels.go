package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/config"
	"github.com/x1han/agy-Director/internal/scan"
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
// 子命令实现
// ─────────────────────────────────────────────────────────────────

func channelsList() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Channels) == 0 {
		path, _ := config.File()
		fmt.Printf("还没有配置任何通道。\n配置文件: %s\n\n", path)
		fmt.Println("用法:")
		fmt.Println("  agy-Director channels add v2rayN http://127.0.0.1:10808")
		fmt.Println("  agy-Director channels scan")
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
		return fmt.Errorf("用法: agy-Director channels add <名> <URL>")
	}
	name := args[0]
	ch, err := channelFromURL(args[1])
	if err != nil {
		return err
	}
	ch.Name = name

	cfg, err := config.Load()
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
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("已添加通道 %s -> %s\n", name, ch.URL())
	return nil
}

func channelsRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: agy-Director channels remove <名>")
	}
	name := args[0]
	cfg, err := config.Load()
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
	if err := config.Save(cfg); err != nil {
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
		return fmt.Errorf("用法: agy-Director channels test <名|URL>")
	}
	target := args[0]

	ch, ok := config.Find(target)
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
	candidates, err := scan.Discover()
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
