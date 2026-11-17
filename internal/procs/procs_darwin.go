//go:build darwin

package procs

import (
	"os/exec"
	"strconv"
	"strings"
)

// isDarwin 供 procs_unix.go 判断用 ps 的哪种参数形式。
func isDarwin() bool { return true }

// exePathOf 取进程的可执行文件绝对路径（macOS 实现）。
//
// macOS 没有 /proc/<pid>/exe，只能靠 lsof。
// 拿不到就返回 ""，调用方退回到进程树判据。
func exePathOf(pid int) string {
	return darwinExePath(pid)
}

// darwinExePath 用 lsof 查进程的可执行文件绝对路径。
//
// macOS 没有 /proc/<pid>/exe。lsof 的 -a 选项是"所有条件都满足"，
// -d txt 表示只看映射了可执行文本段的 fd。
//
// 代价：每个 PID 都要跑一次 lsof，太慢。所以这里只对
// 名字可疑的进程调用（见 list() 的用法），不做全量扫描。
func darwinExePath(pid int) string {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		// lsof -F 的输出：n<PID> 是 PID，f<fd> 是 fd，n<path> 是名字
		if !strings.HasPrefix(line, "n") {
			continue
		}
		path := strings.TrimPrefix(line, "n")
		if strings.HasPrefix(path, "/") {
			return path
		}
	}
	return ""
}
