# agy-untun — 专家评审意见整改说明

**绝对路径**：`D:\agy-untun\docs\agy-untun-expert-response-20261117-1650.md`
**对应提交**：`e307c4d`（`D:\agy-untun`）
**整改范围**：上一轮评审提出的 P0/P1 全部条目 + 你们点名要的三样东西。

---

## 0. 三句话摘要

1. **P0 全端口扫描已删除**，改成「系统代理（只读）→ 本机监听表 → 常见端口白名单」三级来源。Windows 监听表那一路此前**完全失效**（返回 error 87），本轮修好后与 `netstat` 的 53 条 LISTENING **逐条一致**。
2. **你们点名的三个问题**在 §1/§2/§3 分别回答：`loginShellEnv.js` 与 `main.js` 的完整相关代码、`internal/discovery/discovery.go` 全文、以及 `Channel.Launch` 的处置结论（**已删**）。
3. **整改过程中我自己写出一个 P0 级误杀 bug**：terminate 的判据把 Chrome / Edge 的渲染进程一起杀了（用户实际受害）。已修复，并加了干跑命令 `doctor --processes` 作为回归闸门。

---

## 1. 你们要的：`dist/loginShellEnv.js` 完整代码 + `dist/main.js` 相关代码

**提取方式**：`app.asar` 是 asar 打包格式，头部 JSON 给出每个文件的 offset/size，数据区起点 = `16 + align4(headerLen)`。下面是从 `resources\app.asar` 里解出的原文（Antigravity 2.32.1 / Electron 44.9.1）。

### 1.1 `dist/loginShellPolicy.js`（全文，1515 字节）

```js
/** Set for the login shell, and never taken from it. */
exports.LOGIN_SHELL_INJECTED_ENV = {
    ANTIGRAVITY_RESOLVING_ENVIRONMENT: '1',
    DISABLE_AUTO_UPDATE: 'true',
    ZSH_TMUX_AUTOSTARTED: 'true',
    ZSH_TMUX_AUTOSTART: 'false',
};
/** Never taken from the login shell: its own state, and SSH_CLIENT. */
exports.LOGIN_SHELL_DROPPED_VARS = [
    'PWD', 'OLDPWD', 'SHLVL', '_', 'PS1', 'PS2',
    'PROMPT_COMMAND', 'TERM', 'COLUMNS', 'LINES', 'SSH_CLIENT',
];
exports.LOGIN_SHELL_ENV_WAIT_SEC = 10;
exports.LOGIN_SHELLS_READING_STDIN = ['csh', 'tcsh'];
```

### 1.2 `dist/loginShellEnv.js`（关键部分全文）

```js
const DELIMITER = '_AGY_SHELL_ENV_DELIMITER_';
const COMMAND = `echo -n "${DELIMITER}"; /usr/bin/env; echo -n "${DELIMITER}"; exit`;
const MAX_OUTPUT_CHARS = 1024 * 1024;
const LOAD_BUDGET_MS = 60000;
const KILL_GRACE_MS = 1000;

/** Never taken from the shell. */
const SKIPPED_VARS = new Set([
    ...Object.keys(loginShellPolicy_1.LOGIN_SHELL_INJECTED_ENV),
    ...loginShellPolicy_1.LOGIN_SHELL_DROPPED_VARS,
]);

function parseShellEnvOutput(stdout) {
    const start = stdout.indexOf(DELIMITER);
    const end = start < 0 ? -1 : stdout.indexOf(`\n${DELIMITER}`, start + DELIMITER.length);
    if (end < 0) { return undefined; }
    const env = {};
    let any = false;
    const frame = stdout.slice(start + DELIMITER.length, end);
    for (const line of stripAnsi(frame).split('\n')) {
        const eq = line.indexOf('=');
        if (eq > 0) {
            any = true;
            const name = line.slice(0, eq);
            if (!SKIPPED_VARS.has(name)) {
                env[name] = line.slice(eq + 1);   // ← 代理变量不在 SKIPPED_VARS 里
            }
        }
    }
    return any ? env : undefined;
}

function defaultShell() {
    if (process.env.SHELL) { return process.env.SHELL; }
    try {
        const shell = os.userInfo().shell;
        if (shell) { return shell; }
    } catch { /* No passwd entry. */ }
    return '/bin/sh';
}
const FALLBACK_SHELLS = ['/bin/zsh', '/bin/bash'];

function readLoginShellEnv(opts) {
    if (process.platform === 'win32') {
        return Promise.resolve({});            // ← Windows 直接返回空
    }
    return new Promise((resolve, reject) => {
        const onStdin = loginShellPolicy_1.LOGIN_SHELLS_READING_STDIN.includes(path.basename(opts.shell));
        const child = (0, child_process_1.spawn)(opts.shell, onStdin ? ['-l'] : ['-ilc', COMMAND], {
            stdio: [onStdin ? 'pipe' : 'ignore', 'pipe', 'ignore'],
            detached: true,
            env: { ...process.env, ...loginShellPolicy_1.LOGIN_SHELL_INJECTED_ENV },
        });
        // … 超时 / 收尾逻辑见下 …
        const timer = setTimeout(() => giveUp(`timed out after ${opts.timeoutMs} ms`), opts.timeoutMs);
        child.stdout?.setEncoding('utf8');
        child.stdout?.on('data', (chunk) => {
            stdout += chunk;
            const env = parseShellEnvOutput(stdout);
            if (env) { finish(() => resolve(env)); }
            else if (stdout.length >= MAX_OUTPUT_CHARS) { giveUp(...); }
        });
        child.on('close', (code) => {
            const env = parseShellEnvOutput(stdout);
            finish(() => env ? resolve(env)
                : reject(new Error(`the login shell exited (code ${code}) without an environment`)));
        });
    });
}

async function loadLoginShellEnv(budgetMs = LOAD_BUDGET_MS) {
    const deadline = Date.now() + budgetMs;
    const errors = [];
    for (const shell of candidateShells()) {
        const timeoutMs = deadline - Date.now();
        if (timeoutMs <= 0) { break; }
        try {
            const env = await readLoginShellEnv({ timeoutMs, shell });
            return env;
        } catch (err) {
            errors.push(`${shell}: ${err instanceof Error ? err.message : String(err)}`);
        }
    }
    console.warn(`Could not read the login shell environment: ${errors.join('; ')}`);
    return undefined;
}
```

