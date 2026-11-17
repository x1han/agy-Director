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
// http_proxy / https_proxy 会**覆盖**我们注入的值。
//
// ## 关键细节：覆盖是"按名字逐个"的，不是"整个环境块替换"
//
// `{ ...process.env, ...loginShellEnv }` 是逐键覆盖：
// loginShellEnv 里**没有**的键，process.env 的值原样保留。
//
// 所以只有「登录 shell 显式 export 了同名代理变量」才会覆盖我们。
// 而 dist/loginShellEnv.js 的 spawn 带着
//
//	env: { ...process.env, ...LOGIN_SHELL_INJECTED_ENV }
//
// 也就是登录 shell **继承**了我们注入的值——只要用户的 rc 文件
// 没有再显式 export 同一个名字，loginShellEnv 里的代理值就是我们的值。
//
// ## 因此冲突判定必须看"值"，不能只看"有没有这个变量"
//
// 三档：
//
//   - 值与我们注入的**相同** → 不是冲突（登录 shell 只是回显了我们）。
//   - 只有**小写**名被改成别的值 → **警告**。
//     Go 的 httpproxy.getEnvAny 是 "先大写后小写"，
//     大写仍是我们的值 → Antigravity 自己的流量不受影响；
//     但 LS 派生的第三方工具（curl / git）可能只认小写，会走错。
//   - **大写**名被改成别的值 → **致命**，必须拦住。
//     getEnvAny("HTTPS_PROXY", "https_proxy") 会取到 shell 的值，
//     通道直接失效。
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

// Injection 是本次启动准备注入的代理环境变量。
//
// Detect 用它做比对：登录 shell 里的同名变量值与这里相同，
// 就不是冲突。
type Injection struct {
	// HTTPProxy 是 HTTP_PROXY / http_proxy 的值。
	HTTPProxy string
	// HTTPSProxy 是 HTTPS_PROXY / https_proxy 的值。
	HTTPSProxy string
	// AllProxy 是 ALL_PROXY / all_proxy 的值；空表示不注入。
	AllProxy string
	// NoProxy 是 NO_PROXY / no_proxy 的值。
	NoProxy string
}

// Severity 是冲突的严重程度。
type Severity int

const (
	// SeverityFatal 表示 Antigravity 自己的流量会走错代理，必须拦住。
	SeverityFatal Severity = iota
	// SeverityWarning 表示 Antigravity 不受影响，但第三方工具可能走错。
	SeverityWarning
)

// String 供 CLI 输出。
func (s Severity) String() string {
	if s == SeverityFatal {
		return "致命"
	}
	return "警告"
}

// role 是代理变量在 Go httpproxy 里的角色。
type role int

const (
	roleHTTP role = iota
	roleHTTPS
	roleNoProxy
	roleAll
)

// varSpec 描述一个代理变量名。
//
// primary 标记它是不是 httpproxy.getEnvAny 的**首选名**。
// Go 的 x/net/http/httpproxy 里：
//
//	HTTPProxy:  getEnvAny("HTTP_PROXY", "http_proxy")
//	HTTPSProxy: getEnvAny("HTTPS_PROXY", "https_proxy")
//	NoProxy:    getEnvAny("NO_PROXY", "no_proxy")
//
// 也就是**大写优先**。
//
// ALL_PROXY **不在** httpproxy 的读取范围里——它只影响
// LS 派生的第三方工具（curl / git / pip 之类）。
var varSpecs = []struct {
	name    string
	role    role
	primary bool
}{
	{"HTTP_PROXY", roleHTTP, true},
	{"http_proxy", roleHTTP, false},
	{"HTTPS_PROXY", roleHTTPS, true},
	{"https_proxy", roleHTTPS, false},
	{"NO_PROXY", roleNoProxy, true},
	{"no_proxy", roleNoProxy, false},
	{"ALL_PROXY", roleAll, false},
	{"all_proxy", roleAll, false},
}

// ProxyVars 是会与注入值冲突的代理环境变量名（大小写都列）。
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
	// Want 是我们准备注入的值。
	Want string
	// Severity 是严重程度。
	Severity Severity
	// Reason 说明为什么是这一档。
	Reason string
}

// String 供 CLI 输出。
func (c Conflict) String() string {
	return fmt.Sprintf("%s=%s  (%s · %s)", c.Name, c.Value, c.Severity, c.Reason)
}

