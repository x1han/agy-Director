# agy-Director 技术细节

README 只讲"是什么、怎么装、怎么用、出错了怎么办"。这里是那些**用不到但值得知道**的细节。

---

## 启动桌面版时追加的命令行参数

启动 Antigravity 桌面版时，agy-Director 会在用户自己的参数之后追加这些 Chromium 开关：

| 参数 | 作用 |
|---|---|
| `--proxy-server=<通道 URL>` | 让 Electron 壳自己的网络栈（渲染进程、扩展、DevTools 下载）也走代理 |
| `--proxy-bypass-list=<转换后的 NO_PROXY>` | 与 `NO_PROXY` 对应，避免把本地开发端口也代理掉 |
| `--remote-debugging-port=0` | 开启本地 CDP 调试端口（Chromium 随机挑端口） |
| `--remote-debugging-address=127.0.0.1` | 把 CDP 端口绑在回环上，外部访问不到 |

### 为什么需要 CDP 调试参数

Antigravity 2.33+ 的 `dist/main.js`（`app.asar` 内）里只有：

```js
const activePortFile = getActivePortFilePath();
if (!electron_1.app.commandLine.hasSwitch('remote-debugging-port')) {
    removeStaleActivePortFile(activePortFile);
}
```

它**只判断有没有这个开关，从不自己打开** —— 整个 `app.asar` 里 `appendSwitch('remote-debugging-port'` 零命中（`appendSwitch` 共 8 处，都是 headless / disable-features 之类）。

不传的话：

- CDP 端口不开
- `%APPDATA%\antigravity\DevToolsActivePort` 不生成
- 依赖 CDP 的插件（内联状态条等）连不上，报 `Host started without --remote-debugging-port=0`

> 顺带说明：插件自己有个自愈机制，会扫描桌面 / 开始菜单 / 任务栏里的 Antigravity 快捷方式并补上这两个参数。所以**从开始菜单启动 Antigravity 时插件一直能用** —— 那不是 Antigravity 自带参数，是快捷方式被插件改写过。但插件修不了直接 `exec` 启动的进程（快捷方式不参与那条路），所以用 agy-Director 启动时必须自己带。

顺带地，agy-Director 自己的"桌面版是否在运行"判断也依赖 `DevToolsActivePort` + CDP 端点可达；开了 CDP 之后这个判断才正确（否则永远判定为"没在运行"，单实例守卫会失效，Electron 把 argv 转发给旧实例后退出，代理环境变量就丢了）。

**副作用**：本机多开了一个只监听 `127.0.0.1` 的调试端口。这是 Electron 插件生态的通行做法，但确实多了一个调试面 —— 不需要的话用 `agy-Director launch -- <参数>` 之外的方式启动 Antigravity 即可（从开始菜单启动时快捷方式已带同样的参数）。

用户自己传了 `--remote-debugging-port` 或 `--remote-debugging-address` 时，agy-Director **不会重复追加**（Chromium 取最后一个同名开关，重复追加会覆盖用户指定的端口）。

---

## 终端窗口是怎么弹出来的

**Windows**：二进制是 **console subsystem** 编译的（没有加 `-H windowsgui`）。Windows 对 console subsystem 的可执行文件有特殊待遇 —— 从资源管理器双击时会自动分配一个控制台（conhost / Windows Terminal），并接好 stdin/stdout/stderr。这就是"双击自动开 terminal"，不需要任何 `AllocConsole` 手工活。

程序还会把控制台代码页设成 UTF-8（否则中文是乱码），并在**退出时改回去** —— 代码页是控制台级别的设置，不还原的话用户父 shell 之后的中文输出会变乱码。

**macOS**：`.app` bundle 被 Finder 双击时，LaunchServices 把 stdout/stderr 接到 `/dev/null`，没有终端可渲染。所以程序会检测到"在 .app 里 + stdout 不是 tty"，然后用 `osascript` 让 Terminal.app 开一个窗口跑自己。

> ⚠️ macOS 首次运行会弹「终端想要控制 Terminal.app」的自动化授权框，需要点「好」。这是系统级的授权机制，无法绕过。

**Linux**：用户本来就在终端里，什么都不用做。

---

## 日志

TUI 运行时（`cmd_tui.go` 里调 `logging.Setup`）会写日志：

- **stderr** —— 实时输出。TUI 渲染期间会临时静音（Windows 控制台里 stdout/stderr 同屏，日志插进来会把界面搅乱），TUI 退出后恢复。
- **文件** —— 始终写入 `<用户配置目录>/agy-Director/logs/agy-director.log`。

日志目录用 `os.UserConfigDir()`（`internal/logging/logging.go:89`），各平台实际路径：

| 平台 | 日志文件 |
|---|---|
| Windows | `%APPDATA%\agy-Director\logs\agy-director.log` |
| macOS | `~/Library/Application Support/agy-Director/logs\agy-director.log` |
| Linux | `$XDG_CONFIG_HOME/agy-Director/logs/agy-director.log`（没设就是 `~/.config/agy-Director/logs/`） |

> ⚠️ 注意 macOS 和其它平台不一样：`os.UserConfigDir()` 在 darwin 上返回
> `$HOME/Library/Application Support`（不是 `~/.config`）。
> 但**通道配置文件**（`channels.json`）走的是 `internal/config/config.go`，
> 那里用的是 `XDG_CONFIG_HOME` / `~/.config` —— 所以 macOS 上
> 配置在 `~/.config/agy-Director/`，日志在
> `~/Library/Application Support/agy-Director/logs/`，两个地方不一样。