**逐条回应你们的 P0 论断——你们是对的，我原来的理解有误：**

| 你们的论断 | 核实结果 |
|---|---|
| `SKIPPED_VARS` 不含代理变量 | ✅ 确认。`SKIPPED_VARS` = injected env 名字 ∪ dropped vars，**没有** `http_proxy`/`https_proxy`/`all_proxy` |
| shell 的 `http_proxy` 会覆盖注入值 | ✅ 确认。`parseShellEnvOutput` 把 shell 里的一切（除 SKIPPED_VARS）都收进 `loginShellEnv` |
| 覆盖发生在 `dist/languageServer.js` | ✅ 确认，见 §1.3 |
| win32 不读 shell | ✅ 确认，`readLoginShellEnv` 首行 `return Promise.resolve({})` |
| 存在跳过 shell env 的开关 | ❌ **不存在**。我搜遍整个 `app.asar`：`ANTIGRAVITY_*` 环境变量只有 `OPEN_URL / RESOLVING_ENVIRONMENT / SSH_ASKPASS_SOCKET / SSH_ASKPASS_TOKEN / SSH_BIN / SSH_LS_REMOTE_PATH / SSH_LS_VERSION / VSCODE_HOST / WSL_DISTRO / WSL_LS_PATH`，**没有**任何与 shell env 相关的开关；也没有 `--skip-shell-env` 之类的 CLI 参数 |
| `--proxy-server` 能解决 | ❌ 不能。它只作用于 Chromium 的网络栈，对 `loginShellEnv` 这段逻辑毫无影响 |

### 1.3 `dist/languageServer.js`（spawn 后端的那段）

```js
async function startLanguageServer(port, csrf, options = {}) {
    const { headless, hostBridgeUrl, hostBridgeToken, wsl } = options;
    const loginShellEnv = await shellEnvReads.take();
    return new Promise((resolve, reject) => {
        const args = [ ...(0, lsDefaults_1.baseLsArgs)({...}), '--enable_sidecars' ];
        // …
        const env = { ...process.env, ...loginShellEnv };   // ★ loginShellEnv 在后 → 覆盖注入值
        env['AGY_BROWSER_ACTIVE_PORT_FILE'] = (0, paths_1.getActivePortFilePath)();
        (0, utils_1.setupNodeWrapper)(env);
        _lsProcess = (0, child_process_1.spawn)(spawnCmd, spawnArgs, { stdio: [...], env });
        // …
    });
}
```

`prefetchShellEnv()` 在 `main.js` 里于 `!DEV_URL` 时调用（预取，与启动重叠，不阻塞）。

### 1.4 `dist/main.js`（CLI 解析 + 单实例锁）

**单实例锁**（`dist/main.js:74`）：

```js
const gotTheLock = electron_1.app.requestSingleInstanceLock();
if (!gotTheLock) {
    electron_1.app.quit();
    process.exit(0);        // ← 第二次启动：把 argv 转发给已有实例后立刻退出，丢掉新 env
}
```

**第二实例的 argv 处理**（`dist/main.js:199`）：

