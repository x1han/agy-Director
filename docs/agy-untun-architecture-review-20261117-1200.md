# agy-untun 架构评审报告

> 生成时间：2026-11-17 12:00 (Asia/Shanghai)
> 项目路径：`D:\agy-untun`
> Git 仓库：已初始化，初始提交 `c104a09`，分支 `master`
> 本报告用途：供专家团评审架构与技术选型，列出待决策问题与风险
> 代码位置：全部源码在 `D:\agy-untun`（Go，2344 行含测试），本报告只嵌入**关键决策片段**与**实测证据**，不复制全部源码

---

## 0. 一页速览

| 项 | 结论 |
|---|---|
| 问题 | Antigravity 桌面版与 `agy` CLI 必须开 TUN 才能正常联网 |
| 根因 | Go 后端只从**环境变量**读代理，不读系统代理注册表 |
| 解法 | 启动前探测本地代理端点（http/socks5），注入进程级代理环境变量再启动 |
| 语言 | **Go**（用户已定），产物 8.7–9.5 MB，交叉编译 6 平台零配置 |
| 交互 | **纯 CLI**，无 GUI、无托盘，所有对话走终端 |
| 目标 | 同时支持 Antigravity 桌面版（Electron）与 `agy` CLI（Go 原生） |
| 当前状态 | 核心逻辑已实现并**端到端验证通过**（后端 8 条连接全走代理，直连 0） |
| 待决 | 5 个问题见 §8 |

---

## 1. 问题根因（实证）

Antigravity 的网络请求由 Go 后端 `language_server` 发出（桌面版 `resources/bin/language_server.exe`，197 MB）。

**观察**：在 `language_server.exe`（215,547,392 字节）中检索符号：

```
HTTPS_PROXY ×2   HTTP_PROXY ×3   ALL_PROXY ×1   NO_PROXY ×2
https_proxy ×2   http_proxy ×3   all_proxy ×3   no_proxy ×2
golang.org/x/net/http/httpproxy  ×44
google3/third_party/jetski/language_server/code_assist_client/codeassistclient.hasConfiguredProxyEnv
google3/third_party/jetski/language_server/code_assist_client/codeassistclient.resolveProxyEnvState
google3/third_party/jetski/language_server/code_assist_client/codeassistclient.dynamicEnvProxy
```

**观察**：同一二进制中，`WinHttpGetIEProxyConfig` / `InternetQueryOption` / 注册表读取 API **零命中**。

**推论**：后端的代理解析完全来自环境变量（`golang.org/x/net/http/httpproxy.FromEnvironment`），Windows 系统代理（`ProxyEnable=1`, `ProxyServer=127.0.0.1:10808`）对它不可见。这就是必须开 TUN 的根本原因——TUN 在网络层拦截，不依赖应用层代理配置。

**环境变量透传路径**（`resources/app.asar` → `dist/languageServer.js`）：

```js
// Electron apps don't inherit shell environment variables when they are not
// launched through the terminal.
const env = { ...process.env, ...loginShellEnv };
env['AGY_BROWSER_ACTIVE_PORT_FILE'] = getActivePortFilePath();
setupNodeModules(env, [...]);
_lsProcess = spawn(spawnCmd, spawnArgs, { stdio: [...], env });
```

**推论**：在 Electron 父进程注入 `HTTPS_PROXY`，后端子进程即继承。

### 1.1 ⚠️ 平台差异（Unix 上会静默失效）

`dist/loginShellEnv.js`：

```js
function readLoginShellEnv(opts) {
    if (process.platform === 'win32') {
        return Promise.resolve({});          // Windows: 空对象
    }
    // ... 读登录 shell 的 env
}
```

结合上面的 `const env = { ...process.env, ...loginShellEnv }`：

| 平台 | 后端实际 env | 注入是否生效 |
|---|---|---|
| Windows | `process.env`（无 shell 覆盖） | ✅ 一定生效 |
| macOS/Linux | 登录 shell env **覆盖** `process.env` | ⚠️ 若用户 rc 里有 `export http_proxy=...` 则被顶掉 |

**这是本项目最大的跨平台风险点**，Windows 上测通不能代表 Unix 测通。实现上必须在 Unix 上先检测 shell env 冲突（`doctor` 已实现检测，`launch` 尚未拦截）。

---

## 2. 关键发现：`socks5://` 通道可用

**观察**：二进制中含

```
proxy address %q has unsupported scheme %q; must be http, https, or socks5
golang.org/x/net/proxy/proxy.SOCKS5
golang.org/x/net/proxy/proxy.FromURL
golang.org/x/net/proxy/proxy.FromEnvironmentUsing
```

