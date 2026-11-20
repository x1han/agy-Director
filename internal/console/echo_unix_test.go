//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package console

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// TestSuppressEchoUnixTurnsOffEchoAndRestores 验证 Unix 上 ECHO 位
// 被关掉，并且**精确**还原。
//
// 为什么重要：终端默认开着 ECHO —— 用户按回车时会把 "\n" 回显出来，
// 那个换行把光标推到下一行。倒计时靠 "\r" 原地重画，
// 光标跑偏就会擦错行，屏幕上留下残影。
func TestSuppressEchoUnixTurnsOffEchoAndRestores(t *testing.T) {
	// 用 /dev/tty —— 测试进程的 os.Stdin 是管道，不是 tty。
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("没有控制终端: %v", err)
	}
	defer tty.Close()

	fd := int(tty.Fd())
	before, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		t.Skipf("IoctlGetTermios 失败: %v", err)
	}

	restore := SuppressEcho(tty)
	if restore == nil {
		t.Fatal("SuppressEcho 应该返回恢复函数")
	}

	during, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		t.Fatalf("IoctlGetTermios 失败: %v", err)
	}
	if during.Lflag&unix.ECHO != 0 {
		t.Errorf("等待期间 ECHO 位应该被关掉（Lflag=%#x）", during.Lflag)
	}
	// ISIG 必须还在 —— Ctrl+C 要能中断。
	if during.Lflag&unix.ISIG == 0 {
		t.Error("ISIG 位不该被清掉（否则 Ctrl+C 失效）")
	}

	restore()

	after, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		t.Fatalf("IoctlGetTermios 失败: %v", err)
	}
	if after.Lflag != before.Lflag {
		t.Errorf("恢复后 Lflag 应该和原来一样：before=%#x after=%#x", before.Lflag, after.Lflag)
	}
}

// 非终端（普通文件）时必须返回 nil —— 不能乱改。
func TestSuppressEchoUnixNonTerminalReturnsNil(t *testing.T) {
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
