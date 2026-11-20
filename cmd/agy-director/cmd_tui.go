package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/x1han/agy-Director/internal/channel"
	"github.com/x1han/agy-Director/internal/console"
	"github.com/x1han/agy-Director/internal/discovery"
	"github.com/x1han/agy-Director/internal/logging"
	"github.com/x1han/agy-Director/internal/tui"
	"golang.org/x/term"
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
// 不想被一个"按回车关闭"卡在那儿。所以默认最多等 5 秒：
//   - 5 秒到了自动关
//   - 期间按回车可以立刻关
//   - 等待期间终端上显示**实时倒计时**（5 → 1），用户能确切知道
//     还剩几秒，不用猜窗口什么时候消失
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
	waitForEnterOrTimeoutTo(os.Stdout, r, timeout, isLiveTerminal(os.Stdout))
}

// suppressEcho 关闭终端输入回显，返回恢复函数（可能为 nil）。
//
// 回显是**输入侧**的属性，所以看的是 stdin 是不是终端 ——
// 不是 stdout。stdout 被重定向到文件、stdin 仍是终端时，
// 回显同样要关（否则用户敲的键会打进文件里）。
//
// stdin 不是终端（管道 / 文件）时，console.SuppressEcho 返回 nil ——
// 那种情况下本来就没有回显这回事。
func suppressEcho() func() {
	return console.SuppressEcho(os.Stdin)
}

// isLiveTerminal 判断 w 是不是真实终端。
//
// 倒计时靠 "\r" 原地重画 —— 输出到管道或文件时会有字面量 \r 和一堆
// 残影（`agy-Director > out.txt` 会写出一行乱码）。所以非终端时
// 只打一行静态提示，不做倒计时。
func isLiveTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// waitForEnterOrTimeoutTo 是真正干活的版本，输出与"是否终端"都可注入 ——
// 测试要能断言倒计时到底输出了什么。
//
// ⚠️ 倒计时和等待**必须共用同一个 deadline**。
// 分成"先打 N 个刻度、再等 N 秒"的话，窗口会变成 2N 秒 ——
// 用户说"5 秒关闭"就一定得是 5 秒，不是 10 秒。
//
// ⚠️ 这段时间里不要往 stderr 写东西 —— Windows 控制台里 stdout 和
// stderr 共享同一个屏幕缓冲，交错输出会把倒计时那一行冲掉。
func waitForEnterOrTimeoutTo(w io.Writer, r io.Reader, timeout time.Duration, live bool) {
	// ── 先关掉输入回显 ────────────────────────────────────────────────
	//
	// 控制台/终端默认开着 ECHO：用户按回车时会把 "\r\n" 回显到屏幕上，
	// 那个换行把光标推到下一行 —— 之后擦"当前行"擦的是新行，
	// 倒计时那一行就留在屏幕上了（用户按回车后看到两行残留）。
	//
	// 关掉回显，顺带也消掉了等待期间误敲的字母留下的残字。
	//
	// ⚠️ 必须在**任何**输出之前关掉 —— 否则第一次回显已经把光标推下去了。
	restoreEcho := suppressEcho()
	if restoreEcho != nil {
		defer restoreEcho()
	}

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

	// 不到 1 秒就不做倒计时 —— 亚秒级只出现在测试里，
	// 显示"0 秒后自动关闭"是纯噪声。
	totalSecs := int(timeout / time.Second)
	if totalSecs < 1 {
		select {
		case <-entered:
		case <-timer.C:
		}
		return
	}

	// 秒数字段固定宽度（%*d）：--wait=10 时会从 "10" 变成 " 9"，
	// 不补齐的话重画出来的行比原来短，末尾会留下残字。
	digits := len(strconv.Itoa(totalSecs))
	countdown := func(secs int) string {
		return fmt.Sprintf("%s（%*d 秒后自动关闭）", waitPrompt, digits, secs)
	}
	closedText := fmt.Sprintf("%s（已自动关闭）", waitPrompt)
	enterText := fmt.Sprintf("%s（已关闭）", waitPrompt)

	// 一行文字的显示宽度 —— 擦除时要用等宽的空格把整行盖掉。
	width := dispWidth(countdown(totalSecs))
	for _, s := range []string{closedText, enterText} {
		if w := dispWidth(s); w > width {
			width = w
		}
	}

	if live {
		fmt.Fprintf(w, "\n%s", countdown(totalSecs))
	} else {
		fmt.Fprintf(w, "\n%s\n", countdown(totalSecs))
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	remaining := totalSecs
	for {
		select {
		case <-entered:
			// 用户主动关的：擦掉倒计时，留一句收尾。
			if live {
				eraseLine(w, width)
				fmt.Fprintf(w, "%s\n", padTo(enterText, width))
			}
			return
		case <-ticker.C:
			remaining--
			if remaining < 1 {
				// 交给 deadline 分支收尾，别显示"0 秒"。
				continue
			}
			if live {
				fmt.Fprintf(w, "\r%s", padTo(countdown(remaining), width))
			}
		case <-timer.C:
			if live {
				eraseLine(w, width)
				fmt.Fprintf(w, "%s\n", padTo(closedText, width))
			}
			return
		}
	}
}

// waitPrompt 是等待提示的前缀。
const waitPrompt = "按回车关闭..."

// eraseLine 把当前行擦掉（\r 回行首 + 等宽空格 + \r 再回行首）。
//
// 宽度必须等于那一行的**显示宽度**，否则擦不干净会留残字。
func eraseLine(w io.Writer, width int) {
	fmt.Fprintf(w, "\r%s\r", strings.Repeat(" ", width))
}

// padTo 把 s 右侧补空格到 width 列。
func padTo(s string, width int) string {
	pad := width - dispWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// dispWidth 返回字符串在终端里的显示宽度（列数）。
//
// 中日韩字符是全角，占 2 列。用 rune 数算会少算一半，
// 擦除宽度就不对 —— 倒计时行会擦不干净。
func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isWideRune 判断 r 是否是全角字符（East Asian Wide / Fullwidth）。
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E,   // CJK Radicals, Kangxi, CJK Symbols
		r >= 0x3041 && r <= 0x33FF,   // Hiragana .. CJK Compatibility
		r >= 0x3400 && r <= 0x4DBF,   // CJK Extension A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF,   // Yi
		r >= 0xAC00 && r <= 0xD7A3,   // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE6F,   // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60,   // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,   // Fullwidth Signs
		r >= 0x20000 && r <= 0x3FFFD: // CJK Extension B and beyond
		return true
	}
	return false
}

// runPlainSummary 是没有终端时的退化路径。
func runPlainSummary() error {
	fmt.Println("agy-Director")
	fmt.Println()
	fmt.Println("找不到可用的终端，无法渲染界面。")
	fmt.Println("请在终端里直接运行本程序，或用 agy-Director launch 走命令行模式。")
	return nil
}