**实证（决定性测试）**：杀掉所有 Antigravity 进程后，用 `socks5://127.0.0.1:10808` 注入环境变量启动：

```
spawned pid 86660 → LS pid 89720
to-proxy(10808) = 8     direct(443) = 0
```

日志中 `unexpected EOF` / `proxy_connect_failed` / `unsupported scheme` 计数 = **0**。

**推论**：通道可抽象为 `http://` 或 `socks5://` 的本地端点，与具体客户端无关。这是「只关注通道，不绑定 v2ray」的技术基础。

**协议识别实测**（同一端口可能是 http、socks5、mixed 或都不是）：

```
10808  http: http-proxy  | socks5: socks5-ok     ← v2rayN（protocol: "mixed"）
10810  http: http-proxy  | socks5: socks5-ok     ← v2rayN 局域网口（listen=0.0.0.0）
7890   http: error       | socks5: error
3010   http: not-http    | socks5: null         ← 是别的服务
```

---

## 3. 目标对象：桌面版 与 `agy` CLI

### 3.1 Antigravity 桌面版

**观察**（`resources/app.asar` → `package.json`）：

```json
{ "name": "antigravity", "productName": "Antigravity", "version": "2.32.1",
  "main": "dist/main.js" }
```

**观察**：`resources/version` 文件内容为 `44.9.1`——这是 **Electron 版本**，不是 Antigravity 版本。**易错点**：`app.getVersion()` 返回 Electron 版本，`package.json` 里的 `2.32.1` 才是产品版本。

**观察**：Antigravity **没有 CLI 参数**（检索 `dist/main.js` 仅发现 `--wsl-distro` 与 `--headless`），只能 `spawn` 可执行文件本体。

**观察**（单实例锁）：

```js
const gotTheLock = electron_1.app.requestSingleInstanceLock();
if (!gotTheLock) { electron_1.app.quit(); process.exit(0); }
```

**推论**：第二次启动会转发 argv 给已有实例并立刻退出，**丢掉新 env**。已有实例在跑时必须中止启动，否则用户以为走了代理，实际用的是没代理的旧实例。

**观察**（存活判定）：`app.getPath('userData')/DevToolsActivePort` 内容为

```
65116
/devtools/browser/8fec480f-fa33-4226-b29c-44a738db5e72
```

请求 `http://127.0.0.1:65116/json/version` 返回：

```json
{"Browser":"Chrome/152.0.7977.167",
 "User-Agent":"... Antigravity/2.32.1 Chrome/152.0.7977.167 Electron/44.9.1 ...",
 "webSocketDebuggerUrl":"ws://127.0.0.1:65116/devtools/browser/8fec480f-..."}
```

**⚠️ 实测坑**：`DevToolsActivePort` 在进程被强杀后**残留不删**（Electron 不会清理）。只凭文件存在判断「在运行」会误报。本项目已改为**必须 CDP 端点可达**才算在运行。

### 3.2 `agy` CLI

**观察**：从 Google 官方安装脚本 `https://antigravity.google/cli/install.sh` 反查：

```bash
TARGET_DIR="$HOME/.local/bin"        # Unix 默认
BINARY_PATH="$TARGET_DIR/agy"
# 7. Native Setup Handoff (Go-Native Setup Trigger - POSIX /bin/sh Compliant)
```

Windows 版 `https://antigravity.google/cli/install.ps1`：

```powershell
$TARGET_DIR = Join-Path $env:LOCALAPPDATA "agy\bin"
$binaryPath = Join-Path $TARGET_DIR "agy.exe"
```

**观察**：6 个平台全部发布（v1.5.9）：

```
linux_amd64 / linux_amd64_musl / linux_arm64
darwin_arm64 / darwin_x64（同一 platform key 命名）
windows_amd64 / windows_arm64
```

**观察**：下载真实二进制（232 MB）并运行：

```
$ agy --version
1.5.9

$ agy --help
  --add-dir  --agent  -c/--continue  --conversation  --dangerously-skip-permissions
  --effort  -i/--prompt-interactive  --input-format  --json-schema  --log-file
  --mode  --model  --new-project  --output-format  -p/--print  --print-timeout
  --project  --prompt  --prompt-interactive  --remote-agent  --remote-control
  --sandbox  --settings
Available subcommands:
  acp  agent  agents  changelog  help  install  mcp  mic-serve  models
  plugin  plugins  remote-control  update
```

**观察**：agy 二进制内**嵌了 language server**（`language_server` 符号 63055 次），日志在 `~/.gemini/antigravity-cli/cli.log`，内容是同一个 Go server。

**观察**：agy 读环境变量代理（二进制符号）：

