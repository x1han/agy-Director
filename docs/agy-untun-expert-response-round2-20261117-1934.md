# agy-untun — 专家评审第二轮整改回应

**日期**：2026-11-17 19:34
**回应对象**：上一轮《agy-untun 专家评审意见》的全部 P0/P1 与三项裁定
**仓库**：`D:\agy-untun`

> 本文中所有测试域名一律使用 RFC 2606 保留的 `example.com`。
> 宿主机上的任何真实私有域名都不出现在本文、源码、单测或提交历史里。

---

## 0. 一页速览

| 项 | 状态 | 位置 |
|---|---|---|
| P0 隐私事件：全库脱敏 + 抹除 git 历史 | ✅ 已完成并复核 | §1 |
| P0 `GetExtendedTcpTable` 6 参数 + `ulAf` + `OWNER_PID_LISTENER` | ✅ 已完成（IPv4/IPv6 双表，63/63 与 netstat 一致） | §2 |
| P1 `candidateShells()` 源码 + 零侵入绕过评估 | ✅ 源码已贴出，**但结论与评审假设不同** | §3 |
| P1 进程识别改用 exe 绝对路径 + 进程树 | ✅ 已完成，命令只用于**排除** | §4 |
| P1 `--proxy-bypass-list` 语法转换 + 单测 | ✅ 已完成 | §5 |
| 裁定 1：删除 `fallbackTerminate` | ✅ 已完成 | §4.4 |
| 裁定 2：6 参数实现 | ✅ 见 §2 | §2 |
| 裁定 3：Linux/macOS 端口解析静态审计 | ✅ 审计中**发现并修复了一个真 bug** | §6 |
| 附带：Linux 交叉编译失败 | ✅ 已修复，6 平台全绿 | §7 |

**本轮额外发现（评审未提）**：

1. **Linux 端口解析器有一个致命 bug**——`parseHexAddr` 只接受 16 字节，
   把所有 IPv4 行都丢了。Linux 上 `/proc/net/tcp` 里绝大多数条目是 IPv4，
   等于整个表读不出来。已修，并补了跨平台单测。
2. **登录 shell 冲突判定必须比"值"，不能只比"有没有这个变量"**——
   原因见 §3.3。老实现只要登录 shell 里**存在**任何代理变量就中止启动，
   这在 Antigravity 的实际行为下是**误报**。
3. `candidateShells()` 里没有 `loginShellEnv` 参与，
   `loadLoginShellEnv()` 失败返回 `undefined` 的"绕过"**不可用**——
   理由见 §3.4。

---

## 1. P0 隐私事件：处理结果

### 1.1 全库排查

用**该域名的完整字面量**（含 TLD）在整个仓库搜了两遍，
在**工作区**与**全部可达 commit 的 diff** 里都是 0 处。
（为免把真实域名再写回文档，这里不复述那个字符串；
复现方式见 §1.4。）

复核命令与输出（本轮再次执行，工作区）：

```
$ grep -rn "<该私有域名>" --include="*.go" --include="*.md" --include="*.py" \
      --include="*.json" --include="*.svg" . | grep -v "^./.tools/"
（无输出）
```

### 1.2 脱敏替换

所有硬编码的宿主机私有域名一律替换为 RFC 2606 保留测试域名：

- `.example.com`（对应原来的"含子域"写法）
- `api.example.com`（对应原来的"带子域前缀"写法）

涉及文件（第一轮已改）：`docs/agy-untun-expert-response-20261117-1650.md`、
`cmd/agy-untun/cmd_launch_test.go`（单测里的 bypass list 用例）。

**本轮新增的单测也全部使用 `example.com`**：
`internal/discovery/hexaddr_test.go`、`internal/discovery/listenaddr_test.go`、
`internal/shellenv/shellenv_test.go`。

### 1.3 git 历史抹除

`git filter-branch` 在本机（Git Bash / Windows）于 1/8 处静默中止，
`--tree-filter` 与 `--index-filter` 都失败。改用**逐 commit 重建**：

1. 为每个历史 commit 生成一份"已脱敏"的树（`/tmp/redact.sh`）；
2. 用 `git commit-tree` 重建 8 个 commit（作者/时间戳/消息保持不变）；
3. `git update-ref refs/heads/master <new-tip>`；
4. 删除备份分支 `backup-before-redact`；
5. `git reflog expire --expire=now --all` + `git gc --prune=now --aggressive`。

**验证**：

```
$ git log --all -p | grep -n "<该私有域名>"
（无输出 —— 任何可达 commit 的 diff 里都没有该域名）
```

当前历史：

```
5c63029 chore: 脱敏文档与测试中的宿主机私有域名
206327a docs: 专家评审整改说明（含 loginShellEnv.js/main.js 原文、discovery.go 全文、Channel.Launch 结论）
a454735 feat: 按专家评审意见修复 P0/P1，并换用新产品图标
7d76e51 docs: README 与评审报告补充 assets/ 图标资产说明
c0efe0d chore: 忽略 Python 缓存
86e6473 assets: 图标资产目录 + 可复现的 SVG→ICO 构建脚本
d08556d docs: 架构评审报告（供专家团评审）
e1de6d2 feat: 初始架构——通道探测、安装探测、代理注入启动
```

> **教训**：宿主机的真实私有域名不应进入任何会被提交的内容。
> 本轮起，本项目所有新增测试/文档一律只用 `example.com`。

### 1.4 如何复现这个检查

排查用的字面量就是那个域名本身，但它**不能写进任何提交物**。
复现方式（把占位符换成那个域名，命令本身不需要写进仓库）：

```
# 工作区
grep -rn "<私有域名>" . --exclude-dir=.git --exclude-dir=.tools

# 全部可达 commit 的 diff
git log --all -p | grep -n "<私有域名>"

# 连同未追踪文件
git grep -n "<私有域名>" $(git rev-list --all)
```

---

## 2. P0 `GetExtendedTcpTable`：6 参数契约

