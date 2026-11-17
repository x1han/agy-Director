package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/user/agy-untun/internal/channel"
	"github.com/user/agy-untun/internal/discovery"
	"github.com/user/agy-untun/internal/launcher"
)

// launchOptions 是 launch 子命令的解析结果。
type launchOptions struct {
	forceIDE bool
	forceCLI bool
	channel   string
	proxyURL  string
	scan      bool
	yes       bool
	passArgs  []string
}

// parseLaunchArgs 解析 launch 的命令行参数。
func parseLaunchArgs(args []string) (*launchOptions, error) {
	o := &launchOptions{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			o.passArgs = append(o.passArgs, args[i+1:]...)
			return o, nil
		case a == "--ide":
			o.forceIDE = true
		case a == "--cli":
			o.forceCLI = true
		case a == "--scan":
			o.scan = true
		case a == "--yes" || a == "-y":
			o.yes = true
		case a == "--channel":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--channel 需要一个参数")
			}
			i++
			o.channel = args[i]
		case a == "--proxy":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--proxy 需要一个参数")
			}
			i++
			o.proxyURL = args[i]
		default:
			if strings.HasPrefix(a, "-") {
				return nil, fmt.Errorf("未知选项: %s", a)
			}
			o.passArgs = append(o.passArgs, a)
		}
	}
	return o, nil
}

