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
// ## 关键细节一：覆盖是"按名字逐个"的，不是"整个环境块替换"
//
// `{ ...process.env, ...loginShellEnv }` 是逐键覆盖：
// loginShellEnv 里**没有**的键，process.env 的值原样保留。
//
// 所以只有「登录 shell 显式 export 了同名代理变量」才会覆盖我们。
//
// ## 关键细节二：登录 shell **继承**了我们注入的值
//
// dist/loginShellEnv.js 的 spawn 带着：
//
//	env: { ...process.env, ...LOGIN_SHELL_INJECTED_ENV }
//
// 也就是登录 shell 从我们注入的值出发。只要用户的 rc 文件
// 没有再显式 export 同一个名字，loginShellEnv 里的代理值
// 就是我们的值 —— **不是冲突**。
//
// 典型会被误判的场景：用户的 .bashrc 里有
//
//	export HTTP_PROXY=${HTTP_PROXY:-http://127.0.0.1:7890}
//
// 这行只在**父进程没有设 HTTP_PROXY** 时才生效。
// 预检时必须把注入值带进探测用的 shell，否则会读到 7890，
// 误判成致命冲突——而真正启动时父进程是有注入值的。
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
	"bytes"
	"context"
	"fmt"
	"io"
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

// AntigravityInjectedEnv 是 Antigravity 自己注入给登录 shell 的变量。
//
// 来自 dist/loginShellPolicy.js 的 LOGIN_SHELL_INJECTED_ENV。
// 预检用的 shell 必须带上同样的值，否则行为与真正启动时不一致：
// 有的用户在 .zshrc 里判断 ANTIGRAVITY_RESOLVING_ENVIRONMENT
// 直接跳过耗时初始化，带上它才不会白等。
var AntigravityInjectedEnv = map[string]string{
	"ANTIGRAVITY_RESOLVING_ENVIRONMENT": "1",
	"DISABLE_AUTO_UPDATE":               "true",
	"ZSH_TMUX_AUTOSTARTED":              "true",
	"ZSH_TMUX_AUTOSTART":                "false",
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

// Result 是一次预检的结果。
type Result struct {
	// Conflicts 是查到的冲突。
	Conflicts []Conflict
	// Inconclusive 为 true 表示**没能读到**登录 shell 的环境
	// （探测失败或超时），因此无法确认有没有冲突。
	//
	// 这不等于"没有冲突"：Antigravity 自己最多等
	// LOGIN_SHELL_ENV_WAIT_SEC(10s)，我们的预检只等 5s，
	// 所以存在一个 5–10s 的窗口：预检超时放弃，
	// Antigravity 却仍然读到了环境并可能撞上致命冲突。
	// 因此调用方应当**警告**，而不是当作干净。
	Inconclusive bool
	// Err 是探测失败的原因（Inconclusive 时有值）。
	Err error
}

// Fatal 返回致命的那部分冲突。
func (r Result) Fatal() []Conflict {
	var out []Conflict
	for _, c := range r.Conflicts {
		if c.Severity == SeverityFatal {
			out = append(out, c)
		}
	}
	return out
}

// Detect 在登录 shell 的环境里做只读预检。
//
// 只在 macOS/Linux 有意义；Windows 上 Antigravity 的
// readLoginShellEnv 直接返回 {}，不存在这个覆盖问题。
//
// 做法：跑一次 `SHELL -ilc 'env'`，解析出代理变量。
//
// ⚠️ **探测用的 shell 必须带上我们要注入的变量**（见 buildProbeEnv），
// 否则用户 .bashrc 里 `${HTTP_PROXY:-...}` 这类写法会让预检
// 读到一个真启动时根本不存在的值。
//
// 注意这是**只读**的：不改任何 rc 文件，不改任何环境设置。
// 只是「看一眼登录 shell 里有什么」。
//
// timeout 给 5 秒：慢的 rc 文件会让它超时，超时按「查不到」处理
// （Result.Inconclusive = true），不阻塞启动。
func Detect(timeout time.Duration, inj Injection) Result {
	if runtime.GOOS == "windows" {
		return Result{}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	shell := defaultShell()
	if shell == "" {
		return Result{Inconclusive: true, Err: fmt.Errorf("找不到登录 shell")}
	}

	// 与 Antigravity 自己的做法一致：`-ilc` 交互式登录 shell。
	// stderr 丢掉（rc 文件的告警噪音）。
	//
	// ⚠️ **必须用带 context 的版本**：exec.Command + Output() 没有超时，
	// 一个卡住的 rc 文件（比如等输入、或起了个后台任务不退出）
	// 会让启动器永久卡住。实测之前这里就是直接 Command(...).Output()，
	// timeout 参数形同虚设。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// ⚠️ 用 Antigravity 的**分隔符取帧**方式，不用裸 env。
	//
	// 原因：cmd.Output() 要等 stdout **EOF**。如果用户的 rc 文件
	// 起了个后台任务、它持有 stdout 不退出，预检就会一直等到超时，
	// 每次启动都报"无法确认"。Antigravity 的做法是：
	// 命令自己打一个分隔符，解析器在读到收尾分隔符时就停，
	// 不等 EOF。
	//
	// 见 dist/loginShellEnv.js 的 COMMAND 与 parseShellEnvOutput。
	cmd := exec.CommandContext(ctx, shell, "-ilc", probeCommand())
	// ⚠️ 关键：把注入值带进探测用的 shell，让预检与真启动一致。
	cmd.Env = buildProbeEnv(inj)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Inconclusive: true, Err: err}
	}
	if err := cmd.Start(); err != nil {
		return Result{Inconclusive: true, Err: err}
	}

	frame, err := readFramedEnv(stdout, timeout)
	// 无论成败都要收尸，否则会留下僵尸进程。
	_ = cmd.Wait()
	if err != nil {
		// 查不到**不等于**没有冲突：只能报告"无法确认"。
		return Result{Inconclusive: true, Err: err}
	}

	return Result{Conflicts: classify(frame, inj)}
}

