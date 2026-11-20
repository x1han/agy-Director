//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package console

import (
	"os"

	"golang.org/x/sys/unix"
)

// SuppressEcho 关闭终端输入回显，返回恢复函数。
//
// ── 为什么需要关回显 ──────────────────────────────────────────────────
//
// 摘要在终端上显示倒计时，靠 "\r" 原地重画那一行。
// 但 tty 默认开着 **ECHO**（输入回显）：
//
//  1. 用户按下回车 → 终端把 "\n" **回显**到屏幕上
//  2. 回显的换行把光标推到**下一行**
//  3. 这时再去擦除"当前行"，擦的是新行 —— 倒计时那一行留在屏幕上
//
// 表现就是：用户按了回车，屏幕上**同时**留着倒计时和"已关闭"两行，
// 看起来像 bug。
//
// 顺带还有个更糟的情况：等待期间误敲的字母（比如 "q"）也会被回显，
// 而它落在擦除宽度之外，会变成擦不掉的残字。
//
// 关掉回显，两个问题一起消失。
//
// ⚠️ 只改 ECHO 标志，不动 ICANON / ISIG ——
// 换行符照常翻译、Ctrl+C 照常能中断，其余终端行为保持不变。
//
// 恢复函数必须被调用 —— 否则用户的终端会一直处于不回显状态。
//
// 非终端（管道 / 文件）返回 nil，调用方跳过。
func SuppressEcho(f *os.File) func() {
	if f == nil {
		return nil
	}
	fd := int(f.Fd())

	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		// 不是 tty（管道 / 文件）—— 没得关。
		return nil
	}
	if termios.Lflag&unix.ECHO == 0 {
		// 本来就没开回显 —— 不用动，但还是要给个可调用的恢复函数，
		// 好让调用方走同一条路径。
		return func() {}
	}

	newTermios := *termios
	newTermios.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &newTermios); err != nil {
		return nil
	}

	return func() {
		_ = unix.IoctlSetTermios(fd, ioctlWriteTermios, termios)
	}
}