```js
electron_1.app.on('second-instance', (_event, commandLine) => {
    // … deep-link 处理 …
});
```

**CLI 参数解析**——**结论：`main.js` 不拒绝未知参数**。全文里 `app.commandLine.hasSwitch` 只出现在两处：

```js
// dist/main.js:107  —— WSL 发行版
if (electron_1.app.commandLine.hasSwitch('wsl-distro')) {
    return electron_1.app.commandLine.getSwitchValue('wsl-distro');
}

// dist/main.js:133  —— 远程调试端口
if (!electron_1.app.commandLine.hasSwitch('remote-debugging-port')) {
    electron_1.app.commandLine.appendSwitch('remote-debugging-port', '0');
}
```

其余参数走 `process.argv` 自行扫描（`sshHostFromArgs` / `openSshHostFromArgs` / deep-link），**没有参数白名单**。所以我们传的 `--proxy-server` / `--proxy-bypass-list` 不会被拒；`--proxy-server` 由 Chromium 自己的命令行解析器消费（整个 asar 里 `proxy-server` **零命中**，说明 Antigravity 自己完全没碰这个开关，纯粹交给 Chromium）。

**关于 PAC**：`main.js:336` 有 `preconfigureWebviewPartitionProxies()`（BeyondCorp 专用，仅针对 `<webview>` 分区），与通用代理无关，我们不碰它。

### 1.5 由此得出的 Unix 方案（我们最终采取的做法）

**结论：零侵入的绕过手段不存在。** 因此严格按你们的裁定执行——**只红字报错拦截，打印冲突变量名，让用户自己改**：

```go
// internal/shellenv/shellenv.go
var ProxyVars = []string{
    "http_proxy", "https_proxy", "all_proxy",
    "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
}

// Detect 只读检测：跑一次 `SHELL -ilc env`，解析出代理变量。
// 不写任何文件，不改任何全局设置。
func Detect(timeout time.Duration) []Conflict { … }
```

**硬约束的落实**：
- ❌ **绝不**触碰 `.bashrc` / `.zshrc` / `.profile` —— 代码里没有任何写这些文件的路径；
- ❌ **绝不**修改系统级代理或全局环境变量 —— `internal/discovery/systemproxy.go` 对注册表只做 `reg query`（**只读**），macOS 用 `scutil --proxy`（只读），Linux 用 `gsettings get`（只读）；
- ✅ 冲突时在终端打印红字说明 + 冲突变量名 + 手动修改指引，然后**中止启动**。

**顺带修掉的一个假超时**：原 `Detect(timeout)` 形同虚设——`exec.Command(...).Output()` 没有 deadline，卡住的 rc 文件会让启动器永久挂起。现已改为 `exec.CommandContext(ctx, shell, "-ilc", "env")`，超时 5 秒。

---

## 2. 你们要的：完整的 `internal/discovery/discovery.go`

**注意**：这是整改后的版本——**全端口扫描已经删除**，`ListenPorts()` 改为读系统监听表。相关新文件：`ports_windows.go` / `ports_linux.go` / `ports_darwin.go` / `ports_other.go` / `systemproxy.go`。

