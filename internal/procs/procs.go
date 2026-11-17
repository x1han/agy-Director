// Package procs 枚举本机进程，用于单实例守卫与终止失败时的引导。
//
// 目的很具体：Electron 的 requestSingleInstanceLock() 让第二次启动
// 把 argv 转发给已有实例并立刻退出（**丢掉新的环境变量**）。
// 所以启动前必须确认没有旧实例在跑；而如果终止失败，
// 必须把**精确的 PID** 告诉用户，让他能自己手动结束。
package procs

import (
	"fmt"
	"strconv"
	"strings"
)

// Proc 是一个进程。
type Proc struct {
	PID  int
	Name string
	// CmdLine 是完整命令行（可能为空）。
	//
	// **必须带上它**：Windows 上 agy CLI 的 Node sidecar 也叫
	// Antigravity.exe（实测：Antigravity.exe --import=file://...main.mjs），
	// 只按进程名匹配会误杀用户的 agy CLI 会话。
	CmdLine string
	// ParentPID 是父进程 PID（0 表示未知）。
	ParentPID int
	// ParentCmdLine 是父进程的命令行。
	//
	// 用来区分「桌面版自己的 sidecar」和「用户 agy CLI 的 sidecar」：
	// 两者命令行长得一样，但父进程不同——
	//   - 桌面版 sidecar 的父进程是 Electron（命令行含 --proxy-server 等）
	//   - agy CLI sidecar 的父进程是 cmd.exe /c "...agy-node.cmd" ...
	ParentCmdLine string
}

// String 用于 CLI 输出。
func (p Proc) String() string {
	return fmt.Sprintf("%s (PID %d)", p.Name, p.PID)
}

// IsSidecar 判断这个进程是不是 **agy CLI 的** Node sidecar
// （即用户 CLI 会话的一部分，不是桌面版的一部分）。
//
// 特征链：
//  1. 命令行里有 --import=file:// 且以 main.mjs / server.cjs 结尾
//     —— Antigravity 自带的 Electron 二进制被当作 Node 宿主跑 sidecar。
//  2. **父进程命令行里含 agy-node.cmd** —— 这才是 CLI 会话。
//
// 为什么要第 2 条：桌面版 Antigravity 也会起同样的 sidecar
// （实测 PID 81832 / 77584 就是桌面版的），
// 只按第 1 条判断会把桌面版自己的 sidecar 误判成 CLI 的，从而漏杀，
// 旧实例就会残留。
func (p Proc) IsSidecar() bool {
	cl := strings.ToLower(p.CmdLine)
	if !strings.Contains(cl, "--import=file://") {
		return false
	}
	if !strings.HasSuffix(cl, "main.mjs") &&
		!strings.HasSuffix(cl, "server.cjs") &&
		!strings.Contains(cl, "register_loader.js") {
		return false
	}
	// 父进程是 agy-node.cmd 才是 CLI 会话。
	return strings.Contains(strings.ToLower(p.ParentCmdLine), "agy-node.cmd")
}

// List 列出本机所有进程。
//
// 各平台实现放在 procs_<os>.go，各自带 build tag，
// 只有当前平台的那个会被编译进来。
func List() ([]Proc, error) {
	return list()
}

// Find 按进程名（大小写不敏感）找出匹配的进程。
//
// name 参数不带扩展名，"Antigravity" 会同时匹配
// "Antigravity" 与 "Antigravity.exe"。
func Find(name string) ([]Proc, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	target := strings.ToLower(name)
	// 同时接受带扩展名的输入
	targetBase := strings.TrimSuffix(target, ".exe")

	var out []Proc
	for _, p := range all {
		lower := strings.ToLower(p.Name)
		if lower == target || lower == targetBase ||
			strings.TrimSuffix(lower, ".exe") == targetBase {
			out = append(out, p)
		}
	}
	return out, nil
}