```
HTTPS_PROXY ×1  HTTP_PROXY ×3  ALL_PROXY ×1  NO_PROXY ×1
x/net/http/httpproxy ×22  proxy.FromEnvironment ×4  proxy.SOCKS5 ×3
```

**实证**：

```bash
$ HTTPS_PROXY=socks5://127.0.0.1:10808 agy models
Fetching available models...
gemini-4-argon-medium    Gemini 4 Argon (Medium)
...
```

**决定性判定**（抓出站连接）：

```
pid=23988 -> 127.0.0.1:10808 Established
```

**推论**：桌面版与 CLI **都是 Go 后端、都只读环境变量**，注入机制完全一致。差别只在启动目标。

### 3.3 平台发布矩阵（从 updater channel 反查）

`dist/updater.js` 按平台拼 channel，实测各 channel 的 manifest HTTP 状态：

| 平台 | channel | 状态 | 产物 |
|---|---|---|---|
| win-x64 | `latest-x64-win` | 200 | `Antigravity-x64.exe` (NSIS) |
| win-arm64 | `latest-arm64-win` | 200 | ✅ |
| mac-x64 | `latest-x64-mac` | 200 | `Antigravity.zip` (.app bundle) |
| mac-arm64 | `latest-arm64-mac` | 200 | ✅ |
| linux-x64 | `latest-x64-linux` | 200 | `Antigravity.AppImage` + `.deb` |
| **linux-arm64** | `latest-arm64-linux` | **404** | **Linux arm64 未发布** |

**推论**：agy-untun 自身支持 linux-arm64，但**其目标 Antigravity 桌面版在 linux-arm64 上不存在**。Linux arm64 用户只能用 agy CLI（该平台 agy 确有发布）。

### 3.4 Linux 上为什么优先 CLI

桌面版在 Linux 是 AppImage/.deb（位置不确定、无固定安装路径）；agy CLI 是固定路径 `~/.local/bin/agy` 的单文件。**Linux 上应优先找 agy CLI**——已实现。

---

## 4. 技术选型：为什么是 Go

用户已定 Go。以下是**实测数据**支撑，非偏好判断。

### 4.1 交叉编译实测

| 栈 | 目标 | 结果 | 耗时 | 产物大小 |
|---|---|---|---|---|
| **Go 1.25.1** | linux-amd64/arm64、darwin-amd64/arm64、windows-amd64/arm64 | ✅ 全通 | ~10 s / 目标 | **8.7–9.5 MB** |
| Bun 1.3.14 | 同上 | ✅ 全通 | 127–366 s / 目标（需下载 ~100 MB 目标运行时） | 63–98 MB |
| Rust 1.95 | linux-gnu | ❌ `linker 'cc' not found`（需额外装 zig/mingw） | — | 125 KB（最小） |

Go 的 `GOOS/GOARCH` 零配置零工具链，是唯一「一次命令出 6 个包」的方案。

### 4.2 Go 对本项目的天然契合

- `golang.org/x/net/proxy` 自带 SOCKS5 dialer；实测 3 行完成握手（响应 `[5 0]`）
- `net/http.Transport` + `http.ProxyURL` **原生支持 `socks5://` scheme**——这正是 Antigravity 后端的行为，验证逻辑与目标行为一致
- `net.DialTimeout` / `net.Listen` 做端口探测
- 静态链接，用户端零运行时依赖

**唯一外部依赖**：`golang.org/x/net`（仅 SOCKS5 dialer 一处需要，且 `net/http` 已覆盖 90% 场景）。

### 4.3 选型对比中被否决的方案

| 方案 | 否决理由 |
|---|---|
| Bun + TS | 产物 30 倍大；交叉编译 CI 时间长 30 倍 |
| Rust + Tauri | 需重写为 Rust；交叉编译需配 linker 链；GUI 不在需求内 |
| 借用 Antigravity 自带 Node | 依赖 Electron RunAsNode fuse（当前开着，但 Google 可能关）；mac bundle 路径不同；**不带 TS 编译器**（asar 内 `typescript` 包 0 命中、`.ts` 文件 0 个、tsc 0 命中） |

---

## 5. 架构

### 5.1 包结构

