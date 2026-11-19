package main

import (
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
	resetWait := func() { waitTimeout = 3 }

	t.Run("默认 3 秒", func(t *testing.T) {
		resetWait()
		verboseMode, noWait = false, false
		if err := parseTUIArgs(nil); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if waitTimeout != 3 {
			t.Errorf("默认等待应该是 3 秒，实际 %d", waitTimeout)
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
