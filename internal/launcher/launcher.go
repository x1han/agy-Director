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
// 注意 NO_PROXY 的处理：保留用户已有的值并追加回环地址，
// 避免覆盖用户自己的 bypass 列表（曾用注册表 HKCU\Environment 的值）。
func BuildEnv(ch channel.Channel, extra map[string]string) []string {
	env := os.Environ()

	proxyURL := ch.URL()
	// 同时设置大小写两种形式：Go 的 httpproxy 同时认
	// HTTP_PROXY/HTTPS_PROXY 和 http_proxy/https_proxy，
	// 但某些子工具只认小写。
	env = setEnv(env, "HTTP_PROXY", proxyURL)
	env = setEnv(env, "http_proxy", proxyURL)
	env = setEnv(env, "HTTPS_PROXY", proxyURL)
	env = setEnv(env, "https_proxy", proxyURL)
	env = setEnv(env, "ALL_PROXY", proxyURL)
	env = setEnv(env, "all_proxy", proxyURL)

	env = setEnv(env, "NO_PROXY", channel.JoinNoProxy(os.Getenv("NO_PROXY")))
	env = setEnv(env, "no_proxy", channel.JoinNoProxy(os.Getenv("no_proxy")))

	for k, v := range extra {
		env = setEnv(env, k, v)
	}
	return env
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
