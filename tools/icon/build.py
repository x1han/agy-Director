#!/usr/bin/env python3
"""从 SVG 生成多尺寸 Windows ICO。

用法:
    python tools/icon/build.py <输入.svg> <输出.ico> [--tile] [--radius N]

流程:
    1. 用无头 Chrome 把 SVG 渲染成透明背景的 512x512 PNG
    2. 逐尺寸缩放，打包成 32bpp truecolor ICO

为什么不用 Python 的 PIL/Cairo:
    本机没有装，且引入额外依赖不划算；Chrome 已在系统里。
"""

import argparse
import json
import os
import shutil
import struct
import subprocess
import sys
import tempfile

# ── Chrome 查找 ────────────────────────────────────────────────────────

CHROME_CANDIDATES = [
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
]


def find_chrome():
    for p in CHROME_CANDIDATES:
        if os.path.exists(p):
            return p
    which = shutil.which("chrome") or shutil.which("chromium") or shutil.which("google-chrome")
    if which:
        return which
    sys.exit("找不到 Chrome/Chromium，无法渲染 SVG")


# ── SVG 渲染 ───────────────────────────────────────────────────────────

HTML_TEMPLATE = """<!DOCTYPE html><html><head><meta charset="utf-8"><style>
html,body{{margin:0;padding:0;background:transparent}}
img{{display:block;width:{size}px;height:{size}px}}
</style></head><body><img src="{svg}"></body></html>"""


def render_svg_to_png(chrome, svg_path, out_png, size=512):
    """用无头 Chrome 把 SVG 渲染成透明背景 PNG。

    注意：必须显式给 --screenshot=... 的**文件类型**。
    只把 .png 当成扩展名时，某些 Chrome 版本写出来的是
    **BMP**（实测：文件头 00 00 01 00 = BITMAPINFOHEADER），
    后面的 PNG 解析器直接崩。所以这里用 --screenshot=<path>.png
    并在写完后校验文件头。
    """
    workdir = tempfile.mkdtemp(prefix="ico-build-")
    try:
        svg_name = os.path.basename(svg_path)
        shutil.copy(svg_path, os.path.join(workdir, svg_name))
        html = os.path.join(workdir, "page.html")
        with open(html, "w", encoding="utf-8") as f:
            f.write(HTML_TEMPLATE.format(size=size, svg=svg_name))

        url = "file:///" + html.replace("\\", "/").lstrip("/")
        # 用一个独立的 user-data-dir，避免与用户正在运行的 Chrome 抢实例
        # （会直接把请求转发到已有实例，导致截不到图）。
        profile = os.path.join(workdir, "profile")
        cmd = [
            chrome, "--headless=new", "--disable-gpu",
            f"--user-data-dir={profile}",
            f"--screenshot={out_png}",
            f"--window-size={size},{size}",
            "--default-background-color=00000000",
            "--hide-scrollbars",
            "--no-first-run",
            "--no-default-browser-check",
            url,
        ]
        r = subprocess.run(cmd, capture_output=True, timeout=120)
        if not os.path.exists(out_png):
            sys.exit(f"渲染失败:\n{r.stdout.decode(errors='replace')}\n{r.stderr.decode(errors='replace')}")

        with open(out_png, "rb") as f:
            sig = f.read(8)
        if sig != b"\x89PNG\r\n\x1a\n":
            sys.exit(f"渲染出来的不是 PNG（文件头 {sig.hex()}）——"
                     f"多半是 Chrome 写成了 BMP")
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


# ── PNG 读取（只用标准库） ─────────────────────────────────────────────

