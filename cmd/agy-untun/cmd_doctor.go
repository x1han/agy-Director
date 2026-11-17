package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/user/agy-untun/internal/channel"
	"github.com/user/agy-untun/internal/discovery"
	"github.com/user/agy-untun/internal/launcher"
	"github.com/user/agy-untun/internal/procs"
	"github.com/user/agy-untun/internal/shellenv"
)

// runDoctor 输出完整的诊断报告。
func runDoctor(args []string) error {
	// --processes 只列出「会被终止的进程」，不做任何终止动作。
	// 用来在真跑之前确认判据没误伤（曾误伤过 Chrome 的渲染进程）。
	if len(args) > 0 && args[0] == "--processes" {
		return listKillTargets()
	}

	fmt.Println("agy-untun 诊断")
	fmt.Println(strings.Repeat("=", 60))

	section("运行环境")
	fmt.Printf("  平台        %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  Go 版本     %s\n", runtime.Version())

	section("Antigravity 桌面版")
	ide, ideOK := discovery.FindTarget(discovery.TargetIDE)
	if ideOK {
		fmt.Printf("  ✓ 已找到    %s\n", ide.Path)
		if ide.Version != "" {
			fmt.Printf("    版本      %s\n", ide.Version)
		}
	} else {
		fmt.Println("  ✗ 未找到")
		for _, c := range discovery.IDECandidates() {
			fmt.Printf("      试过 %s\n", c)
		}
	}

	section("agy CLI")
	cli, cliOK := discovery.FindTarget(discovery.TargetCLI)
	if cliOK {
		fmt.Printf("  ✓ 已找到    %s\n", cli.Path)
		if cli.Version != "" {
			fmt.Printf("    版本      %s\n", cli.Version)
		}
	} else {
		fmt.Println("  ✗ 未找到")
		for _, c := range discovery.CLICandidates() {
			fmt.Printf("      试过 %s\n", c)
		}
	}

	section("运行中的实例")
	if inst, running := launcher.CheckRunning(2 * time.Second); running {
		fmt.Printf("  ⚠ 桌面版正在运行（端口 %d%s）\n", inst.Port, versionSuffix(inst.Version))
		fmt.Println("    已有实例不会继承新的代理环境变量，")
		fmt.Println("    启动前必须先退出它。")
	} else {
		fmt.Println("  ✓ 没有运行中的桌面版实例")
	}
	if cliRunning() {
		fmt.Println("  ⚠ agy CLI 正在运行")
	} else {
		fmt.Println("  ✓ 没有运行中的 agy CLI")
	}

	section("代理环境变量")
	printEnvProxy("HTTP_PROXY", os.Getenv("HTTP_PROXY"))
	printEnvProxy("HTTPS_PROXY", os.Getenv("HTTPS_PROXY"))
	printEnvProxy("ALL_PROXY", os.Getenv("ALL_PROXY"))
	printEnvProxy("NO_PROXY", os.Getenv("NO_PROXY"))

	section("Shell 环境冲突（仅 macOS/Linux 有效）")
	if runtime.GOOS == "windows" {
		fmt.Println("  · Windows 上 Antigravity 不读登录 shell 的环境变量，无此冲突")
	} else {
		conflicts := shellenv.Detect(5 * time.Second)
		if len(conflicts) == 0 {
			fmt.Println("  ✓ 未发现冲突")
		} else {
			for _, c := range conflicts {
				fmt.Printf("  ⚠ %s\n", c.String())
			}
			fmt.Println("    Antigravity 会用登录 shell 的值覆盖启动器注入的值，")
			fmt.Println("    导致通道失效。需要你手动从 rc 文件里移除这些 export。")
			fmt.Println("    （agy-untun 绝不修改你的 shell 配置文件。）")
		}
	}

	section("本机代理端点")
	candidates, err := discoverChannels()
	if err != nil {
		fmt.Printf("  扫描失败: %v\n", err)
	} else if len(candidates) == 0 {
		fmt.Println("  未发现 http/socks5 端点")
	} else {
		for _, c := range candidates {
			fmt.Printf("  ✓ %-22s %s", c.Addr(), schemeList(c))
			if c.LAN {
				fmt.Printf("  [局域网监听]")
			}
			fmt.Println()
		}
	}

	section("配置的通道")
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("  读取失败: %v\n", err)
	} else if len(cfg.Channels) == 0 {
		path, _ := configFile()
		fmt.Printf("  还没有配置任何通道（%s）\n", path)
	} else {
		for _, c := range cfg.Channels {
			mark := " "
			if c.Name == cfg.Preferred {
				mark = "*"
			}
			ok, detail := channel.VerifyChannel(c, 8*time.Second)
			status := "✗"
			if ok {
				status = "✓"
			}
			fmt.Printf(" %s %-16s %-22s %s\n", mark, c.Name, c.Addr(), status)
			if !ok {
				fmt.Printf("     %s\n", detail)
			}
		}
	}

	fmt.Println()
	return nil
}

