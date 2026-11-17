// Package procs 枚举本机进程，用于单实例守卫与终止失败时的引导。
//
// 目的很具体：Electron 的 requestSingleInstanceLock() 让第二次启动
// 把 argv 转发给已有实例并立刻退出（**丢掉新的环境变量**）。
// 所以启动前必须确认没有旧实例在跑；而如果终止失败，
// 必须把**精确的 PID** 告诉用户，让他能自己手动结束。
//
// ## 判据的优先级（这是踩过坑之后定的）
//
//  1. **可执行文件绝对路径**（首选，最可靠）
//     操作系统天然提供：Windows 用 QueryFullProcessImageNameW，
//     Linux 用 /proc/<pid>/exe，macOS 用 proc_pidpath。
//     桌面版 Antigravity 装在已知目录（如
//     %LOCALAPPDATA%\Programs\antigravity\），只要进程的 exe 路径
//     落在那个前缀下，就是它。
//
//  2. **进程树**（次选）
//     桌面版的子进程（渲染进程 / GPU 进程 / utility 进程）都由
//     桌面版主进程派生，从主进程沿 PPID 往上走能认出来。
//
//  3. 命令行特征（最后手段，只用来**排除**）
//
// 绝不用命令行特征来**认定**"这是 Antigravity"——
// Chromium 的渲染进程命令行同样带 --type=renderer，
// 曾经因此把用户 Chrome / Edge 的渲染进程一起杀掉（实际发生过）。
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
	ParentCmdLine string
	// ExePath 是可执行文件的绝对路径（可能为空）。
	//
	// **这是最可靠的判据**：操作系统给出的真实路径，
	// 不受进程名重名、命令行伪装的影响。
	ExePath string
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
//
// 注意：CLI sidecar 的 exe 路径与桌面版**相同**
// （都是 %LOCALAPPDATA%\Programs\antigravity\Antigravity.exe），
// 所以路径前缀判据区分不了二者，必须靠这条命令��特征。
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

// HasExeUnder 判断这个进程的可执行文件是否位于给定目录下。
//
// 用它做"这是不是 Antigravity"的认定：
//   - Chrome 在 C:\Program Files\Google\Chrome\Application\
//   - Edge  在 C:\Program Files (x86)\Microsoft\Edge\Application\
//   - Antigravity 在 %LOCALAPPDATA%\Programs\antigravity\
//
// 三者路径不同，**不可能误判**。
//
// 路径比较用不区分大小写 + 分隔符归一化，
// 因为 Windows 上路径分隔符可能是 \ 或 /，盘符大小写也可能不同。
func (p Proc) HasExeUnder(dir string) bool {
	if p.ExePath == "" || dir == "" {
		return false
	}
	return pathHasPrefix(p.ExePath, dir)
}

// pathHasPrefix 判断 path 是否在 dir 之下（目录边界正确）。
//
// "C:\Program Files\Google\Chrome" 不应被当成
// "C:\Program Files\Google\Chrome\Application" 的前缀，反之亦然。
func pathHasPrefix(path, dir string) bool {
	path = normalizePath(path)
	dir = normalizePath(dir)
	dir = strings.TrimRight(dir, "/")
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+"/")
}

// normalizePath 把路径归一化：小写 + 反斜杠转正斜杠 + 去尾斜杠。
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimRight(p, "/")
	return strings.ToLower(p)
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
// 实现要点：必须返回**全部**同名进程。Electron 桌面版会同时起
// 十几个 Antigravity.exe（主进程 + 渲染进程 + GPU 进程 + utility 进程），
// 只杀一个等于没杀。
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

// Descendants 返回以 root 为根的整棵进程树（含 root 自己）。
//
// 用 PPID 往上走：先找出所有 parent 是 root 的进程，
// 再递归。这是"桌面版主进程派生的子进程"的可靠认定方式——
// 子进程的命令行可能千奇百怪，但它们的**祖先**一定是桌面版。
func Descendants(all []Proc, root int) []Proc {
	children := map[int][]Proc{}
	for _, p := range all {
		if p.ParentPID > 0 {
			children[p.ParentPID] = append(children[p.ParentPID], p)
		}
	}

	var out []Proc
	seen := map[int]bool{}
	var walk func(pid int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		for _, p := range children[pid] {
			out = append(out, p)
			walk(p.PID)
		}
	}
	walk(root)
	return out
}

// Atoi 是 strconv.Atoi 的别名，保持本包自洽。
func Atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// parsePID 解析 PID 字符串。
func parsePID(s string) int {
	return Atoi(s)
}