def read_png_rgba(path):
    """读 PNG，返回 (width, height, bytearray of RGBA)。"""
    import zlib

    with open(path, "rb") as f:
        data = f.read()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        sys.exit("不是合法 PNG")

    pos = 8
    idat = bytearray()
    width = height = 0
    bitdepth = colortype = 0
    plte = None
    trns = None

    while pos < len(data):
        (length,) = struct.unpack(">I", data[pos:pos + 4])
        ctype = data[pos + 4:pos + 8]
        chunk = data[pos + 8:pos + 8 + length]
        pos += 12 + length  # length + type + data + crc

        if ctype == b"IHDR":
            width, height, bitdepth, colortype = struct.unpack(">IIBB", chunk[:10])
        elif ctype == b"PLTE":
            plte = chunk
        elif ctype == b"tRNS":
            trns = chunk
        elif ctype == b"IDAT":
            idat += chunk
        elif ctype == b"IEND":
            break

    if bitdepth != 8:
        sys.exit(f"暂只支持 8 位深度的 PNG（当前 {bitdepth}）")

    channels = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[colortype]
    raw = zlib.decompress(bytes(idat))

    stride = width * channels
    out = bytearray(width * height * 4)
    prev = bytearray(stride)

    pos = 0
    for y in range(height):
        ftype = raw[pos]
        pos += 1
        line = bytearray(raw[pos:pos + stride])
        pos += stride

        # Paeth / Sub / Up / Average 反滤波
        if ftype == 1:
            for i in range(channels, stride):
                line[i] = (line[i] + line[i - channels]) & 0xFF
        elif ftype == 2:
            for i in range(stride):
                line[i] = (line[i] + prev[i]) & 0xFF
        elif ftype == 3:
            for i in range(stride):
                left = line[i - channels] if i >= channels else 0
                line[i] = (line[i] + ((left + prev[i]) >> 1)) & 0xFF
        elif ftype == 4:
            for i in range(stride):
                a = line[i - channels] if i >= channels else 0
                b = prev[i]
                c = prev[i - channels] if i >= channels else 0
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                if pa <= pb and pa <= pc:
                    pr = a
                elif pb <= pc:
                    pr = b
                else:
                    pr = c
                line[i] = (line[i] + pr) & 0xFF

        # 展开成 RGBA
        base = y * width * 4
        for x in range(width):
            s = x * channels
            d = base + x * 4
            if colortype == 6:
                out[d:d + 4] = line[s:s + 4]
            elif colortype == 2:
                out[d:d + 3] = line[s:s + 3]
                out[d + 3] = 255
            elif colortype == 0:
                g = line[s]
                out[d] = out[d + 1] = out[d + 2] = g
                out[d + 3] = 255
            elif colortype == 4:
                g = line[s]
                out[d] = out[d + 1] = out[d + 2] = g
                out[d + 3] = line[s + 1]
            elif colortype == 3:
                idx = line[s]
                out[d:d + 3] = plte[idx * 3:idx * 3 + 3]
                out[d + 3] = trns[idx] if trns and idx < len(trns) else 255
        prev = line

    return width, height, out


def resize_rgba(src, sw, sh, dw, dh):
    """双线性缩放 RGBA。

    ⚠️ 采样坐标必须先夹到 [0, size-1] 再算插值权重。
    放大时（sh < dh）边缘的采样坐标会是负数
    （如 y=0 时 sy = 0.5*sh/dh - 0.5 < 0），
    权重变成负数 → 外插 → 值越界 → bytearray 赋值报
    "byte must be in range(0, 256)"。
    之前只在缩小（512→256 及更小）时用过这条路径，
    坐标恒为正，所以这个坑一直没暴露；放大时才炸。
    """
    out = bytearray(dw * dh * 4)
    for y in range(dh):
        sy = (y + 0.5) * sh / dh - 0.5
        sy = min(max(sy, 0.0), float(sh - 1))
        y0 = int(sy)
        y1 = min(sh - 1, y0 + 1)
        fy = sy - y0
        for x in range(dw):
            sx = (x + 0.5) * sw / dw - 0.5
            sx = min(max(sx, 0.0), float(sw - 1))
            x0 = int(sx)
            x1 = min(sw - 1, x0 + 1)
            fx = sx - x0

            i00 = (y0 * sw + x0) * 4
            i01 = (y0 * sw + x1) * 4
            i10 = (y1 * sw + x0) * 4
            i11 = (y1 * sw + x1) * 4
            d = (y * dw + x) * 4

            for c in range(4):
                a = src[i00 + c] * (1 - fx) + src[i01 + c] * fx
                b = src[i10 + c] * (1 - fx) + src[i11 + c] * fx
                v = int(round(a * (1 - fy) + b * fy))
                out[d + c] = 0 if v < 0 else (255 if v > 255 else v)
    return out


# ── ICO 打包 ───────────────────────────────────────────────────────────