```go
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

type TargetKind string

const (
    TargetIDE TargetKind = "ide"
    TargetCLI TargetKind = "cli"
)

type Target struct {
    Kind    TargetKind
    Path    string
    Version string
    Source  string
}

func (t Target) String() string {
    v := t.Version
    if v == "" { v = "版本未知" }
    return fmt.Sprintf("%s  %s  (%s)", t.Kind, t.Path, v)
}

// ── 安装位置探测 ────────────────────────────────────────────────────

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
            out = append(out, filepath.Join(home, "Applications", "Antigravity.app", "Contents", "MacOS", "Antigravity"))
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
    for _, name := range []string{"antigravity", "Antigravity"} {
        if p, err := exec.LookPath(name); err == nil { out = append(out, p) }
    }
    return out
}

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
    for _, name := range []string{"agy", "agy.exe"} {
        if p, err := exec.LookPath(name); err == nil { out = append(out, p) }
    }
    return out
}

func FindTarget(kind TargetKind) (Target, bool) {
    var candidates []string
    switch kind {
    case TargetIDE: candidates = IDECandidates()
    case TargetCLI: candidates = CLICandidates()
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

func FindAll() []Target {
    var out []Target
    for _, kind := range []TargetKind{TargetIDE, TargetCLI} {
        if t, ok := FindTarget(kind); ok { out = append(out, t) }
    }
    return out
}

// probeVersion：IDE 的安装目录 version 文件里放的是 **Electron 版本**，
// Antigravity 自己的版本在 app.asar 的 package.json 里，故优先读后者。
func probeVersion(kind TargetKind, path string) string {
    switch kind {
    case TargetIDE:
        base := filepath.Dir(path)
        if v := readAsarPackageVersion(base); v != "" { return v }
        for _, cand := range []string{
            filepath.Join(base, "version"),
            filepath.Join(base, "..", "Resources", "version"),
        } {
            if b, err := os.ReadFile(cand); err == nil {
                if v := strings.TrimSpace(string(b)); v != "" { return v + " (electron)" }
            }
        }
        return ""
    case TargetCLI:
        cmd := exec.Command(path, "--version")
        out, err := cmd.Output()
        if err != nil { return "" }
        return strings.TrimSpace(string(out))
    }
    return ""
}

// readAsarPackageVersion 从 app.asar 里读顶层 package.json 的 version。
//
// asar 布局：前 16 字节是头部，
//   [0:4] uint32 = 4
//   [4:8] uint32 = 头部 pickle 大小
//   [8:12] uint32 = 4
//   [12:16] uint32 = 头部 JSON 长度
//   [16:16+len] 头部 JSON
// 数据区起点 = 16 + align4(headerLen)。
//
// 两个坑（实测踩过）：
//  1) 头部 JSON 里有嵌套的 package.json，必须解析出顶层 "files" 下的那一个。
//  2) 文件项的 offset 可能是**字符串**（如 "5371832"），也可能是数字。
func readAsarPackageVersion(installDir string) string {
    asarPath := filepath.Join(installDir, "resources", "app.asar")
    b, err := os.ReadFile(asarPath)
    if err != nil || len(b) < 16 { return "" }

    headerLen := binary.LittleEndian.Uint32(b[12:16])
    if int64(headerLen)+16 > int64(len(b)) { return "" }

    var header struct {
        Files map[string]asarEntry `json:"files"`
    }
    if err := json.Unmarshal(b[16:16+headerLen], &header); err != nil { return "" }

    entry, ok := header.Files["package.json"]
    if !ok { return "" }
    offset, ok := entry.offsetValue()
    if !ok { return "" }

    dataOff := int64(16) + int64(headerLen)
    if dataOff%4 != 0 { dataOff += 4 - dataOff%4 }
    start := dataOff + int64(offset)
    if start < 0 || start >= int64(len(b)) { return "" }

    size := int64(entry.Size)
    if size <= 0 || start+size > int64(len(b)) { size = int64(len(b)) - start }
    var pkg struct {
        Version string `json:"version"`
    }
    if err := json.Unmarshal(b[start:start+size], &pkg); err != nil { return "" }
    return pkg.Version
}

type asarEntry struct {
    Size   int             `json:"size"`
    Offset json.RawMessage `json:"offset"`
}

func (e asarEntry) offsetValue() (uint64, bool) {
    if len(e.Offset) == 0 { return 0, false }
    if e.Offset[0] == '"' {
        var s string
        if err := json.Unmarshal(e.Offset, &s); err != nil { return 0, false }
        n, err := strconv.ParseUint(s, 10, 64)
        return n, err == nil
    }
    var n uint64
    if err := json.Unmarshal(e.Offset, &n); err != nil { return 0, false }
    return n, true
}

// ── 端口枚举 ────────────────────────────────────────────────────────

// ListenPort 是一个本机监听端口。
type ListenPort struct {
    // Host 是**连接目标**地址。已归一化：
    // 监听通配地址 "0.0.0.0" / "::" 会被换成 127.0.0.1 / ::1。
    Host string
    Port int
    // Wildcard 为 true 表示原始监听地址是通配地址（局域网可见）。
    Wildcard bool
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
// 具体实现放在 ports_<os>.go，各自带 build tag。
// 任何一条路失败时返回错误，**不做全端口扫描兜底**。
func ListenPorts() ([]ListenPort, error) {
    return listenPorts()
}

// normalizeListenHost 把监听地址转成连接目标地址。
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
```

**配套的新文件**（篇幅原因不逐字贴，列出职责与关键结论）：

| 文件 | 职责 | 关键结论 |
|---|---|---|
| `internal/discovery/systemproxy.go` | 只读系统代理：Windows 注册表 `reg query` / macOS `scutil --proxy` / Linux `gsettings get` + 环境变量 | **只读**，不写任何键 |
| `internal/discovery/ports_windows.go` | `GetExtendedTcpTable` | 见 §4.1，**修好后与 netstat 逐条一致** |
| `internal/discovery/ports_linux.go` | 解析 `/proc/net/tcp`、`/proc/net/tcp6` | state `0A` = LISTEN；地址是小端 hex |
| `internal/discovery/ports_darwin.go` | `lsof -nP -iTCP -sTCP:LISTEN` | 解析 `(LISTEN)` 前的地址字段 |
| `internal/discovery/ports_other.go` | 不支持的平台 | 返回明确错误，**不做兜底扫描** |

---

