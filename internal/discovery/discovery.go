// Package discovery 负责在本机定位 Antigravity 桌面版与 agy CLI，
// 并枚举本机监听端口以发现候选代理通道。
package discovery

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// TargetKind 区分两种启动目标。
type TargetKind string

const (
	// TargetIDE 是 Electron 桌面版 Antigravity。
	TargetIDE TargetKind = "ide"
	// TargetCLI 是 Go 原生的 agy CLI。
	TargetCLI TargetKind = "cli"
)

// Target 是一个找到的启动目标。
type Target struct {
	Kind TargetKind
	// Path 是可执行文件绝对路径。
	Path string
	// Version 是探测到的版本号（尽力而为，可能为空）。
	Version string
	// Source 说明它是怎么被找到的，用于 CLI 输出。
	Source string
}

// String 用于 CLI 输出。
func (t Target) String() string {
	v := t.Version
	if v == "" {
		v = "版本未知"
	}
	return fmt.Sprintf("%s  %s  (%s)", t.Kind, t.Path, v)
}

// IDECandidates 返回桌面版 Antigravity 的候选路径，按平台与优先级排序。
//
// 依据：
//   - Windows: 开始菜单 Antigravity.lnk 指向
//     %LOCALAPPDATA%\Programs\antigravity\Antigravity.exe（已实测）
//   - macOS:   /Applications/Antigravity.app/Contents/MacOS/Antigravity
//     （updater 发布的产物是 Antigravity.zip，即 .app bundle）
//   - Linux:   AppImage / deb / tarball 均可，位置不确定，
//     故覆盖多个常见路径 + PATH 兜底
func IDECandidates() []string {
	var out []string

	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			out = append(out, filepath.Join(base, "Programs", "antigravity", "Antigravity.exe"))
		}
		if home := os.Getenv("USERPROFILE"); home != "" {
			out = append(out, filepath.Join(home, "AppData", "Local", "Programs", "antigravity", "Antigravity.exe"))
		}

	case "darwin":
		out = append(out,
			"/Applications/Antigravity.app/Contents/MacOS/Antigravity",
			"/Applications/Antigravity.app/Contents/MacOS/antigravity",
		)
		if home := homeDir(); home != "" {
			out = append(out,
				filepath.Join(home, "Applications", "Antigravity.app", "Contents", "MacOS", "Antigravity"),
			)
		}

	case "linux":
		out = append(out,
			"/opt/Antigravity/antigravity",
			"/opt/Antigravity/Antigravity",
			"/usr/bin/antigravity",
			"/usr/local/bin/antigravity",
		)
		if home := homeDir(); home != "" {
			out = append(out,
				filepath.Join(home, "Applications", "Antigravity.AppImage"),
				filepath.Join(home, ".local", "bin", "antigravity"),
				filepath.Join(home, "opt", "Antigravity", "antigravity"),
			)
		}
	}

	// PATH 兜底
	for _, name := range []string{"antigravity", "Antigravity"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}

	return out
}

// CLICandidates 返回 agy CLI 的候选路径。
//
// 依据（来自 Google 官方安装脚本）：
//   - Unix:   $HOME/.local/bin/agy
//   - Windows: %LOCALAPPDATA%\agy\bin\agy.exe
//   - 另外官方 install.sh 支持 --dir 自定义目录，故也扫 ~/.local/bin 与 PATH。
func CLICandidates() []string {
	var out []string

	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			out = append(out, filepath.Join(base, "agy", "bin", "agy.exe"))
		}
	} else {
		if home := homeDir(); home != "" {
			out = append(out, filepath.Join(home, ".local", "bin", "agy"))
			out = append(out, filepath.Join(home, "bin", "agy"))
		}
		out = append(out, "/usr/local/bin/agy", "/usr/bin/agy", "/opt/agy/bin/agy")
	}

	// PATH 兜底
	for _, name := range []string{"agy", "agy.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}

	return out
}

// FindTarget 按类型查找启动目标，返回第一个命中的。
func FindTarget(kind TargetKind) (Target, bool) {
	var candidates []string
	switch kind {
	case TargetIDE:
		candidates = IDECandidates()
	case TargetCLI:
		candidates = CLICandidates()
	}

	for _, c := range candidates {
		if executableExists(c) {
			t := Target{Kind: kind, Path: c, Source: "路径探测"}
			t.Version = probeVersion(kind, c)
			return t, true
		}
	}
	return Target{}, false
}

// FindAll 依次查找桌面版与 CLI，返回找到的那些。
func FindAll() []Target {
	var out []Target
	for _, kind := range []TargetKind{TargetIDE, TargetCLI} {
		if t, ok := FindTarget(kind); ok {
			out = append(out, t)
		}
	}
	return out
}

