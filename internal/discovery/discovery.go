// Package discovery 负责在本机定位 Antigravity 桌面版与 agy CLI，
// 并枚举本机监听端口以发现候选代理通道。
package discovery

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
		if home, err := os.UserHomeDir(); err == nil {
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
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out,
				filepath.Join(home, "Applications", "Antigravity.AppImage"),
				filepath.Join(home, ".local", "bin", "antigravity"),
				filepath.Join(home, "opt", "Antigravity", "antigravity"),
			)
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
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, ".local", "bin", "agy"))
			out = append(out, filepath.Join(home, "bin", "agy"))
		}
		out = append(out, "/usr/local/bin/agy", "/usr/bin/agy", "/opt/agy/bin/agy")
	}

	// PATH 兜底
	if p, err := exec.LookPath("agy"); err == nil {
		out = append(out, p)
	}
	if p, err := exec.LookPath("agy.exe"); err == nil {
		out = append(out, p)
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
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
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
	// 字符串形式
	if e.Offset[0] == '"' {
		var s string
		if err := json.Unmarshal(e.Offset, &s); err != nil {
			return 0, false
		}
		n, err := strconv.ParseUint(s, 10, 64)
		return n, err == nil
	}
	// 数字形式
	var n uint64
	if err := json.Unmarshal(e.Offset, &n); err != nil {
		return 0, false
	}
	return n, true
}

// IDEVersionFile 暴露给上层，用于在 IDE 未安装时提示版本。
// 返回空串表示读不到。
func IDEVersionFile(path string) string { return probeVersion(TargetIDE, path) }

// ─────────────────────────────────────────────────────────────────
// 端口枚举
// ─────────────────────────────────────────────────────────────────

// ListenPort 是一个本机监听端口。
type ListenPort struct {
	Host string
	Port int
}

// IsLoopback 判断该端口是否只监听回环地址。
func (p ListenPort) IsLoopback() bool {
	return p.Host == "127.0.0.1" || p.Host == "::1" || p.Host == "localhost"
}

// ListenPorts 枚举本机所有 TCP 监听端口。
//
// 策略：优先用系统工具读监听表（快，实测 netstat 约 3s / 47 个端口）；
// 读不到时退化为并发扫描 1..65535（实测约 10.5s）。
func ListenPorts() ([]ListenPort, error) {
	if ports, err := listenPortsFromSystem(); err == nil && len(ports) > 0 {
		return ports, nil
	}
	return scanAllPorts(256)
}

// listenPortsFromSystem 用 netstat / ss 读取监听表。
func listenPortsFromSystem() ([]ListenPort, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("netstat", "-ano", "-p", "tcp")
	case "linux":
		cmd = exec.Command("ss", "-lntpH")
	case "darwin":
		cmd = exec.Command("netstat", "-an", "-p", "tcp")
	default:
		return nil, fmt.Errorf("unsupported os: %s", runtime.GOOS)
	}

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var ports []ListenPort

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if runtime.GOOS != "linux" && !strings.Contains(line, "LISTEN") {
			continue
		}
		host, port, ok := parseListenLine(line)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s:%d", host, port)
		if seen[key] {
			continue
		}
		seen[key] = true
		ports = append(ports, ListenPort{Host: host, Port: port})
	}
	return ports, nil
}

// parseListenLine 从一行系统输出里解析出 host 与 port。
func parseListenLine(line string) (string, int, bool) {
	fields := strings.Fields(line)
	for _, f := range fields {
		if !strings.Contains(f, ":") {
			continue
		}
		// 跳过 PID 之类的纯数字
		idx := strings.LastIndex(f, ":")
		if idx < 0 {
			continue
		}
		hostPart := f[:idx]
		portPart := f[idx+1:]
		if portPart == "" {
			continue
		}
		port, err := atoi(portPart)
		if err != nil {
			continue
		}
		host := hostPart
		// IPv6: [::] 或 [::1]
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
		// Windows 的 0.0.0.0 / :: 视为全接口监听
		switch host {
		case "0.0.0.0", "::", "*":
			host = "0.0.0.0"
		}
		return host, port, true
	}
	return "", 0, false
}

func atoi(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
		if n > 65535 {
			return 0, fmt.Errorf("out of range")
		}
	}
	return n, nil
}

// scanAllPorts 并发扫描 1..65535，收集处于 LISTEN 的端口。
func scanAllPorts(concurrency int) ([]ListenPort, error) {
	if concurrency <= 0 {
		concurrency = 256
	}

	var mu sync.Mutex
	var ports []ListenPort
	next := 1

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				p := next
				next++
				mu.Unlock()
				if p > 65535 {
					return
				}
				ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
				if err == nil {
					mu.Lock()
					ports = append(ports, ListenPort{Host: "127.0.0.1", Port: p})
					mu.Unlock()
					ln.Close()
				}
			}
		}()
	}
	wg.Wait()
	return ports, nil
}
