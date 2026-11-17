// Package shellenv 负责 Unix 上「登录 shell 环境覆盖注入环境」的问题。
//
// ## 背景（已实证，Antigravity 2.32.1 / Electron 44.9.1）
//
// Antigravity 的 dist/languageServer.js 里：
//
//	const env = { ...process.env, ...loginShellEnv };
//
// 其中 loginShellEnv 来自 dist/loginShellEnv.js：在 win32 直接返回
// `{}`；在 macOS/Linux 会用 `SHELL -ilc COMMAND` 跑一次登录 shell，
// 把它的环境解析出来。
//
// dist/loginShellPolicy.js 的 SKIPPED_VARS 是：
//
//	injected env 的名字 ∪ dropped vars
//	= {ANTIGRAVITY_RESOLVING_ENVIRONMENT, DISABLE_AUTO_UPDATE,
//	   ZSH_TMUX_AUTOSTARTED, ZSH_TMUX_AUTOSTART}
//	∪ {PWD, OLDPWD, SHLVL, _, PS1, PS2, PROMPT_COMMAND,
//	    TERM, COLUMNS, LINES, SSH_CLIENT}
//
// **代理变量不在这个集合里**，所以登录 shell 里 export 的
// http_proxy / https_proxy / all_proxy 会**覆盖**我们注入的
// HTTPS_PROXY / HTTP_PROXY（后者在后面展开，优先级更高）。
//
// ## 结论：没有零侵入的绕过手段
//
// 我们查过所有可能：asar 里没有任何 "skip shell env" 之类的开关，
// 没有 ANTIGRAVITY_* 环境变量能关掉它，--proxy-server 也不影响
// 这一段（那只管 Chromium 的网络栈）。所以唯一能保证正确性的做法是：
//
//	**检测到冲突就报错拦住，把冲突的变量名打印出来，让用户自己去改。**
//
// ## 绝对禁止的事情（专家裁定）
//
//	绝对禁止触碰用户的 shell profile 文件（.bashrc / .zshrc / .profile）。
//	自动改写它们是恶意软件级别的侵入行为。
//	同样禁止修改系统级代理或全局环境变量。
//
// 本包只做**只读**检测：不写任何文件，不改任何全局设置。
package shellenv

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ProxyVars 是会与注入值冲突的代理环境变量名。
//
// 大小写两种形式都列：Go 的 httpproxy.FromEnvironment 两种都认，
// 而用户 rc 文件里通常写的是小写。
var ProxyVars = []string{
	"http_proxy", "https_proxy", "all_proxy",
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
}

// Conflict 是检测到的一个冲突。
type Conflict struct {
	// Name 是变量名，如 "http_proxy"。
	Name string
	// Value 是登录 shell 里的值。
	Value string
	// Source 是它在哪个文件里被设置的（如果能定位到）。
	Source string
}

// String 供 CLI 输出。
func (c Conflict) String() string {
	if c.Source != "" {
		return fmt.Sprintf("%s=%s  (%s)", c.Name, c.Value, c.Source)
	}
	return fmt.Sprintf("%s=%s", c.Name, c.Value)
}

// Detect 返回当前登录 shell 里与注入值冲突的代理变量。
//
// 只在 macOS/Linux 有意义；Windows 上 Antigravity 的
// readLoginShellEnv 直接返回 {}，不存在这个覆盖问题，返回 nil。
//
// 做法：跑一次 `SHELL -ilc 'env'`，解析出代理变量。
//
// 注意这是**只读**的：不改任何 rc 文件，不改任何环境设置。
// 只是「看一眼登录 shell 里有什么」。
//
// timeout 给 5 秒：慢的 rc 文件会让它超时，超时按「查不到」处理，
// 不阻塞启动。
func Detect(timeout time.Duration) []Conflict {
	if runtime.GOOS == "windows" {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	shell := defaultShell()
	if shell == "" {
		return nil
	}

	// 与 Antigravity 自己的做法一致：`-ilc` 交互式登录 shell。
	// stdout 只取 env 的输出；stderr 丢掉（rc 文件的告警噪音）。
	//
	// ⚠️ **必须用带 context 的版本**：exec.Command + Output() 没有超时，
	// 一个卡住的 rc 文件（比如等输入、或起了个后台任务不退出）
	// 会让启动器永久卡住。实测之前这里就是直接 Command(...).Output()，
	// timeout 参数形同虚设。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-ilc", "env")
	out, err := cmd.Output()
	if err != nil {
		// 查不到不是错误，只是「无法确认是否有冲突」。
		// 超时也走这条路：查不到就当没有冲突，让用户正常启动。
		return nil
	}

	return parseProxyVars(string(out))
}

// defaultShell 返回用户的登录 shell，逻辑与 Antigravity 一致：
// 先看 SHELL，再看 passwd 里的 shell，最后退回 /bin/sh。
func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// parseProxyVars 从 env 的输出里挑出代理变量。
func parseProxyVars(envOut string) []Conflict {
	found := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(envOut))
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		k, v := line[:idx], line[idx+1:]
		if v == "" {
			continue
		}
		for _, w := range ProxyVars {
			if k == w {
				found[k] = v
			}
		}
	}
	if len(found) == 0 {
		return nil
	}

	var out []Conflict
	for _, w := range ProxyVars {
		if v, ok := found[w]; ok {
			out = append(out, Conflict{Name: w, Value: v})
		}
	}
	return out
}

// Report 把冲突渲染成给用户看的红字说明。
//
// 措辞刻意是「请你手动改」而不是「我已经帮你改」——
// 我们不碰用户的 rc 文件，这是硬性约束。
func Report(conflicts []Conflict) string {
	if len(conflicts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("========================================================\n")
	b.WriteString(" 启动已中止：登录 shell 里存在会覆盖代理的变量\n")
	b.WriteString("========================================================\n\n")
	b.WriteString("检测到的冲突变量:\n")
	for _, c := range conflicts {
		b.WriteString(fmt.Sprintf("  %s=%s\n", c.Name, c.Value))
	}
	b.WriteString("\n原因:\n")
	b.WriteString("  Antigravity 会用登录 shell 的环境覆盖启动器注入的代理变量,\n")
	b.WriteString("  所以这些变量指向的代理会生效, 而不是本次选定的通道。\n\n")
	b.WriteString("请手动处理（agy-untun 不会替你改这些文件）:\n")
	b.WriteString("  1. 编辑你的 shell 配置文件, 移除上面的 export, 例如:\n")
	b.WriteString("       $EDITOR ~/.bashrc     # 或 ~/.zshrc / ~/.profile\n")
	b.WriteString("  2. 或者注释掉它们, 例如:\n")
	b.WriteString("       # unset http_proxy https_proxy\n")
	b.WriteString("  3. 然后重新打开一个终端, 再运行本程序。\n\n")
	b.WriteString("说明: 这不是本程序的 bug, 是 Antigravity 的既定行为\n")
	b.WriteString("      (dist/loginShellEnv.js 读取登录 shell 环境,\n")
	b.WriteString("       dist/languageServer.js 用它覆盖 process.env)。\n")
	return b.String()
}

// ConflictsWith 判断某个变量名是否属于冲突集合。
func ConflictsWith(name string) bool {
	for _, w := range ProxyVars {
		if w == name {
			return true
		}
	}
	return false
}