## 3. 你们要的：`Channel.Launch` 该不该砍？

**结论：砍。已经删了。**

**理由**：

1. **代码里零引用**。删前全库 grep `Launch`，命中的只有 `launcher.Launch` 这个**函数**（`internal/launcher/launcher.go:90`）和 `runLaunch`/`parseLaunchArgs` 这些**子命令名**，没有一处读写 `Channel.Launch` 这个**字段**。它是一个纯装饰性的死字段。
2. **它承载的语义已被专家裁定移出冷冻区**。`Launch []string` 的原意是「通道端口没就绪时自动拉起代理客户端」——这属于**多通道 failover / 守护进程**范畴，正是你们划进冷冻区的内容。留着它只会让人误以为这个功能存在。
3. **留着有风险**：它是一个会被 JSON 序列化到用户配置文件里的字段（`json:"launch,omitempty"`）。一旦有人误填成 `["powershell", "-File", "evil.ps1"]`，就成了配置文件里的命令注入面。删掉比留着好。

**已做的改动**：

```diff
 type Channel struct {
     Name string `json:"name"`
     Host string `json:"host"`
     Port int    `json:"port"`
     Scheme Scheme `json:"scheme,omitempty"`
     Protocols []Scheme `json:"-"`
     LAN bool `json:"lan,omitempty"`
-    Launch []string `json:"launch,omitempty"`
     Preferred bool `json:"preferred,omitempty"`
 }
```

---

## 4. P0/P1 整改清单

### 4.1 P0：全端口扫描 → 三级来源 ✅

**旧代码**（已删除）：`scanAllPorts()` 对 65535 个端口逐个拨号，10 秒内发起上万次 SYN —— 你们说会触发 Defender / 火绒 / CrowdStrike / Little Snitch 的进程隔离并打满 `ulimit -n`，这个判断我核对后**完全同意**。

**新代码**（`cmd/agy-untun/cmd_channels.go` 的 `discoverChannels()`）：

```go
// ── 来源 1：系统代理 ────────────────────────────────────────────
if sp, err := discovery.ReadSystemProxy(); err == nil && sp.Enabled && sp.Port > 0 {
    ch := channel.Channel{Host: sp.Host, Port: sp.Port, Scheme: channel.Scheme(sp.Scheme)}
    // …
    add(ch, sp.Source)
}

// ── 来源 2：本机监听表 ──────────────────────────────────────────
if ports, err := discovery.ListenPorts(); err == nil {
    for _, p := range ports {
        if isSystemPort(p.Port) { continue }
        probe := channel.ProbeTarget(p.Host, p.Port, 1200*time.Millisecond)
        if !probe.HTTPProxy && !probe.SOCKS5 { continue }
        add(channel.Channel{…}, "本机监听表")
    }
}

// ── 来源 3：常见端口白名单（兜底）──────────────────────────────
if len(out) == 0 {
    for _, port := range discovery.CommonProxyPorts { … }
}
```

**Windows 监听表读取的四个坑**（都是实测撞出来的，值得写下来给未来维护者）：

| # | 坑 | 症状 | 正确做法 |
|---|---|---|---|
| 1 | `GetExtendedTcpTable` 是 **5 个**参数，不是 4 个 | `ERROR_INVALID_PARAMETER (87)`，表永远读不到 | `(pTcpTable, pdwSize, bOrder, ulClass, Reserved)` |
| 2 | `ulClass` 只有 **2**（`TCP_TABLE_BASIC_ALL`）在本机可用 | class 0/1/3/4/5 全部 87 | 用 class=2 再按 `dwState` 自己过滤 |
| 3 | 首次调用（`pTcpTable=NULL`）**必须**返回 `ERROR_INSUFFICIENT_BUFFER (122)` | 把 122 当失败 → 永远拿不到表 → 「本机一个监听端口都没有」 | 只在 ret==122 时继续 |
| 4 | 表体前有 **4 字节行数头** | 漏掉 → 把表头当第一行的 `dwState` | `count = LE32(buf[0:4])`，行从偏移 4 开始 |

> 这一条我原先写错了：**整改前 Windows 监听表这一路是完全失效的**。之前 `channels scan` 能找到 10808，走的是**系统代理**那一路，不是监听表。修好之后扫描结果从 1 个候选变成 2 个（多出 10810，且识别出它是 `http+socks5` 的 mixed inbound）。

**验证（可复现）**：

```
$ netstat -ano -p TCP | grep -c LISTENING        →  53
$ agy-untun channels scan
  发现 2 个候选:
    127.0.0.1:10808        http
    127.0.0.1:10810        http+socks5
```

逐条比对：本实现读到的 53 个监听端口与 `netstat` 的 53 条 LISTENING **完全一致**。

### 4.2 P1：`0.0.0.0` 归一化 ✅

