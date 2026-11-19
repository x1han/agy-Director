<div align="center">
  <img src="assets/agy-Director-tile.svg" width="128" alt="agy-Director">
</div>

# agy-Director

让 Google Antigravity（桌面版与 `agy` CLI）**不开 TUN** 也能出网。

## 1. 介绍

### 是什么

一个带图标的启动器。**双击它**，终端里会弹出一个 TUI 界面，扫出你机器上的 Antigravity 和可用的代理通道，问你一句「是否启动」，回车即启动。

界面长这样：

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

扫不到东西时它不会闷头启动，而是红字告诉你**缺哪一个**：

```
  ╭─────────────────────────────╮
  │ 无法启动                     │
  ╰─────────────────────────────╯
  ✗ 未找到 Antigravity 桌面版
  ✗ 未找到可用的代理通道
```

### 为什么需要它

Antigravity 的网络请求是由 Go 后端 `language_server` 发出的（桌面版 `resources/bin/language_server.exe`，CLI 内嵌同源）。这个后端**只从环境变量**读代理 —— 它不读 Windows 注册表的系统代理，也不走 Electron 的 `--proxy-server`。

所以不开 TUN 时，Go 后端会直连然后被掐断（`unexpected EOF`），而界面看起来一切正常 —— 这种「UI 正常但请求全挂」的情况很难查。

agy-Director 的做法是：启动前探测出可用的本地代理通道，以**进程环境变量**的形式注入给 Antigravity。不需要 TUN，不需要 VPN 模式，也不需要动系统设置。

**它不改你的系统**：不写注册表代理、不改全局环境变量、不碰 `.bashrc` / `.zshrc` / `.profile`、不改桌面快捷方式。所有代理配置只作用在被启动的那个进程及其子进程上。

> 更细的原理、构建方式、日志位置等在 [`docs/agy-Director-technical-details.md`](docs/agy-Director-technical-details.md)。

---

## 2. 怎么安装

从 [Releases](https://github.com/x1han/agy-Director/releases) 下载对应平台的产物。

### 怎么挑架构

| 架构名 | 是什么 | 谁用 |
|---|---|---|
| **x64** | x86-64 | **Intel 和 AMD 的 CPU 都用这个**（绝大多数 PC） |
| **arm64** | ARM64 | Apple Silicon（M 系列）、高通 Snapdragon X（Windows on ARM）、ARM 服务器 |

> `x64` 指的是**指令集**，不是某个 CPU 厂商。两家都出 x86-64 的 CPU，跑的是同一个二进制 —— 所以「Intel 版」和「AMD 版」其实是同一个文件。

### 下载哪个文件

| 平台 | 文件 | 说明 |
|---|---|---|
| Windows x64 | `agy-Director-windows-x64.exe` | 直接双击 |
| Windows x64 | `agy-Director-windows-x64.zip` | zip 版，给不让直接跑 exe 的环境 |
| Windows ARM64 | `agy-Director-windows-arm64.exe` | 直接双击 |
| Windows ARM64 | `agy-Director-windows-arm64.zip` | zip 版 |
| macOS Intel | `agy-Director-macos-x64.zip` | 解压得到 `agy-Director.app`，双击 |
| macOS Intel | `agy-Director-macos-x64.app.tar.gz` | 同上（tar.gz 版） |
| macOS Apple Silicon | `agy-Director-macos-arm64.zip` | 同上 |
| macOS Apple Silicon | `agy-Director-macos-arm64.app.tar.gz` | 同上 |
| Linux x64 | `agy-Director-linux-x64.tar.gz` | 静态链接，直接运行 |
| Linux ARM64 | `agy-Director-linux-arm64.tar.gz` | 同上 |

校验和见 Release 里的 `SHA256SUMS.txt`。

### 各平台怎么装

**Windows** —— 下载 `.exe`，双击。程序会自动弹出一个终端窗口，界面就在里面。放到任何目录都行，不用装，不用配环境变量。

**macOS** —— 下载 `.zip` 或 `.app.tar.gz`，解压得到 `agy-Director.app`，**双击**即可。

> ⚠️ 首次运行会弹「终端想要控制 Terminal.app」的自动化授权框，需要点「好」。这是 macOS 的系统级授权机制，无法绕过。
>
> 未做开发者签名的 `.app` 首次打开可能被 Gatekeeper 拦，右键点「打开」即可。

**Linux** —— 下载 `.tar.gz` 后：

```bash
tar xzf agy-Director-linux-x64.tar.gz
./agy-Director-linux-x64/agy-Director
```

想让它进 `PATH`：

```bash
sudo mv agy-Director-linux-x64/agy-Director /usr/local/bin/
```

产物是 `CGO_ENABLED=0` 静态编译的，任何 glibc / musl 发行版都能跑。

---

## 3. 怎么用

### TUI（默认）

双击图标就是这一条。命令行里运行也可以：

```bash
agy-Director
```

界面里能做什么：

| 按键 | 作用 |
|---|---|
| `↑` `↓` | 在「Antigravity / 代理通道 / 是否启动」三行之间移动 |
| `←` `→` | 在当前行上切换候选项（多个目标或多个通道时） |
| `⏎` | 确认当前选择（**默认就是「是」，回车即启动**） |
| `y` / `n` | 直接选「是」/「否」 |
| `q` / `esc` / `ctrl+c` | 退出（等同选「否」） |

光标默认停在「是否启动」上，所以**直接回车就是启动**。

### 命令行

```bash
agy-Director            # TUI：扫描 → 确认 → 启动
agy-Director launch     # 命令行方式：探测通道并直接启动
agy-Director doctor     # 诊断：通道、代理环境变量、运行中的实例
agy-Director channels   # 管理代理通道
agy-Director wrap <cmd> # 在代理环境变量下运行任意命令
agy-Director version    # 显示版本
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

### 手动指定通道

```bash
agy-Director channels add v2rayN http://127.0.0.1:10808
agy-Director channels list
agy-Director channels test v2rayN
```

---

## 4. 报错了怎么看、怎么提 issue

### 先看日志

程序整个运行过程都在写日志，**每次运行都有**：

| 平台 | 日志文件 |
|---|---|
| Windows | `%APPDATA%\agy-Director\logs\agy-director.log` |
| macOS / Linux | `~/.config/agy-Director/logs/agy-director.log` |

日志里能看到它扫了什么、选了什么通道、启动时注入了哪些环境变量。

TUI 运行期间 stderr 是静音的（日志插进界面会把界面搅乱），但**文件日志一直在写**。

### 跑一下诊断

```bash
agy-Director doctor
```

会打印：Antigravity 装在哪、`agy` CLI 在不在、有没有实例在跑、代理环境变量是什么、有哪些本地代理端点、shell 环境有没有冲突。

### 提 issue

带上这些信息会快很多：

1. 操作系统和版本
2. `agy-Director doctor` 的完整输出
3. 日志文件里**出问题那一次**的内容
4. 你用的代理软件和端口

[Issues →](https://github.com/x1han/agy-Director/issues)

---

## 许可

MIT，见 [LICENSE](LICENSE)。