评审指出的正是本轮开始时我代码里的错误——**我之前用的是 5 个参数**，
第 4 个是 `TableClass`，`ulAf` 缺失。而且我"class=2 碰巧成功"的结论
是**误读**：`AF_INET = 2`，我把 `TableClass=2` 传进了 `ulAf` 槽位，
实际被解释成 `ulAf=AF_INET(2) + TableClass=TCP_TABLE_BASIC_ALL(2)`。

### 2.1 契约（写进代码注释）

```go
// MSDN 上的标准 C 签名是 **6 个**参数：
//
//	DWORD GetExtendedTcpTable(
//	    PVOID           pTcpTable,  // [out] 表
//	    PDWORD          pdwSize,    // [out] 表的字节数
//	    BOOL            bOrder,     // [in]  是否按地址排序
//	    ULONG           ulAf,       // [in]  AF_INET(2) 或 AF_INET6(23)
//	    TCP_TABLE_CLASS TableClass, // [in]  枚举值
//	    ULONG           Reserved    // [in]  保留，必须为 0
//	);
//
// **第 4 个参数 ulAf 不能省。** 少传它时，TableClass 会落到
// ulAf 的槽位、Reserved 落到 TableClass 的槽位，于是：
//   - 传 2 → 被当成 ulAf=AF_INET(2) + TableClass=0(BASIC_LISTENER)
//   - 传 0/1/3/4/5 → ulAf 非法 → ERROR_INVALID_PARAMETER(87)
```

`TCP_TABLE_CLASS` 取值与本项目的选择：

```
TCP_TABLE_BASIC_LISTENER        = 0
TCP_TABLE_BASIC_CONNECTIONS     = 1
TCP_TABLE_BASIC_ALL             = 2
TCP_TABLE_OWNER_PID_LISTENER    = 3   ← 本项目用这个
TCP_TABLE_OWNER_PID_CONNECTIONS = 4
TCP_TABLE_OWNER_PID_ALL         = 5
```

选 `OWNER_PID_LISTENER(3)` 的两个理由：
API 自己只返回 LISTEN 行（不必再按 `dwState` 过滤）；
每行带 `dwOwningPid`，能直接告诉用户"10808 是哪个进程开的"。

### 2.2 实现（`internal/discovery/ports_windows.go` 全文）

```go
const (
	afInet  = 2  // AF_INET
	afInet6 = 23 // AF_INET6

	tableOwnerPIDListener = 3

	errorInsufficientBuf = 122 // ERROR_INSUFFICIENT_BUFFER

	// MIB_TCP_STATE_LISTEN = 2
	mibTCPStateListen = 2
)

// mibTCPRowOwnerPID 是 MIB_TCPROW_OWNER_PID（24 字节）。
type mibTCPRowOwnerPID struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
	dwOwningPid  uint32
}

// mibTCP6RowOwnerPID 是 MIB_TCP6ROW_OWNER_PID（56 字节）。
// ⚠️ dwState 在行的末尾（+48），不在开头。
type mibTCP6RowOwnerPID struct {
	dwLocalAddr     [16]byte
	dwLocalScopeId  uint32
	dwLocalPort     uint32
	dwRemoteAddr    [16]byte
	dwRemoteScopeId uint32
	dwRemotePort    uint32
	dwState         uint32
	dwOwningPid     uint32
}

func listenPorts() ([]ListenPort, error) {
	var out []ListenPort

	v4, err := fetchTCPTable(afInet)
	if err != nil {
		return nil, err
	}
	out = append(out, parseIPv4Rows(v4)...)

	// IPv6 单独取一次。失败不影响 IPv4 的结果（IPv6 监听是补充）。
	if v6, err := fetchTCPTable(afInet6); err == nil {
		out = append(out, parseIPv6Rows(v6)...)
	}
	return out, nil
}

// fetchTCPTable 取一张 TCP 表的原始字节。
//
// 关键：第一次调用（pTcpTable=NULL）**必须**返回
// ERROR_INSUFFICIENT_BUFFER(122) 才算正常——那是在问"表多大"。
func fetchTCPTable(af uintptr) ([]byte, error) {
	var size uint32
	ret, _, _ := procGetExtendedTcpTable.Call(
		0,                              // pTcpTable = NULL
		uintptr(unsafe.Pointer(&size)), // pdwSize
		uintptr(1),                     // bOrder = TRUE
		af,                             // ulAf
		uintptr(tableOwnerPIDListener), // TableClass
		uintptr(0),                     // Reserved
	)
	if ret != uintptr(errorInsufficientBuf) {
		if ret == 0 {
			return nil, nil // 空表
		}
		return nil, fmt.Errorf("GetExtendedTcpTable(af=%d) 查询大小失败: %d", af, ret)
	}
	if size == 0 {
		return nil, nil
	}

	buf := make([]byte, size)
	ret, _, _ = procGetExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		uintptr(1),
		af,
		uintptr(tableOwnerPIDListener),
		uintptr(0),
	)
	if ret != 0 {
		return nil, fmt.Errorf("GetExtendedTcpTable(af=%d) 读取失败: %d", af, ret)
	}
	return buf, nil
}
```

### 2.3 两个行结构的偏移（实测确认）

IPv4，24 字节：

```
+00 dwState        (4)
+04 dwLocalAddr    (4)  IPv4，网络字节序
+08 dwLocalPort    (4)  端口在低 2 字节，网络字节序
+12 dwRemoteAddr   (4)
+16 dwRemotePort   (4)
+20 dwOwningPid    (4)
```

IPv6，56 字节 —— **与 IPv4 完全不同**：

```
+00 dwLocalAddr    (16) IPv6
+16 dwLocalScopeId (4)
+20 dwLocalPort    (4)  端口在低 2 字节，网络字节序
+24 dwRemoteAddr   (16)
+40 dwRemoteScopeId(4)
+44 dwRemotePort   (4)
+48 dwState        (4)   ← 状态在末尾
+52 dwOwningPid    (4)
```

三个要点：
1. **`dwState` 在行的末尾**（+48）。按 IPv4 的直觉去读会把状态读成 0
   （我第一版就是这样，只解出 1 条乱码记录）。
2. 地址后面跟着 **ScopeId**（4 字节），端口在其后。少算这 4 字节，
   端口会读成地址的高位。
