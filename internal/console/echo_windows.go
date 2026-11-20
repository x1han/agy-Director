//go:build windows

package console

import (
	"os"

	"golang.org/x/sys/windows"
)

// SuppressEcho 关闭控制台输入回显，返回恢复函数。
//
// ── 为什么需要关回显 ──────────────────────────────────────────────────
//
// 摘要在终端上显示倒计时，靠 "\r" 原地重画那一行。
// 但控制台默认开着 **ECHO**（输入回显）：
//
//  1. 用户按下回车 → 控制台把 "\r\n" **回显**到屏幕上
//  2. 回显的 "\n" 把光标推到**下一行**
//  3. 这时再去擦除"当前行"，擦的是新行 —— 倒计时那一行留在屏幕上
//
// 表现就是：用户按了回车，屏幕上**同时**留着倒计时和"已关闭"两行，
// 看起来像 bug。
//
// 顺带还有个更糟的情况：等待期间误敲的字母（比如 "q"）也会被回显，
// 而它落在擦除宽度之外，会变成擦不掉的残字。
//
// 关掉回显，两个问题一起消失：
//   - 回车不再回显 "\r\n"，光标不跑偏
//   - 误敲的键不留残影
//
// ⚠️ 只改**输入**的控制台模式，不动代码页、不动输出模式。
// 恢复函数必须被调用 —— 否则用户的终端会一直处于不回显状态。
//
// 非终端（管道 / 文件）返回 nil，调用方跳过。
func SuppressEcho(f *os.File) func() {
	if f == nil {
		return nil
	}
	handle := windows.Handle(f.Fd())

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		// 不是控制台（管道 / 文件）—— 没得关。
		return nil
	}
	if mode&windows.ENABLE_ECHO_INPUT == 0 {
		// 本来就没开回显 —— 不用动，但还是要给个可调用的恢复函数，
		// 好让调用方走同一条路径。
		return func() {}
	}

	newMode := mode &^ uint32(windows.ENABLE_ECHO_INPUT)
	if err := windows.SetConsoleMode(handle, newMode); err != nil {
		return nil
	}

	return func() {
		_ = windows.SetConsoleMode(handle, mode)
	}
}