// FindAny 按一组候选名字找出匹配进程，返回全部命中。
//
// 各平台进程名不同：
//   - Windows: Antigravity.exe
//   - macOS:   Antigravity（.app 里的可执行文件）
//   - Linux:   antigravity（AppImage / deb 里的 tarball）
//
// 所以调用方给一份候选列表，而不是一个名字。
//
// 实现要点：
//  1. 必须返回**全部**同名进程。Electron 桌面版会同时起十几个
//     Antigravity.exe（主进程 + 渲染进程 + GPU 进程 + utility 进程），
//     只杀一个等于没杀。
//  2. 默认**排除** agy CLI 的 Node sidecar（见 Proc.IsSidecar）——
//     它同名但属于用户的 CLI 会话，不该被桌面版的终止流程杀掉。
//     要连它一起杀时用 FindAnyIncludingSidecar。
//
// 曾经踩过的坑：用 map[name]proc 索引会只留下最后一个同名进程，
// 于是只 kill 了一个，其余 Electron 进程还活着——
// 第二次启动会命中 requestSingleInstanceLock，把新 env 丢掉。
func FindAny(names []string) ([]Proc, error) {
	return findAny(names, false)
}

// FindAnyIncludingSidecar 与 FindAny 相同，但**包含** agy CLI 的
// Node sidecar。用于「用户明确要求把所有 Antigravity 相关进程都停掉」。
func FindAnyIncludingSidecar(names []string) ([]Proc, error) {
	return findAny(names, true)
}

// IsDesktopApp 判断这个进程**看起来像** Electron 应用的一个子进程。
//
// 桌面版 Electron 进程的共同特征：
//   - 主进程命令行带 --proxy-server / --proxy-bypass-list
//     （由 agy-untun 注入；用户从开始菜单启动时没有）
//   - 子进程命令行带 --type=gpu-process / --type=utility / --type=renderer
//   - 都有 --user-data-dir=...AppData\Roaming\Antigravity
//
// ⚠️ **这个函数单独用是不安全的**。
// Chrome / Edge 的渲染进程命令行同样带 --type=renderer，
// 所以它的返回值**必须**与进程名一起判断——
// 先确认进程名是 Antigravity，再看这个标志。
// 调用方请用 IsIdeProcess（名字 + 命令行双判据）而不是这个。
func (p Proc) IsDesktopApp() bool {
	cl := strings.ToLower(p.CmdLine)
	if cl == "" {
		return false
	}
	if strings.Contains(cl, "--user-data-dir=") {
		return true
	}
	return strings.Contains(cl, "--type=gpu-process") ||
		strings.Contains(cl, "--type=renderer") ||
		strings.Contains(cl, "--type=utility") ||
		strings.Contains(cl, "--proxy-server")
}

// IsIdeProcess 判断这个进程是不是 Antigravity 桌面版的一部分。
//
// **两道过滤都要过**：
//  1. 进程名是 Antigravity / language_server —— 排除 Chrome / Edge。
//  2. 命令行符合 Electron 桌面版的特征 —— 排除 agy CLI 的同名 sidecar。
//
// 这个函数是"要不要杀掉它"的唯一判据。
func (p Proc) IsIdeProcess() bool {
	name := strings.ToLower(p.Name)
	name = strings.TrimSuffix(name, ".exe")
	if name != "antigravity" && name != "language_server" {
		return false
	}
	if name == "language_server" {
		return true
	}
	return p.IsDesktopApp()
}

func findAny(names []string, includeSidecar bool) ([]Proc, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}

	// 归一化候选名：去扩展名 + 小写。
	wanted := map[string]bool{}
	for _, n := range names {
		key := strings.ToLower(strings.TrimSpace(n))
		key = strings.TrimSuffix(key, ".exe")
		if key != "" {
			wanted[key] = true
		}
	}

	var out []Proc
	seen := map[int]bool{}
	for _, p := range all {
		key := strings.ToLower(p.Name)
		key = strings.TrimSuffix(key, ".exe")
		if !wanted[key] {
			continue
		}
		if seen[p.PID] {
			continue
		}
		if !includeSidecar && p.IsSidecar() {
			continue
		}
		seen[p.PID] = true
		out = append(out, p)
	}
	return out, nil
}

// Atoi 是 strconv.Atoi 的别名，保持本包自洽。
func Atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// listPorts 供各平台实现使用：把 ps 的 PID 列解析成整数。
func parsePID(s string) int {
	return Atoi(s)
}
