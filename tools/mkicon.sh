#!/usr/bin/env bash
#
# 重新生成 Windows 图标资源（.syso）。
#
# 用法:
#   bash tools/mkicon.sh
#
# 前置条件:
#   - Go 工具链（用于 go install rsrc）
#   - assets/agy-Director-tile.svg（源图）
#   - 系统上有 Chrome/Chromium/Edge（build.py 用无头浏览器渲染 SVG）
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
# ── 为什么用小一号的 ICO ──────────────────────────────────────────────
#
# 256×256 的 32bpp DIB 有 270KB（未压缩 BGRA），
# 撑得 .syso 有 373KB。Windows 资源管理器实际用到的最大尺寸
# 通常不超过 128，所以 .syso 用 16..128 那套，体积降到 ~100KB。
# 完整的 256 版仍然保留在 assets/ 里给需要的人用。

set -euo pipefail

cd "$(dirname "$0")/.."

SVG="assets/agy-Director-tile.svg"
LOGO="assets/agy-Director-logo.svg"
ICO_FULL="assets/agy-Director.ico"
PNG_FULL="assets/agy-Director.png"
ICO_SLIM="$(mktemp -d)/agy-Director-slim.ico"
OUT_DIR="cmd/agy-director"

cleanup() { rm -rf "$(dirname "$ICO_SLIM")"; }
trap cleanup EXIT

if [ ! -f "$SVG" ]; then
    echo "找不到 $SVG" >&2
    exit 1
fi

echo "==> 生成 tile SVG（白底 + 黑标，标志占 90%）"
python tools/icon/tile.py "$LOGO" "$SVG" --bg "#FFFFFF" --scale 0.90

echo "==> 生成完整 ICO（16..256）"
python tools/icon/build.py "$SVG" "$ICO_FULL" --sizes 16,24,32,48,64,128,256

echo "==> 生成 macOS iconset 源图（1024×1024 PNG）"
python tools/icon/build.py "$SVG" "$PNG_FULL" --png --png-size 1024

echo "==> 生成精简 ICO（16..128，用于 .syso）"
python tools/icon/build.py "$SVG" "$ICO_SLIM" --sizes 16,24,32,48,64,128

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
    "$RSRC" -ico "$ICO_SLIM" -arch "$arch" -o "$OUT_DIR/rsrc_windows_$arch.syso"
done

echo "==> 完成"
ls -la "$OUT_DIR"/*.syso