// runLaunch 是 launch 子命令的入口。
func runLaunch(args []string) error {
	opts, err := parseLaunchArgs(args)
	if err != nil {
		return err
	}

	// 1) 选通道
	ch, err := selectChannel(opts)
	if err != nil {
		return err
	}
	if ch == nil {
		return fmt.Errorf("没有可用的代理通道")
	}

	// 2) 选启动目标
	target, err := selectTarget(opts)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("找不到 Antigravity 桌面版，也找不到 agy CLI")
	}

	// 3) 桌面版单实例守卫
	//    Electron 有 requestSingleInstanceLock()，第二次启动会
	//    把 argv 转发给已有实例并立刻退出（丢掉新 env）。
	//    所以已有实例在跑时必须中止，否则用户以为走了代理，
	//    实际用的是那个没代理的旧实例。
	if target.Kind == discovery.TargetIDE {
		if inst, running := launcher.CheckRunning(2 * time.Second); running {
			fmt.Printf("Antigravity 桌面版已在运行（端口 %d%s）。\n",
				inst.Port, versionSuffix(inst.Version))
			fmt.Println("已有实例不会继承新的代理环境变量，需要先退出它。")
			if !opts.yes {
				if !confirm("现在要终止已有实例并重新启动吗？") {
					return fmt.Errorf("已取消")
				}
			}
			if err := terminateIDE(inst); err != nil {
				return fmt.Errorf("终止已有实例失败: %w", err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	// 4) 启动
	fmt.Printf("\n启动 %s\n", target.Path)
	fmt.Printf("通道  %s\n", ch.String())
	fmt.Printf("代理  %s\n", ch.URL())

	if target.Kind == discovery.TargetCLI {
		// CLI：前台运行，stdin/stdout 直连终端，用户可以正常对话。
		fmt.Println()
		return launcher.RunInteractive(context.Background(), launcher.Options{
			Channel: *ch,
			Target:  target.Path,
			Args:    opts.passArgs,
		})
	}

	// 桌面版：脱离启动，父进程退出不影响它。
	if _, err := launcher.Launch(context.Background(), launcher.Options{
		Channel: *ch,
		Target:  target.Path,
		Args:    opts.passArgs,
		Detached: true,
	}); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	fmt.Println("\n已启动。")
	return nil
}

// selectChannel 按优先级选一个可用通道。
func selectChannel(opts *launchOptions) (*channel.Channel, error) {
	// 显式 --proxy 最高优先。
	if opts.proxyURL != "" {
		ch, err := channelFromURL(opts.proxyURL)
		if err != nil {
			return nil, err
		}
		return &ch, nil
	}

	// 配置里的通道。
	if !opts.scan {
		if ch, ok := findConfiguredChannel(opts.channel); ok {
			return &ch, nil
		}
	}

	// 扫描本机。
	candidates, err := discoverChannels()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("本机没有发现任何 http/socks5 代理端点")
	}

	// 逐个做完整连通性验证（真实请求 Google 端点）。
	var usable []channel.Channel
	for _, c := range candidates {
		ok, detail := channel.VerifyChannel(c, 8*time.Second)
		if ok {
			fmt.Printf("  ✓ %-32s %s\n", c.Addr(), detail)
			usable = append(usable, c)
		} else {
			fmt.Printf("  ✗ %-32s %s\n", c.Addr(), detail)
		}
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("扫描到 %d 个代理端点，但没有一个能连通 Antigravity 的后端端点", len(candidates))
	}

	if opts.yes {
		return &usable[0], nil
	}
	if len(usable) == 1 {
		return &usable[0], nil
	}

	fmt.Println()
	for i, c := range usable {
		fmt.Printf("  [%d] %s\n", i+1, c.String())
	}
	choice, err := promptIndex(len(usable), "选哪个通道？")
	if err != nil {
		return nil, err
	}
	return &usable[choice], nil
}

// selectTarget 决定启动桌面版还是 CLI。
func selectTarget(opts *launchOptions) (*discovery.Target, error) {
	if opts.forceIDE {
		if t, ok := discovery.FindTarget(discovery.TargetIDE); ok {
			return &t, nil
		}
		return nil, fmt.Errorf("找不到 Antigravity 桌面版")
	}
	if opts.forceCLI {
		if t, ok := discovery.FindTarget(discovery.TargetCLI); ok {
			return &t, nil
		}
		return nil, fmt.Errorf("找不到 agy CLI")
	}

	// 自动：Linux 上优先 CLI（路径固定），其余平台优先桌面版。
	order := []discovery.TargetKind{discovery.TargetIDE, discovery.TargetCLI}
	if isLinux() {
		order = []discovery.TargetKind{discovery.TargetCLI, discovery.TargetIDE}
	}

	var found []discovery.Target
	for _, k := range order {
		if t, ok := discovery.FindTarget(k); ok {
			found = append(found, t)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("找不到 Antigravity 桌面版，也找不到 agy CLI")
	}
	if len(found) == 1 {
		return &found[0], nil
	}

	if opts.yes {
		return &found[0], nil
	}

	fmt.Println()
	for _, t := range found {
		fmt.Printf("  %s\n", t.String())
	}
	fmt.Println()
	fmt.Println("  桌面版与 CLI 都可用。")
	fmt.Println("  [1] 桌面版 Antigravity")
	fmt.Println("  [2] agy CLI")
	choice, err := promptIndex(2, "启动哪个？")
	if err != nil {
		return nil, err
	}
	return &found[choice], nil
}

// channelFromURL 把 "http://127.0.0.1:10808" 解析成 Channel。
func channelFromURL(raw string) (channel.Channel, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return channel.Channel{}, fmt.Errorf("代理地址格式错误: %w", err)
	}
	scheme := channel.Scheme(u.Scheme)
	switch scheme {
	case channel.SchemeHTTP, channel.SchemeSOCKS5:
	default:
		return channel.Channel{}, fmt.Errorf("不支持的代理协议 %q（仅 http / socks5）", u.Scheme)
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return channel.Channel{}, fmt.Errorf("代理地址缺少端口: %s", raw)
	}
	return channel.Channel{
		Name:   raw,
		Host:   host,
		Port:   port,
		Scheme: scheme,
	}, nil
}

// versionSuffix 给版本号加前缀，用于输出。
func versionSuffix(v string) string {
	if v == "" {
		return ""
	}
	return "，版本 " + v
}
