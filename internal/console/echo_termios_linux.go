//go:build linux

package console

import "golang.org/x/sys/unix"

// Linux 的 termios ioctl 请求码。
//
// Linux 用 TCGETS / TCSETS；BSD 系（darwin 等）用 TIOCGETA / TIOCSETA ——
// 值不一样，所以按平台拆文件。
const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)