// probeVersion 尽力而为地取版本号。
//
// IDE 没有 --version 参数。注意：安装目录下的 version 文件里放的是
// **Electron 版本**（实测 44.9.1），不是 Antigravity 自己的版本
// （后者在 app.asar 的 package.json 里，实测 2.32.1）。
// 因此这里优先读 app.asar 的 package.json；读不到再退回 version 文件，
// 并在返回值里标注来源，避免把 Electron 版本当成 Antigravity 版本。
func probeVersion(kind TargetKind, path string) string {
	switch kind {
	case TargetIDE:
		base := filepath.Dir(path)
		if v := readAsarPackageVersion(base); v != "" {
			return v
		}
		for _, cand := range []string{
			filepath.Join(base, "version"),
			filepath.Join(base, "..", "Resources", "version"),
		} {
			if b, err := os.ReadFile(cand); err == nil {
				if v := strings.TrimSpace(string(b)); v != "" {
					return v + " (electron)"
				}
			}
		}
		return ""

	case TargetCLI:
		cmd := exec.Command(path, "--version")
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return ""
}

// readAsarPackageVersion 从 app.asar 里读顶层 package.json 的 version。
//
// asar 布局：前 16 字节是头部，
//   [0:4]   uint32 = 4
//   [4:8]   uint32 = 头部 pickle 大小
//   [8:12]  uint32 = 4
//   [12:16] uint32 = 头部 JSON 长度
//   [16:16+len]  头部 JSON
// 数据区起点 = 16 + 向上对齐到 4 的头部 JSON 长度。
//
// 两个坑（实测踩过）：
//  1) 头部 JSON 里有嵌套的 package.json（node_modules 里有很多），
//     必须解析出顶层 "files" 下的那一个，不能字符串搜索。
//  2) 文件项的 offset 可能是**字符串**（如 "5371832"），
//     也可能是数字，两种都要接受。
func readAsarPackageVersion(installDir string) string {
	asarPath := filepath.Join(installDir, "resources", "app.asar")
	b, err := os.ReadFile(asarPath)
	if err != nil || len(b) < 16 {
		return ""
	}

	headerLen := binary.LittleEndian.Uint32(b[12:16])
	if int64(headerLen)+16 > int64(len(b)) {
		return ""
	}

	var header struct {
		Files map[string]asarEntry `json:"files"`
	}
	if err := json.Unmarshal(b[16:16+headerLen], &header); err != nil {
		return ""
	}

	entry, ok := header.Files["package.json"]
	if !ok {
		return ""
	}
	offset, ok := entry.offsetValue()
	if !ok {
		return ""
	}

	dataOff := int64(16) + int64(headerLen)
	if dataOff%4 != 0 {
		dataOff += 4 - dataOff%4
	}
	start := dataOff + int64(offset)
	if start < 0 || start >= int64(len(b)) {
		return ""
	}

	size := int64(entry.Size)
	if size <= 0 || start+size > int64(len(b)) {
		size = int64(len(b)) - start
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b[start:start+size], &pkg); err != nil {
		return ""
	}
	return pkg.Version
}

// asarEntry 是 asar 头部里的一个文件项。
type asarEntry struct {
	Size int `json:"size"`
	// Offset 可能是数字，也可能是字符串。
	Offset json.RawMessage `json:"offset"`
}

// offsetValue 兼容数字与字符串两种 offset 表示。
func (e asarEntry) offsetValue() (uint64, bool) {
	if len(e.Offset) == 0 {
		return 0, false
	}
	if e.Offset[0] == '"' {
		var s string
		if err := json.Unmarshal(e.Offset, &s); err != nil {
			return 0, false
		}
		n, err := strconv.ParseUint(s, 10, 64)
		return n, err == nil
	}
	var n uint64
	if err := json.Unmarshal(e.Offset, &n); err != nil {
		return 0, false
	}
	return n, true
}

// ─────────────────────────────────────────────────────────────────
// 端口枚举
// ─────────────────────────────────────────────────────────────────

// ListenPort 是一个本机监听端口。
type ListenPort struct {
	// Host 是**连接目标**地址。已归一化：
	// 监听通配地址 "0.0.0.0" / "::" 会被换成 127.0.0.1 / ::1。
	Host string
	Port int
	// Wildcard 为 true 表示原始监听地址是通配地址（局域网可见）。
	Wildcard bool
	// PID 是占用这个端口的进程（0 表示未知）。
	//
	// 有这个字段就能在 CLI 里直接说「10808 是 v2rayN 开的」，
	// 而不是只丢一个端口号让用户猜。
	PID int
}

// CommonProxyPorts 是常见代理客户端端口白名单。
//
// 只在系统监听表读不到时才用它兜底。
// 绝不做全端口扫描——10 秒内 65535 次 SYN 会被杀毒软件
// （Defender / 火绒 / CrowdStrike / Little Snitch）直接判定为
// 端口扫描器并隔离进程，在 macOS/Linux 上还会打满 ulimit -n。
var CommonProxyPorts = []int{
	1080, 1081, 10808, 10809, 10810,
	7890, 7891, 7892, 7897,
	8080, 8081, 8118, 8888, 8889,
	10800, 20170, 20171, 20172,
}

// ListenPorts 枚举本机所有 TCP 监听端口。
//
// 各平台用系统原生途径，避免脆弱的外部命令字符串解析：
//   - Windows: iphlpapi 的 GetExtendedTcpTable
//   - Linux:   /proc/net/tcp 与 /proc/net/tcp6
//   - macOS:   lsof -iTCP -sTCP:LISTEN
//
// 具体实现放在 ports_<os>.go，各自带 build tag；
// listenPorts() 在不支持的平台是一个桩，返回错误。
//
// 任何一条路失败时返回错误，**不做全端口扫描兜底**。
func ListenPorts() ([]ListenPort, error) {
	return listenPorts()
}

// normalizeListenHost 把监听地址转成连接目标地址。
//
// "0.0.0.0" / "" / "*" -> 127.0.0.1（并标记 wildcard）
// "::"                 -> ::1（并标记 wildcard）
func normalizeListenHost(host string) (string, bool) {
	switch strings.TrimSpace(host) {
	case "", "*", "0.0.0.0":
		return "127.0.0.1", true
	case "::", "[::]":
		return "::1", true
	}
	return host, false
}
