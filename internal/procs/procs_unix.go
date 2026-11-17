//go:build linux || darwin

package procs

import (
	"os/exec"
	"strconv"
	"strings"
)

// list 用 ps 枚举进程。
//
//   - Linux:   ps -eo pid=,comm=,args=
//   - macOS:   ps -axo pid=,comm=,args=
//
// macOS 的 ps 不支持 -e（那是 System V 的写法），必须用 -a。
// args 就是完整命令行，用来识别 sidecar。
func list() ([]Proc, error) {
	args := []string{"-eo", "pid=,comm=,args="}
	if isDarwin() {
		args = []string{"-axo", "pid=,comm=,args="}
	}
	out, err := exec.Command("ps", args...).Output()
	if err != nil {
		return nil, err
	}

	var result []Proc
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// pid comm args —— comm 与 args 之间可能有空格，
		// 所以按前两个空白切分，剩下的整体是 args。
		first := strings.IndexAny(line, " \t")
		if first < 0 {
			continue
		}
		rest := strings.TrimLeft(line[first:], " \t")
		second := strings.IndexAny(rest, " \t")
		if second < 0 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line[:first]))
		if err != nil || pid <= 0 {
			continue
		}
		name := strings.TrimSpace(rest[:second])
		cmdline := strings.TrimSpace(rest[second:])
		result = append(result, Proc{
			PID:     pid,
			Name:    name,
			CmdLine: cmdline,
			ExePath: exePathOf(pid),
		})
	}
	return result, nil
}

// exePathOf 的实现按平台分文件：
//
//   - Linux: procs_linux.go   —— /proc/<pid>/exe 软链接
//   - macOS: procs_darwin.go  —— lsof -d txt
//
// 这里不定义，避免任一平台引用到另一个平台的符号。