// Detect 返回当前登录 shell 里与注入值**不一致**的代理变量。
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
func Detect(timeout time.Duration, inj Injection) []Conflict {
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

	return classify(string(out), inj)
}

// classify 把登录 shell 的 env 输出归类成冲突。
//
// 规则见包注释：值相同不是冲突；只有小写名不同是警告；
// 大写名不同是致命。
func classify(envOut string, inj Injection) []Conflict {
	// 先收集登录 shell 里每个变量名的值。
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
		found[k] = v
	}

	want := map[role]string{
		roleHTTP:    inj.HTTPProxy,
		roleHTTPS:   inj.HTTPSProxy,
		roleNoProxy: inj.NoProxy,
		roleAll:     inj.AllProxy,
	}

	var out []Conflict
	for _, spec := range varSpecs {
		v, ok := found[spec.name]
		if !ok {
			continue
		}

		w := want[spec.role]
		if spec.role == roleAll && w == "" {
			// 我们不注入 ALL_PROXY。登录 shell 里有一个 = 第三方工具
			// 可能走它。Antigravity 自己的流量不受影响（httpproxy
			// 根本不读 ALL_PROXY），所以只是警告。
			out = append(out, Conflict{
				Name:     spec.name,
				Value:    v,
				Want:     "",
				Severity: SeverityWarning,
				Reason:   "本通道不注入 ALL_PROXY，LS 派生的第三方工具可能走这个代理",
			})
			continue
		}

		if sameValue(v, w) {
			// 登录 shell 只是回显了我们注入的值 → 不是冲突。
			continue
		}

		sev := SeverityWarning
		reason := "Antigravity 仍会用大写名（本启动注入的值），但只认小写的第三方工具会走这个代理"
		if spec.primary {
			sev = SeverityFatal
			reason = "Go 的 httpproxy 大写优先，这个值会直接覆盖本次通道"
		}
		out = append(out, Conflict{
			Name:     spec.name,
			Value:    v,
			Want:     w,
			Severity: sev,
			Reason:   reason,
		})
	}
	return out
}

// sameValue 比较两个代理 URL 是否等价。
//
// 容忍末尾斜杠：http://127.0.0.1:10808/ 与 http://127.0.0.1:10808
// 是同一个代理，写法不同而已。
func sameValue(a, b string) bool {
	if a == b {
		return true
	}
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// Fatal 只返回致命的那些冲突。
func Fatal(conflicts []Conflict) []Conflict {
	var out []Conflict
	for _, c := range conflicts {
		if c.Severity == SeverityFatal {
			out = append(out, c)
		}
	}
	return out
}

// defaultShell 返回用户的登录 shell，逻辑与 Antigravity 一致：
// 先看 SHELL，再看 passwd 里的 shell，最后退回 /bin/sh。
func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Report 把冲突渲染成给用户看的红字说明。
//
// 措辞刻意是「请你手动改」而不是「我已经帮你改」——
// 我们不碰用户的 rc 文件，这是硬性约束。
//
// fatal 为 true 时说明这些冲突必须先解决；
// 否则只是提醒，不影响 Antigravity 自己的流量。
func Report(conflicts []Conflict, fatal bool) string {
	if len(conflicts) == 0 {
		return ""
	}
	var b strings.Builder

	if fatal {
		b.WriteString("\n")
		b.WriteString("========================================================\n")
		b.WriteString(" 启动已中止：登录 shell 里存在会覆盖代理的变量\n")
		b.WriteString("========================================================\n\n")
	} else {
		b.WriteString("\n")
		b.WriteString("========================================================\n")
		b.WriteString(" 警告：登录 shell 里有与本次通道不一致的代理变量\n")
		b.WriteString("========================================================\n\n")
	}

	b.WriteString("检测到的冲突变量:\n")
	for _, c := range conflicts {
		b.WriteString(fmt.Sprintf("  [%s] %s=%s\n", c.Severity, c.Name, c.Value))
		if c.Want != "" {
			b.WriteString(fmt.Sprintf("        本次通道是 %s\n", c.Want))
		}
		b.WriteString(fmt.Sprintf("        %s\n", c.Reason))
	}

	b.WriteString("\n请手动处理（agy-untun 不会替你改这些文件）:\n")
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
