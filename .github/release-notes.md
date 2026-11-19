## agy-Director v1.4.0

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

- **摘要页最多显示 3 秒就自动关闭**，期间按回车可以立刻关掉。
  之前是无限等回车 —— 双击图标只想看一眼结果，却被卡在那儿。
  - `--wait=N` 改秒数，`--wait` 一直等，`--no-wait` 立刻退。
- **图标不再糊** —— 之前 exe 里只嵌了 16..128 的图标帧，
  资源管理器的大图标视图要 256px，找不到时 Windows 会把 128 帧**放大**。
  现在补上了真正的 256 帧（PNG 压缩，6KB 而非 270KB），
  大图标视图下与原图的误差从 4.39 降到 1.20。
- **图标改用面积平均降采样** —— 之前用双线性（只取 2×2 邻域），
  从大图缩到 16px 等于只看了源图 1/1024 的信息，细线条直接被抹掉。
  换成面积平均（预乘 alpha）并让每个尺寸单独渲染后，
  以 1024px 光栅化为真值：16px 误差 22.5 → 3.1，32px 13.2 → 3.9。
- README 重写为四个板块：介绍 / 怎么安装 / 怎么用 / 报错怎么查看·提 issue，
  细节（CDP 参数、终端窗口机制、构建方式）搬到 `docs/`。
- 项目从 `agy-untun` 改名为 `agy-Director`
- 新增 TUI 启动器，双击图标弹终端跑界面
- 日志同时写 stderr 和文件（TUI 期间静音 stderr，避免和界面抢屏）
- macOS 出 `.app` bundle（带图标，ad-hoc 签名）
- 产物命名对齐 opencode：`windows/macos/linux` × `x64/arm64`，
  x64 同时覆盖 Intel 和 AMD
