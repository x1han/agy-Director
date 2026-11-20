## agy-Director v1.5.1

修图标。v1.5.0 那版的图标**画胖了**，这一版改回来。

### 下载

| 平台 | 产物 |
|---|---|
| Windows x64 | `agy-Director-windows-x64.exe`（或 `.zip`） |
| Windows ARM64 | `agy-Director-windows-arm64.exe`（或 `.zip`） |
| macOS Intel | `agy-Director-macos-x64.zip`（或 `.app.tar.gz`） |
| macOS Apple Silicon | `agy-Director-macos-arm64.zip`（或 `.app.tar.gz`） |
| Linux x64 | `agy-Director-linux-x64.tar.gz` |
| Linux ARM64 | `agy-Director-linux-arm64.tar.gz` |

### 这次改了什么

- **图标不再画胖**。v1.5.0 的图标在桌面上明显比旧版"胖"一圈，
  逐像素比对后确认：**不是被放大糊的，是图形本身画得太大**。
  - 桌面截图上两个图标（都是 72 物理像素的白底块）逐像素对比：
    旧版图形占块高 **61%**、眼条 6px；v1.5.0 占 **88%**、眼条 9px。
    高度差 43%、眼条粗 50% —— 观感差异的全部来源。
  - 元凶是 `tile.py` 的 `--scale`：v1.4.0 曾从 0.60 提到 0.90，
    理由是"标志放大更清楚"。方向反了：白底方块里塞一个几乎顶到边的
    大黑图形，**小尺寸下两根眼条会连成一片**，整个标志糊成一坨。
  - 改回 **0.62**（图形占块高 61%）。用 Windows 自己的图标 API 渲染
    新 exe 的 72px，跟桌面截图里"好看的那张"逐像素算 IoU：**0.9503**
    （对着"胖的那张"只有 0.2812）。
- **光学加粗的公式修对了**。旧公式漏了 tile 的 transform 缩放系数，
  小尺寸的笔画因此粗了 1.2~1.6 倍 —— 眼条之间被填死。
  现在从 SVG 里**解析出**实际缩放系数再算，改 `--scale` 不会静默算错。
- 顺带把基线笔画宽从 10 改成实测的 10.5 单位（拱形的腿才是最细处，
  眼睛是 20 单位的圆头矩形）。

细节见 `docs/agy-Director-technical-details.md` 的「图标」一节。

---

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
- **图标不再糊 —— 尺寸阶梯补密了**（但见 v1.5.1：这还不是全部原因）。
  实测 Windows 选帧的规则是「选**不超过**请求尺寸的最大那一帧，
  再缩放过去」—— 方向和直觉相反。150% 缩放的屏幕上，
  资源管理器 48 逻辑像素的槽位要 72 物理像素，
  而旧阶梯只有 16/24/32/48/64/128/256 → 只能命中 64 帧并放大 1.13 倍。
  - 阶梯改成 16/20/24/32/40/48/60/64/72/96/128/144/192/256，
    150% 下精确命中率从 2/9 提到 9/9
  - 96 及以上用 PNG 帧（实测 Windows 会正确选用），72 及以下必须用 DIB
  - 小尺寸配**光学加粗**的独立图形（天线笔画在 16px 下只有 0.89px，
    再好的重采样算法也救不回来）
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
