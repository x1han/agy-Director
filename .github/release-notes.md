## agy-Director v1.2.0

让 Antigravity 不开 TUN 也能出网 —— 现在是**带图标的终端界面**。

### 下载

| 平台 | 产物 |
|---|---|
| Windows x64 | `agy-Director-windows-x64.exe`（或 `.zip`） |
| Windows ARM64 | `agy-Director-windows-arm64.exe`（或 `.zip`） |
| macOS Intel | `agy-Director-macos-x64.zip`（或 `.app.tar.gz`） |
| macOS Apple Silicon | `agy-Director-macos-arm64.zip`（或 `.app.tar.gz`） |
| Linux x64 | `agy-Director-linux-x64.tar.gz` |
| Linux ARM64 | `agy-Director-linux-arm64.tar.gz` |

**怎么挑架构**：

- **x64** = x86-64，**Intel 和 AMD 的 CPU 都用这个**（绝大多数 PC）
- **arm64** = ARM64，Apple Silicon（M 系列）、高通 Snapdragon X（Windows on ARM）、ARM 服务器

Linux 产物是静态链接的，任何 glibc/musl 发行版都能跑。

Linux 的 `.tar.gz` 解包后是一个 `agy-Director-linux-<arch>/` 目录，
里面是 `agy-Director` 可执行文件加 README/LICENSE。

校验和见 `SHA256SUMS.txt`。

### 用法

Windows 和 macOS：**双击**就行 —— 程序会自动弹出一个终端窗口，里面是 TUI 界面：

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

界面会告诉你扫到的 Antigravity 路径和可用代理通道；缺哪个就明说缺哪个，
不会闷头启动。**光标默认停在「是否启动」上，回车就是启动。**

Linux：直接 `agy-Director`。

命令行方式（不想要界面时）：

```bash
agy-Director launch     # 探测通道并直接启动
agy-Director doctor     # 诊断
agy-Director channels   # 管理通道
```

### 这次改了什么

- 项目从 `agy-untun` 改名为 `agy-Director`
- 新增 TUI 启动器，双击图标弹终端跑界面
- 日志同时写 stderr 和文件（TUI 期间静音 stderr，避免和界面抢屏）
- macOS 出 `.app` bundle（带图标，ad-hoc 签名）
- Windows exe 带图标
- 产物命名对齐 opencode：`windows/macos/linux` × `x64/arm64`，
  x64 同时覆盖 Intel 和 AMD
- 图标放大到 90%，白底黑标在小尺寸下更清楚