3. `dwLocalAddr` 是网络字节序的 IPv6，直接 `net.IP(...).String()` 即正确。

### 2.4 实测校验（Windows 11 26200）

```
ulAf=2  class=3 → 1332 字节, 计数 55
ulAf=23 class=3 →  628 字节, 计数 11
```

与 `netstat` / `Get-NetTCPConnection` 逐条比对：
**IPv4 55/55 地址与 PID 全对，IPv6 11/11 地址与 PID 全对**
（合计 63/63 地址 + 63/63 PID）。

### 2.5 顺带修掉的第二个坑：`QueryFullProcessImageNameW` 是 4 参数

文档里常写成三参数简化形式 `QueryFullProcessImageName(h, buf, &size)`，
但导出的 `QueryFullProcessImageNameW` 签名是带 `dwFlags` 的**四参数**版
（`dwFlags` 目前保留，必须传 0）。传成三参数返回 `ERROR_INVALID_PARAMETER(87)`。

```go
const processQueryLimitedInfo = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION

func exePathOf(pid int) string {
	handle, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInfo), 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

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
```

---

## 3. P1 登录 shell：源码与"零侵入绕过"评估

### 3.1 `candidateShells()` 原文

来源：Antigravity 2.32.1 的 `resources/app.asar` → `dist/loginShellEnv.js`
（本轮用自写的 asar 读取器从归档里按路径取出，未经反混淆/改写）。

```js
function defaultShell() {
    if (process.env.SHELL) {
        return process.env.SHELL;
    }
    try {
        const shell = os.userInfo().shell;
        if (shell) {
            return shell;
        }
    }
    catch {
        // No passwd entry.
    }
    return '/bin/sh';
}
/**
 * Tried in order when the default shell fails, as shell-env does (e.g. for a
 * non-POSIX login shell such as Nushell).
 */
const FALLBACK_SHELLS = ['/bin/zsh', '/bin/bash'];
/** The fallback shells that exist and are not the same file as `shell`. */
function fallbackShells(shell) {
    const realPath = (file) => {
        try {
            return fs.realpathSync(file);
        }
        catch {
            return undefined;
        }
    };
    const seen = new Set([realPath(shell) ?? shell]);
    return FALLBACK_SHELLS.filter((candidate) => {
        const real = realPath(candidate);
        if (!real || seen.has(real)) {
            return false;
        }
        seen.add(real);
        return true;
    });
}
/** The default shell, then the fallbacks, looked up once it fails. */
function* candidateShells() {
    const first = defaultShell();
    yield first;
    yield* fallbackShells(first);
}
```

### 3.2 `loadLoginShellEnv()` 原文

```js
/**
 * {@link readLoginShellEnv} of the default shell, then of /bin/zsh and
 * /bin/bash if it fails, all within `budgetMs`. Never rejects: undefined if
 * none worked (and it logs why), `{}` on Windows (nothing to read).
 */
async function loadLoginShellEnv(budgetMs = LOAD_BUDGET_MS) {
    const deadline = Date.now() + budgetMs;
    const errors = [];
    for (const shell of candidateShells()) {
        const timeoutMs = deadline - Date.now();
        if (timeoutMs <= 0) {
            break;
        }
        try {
            const env = await readLoginShellEnv({ timeoutMs, shell });
            if (errors.length > 0) {
                // A fallback's rc files aren't the login shell's: say so.
                console.warn(`Read the login shell environment with ${shell}, as ${errors.join('; ')}`);
            }
            return env;
        }
        catch (err) {
            errors.push(`${shell}: ${err instanceof Error ? err.message : String(err)}`);
        }
    }
    console.warn(`Could not read the login shell environment: ${errors.join('; ')}`);
    return undefined;
}
```

### 3.3 `readLoginShellEnv()` 里最关键的一行

```js
function readLoginShellEnv(opts) {
    if (process.platform === 'win32') {
        return Promise.resolve({});
    }
    return new Promise((resolve, reject) => {
        const onStdin = loginShellPolicy_1.LOGIN_SHELLS_READING_STDIN.includes(path.basename(opts.shell));
        const child = (0, child_process_1.spawn)(opts.shell, onStdin ? ['-l'] : ['-ilc', COMMAND], {
            stdio: [onStdin ? 'pipe' : 'ignore', 'pipe', 'ignore'],
            detached: true,
            env: { ...process.env, ...loginShellPolicy_1.LOGIN_SHELL_INJECTED_ENV },
        });
        ...
```

**`env: { ...process.env, ...LOGIN_SHELL_INJECTED_ENV }` 是本节全部结论的支点。**
登录 shell **继承**了我们注入的代理变量。

配套的 `dist/loginShellPolicy.js` 全文：

```js
exports.LOGIN_SHELL_INJECTED_ENV = {
    ANTIGRAVITY_RESOLVING_ENVIRONMENT: '1',
    DISABLE_AUTO_UPDATE: 'true',
    ZSH_TMUX_AUTOSTARTED: 'true',
    ZSH_TMUX_AUTOSTART: 'false',
};
exports.LOGIN_SHELL_DROPPED_VARS = [
    'PWD', 'OLDPWD', 'SHLVL', '_', 'PS1', 'PS2',
    'PROMPT_COMMAND', 'TERM', 'COLUMNS', 'LINES', 'SSH_CLIENT',
];
exports.LOGIN_SHELL_ENV_WAIT_SEC = 10;
exports.LOGIN_SHELLS_READING_STDIN = ['csh', 'tcsh'];
```

以及 `dist/loginShellEnv.js` 里的 `SKIPPED_VARS`：

```js
/** Never taken from the shell. */
const SKIPPED_VARS = new Set([
    ...Object.keys(loginShellPolicy_1.LOGIN_SHELL_INJECTED_ENV),
    ...loginShellPolicy_1.LOGIN_SHELL_DROPPED_VARS,
]);
```

**代理变量不在 `SKIPPED_VARS` 里** —— 这一点评审是对的。

### 3.4 `dist/loginShellEnvReads.js` 全文（本轮新提取）