// listKillTargets 列出 terminateIDE 会终止的进程，不动手。
//
// 这是安全闸门：判据一旦写宽（比如只按 --type=renderer 匹配），
// 用户的 Chrome / Edge 渲染进程就会被一起杀掉。
// 上线前先跑这个命令，确认列表里只有 Antigravity 相关的。
func listKillTargets() error {
	all, err := procs.List()
	if err != nil {
		return fmt.Errorf("读取进程列表失败: %w", err)
	}

	fmt.Println("将会被终止的进程（桌面版 + Go 后端）:")
	fmt.Println(strings.Repeat("-", 60))
	n := 0
	for _, p := range all {
		if p.IsSidecar() {
			continue
		}
		if !p.IsIdeProcess() {
			continue
		}
		fmt.Printf("  %s\n", p.String())
		n++
	}
	if n == 0 {
		fmt.Println("  （无）")
	}

	fmt.Println()
	fmt.Println("将被跳过的进程（agy CLI 的 sidecar —— 不属于桌面版）:")
	fmt.Println(strings.Repeat("-", 60))
	m := 0
	for _, p := range all {
		if p.IsSidecar() {
			fmt.Printf("  %s\n", p.String())
			m++
		}
	}
	if m == 0 {
		fmt.Println("  （无）")
	}
	return nil
}

// section 打印一个分节标题。
func section(title string) {
	fmt.Printf("\n%s\n", title)
}

// printEnvProxy 打印一个代理环境变量。
func printEnvProxy(name, value string) {
	if value == "" {
		fmt.Printf("  %-12s （未设置）\n", name)
		return
	}
	fmt.Printf("  %-12s %s\n", name, value)
}

// cliRunning 检查是否有 agy 进程在跑。
func cliRunning() bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq agy.exe").Output()
		return err == nil && strings.Contains(string(out), "agy.exe")
	}
	out, err := exec.Command("pgrep", "-x", "agy").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// readShellProxyConflicts 已移到 internal/shellenv。
// 保留这个名字是为了不破坏 doctor 的其它引用点；
// 实现完全一致：只读检测，绝不修改用户的 shell 配置文件。
func readShellProxyConflicts() []string {
	conflicts := shellenv.Detect(5 * time.Second)
	out := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, c.String())
	}
	return out
}

// runWrap 在代理环境变量下运行任意命令。
// 这给「让 agy 命令永远走代理」提供了基础：
// 可以生成 shell alias `agy='agy-untun wrap agy'`。
func runWrap(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: agy-untun wrap <命令> [参数...]")
	}

	ch, err := selectChannel(&launchOptions{})
	if err != nil {
		return err
	}
	if ch == nil {
		return fmt.Errorf("没有可用的代理通道")
	}

	fmt.Printf("通道 %s\n", ch.String())
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = launcher.BuildEnv(*ch, nil)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// isIdeName 判断进程名是否属于 Antigravity 桌面版 / 后端。
//
// 这是 terminate 的**第一道**过滤，命令行判据是第二道。
// 两道都必须过——只看名字会误杀 agy CLI 的同名 sidecar，
// 只看命令行会把 Chrome / Edge 的渲染进程一起杀掉
// （它们的命令行也带 --type=renderer）。
func isIdeName(name string) bool {
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "antigravity", "language_server":
		return true
	}
	return false
}