```go
// internal/channel/probe.go
func NormalizeTarget(host string) string {
    switch strings.TrimSpace(host) {
    case "", "*", "0.0.0.0":
        return "127.0.0.1"
    case "::", "[::]":
        return "::1"
    }
    return host
}
```

调用点在 `ProbeTarget` 的**第一行**（先归一化再拨号），并由 `discovery.normalizeListenHost` 配套产出 `Wildcard` 标记，CLI 里显示 `[局域网监听]`。

### 4.3 P1：`io.Read` → `io.ReadFull`、`strings.Contains(" 200")` → `http.ReadResponse` ✅

```go
// SOCKS5 greeting 的 2 字节应答
resp := make([]byte, 2)
if _, err := io.ReadFull(conn, resp); err != nil { return false }
// 必须 io.ReadFull：TCP 无消息边界，一次 Read 可能只返回 1 字节。

// HTTP CONNECT 的应答
resp, err := http.ReadResponse(bufio.NewReader(conn), req)
if err != nil { return false }
defer resp.Body.Close()
return resp.StatusCode == http.StatusOK
```

### 4.4 P1：Electron「脑裂」——`--proxy-server` + `--proxy-bypass-list` ✅

**你们说得对**：只注入环境变量，Chromium 壳是直连；只加 `--proxy-server`，Go 后端是直连。**两者都要**。

```go
launchArgs = append(launchArgs,
    "--proxy-server="+ch.URL(),
    "--proxy-bypass-list="+chromiumBypassList(*ch),
)
```

`chromiumBypassList` 把 Go 的 `NO_PROXY`（逗号分隔）转成 Chromium 的分号分隔，并额外排除代理服务器自身的地址。

**实测确认参数确实落到进程上**：

```
Antigravity.exe --proxy-server=http://127.0.0.1:10808
  --proxy-bypass-list=api.example.com;.example.com;localhost;127.0.0.1;::1;127.0.0.1:10808
```

**实测确认后端全部走代理**（`language_server.exe` 的出站连接）：

```
到 10808: 8
到 10810: 0
直连非回环: 0        ← 一条都没有
```

### 4.5 P1：`ALL_PROXY` 只在纯 socks5 时注入 ✅

```go
env = setEnv(env, "HTTP_PROXY", proxyURL)
env = setEnv(env, "http_proxy", proxyURL)
env = setEnv(env, "HTTPS_PROXY", proxyURL)
env = setEnv(env, "https_proxy", proxyURL)

if ch.Scheme == channel.SchemeSOCKS5 {
    env = setEnv(env, "ALL_PROXY", proxyURL)
    env = setEnv(env, "all_proxy", proxyURL)
} else {
    env = unsetEnv(env, "ALL_PROXY")
    env = unsetEnv(env, "all_proxy")
}
```

理由：`ALL_PROXY` 没有 scheme 之分，多数工具（curl / git / pip）按 socks5 解释。http 通道写成 `ALL_PROXY=http://…` 会让只读 ALL_PROXY 的工具按 socks5 握手而失败。

### 4.6 P1：单实例守卫与跨平台进程终止 ✅ —— **这里出了我自己的 P0 bug**

**先说 bug**。整改过程中我写了进程终止逻辑，判据用了「命令行含 `--type=renderer` / `--type=gpu-process` / `--user-data-dir=`」。**Chromium 的渲染进程命令行同样带 `--type=renderer`** —— 于是用户的 Chrome / Edge 渲染进程被一起杀掉。**用户实际受害了浏览器崩溃。**

根因：判据只看了命令行，**没有看进程名**。

**修复**：改成「进程名 + 命令行」双判据，缺一不可：

```go
// internal/procs/procs.go
func (p Proc) IsIdeProcess() bool {
    name := strings.ToLower(p.Name)
    name = strings.TrimSuffix(name, ".exe")
    if name != "antigravity" && name != "language_server" {
        return false          // ← 第一道：进程名。挡住 Chrome / Edge
    }
    if name == "language_server" { return true }
    return p.IsDesktopApp()   // ← 第二道：命令行。挡住同名的 agy CLI sidecar
}
```

**连带发现的一个陷阱**：Windows 上 `agy` CLI 的 Node sidecar **也叫 `Antigravity.exe`**（实测命令行 `Antigravity.exe --import=file://…register_loader.js main.mjs`，父进程是 `cmd.exe /c "…agy-node.cmd…"`）。而桌面版 Antigravity **自己也会起同样的 sidecar**。两者命令行几乎一样，唯一的区别是父进程：

