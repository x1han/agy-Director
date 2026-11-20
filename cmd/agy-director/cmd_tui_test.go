package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// 摘要页的等待行为：最多等 N 秒，期间按回车可以立刻关。
//
// 这个逻辑决定了用户双击图标后终端窗口会挂多久 ——
// 等太久烦人，太短又看不到信息。

func TestWaitForEnterOrTimeoutReturnsOnEnter(t *testing.T) {
	// 输入里有回车 —— 应该立刻返回，而不是等满 timeout。
	done := make(chan struct{})
	go func() {
		waitForEnterOrTimeout(strings.NewReader("\r"), 10*time.Second)
		close(done)
	}()

	select {
	case <-done:
		// 正常
	case <-time.After(2 * time.Second):
		t.Fatal("读到回车后应该立刻返回，却等超时了")
	}
}

func TestWaitForEnterOrTimeoutTimesOut(t *testing.T) {
	// 什么都不输入 —— 应该超时返回。
	start := time.Now()
	waitForEnterOrTimeout(strings.NewReader(""), 200*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 150*time.Millisecond {
		t.Errorf("超时返回太早了：%v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("超时返回太晚了：%v", elapsed)
	}
}

func TestWaitForEnterOrTimeoutIgnoresOtherKeys(t *testing.T) {
	// 只有字母和空格，没有回车 —— 不该被当成"按了回车"。
	// 超时应该照常发生。
	start := time.Now()
	waitForEnterOrTimeout(strings.NewReader("abc def"), 300*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 250*time.Millisecond {
		t.Errorf("非回车的按键不该关闭窗口，但 %v 就返回了", elapsed)
	}
}

func TestWaitForEnterOrTimeoutAcceptsNewline(t *testing.T) {
	// Unix 终端回车是 \n，也应该立刻返回。
	done := make(chan struct{})
	go func() {
		waitForEnterOrTimeout(strings.NewReader("\n"), 10*time.Second)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("读到换行后应该立刻返回，却等超时了")
	}
}

// --wait=N / --wait 的解析。
func TestParseTUIArgsWait(t *testing.T) {
	resetWait := func() { waitTimeout = 5 }

	t.Run("默认 5 秒", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs(nil); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if waitTimeout != 5 {
			t.Errorf("默认等待应该是 5 秒，实际 %d", waitTimeout)
		}
	})

	t.Run("--wait=10", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--wait=10"}); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if waitTimeout != 10 {
			t.Errorf("--wait=10 应该得到 10，实际 %d", waitTimeout)
		}
	})

	t.Run("--wait=0 表示一直等", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--wait=0"}); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if waitTimeout != 0 {
			t.Errorf("--wait=0 应该得到 0（无限等），实际 %d", waitTimeout)
		}
	})

	t.Run("--wait 表示一直等", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--wait"}); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if waitTimeout != 0 {
			t.Errorf("--wait 应该得到 0（无限等），实际 %d", waitTimeout)
		}
	})

	t.Run("--wait=abc 应当报错", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--wait=abc"}); err == nil {
			t.Error("非法的秒数应当报错")
		}
	})

	t.Run("--wait=-1 应当报错", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs([]string{"--wait=-1"}); err == nil {
			t.Error("负数秒数应当报错")
		}
	})
}

// waitForEnter 本身：把 os.Stdin 换成管道，验证它真的按预期退出。
func TestWaitForEnterClosesOnEnter(t *testing.T) {
	oldStdin := os.Stdin
	oldNoWait, oldTimeout := noWait, waitTimeout
	defer func() {
		os.Stdin = oldStdin
		noWait, waitTimeout = oldNoWait, oldTimeout
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道失败: %v", err)
	}
	os.Stdin = r
	noWait = false
	waitTimeout = 30 // 超时给足，回车应该让它立刻返回

	go func() {
		_, _ = w.WriteString("\r")
	}()

	start := time.Now()
	waitForEnter()
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("按回车后应该立刻返回，却等了 %v", elapsed)
	}
	_ = w.Close()
}

