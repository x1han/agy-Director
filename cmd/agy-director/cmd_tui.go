package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/console"
	"github.com/x1han/agy-Director/internal/discovery"
	"github.com/x1han/agy-Director/internal/logging"
	"github.com/x1han/agy-Director/internal/tui"
)

// runTUI 是 tui 子命令的入口，也是**双击图标时的默认动作**。
//
// 整体流程：
//
//  1. 准备终端（Windows 设代码页/标题，macOS 从 .app 拉 Terminal）
//  2. 初始化日志（stderr + 文件双 sink）
//  3. 跑 TUI：扫描目标与通道，等用户选「是否启动」
//  4. 选「是」→ 真的启动 Antigravity
//  5. 打印摘要，等回车再退出
func runTUI(args []string) error {
	if err := parseTUIArgs(args); err != nil {
		return err
	}

	// ── 1. 终端准备 ───────────────────────────────────────────────
	//
	// macOS 上这一步可能会**在 Terminal.app 里另开一个窗口跑我们**，
	// 然后当前进程就退出了。所以要先问一句"终端可用吗"。
	//
	// restore 负责把改动过的终端设置还原（Windows 的控制台代码页是
	// 控制台级别的设置，不还原会影响用户父 shell）。
	ok, restore, err := console.Prepare()
	if err != nil {
		fmt.Fprintf(os.Stderr, "准备终端失败: %v\n", err)
	}
	if restore != nil {
		defer restore()
	}
	if !ok {
		// 没有终端可渲染 → 退化成纯文本输出，至少让用户看到信息。
		fmt.Fprintln(os.Stderr, "没有可用的终端，退化为纯文本模式。")
		return runPlainSummary()
	}

	// ── 2. 日志 ───────────────────────────────────────────────────
	logPath, closeLog, err := logging.Setup(verboseMode)
	if err != nil {
		// 日志开不了不该挡住主流程，只是排障会变难。
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		logPath = ""
	} else {
		defer closeLog()
		logging.Info("agy-Director 启动", "版本", version, "日志", logPath)
	}

	// ── 3. 跑 TUI ─────────────────────────────────────────────────
	//
	// TUI 期间静音 stderr：Windows 控制台里 stdout/stderr 同屏，
	// 日志插进来会把界面搅乱。文件 sink 全程开着，一行都不丢。
	logging.MuteStderr()
	outcome, err := tui.Run()
	logging.UnmuteStderr()

	if err != nil {
		logging.Error("TUI 运行失败", "err", err)
		return fmt.Errorf("TUI 运行失败: %w", err)
	}

	// ── 4. 结果分派 ───────────────────────────────────────────────
	if !outcome.Result.Ready() {
		// 缺目标或缺通道 → 明确说缺哪个，无法启动。
		logging.Warn("扫描结果不完整，无法启动", "目标数", len(outcome.Result.Targets), "通道数", len(outcome.Result.Channels))
		printMissing(outcome.Result)
		waitForEnter()
		return fmt.Errorf("无法启动")
	}

	target := outcome.Target
	ch := outcome.Channel
	if !outcome.Found || !outcome.ChannelFound {
		printMissing(outcome.Result)
		waitForEnter()
		return fmt.Errorf("无法启动")
	}

	if !outcome.Accepted {
		logging.Info("用户选择不启动", "目标", target.Path, "通道", ch.Name)
		fmt.Println("\n已取消启动。")
		printSummary(target, ch, logPath)
		waitForEnter()
		return nil
	}

	// ── 5. 真的启动 ───────────────────────────────────────────────
	logging.Info("用户确认启动", "目标", target.Path, "通道", ch.Name, "代理", ch.URL())
	if err := launchSelected(&target, &ch, &launchOptions{yes: true}); err != nil {
		logging.Error("启动失败", "err", err)
		printSummary(target, ch, logPath)
		waitForEnter()
		return err
	}

	printSummary(target, ch, logPath)
	waitForEnter()
	return nil
}

