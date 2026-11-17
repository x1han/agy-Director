//go:build windows

package procs

import (
	"encoding/csv"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// list 用 tasklist 枚举进程，再用 CIM 补命令行、父进程与 exe 路径。
//
// 为什么分两步：tasklist /FO CSV 稳定且快，但没有命令行；
// 而命令行是区分「桌面版 Antigravity.exe」和「agy CLI 的 Node sidecar
// Antigravity.exe」的唯一依据（见 Proc.IsSidecar）。
//
// wmic 在 Windows 11 上已弃用，所以命令行用 PowerShell 的
// Get-CimInstance 取；wmic 只是备用路径。
func list() ([]Proc, error) {
	result, err := listBasic()
	if err != nil {
		return nil, err
	}

	info := listCimInfo()
	for i := range result {
		if in, ok := info[result[i].PID]; ok {
			result[i].CmdLine = in.cmdLine
			result[i].ParentPID = in.parentPID
			result[i].ParentCmdLine = in.parentCmdLine
		}
		// exe 路径用 Win32 API 直接查，比 CIM 快得多，
		// 而且不依赖 PowerShell 能不能起来。
		result[i].ExePath = exePathOf(result[i].PID)
	}
	return result, nil
}

// procInfo 是一个进程的关键信息。
type procInfo struct {
	cmdLine       string
	parentPID     int
	parentCmdLine string
	exePath       string
}

// listCimInfo 返回 PID -> procInfo。
//
// 用一次 Get-CimInstance Win32_Process 拿全部字段
// （ProcessId / ParentProcessId / CommandLine / ExecutablePath），
// 然后在本地建父子索引，避免为了取父进程命令行再跑一次 CIM。
func listCimInfo() map[int]procInfo {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,CommandLine,ExecutablePath | ConvertTo-Csv -NoTypeInformation").Output()
	if err != nil {
		return map[int]procInfo{}
	}

	records, pidCol, parentCol, cmdCol, exeCol := parseCimCSV(string(out))
	if pidCol < 0 {
		return map[int]procInfo{}
	}

	// 第一遍：PID -> 命令行 / exe 路径
	cmdlines := map[int]string{}
	exepaths := map[int]string{}
	parentOf := map[int]int{}
	for _, rec := range records {
		if len(rec) <= pidCol {
			continue
		}
		pid := parsePID(rec[pidCol])
		if pid <= 0 {
			continue
		}
		if cmdCol >= 0 && len(rec) > cmdCol {
			cmdlines[pid] = strings.TrimSpace(strings.Trim(rec[cmdCol], `"`))
		}
		if exeCol >= 0 && len(rec) > exeCol {
			exepaths[pid] = strings.TrimSpace(strings.Trim(rec[exeCol], `"`))
		}
		if parentCol >= 0 && len(rec) > parentCol {
			parentOf[pid] = parsePID(rec[parentCol])
		}
	}

	// 第二遍：填父进程命令行
	result := map[int]procInfo{}
	for pid, cl := range cmdlines {
		p := procInfo{cmdLine: cl, parentPID: parentOf[pid], exePath: exepaths[pid]}
		if p.parentPID > 0 {
			p.parentCmdLine = cmdlines[p.parentPID]
		}
		result[pid] = p
	}
	return result
}

// parseCimCSV 解析 ConvertTo-Csv 的输出，返回记录与四个列的下标。
//
// 表头可能是：
//
//	"ProcessId","ParentProcessId","CommandLine","ExecutablePath"
//	"Node,NODE,ProcessId,ParentProcessId,CommandLine,ExecutablePath"
//
// 所以按表头名字找列，不按列号。
func parseCimCSV(out string) ([][]string, int, int, int, int) {
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, -1, -1, -1, -1
	}

	header := records[0]
	pidCol, parentCol, cmdCol, exeCol := -1, -1, -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(strings.Trim(h, `"`))) {
		case "processid":
			pidCol = i
		case "parentprocessid":
			parentCol = i
		case "commandline":
			cmdCol = i
		case "executablepath":
			exeCol = i
		}
	}
	return records[1:], pidCol, parentCol, cmdCol, exeCol
}

// listBasic 用 tasklist 拿 PID 与进程名。
//
// 输出格式：/FO CSV /NH
//
//	"Antigravity.exe","1234","Console","1","250,000 K"
func listBasic() ([]Proc, error) {
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, err
	}

	var result []Proc
	r := csv.NewReader(strings.NewReader(string(out)))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		if len(rec) < 2 {
			continue
		}
		name := strings.Trim(rec[0], `"`)
		if name == "" || strings.HasPrefix(name, "INFO:") {
			continue
		}
		pid := parsePID(rec[1])
		if pid <= 0 {
			continue
		}
		result = append(result, Proc{PID: pid, Name: name})
	}
	return result, nil
}

// ── QueryFullProcessImageNameW ────────────────────────────────────────
//
// 用它直接拿到进程的可执行文件绝对路径——
// 这比任何命令行字符串匹配都可靠，
// 是「这个进程是不是 Antigravity」的第一判据。
//
// 之所以不用 CIM 的 ExecutablePath 作为唯一来源：
// CIM 是 PowerShell 子进程，慢（几百毫秒到秒级）且会失败；
// 直接调 Win32 API 可以在拿到 PID 后立刻查路径。

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procQueryFullProcessImage = kernel32.NewProc("QueryFullProcessImageNameW")
	procOpenProcess           = kernel32.NewProc("OpenProcess")
)

// processQueryLimitedInfo 是 OpenProcess 需要的访问权限。
//
// 0x1000 = PROCESS_QUERY_LIMITED_INFORMATION。
// 用这个最小权限就够了：只要读 exe 路径，不需要读内存，
// 也不需要对管理员进程有额外权限。
const processQueryLimitedInfo = 0x1000

// exePathOf 用 QueryFullProcessImageNameW 取进程的 exe 路径。
//
// ⚠️ 参数是 **4 个**：`(hProcess, dwFlags, lpExeName, lpdwSize)`。
//
// 这点容易搞错：文档里常写成三参数的简化形式
// `QueryFullProcessImageName(h, buf, &size)`，
// 但 Windows 实际导出的 `QueryFullProcessImageNameW`
// 签名是带 dwFlags 的四参数版（dwFlags 目前保留，必须传 0）。
// 传成三参数会返回 ERROR_INVALID_PARAMETER(87)。
//
// 查不到（进程已退出 / 无权限）时返回 ""。
func exePathOf(pid int) string {
	handle, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInfo), 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	// PROCESS_NAME_MAX = 260，MAX_PATH 同。
	buf := make([]uint16, 260)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImage.Call(
		handle,
		0, // dwFlags，保留，必须为 0
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}
