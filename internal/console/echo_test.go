//go:build windows

package console

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestSuppressEchoTurnsOffEchoAndRestores 验证等待期间 ECHO 位被关掉，
// 结束后**精确**还原。
//
// 为什么重要：控制台默认开着 ECHO —— 用户按回车时会把 "\r\n" 回显到
// 屏幕上，那个换行把光标推到下一行。倒计时靠 "\r" 原地重画，
// 光标跑偏就会擦错行，屏幕上留下残影。
//
// 这个测试用 CONIN$（真实控制台输入设备），而不是 os.Stdin ——
// 测试进程的 os.Stdin 是管道。
func TestSuppressEchoTurnsOffEchoAndRestores(t *testing.T) {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("没有控制台输入设备: %v", err)
	}
	defer in.Close()

	handle := windows.Handle(in.Fd())

	var before uint32
	if err := windows.GetConsoleMode(handle, &before); err != nil {
		t.Skipf("GetConsoleMode 失败: %v", err)
	}

	restore := SuppressEcho(in)
	if restore == nil {
		t.Fatal("SuppressEcho 应该返回恢复函数")
	}

	var during uint32
	if err := windows.GetConsoleMode(handle, &during); err != nil {
		t.Fatalf("GetConsoleMode 失败: %v", err)
	}
	if during&windows.ENABLE_ECHO_INPUT != 0 {
		t.Errorf("等待期间 ECHO 位应该被关掉（mode=%#x）", during)
	}
	// 除了 ECHO，其它位不该被改 —— 尤其是 ICANON / ISIG
	// （Ctrl+C 要还能中断）。
	if during&^(uint32(windows.ENABLE_ECHO_INPUT)) != before&^(uint32(windows.ENABLE_ECHO_INPUT)) {
		t.Errorf("除了 ECHO 之外不该改别的位：before=%#x during=%#x", before, during)
	}

	restore()

	var after uint32
	if err := windows.GetConsoleMode(handle, &after); err != nil {
		t.Fatalf("GetConsoleMode 失败: %v", err)
	}
	if after != before {
		t.Errorf("恢复后应该和原来完全一样：before=%#x after=%#x", before, after)
	}
}

// 非控制台（管道 / 文件）时必须返回 nil —— 不能乱改。
func TestSuppressEchoNonTerminalReturnsNil(t *testing.T) {
	f, err := os.CreateTemp("", "echo-probe-*")
	if err != nil {
		t.Fatalf("建临时文件失败: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if restore := SuppressEcho(f); restore != nil {
		t.Error("非终端输入时应该返回 nil（没有回显可关）")
	}
	if restore := SuppressEcho(nil); restore != nil {
		t.Error("nil 输入时应该返回 nil")
	}
}
