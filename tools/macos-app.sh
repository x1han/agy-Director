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

if [ ! -f "$BIN" ]; then
    echo "找不到二进制: $BIN" >&2
    exit 1
fi
if [ ! -f "$ICNS_SRC" ]; then
    echo "找不到图标源图: $ICNS_SRC（跑 python tools/icon/build.py ... --png 生成）" >&2
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