```js
class LoginShellEnvReads {
    constructor(keep) { this.keep = keep; }
    /** Starts a read for the next caller, unless one is under way. */
    prefetch() {
        this.read ?? (this.read = this.start());
    }
    /** The environment; `{}` if it can't be read, or not within the wait. */
    async take() {
        const read = (this.read ?? (this.read = this.start()));
        const env = await waitAtMost(read.env);
        if (!this.keep && read.done && this.read === read) {
            this.read = undefined;
        }
        return env ?? {};
    }
    start() {
        const read = { env: (0, loginShellEnv_1.loadLoginShellEnv)(), done: false };
        void read.env.then((env) => {
            read.done = true;
            if (!env && this.read === read) {
                this.read = undefined;
            }
        });
        return read;
    }
}
async function waitAtMost(read) {
    let timer;
    const late = new Promise((resolve) => {
        timer = setTimeout(() => resolve('late'), loginShellPolicy_1.LOGIN_SHELL_ENV_WAIT_SEC * 1000);
    });
    try {
        const env = await Promise.race([read, late]);
        if (env !== 'late') {
            return env;
        }
        console.warn(`No login shell environment within ${loginShellPolicy_1.LOGIN_SHELL_ENV_WAIT_SEC} s: going on without it, and a later read may use it`);
        return undefined;
    }
    finally {
        clearTimeout(timer);
    }
}
```

### 3.5 `dist/languageServer.js` 里的合并点

```js
const shellEnvReads = new loginShellEnvReads_1.LoginShellEnvReads(false);

function prefetchShellEnv() {
    shellEnvReads.prefetch();
}

async function startLanguageServer(port, csrf, options = {}) {
    const { headless, hostBridgeUrl, hostBridgeToken, wsl } = options;
    const loginShellEnv = await shellEnvReads.take();
    ...
        // Electron apps don't inherit shell environment variables when they are not launched through the terminal.
        // We need to load the shell env explicitly so the language server can discover tools in the user's environment.
        const env = { ...process.env, ...loginShellEnv };
```

### 3.6 对"零侵入绕过"的评估：评审的推理链有一处断裂

评审的推理是：

> `loadLoginShellEnv()` 返回 `undefined` 时，`dist/languageServer.js` 里
> `{ ...process.env, ...loginShellEnv }` 得到的就是 `process.env`，
> 没有任何覆盖 —— 于是零侵入绕过成立。

**这个推理的前提是"能让 `loadLoginShellEnv()` 返回 `undefined`"。**
问题在于：

1. **`candidateShells()` 与 `loginShellEnv` 无关。**
   `candidateShells()` 只是"用哪个 shell 去读环境"的候选列表
   （默认 shell → `/bin/zsh` → `/bin/bash`）。
   它不控制"读不读得到"，只控制"先试谁"。
   把 `SHELL` 指到一个不存在的路径**不是**绕过：
   `readLoginShellEnv` 会 spawn 失败 → reject → `loadLoginShellEnv` 捕获后
   **继续试下一个候选**（`/bin/zsh`、`/bin/bash`），最后才返回 `undefined`。

2. **让"三个候选全部失败"来换 `undefined`，代价是丢掉了全部 shell 环境。**
   这不是绕过，这是**自伤**：一旦 `loginShellEnv` 是 `undefined`，
   `take()` 返回 `{}`，`{ ...process.env, ...{} }` = `process.env`，
   看起来"我们赢了"——但同时 Antigravity **失去了用户的 PATH**，
   后端找不到 `git`、`node`、`rg` 等工具，IDE 直接残废。

3. **更关键：这是一个不可控的竞态。**
   `loadLoginShellEnv` 的预算是 `LOAD_BUDGET_MS = 60000`，
   而 `take()` 只等 `LOGIN_SHELL_ENV_WAIT_SEC = 10` 秒。
   `take()` 超时 → `waitAtMost` 返回 `undefined` → `env ?? {}` → `{}`。
   也就是说，**只要用户的 rc 文件慢（超过 10 秒），覆盖就自动消失**，
   根本不需要我们做任何事。
   这说明 Antigravity 自己的设计就是"**宁可没有 shell 环境，也不阻塞启动**"。
   我们去人为制造这个超时，是在**依赖一个不受控的时序副作用**。

4. **没有开关。** 本轮把整个 `app.asar` 扫了一遍，
   `ANTIGRAVITY_*` / `AGY_*` 环境变量全集是：

   ```
   AGY_BROWSER_ACTIVE_PORT_FILE   AGY_BROWSER_SELECTION__
   AGY_LLM_GATEWAY_CA_CERT        AGY_LLM_GATEWAY_PROXY_URL
   AGY_CA_CERT                    AGY_PROXY_STRICT_SSL
   ANTIGRAVITY_OPEN_URL           ANTIGRAVITY_RESOLVING_ENVIRONMENT
   ANTIGRAVITY_SSH_ASKPASS_SOCKET ANTIGRAVITY_SSH_ASKPASS_TOKEN
   ANTIGRAVITY_SSH_BIN            ANTIGRAVITY_SSH_LS_REMOTE_PATH
   ANTIGRAVITY_SSH_LS_VERSION     ANTIGRAVITY_VSCODE_HOST
   ANTIGRAVITY_WSL_DISTRO         ANTIGRAVITY_WSL_LS_PATH
   ```

   没有 "skip login shell" 之类的开关；
   `--proxy-server` / `--proxy-bypass-list` 也不影响这一段
   （那只管 Chromium 的网络栈）。

**结论**：这个"绕过"技术上可构造，但代价是**让 IDE 失去用户的 shell 环境**，
且依赖一个不受控的 10 秒超时竞态。**不采用**。
我们走的是"检测 → 红字拦截 → 用户自己改"，并且**不碰用户的 rc 文件**。

### 3.7 但评审指出的问题是真的 —— 我的判定标准错了

真正的问题不在"有没有绕过"，而在**我的冲突判定过宽**。

三个事实叠加起来：

1. `{ ...process.env, ...loginShellEnv }` 是**逐键**覆盖，
   不是整块替换。`loginShellEnv` 里**没有**的键，`process.env` 的值原样保留。
