package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/agy-untun/internal/channel"
	"github.com/user/agy-untun/internal/discovery"
	"github.com/user/agy-untun/internal/launcher"
	"github.com/user/agy-untun/internal/shellenv"
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

	// 4) Unix 上检查登录 shell 里的代理变量冲突。
	//
	//    Antigravity 会用登录 shell 的环境覆盖我们注入的代理变量
	//    （dist/languageServer.js: `const env = { ...process.env, ...loginShellEnv }`），
	//    且没有任何开关能跳过这一步。
	//    检测到冲突就**红字报错拦住**，把变量名告诉用户让他自己改——
	//    我们绝不触碰用户的 .bashrc/.zshrc/.profile。
	if conflicts := shellenv.Detect(5 * time.Second); len(conflicts) > 0 {
		fmt.Fprint(os.Stderr, shellenv.Report(conflicts))
		return fmt.Errorf("登录 shell 里存在会覆盖代理的变量，已中止启动")
	}

	// 5) 启动
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
	//
	// 追加 Chromium 原生代理参数：
	//   --proxy-server       让 Electron 壳自己的网络栈（渲染进程、
	//                       扩展、DevTools 下载）也走代理。
	//                       Go 后端不吃这个参数，它只看环境变量。
	//   --proxy-bypass-list  与 NO_PROXY 对应，避免把本地开发端口
	//                       （如 127.0.0.1:3000 之类）也代理掉。
	//
	// 两个参数都带上，桌面版才算"整体走代理"——
	// 只带环境变量的话壳是直连，只带参数的话后端是直连（实测）。
	launchArgs := append([]string{}, opts.passArgs...)
	launchArgs = append(launchArgs,
		"--proxy-server="+ch.URL(),
		"--proxy-bypass-list="+chromiumBypassList(*ch),
	)

	if _, err := launcher.Launch(context.Background(), launcher.Options{
		Channel: *ch,
		Target:  target.Path,
		Args:    launchArgs,
		Detached: true,
	}); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	fmt.Println("\n已启动。")
	return nil
}

// chromiumBypassList 生成 --proxy-bypass-list 的值。
//
// Chromium 用分号分隔，Go 的 NO_PROXY 用逗号分隔，格式不同要转。
// Chromium 支持 "<local>" 表示所有不含点的主机名（对应内网）。
func chromiumBypassList(ch channel.Channel) string {
	noProxy := channel.JoinNoProxy(os.Getenv("NO_PROXY"))
	parts := strings.Split(noProxy, ",")
	out := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Go 的无端口写法与 Chromium 一致，直接用。
		out = append(out, p)
	}
	// 通道自己所在的回环端口不能被代理（代理服务器本身）。
	if ch.Host != "" {
		out = append(out, ch.Addr())
	}
	return strings.Join(out, ";")
}

// verifyAll 并发验证所有候选通道，返回可用的那些。
//
// 并发而不是串行：每个候选要真实请求 2 个 Google 端点，
// 单个候选最长 16s；本机监听表里通常有几十个端口，
// 串行会让"扫描"变成几分钟的等待。
// 并发后总耗时 ≈ 最慢的单个候选。
func verifyAll(candidates []channel.Channel, timeout time.Duration) ([]channel.Channel, error) {
	type result struct {
		ch     channel.Channel
		ok     bool
		detail string
	}

	results := make([]result, len(candidates))
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
			results[i] = result{ch: c, ok: ok, detail: detail}
		}(i, c)
	}
	wg.Wait()

	var usable []channel.Channel
	for _, r := range results {
		if r.ok {
			fmt.Printf("  ✓ %-32s %s\n", r.ch.Addr(), r.detail)
			usable = append(usable, r.ch)
		} else {
			fmt.Printf("  ✗ %-32s %s\n", r.ch.Addr(), r.detail)
		}
	}
	return usable, nil
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
	// 并发执行：每个候选最多 8s x 2 个端点，串行会让几十个候选
	// 把启动拖到几分钟；并发后总耗时约等于单个候选的耗时。
	usable, err := verifyAll(candidates, 8*time.Second)
	if err != nil {
		return nil, err
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