func TestWaitForEnterClosesOnTimeout(t *testing.T) {
	oldStdin := os.Stdin
	oldNoWait, oldTimeout := noWait, waitTimeout
	defer func() {
		os.Stdin = oldStdin
		noWait, waitTimeout = oldNoWait, oldTimeout
	}()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道失败: %v", err)
	}
	defer w.Close()
	os.Stdin = r
	noWait = false
	waitTimeout = 1

	start := time.Now()
	waitForEnter()
	elapsed := time.Since(start)

	if elapsed < 800*time.Millisecond {
		t.Errorf("应该等满 1 秒才返回，实际 %v", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("等太久了：%v", elapsed)
	}
}

func TestWaitForEnterNoWaitReturnsImmediately(t *testing.T) {
	oldNoWait, oldTimeout := noWait, waitTimeout
	defer func() { noWait, waitTimeout = oldNoWait, oldTimeout }()

	noWait = true
	waitTimeout = 30

	start := time.Now()
	waitForEnter()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("--no-wait 应该立刻返回，实际等了 %v", elapsed)
	}
}

// 生产默认值就是 5 秒 —— 用真实的 waitTimeout 跑一遍，
// 确认默认行为真的是"等 5 秒自动关"。
func TestWaitForEnterDefaultIsFiveSeconds(t *testing.T) {
	oldStdin := os.Stdin
	oldNoWait, oldTimeout := noWait, waitTimeout
	defer func() {
		os.Stdin = oldStdin
		noWait, waitTimeout = oldNoWait, oldTimeout
	}()

	// 直接断言包级默认值 —— 这才是"生产默认值就是 5 秒"的真正守卫。
	// （把 waitTimeout 设成 5 再断言它等于 5 是同义反复，测不出默认值被改。）
	if waitTimeout != 5 {
		t.Fatalf("waitTimeout 的生产默认值应该是 5，实际 %d", waitTimeout)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道失败: %v", err)
	}
	defer w.Close()
	os.Stdin = r
	noWait = false

	start := time.Now()
	waitForEnter()
	elapsed := time.Since(start)

	if elapsed < 4800*time.Millisecond {
		t.Errorf("应该等满 5 秒，实际只等了 %v", elapsed)
	}
	if elapsed > 6500*time.Millisecond {
		t.Errorf("等太久了：%v", elapsed)
	}
}

// 倒计时：等待期间要能看到 5 → 1 的递减。
func TestCountdownShowsDecreasingSeconds(t *testing.T) {
	var buf bytes.Buffer
	// live=true 才会重画；这里用 live 模式验证倒计时内容。
	waitForEnterOrTimeoutTo(&buf, strings.NewReader(""), 3*time.Second, true)

	out := buf.String()
	for _, want := range []string{"3 秒后自动关闭", "2 秒后自动关闭", "1 秒后自动关闭"} {
		if !strings.Contains(out, want) {
			t.Errorf("倒计时里应该出现 %q，实际输出:\n%q", want, out)
		}
	}
}

// 关键回归：**总窗口必须正好是 N 秒，不能是 2N 秒**。
//
// 倒计时刻度和等待截止如果分成两套计时器，
// "先打 5 个刻度再等 5 秒"就会变成 10 秒 —— 用户说的 5 秒就没了。
func TestCountdownAndWaitShareOneDeadline(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	waitForEnterOrTimeoutTo(&buf, strings.NewReader(""), 2*time.Second, true)
	elapsed := time.Since(start)

	if elapsed < 1900*time.Millisecond {
		t.Errorf("应该等满 2 秒，实际只等了 %v", elapsed)
	}
	if elapsed > 3200*time.Millisecond {
		t.Errorf("总窗口超过 2 秒了（倒计时和等待用了两套计时器？）：%v", elapsed)
	}
}

// 按回车要立刻关，不能等满倒计时。
func TestCountdownClosesImmediatelyOnEnter(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	waitForEnterOrTimeoutTo(&buf, strings.NewReader("\r"), 30*time.Second, true)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("按回车后应该立刻返回，却等了 %v", elapsed)
	}
	if !strings.Contains(buf.String(), "已关闭") {
		t.Errorf("按回车后应该留一句收尾，实际输出:\n%q", buf.String())
	}
}