// probeDelimiter 是取帧用的分隔符。
//
// 与 Antigravity 的 '_AGY_SHELL_ENV_DELIMITER_' 同思路：
// 一个足够独特的串，rc 文件里几乎不可能出现。
const probeDelimiter = "_AGY_UNTUN_SHELL_ENV_DELIMITER_"

// probeCommand 返回探测用的 shell 命令。
//
// 与 dist/loginShellEnv.js 的 COMMAND 结构一致：
//
//	echo -n "<分隔符>"; /usr/bin/env; echo -n "<分隔符>"; exit
//
// 收尾的 exit 是必须的：交互式登录 shell 在 -c 之后可能仍不退出。
func probeCommand() string {
	return fmt.Sprintf(`echo -n "%s"; /usr/bin/env; echo -n "%s"; exit`,
		probeDelimiter, probeDelimiter)
}

// readFramedEnv 从 stdout 里取出分隔符之间的那一帧。
//
// 与 Antigravity 的 parseShellEnvOutput 同思路：
//   - 帧从第一个分隔符开始；
//   - 帧在**行首**的下一个分隔符处结束
//     （env 只在 dash 的 `_=<分隔符>` 形式里打印过一次分隔符，
//     而 rc 文件留下的后台任务可能在帧之后继续打印）；
//   - 读到帧就返回，**不等 EOF**。
//
// 返回的字符串是帧内的原始文本（交给 classify 解析）。
func readFramedEnv(r io.Reader, timeout time.Duration) (string, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 8192)
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("读取登录 shell 环境超时（%v）", timeout)
		}
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			if frame, ok := extractFrame(buf.String()); ok {
				return frame, nil
			}
		}
		if err != nil {
			if frame, ok := extractFrame(buf.String()); ok {
				return frame, nil
			}
			return "", err
		}
	}
}