```
D:\agy-untun\
├── cmd/agy-untun/          CLI 入口与子命令
│   ├── main.go             顶层路由
│   ├── cmd_launch.go       launch：选通道 → 选目标 → 单实例守卫 → 启动
│   ├── cmd_channels.go     channels：list/scan/add/remove/test
│   └── cmd_doctor.go       doctor：完整诊断 + wrap
├── internal/
│   ├── channel/            通道模型 + 协议探测 + 连通性验证
│   │   ├── channel.go      Channel 类型、URL 构造、NO_PROXY 合并
│   │   ├── probe.go        ProbeTarget / CheckReachability / VerifyChannel
│   │   └── probe_test.go
│   ├── discovery/          安装探测 + 端口枚举
│   │   ├── discovery.go    IDE/CLI 候选路径、asar 版本解析、监听端口枚举
│   │   └── discovery_test.go
│   └── launcher/           代理注入 + 启动
│       ├── launcher.go     BuildEnv / Launch / CheckRunning
│       ├── launcher_windows.go / launcher_unix.go   平台 detach + CDP 探测
│       └── version.go      User-Agent 版本提取
├── docs/                   本报告
└── .tools/go/              本地 Go 工具链（已 gitignore）
```

### 5.2 核心数据流

```
agy-untun launch
   │
   ├─► selectChannel()
   │     ├─ --proxy <URL>          最高优先
   │     ├─ 配置文件 channels.json  次优先
   │     └─ discoverChannels()     兜底：枚举监听端口 → 协议探测 → 真实请求验证
   │
   ├─► selectTarget()
   │     ├─ IDE: %LOCALAPPDATA%\Programs\antigravity\Antigravity.exe
   │     │      /Applications/Antigravity.app/Contents/MacOS/Antigravity
   │     │      /opt/Antigravity/antigravity  等
   │     └─ CLI: ~/.local/bin/agy  /  %LOCALAPPDATA%\agy\bin\agy.exe  /  PATH
   │        Linux 上 CLI 优先
   │
   ├─► 单实例守卫（仅 IDE）
   │     DevToolsActivePort → CDP /json/version 必须可达
   │     可达 → 提示并询问是否终止后重启
   │
   └─► Launch()
         env = os.Environ() + HTTP_PROXY/HTTPS_PROXY/ALL_PROXY(大小写两套)
                            + NO_PROXY(合并用户原有值)
         IDE: Setsid/CREATE_NEW_PROCESS_GROUP 脱离
         CLI: 前台运行，stdin/stdout 直连终端
```

### 5.3 关键实现片段

**代理环境变量注入**（`internal/launcher/launcher.go`）——注意大小写两套都设，且同名去重：

```go
func BuildEnv(ch channel.Channel, extra map[string]string) []string {
	env := os.Environ()
	proxyURL := ch.URL()
	// Go 的 httpproxy 同时认 HTTP_PROXY/HTTPS_PROXY 和小写，
	// 但某些子工具只认小写。
	env = setEnv(env, "HTTP_PROXY", proxyURL)
	env = setEnv(env, "http_proxy", proxyURL)
	env = setEnv(env, "HTTPS_PROXY", proxyURL)
	env = setEnv(env, "https_proxy", proxyURL)
	env = setEnv(env, "ALL_PROXY", proxyURL)
	env = setEnv(env, "all_proxy", proxyURL)
	env = setEnv(env, "NO_PROXY", channel.JoinNoProxy(os.Getenv("NO_PROXY")))
	env = setEnv(env, "no_proxy", channel.JoinNoProxy(os.Getenv("no_proxy")))
	for k, v := range extra { env = setEnv(env, k, v) }
	return env
}
```

**NO_PROXY 合并**（保留用户原有值，不覆盖）：

```go
func JoinNoProxy(existing string) string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] { return }
		seen[v] = true; out = append(out, v)
	}
	for _, part := range strings.Split(existing, ",") { add(part) }
	add("localhost"); add("127.0.0.1"); add("::1")
	return strings.Join(out, ",")
}
```

**SOCKS5 协议识别**（只做 greeting，不做 CONNECT——见 §7.1）：

```go
func probeSOCKS5(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), timeout)
	if err != nil { return false }
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil { return false }
	resp := make([]byte, 2)
	if _, err := conn.Read(resp); err != nil { return false }
	return resp[0] == 0x05 && resp[1] == 0x00
}
```

**HTTP 代理识别**（首行必须是 `HTTP/` 且含 `200`）：

```go
func probeHTTPConnect(host string, port int, timeout time.Duration) bool {
	// ... CONNECT generativelanguage.googleapis.com:443 HTTP/1.1 ...
	line, err := reader.ReadString('\n')
	if err != nil { return false }
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "HTTP/") { return false }
	return strings.Contains(line, " 200")
}
```

**连通性验证**（区分「端口在监听」与「通道真的能用」）：

```go
var ProbeURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://generativelanguage.googleapis.com/v1beta/models",
}

// 判定：5xx 与 407 失败；403 视为链路通（缺 API key 但真连到了 Google）；
// 且必须响应头里 Server 是 Google 的（ESF / scaffolding on HTTPServer2），
// 否则说明被代理侧或中间设备拦截并返回了假响应。
```