// 非终端（输出到管道/文件）时不能有 \r 重画 —— 否则文件里是一行乱码。
func TestCountdownNonTerminalHasNoCarriageReturn(t *testing.T) {
	var buf bytes.Buffer
	waitForEnterOrTimeoutTo(&buf, strings.NewReader(""), 2*time.Second, false)

	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Errorf("非终端输出里不该有 \\r，实际输出:\n%q", out)
	}
	if !strings.Contains(out, "2 秒后自动关闭") {
		t.Errorf("非终端也该打一行静态提示，实际输出:\n%q", out)
	}
}

// 倒计时行的宽度必须固定 —— 秒数从 10 变 9 时不能留下残字。
//
// 同时校验**擦除宽度 == 倒计时宽度**：擦不干净就会在行尾留下残字。
func TestCountdownLineHasConstantWidth(t *testing.T) {
	var buf bytes.Buffer
	waitForEnterOrTimeoutTo(&buf, strings.NewReader(""), 2*time.Second, true)

	segments := strings.Split(buf.String(), "\r")

	countdownWidths := map[int]bool{}
	eraseWidth := -1
	for _, seg := range segments {
		trimmed := strings.Trim(seg, " \n")
		if trimmed == "" {
			// 全是空格的段 = 擦除行
			if eraseWidth < 0 {
				eraseWidth = dispWidth(seg)
			}
			continue
		}
		if strings.Contains(trimmed, "秒后自动关闭") {
			countdownWidths[dispWidth(trimmed)] = true
		}
	}

	if len(countdownWidths) != 1 {
		t.Errorf("倒计时各行宽度应该一致，实际有 %d 种宽度: %v", len(countdownWidths), countdownWidths)
	}
	if eraseWidth < 0 {
		t.Fatal("应该有一次擦除（\r + 空格 + \r）")
	}
	for w := range countdownWidths {
		if eraseWidth != w {
			t.Errorf("擦除宽度 %d 应该等于倒计时行宽度 %d（否则会留残字）", eraseWidth, w)
		}
	}
}

// 全角字符按 2 列算 —— 中文提示的擦除宽度要按显示宽度算，不是 rune 数。
func TestDispWidthCountsWideRunesAsTwo(t *testing.T) {
	if got := dispWidth("按回车关闭"); got != 10 {
		t.Errorf("5 个全角汉字应该是 10 列，实际 %d", got)
	}
	if got := dispWidth("abc"); got != 3 {
		t.Errorf("3 个 ASCII 应该是 3 列，实际 %d", got)
	}
}

// 倒计时结束后，屏幕上**不能**还留着倒计时那一行。
//
// 根因：控制台默认开着 ECHO —— 用户按回车时会把 "\r\n" 回显出来，
// 那个换行把光标推到下一行。之后擦"当前行"擦的是新行，
// 倒计时那一行就留在屏幕上了。
//
// 关掉回显（console.SuppressEcho）之后，回车不再回显换行，
// 擦除才落在正确的行上。
//
// 这里验证的是"关掉回显之后，擦除用的是**同一个行**"——
// 通过检查输出里 \r 的用法：擦除必须是 "\r" + 空格 + "\r"，
// 不能含有 "\n"（含 \n 就是跑到下一行去了）。
func TestCountdownEraseDoesNotAdvanceRow(t *testing.T) {
	var buf bytes.Buffer
	waitForEnterOrTimeoutTo(&buf, strings.NewReader("\r"), 5*time.Second, true)

	out := buf.String()

	// 开头那个 \n 是**故意**的 —— 摘要页之后另起一行显示倒计时。
	// 之后（倒计时重画阶段）不能再有 \n：换行会把光标推到下一行，
	// 擦除就落在错的行上。
	trimmed := strings.TrimPrefix(out, "\n")
	if i := strings.Index(trimmed, "已关闭"); i >= 0 {
		trimmed = trimmed[:i]
	}
	if strings.Contains(trimmed, "\n") {
		t.Errorf("倒计时重画阶段不能有 \\n（会把光标推到下一行）：\n%q", trimmed)
	}

	// 收尾那一行可以带 \n（要把最后的提示留在屏幕上）。
	if !strings.HasSuffix(strings.TrimRight(out, " \n"), "已关闭）") {
		t.Errorf("收尾应该是「已关闭」：\n%q", out)
	}
}
