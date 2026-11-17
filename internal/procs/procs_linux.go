//go:build linux

package procs

import (
	"os"
	"strconv"
)

// isDarwin 在 Linux 上恒为 false。
func isDarwin() bool { return false }

// exePathOf 取进程的可执行文件绝对路径。
//
// /proc/<pid>/exe 是指向真实二进制的软链接，readlink 一次即可。
//
// ⚠️ 不能用 os.Stat 跟随软链接——那会 stat 到目标文件本身，
// 拿不到路径。必须 readlink。
//
// 进程已退出 / 无权限时返回 ""，调用方退回到进程树判据。
func exePathOf(pid int) string {
	link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return link
}