### 5.4 CLI 交互设计（全部走终端，无 GUI）

```
$ agy-untun launch --yes

  ✓ 127.0.0.1:10808    127.0.0.1:10808 (http://127.0.0.1:10808) 可用
  ✓ 0.0.0.0:10810      0.0.0.0:10810 (http://0.0.0.0:10810) 可用

启动 C:\Users\hxsci\AppData\Local\Programs\antigravity\Antigravity.exe
通道  127.0.0.1:10808 (http://127.0.0.1:10808)
代理  http://127.0.0.1:10808

已启动。
```

`doctor` 实际输出（本机真实运行，节选）：

```
运行环境
  平台        windows/amd64
  Go 版本     go1.25.1

Antigravity 桌面版
  ✓ 已找到    C:\Users\hxsci\AppData\Local\Programs\antigravity\Antigravity.exe
    版本      2.32.1

运行中的实例
  ✓ 没有运行中的桌面版实例

代理环境变量
  NO_PROXY    api.example.com,.example.com,localhost,127.0.0.1

Shell 环境冲突（仅 macOS/Linux 有效）
  · Windows 上 Antigravity 不读登录 shell 的环境变量，无此冲突

本机代理端点
  ✓ 127.0.0.1:10808        http+socks5
  ✓ 0.0.0.0:10810          http+socks5  [局域网监听]
```

---

## 6. 当前实现状态与验证结果

### 6.1 已完成

| 模块 | 状态 | 验证方式 |
|---|---|---|
| 通道模型 + NO_PROXY 合并 | ✅ | 单元测试 6 个全通过 |
| HTTP/SOCKS5 协议探测 | ✅ | 单元测试在真实 v2rayN 10808 上：`http=true socks5=true` |
| 连通性验证（真实请求 Google 端点） | ✅ | `doctor` 实测两个端点均返回 HTTP 状态 + Google Server 头 |
| 桌面版安装探测 + asar 版本解析 | ✅ | 单元测试：`asar version = 2.32.1` |
| agy CLI 安装探测 | ✅ | 候选路径推导自官方 install 脚本 |
| 监听端口枚举 | ✅ | 实测 47 个端口，回环优先排序已生效 |
| 代理环境变量注入 | ✅ | 见下方端到端 |
| 单实例守卫（CDP 可达性判定） | ✅ | 修复了残留文件误报 |
| CLI 全套子命令 | ✅ | `doctor` / `launch` 实机运行 |
| 交叉编译 6 平台 | ✅ | 产物均为正确格式（PE32+/Mach-O x2/ELF x2），8.7–9.5 MB |
| `go vet` / `go test ./...` | ✅ | 全通过 |

### 6.2 端到端验证（决定性）

```
$ agy-untun launch --yes
已启动。

# 等 22 秒后检查后端出站：
LS pid <n>
to-proxy(10808) = 8     direct(443) = 0
```

**对照实验**（此前 PowerShell 原型阶段做过）：不带启动器直接启动 Antigravity → 后端直连 `172.217.117.4:443`、`74.125.200.95:443` 等 Google IP，代理口 0 条。

### 6.3 尚未验证

| 项 | 原因 |
|---|---|
| macOS / Linux 上的实际运行 | 本机只有 Windows |
| macOS / Linux 上的 shell env 冲突处理 | 同上（代码已写，逻辑已推演，未实机验证） |
| agy CLI 经 agy-untun 启动的端到端 | 本机未安装 agy CLI（`~/.local/bin/agy` 与 `%LOCALAPPDATA%\agy\bin\agy.exe` 均无） |
| `agy-untun wrap` 的实际使用 | 未实机运行 |
| 自动拉起代理客户端（`Channel.Launch`） | 字段已定义，实现未写 |

---

## 7. 我自己认为存在的问题（希望专家重点看这几条）

### 7.1 协议探测的正确性 vs 速度权衡 ⚠️

**当前实现**：SOCKS5 探测**只做 greeting**（`05 01 00` → `05 00`），不做 CONNECT。

**原因**：最初实现做了完整 CONNECT，但在扫描阶段 1.2 s 预算下会漏判 socks5（实测 `10808 socks5: null`）。改为只做 greeting 后正确识别 `http+socks5`。

**风险**：greeting 只能证明「对端讲 SOCKS5 协议」，**不能证明它能真正出网**（比如一个只监听但不转发的 SOCKS5 端口也会通过 greeting）。真正的验证依赖后续 `CheckReachability`——但那只在 `VerifyChannel` 路径里做，`discoverChannels`（扫描路径）**只做协议探测不做真实请求**。

