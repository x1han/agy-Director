//go:build windows

package console

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ── kernel32 里的控制台 API ───────────────────────────────────────────
//
// 用 syscall.NewLazyDLL 直接调，不引第三方依赖。
//
// 为什么不用 golang.org/x/sys/windows：
// AllocConsole / AttachConsole / GetConsoleWindow / SetConsoleTitle
// 是 x/sys v0.40 之后才加的，而更新的 x/sys 要求 Go 1.26+。
// 本项目用 Go 1.25，只能自己调 syscall —— 也就四五个函数，不值得为它抬 Go 版本。

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
	procGetConsoleOutputCP = kernel32.NewProc("GetConsoleOutputCP")
	procSetConsoleCP       = kernel32.NewProc("SetConsoleCP")
	procGetConsoleCP       = kernel32.NewProc("GetConsoleCP")
	procSetConsoleTitleW   = kernel32.NewProc("SetConsoleTitleW")
	procGetConsoleWindow   = kernel32.NewProc("GetConsoleWindow")
	procAllocConsole       = kernel32.NewProc("AllocConsole")
	procAttachConsole      = kernel32.NewProc("AttachConsole")
	procGetConsoleMode     = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode     = kernel32.NewProc("SetConsoleMode")
)

const (
	enableProcessedOutput = uint32(0x0001)
	enableVirtualTerminal = uint32(0x0004)
	attachParentProcess   = uintptr(0xFFFFFFFF)
	errorAccessDenied     = syscall.Errno(5)
	consoleOutputCPUTF8   = uint32(65001)
	consoleCPUTF8         = uint32(65001)
	stdOutputHandle       = -12 // (DWORD)-12
)

// prepare 在 Windows 上准备终端。
//
// ── 双击 exe 为什么会自己弹出窗口 ─────────────────────────────────────
//
// 因为这个二进制是 **console subsystem** 编译的（没有加 -H windowsgui）。
// Windows 对 console subsystem 的可执行文件有特殊待遇：
// 从资源管理器双击时，CreateProcess 会自动为它分配一个控制台
// （conhost.exe / Windows Terminal），并把 stdin/stdout/stderr 接上去。
//
// 这正是我们要的「打开之后自动开 terminal」，而且**不需要**任何
// AllocConsole + 重开 CONOUT$ 的手工活 —— 那些是给 GUI subsystem
// 程序用的（加了 -H windowsgui 之后系统不分配控制台，才得自己申请）。
//
// 保留 AllocConsole 兜底，是为了应对「被别的程序以无控制台方式启动」
// 的边缘情况（比如某些计划任务、或者被显式传了 DETACHED_PROCESS）。
func prepare() (bool, func(), error) {
	noop := func() {}

	// ── 已有控制台 ─────────────────────────────────────────────────
	//
	// GetConsoleWindow 在没有控制台时返回 0（不是错误）。
	if consoleWindow() != 0 {
		// 可能是"从 Explorer 双击"（控制台是我们独占的），
		// 也可能是"从 cmd 启动"（和控制台是父进程共用的）。
		// 两种情况下都先保存旧代码页、设成 UTF-8，
		// 退出时再恢复 —— 见 setUTF8Codepage 的注释。
		restore := setUTF8Codepage()
		enableVT()
		setConsoleTitle(Title)
		return true, restore, nil
	}

	// ── 没有控制台：先试着附着父进程的控制台 ───────────────────────
	//
	// 用户从 cmd/PowerShell 启动我们、但父进程没给我们继承控制台时，
	// 走这条路。成功后控制台是父进程的，用完要恢复代码页。
	if _, _, err := procAttachConsole.Call(attachParentProcess); err == nil {
		if err := reopenStdio(); err != nil {
			return false, noop, fmt.Errorf("重新绑定标准输入输出失败: %w", err)
		}
		restore := setUTF8Codepage()
		enableVT()
		setConsoleTitle(Title)
		return true, restore, nil
	}

	// ── 真的没有：自己开一个 ───────────────────────────────────────
	//
	// 这是"双击图标"的主路径。控制台是我们独占的，
	// 随便设代码页，退出时控制台也跟着没了，不用恢复。
	if _, _, err := procAllocConsole.Call(); err != nil {
		// ERROR_ACCESS_DENIED(5) = 其实已经附着在某个控制台上了
		// （GetConsoleWindow 为 0 但句柄有效的情况），不当错误。
		if errno, ok := err.(syscall.Errno); ok && errno == errorAccessDenied {
			return true, noop, nil
		}
		return false, noop, fmt.Errorf("申请控制台失败: %w", err)
	}

	if err := reopenStdio(); err != nil {
		return false, noop, fmt.Errorf("重新绑定标准输入输出失败: %w", err)
	}
	setUTF8Codepage()
	enableVT()
	setConsoleTitle(Title)
	return true, noop, nil
}