```go
// agy CLI 的 sidecar：父进程命令行含 agy-node.cmd
func (p Proc) IsSidecar() bool {
    cl := strings.ToLower(p.CmdLine)
    if !strings.Contains(cl, "--import=file://") { return false }
    if !strings.HasSuffix(cl, "main.mjs") && !strings.HasSuffix(cl, "server.cjs") &&
        !strings.Contains(cl, "register_loader.js") { return false }
    return strings.Contains(strings.ToLower(p.ParentCmdLine), "agy-node.cmd")
}
```

**新增回归闸门**：`agy-untun doctor --processes` —— 干跑，列出「会被杀的」和「会被跳过的」，不做任何终止动作：

```
将会被终止的进程（桌面版 + Go 后端）:
  Antigravity.exe (PID 44152) … （共 11 个）
  language_server.exe (PID 39616)
将被跳过的进程（agy CLI 的 sidecar —— 不属于桌面版）:
  Antigravity.exe (PID 24084)
  Antigravity.exe (PID 101368)
```

**没有 Chrome、没有 Edge**。

**跨平台进程名**：

| 平台 | 桌面版 | Go 后端 | CLI sidecar |
|---|---|---|---|
| Windows | `Antigravity.exe` | `language_server.exe` | `Antigravity.exe`（父进程含 `agy-node.cmd`） |
| macOS | `Antigravity` | `language_server` | — |
| Linux | `antigravity` | `language_server` | — |

**终止失败时输出精确 PID**：

```go
return fmt.Errorf("终止失败:\n  %s\n\n请手动结束这些进程:\n%s",
    strings.Join(failures, "\n  "), formatProcs(targets))
```

**实测**：terminate + relaunch 全程，Chrome 进程数 19→19、Edge 17→17，未减少一个。

### 4.7 P1：Unix shell env —— 红字报错拦截 ✅

见 §1.5。**没有零侵入绕过方案**（asar 里翻遍了），因此严格按裁定执行「只报错拦截 + 打印变量名」。

**新包** `internal/shellenv`（208 行）：

```go
func Detect(timeout time.Duration) []Conflict   // 只读检测
func Report(conflicts []Conflict) string        // 红字说明 + 冲突变量名 + 手动改法
```

### 4.8 其它 ✅

- **删除 `Channel.Launch`** —— 见 §3。
- **扫描阶段并发 `VerifyChannel`** —— `cmd_launch.go` 的 `verifyAll()`，并发上限 8，避免几十个候选串行等几分钟。
- **`go vet` 干净；6 个目标交叉编译通过**（windows/linux/darwin × amd64/arm64）。

---

## 5. 我们的自查：还有什么已知不足

诚实列出，请专家继续挑：

1. **macOS / Linux 的监听表代码没有真机验证过**。Windows 那条是逐条比对 netstat 验过的；Linux 的 `/proc/net/tcp` 小端 hex 解析与 macOS 的 lsof 解析**只做了代码审查，没有实机跑过**。这是当前最大的空白。
2. **IPv6 监听端口在 Windows 上读不到**。`MIB_TCPROW` 只描述 IPv4；IPv6 需要 `MIB_TCP6ROW` + `TCP_TABLE_OWNER_PID_LISTENER`（class 3），而 class 3 在本机返回 87。因此「只监听在 `::1` 的代理」会被漏掉。对 v2rayN / Clash 这类默认监听 `127.0.0.1` 的客户端无影响，但属于功能缺口。
3. **`fallbackTerminate()` 仍会误伤**。它是进程列表读不到时的兜底，Windows 上用 `taskkill /IM Antigravity.exe`，会连带杀掉 agy CLI 的同名 sidecar。已加了显式警告，但更好的做法是**宁可不杀、报错让用户手动关**。
4. **`--proxy-bypass-list` 的格式未经 Chromium 官方文档核对**。当前把 `NO_PROXY` 的各项（含 `api.example.com` 这种带点的域名）直接用分号拼接。Chromium 的 bypass 规则与 NO_PROXY 并不完全等价（例如 Chromium 支持 `<local>` 这种 NO_PROXY 没有的语法），**存在漏代理或过度代理的可能**。
5. **单实例检测依赖 `DevToolsActivePort` + CDP 可达性**。文件在异常退出后会残留（已用 CDP 可达性过滤），但如果用户开了 `--remote-debugging-port` 之外的调试方式、或 CDP 端口被别的进程占用，仍可能误判。
6. **`shellenv.Detect` 会真的执行一次登录 shell**。虽然只读、不写任何文件，但用户的 rc 文件里可能有副作用（比如自动更新、启动后台任务）。Antigravity 自己也这么做（`prefetchShellEnv`），所以行为对齐；但我们额外跑了一次，频率是 Antigravity 的两倍。
7. **并发上限 8 是拍脑袋定的**。本机监听端口多时（实测 53 个）需要探测 ~10 个真实代理端点，8 个并发够用；但没做过压测。

---

## 6. 给专家的三个问题