**问题**：`launch` 的扫描路径里，每个候选都会走 `VerifyChannel`（真实请求），所以最终选中的通道是验证过的。但 `doctor` 的「本机代理端点」和 `channels scan` 只列协议探测结果——**一个"讲 SOCKS5 但不出网"的端口会出现在候选列表里**。

**建议**：候选列表区分「协议匹配」与「完整验证」两级状态，或在 scan 时对每个候选都跑一次真实请求（代价：每个约 1–2 s，10 个候选 = 10–20 s）。

### 7.2 端口扫描的规模与超时 ⚠️

**当前实现**：`discovery.ListenPorts()` 优先用 `netstat`/`ss` 解析监听表，实测 3.2 s / 47 端口；读不到时退化为并发全端口扫描（实测 10.5 s / 65535 端口，512 并发）。

**问题**：
1. `netstat` 在 Windows 上**需要管理员权限**才能看到全部 PID/端口？——本机实测不需要，但需确认。
2. 全端口扫描退避路径会**打开 65535 个 socket 逐个试 bind**——这可能触发杀毒软件/EDR 的可疑行为告警，且在 macOS/Linux 上会短暂占用大量端口。
3. `netstat` 解析里 `parseListenLine` 用 `strings.Fields` + `LastIndex(":")`，Windows 的 IPv6 行 `[::]:port` 已处理，但**本地化输出**（非英文 Windows 的 netstat 表头）不影响解析（只按 LISTEN 关键字匹配，但中文系统上 LISTENING 仍是英文）。
4. `isSystemPort` 排除了 `<1024` 和 `49152–65535`——但**很多代理客户端用 49152+ 端口**（如某些 Clash 配置）。这会漏掉真实通道。

**建议**：`isSystemPort` 的排除策略应改为「默认全扫，但把 1024 以下和 49152+ 降级为可选深扫」。

### 7.3 单实例守卫的竞态 ⚠️

**当前实现**：`CheckRunning` 读 `DevToolsActivePort` → 请求 CDP `/json/version` → 可达才算在运行。

**问题**：
1. **TOCTOU**：检查通过后到实际 spawn 之间，旧实例可能退出（此时新实例能拿到锁，没问题）；但也可能旧实例正在退出、新实例刚起（此时新实例会被 Electron 转发 argv 并退出，**用户会看到"启动了但窗口没动"**）。当前无重试。
2. **窗口间实例**：Electron 的 `requestSingleInstanceLock` 是**按 userData 目录**的。如果用户同时用不同 userData 启动，会有两个实例——检测不到。
3. **Windows 上 terminate 不可靠**：`taskkill /IM Antigravity.exe` 杀的是所有同名进程，包括子进程；但如果 Antigravity 是以管理员权限运行的，普通权限的 agy-untun **杀不掉**（会弹 UAC）。当前实现会静默失败（taskkill 返回非 0 → 报错终止）。
4. **未处理**：杀掉旧实例后 `DevToolsActivePort` 可能仍是旧端口，新实例启动后才会更新。当前 `time.Sleep(2s)` 是硬等待。

**建议**：terminate 失败时明确提示「请手动关闭 Antigravity」并给出具体 PID，而不是只报错退出。

### 7.4 Unix shell env 冲突：检测到了但没拦截 ⚠️

**当前实现**：`doctor` 会检测（用 `shell -ilc env` 读登录 shell），但 `launch` **完全没有拦截**——用户会在 Unix 上得到一个"启动了但代理没生效"的静默失败。

**问题**：
1. `shell -ilc env` 会**执行用户的 rc 文件**，可能有副作用（oh-my-zsh 插件、nvm 初始化等），慢且有噪音。
2. 检测到冲突后**怎么修**？自动改用户的 `~/.bashrc` 是侵入性操作。
3. 冲突时是否应该直接拒绝启动？

**建议**：在 `launch` 里加检测，冲突时明确提示并给出三条选项（跳过 / 打印 rc 文件位置让用户自己改 / 提供一个 `agy-untun shell-fix` 子命令做精确的行级注释）。

### 7.5 `NO_PROXY` 语义假设 ⚠️

**当前实现**：`JoinNoProxy` 无条件追加 `localhost,127.0.0.1,::1`。