// ideProcessNames 是桌面版在不同平台上的进程名候选。
//
//   - Windows: Antigravity.exe（Electron 打包产物）
//   - macOS:   Antigravity（.app 里的可执行文件）
//   - Linux:   antigravity（AppImage / deb / tarball）
//
// 还要带上 Go 后端：language_server.exe 是**独立的进程树**，
// 杀掉 Electron 不会自动带走它，而它才是真正发流式请求的那个进程。
// 不杀掉它的话，旧的直连后端会继续挂在后台。
var ideProcessNames = []string{
	"Antigravity", "antigravity",
	"language_server", "language_server.exe",
}

// terminateIDE 终止正在运行的桌面版实例。
//
// 分三类处理：
//  1. **桌面版 Electron 进程**（命令行含 --user-data-dir / --type=gpu-process
//     / --proxy-server）—— 这些必须全部杀掉，只杀主进程会留下渲染进程，
//     而 requestSingleInstanceLock 仍被占用。
//  2. **Go 后端 language_server** —— 独立进程树，必须单独杀。
//  3. **agy CLI 的 Node sidecar**（父进程是 agy-node.cmd）——
//     **不杀**，那是用户的 CLI 会话，与桌面版无关。
//
// 容忍"进程已经不在"的情况：taskkill 对找不到的进程返回 exit 128，
// pkill 对没有匹配进程返回 exit 1。这两种都不该算失败。
//
// 失败时把**精确的 PID** 报出来，让用户能自己手动结束。
func terminateIDE(inst launcher.RunningInstance) error {
	all, err := procs.List()
	if err != nil {
		return fallbackTerminate()
	}

	var desktop, backend []procs.Proc
	for _, p := range all {
		name := strings.ToLower(p.Name)
		switch {
		case p.IsSidecar():
			// agy CLI 的 sidecar —— 不动。
		case strings.Contains(name, "language_server"):
			backend = append(backend, p)
		case isIdeName(name) && p.IsIdeProcess():
			// 名字 + 命令行双判据，缺一不可。
			// 只看命令行会把 Chrome / Edge 的渲染进程一起杀掉
			// （它们的命令行也带 --type=renderer）——
			// 这是实测踩到过的坑：曾经一启动 terminate 就把 Chrome 干掉了。
			desktop = append(desktop, p)
		}
	}

	targets := append(desktop, backend...)
	if len(targets) == 0 {
		return fallbackTerminate()
	}

	var failures []string
	for _, p := range targets {
		if err := killPID(p.PID); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", p.String(), err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("终止失败:\n  %s\n\n请手动结束这些进程:\n%s",
			strings.Join(failures, "\n  "), formatProcs(targets))
	}
	return nil
}

// fallbackTerminate 在拿不到进程列表时按镜像名杀。
//
// ⚠️ 代价明确：Windows 上 Antigravity.exe 这个镜像名**同时**是
// agy CLI 的 Node sidecar 的名字，所以 taskkill /IM 会连带干掉
// 用户的 CLI 会话。因此这条路只在进程列表不可用时才走
// （正常路径不会到这里），并且**必须**先打印一条警告。
func fallbackTerminate() error {
	fmt.Fprintln(os.Stderr,
		"⚠ 无法读取进程列表，退回到按镜像名终止。")
	fmt.Fprintln(os.Stderr,
		"  这可能会连带结束 agy CLI 的同名 sidecar 进程。")

	if runtime.GOOS == "windows" {
		err := exec.Command("taskkill", "/F", "/IM", "Antigravity.exe").Run()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
				return nil
			}
			return err
		}
		return nil
	}

	err := exec.Command("pkill", "-f", "Antigravity.app").Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}
	return nil
}

// killPID 精确终止一个 PID。
func killPID(pid int) error {
	if runtime.GOOS == "windows" {
		err := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
				return nil // 已经没了
			}
			return err
		}
		return nil
	}
	err := exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil // 已经没了
		}
		return err
	}
	return nil
}

// formatProcs 把进程列表渲染成给用户看的多行文本。
func formatProcs(list []procs.Proc) string {
	var b strings.Builder
	for _, p := range list {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}

// decodeJSON 是 json.Unmarshal 的薄封装，便于以后换实现。
func decodeJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

// encodeJSON 是 json.MarshalIndent 的薄封装。
func encodeJSON(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// contextUnused 保留 context 引用，避免导入被裁掉。
var _ = context.Background
