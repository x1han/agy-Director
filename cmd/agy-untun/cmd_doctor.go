package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		// ⚠️ 比对基准用**原始**值（HostNoProxy），不用合并过的
		// EffectiveNoProxy()。doctor 此刻还没有选定通道，
		// 问的是"登录 shell 的值 vs 我自己进程里的值"；
		// 拿合并过的值当基准会把回环项算成差异，产生假冲突。
		result := shellenv.Detect(5*time.Second, shellenv.Injection{
			HTTPProxy:  os.Getenv("HTTP_PROXY"),
			HTTPSProxy: os.Getenv("HTTPS_PROXY"),
			AllProxy:   os.Getenv("ALL_PROXY"),
			NoProxy:    channel.HostNoProxy(),
		})
		if result.Inconclusive {
			// ⚠️ "没能确认"和"没发现冲突"是两回事，不能同时报。
			fmt.Printf("  ⚠ 没能读取登录 shell 的环境，无法确认有无冲突（%v）\n", result.Err)
			fmt.Println("    预检只等 5 秒，Antigravity 自己最多等 10 秒；")
			fmt.Println("    如果 rc 文件很慢，可能出现「预检放弃、Antigravity 读到了并撞上冲突」。")
		} else if len(result.Conflicts) == 0 {
			fmt.Println("  ✓ 未发现冲突")
		} else {
			for _, c := range result.Conflicts {
				fmt.Printf("  %s\n", c.String())
			}
			fmt.Println("    Antigravity 会用登录 shell 的值覆盖启动器注入的值。")
			fmt.Println("    致命档（大写名被改）必须先处理；警告档只影响第三方工具。")
			fmt.Println("    需要你手动从 rc 文件里移除这些 export。")
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
	targets := ideKillTargets(all)
	for _, p := range targets {
		fmt.Printf("  %s  %s\n", p.String(), p.ExePath)
	}
	if len(targets) == 0 {
		fmt.Println("  （无）")
	}

	fmt.Println()
	fmt.Println("将被跳过的进程（agy CLI 的 sidecar —— 不属于桌面版）:")
	fmt.Println(strings.Repeat("-", 60))
	m := 0
	for _, p := range all {
		if p.IsSidecar() {
			fmt.Printf("  %s  %s\n", p.String(), p.ExePath)
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
// ── 判据（按可靠性排序）──────────────────────────────────────────
//
//  1. **可执行文件绝对路径前缀**（首选）
//     桌面版 Antigravity.exe 装在已知目录，进程的 exe 路径落在
//     那个前缀下 → 一定是它。
//     Chrome 在 C:\Program Files\Google\Chrome\Application\，
//     Edge  在 C:\Program Files (x86)\Microsoft\Edge\Application\，
//     路径不同，**不可能误判**。
//
//  2. **进程树**（次选）
//     桌面版主进程派生的子进程（渲染 / GPU / utility），
//     沿 PPID 往上走能认出来。子进程的命令行千奇百怪，
//     但它们的祖先一定是桌面版。
//
//  3. 命令行特征（**只用来排除**，从不用来认定）
//
// ── 三条红线 ────────────────────────────────────────────────────
//
//	a. **绝不**用 `taskkill /IM Antigravity.exe` 这类按镜像名的盲杀。
//	   Antigravity.exe 同时是 agy CLI 的 Node sidecar 的名字，
//	   盲杀会干掉用户的 CLI 会话。
//	   拿不到精确的进程信息时**宁可不杀**，报错让用户手动关。
//
//	b. **绝不**用命令行特征来"认定"一个进程是 Antigravity。
//	   Chromium 的渲染进程命令行也带 --type=renderer，
//	   曾经因此把用户的 Chrome / Edge 渲染进程一起杀掉（实际发生过）。
//
//	c. agy CLI 的 Node sidecar（父进程是 agy-node.cmd）**不杀**。
//	   注意它与桌面版的 exe 路径**相同**，路径判据区分不了，
//	   必须靠这条命令行特征排除。
//
// 失败时把**精确的 PID** 报出来，让用户能自己手动结束。
func terminateIDE(inst launcher.RunningInstance) error {
	all, err := procs.List()
	if err != nil {
		// 拿不到精确的进程信息 → **不杀**。
		// 宁可让用户自己点一下，也不误杀他的浏览器或 CLI 会话。
		return fmt.Errorf(
			"无法读取进程列表（%v），已放弃自动终止。\n"+
				"请手动关闭 Antigravity 桌面版后重试。\n"+
				"（说明：Antigravity.exe 这个名字同时属于 agy CLI 的 sidecar，"+
				"按名字盲杀会误伤你的 CLI 会话，所以这里不做猜测。）", err)
	}

	targets := ideKillTargets(all)
	if len(targets) == 0 {
		return fmt.Errorf(
			"没有找到需要终止的 Antigravity 进程。\n" +
				"请手动关闭 Antigravity 桌面版后重试。")
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

// ideKillTargets 从进程列表里挑出「该杀的」那些。
//
// 判定顺序：
//  1. Go 后端 language_server —— 名字唯一，不会认错，直接要。
//  2. 桌面版主进程 —— 用 exe 路径前缀认。
//  3. 桌面版的子进程 —— 沿进程树认。
//  4. agy CLI 的 sidecar —— 排除。
func ideKillTargets(all []procs.Proc) []procs.Proc {
	// 桌面版安装目录（exe 路径前缀）。
	dirs := ideInstallDirs()

	var backend, desktop []procs.Proc
	roots := map[int]bool{}

	for _, p := range all {
		name := strings.ToLower(p.Name)
		switch {
		case strings.Contains(name, "language_server"):
			// Go 后端：独立进程树，名字唯一，直接要。
			backend = append(backend, p)

		case p.IsSidecar():
			// agy CLI 的 Node sidecar —— 排除。
			// 注意它的 exe 路径与桌面版相同，靠路径分不出来。

		case ideExeUnder(p, dirs):
			// exe 路径落在 Antigravity 安装目录下 —— 一定是它。
			desktop = append(desktop, p)
			roots[p.PID] = true
		}
	}

	// 桌面版主进程的子孙进程（渲染 / GPU / utility）。
	// 这些的 exe 路径也在安装目录下，上面已经收进来了；
	// 这里再补一遍进程树，覆盖那些 exe 路径取不到的情况
	// （例如某些进程没有权限读路径）。
	for _, p := range all {
		if p.IsSidecar() {
			continue
		}
		if !isIdeName(strings.ToLower(p.Name)) {
			continue
		}
		if ideExeUnder(p, dirs) {
			continue // 已收
		}
		if ideDescendsFrom(p, all, roots) {
			desktop = append(desktop, p)
		}
	}

	return append(desktop, backend...)
}

// ideInstallDirs 返回 Antigravity 桌面版可能的安装目录。
//
// 取自 discovery.IDECandidates 的实际安装位置，
// 也就是用户真的装了它的那几个路径。
//
// ⚠️ 只取**精确的安装目录**，不取它的父目录。
// 曾经取过 %LOCALAPPDATA%\Programs，那会把同一个目录下的
// 别的应用（WinDynamicDesktop、codync-host…）一起收进来，
// 差点误杀用户的程序。
//
// Go 后端在 resources\bin 下，是桌面版目录的子目录，
// 所以它自然被包含在前缀里——不需要单独列。
func ideInstallDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	for _, exe := range discovery.IDECandidates() {
		d := filepath.Dir(exe)
		if d == "" || seen[strings.ToLower(d)] {
			continue
		}
		seen[strings.ToLower(d)] = true
		dirs = append(dirs, d)
	}
	return dirs
}

// ideExeUnder 判断进程的 exe 路径是否落在任一安装目录下。
func ideExeUnder(p procs.Proc, dirs []string) bool {
	for _, d := range dirs {
		if d != "" && p.HasExeUnder(d) {
			return true
		}
	}
	return false
}

// ideDescendsFrom 判断 p 是否是某个 root 的后代。
func ideDescendsFrom(p procs.Proc, all []procs.Proc, roots map[int]bool) bool {
	byPID := map[int]procs.Proc{}
	for _, x := range all {
		byPID[x.PID] = x
	}
	cur := p
	for i := 0; i < 32; i++ { // 防止环
		if cur.ParentPID <= 0 {
			return false
		}
		if roots[cur.ParentPID] {
			return true
		}
		next, ok := byPID[cur.ParentPID]
		if !ok {
			return false
		}
		cur = next
	}
	return false
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