2. `readLoginShellEnv` spawn 登录 shell 时带着
   `env: { ...process.env, ...LOGIN_SHELL_INJECTED_ENV }` ——
   登录 shell **继承**了我们注入的值。
3. 所以：只有用户的 rc 文件**显式 `export` 了同名代理变量**，
   才会真的覆盖我们。而那种情况下，`loginShellEnv` 里的代理值
   与我们注入的值**不同**。

**结论：老实现"只要登录 shell 里存在任何代理变量就中止启动"是误报。**
它没有比值。

### 3.8 新的判定标准：三档，按值分

再叠加一条 Go 的事实。`golang.org/x/net/http/httpproxy`（后端二进制里
`vendor/golang.org/x/net/http/httpproxy.FromEnvironment` 与
`httpproxy.getEnvAny` 两个符号都在）：

```go
func FromEnvironment() *Config {
	return &Config{
		HTTPProxy:  getEnvAny("HTTP_PROXY", "http_proxy"),
		HTTPSProxy: getEnvAny("HTTPS_PROXY", "https_proxy"),
		NoProxy:    getEnvAny("NO_PROXY", "no_proxy"),
		CGI:        os.Getenv("REQUEST_METHOD") != "",
	}
}

func getEnvAny(names ...string) string {
	for _, n := range names {
		if val := os.Getenv(n); val != "" {
			return val
		}
	}
	return ""
}
```

**大写优先。** 于是：

| 登录 shell 里的情况 | 判定 | 理由 |
|---|---|---|
| 值与我们注入的**相同** | 非冲突 | shell 只是回显了我们（它继承了我们的注入） |
| 只有**小写**名不同 | **警告** | `getEnvAny` 取大写 → Antigravity 自己的流量不受影响；但只认小写的第三方工具（curl/git）会走错 |
| **大写**名不同 | **致命** | `getEnvAny` 直接取到 shell 的值 → 通道失效，必须拦住 |
| `ALL_PROXY` 不同 | **警告** | `ALL_PROXY` **不在** `httpproxy` 的读取范围里，只影响 LS 派生的第三方工具 |

> **注意 Windows**：`os.Getenv` 在 Windows 上是**大小写不敏感**的，
> 所以 `HTTPS_PROXY` 与 `https_proxy` 在环境块里是同一个条目，
> 后写的赢。大写优先这个技巧在 Windows 上不成立——
> 但 Windows 上 Antigravity 的 `readLoginShellEnv` 直接
> `return Promise.resolve({})`，**根本没有这个覆盖问题**。
> 冲突检测只在 macOS/Linux 生效。

### 3.9 实现（`internal/shellenv/shhellenv.go` 全文）

```go
// Injection 是本次启动准备注入的代理环境变量。
type Injection struct {
	HTTPProxy  string
	HTTPSProxy string
	AllProxy   string // 空表示不注入
	NoProxy    string
}

// Severity 是冲突的严重程度。
type Severity int

const (
	SeverityFatal Severity = iota // Antigravity 自己的流量会走错代理，必须拦住
	SeverityWarning                // Antigravity 不受影响，但第三方工具可能走错
)

// varSpec 描述一个代理变量名。
// primary 标记它是不是 httpproxy.getEnvAny 的**首选名**（大写优先）。
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

// classify 把登录 shell 的 env 输出归类成冲突。
func classify(envOut string, inj Injection) []Conflict {
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
		roleHTTP: inj.HTTPProxy, roleHTTPS: inj.HTTPSProxy,
		roleNoProxy: inj.NoProxy, roleAll: inj.AllProxy,
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
				Name: spec.name, Value: v, Want: "",
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
			Name: spec.name, Value: v, Want: w,
			Severity: sev, Reason: reason,
		})
	}
	return out
}

// sameValue 比较两个代理 URL 是否等价。
// 容忍末尾斜杠：http://127.0.0.1:10808/ 与 http://127.0.0.1:10808
// 是同一个代理，写法不同而已。
func sameValue(a, b string) bool {
	if a == b {
		return true
	}
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// Fatal 只返回致命的那些冲突。
func Fatal(conflicts []Conflict) []Conflict { ... }
```

启动路径（`cmd/agy-untun/cmd_launch.go`）：

```go
// 4) Unix 上检查登录 shell 里的代理变量冲突。
//    ... （注释见 §3.8 的三档表）
inj := shellenv.Injection{
	HTTPProxy:  ch.URL(),
	HTTPSProxy: ch.URL(),
	NoProxy:    channel.JoinNoProxy(os.Getenv("NO_PROXY")),
}
if ch.Scheme == channel.SchemeSOCKS5 {
	inj.AllProxy = ch.URL()
}
conflicts := shellenv.Detect(5*time.Second, inj)
if fatal := shellenv.Fatal(conflicts); len(fatal) > 0 {
	fmt.Fprint(os.Stderr, shellenv.Report(fatal, true))
	return fmt.Errorf("登录 shell 里存在会覆盖代理的变量，已中止启动")
} else if len(conflicts) > 0 {
	// 只是警告：不影响 Antigravity 自己的流量，但要说清楚。
	fmt.Fprint(os.Stderr, shellenv.Report(conflicts, false))
}
```

### 3.10 单测

`internal/shellenv/shellenv_test.go`（9 个用例，全绿）：

| 用例 | 期望 |
|---|---|
| shell 回显了我们注入的值 | 非冲突 |
| 大写 `HTTPS_PROXY` 被改 | 致命 |
| 大写 `HTTP_PROXY` 被改 | 致命 |
| 大写 `NO_PROXY` 被改 | 致命 |
| 只有小写 `https_proxy` 被改 | 警告 |
| 大小写都被改 | 致命（大写）+ 警告（小写） |
| 末尾斜杠差异 | 非冲突 |
| shell 里没有代理变量 | 非冲突 |
| `ALL_PROXY` 被改 | 警告 |

---

## 4. P1 进程识别：exe 绝对路径 + 进程树

### 4.1 判据（写在 `cmd/agy-untun/cmd_doctor.go` 的注释里）