// extractFrame 从累积的输出里切出那一帧。
//
// 找不到完整的帧时返回 ok=false（还要继续读）。
func extractFrame(s string) (string, bool) {
	start := strings.Index(s, probeDelimiter)
	if start < 0 {
		return "", false
	}
	rest := s[start+len(probeDelimiter):]
	end := strings.Index(rest, "\n"+probeDelimiter)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// buildProbeEnv 构造探测用登录 shell 的环境。
//
// 目标是**尽量贴近 Antigravity 真正启动时的环境**：
//
//  1. 继承当前进程的环境（Antigravity 是
//     `env: { ...process.env, ...LOGIN_SHELL_INJECTED_ENV }`）。
//  2. 覆盖成本次准备注入的代理变量（大小写两种都设），
//     这样用户 rc 里的 `${HTTP_PROXY:-默认值}` 不会生效，
//     预检读到的就是我们真正会注入的值。
//  3. 带上 Antigravity 的 LOGIN_SHELL_INJECTED_ENV 四个变量。
//  4. ALL_PROXY：只在本次**会注入**它时才设；否则要**移除**，
//     与 launcher.BuildEnv 的行为一致（http 通道下 ALL_PROXY 必须清掉）。
//
// ⚠️ 空值**不写**进去：Injection 的字段可能为空（例如 doctor
// 只想看当前环境），此时必须保留继承来的值。写空值会把
// 用户 rc 里的 `${HTTP_PROXY:-默认值}` 变成"有变量但为空"，
// 于是默认值生效 —— 正好制造我们要避免的误报。
func buildProbeEnv(inj Injection) []string {
	env := os.Environ()

	// Antigravity 自己注入的四个变量。
	for k, v := range AntigravityInjectedEnv {
		env = setEnv(env, k, v)
	}

	// 本次准备注入的代理变量。
	setIfNotEmpty := func(env []string, key, value string) []string {
		if value == "" {
			return env
		}
		return setEnv(env, key, value)
	}
	env = setIfNotEmpty(env, "HTTP_PROXY", inj.HTTPProxy)
	env = setIfNotEmpty(env, "http_proxy", inj.HTTPProxy)
	env = setIfNotEmpty(env, "HTTPS_PROXY", inj.HTTPSProxy)
	env = setIfNotEmpty(env, "https_proxy", inj.HTTPSProxy)
	env = setIfNotEmpty(env, "NO_PROXY", inj.NoProxy)
	env = setIfNotEmpty(env, "no_proxy", inj.NoProxy)

	if inj.AllProxy != "" {
		env = setEnv(env, "ALL_PROXY", inj.AllProxy)
		env = setEnv(env, "all_proxy", inj.AllProxy)
	} else {
		// 本次不注入 ALL_PROXY → 与 launcher.BuildEnv 一致地清掉。
		env = unsetEnv(env, "ALL_PROXY")
		env = unsetEnv(env, "all_proxy")
	}

	return env
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

		// ⚠️ 我们**没有**注入这个变量（w == ""），而登录 shell 有。
		// 这不是"shell 覆盖了我们"——是我们压根没管这个变量，
		// 真启动时它同样会生效。所以按"值不同"的常规档处理。
		// 大写名的仍然致命：那正是真启动时会被读到的值。
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

// setEnv 替换已有键或追加新键。
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0:0]
	replaced := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			if replaced {
				continue // 去重
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

// unsetEnv 删除指定的环境变量。
func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
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

// ReportInconclusive 说明「没能确认」这件事。
//
// 措辞很重要：不是"没有冲突"，而是"没能读到登录 shell 的环境，
// 无法确认"。Antigravity 自己最多等 10s，我们只等 5s，
// 所以这个窗口是真实存在的。
func ReportInconclusive(err error) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("========================================================\n")
	b.WriteString(" 警告：没能读取登录 shell 的环境，无法确认有无冲突\n")
	b.WriteString("========================================================\n\n")
	if err != nil {
		b.WriteString(fmt.Sprintf("原因: %v\n\n", err))
	}
	b.WriteString("Antigravity 自己最多等 10 秒读取登录 shell 环境，本次预检只等 5 秒。\n")
	b.WriteString("如果你的 rc 文件很慢，可能出现「预检放弃、Antigravity 读到了并撞上冲突」的情况。\n")
	b.WriteString("若启动后通道没生效，请手动检查 rc 文件里的代理 export。\n")
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
