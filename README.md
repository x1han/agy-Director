# agy-Director

让 Google Antigravity（桌面版与 `agy` CLI）**不走 TUN**也能出网：启动前探测可用代理通道，注入进程级代理环境变量后再启动。

界面是**终端里的 TUI**（不是原生 GUI）：Windows / macOS 双击带图标的程序会自动弹出终端窗口，窗口里渲染界面；Linux 直接在当前终端跑。

```
  ╭──────────────╮
  │ agy-Director │
  ╰──────────────╯

  Antigravity
  ▸   桌面版  C:\Users\…\Programs\antigravity\Antigravity.exe
         版本 2.33.0

  代理通道
  ▸   v2rayN  http://127.0.0.1:10808

  是否启动？    ▶ 是   否
    ↑↓ 切换行   ←→ 换选项   ⏎ 确认   y/n 直选   q 退出
```

## 为什么需要它

Antigravity 的网络请求由 Go 后端 `language_server`（桌面版 `resources/bin/language_server.exe`，CLI 内嵌同源）发出。该后端**只从环境变量**解析代理，不读 Windows 注册表的系统代理，也不走 Electron 的 `--proxy-server`。因此不开 TUN 时，Go 后端会直连并被中断（`unexpected EOF`），而 UI 层看起来一切正常。

注入 `HTTP_PROXY` / `HTTPS_PROXY` 即可修复，不需要 TUN。

## 使用

```bash
agy-Director            # TUI：扫描 → 确认 → 启动（双击图标也是这个）
agy-Director launch     # 命令行方式：探测通道并直接启动
agy-Director doctor     # 诊断：通道、代理环境变量、运行中的实例、登录 shell 冲突
agy-Director channels   # 管理代理通道
agy-Director wrap <cmd> # 在代理环境变量下运行任意命令
```

### TUI 里能做什么

| 按键 | 作用 |
|---|---|
| `↑` `↓` | 在「Antigravity / 代理通道 / 是否启动」三行之间移动 |
| `←` `→` | 在当前行上切换候选项（多个目标或多个通道时） |
| `⏎` | 确认当前选择（**默认就是「是」，回车即启动**） |
| `y` / `n` | 直接选「是」/「否」 |
| `q` / `esc` / `ctrl+c` | 退出（等同选「否」） |

扫描缺东西时不会问「是否启动」，而是直接告诉你**缺哪一个**：

```
  ╭─────────────────────────────╮
  │ 无法启动                     │
  ╰─────────────────────────────╯
  ✗ 未找到 Antigravity 桌面版
  ✗ 未找到可用的代理通道
```

### launch 的选项

| 选项 | 说明 |
|---|---|
| `--ide` | 强制启动桌面版 Antigravity |
| `--cli` | 强制启动 `agy` CLI |
| `--channel <名>` | 指定通道（默认用配置里的首选，或自动探测） |
| `--proxy <URL>` | 直接指定代理 URL，如 `http://127.0.0.1:10808` |
| `--scan` | 忽略配置，扫描本机寻找可用通道 |
| `--yes` | 不交互，全部用自动选择 |
| `-- <参数>` | 之后的参数原样传给被启动的程序 |

**它不改任何系统设置**：不写注册表代理、不改全局环境变量、不碰你的 shell 配置文件（`.bashrc` / `.zshrc` / `.profile`）。所有代理配置只以**进程环境变量**的形式注入到被启动的那个进程及其子进程。

唯一会改的东西是**你自己传的参数**（`--` 之后）—— 那是你自己的决定。

## 启动桌面版时追加的命令行参数

启动 Antigravity 桌面版时，agy-Director 会在用户自己的参数之后追加三个 Chromium 开关：

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

## 日志

整个运行过程都会写日志：

- **stderr** —— 实时输出。TUI 渲染期间会临时静音（Windows 控制台里 stdout/stderr 同屏，日志插进来会把界面搅乱），TUI 退出后恢复。
- **文件** —— 始终写入 `os.UserConfigDir()/agy-Director/logs/agy-director.log`。