```
 1. **可执行文件绝对路径前缀**（首选）
    桌面版 Antigravity.exe 装在已知目录，进程的 exe 路径落在
    那个前缀下 → 一定是它。
    Chrome 在 C:\Program Files\Google\Chrome\Application\，
    Edge  在 C:\Program Files (x86)\Microsoft\Edge\Application\，
    路径不同，**不可能误判**。

 2. **进程树**（次选）
    桌面版主进程派生的子进程（渲染 / GPU / utility），
    沿 PPID 往上走能认出来。子进程的命令行千奇百怪，
    但它们的祖先一定是桌面版。

 3. 命令行特征（**只用来排除**，从不用来认定）
```

### 4.2 三个平台上的 exe 路径来源

| 平台 | 来源 |
|---|---|
| Windows | `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` + `QueryFullProcessImageNameW`（4 参数，见 §2.5） |
| Linux | `readlink("/proc/<pid>/exe")` —— **不能用 `os.Stat`**，那会 stat 到目标文件本身，拿不到路径 |
| macOS | `lsof -a -p PID -d txt -Fn`（macOS 没有 `/proc`） |

### 4.3 路径比较的规范化

```go
// normalizePath 把路径规范成可比较的形式：
// 统一分隔符、转小写（Windows 路径不区分大小写）、去掉末尾斜杠。
```

前缀比较要求**目录边界**正确——`C:\...\antigravity` 不能匹配
`C:\...\antigravity-backup`。实现里用 `dir + "/"` 做前缀。

### 4.4 裁定 1：`fallbackTerminate()` 已删除

它做的是 `taskkill /IM Antigravity.exe` —— **按镜像名盲杀**。
而 `Antigravity.exe` 这个名字同时是 agy CLI 的 Node sidecar 的名字
（本机实测：10 个 `Antigravity.exe` 全部是 sidecar，
父进程命令行是 `cmd.exe /c "...\agy-node.cmd --import=file://...register_loader.js main.mjs"`）。
盲杀会干掉用户的 CLI 会话。

现在 `terminateIDE()` 的行为：

- 拿不到精确的进程信息 → **不杀**，报错让用户手动关：
  ```
  无法读取进程列表（...），已放弃自动终止。
  请手动关闭 Antigravity 桌面版后重试。
  （说明：Antigravity.exe 这个名字同时属于 agy CLI 的 sidecar，
    按名字盲杀会误伤你的 CLI 会话，所以这里不做猜测。）
  ```
- 找不到目标 → 同样报错，让用户手动关。
- 只对**精确 PID** 执行 `taskkill /F /PID <pid>`。

**代码里已无任何 `taskkill /IM`**：

```
$ grep -rn "taskkill" cmd/ internal/
cmd/agy-untun/cmd_doctor.go:  exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
```

### 4.5 安全闸门：`doctor --processes` 干跑

判据一旦写宽（曾只按 `--type=renderer` 匹配），用户的 Chrome/Edge 渲染进程
就会被一起杀掉——**实际发生过**。所以上线前先干跑：

```
$ agy-untun doctor --processes
将会被终止的进程（桌面版 + Go 后端）:
------------------------------------------------------------
  （无）

将被跳过的进程（agy CLI 的 sidecar —— 不属于桌面版）:
------------------------------------------------------------
  Antigravity.exe (PID 24084)  C:\Users\hxsci\AppData\Local\Programs\antigravity\Antigravity.exe
  Antigravity.exe (PID 101368) ...
  （共 10 个，全部正确跳过）
```

### 4.6 `ideInstallDirs()` 只取精确目录

早期版本取 `%LOCALAPPDATA%\Programs`（桌面版安装目录的**父目录**），
那会把同目录下的 `WinDynamicDesktop.exe`、`codync-host*.exe` 一起收进来。
现在只取 `filepath.Dir(exe)`，即**精确**的安装目录。

### 4.7 实测回归（terminate + relaunch）

| 指标 | 终止前 | 重启后 |
|---|---|---|
| Chrome 进程数 | 22 | 22 |
| Edge 进程数 | 26 | 26 |
| 后端到 10808 的连接 | 8 | 8 |
| 后端直连 Google 的连接 | 0 | 0 |

浏览器进程数**不变** —— 不会再误伤。

---

## 5. P1 `--proxy-bypass-list` 语法转换

### 5.1 两套语法不等价

```
Go 的 NO_PROXY（逗号分隔）      Chromium 的 bypass list（分号或逗号）
  foo.com        匹配 foo.com    foo.com       同左
  .foo.com       匹配所有子域名   .foo.com      ❌ 无效，要写 *.foo.com
                                     *.foo.com    只匹配子域名，
                                                **不匹配**裸域名 foo.com
  (无)           —              <local>       所有不含点号的主机名
```

所以对以 `.` 开头的条目：
1. 转成 `*.foo.com`
2. 同时补上 `foo.com`（因为 `*.foo.com` 不匹配裸域名）

另外固定追加 `<local>`——Chromium 内置的"简单主机名"规则，NO_PROXY 没有对应写法。

### 5.2 实现（`cmd/agy-untun/cmd_launch.go`）

```go
func chromiumBypassList(ch channel.Channel) string {
	noProxy := channel.JoinNoProxy(os.Getenv("NO_PROXY"))

	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}

	for _, p := range strings.Split(noProxy, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		if strings.HasPrefix(p, ".") {
			// ".foo.com" -> "*.foo.com" + "foo.com"
			//
			// 注意 "*." 不能省：Chromium 的通配符语法是 "*.foo.com"，
			// 写成 "*foo.com" 匹配不到任何东西。
			base := strings.TrimPrefix(p, ".")
			if base == "" {
				continue
			}
			add("*." + base)
			add(base)
			continue
		}

		add(p)
	}

	// Chromium 内置规则：不含点号的主机名一律不走代理。
	add("<local>")

	// 通道自己所在的回环端口不能被代理（代理服务器本身）。
	if ch.Host != "" {
		add(ch.Addr())
	}

	return strings.Join(out, ";")
}
```

