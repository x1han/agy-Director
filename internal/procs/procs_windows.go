//go:build windows

package procs

import (
	"encoding/csv"
	"os/exec"
	"strings"
)

// list 用 tasklist 枚举进程，再用 CIM 补命令行。
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

	// 一次 CIM 查询同时拿 PID / 父 PID / 两个命令行。
	info := listCimInfo()
	for i := range result {
		if in, ok := info[result[i].PID]; ok {
			result[i].CmdLine = in.cmdLine
			result[i].ParentPID = in.parentPID
			result[i].ParentCmdLine = in.parentCmdLine
		}
	}
	return result, nil
}

// procInfo 是一个进程的关键信息。
type procInfo struct {
	cmdLine       string
	parentPID     int
	parentCmdLine string
}

// listCimInfo 返回 PID -> procInfo。
//
// 用一次 Get-CimInstance Win32_Process 拿全部字段
// （ProcessId / ParentProcessId / CommandLine），
// 然后在本地建父子索引，避免为了取父进程命令行再跑一次 CIM。
func listCimInfo() map[int]procInfo {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,CommandLine | ConvertTo-Csv -NoTypeInformation").Output()
	if err != nil {
		return map[int]procInfo{}
	}

	records, pidCol, parentCol, cmdCol := parseCimCSV(string(out))
	if pidCol < 0 {
		return map[int]procInfo{}
	}

	// 第一遍：PID -> 命令行
	cmdlines := map[int]string{}
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
		if parentCol >= 0 && len(rec) > parentCol {
			parentOf[pid] = parsePID(rec[parentCol])
		}
	}

	// 第二遍：填父进程命令行
	result := map[int]procInfo{}
	for pid, cl := range cmdlines {
		p := procInfo{cmdLine: cl, parentPID: parentOf[pid]}
		if p.parentPID > 0 {
			p.parentCmdLine = cmdlines[p.parentPID]
		}
		result[pid] = p
	}
	return result
}

// parseCimCSV 解析 ConvertTo-Csv 的输出，返回记录与三个列的下标。
//
// 表头可能是：
//
//	"ProcessId","ParentProcessId","CommandLine"
//	"Node,NODE,ProcessId,ParentProcessId,CommandLine"
//
// 所以按表头名字找列，不按列号。
func parseCimCSV(out string) ([][]string, int, int, int) {
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, -1, -1, -1
	}

	header := records[0]
	pidCol, parentCol, cmdCol := -1, -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(strings.Trim(h, `"`))) {
		case "processid":
			pidCol = i
		case "parentprocessid":
			parentCol = i
		case "commandline":
			cmdCol = i
		}
	}
	return records[1:], pidCol, parentCol, cmdCol
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

// listCmdLines 返回 PID -> 命令行。
func listCmdLines() map[int]string {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,CommandLine | ConvertTo-Csv -NoTypeInformation").Output()
	if err == nil {
		if m := parseCmdLineCSV(string(out)); len(m) > 0 {
			return m
		}
	}

	// 备用：wmic。
	out, err = exec.Command("wmic", "process", "get", "ProcessId,CommandLine", "/format:csv").Output()
	if err != nil {
		return map[int]string{}
	}
	return parseCmdLineCSV(string(out))
}

// parseCmdLineCSV 解析 ConvertTo-Csv 的输出。
//
// 两种表头都可能出现：
//
//	"ProcessId","CommandLine"
//	"Node,NODE,ProcessId,CommandLine"
//
// 所以不按列号取，而是按表头名字找。
func parseCmdLineCSV(out string) map[int]string {
	result := map[int]string{}

	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return result
	}

	header := records[0]
	pidCol, cmdCol := -1, -1
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(strings.Trim(h, `"`))) {
		case "processid":
			pidCol = i
		case "commandline":
			cmdCol = i
		}
	}
	if pidCol < 0 || cmdCol < 0 {
		return result
	}

	for _, rec := range records[1:] {
		if len(rec) <= pidCol || len(rec) <= cmdCol {
			continue
		}
		pid := parsePID(rec[pidCol])
		if pid <= 0 {
			continue
		}
		result[pid] = strings.TrimSpace(strings.Trim(rec[cmdCol], `"`))
	}
	return result
}
