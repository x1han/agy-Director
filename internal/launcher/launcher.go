// Package launcher 负责注入代理环境变量并启动 Antigravity 桌面版或 agy CLI。
//
// 核心机制（已实证）：Antigravity 的 Go 后端只从环境变量解析代理。
// Electron 壳的 dist/languageServer.js 里
//     const env = { ...process.env, ...loginShellEnv };
// 把 Electron 进程的 env 透传给 language_server 子进程，
// 因此在父进程注入 HTTPS_PROXY 即可让后端走代理。
package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/user/agy-untun/internal/channel"
)

// Options 是一次启动的参数。
type Options struct {
	// Channel 是要使用的代理通道。
	Channel channel.Channel
	// Target 是要启动的可执行文件。
	Target string
	// Args 是传给目标程序的额外参数。
	Args []string
	// ExtraEnv 是额外注入的环境变量（覆盖同名项）。
	ExtraEnv map[string]string
	// Detached 为 true 时不等待子进程退出（桌面版默认如此）。
	Detached bool
	// WorkingDir 为空时用 Target 所在目录。
	WorkingDir string
}

// BuildEnv 构造启动 Antigravity 用的环境变量。
//
// 规则：
//   - HTTP_PROXY / http_proxy / HTTPS_PROXY / https_proxy 一律设为通道 URL。
//     Go 的 httpproxy 同时认大小写两种形式（httpproxy.FromEnvironment），
//     某些子工具只认小写，所以两种都设。
//   - **ALL_PROXY / all_proxy 只在通道是纯 socks5 时才设。**
//     原因：ALL_PROXY 没有 scheme 之分，多数工具（curl、git、pip）把它
//     当成 socks5 代理。若通道是 http:// 却写成 ALL_PROXY=http://...，
//     一部分只读 ALL_PROXY 的工具会按 socks5 去握手，直接握手失败。
//     反过来 socks5 通道设 ALL_PROXY 是最正确的表达。
//   - NO_PROXY 保留用户已有的值并追加回环地址，
//     不覆盖用户自己的 bypass 列表（曾用注册表 HKCU\Environment 的值）。
func BuildEnv(ch channel.Channel, extra map[string]string) []string {
	env := os.Environ()

	proxyURL := ch.URL()
	env = setEnv(env, "HTTP_PROXY", proxyURL)
	env = setEnv(env, "http_proxy", proxyURL)
	env = setEnv(env, "HTTPS_PROXY", proxyURL)
	env = setEnv(env, "https_proxy", proxyURL)

	// ALL_PROXY 只在纯 socks5 通道下注入。
	if ch.Scheme == channel.SchemeSOCKS5 {
		env = setEnv(env, "ALL_PROXY", proxyURL)
		env = setEnv(env, "all_proxy", proxyURL)
	} else {
		// http 通道下**不设** ALL_PROXY。
		// 用户环境里若已有一个指向别的代理的 ALL_PROXY，这里从**子进程**
		// 环境里去掉它——否则只读 ALL_PROXY 的子工具会走错代理。
		// 注意这只影响我们 spawn 出来的子进程，不改用户自己的环境，
		// doctor 也会把"检测到你有 ALL_PROXY，已在子进程中忽略"报出来。
		env = unsetEnv(env, "ALL_PROXY")
		env = unsetEnv(env, "all_proxy")
	}

	env = setEnv(env, "NO_PROXY", channel.JoinNoProxy(os.Getenv("NO_PROXY")))
	env = setEnv(env, "no_proxy", channel.JoinNoProxy(os.Getenv("no_proxy")))

	for k, v := range extra {
		env = setEnv(env, k, v)
	}
	return env
}

// unsetEnv 删除指定的环境变量（大小写两种都删）。
func unsetEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// setEnv 替换已有键或追加新键。
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0:0]
	replaced := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			if replaced {
				continue // 去重：同名只保留一条
			}
			out = append(out, prefix+value)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

// Launch 启动目标程序。
// 返回已启动的进程（Detached 时进程可能仍在运行）。
func Launch(ctx context.Context, opts Options) (*exec.Cmd, error) {
	if opts.Target == "" {
		return nil, fmt.Errorf("未指定启动目标")
	}
	if st, err := os.Stat(opts.Target); err != nil || st.IsDir() {
		return nil, fmt.Errorf("目标不存在或不可执行: %s", opts.Target)
	}

	wd := opts.WorkingDir
	if wd == "" {
		wd = filepath.Dir(opts.Target)
	}

	cmd := exec.Command(opts.Target, opts.Args...)
	cmd.Dir = wd
	cmd.Env = BuildEnv(opts.Channel, opts.ExtraEnv)

	if opts.Detached {
		// 桌面版：脱离当前进程，父进程退出不影响它。
		cmd.SysProcAttr = detachAttrs()
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		_ = cmd.Process.Release()
		return cmd, nil
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// detachAttrs 返回让子进程脱离当前进程组的属性。
// 平台实现见 launcher_unix.go / launcher_windows.go。

// RunInteractive 以前台交互方式运行目标程序（CLI 模式）。
// stdio 直接连到当前终端，用户可以正常对话。
func RunInteractive(ctx context.Context, opts Options) error {
	cmd := exec.CommandContext(ctx, opts.Target, opts.Args...)
	cmd.Dir = opts.WorkingDir
	cmd.Env = BuildEnv(opts.Channel, opts.ExtraEnv)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// PIDFile 返回桌面版的 userData 目录名，供运行态探测使用。
// Antigravity 的 userData 来自 Electron 的 app.getPath('userData')，
// Windows 上是 %APPDATA%\antigravity，Linux 是 ~/.config/antigravity。
func UserDataDir() string {
	switch runtime.GOOS {
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "antigravity")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "antigravity")
		}
	case "linux":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".config", "antigravity")
		}
	}
	return ""
}

// DevToolsActivePortPath 返回 DevToolsActivePort 文件路径。
// 该文件由 Electron 写入，内容是 "<port>\n/devtools/browser/<uuid>"，
// 是判断"桌面版是否正在运行"的跨平台可靠依据
// （不依赖进程名或命令行格式，各平台一致）。
func DevToolsActivePortPath() string {
	dir := UserDataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "DevToolsActivePort")
}

// RunningInstance 描述一个检测到的运行中桌面版实例。
type RunningInstance struct {
	Port int
	// Version 是 /json/version 报告的版本（可能为空）。
	Version string
}

// CheckRunning 检测 Antigravity 桌面版是否正在运行。
//
// 实现：读 DevToolsActivePort 拿到端口，再请求
// http://127.0.0.1:<port>/json/version。
//
// 关键：文件可能**残留**——Electron 异常退出或被强杀时不会删它
// （实测：杀掉全部 Antigravity 进程后该文件仍在，内容还是旧端口）。
// 因此仅凭文件存在就判定"在运行"会误报，进而让启动器去 kill
// 一个已经死掉的进程（taskkill 返回 exit 128）。
// 正确做法是必须连上 CDP 端点才算在运行。
func CheckRunning(timeout time.Duration) (RunningInstance, bool) {
	path := DevToolsActivePortPath()
	if path == "" {
		return RunningInstance{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return RunningInstance{}, false
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return RunningInstance{}, false
	}
	var port int
	if _, err := fmt.Sscanf(fields[0], "%d", &port); err != nil || port <= 0 {
		return RunningInstance{}, false
	}

	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	// 必须真的连得上 CDP 才算在运行。
	version, alive := fetchBrowserVersion(port, timeout)
	if !alive {
		return RunningInstance{}, false
	}
	return RunningInstance{Port: port, Version: version}, true
}