> 从 Finder 双击 `.app` 时，macOS 会把 stdout/stderr 接到 `/dev/null` —— stderr 里的日志会**全部丢失**。所以文件日志是硬需求，不是可选项。

`doctor` / `channels` 等非 TUI 命令**不写文件日志**（它们不调 `logging.Setup`），
只在 stderr 输出 —— 本来就是排查用的，直接看终端即可。

---

## 构建

```bash
go build -o agy-Director ./cmd/agy-director
```

交叉编译：

```bash
GOOS=linux   GOARCH=amd64 go build -o dist/agy-Director-linux-x64      ./cmd/agy-director
GOOS=linux   GOARCH=arm64 go build -o dist/agy-Director-linux-arm64    ./cmd/agy-director
GOOS=darwin  GOARCH=amd64 go build -o dist/agy-Director-macos-x64      ./cmd/agy-director
GOOS=darwin  GOARCH=arm64 go build -o dist/agy-Director-macos-arm64    ./cmd/agy-director
GOOS=windows GOARCH=amd64 go build -o dist/agy-Director-windows-x64.exe   ./cmd/agy-director
GOOS=windows GOARCH=arm64 go build -o dist/agy-Director-windows-arm64.exe ./cmd/agy-director
```

（Go 的 `GOARCH=amd64` 对应的就是产物名里的 `x64`。）

Windows 的图标资源（`cmd/agy-director/rsrc_windows_*.syso`）已经提交进仓库，交叉编译时 Go 链接器会自动带上，不需要额外步骤。

---

## 图标

图标资产统一放在 `assets/`：

| 文件 | 说明 |
|---|---|
| `assets/agy-Director-logo.svg` | Antigravity 单色标志（`#202124`），来源：Google 官方 |
| `assets/agy-Director-tile.svg` | 上者 + 白底圆角矩形（启动器实际使用） |
| `assets/agy-old-man.svg` | 早期 Antigravity 品牌（机器人脸），备用 |
| `assets/agy-Director.png` | 1024×1024 PNG（macOS iconset 的源图） |
| `assets/agy-Director.ico` | 由 `agy-Director-tile.svg` 生成的多尺寸 Windows ICO |

重新生成：

```bash
# 一步到位：tile SVG → ICO → PNG → .syso
bash tools/mkicon.sh
```

构建脚本只用标准库 + 无头 Chrome，无需额外 Python 依赖。

macOS 的 `.icns` 由 `tools/macos-app.sh` 在 macOS 上用 `sips` + `iconutil` 生成（这两个工具 macOS 独有）。

### 图标的两个坑

**1. 必须有 256px 帧。** 资源管理器的大图标视图要 256px。找不到 256 帧时 Windows 会把 128 帧**放大** —— 这就是"图标看起来糊"的真正原因。

256 帧存成 PNG 压缩格式后只有 6.1KB（存成未压缩 DIB 是 270KB，差 40 倍）。Windows 自己的 `explorer.exe` / `cmd.exe` / `notepad.exe` 用的就是这个混合格式：小尺寸 DIB + 256 尺寸 PNG。

**2. 降采样要用面积平均，不能用双线性。** 双线性只取 2×2 邻域，从大图缩到 16px 等于只看了源图 1/1024 的信息，细线条直接被抹掉。实测误差（以 1024px 光栅化为真值）：

| 尺寸 | 双线性 | 面积平均 |
|---|---|---|
| 16 | 22.5 | **3.1** |
| 32 | 13.2 | **3.9** |
| 64 | 7.5 | **3.9** |
| 128 | 3.9 | **2.4** |

16px 是任务栏和资源管理器列表的常用尺寸，22.5 的误差意味着"明显糊"。

另外每个尺寸要**单独渲染**（渲染成 `size × 4` 再降采样），而不是渲染一张大图再缩 —— 小尺寸需要的是源图在该尺寸下的准确光栅化。

---

## 代理通道的扫描方式

`internal/scan` 按三个来源找通道，**只探测回环地址**，不做 65535 全端口扫描：

1. 系统代理设置（Windows 注册表 / macOS 系统偏好 / Linux 桌面环境）
2. 本机监听表里的端口
3. 一份常见端口白名单

找到候选后并发探测（同时最多 16 个，每个 1200ms 超时），判断它到底是不是 HTTP 代理。

配置在 `channels.json` 里，首选通道排在最前。

---

## 不碰你的系统

agy-Director **不改任何系统设置**：

- 不写注册表代理
- 不改全局环境变量
- 不碰 shell 配置文件（`.bashrc` / `.zshrc` / `.profile`）
- 不改桌面 / 开始菜单快捷方式

所有代理配置只以**进程环境变量**的形式注入到被启动的那个进程及其子进程。

检测到 shell 环境变量冲突时，只在终端红字报错并打印出冲突的变量名，**让用户自己去改**。

唯一会改的东西是**你自己传的参数**（`--` 之后）—— 那是你自己的决定。

---

## 其它文档

`docs/` 下还有开发过程中的架构评审与专家问答记录，解释了各个设计决定的依据（含关键代码原文引用）。