def build_dib(rgba, size):
    """构造 32bpp truecolor 的 BITMAPINFOHEADER + XOR + AND。"""
    row_bytes = size * 4

    # XOR：自底向上，BGRA
    xor = bytearray(row_bytes * size)
    for row in range(size):
        s = row * size * 4
        d = (size - 1 - row) * row_bytes
        for x in range(size):
            si = s + x * 4
            di = d + x * 4
            xor[di] = rgba[si + 2]      # B
            xor[di + 1] = rgba[si + 1]  # G
            xor[di + 2] = rgba[si]      # R
            xor[di + 3] = rgba[si + 3]  # A

    # AND mask：位=1 表示该像素被屏蔽（透明）。
    #
    # 关键：即使 DIB 是 32bpp（alpha 已在 XOR 里表达了透明），
    # AND mask 也必须按 alpha 正确置位，不能给全 0。
    # 给全 0 时 Windows 会把透明区域当作不透明来合成，
    # 表现为"图标是一块黑方块"（实测踩过）。
    mask_stride = ((size + 31) // 32) * 4
    mask = bytearray(mask_stride * size)
    for row in range(size):
        s = row * size * 4
        d = (size - 1 - row) * mask_stride
        for x in range(size):
            if rgba[s + x * 4 + 3] < 128:
                byte_off = d + x // 8
                bit = 7 - (x % 8)
                mask[byte_off] |= (1 << bit)

    hdr = struct.pack("<IiiHHIIiiII",
                       40,          # biSize
                       size,        # biWidth
                       size * 2,    # biHeight（XOR + AND）
                       1,           # biPlanes
                       32,          # biBitCount
                       0,           # biCompression = BI_RGB
                       len(xor),    # biSizeImage
                       0, 0, 0, 0)
    return bytes(hdr) + bytes(xor) + bytes(mask)


def build_ico(dibs, sizes):
    """打包 ICO。

    关键（踩过的坑）:
      - ICONDIRENTRY 的 bytesInRes 在文件偏移 14、imageOffset 在偏移 18
        （entry 从偏移 6 开始，占 16 字节）
      - entry 的 bitCount 字段必须为 0（真实 bpp 由 DIB 头给出）
    """
    total = 6 + 16 * len(dibs)
    for d in dibs:
        total += len(d)

    ico = bytearray(total)
    ico[0:2] = struct.pack("<H", 0)      # reserved
    ico[2:4] = struct.pack("<H", 1)      # type = icon
    ico[4:6] = struct.pack("<H", len(dibs))

    # 两趟写入：先全部 entry 表，再全部图像数据。
    #
    # 为什么不能一趟写：把 entry 表和图像数据混在一个循环里写时，
    # 后续对 entry 表的切片赋值会触发 bytearray 的整体搬移，
    # 把前面已经拷进去的图像数据挪走——表现为"只有最后一个尺寸
    # 的 DIB 有内容，前面的全是 0"（实测踩过）。
    # 分两趟后不再有交叉写入，结果稳定。
    offset = 6 + 16 * len(dibs)
    for i, (dib, size) in enumerate(zip(dibs, sizes)):
        e = 6 + 16 * i
        w = 0 if size >= 256 else size
        h = 0 if size >= 256 else size
        ico[e] = w
        ico[e + 1] = h
        ico[e + 2] = 0        # colorCount
        ico[e + 3] = 0        # reserved
        ico[e + 4:6] = struct.pack("<H", 1)   # planes
        ico[e + 6:8] = struct.pack("<H", 0)   # bitCount = 0
        ico[e + 8:12] = struct.pack("<I", len(dib))
        ico[e + 12:16] = struct.pack("<I", offset)
        offset += len(dib)

    offset = 6 + 16 * len(dibs)
    for dib in dibs:
        ico[offset:offset + len(dib)] = dib
        offset += len(dib)

    return bytes(ico)


def build_png(rgba, size):
    """把 RGBA 像素编码成 PNG 字节流。

    PNG 格式（简化）：
      - 8 字节签名
      - IHDR：宽、高、位深 8、颜色类型 6（RGBA）、压缩/滤波/交错全 0
      - IDAT：zlib 压缩的扫描线，每行前面加一个滤波字节
      - IEND

    滤波用 0（None）—— 体积大一点，但省事且绝对正确。
    macOS 的 sips/iconutil 只认格式，不在乎压缩率。
    """
    import zlib

    raw = bytearray()
    stride = size * 4
    for row in range(size):
        raw.append(0)  # 滤波类型 0 = None
        raw += rgba[row * stride:(row + 1) * stride]

    def chunk(tag, data):
        out = struct.pack(">I", len(data)) + tag + data
        return out + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
            + chunk(b"IEND", b""))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("svg")
    ap.add_argument("out")
    ap.add_argument("--sizes", default="16,24,32,48,64,128,256")
    ap.add_argument("--png", action="store_true",
                    help="输出 PNG 而不是 ICO（macOS iconset 需要）")
    ap.add_argument("--png-size", type=int, default=1024,
                    help="--png 模式下的边长，默认 1024")
    args = ap.parse_args()

    chrome = find_chrome()
    with tempfile.TemporaryDirectory(prefix="ico-build-") as td:
        png = os.path.join(td, "render.png")
        render_svg_to_png(chrome, args.svg, png)
        sw, sh, rgba = read_png_rgba(png)
        print(f"渲染完成: {sw}x{sh}")

        os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)

        if args.png:
            scaled = resize_rgba(rgba, sw, sh, args.png_size, args.png_size)
            data = build_png(scaled, args.png_size)
            with open(args.out, "wb") as f:
                f.write(data)
            print(f"已写入 {args.out} ({len(data)} 字节, PNG {args.png_size}x{args.png_size})")
            return

        sizes = [int(s) for s in args.sizes.split(",")]
        dibs = []
        for size in sizes:
            scaled = resize_rgba(rgba, sw, sh, size, size)
            dibs.append(build_dib(scaled, size))

        ico = build_ico(dibs, sizes)
        with open(args.out, "wb") as f:
            f.write(ico)
        print(f"已写入 {args.out} ({len(ico)} 字节, {len(sizes)} 个尺寸)")


if __name__ == "__main__":
    main()
