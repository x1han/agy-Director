# agy-untun

让 Google Antigravity（桌面版与 `agy` CLI）**不走 TUN**也能出网：启动前探测可用代理通道，注入进程级代理环境变量后再启动。

## 为什么需要它

Antigravity 的网络请求由 Go 后端 `language_server`（桌面版 `resources/bin/language_server.exe`，CLI 内嵌同源）发出。该后端**只从环境变量**解析代理，不读 Windows 注册表的系统代理，也不走 Electron 的 `--proxy-server`。因此不开 TUN 时，Go 后端会直连并被中断（`unexpected EOF`），而 UI 层看起来一切正常。

注入 `HTTP_PROXY` / `HTTPS_PROXY` 即可修复，不需要 TUN。

## 启动桌面版时追加的命令行参数

启动 Antigravity 桌面版时，agy-untun 会在用户自己的参数之后追加三个 Chromium 开关：

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

> 顺带说明：插件自己有个自愈机制，会扫描桌面 / 开始菜单 / 任务栏里的 Antigravity 快捷方式并补上这两个参数。所以**从开始菜单启动 Antigravity 时插件一直能用** —— 那不是 Antigravity 自带参数，是快捷方式被插件改写过。但插件修不了直接 `exec` 启动的进程（快捷方式不参与那条路），所以用 agy-untun 启动时必须自己带。

顺带地，agy-untun 自己的"桌面版是否在运行"判断也依赖 `DevToolsActivePort` + CDP 端点可达；开了 CDP 之后这个判断才正确（否则永远判定为"没在运行"，单实例守卫会失效，Electron 把 argv 转发给旧实例后退出，代理环境变量就丢了）。

**副作用**：本机多开了一个只监听 `127.0.0.1` 的调试端口。这是 Electron 插件生态的通行做法，但确实多了一个调试面 —— 不需要的话用 `agy-untun launch -- <参数>` 之外的方式启动 Antigravity 即可（从开始菜单启动时快捷方式已带同样的参数）。

用户自己传了 `--remote-debugging-port` 或 `--remote-debugging-address` 时，agy-untun **不会重复追加**（Chromium 取最后一个同名开关，重复追加会覆盖用户指定的端口）。

## 安装

从 [Releases](https://github.com/x1han/agy-untun/releases) 下载对应平台的二进制，或用 Go 自行构建（见下文）。

把二进制放进 `PATH` 里的任意目录即可。

## 使用

```bash
agy-untun            # 等价于 agy-untun launch：探测通道并启动 Antigravity
agy-untun doctor     # 诊断：通道、代理环境变量、运行中的实例、登录 shell 冲突
agy-untun channels   # 管理代理通道
agy-untun wrap <cmd> # 在代理环境变量下运行任意命令
```

`launch` 的选项：

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

## 构建

```bash
go build -o agy-untun ./cmd/agy-untun
```

交叉编译：

```bash
GOOS=linux   GOARCH=amd64 go build -o dist/agy-untun-linux-amd64   ./cmd/agy-untun
GOOS=linux   GOARCH=arm64 go build -o dist/agy-untun-linux-arm64   ./cmd/agy-untun
GOOS=darwin  GOARCH=amd64 go build -o dist/agy-untun-darwin-amd64  ./cmd/agy-untun
GOOS=darwin  GOARCH=arm64 go build -o dist/agy-untun-darwin-arm64  ./cmd/agy-untun
GOOS=windows GOARCH=amd64 go build -o dist/agy-untun.exe           ./cmd/agy-untun
GOOS=windows GOARCH=arm64 go build -o dist/agy-untun-arm64.exe     ./cmd/agy-untun
```

## 图标

图标资产统一放在 `assets/`：

| 文件 | 说明 |
|---|---|
| `assets/agy-untun-logo.svg` | Antigravity 单色标志（`#202124`），来源：Google 官方 |
| `assets/agy-untun-tile.svg` | 上者 + 白底圆角矩形（启动器实际使用） |
| `assets/agy-old-man.svg` | 早期 Antigravity 品牌（机器人脸），备用 |
| `assets/agy-untun.ico` | 由 `agy-untun-tile.svg` 生成的多尺寸 Windows ICO |

重新生成 ICO：

```bash
python tools/icon/build.py assets/agy-untun-tile.svg assets/agy-untun.ico
```

构建脚本只用标准库 + 无头 Chrome，无需额外 Python 依赖。

## 文档

`docs/` 下是开发过程中的架构评审与专家问答记录，解释了各个设计决定的依据（含关键代码原文引用）。

## 许可

MIT，见 [LICENSE](LICENSE)。