1. **第 5 节第 2 条（IPv6 监听端口）**：是否值得为「只监听 `::1` 的代理」补一条 `MIB_TCP6ROW` 的路？考虑到 class 3 在本机就返回 87，实现成本可能比想象中高。
2. **第 5 节第 3 条（fallbackTerminate）**：是否应直接**取消**这条兜底路，改成「读不到进程列表就不杀，报错让用户手动关」？我们倾向取消——那条路唯一的价值是覆盖一个极罕见的场景，代价是误杀用户 CLI 会话。
3. **`--proxy-bypass-list` 的正确格式**：有没有 Chromium 的权威文档或可参考的实现？我们当前的处理是「逗号转分号 + 追加代理自身地址」，但不确定 `<local>` 之类的语义。

---

## 7. 冻结声明（本轮**没有**做的事）

严格遵守冷冻区，本轮**未**实现以下任何一项：

- ❌ 守护进程 / 断线重连监督器
- ❌ 多通道 failover 组
- ❌ `--settings` JSON 注入
- ❌ 开机自启 / 系统服务集成
- ❌ GUI / 托盘

并且以下**硬约束**在本轮所有改动中保持不变：

- ❌ **绝不**触碰用户的 `.bashrc` / `.zshrc` / `.profile`（只读检测，不写）
- ❌ **绝不**做 65535 全端口扫描
- ❌ **绝不**修改系统级代理设置或全局环境变量（注册表只 `reg query`）
- ❌ **绝不**修改系统级注册表代理项

---

## 8. 复现命令

```bash
# 编译（Windows）
GOROOT="D:\agy-untun\.tools\go" "D:/agy-untun/.tools/go/bin/go.exe" build -o dist/agy-untun.exe ./cmd/agy-untun

# 静态检查 + 6 平台交叉编译
GOROOT="D:\agy-untun\.tools\go" "D:/agy-untun/.tools/go/bin/go.exe" vet ./...
for t in "windows amd64" "windows arm64" "linux amd64" "linux arm64" "darwin amd64" "darwin arm64"; do
  set -- $t
  GOROOT="D:\agy-untun\.tools\go" GOOS=$1 GOARCH=$2 \
    "D:/agy-untun/.tools/go/bin/go.exe" build -o "dist/agy-untun-$1-$2" ./cmd/agy-untun
done

# 功能验证
./dist/agy-untun.exe channels scan       # → 127.0.0.1:10808 http / 127.0.0.1:10810 http+socks5
./dist/agy-untun.exe doctor              # 完整诊断
./dist/agy-untun.exe doctor --processes  # 干跑：列出会被杀的进程
./dist/agy-untun.exe launch --ide --yes  # 启动

# 关键验证：后端出站是否全部走代理
powershell -NoProfile -Command "
  \$ls = Get-Process language_server | Select-Object -First 1
  \$c = Get-NetTCPConnection -OwningProcess \$ls.Id -State Established
  '到 10808: ' + (\$c | Where-Object {\$_.RemotePort -eq 10808}).Count
  '直连非回环: ' + (\$c | Where-Object {\$_.RemoteAddress -notlike '127.*'}).Count
"
# 期望：到 10808: 8 / 直连非回环: 0
```

---

## 9. 引用来源

| 断言 | 来源 |
|---|---|
| `readLoginShellEnv` win32 返回 `{}` | `app.asar` → `dist/loginShellEnv.js`（§1.2 原文） |
| `SKIPPED_VARS` 不含代理变量 | `app.asar` → `dist/loginShellPolicy.js`（§1.1 原文） |
| `env = { ...process.env, ...loginShellEnv }` | `app.asar` → `dist/languageServer.js`（§1.3 原文） |
| 单实例锁丢弃新 env | `app.asar` → `dist/main.js:74` |
| `main.js` 无参数白名单 | `app.asar` → `dist/main.js` 全文（`hasSwitch` 仅 2 处） |
| 无 shell-env 跳过开关 | `app.asar` 全文 grep `ANTIGRAVITY_[A-Z_]+`（10 个，无相关项） |
| `proxy-server` 在 asar 中零命中 | `app.asar` 全文 grep |
| 后端只认环境变量 | `language_server.exe` 二进制符号表（`httpproxy.FromEnvironment` 3 处；无 `WinHttpGetIEProxyConfig` / `InternetQueryOption`） |
| Windows 监听表布局 | 本机实测 + 逐条比对 `netstat -ano -p TCP`（53/53 一致） |
| 后端出站全部走代理 | `Get-NetTCPConnection -OwningProcess <language_server pid>` 实测 |

---

**提交**：`e307c4d` — `D:\agy-untun`
**本文档**：`D:\agy-untun\docs\agy-untun-expert-response-20261117-1650.md`