// printMissing 打印缺什么。
func printMissing(res tui.ScanResult) {
	fmt.Fprintln(os.Stderr)
	if len(res.Targets) == 0 {
		msg := res.TargetErr
		if msg == "" {
			msg = "未找到 Antigravity 桌面版，也未找到 agy CLI"
		}
		fmt.Fprintf(os.Stderr, "✗ %s\n", msg)
	}
	if len(res.Channels) == 0 {
		msg := res.ChannelErr
		if msg == "" {
			msg = "未找到可用的代理通道"
		}
		fmt.Fprintf(os.Stderr, "✗ %s\n", msg)
	}
	fmt.Fprintln(os.Stderr, "\n无法启动。")
}

// printSummary 打印启动摘要。
func printSummary(target discovery.Target, ch channel.Channel, logPath string) {
	fmt.Println()
	fmt.Println(strings.Repeat("─", 56))
	fmt.Printf("  目标  %s\n", target.Path)
	if target.Version != "" {
		fmt.Printf("  版本  %s\n", target.Version)
	}
	fmt.Printf("  通道  %s\n", ch.Name)
	fmt.Printf("  代理  %s\n", ch.URL())
	if logPath != "" {
		fmt.Printf("  日志  %s\n", logPath)
	}
	fmt.Println(strings.Repeat("─", 56))
}

// waitForEnter 等用户按回车，**或者最多等 waitTimeout 秒**。
//
// Windows 上控制台窗口随进程退出而关闭 —— 不等的话用户什么都看不到。
// macOS 的 Terminal 窗口本来就常驻，但走同一条路径保持行为一致。
//
// 为什么不无限等：用户双击图标只想看一眼"扫到了什么、用了哪个通道"，
// 不想被一个"按回车关闭"卡在那儿。所以默认最多等 3 秒：
//   - 3 秒到了自动关
//   - 期间按回车可以立刻关
//
// --no-wait 时直接返回（脚本里用）；--wait / --wait=N 可以调这个上限。
func waitForEnter() {
	if noWait {
		return
	}

	// 无限等：保持原来的行为（等回车）。
	if waitTimeout <= 0 {
		fmt.Print("\n按回车关闭...")
		var s string
		_, _ = fmt.Fscanln(os.Stdin, &s)
		return
	}

	fmt.Printf("\n按回车关闭...（%d 秒后自动关闭）", waitTimeout)
	waitForEnterOrTimeout(os.Stdin, time.Duration(waitTimeout)*time.Second)
}

// waitForEnterOrTimeout 等回车或超时，谁先来算谁。
//
// 抽出来是为了能测 —— 直接测 waitForEnter 会读到测试进程自己的
// stdin，测不了。
//
// ⚠️ 读 stdin 的 goroutine 会一直阻塞在 Read 上，直到真的按了回车。
// 主流程超时返回后进程就退了，那个 goroutine 会被一起带走。
// 但如果调用方不退出，它会一直占着这个 reader —— 所以这里
// 传进来的 io.Reader 用完就不要再复用。
func waitForEnterOrTimeout(r io.Reader, timeout time.Duration) {
	reader := bufio.NewReader(r)
	entered := make(chan struct{})

	go func() {
		for {
			b, err := reader.ReadByte()
			if err != nil {
				// EOF 或读错误 —— **不当作"用户按了回车"**。
				// stdin 被关掉 / 被重定向到空文件时，
				// 这里直接返回，entered 永远不会关闭，
				// 主流程会照常等到超时。
				//
				// （早先这里 close(ch) 会让 select 立刻返回，
				//  等于"stdin 一空窗口就闪退"。）
				return
			}
			// 只认回车：Windows 是 \r，Unix 是 \n。
			// 其它按键忽略 —— 用户可能手滑敲了个字母，
			// 不该把窗口关了。
			if b == '\r' || b == '\n' {
				close(entered)
				return
			}
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-entered:
	case <-timer.C:
	}
}

// runPlainSummary 是没有终端时的退化路径。
func runPlainSummary() error {
	fmt.Println("agy-Director")
	fmt.Println()
	fmt.Println("找不到可用的终端，无法渲染界面。")
	fmt.Println("请在终端里直接运行本程序，或用 agy-Director launch 走命令行模式。")
	return nil
}
