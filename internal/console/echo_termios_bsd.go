//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package console

import "golang.org/x/sys/unix"

// BSD 系（含 darwin）的 termios ioctl 请求码。
//
// BSD 用 TIOCGETA / TIOCSETA；Linux 用 TCGETS / TCSETS —— 值不一样，
// 所以按平台拆文件。
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