**问题**：
1. Go 的 `httpproxy` 对 `NO_PROXY` 值的匹配规则是**域名后缀匹配**（`.x.com` 匹配 `a.x.com`），裸 `localhost` 精确匹配。追加是安全的。
2. 但用户原有的 `NO_PROXY=api.example.com,.example.com,localhost,127.0.0.1` 来自注册表 `HKCU\Environment`——**这是全局设置**，agy-untun 只是继承，没改系统。
3. **真正的问题**：`HTTP_PROXY`/`HTTPS_PROXY` 只在**子进程**里设置，不污染父 shell。但如果用户用 `agy-untun wrap` 包一个长驻进程，那个进程的子进程会继承——这是期望行为。
4. **`ALL_PROXY` 的语义分歧**：`ALL_PROXY` 在 curl 里是「所有协议都走这个代理」，在 Go 的 `httpproxy` 里**不被读取**。设它主要是给其他工具用。但 `ALL_PROXY` 设成 `socks5://` 时，某些工具（如 pip）不支持 socks5，会报错。**是否该只设 http/https？**

**建议**：请专家评估 `ALL_PROXY` 的必要性，以及是否该为 socks5 通道额外提供一个 `http://` 的本地 HTTP 转发（很多工具不支持 socks5）。

### 7.6 `agy` CLI 的特殊问题 ⚠️

**观察**：agy 是 Go 原生、**内嵌 language server**、会自更新（`agy update`）、`agy install` 会改 shell 配置（PATH + alias）。

**问题**：
1. **agy 会 spawn language server 子进程**——父进程有 env 就够，但需确认子进程是否也读登录 shell env（agy 自己的实现细节，未验证）。若 agy 内部也做 shell env 合并，则 Unix 上同样有覆盖风险。
2. **agy 自更新**可能在新版本里改变代理行为。
3. `agy` 的 `--settings` 支持内联 JSON——**这可能是比环境变量更可靠的注入方式**？需要反编译 agy 看它是否读某个 settings 字段做代理。需要专家判断是否值得深挖。
4. **agy 与桌面版共享 `~/.gemini/antigravity-cli/` 数据目录**（实测），意味着两者可共享登录态——这对"统一入口"是好事，但也意味着同时跑两者可能有状态竞争。

### 7.7 其他小问题

| 问题 | 说明 |
|---|---|
| `Channel.Launch` 字段未实现 | 原型 PowerShell 版有"自动拉起 v2rayN"，Go 版只留了字段 |
| `wrap` 子命令每次都重新扫描通道 | 慢；应缓存 |
| 无配置文件迁移机制 | 从 PowerShell 版迁到 Go 版需手工重建 `channels.json` |
| 无日志 | PowerShell 版有 `agy-launcher.log`，Go 版只有 stdout |
| `cmd_doctor.go` 里 `contextUnused` 是占位 hack | 应删 |
| `terminateIDE` 依赖进程名 | macOS 上进程名是 `Antigravity`，Linux 是 `antigravity`（大小写不同），当前只写了 mac 的 |
| 未处理 `--proxy-server` | 桌面版 Electron 渲染进程走系统代理；`--proxy-server` 可能更可靠，但当前靠系统代理。**若用户没开系统代理，UI 层不走代理但后端走——会不会有混合行为问题？** |
| Windows 上 `AGY_CA_CERT` / `AGY_PROXY_STRICT_SSL` | 二进制里有这两个开关，用于 MITM 场景；当前未暴露 |

---

## 8. 需要专家团决策的问题

| # | 问题 | 我的倾向 | 影响面 |
|---|---|---|---|
| 1 | 扫描时是否对每个候选都跑真实请求？ | 建议「协议探测列候选 + 选定后才完整验证」，但需在 UI 上明确区分两级状态 | 体验 vs 速度 |
| 2 | `ALL_PROXY` 是否设置？socks5 通道要不要额外提供本地 HTTP 转发？ | 倾向不设 `ALL_PROXY`；提供 HTTP 转发作为可选功能 | 兼容性 |
| 3 | Unix shell env 冲突：自动改 rc 文件 / 只提示 / 提供独立修复命令？ | 倾向「只提示 + 提供 `shell-fix` 子命令」，不做静默自动修改 | 用户信任 |
| 4 | 单实例守卫失败（杀不掉进程）时的 UX？ | 倾向：给出 PID 让用户手动关，而不是报错退出 | 可用性 |
| 5 | `agy` CLI 是否需要 `--settings` 注入作为环境变量的备份方案？ | 倾向：先不做，除非反编译确认有代理字段 | 鲁棒性 |
| 6 | 是否要做「守护模式」（长期驻留，代理客户端断线自动重连/切换节点）？ | 倾向：先不做，MVP 边界 | 范围 |
| 7 | 通道配置是否需要支持「组」和「自动 failover」（多通道轮换）？ | 倾向：MVP 只做单通道 + 自动探测 | 范围 |
| 8 | 是否需要 Windows 服务 / launchd / systemd 集成（开机自启、菜单栏快捷方式）？ | 倾向：CLI 为主，安装器另议 | 分发 |