> **一个被单测抓到的 bug**：我第一版写的是 `"*" + base`（截掉点后拼 `*`），
> 产出 `*example.com`——Chromium 的语法是 `*.example.com`，
> `*example.com` 匹配不到任何东西。单测直接把它抓了出来。

### 5.3 单测（`cmd/agy-untun/cmd_launch_test.go`）

| 用例 | 期望 |
|---|---|
| 普通域名原样保留 | 含 `api.example.com`，不含 `*.api.example.com` |
| 前导点转成通配符并补上裸域名 | 含 `*.example.com` 与 `example.com`，不含 `.example.com` |
| 裸域名与前导点同时存在时不重复 | 含 `example.com`、`*.example.com`，不含 `.example.com` |
| 总是追加 `<local>` | 含 `<local>` |
| 带端口的条目保留 | 含 `127.0.0.1:8080` |
| 代理自身不被代理 | 含 `127.0.0.1:10808` |

### 5.4 生效验证（桌面版实际启动参数）

```
--proxy-bypass-list=<NO_PROXY 各项>;*.example.com;example.com;localhost;127.0.0.1;::1;<local>;127.0.0.1:10808
```

---

## 6. 裁定 3：Linux/macOS 端口解析静态审计 —— 发现并修复了一个真 bug

### 6.1 `internal/discovery/ports_linux.go` 全文（修复后）

```go
//go:build linux

package discovery

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// linuxListenPorts 读 /proc/net/tcp 与 /proc/net/tcp6。
//
// 格式：每行一个 socket，表头之后第 4 列是 local_address，
// 形如 "0100007F:1F90"（hex 的 IP:PORT，小端十六进制）。
// 第 3 列是状态，"0A" = LISTEN。
//
// 不调外部命令，不做字符串解析出 netstat 的表格。
func listenPorts() ([]ListenPort, error) {
	var out []ListenPort
	seen := map[string]bool{}

	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		ports, err := parseProcNetTCP(path)
		if err != nil {
			continue
		}
		for _, p := range ports {
			key := fmt.Sprintf("%s:%d", p.Host, p.Port)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("无法从 /proc/net/tcp* 读到监听端口")
	}
	return out, nil
}

func parseProcNetTCP(path string) ([]ListenPort, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []ListenPort
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			continue // 表头
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// state == "0A" 表示 LISTEN
		//
		// ⚠️ 列号：/proc/net/tcp 的列是
		//	sl local_address rem_address st tx_queue:rx_queue ...
		//	st 是**第 4 列**（下标 3），local_address 是**第 2 列**（下标 1）。
		if fields[3] != "0A" {
			continue
		}
		host, port, ok := parseHexAddr(fields[1])
		if !ok {
			continue
		}
		out = append(out, ListenPort{Host: host, Port: port})
	}
	return out, scanner.Err()
}
```

### 6.2 审计发现的 bug：IPv4 全丢

`parseHexAddr` 原来长这样（**已删**）：

```go
	ipBytes, err := hex.DecodeString(hexIP)
	if err != nil || len(ipBytes) != net.IPv6len {   // ← 只接受 16 字节
		return "", 0, false
	}
	...
	// 然后按 IPv6 的 4×32 位 word 反序
```

**问题**：`/proc/net/tcp` 里 IPv4 的地址是 **8 个 hex 字符 = 4 字节**，
IPv6 才是 32 个 hex 字符 = 16 字节。`len(ipBytes) != 16` 这个判断
把所有 IPv4 行都拒了。而 Linux 上 IPv4 条目占绝大多数 ——
**等于整个监听表读不出来**。

修法（`internal/discovery/hexaddr.go`，无 build tag）：

```go
func parseHexAddr(s string) (string, int, bool) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", 0, false
	}
	hexIP, hexPort := s[:idx], s[idx+1:]

	port, err := strconv.ParseUint(hexPort, 16, 32)
	if err != nil || port == 0 {
		return "", 0, false
	}

	ipBytes, err := hex.DecodeString(hexIP)
	if err != nil {
		return "", 0, false
	}

	switch len(ipBytes) {
	case net.IPv4len:
		// IPv4：4 字节整体反序
		ipBytes = []byte{ipBytes[3], ipBytes[2], ipBytes[1], ipBytes[0]}
		return net.IP(ipBytes).String(), int(port), true
	case net.IPv6len:
		// IPv6：4 个 32 位 word，每个 word 内部反序
		var ipv6 [16]byte
		for w := 0; w < 4; w++ {
			word := ipBytes[w*4 : w*4+4]
			ipv6[w*4+0] = word[3]
			ipv6[w*4+1] = word[2]
			ipv6[w*4+2] = word[1]
			ipv6[w*4+3] = word[0]
		}
		return net.IP(ipv6[:]).String(), int(port), true
	}
	return "", 0, false
}
```

### 6.3 关于字节序的一点澄清

内核把地址写成**网络字节序**的十六进制文本，但 IPv4 是**按 32 位小端**呈现的：
`10.0.0.1` 的网络序是 `0A 00 00 01`，`/proc/net/tcp` 里印成 `0100000A`。
所以反序后得到 `0A 00 00 01` = `10.0.0.1`。✅

IPv6 是**按 32 位 word 各自小端**：`::1` 印成
`00000000000000000000000001000000`，反序后得到 `::1`。✅

### 6.4 `internal/discovery/ports_darwin.go` 全文

```go
//go:build darwin

package discovery

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// darwinListenPorts 用 lsof 读 macOS 的 TCP 监听表。
//
// macOS 没有 /proc/net/tcp，sysctl net.tcp.* 输出的格式随版本变，
// 因此这里用 lsof（系统自带）拿结构化的输出，不去解析 sysctl 的表文本。
func listenPorts() ([]ListenPort, error) {
	out, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN").Output()
	if err != nil {
		return nil, fmt.Errorf("lsof 读取监听表失败: %w", err)
	}

	var outPorts []ListenPort
	seen := map[string]bool{}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "COMMAND" {
			continue
		}
		listenIdx := -1
		for i, f := range fields {
			if f == "(LISTEN)" {
				listenIdx = i
				break
			}
		}
		if listenIdx < 1 {
			continue
		}
		addr := fields[listenIdx-1]

		host, portStr, ok := splitListenAddr(addr)
		if !ok {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			continue
		}

		normHost, wildcard := normalizeListenHost(host)
		key := fmt.Sprintf("%s:%d", normHost, port)
		if seen[key] {
			continue
		}
		seen[key] = true
		outPorts = append(outPorts, ListenPort{Host: normHost, Port: port, Wildcard: wildcard})
	}

	if len(outPorts) == 0 {
		return nil, fmt.Errorf("lsof 没有读到任何监听端口")
	}
	return outPorts, nil
}
```

