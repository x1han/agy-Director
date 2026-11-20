#!/usr/bin/env bash
#
# 重新生成 Windows 图标资源（.syso）。
#
# 用法:
#   bash tools/mkicon.sh
#
# 前置条件:
#   - Go 工具链（用于 go install rsrc）
#   - tools/icon/source/agy-Director-logo.svg（矢量源图）
#   - 系统上有 Chrome/Chromium/Edge（build.py 用无头浏览器渲染 SVG）
#
# ── 为什么源图不在 assets/ 里 ────────────────────────────────────────
#
# assets/ 只放**要随包分发的产物**（.ico / .png）。
# 矢量源图是**构建输入**，不是产物 —— 它放在
# tools/icon/source/ 下，CI 和本地都能重新生成所有产物。
#
# "白底圆角方块 + 黑标" 的 tile 是由 logo 加一个 <rect> 拼出来的，
# 不需要单独存 —— tile.py 每次现算（同样的输入必然得到同样的输出，
# 实测字节级一致）。
#
# ── 为什么 .syso 要提交进仓库 ──────────────────────────────────────────
#
# Go 标准库没有给 exe 换图标的能力。换图标必须靠 PE 资源，
# 而 PE 资源只能在**链接期**注入 —— 也就是需要一个 .syso 文件
# 放在 main 包同目录下，让 Go 链接器自动带上。
#
# .syso 是二进制产物，理论上可以每次构建时生成，但那样：
#   - 本地构建得先装 rsrc，麻烦
#   - CI 得额外装 rsrc + 渲染 SVG（依赖 Chrome），更麻烦
#
# 所以直接提交生成好的 .syso，CI 只管 go build。
# 换图标时跑一次本脚本即可。
#
# ── 为什么必须包含 256px 帧 ──────────────────────────────────────────
#
# 之前 .syso 只嵌了 16..128，把 256 留在 assets/ 里 —— 理由是
# "256 的 32bpp DIB 有 270KB，太大"。这个取舍是错的：
#
#   资源管理器的大图标视图要 256px。找不到 256 帧时，
#   Windows 会把 128 帧**放大**。
#
# 现在 256 帧存成 PNG 压缩格式：同一张图，270KB → 6.1KB（差 40 倍），
# 画质无损。
#
# ── 为什么尺寸阶梯要这么密 ─────────────────────────────────────────
#
# 实测（每帧涂不同颜色的图标，问 Windows 自己挑哪一帧，走的是
#       PrivateExtractIconsW —— 和资源管理器同一条路）：
#
#   Windows 的规则是「选**不超过**请求尺寸的最大那一帧，再缩放」。
#   注意方向 —— 不是"选最小的够大的帧再缩小"。
#
#   请求 72px，容器里只有 16/24/32/48/64/128/256：
#     → 命中 64 帧，放大 1.13 倍。细笔画被拉成灰边。
#
#   150% 缩放的屏幕上，资源管理器 48 逻辑像素的图标要 72 物理像素，
#   于是每一个 48px 的图标都必然被放大一次。
#
# 现在这条阶梯 16/20/24/32/40/48/60/64/72/96/128/144/192/256
# 覆盖了 100/125/150/175/200% 缩放下所有常见槽位的**物理**尺寸。
# 用同一条探针实测：每一档都精确命中同尺寸的帧，一次缩放都没有。
#
# 96 及以上用 PNG 帧：实测 Windows 会正确选用这些帧。
# 72 及以下必须用 DIB —— 实测 PNG 帧在那些尺寸会被 Windows 跳过。
#
# ── ⚠️ 但阶梯密度只解决"缩放"问题 ──────────────────────────────────
#
# 用户看到"图标又胖又糊"时，真正的元凶是**图形在 tile 里画得太大**：
#
#   --scale 0.62  ->  scale(0.842264)  ->  图形占块高 61%   ← 正确
#   --scale 0.90  ->  scale(1.222642)  ->  图形占块高 88%   ← 退化
#
# 逐像素比对用户桌面截图：旧版图形占块高 61%、眼条 6px；
# --scale 0.90 的占 88%、眼条 9px。高度差 43%、眼条粗 50%。
#
# 小尺寸下两个几乎顶到边的眼条会连成一片 —— 整个标志糊成一坨。
# 所以下面的 --scale 是 0.62，**不要改大**。
# 详见 tools/icon/tile.py 的模块文档。

set -euo pipefail

cd "$(dirname "$0")/.."

LOGO="tools/icon/source/agy-Director-logo.svg"
ICO_FULL="assets/agy-Director.ico"
PNG_FULL="assets/agy-Director.png"
OUT_DIR="cmd/agy-director"

if [ ! -f "$LOGO" ]; then
    echo "找不到 $LOGO" >&2
    exit 1
fi

# tile SVG 是**中间产物**，不进仓库 —— 放到临时目录里。
#
# mktemp -d 必须带模板：BSD 版（macOS）不给模板会直接报用法错误。
tmp="$(mktemp -d "${TMPDIR:-/tmp}/agyicon.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
SVG="$tmp/tile.svg"

echo "==> 生成 tile SVG（白底 + 黑标，标志占 62%）"
python tools/icon/tile.py "$LOGO" "$SVG" --bg "#FFFFFF" --scale 0.62

echo "==> 生成完整 ICO（14 档尺寸，96+ 用 PNG 压缩）"
python tools/icon/build.py "$SVG" "$ICO_FULL"

echo "==> 生成 macOS iconset 源图（1024×1024 PNG）"
python tools/icon/build.py "$SVG" "$PNG_FULL" --png --png-size 1024

echo "==> 安装 rsrc"

# 找 Go：优先用 PATH 上的，其次用仓库里自带的 .tools/go。
if command -v go >/dev/null 2>&1; then
    GO=go
elif [ -x "$PWD/.tools/go/bin/go" ]; then
    GO="$PWD/.tools/go/bin/go"
else
    echo "找不到 go 命令" >&2
    exit 1
fi

GOBIN="$PWD/.tools/bin" "$GO" install github.com/akavel/rsrc@v0.10.2
RSRC="$PWD/.tools/bin/rsrc"
[ -x "$RSRC" ] || RSRC="$PWD/.tools/bin/rsrc.exe"

# 文件名必须带 arch 后缀：Go 链接器按 <name>_<os>_<arch>.syso 匹配，
# 交叉编译时才能各取各的（不带后缀会串架构）。
for arch in amd64 arm64; do
    echo "==> 生成 $OUT_DIR/rsrc_windows_$arch.syso"
    "$RSRC" -ico "$ICO_FULL" -arch "$arch" -o "$OUT_DIR/rsrc_windows_$arch.syso"
done

echo "==> 完成"
ls -la "$OUT_DIR"/*.syso