| 平台 | 配置与日志目录 |
|---|---|
| Windows | `%APPDATA%\agy-Director\` |
| macOS / Linux | `$XDG_CONFIG_HOME/agy-Director/`（没设就是 `~/.config/agy-Director/`） |

> 从 Finder 双击 `.app` 时，macOS 会把 stdout/stderr 接到 `/dev/null` —— stderr 里的日志会**全部丢失**。所以文件日志是硬需求，不是可选项。

## 安装

从 [Releases](https://github.com/x1han/agy-Director/releases) 下载对应平台的产物，或用 Go 自行构建（见下文）。

| 平台 | 产物 | 说明 |
|---|---|---|
| Windows | `agy-Director-windows-amd64.exe` | 带图标，双击弹终端 |
| macOS | `agy-Director-darwin-arm64.zip` | 解压得到 `agy-Director.app`，双击弹终端 |
| Linux | `agy-Director-linux-amd64.tar.gz` | 直接运行 |

## 构建

```bash
go build -o agy-Director ./cmd/agy-director
```

交叉编译：

```bash
GOOS=linux   GOARCH=amd64 go build -o dist/agy-Director-linux-amd64   ./cmd/agy-director
GOOS=linux   GOARCH=arm64 go build -o dist/agy-Director-linux-arm64   ./cmd/agy-director
GOOS=darwin  GOARCH=amd64 go build -o dist/agy-Director-darwin-amd64  ./cmd/agy-director
GOOS=darwin  GOARCH=arm64 go build -o dist/agy-Director-darwin-arm64  ./cmd/agy-director
GOOS=windows GOARCH=amd64 go build -o dist/agy-Director-windows-amd64.exe ./cmd/agy-director
GOOS=windows GOARCH=arm64 go build -o dist/agy-Director-windows-arm64.exe ./cmd/agy-director
```

Windows 的图标资源（`cmd/agy-director/rsrc_windows_*.syso`）已经提交进仓库，交叉编译时 Go 链接器会自动带上，不需要额外步骤。

### 终端窗口是怎么弹出来的

**Windows**：二进制是 **console subsystem** 编译的（没有加 `-H windowsgui`）。Windows 对 console subsystem 的可执行文件有特殊待遇 —— 从资源管理器双击时会自动分配一个控制台（conhost / Windows Terminal），并接好 stdin/stdout/stderr。这就是"双击自动开 terminal"，不需要任何 `AllocConsole` 手工活。

程序还会把控制台代码页设成 UTF-8（否则中文是乱码），并在**退出时改回去** —— 代码页是控制台级别的设置，不还原的话用户父 shell 之后的中文输出会变乱码。

**macOS**：`.app` bundle 被 Finder 双击时，LaunchServices 把 stdout/stderr 接到 `/dev/null`，没有终端可渲染。所以程序会检测到"在 .app 里 + stdout 不是 tty"，然后用 `osascript` 让 Terminal.app 开一个窗口跑自己。

> ⚠️ macOS 首次运行会弹「终端想要控制 Terminal.app」的自动化授权框，需要点「好」。这是系统级的授权机制，无法绕过。

**Linux**：用户本来就在终端里，什么都不用做。

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
# Windows 图标资源（.syso）
bash tools/mkicon.sh

# macOS iconset 源图
python tools/icon/build.py assets/agy-Director-tile.svg assets/agy-Director.png --png --png-size 1024

# 完整 ICO
python tools/icon/build.py assets/agy-Director-tile.svg assets/agy-Director.ico
```

构建脚本只用标准库 + 无头 Chrome，无需额外 Python 依赖。

macOS 的 `.icns` 由 `tools/macos-app.sh` 在 macOS 上用 `sips` + `iconutil` 生成（这两个工具 macOS 独有）。

## 文档

`docs/` 下是开发过程中的架构评审与专家问答记录，解释了各个设计决定的依据（含关键代码原文引用）。

## 许可

MIT，见 [LICENSE](LICENSE)。