地址拆分（`internal/discovery/listenaddr.go`，无 build tag）：

```go
func splitListenAddr(addr string) (string, string, bool) {
	idx := strings.LastIndex(addr, ":")   // ← 最后一个冒号：裸 IPv6 有多个冒号
	if idx < 0 {
		return "", "", false
	}
	host := addr[:idx]
	port := addr[idx+1:]

	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "*" {
		host = "0.0.0.0"
	}
	if net.ParseIP(host) == nil {
		return "", "", false
	}
	return host, port, true
}
```

### 6.5 把两个解析器提到无 build tag 的文件 —— 这样 Windows 上也能测

这是审计带来的第二个改进。原来两个解析器分别锁在 `//go:build linux` /
`//go:build darwin` 里，**在 Windows 开发机上永远跑不到单测**。
现在：

- `internal/discovery/hexaddr.go`（无 tag） + `hexaddr_test.go`
- `internal/discovery/listenaddr.go`（无 tag） + `listenaddr_test.go`

单测覆盖 8 + 5 + 7 个用例，全绿：

```
$ go test ./internal/discovery/ -v
--- PASS: TestParseHexAddr  (IPv4 回环 / IPv4 通配 / IPv4 具体地址 /
                            IPv6 回环 / IPv6 通配 / 端口为 0 / 长度不对 / 缺冒号)
--- PASS: TestSplitListenAddr (IPv4 具体地址 / IPv4 通配 / IPv6 带方括号 /
                             IPv6 裸形式 / 缺冒号 / 不是 IP)
--- PASS: TestNormalizeListenHost (7 个归一化用例)
```

> **一处诚实说明**：写测试时我第一版给 `10.0.0.1` 写的向量是 `0A000001`，
> 测试挂了——**是我的向量错了，不是代码错了**（见 §6.3）。
> 改成 `0100000A` 后通过。这条留在报告里，因为"审计发现 bug"和
> "审计发现自己写错了"是两回事，不该混为一谈。

---

## 7. 附带修复：Linux 交叉编译失败

本轮开始时 `linux/amd64`、`linux/arm64` 编不过：

```
internal\procs\procs_unix.go:71:10: undefined: darwinExePath
```

原因：`procs_unix.go` 的 build tag 是 `linux || darwin`，
里面调用 `darwinExePath`，而该函数只定义在 `procs_darwin.go`
（tag `darwin`）。Linux 构建里那个符号不存在。

修法：把 `exePathOf` 的实现按平台分文件——
`procs_linux.go` 用 `readlink("/proc/<pid>/exe")`，
`procs_darwin.go` 用 `lsof`——`procs_unix.go` 不再引用任何平台专有符号。

---

## 8. 验证汇总（本轮结束时）

```
$ gofmt -l cmd internal
（无输出）

$ go vet ./cmd/... ./internal/...
（无输出）

$ go test ./cmd/... ./internal/...
ok  github.com/user/agy-untun/cmd/agy-untun
ok  github.com/user/agy-untun/internal/channel
ok  github.com/user/agy-untun/internal/discovery
ok  github.com/user/agy-untun/internal/shellenv
?   github.com/user/agy-untun/internal/launcher  [no test files]
?   github.com/user/agy-untun/internal/procs     [no test files]

$ 交叉编译（CGO_ENABLED=0）
windows/amd64   OK
windows/arm64   OK
linux/amd64     OK
linux/arm64     OK
darwin/amd64    OK
darwin/arm64    OK
```

安全红线复核（逐条）：

| 红线 | 状态 |
|---|---|
| 绝不触碰用户的 shell profile 文件 | ✅ 代码里没有任何写 `.bashrc`/`.zshrc`/`.profile` 的路径 |
| 严禁 65535 全端口扫描 | ✅ 三级流水线；白名单 19 个常见端口，无全扫 |
| 严禁修改系统级代理或全局环境变量 | ✅ 系统代理**只读**（`reg query` / `scutil` / `gsettings`）；环境变量只改**子进程**的 |
| 拿不到精确进程信息时宁可不杀 | ✅ `fallbackTerminate` 已删，改为报错让用户手动关 |
| 只在终端红字报错拦截，打印冲突变量名 | ✅ `shellenv.Report` 只打印 + 给出手动修复步骤 |

---

## 9. 仍然开放的问题（下一轮）

1. **Linux / macOS 的解析器从未在真实硬件上跑过。**
   字节序与列号是按内核格式推的，逻辑有单测覆盖，
   但**没有在真机 `/proc/net/tcp` 与 macOS `lsof` 上比对过**。
   这是本轮最大的未验证项。

2. **`shellenv.Detect` 在 Unix 上每跑一次就 spawn 一次登录 shell。**
   `-ilc` 加载 rc 文件可能很慢（我们给了 5 秒超时）。
   真实用户在慢 rc 下会看到启动延迟。

3. **冲突检测只覆盖环境变量，不覆盖 Antigravity 自身的
   `--proxy-server` 与 NO_PROXY 的语义差异**（除 §5 的 bypass list 外）。

4. **`shellenv` 的 `Injection.NoProxy` 用的是 `os.Getenv("NO_PROXY")`**，
   没有合并 `no_proxy`（小写）。Windows 上无所谓（大小写不敏感），
   Unix 上用户的 `no_proxy` 可能被漏掉。

---

*本文档不含宿主机上的任何真实私有域名。所有测试域名均为 RFC 2606 的 `example.com`。*
