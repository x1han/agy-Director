#!/usr/bin/env bash
#
# 构造 macOS 的 .app bundle。
#
# 用法（在 macOS 上跑）:
#   bash tools/macos-app.sh <二进制路径> <输出目录> [版本号]
#
# 例:
#   bash tools/macos-app.sh dist/agy-Director-darwin-arm64 dist 1.1.0
#
# 产物: dist/agy-Director.app
#
# ── bundle 结构 ───────────────────────────────────────────────────────
#
#   agy-Director.app/
#   └── Contents/
#       ├── Info.plist        应用元信息（图标名、可执行文件名、版本）
#       ├── MacOS/
#       │   └── agy-Director  主程序
#       └── Resources/
#           └── agy-Director.icns
#
# CFBundleExecutable **不带扩展名** —— 即使 bundle 被改名，
# macOS 也能定位到可执行文件。
#
# ── 为什么不设 LSUIElement ───────────────────────────────────────────
#
# LSUIElement=true 会让 app 以"agent app"身份运行：
# **Dock 里没有图标，强制退出窗口里也没有**。
# 我们的场景是"双击图标 → 弹出一个 Terminal 窗口"，
# 设了它用户会以为双击没反应（终端虽然弹了，但 Dock 里看不到）。
#
# ── 为什么必须 codesign ──────────────────────────────────────────────
#
# Apple Silicon 上未签名的 .app 会被 Gatekeeper 直接拒绝：
# "无法打开，因为无法验证开发者"。
# ad-hoc 签名（-sign -，不带开发者证书）足以过这一关。
# 正式分发仍需 Developer ID 签名 + 公证（notarization）。

set -euo pipefail

BIN="${1:?用法: macos-app.sh <二进制路径> <输出目录> [版本号]}"
OUTDIR="${2:?用法: macos-app.sh <二进制路径> <输出目录> [版本号]}"
VERSION="${3:-1.0.0}"

# ── 版本号必须去掉 "v" 前缀 ───────────────────────────────────────────
#
# CI 传进来的是 git tag 名（github.ref_name），形如 "v1.2.0"。
# 但 CFBundleShortVersionString 要的是**纯数字点分版本** "1.2.0" ——
# 带 "v" 的值在 macOS 上会被当成非法版本，
# Finder 的"显示简介"里显示不出来，Spotlight 也搜不到版本号。
VERSION="${VERSION#v}"

APP_NAME="agy-Director"
BUNDLE="$OUTDIR/$APP_NAME.app"
ICNS_SRC="assets/$APP_NAME.png"
LOGO="tools/icon/source/agy-Director-logo.svg"

if [ ! -f "$BIN" ]; then
    echo "找不到二进制: $BIN" >&2
    exit 1
fi
if [ ! -f "$ICNS_SRC" ]; then
    echo "找不到图标源图: $ICNS_SRC（跑 python tools/icon/build.py ... --png 生成）" >&2
    exit 1
fi
if [ ! -f "$LOGO" ]; then
    echo "找不到矢量源图: $LOGO" >&2
    exit 1
fi

echo "==> 清理旧的 bundle"
rm -rf "$BUNDLE"
mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Resources"

echo "==> 复制可执行文件"
cp "$BIN" "$BUNDLE/Contents/MacOS/$APP_NAME"
chmod +x "$BUNDLE/Contents/MacOS/$APP_NAME"

echo "==> 生成 .icns"
ICONSET="$BUNDLE/Contents/Resources/$APP_NAME.iconset"
mkdir -p "$ICONSET"

