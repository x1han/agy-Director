package tui

import (
	"fmt"
	"runtime"
	"time"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/config"
	"github.com/x1han/agy-Director/internal/discovery"
	"github.com/x1han/agy-Director/internal/logging"
	"github.com/x1han/agy-Director/internal/scan"
)

// verifyTimeout 是单个通道做完整连通性验证的超时。
//
// 与 CLI 路径保持一致：每个候选要真实请求 2 个 Google 端点。
const verifyTimeout = 8 * time.Second

// scan 执行一次完整的扫描，返回结果。
//
// 这一步比较慢（要真实联网），所以 TUI 里是在 tea.Cmd 里跑的，
// 界面在扫描期间显示 spinner 而不是卡住。
func doScan() ScanResult {
	var res ScanResult

	// ── 目标 ────────────────────────────────────────────────────────
	targets, err := scanTargets()
	if err != nil {
		res.TargetErr = err.Error()
		logging.Error("扫描启动目标失败", "err", err)
	} else if len(targets) == 0 {
		res.TargetErr = "未找到 Antigravity 桌面版，也未找到 agy CLI"
		logging.Warn("未找到任何启动目标")
	}
	res.Targets = targets
	logging.Info("扫描启动目标完成", "数量", len(targets))

	// ── 通道 ────────────────────────────────────────────────────────
	channels, cerr := scanChannels()
	if cerr != nil {
		res.ChannelErr = cerr.Error()
		logging.Error("扫描代理通道失败", "err", cerr)
	} else if len(channels) == 0 {
		res.ChannelErr = "未找到可用的代理通道"
		logging.Warn("未找到任何可用通道")
	}
	res.Channels = channels
	logging.Info("扫描代理通道完成", "可用数量", len(channels))

	return res
}

// scanTargets 查找启动目标，按平台优先级排序。
//
// Linux 上优先 CLI（路径固定），其余平台优先桌面版。
func scanTargets() ([]discovery.Target, error) {
	order := []discovery.TargetKind{discovery.TargetIDE, discovery.TargetCLI}
	if runtime.GOOS == "linux" {
		order = []discovery.TargetKind{discovery.TargetCLI, discovery.TargetIDE}
	}

	var out []discovery.Target
	for _, k := range order {
		if t, ok := discovery.FindTarget(k); ok {
			out = append(out, t)
			logging.Info("找到启动目标", "类型", string(k), "路径", t.Path, "版本", t.Version)
		}
	}
	return out, nil
}

// scanChannels 扫描并验证本机的代理通道。
//
// 优先用配置里的通道（省掉整个扫描过程），
// 找不到才真的去扫本机。
func scanChannels() ([]channel.Channel, error) {
	// 配置里的通道。
	if ch, ok := config.Find(""); ok {
		logging.Info("使用配置里的通道", "名称", ch.Name, "地址", ch.Addr())
		ok, detail := channel.VerifyChannel(ch, verifyTimeout)
		if ok {
			logging.Info("配置通道可用", "地址", ch.Addr(), "详情", detail)
			return []channel.Channel{ch}, nil
		}
		logging.Warn("配置里的通道不可用", "地址", ch.Addr(), "详情", detail)
	}

	// 扫本机。
	candidates, err := scan.Discover()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("本机没有发现任何 http/socks5 代理端点")
	}
	logging.Info("发现候选通道", "数量", len(candidates))

	results := scan.Verify(candidates, verifyTimeout)
	usable := scan.Usable(results)
	if len(usable) == 0 {
		return nil, fmt.Errorf("扫描到 %d 个代理端点，但没有一个能连通 Antigravity 的后端端点", len(candidates))
	}
	return usable, nil
}