---

## 9. 下一步工作计划

### 第 1 阶段：补齐 MVP（预计 1–2 周）

1. **`launch` 加 shell env 冲突拦截**（Unix）——这是最紧迫的正确性问题
2. **scan/test 区分「协议匹配」与「完整验证」两级状态**，输出明确标注
3. **实现 `Channel.Launch`**：通道端口未就绪时按配置拉起代理客户端并等待就绪
4. **修 `terminateIDE` 的跨平台进程名**，失败时输出 PID 供手动处理
5. **加结构化日志**（`~/.config/agy-untun/agy-untun.log`），便于用户反馈问题
6. **补 `internal/launcher` 的单元测试**（目前 0 个）

### 第 2 阶段：跨平台验证（需要 macOS/Linux 机器或 CI）

7. **CI 矩阵**：GitHub Actions 跑 6 平台交叉编译 + 单元测试
8. **在 macOS 上实测**：shell env 冲突、`Setsid` 行为、`.app` 路径探测、Gatekeeper quarantine
9. **在 Linux 上实测**：AppImage 路径探测、agy CLI 优先逻辑、musl 发行版兼容
10. **交叉验证 agy CLI 端到端**：装一个真实 agy，确认 `agy-untun launch --cli` 生效

### 第 3 阶段：打磨

11. 配置导入（从 PowerShell 版迁移）
12. `agy-untun channels test` 输出更详细（各端点延迟对比）
13. 考虑提供 `--proxy-server` 给 Electron 渲染进程，消除"UI 走系统代理 / 后端走通道"的混合行为
14. 打包与分发：Homebrew cask、npm 包、Arch AUR、winget

### 第 4 阶段（可选，需前置决策）

15. 守护模式、自动 failover、通道组
16. GUI（若专家团认为必要，需重估技术栈——Tauri/Rust 与 Go 分叉）

---

## 10. 附录：可复现的验证命令

```bash
# 构建
cd D:\agy-untun
GOROOT="D:\agy-untun\.tools\go" .tools\go\bin\go.exe build -o agy-untun.exe ./cmd/agy-untun

# 单元测试
.tools\go\bin\go.exe test ./...

# 交叉编译
for t in "linux amd64" "linux arm64" "darwin amd64" "darwin arm64" "windows amd64" "windows arm64"; do
  set -- $t; ext=""; [ "$1" = "windows" ] && ext=".exe"
  GOOS=$1 GOARCH=$2 .tools\go\bin\go.exe build -o "dist/agy-untun-$1-$2$ext" ./cmd/agy-untun
done

# 端到端验证（关键）
# 1) 杀掉所有实例
powershell -Command "Get-Process Antigravity,language_server | Stop-Process -Force"
# 2) 用启动器启动
./agy-untun.exe launch --yes
# 3) 20 秒后检查后端出站
powershell -Command "
  \$ls = Get-Process language_server | Select-Object -First 1
  \$c = Get-NetTCPConnection -OwningProcess \$ls.Id | Where-Object { \$_.RemotePort -in 443,10808 }
  'to-proxy=' + (@(\$c | Where-Object RemotePort -eq 10808).Count) + ' direct=' + (@(\$c | Where-Object RemotePort -eq 443).Count)
"
# 期望：to-proxy=8 direct=0
```

---

## 11. 引用来源

| 结论 | 来源 |
|---|---|
| Go 后端只读环境变量 | `resources/app.asar` → `dist/languageServer.js`（`const env = { ...process.env, ...loginShellEnv }`）；`language_server.exe` 符号检索 |
| win32 不读登录 shell | `dist/loginShellEnv.js`（`if (process.platform === 'win32') return Promise.resolve({})`） |
| Electron 单实例锁 | `dist/main.js`（`requestSingleInstanceLock`） |
| Antigravity 版本 2.32.1 | `resources/app.asar` → `package.json` |
| Electron 版本 44.9.1 | `resources/version` 文件 |
| Node 24.22.0 内嵌 | `ELECTRON_RUN_AS_NODE=1 Antigravity.exe -e "console.log(process.versions)"` |
| asar 布局 | `app.asar` 二进制头解析（offset 16 起为 JSON 头） |
| agy 安装路径 | `https://antigravity.google/cli/install.sh` / `install.ps1`（官方） |
| agy 平台与版本 | `https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/{platform}.json` |
| 桌面版平台矩阵 | `https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-*.yml` |
| socks5 支持 | `language_server.exe` 符号 + 实测出站连接 `127.0.0.1:10808` |
| Go 交叉编译数据 | 本机实测（Go 1.25.1 / Bun 1.3.14 / Rust 1.95） |