# iconutil 要求这 10 个文件齐全，少一个就报 "Unsupported filename"。
#
# 关键：**每个尺寸都从矢量源重新栅格化**（tools/icon/build.py --iconset），
# 而不是把 1024 那张用 sips 缩下来。
# sips 是插值缩小，16/32 这种小尺寸会糊 —— 和 Windows 侧
# "降采样算法"的坑是同一个（实测双线性 vs 面积平均：16px 差 7 倍）。
#
# tile SVG（白底圆角方块 + 黑标）是**中间产物**，仓库里不存 ——
# 这里现算。tools/icon/source/ 下只留单色 logo 母版。
#
# build.py 需要无头 Chrome/Edge。CI 的 macOS runner 上有 Chrome 吗？
# 没有。所以这里 fallback 到 sips —— macOS 上要装 Chrome 才能用
# 矢量直出，日常发版走 sips 也够用（1024 缩到 16 的糊程度比
# Windows 小，因为 macOS 的小尺寸档位用得少）。
if command -v python3 >/dev/null 2>&1 && \
   python3 -c "import sys; sys.exit(0)" 2>/dev/null && \
   { command -v google-chrome >/dev/null 2>&1 || \
     [ -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ]; }; then
    echo "    用矢量源逐尺寸栅格化（Chrome 可用）"

    # mktemp -d 必须带模板：BSD 版（macOS）不给模板会报用法错误。
    tmp="$(mktemp -d "${TMPDIR:-/tmp}/agyicon.XXXXXX")"
    trap 'rm -rf "$tmp"' EXIT

    # 用 python3（不是 python）—— macOS 上没有 python 这个别名。
    #
    # --scale 0.62 是**有讲究的**：图形占 tile 高度 61%。
    # 别改大 —— 0.90 会让图形占 88%，几乎顶到白块的边，
    # 小尺寸下两个眼条会连成一片。详见 tools/icon/tile.py 的模块文档。
    python3 tools/icon/tile.py "$LOGO" "$tmp/tile.svg" \
        --bg "#FFFFFF" --scale 0.62
    python3 tools/icon/build.py "$tmp/tile.svg" "$ICNS_SRC" \
        --iconset "$ICONSET"
else
    echo "    回退到 sips 从 1024 缩放（macOS 上没有 Chrome）"
    sips -z 16 16    "$ICNS_SRC" --out "$ICONSET/icon_16x16.png"       >/dev/null
    sips -z 32 32    "$ICNS_SRC" --out "$ICONSET/icon_16x16@2x.png"    >/dev/null
    sips -z 32 32    "$ICNS_SRC" --out "$ICONSET/icon_32x32.png"       >/dev/null
    sips -z 64 64    "$ICNS_SRC" --out "$ICONSET/icon_32x32@2x.png"    >/dev/null
    sips -z 128 128  "$ICNS_SRC" --out "$ICONSET/icon_128x128.png"     >/dev/null
    sips -z 256 256  "$ICNS_SRC" --out "$ICONSET/icon_128x128@2x.png"  >/dev/null
    sips -z 256 256  "$ICNS_SRC" --out "$ICONSET/icon_256x256.png"     >/dev/null
    sips -z 512 512  "$ICNS_SRC" --out "$ICONSET/icon_256x256@2x.png"  >/dev/null
    sips -z 512 512  "$ICNS_SRC" --out "$ICONSET/icon_512x512.png"     >/dev/null
    sips -z 1024 1024 "$ICNS_SRC" --out "$ICONSET/icon_512x512@2x.png" >/dev/null
fi

iconutil -c icns "$ICONSET" -o "$BUNDLE/Contents/Resources/$APP_NAME.icns"
rm -rf "$ICONSET"

echo "==> 写 Info.plist"
cat > "$BUNDLE/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>${APP_NAME}</string>
	<key>CFBundleDisplayName</key>
	<string>${APP_NAME}</string>
	<key>CFBundleIdentifier</key>
	<string>com.example.agydirector</string>
	<key>CFBundleExecutable</key>
	<string>${APP_NAME}</string>
	<key>CFBundleIconFile</key>
	<string>${APP_NAME}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${VERSION}</string>
	<key>CFBundleVersion</key>
	<string>${VERSION}</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
PLIST

echo "==> ad-hoc 签名"
codesign --force --deep --sign - "$BUNDLE"

echo "==> 校验"
codesign -v "$BUNDLE"
plutil -lint "$BUNDLE/Contents/Info.plist"

echo "==> 完成: $BUNDLE"
