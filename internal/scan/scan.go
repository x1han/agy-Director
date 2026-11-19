// Package scan 负责扫描本机的启动目标与代理通道。
//
// 这些逻辑原先住在 cmd 层的 package main 里，CLI 和 TUI 都要用，
// 所以提到 internal/scan，让两边共用同一份实现。
package scan

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/discovery"
)

// probeTimeout 是单端口协议探测的超时。
//
// 探测本身只是发一个 CONNECT 请求看有没有回应，1200ms 足够。
const probeTimeout = 1200 * time.Millisecond

// probeConcurrency 是协议探测的并发上限。
//
// 监听表里通常有几十个端口，串行探测最坏要等几十秒
// （几十个端口 × 1.2s）。并发之后总耗时约等于单个端口的耗时。
//
// 上限 16 是为了别把本机 socket 资源挤爆。
const probeConcurrency = 16

// Result 是一次通道验证的结果。
type Result struct {
	// Channel 是被验证的通道。
	Channel channel.Channel
	// OK 表示真的能连通 Antigravity 的后端端点。
	OK bool
	// Detail 是人类可读的结果描述（延迟、错误原因等）。
	Detail string
}

// Discover 扫描本机，识别出所有像代理的端点。
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
func Discover() ([]channel.Channel, error) {
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
		candidates := make([]channel.Channel, 0, len(ports))
		for _, p := range ports {
			// 跳过明显的系统端口，降低无谓探测。
			if isSystemPort(p.Port) {
				continue
			}
			candidates = append(candidates, channel.Channel{
				Host:      p.Host,
				Port:      p.Port,
				Scheme:    channel.SchemeHTTP, // 占位，探测后覆盖
				Protocols: nil,
				// ⚠️ probe.LAN 是从**归一化后**的 host 算的
				// （127.0.0.1 永远是回环），所以对监听表来源
				// 永远是 false。真正的"局域网可见"信息在
				// ListenPort.Wildcard 里 —— 监听地址是通配
				// （0.0.0.0 / :: / ::ffff:0.0.0.0）时为 true。
				// 两者取或：任一为真就算局域网监听。
				LAN: p.Wildcard,
			})
		}

		// 并发做协议探测。
		probed := probeAll(candidates)
		for _, c := range probed {
			add(c, "本机监听表")
		}
	}

	// ── 来源 3：常见端口白名单（兜底）──────────────────────────────
	// 只有前两个来源一个都没产出时才有意义。
	if len(out) == 0 {
		candidates := make([]channel.Channel, 0, len(discovery.CommonProxyPorts))
		for _, port := range discovery.CommonProxyPorts {
			candidates = append(candidates, channel.Channel{
				Host:   "127.0.0.1",
				Port:   port,
				Scheme: channel.SchemeHTTP,
			})
		}
		for _, c := range probeAll(candidates) {
			add(c, "常见代理端口")
		}
	}

	// 回环优先
	sort.SliceStable(out, func(i, j int) bool {
		return !out[i].LAN && out[j].LAN
	})
	return out, nil
}

// probeAll 并发探测一批端口，返回确实是代理的那些。
func probeAll(candidates []channel.Channel) []channel.Channel {
	if len(candidates) == 0 {
		return nil
	}

	type probeResult struct {
		index int
		ch    channel.Channel
		ok    bool
	}

	results := make([]probeResult, len(candidates))
	var wg sync.WaitGroup
	sem := make(chan struct{}, probeConcurrency)

	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c channel.Channel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			probe := channel.ProbeTarget(c.Host, c.Port, probeTimeout)
			if !probe.HTTPProxy && !probe.SOCKS5 {
				results[i] = probeResult{index: i, ok: false}
				return
			}
			results[i] = probeResult{
				index: i,
				ok:    true,
				ch: channel.Channel{
					Host:      c.Host,
					Port:      c.Port,
					Scheme:    probe.Best(),
					Protocols: probe.Schemes(),
					LAN:       probe.LAN || c.LAN,
				},
			}
		}(i, c)
	}
	wg.Wait()

	var out []channel.Channel
	for _, r := range results {
		if r.ok {
			out = append(out, r.ch)
		}
	}
	return out
}

// Verify 并发验证所有候选通道，返回每个候选的验证结果。
//
// 并发而不是串行：每个候选要真实请求 2 个 Google 端点，
// 单个候选最长 16s；本机监听表里通常有几十个端口，
// 串行会让"扫描"变成几分钟的等待。
// 并发后总耗时 ≈ 最慢的单个候选。
func Verify(candidates []channel.Channel, timeout time.Duration) []Result {
	results := make([]Result, len(candidates))
	var wg sync.WaitGroup
	// 并发上限：太多同时连接反而会挤爆本机 socket 资源。
	sem := make(chan struct{}, 8)

	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c channel.Channel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ok, detail := channel.VerifyChannel(c, timeout)
			results[i] = Result{Channel: c, OK: ok, Detail: detail}
		}(i, c)
	}
	wg.Wait()
	return results
}

// Usable 从验证结果里挑出可用的通道。
func Usable(results []Result) []channel.Channel {
	var out []channel.Channel
	for _, r := range results {
		if r.OK {
			out = append(out, r.Channel)
		}
	}
	return out
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
