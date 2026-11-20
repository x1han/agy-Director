## agy-Director v1.5.0

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

- **摘要页最多显示 5 秒就自动关闭，等待时有实时倒计时（5 → 1）**，
  期间按回车可以立刻关掉。
  之前是无限等回车 —— 双击图标只想看一眼结果，却被卡在那儿。
  - `--wait=N` 改秒数，`--wait` 一直等，`--no-wait` 立刻退。
- **图标不再糊 —— 真正的原因找到了**。不是矢量图的问题（SVG 本来就
  不能进 exe），是**尺寸阶梯太稀疏**。
  实测 Windows 选帧的规则是「选**不超过**请求尺寸的最大那一帧，
  再缩放过去」—— 方向和直觉相反。150% 缩放的屏幕上，
  资源管理器 48 逻辑像素的槽位要 72 物理像素，
  而旧阶梯只有 16/24/32/48/64/128/256 → 只能命中 64 帧并放大 1.13 倍。
  逐像素比对用户那张截图，确认它就是我们的 64 帧放大到 72（IoU 0.966）。
  - 阶梯改成 16/20/24/32/40/48/60/64/72/96/128/144/192/256，
    150% 下精确命中率从 2/9 提到 9/9
  - 96 及以上用 PNG 帧（实测 Windows 会正确选用），72 及以下必须用 DIB
  - 小尺寸配**光学加粗**的独立图形（天线笔画在 16px 下只有 0.89px，
    再好的重采样算法也救不回来）
  - 实测 48@150%：实心黑像素 71.1% → 81.4%，灰边 20.0% → 14.7%
  - `.syso` 反而更小了（99KB vs 108KB）
- **仓库里不再随包分发 SVG 源图** —— `assets/` 只留真正要分发的产物
  （`.ico` / `.png`），矢量母版挪到 `tools/icon/source/`，
  由构建脚本现算中间产物。README 的 logo 改用 PNG。
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