// setUTF8Codepage 把控制台的输入输出代码页设成 UTF-8，返回恢复函数。
//
// ── 为什么要在退出时恢复 ─────────────────────────────────────────────
//
// SetConsoleOutputCP 改的是**控制台**的代码页，不是进程的。
// 同一个控制台里的所有程序都受影响 —— 包括用户的父 shell。
//
// 如果用户是从 cmd.exe 里跑 agy-Director，而我们把代码页改成 UTF-8
// 还不改回去，他回到 shell 之后敲中文命令就会看到乱码。
// 所以：**保存旧值，退出时改回去**。
//
// 只读不写系统配置、不碰注册表。
func setUTF8Codepage() func() {
	// GetConsoleOutputCP 失败时返回 0，此时没有可恢复的值。
	oldOut, _, _ := procGetConsoleOutputCP.Call()
	oldIn, _, _ := procGetConsoleCP.Call()

	// SetConsoleOutputCP 成功时返回非 0。
	// 注意不能用 err 判断 —— syscall 的 Call 会把 GetLastError 的
	// 残留值当成 error 返回，即使函数本身是成功的。
	if r, _, _ := procSetConsoleOutputCP.Call(uintptr(consoleOutputCPUTF8)); r == 0 {
		fmt.Fprintln(os.Stderr, "设置控制台输出代码页失败，中文可能显示为乱码")
	}
	if r, _, _ := procSetConsoleCP.Call(uintptr(consoleCPUTF8)); r == 0 {
		fmt.Fprintln(os.Stderr, "设置控制台输入代码页失败")
	}

	return func() {
		if oldOut != 0 {
			_, _, _ = procSetConsoleOutputCP.Call(oldOut)
		}
		if oldIn != 0 {
			_, _, _ = procSetConsoleCP.Call(oldIn)
		}
	}
}

// consoleWindow 返回控制台窗口句柄，没有控制台时返回 0。
func consoleWindow() uintptr {
	r, _, _ := procGetConsoleWindow.Call()
	return r
}

// setConsoleTitle 设置控制台窗口标题。
func setConsoleTitle(title string) {
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	_, _, _ = procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p)))
}

// enableVT 打开虚拟终端处理，让 ANSI 转义序列生效。
func enableVT() {
	handle, _ := syscall.GetStdHandle(stdOutputHandle)
	if handle == 0 || handle == syscall.InvalidHandle {
		return
	}
	var mode uint32
	if _, _, err := procGetConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode))); err != nil {
		return
	}
	newMode := mode | enableProcessedOutput | enableVirtualTerminal
	_, _, _ = procSetConsoleMode.Call(uintptr(handle), uintptr(newMode))
}

// reopenStdio 把 os.Stdin/Stdout/Stderr 重新绑到控制台设备上。
//
// ── 为什么必须重绑 ───────────────────────────────────────────────────
//
// Go 的 os 包在 **init 时**就把 os.Stdout/os.Stderr 包成了 *os.File，
// 当时的句柄来自 GetStdHandle —— 那时进程可能还没有控制台，
// 拿到的是 NULL。所以 AllocConsole 之后，os.Stdout 指向的仍然是个
// 空壳句柄，往里写会返回 ERROR_INVALID_HANDLE。
//
// 必须重新 OpenFile("CONOUT$") 拿真正的控制台设备句柄再赋值回去。
//
// 注意 stderr 要**单独** OpenFile 一次，不要和 stdout 共用同一个
// *os.File —— 共用的话 Close 时会互相干扰。
func reopenStdio() error {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("打开 CONIN$ 失败: %w", err)
	}
	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("打开 CONOUT$ 失败: %w", err)
	}
	errFile, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("打开 CONOUT$ (stderr) 失败: %w", err)
	}
	os.Stdin, os.Stdout, os.Stderr = in, out, errFile
	return nil
}
