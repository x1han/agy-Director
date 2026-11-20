//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package console

import "os"

// SuppressEcho 在其它平台上是空操作（没有 termios / 控制台模式可改）。
//
// 倒计时在这些平台上仍然工作 —— 只是用户按回车时终端会回显换行，
// 屏幕上可能多留一行倒计时。已知平台（Windows / Linux / macOS）都实现了
// 真正的关闭回显。
func SuppressEcho(f *os.File) func() {
	return nil
}
