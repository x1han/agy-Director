#!/usr/bin/env python3
"""从 SVG 生成多尺寸 Windows ICO。

用法:
    python tools/icon/build.py <输入.svg> <输出.ico> [--sizes 16,24,...]
    python tools/icon/build.py <输入.svg> <输出.png> --png --png-size 1024

流程:
    1. 每个目标尺寸**单独**用无头 Chrome 渲染成 size*4 的 PNG
       （不是渲染一张大图再缩 —— 小尺寸需要源图在该尺寸下的
       准确光栅化，从大图降采样会抹掉细线）
    2. 用面积平均（预乘 alpha）降采样到目标尺寸
    3. 小尺寸存成 32bpp DIB 帧；>= --png-frame-size 的存成 PNG 帧

为什么 256 帧用 PNG：
    256×256 的未压缩 DIB 有 270KB，PNG 只要 6KB（差 40 倍，画质无损）。
    Windows 自己的图标就是这个混合格式（小尺寸 DIB + 256 尺寸 PNG）。

为什么不用 Python 的 PIL/Cairo:
    引入额外依赖不划算；Chrome 已在系统里。

────────────────────────────────────────────────────────────────────────
Windows 到底选哪一帧？（实测，别猜）
────────────────────────────────────────────────────────────────────────
用一个"每帧涂不同纯色"的图标，交给 Windows 自己挑（PrivateExtractIconsW，
也就是资源管理器用的那个 API），读回来的颜色就是它选中的那一帧。
实测结论：

    请求 16  -> 选 16      请求 48  -> 选 48
    请求 20  -> 选 16 ↑1.25 请求 60  -> 选 60
    请求 24  -> 选 24      请求 72  -> 选 72
    请求 32  -> 选 32      请求 96  -> 选 96
    请求 40  -> 选 40      请求 144 -> 选 144
    请求 64  -> 选 64      请求 192 -> 选 192

规则 = **选不超过请求的最大那一帧，然后缩放到请求尺寸**。
注意方向：不是"选最小的够大的帧再缩小"——正好相反。

这有两个后果：

  1. 尺寸阶梯必须**密**。Windows 只在"恰好命中"时才不缩放。
     我们原来只做 16/24/32/48/64/128/256，在 150% 缩放的屏幕上，
     48 逻辑像素的图标要 72 物理像素 → 只能命中 64 帧 → 放大 1.13 倍。
     这就是"图标发糊"的真正原因（用户 150% 屏幕上的实测截图，
     逐像素比对确认就是我们的 64 帧被放大到 72，IoU 0.966）。
     加一条 72 的帧就能精确命中，误差归零。

  2. **必须提供 256 帧**，否则请求 256 时只能拿 128 放大 2 倍。
     （Windows 自带的 explorer.exe 也是 16..64 + 256 PNG。）

为什么 125% / 150% / 175% 的常见缩放都要覆盖：
    资源管理器的图标槽位（逻辑像素）常见有 16/20/24/32/40/48/64/96/128。
    乘上缩放比例后，物理像素可能是 20/24/30/32/40/48/60/72/80/96/120/
    144/160/192/240/256...。把这条链上的物理尺寸都做成帧，
    才能保证在 125%/150%/175% 缩放下都命中而不是被放大。

    实测（每个阶梯真的编进 exe 再问 Windows）：
        阶梯 A（16/24/32/48/64/128/256）150% 下 2/9 精确命中
        阶梯 B（16/20/24/32/40/48/60/64/72/96/128/144/192/256）
                 150% 下 9/9 精确命中   ← 采用
        阶梯 C（再加 30/36 等）      150% 下 9/9，但 exe 大 18KB
                                     收益没有，放弃

────────────────────────────────────────────────────────────────────────
小尺寸的笔画要加粗（光学尺寸）
────────────────────────────────────────────────────────────────────────
logo 的天线笔画在 180 单位的 viewBox 里宽 10 单位。折算成像素：

    16px -> 0.89 px   （亚像素，永远渲染成一团灰）
    24px -> 1.33 px
    32px -> 1.78 px
    48px -> 2.67 px
    64px -> 3.56 px

小于约 1.5px 的笔画不管用什么重采样算法都糊——它只能变成
一个部分覆盖的像素。所以小尺寸必须用**独立的、更粗的**图形，
这就是"光学尺寸"（optical sizing）：同一个标志在不同尺寸下
用不同的笔画粗细，跟字体有 display/optical 尺寸是一个道理。

做法：给每个尺寸配一份"加粗版"SVG，用 stroke 把路径撑粗
（stroke-width 让笔画向外扩），再渲染。

实测效果（放大到 72 物理像素后数"实心黑"像素）：
    16px  102 -> 260     20px  125 -> 218     24px  126 -> 195
    32px  142 -> 154
同时两根天线仍然分得开（白色缝隙 25..29px 没被填死）。
"""

import argparse
import os
import re
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


# ── 光学尺寸：小尺寸用更粗的笔画 ───────────────────────────────────────

# 目标：把每个尺寸的"最细笔画"撑到至少 2 个像素。
#
# logo 的天线笔画在 180 单位 viewBox 里宽 10 单位，撑粗 d 之后
# 宽度变成 10 + d。渲染出的像素宽度 = (10 + d) / 180 * size。
#
# 反解：要让 size 下的笔画有 target 像素宽，
#     d = target * 180 / size - 10
#
# 表格按 target = 2.0px 反解。再粗会让两根天线粘在一起
# （两根天线中心相距 63 单位，stroke 每边各扩 d，
#  间隙 = 63 - 2*d；d=12 时间隙 39 单位 = 16px 下 3.5px，还行；
#  d=22 时间隙只剩 19 单位 = 1.7px，两根几乎粘死）。
STROKE_TABLE = {
    16: 12.5,   # 2.00 px
    20: 8.0,    # 2.00 px
    24: 5.0,    # 2.00 px
    28: 2.9,    # 2.00 px
    32: 1.3,    # 2.00 px
    36: 0.0,    # 2.00 px（原生就够）
    40: 0.0,    # 2.22 px
}


def make_bold_variant(svg_text, stroke):
    """返回一个加粗版 SVG：给所有 path 加 stroke + stroke-linejoin。

    原理：SVG 的 stroke 画在 path **两侧**，所以给一条 10 单位宽的
    笔画加 d 单位的 stroke，实际宽度变成 10 + d 单位。
    stroke-linejoin=round 让拐角不变尖，视觉上更接近原图。
    """
    if stroke <= 0:
        return svg_text
    out = re.sub(
        r'(<path\b[^>]*?)(/?>)',
        lambda m: (f'{m.group(1)} stroke="currentColor" '
                   f'stroke-width="{stroke:g}" stroke-linejoin="round" '
                   f'stroke-linecap="round"{m.group(2)}'),
        svg_text)
    # 用 currentColor 时要保证颜色来自 fill；直接写死 fill 颜色更稳。
    out = re.sub(r'<g fill="([^"]+)"',
                 lambda m: f'<g fill="{m.group(1)}" color="{m.group(1)}"', out)
    return out


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
    """面积平均（box filter）缩放 RGBA。

    ── 为什么不用双线性 ────────────────────────────────────────────────
    双线性只取 2×2 个邻域像素，做大面积缩小时（512→16）等于
    "只看了源图里 1/1024 的信息"，细线条直接被抹掉或断成锯齿。

    实测（以 chrome@1024 + LANCZOS 为真值，mean|diff| 0-255）：
        尺寸    双线性    面积平均
          16      22.5       3.1
          32      13.2       3.8
          64       7.5       3.8
         128       3.9       2.4
         256       1.7       1.3

    16px 是任务栏和资源管理器列表的常用尺寸 —— 22.5 的误差意味着
    "明显糊"，而 3.1 已经跟系统自带图标一个水平。

    ── 必须预乘 alpha ─────────────────────────────────────────────────
    直接对 RGBA 四个通道独立平均，会在半透明边缘产生色晕
    （黑色轮廓旁边渗出一圈灰边）。正确做法是：
        1. 累加时用 (r*a, g*a, b*a, a)，即预乘
        2. 平均后再除以平均 alpha 还原颜色

    纯标准库实现（不引 PIL）：这个脚本要能在没装任何第三方库的
    机器上跑，CI 也只依赖标准库 + 无头 Chrome。
    """
    out = bytearray(dw * dh * 4)
    x_ratio = sw / dw
    y_ratio = sh / dh

    for y in range(dh):
        # 源图中覆盖这个目标像素的行区间
        sy0 = int(y * y_ratio)
        sy1 = min(sh, max(sy0 + 1, int((y + 1) * y_ratio)))
        for x in range(dw):
            sx0 = int(x * x_ratio)
            sx1 = min(sw, max(sx0 + 1, int((x + 1) * x_ratio)))

            sr = sg = sb = sa = 0.0
            n = 0
            for sy in range(sy0, sy1):
                row_base = sy * sw
                for sx in range(sx0, sx1):
                    i = (row_base + sx) * 4
                    a = src[i + 3] / 255.0
                    sr += src[i] * a
                    sg += src[i + 1] * a
                    sb += src[i + 2] * a
                    sa += src[i + 3]
                    n += 1

            if n:
                d = (y * dw + x) * 4
                avg_a = sa / n
                if avg_a > 0:
                    # 预乘色还原：avg_premult * 255 / avg_a
                    out[d] = _clamp8((sr / n) * 255.0 / avg_a)
                    out[d + 1] = _clamp8((sg / n) * 255.0 / avg_a)
                    out[d + 2] = _clamp8((sb / n) * 255.0 / avg_a)
                out[d + 3] = _clamp8(avg_a)
    return out


def _clamp8(v):
    """把浮点值夹到 0..255 并取整。"""
    iv = int(v + 0.5)
    return 0 if iv < 0 else (255 if iv > 255 else iv)


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


def build_ico(frames, sizes):
    """打包 ICO。

    frames 是 (数据, 是否 PNG) 的列表 —— 每一项要么是 DIB
    （BITMAPINFOHEADER + 像素），要么是 PNG 文件字节。

    ── 为什么 256 帧要用 PNG ─────────────────────────────────────────
    256×256 的 32bpp DIB 是 270KB（未压缩 BGRA），光这一帧就撑爆
    .syso。同一张图存成 PNG 只要 6.1KB —— 差 40 倍，画质无损。

    Windows 自己就是这么干的：explorer.exe / cmd.exe / notepad.exe
    的图标都是"小尺寸 DIB + 256 尺寸 PNG"的混合格式。

    ── 关键（踩过的坑）─────────────────────────────────────────────────
      - ICONDIRENTRY 的 bytesInRes 在文件偏移 14、imageOffset 在偏移 18
        （entry 从偏移 6 开始，占 16 字节）
      - DIB 帧的 bitCount 写 0（真实 bpp 由 DIB 头给出）；
        PNG 帧写 32 —— PNG 里没有 bpp 字段，写 0 虽然实测也能解码，
        但跟 Windows 惯例不符，写 32 更保险
    """
    total = 6 + 16 * len(frames)
    for data, _ in frames:
        total += len(data)

    ico = bytearray(total)
    ico[0:2] = struct.pack("<H", 0)      # reserved
    ico[2:4] = struct.pack("<H", 1)      # type = icon
    ico[4:6] = struct.pack("<H", len(frames))

    # 两趟写入：先全部 entry 表，再全部图像数据。
    #
    # 为什么不能一趟写：把 entry 表和图像数据混在一个循环里写时，
    # 后续对 entry 表的切片赋值会触发 bytearray 的整体搬移，
    # 把前面已经拷进去的图像数据挪走——表现为"只有最后一个尺寸
    # 的 DIB 有内容，前面的全是 0"（实测踩过）。
    # 分两趟后不再有交叉写入，结果稳定。
    offset = 6 + 16 * len(frames)
    for i, ((data, is_png), size) in enumerate(zip(frames, sizes)):
        e = 6 + 16 * i
        w = 0 if size >= 256 else size
        h = 0 if size >= 256 else size
        ico[e] = w
        ico[e + 1] = h
        ico[e + 2] = 0        # colorCount
        ico[e + 3] = 0        # reserved
        # ⚠️ 切片必须写成 e+4:e+6 这样的**完整表达式**。
        # 写成 e+4:6 的话，Python 解析成"从 e+4 到 6"——
        # 终点 6 小于起点（e=6 时起点 10），是**倒挂切片**，
        # 而倒挂切片的赋值语义是**插入**而不是替换，
        # 结果是每帧往文件里多插 12 字节（实测 7 帧多 84 字节尾部垃圾）。
        #
        # 值当时看着是对的（插入位置恰好把字节推到了正确偏移），
        # 但那是巧合 —— 一旦有人按下面"两趟写入"的注释把它合回
        # 一趟，帧数据就会错位。所以这里必须是替换语义。
        ico[e + 4:e + 6] = struct.pack("<H", 1)   # planes
        ico[e + 6:e + 8] = struct.pack("<H", 32 if is_png else 0)
        ico[e + 8:e + 12] = struct.pack("<I", len(data))
        ico[e + 12:e + 16] = struct.pack("<I", offset)
        offset += len(data)

    offset = 6 + 16 * len(frames)
    for data, _ in frames:
        ico[offset:offset + len(data)] = data
        offset += len(data)

    return bytes(ico)


def build_png(rgba, size, height=None):
    """把 RGBA 像素编码成 PNG 字节流。

    PNG 格式（简化）：
      - 8 字节签名
      - IHDR：宽、高、位深 8、颜色类型 6（RGBA）、压缩/滤波/交错全 0
      - IDAT：zlib 压缩的扫描线，每行前面加一个滤波字节
      - IEND

    滤波用 0（None）—— 体积大一点，但省事且绝对正确。
    macOS 的 sips/iconutil 只认格式，不在乎压缩率。

    size 是宽度；height 省略时按正方形处理。
    """
    import zlib

    if height is None:
        height = size

    raw = bytearray()
    stride = size * 4
    for row in range(height):
        raw.append(0)  # 滤波类型 0 = None
        raw += rgba[row * stride:(row + 1) * stride]

    def chunk(tag, data):
        out = struct.pack(">I", len(data)) + tag + data
        return out + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    ihdr = struct.pack(">IIBBBBB", size, height, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(bytes(raw), 9))
            + chunk(b"IEND", b""))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("svg")
    ap.add_argument("out")
    ap.add_argument("--sizes",
                    default="16,20,24,32,40,48,60,64,72,96,128,144,192,256",
                    help="要生成的帧尺寸（逗号分隔）。"
                         "默认这条链保证 Windows 在 100/125/150/175/200 "
                         "百分比的显示缩放下都能命中精确尺寸的帧，"
                         "而不是把小帧放大。")
    ap.add_argument("--png", action="store_true",
                    help="输出 PNG 而不是 ICO（macOS iconset 需要）")
    ap.add_argument("--png-size", type=int, default=1024,
                    help="--png 模式下的边长，默认 1024")
    ap.add_argument("--png-frame-size", type=int, default=96,
                    help="从该尺寸起，ICO 里用 PNG 压缩帧（0 = 全用 DIB）。"
                         "96 及以上用 PNG 是安全的：实测 Windows 会正确"
                         "选用这些帧（96/128/144/192 全部命中）。"
                         "更小的尺寸必须用 DIB —— 实测 PNG 帧在 72 及以下"
                         "会被 Windows 跳过，导致选不中。")
    ap.add_argument("--supersample", type=int, default=4,
                    help="每个尺寸渲染成 size*supersample 再降采样")
    ap.add_argument("--iconset", metavar="DIR",
                    help="输出 macOS .iconset 目录（10 个标准命名的 PNG），"
                         "每个尺寸都从矢量源单独栅格化。"
                         "比 sips 从 1024 缩下来准：sips 是插值缩小，"
                         "16/32 这类小尺寸会糊。")
    args = ap.parse_args()

    chrome = find_chrome()
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)

    # ── --iconset 模式：给 macOS 用 ──────────────────────────────────
    #
    # iconutil 要求这 10 个文件齐全，少一个就报 "Unsupported filename"。
    # 关键区别：**每个尺寸都从矢量源重新栅格化**，而不是把 1024 那张
    # 用 sips 缩下来 —— 后者是插值缩小，16/32 这种小尺寸必然糊，
    # 和 Windows 侧"降采样算法"的坑是同一个。
    if args.iconset:
        os.makedirs(args.iconset, exist_ok=True)
        # (输出文件名, 像素边长)
        entries = [
            ("icon_16x16.png", 16), ("icon_16x16@2x.png", 32),
            ("icon_32x32.png", 32), ("icon_32x32@2x.png", 64),
            ("icon_128x128.png", 128), ("icon_128x128@2x.png", 256),
            ("icon_256x256.png", 256), ("icon_256x256@2x.png", 512),
            ("icon_512x512.png", 512), ("icon_512x512@2x.png", 1024),
        ]
        svg_text = open(args.svg, "r", encoding="utf-8").read()
        for fname, px in entries:
            ss = args.supersample if args.supersample > 1 else 1
            big = px * ss
            stroke = STROKE_TABLE.get(px, 0.0)
            src = args.svg
            if stroke > 0:
                src = os.path.join(tempfile.gettempdir(), f"bold_{px}.svg")
                with open(src, "w", encoding="utf-8") as f:
                    f.write(make_bold_variant(svg_text, stroke))
            with tempfile.TemporaryDirectory(prefix="ics-") as td:
                png = os.path.join(td, "r.png")
                render_svg_to_png(chrome, src, png, size=big)
                sw, sh, rgba = read_png_rgba(png)
                scaled = resize_rgba(rgba, sw, sh, px, px) if ss > 1 else rgba
                data = build_png(scaled, px)
            outp = os.path.join(args.iconset, fname)
            with open(outp, "wb") as f:
                f.write(data)
            note = f"  (加粗 {stroke:g})" if stroke > 0 else ""
            print(f"  {fname:<22} {px:>5}px  {len(data):>7} 字节{note}")
        print(f"已写入 iconset 到 {args.iconset}")
        print("在 macOS 上跑: iconutil -c icns <iconset> -o <名字>.icns")
        return

    # ── --png 模式：给 macOS iconset 用 ──────────────────────────────
    if args.png:
        with tempfile.TemporaryDirectory(prefix="ico-build-") as td:
            png = os.path.join(td, "render.png")
            render_svg_to_png(chrome, args.svg, png, size=args.png_size)
            sw, sh, rgba = read_png_rgba(png)
            print(f"渲染完成: {sw}x{sh}")
            data = build_png(rgba, sw, sh)
            with open(args.out, "wb") as f:
                f.write(data)
            print(f"已写入 {args.out} ({len(data)} 字节, PNG {sw}x{sh})")
        return

    sizes = [int(s) for s in args.sizes.split(",")]

    # ── 每个尺寸单独渲染 ─────────────────────────────────────────────
    #
    # 为什么不用"渲染一张大图再降采样"：
    # 小尺寸（16/24/32）需要的是**源图在该尺寸下的准确光栅化**，
    # 从大图降采样会丢掉细线 —— 16px 的实测误差从 3.1 涨到 22.5。
    #
    # 做法：每个尺寸渲染成 size*supersample 的高分辨率图，
    # 再用面积平均降采样到目标尺寸。
    # 超采样倍数越大越准，4 已经和"直接按该尺寸光栅化"持平。
    frames = []
    with tempfile.TemporaryDirectory(prefix="ico-build-") as td:
        svg_text = open(args.svg, "r", encoding="utf-8").read()
        for size in sizes:
            ss = args.supersample if args.supersample > 1 else 1
            big = size * ss

            # 小尺寸用加粗版（光学尺寸），大尺寸用原图
            stroke = STROKE_TABLE.get(size, 0.0)
            src = args.svg
            if stroke > 0:
                bold = make_bold_variant(svg_text, stroke)
                src = os.path.join(td, f"bold_{size}.svg")
                with open(src, "w", encoding="utf-8") as f:
                    f.write(bold)

            png = os.path.join(td, f"render_{size}.png")
            render_svg_to_png(chrome, src, png, size=big)
            sw, sh, rgba = read_png_rgba(png)

            if ss > 1:
                scaled = resize_rgba(rgba, sw, sh, size, size)
            else:
                scaled = rgba

            use_png = args.png_frame_size > 0 and size >= args.png_frame_size
            if use_png:
                data = build_png(scaled, size)
                frames.append((data, True))
                kind = "PNG"
            else:
                data = build_dib(scaled, size)
                frames.append((data, False))
                kind = "DIB "
            note = f"  (加粗 {stroke:g})" if stroke > 0 else ""
            print(f"  {size:>4}px  {kind} 帧   {len(data):>7} 字节{note}")

    ico = build_ico(frames, sizes)
    verify_ico(ico, sizes)
    with open(args.out, "wb") as f:
        f.write(ico)
        print(f"已写入 {args.out} ({len(ico)} 字节, {len(sizes)} 个尺寸)")


def verify_ico(ico, sizes):
    """自检：把生成的 ICO 重新解析一遍，确认结构自洽。

    为什么要有这一步：
    build_ico 里的 entry 表切片如果写成倒挂形式（e+4:6），
    赋值语义会变成"插入"而不是"替换" —— 字节值看着是对的
    （插入位置恰好把数据推到了正确偏移），但文件里会多出
    每帧 12 字节的尾部垃圾，而且一旦有人把两趟写入合回一趟，
    帧数据就会直接错位。

    这种 bug 从肉眼看输出是发现不了的，只能靠断言。
    """
    n = struct.unpack_from("<H", ico, 4)[0]
    if n != len(sizes):
        sys.exit(f"❌ 帧数不对：目录说 {n} 帧，实际 {len(sizes)} 个")

    expected = 6 + 16 * n
    for i in range(n):
        e = 6 + 16 * i
        w = ico[e] or 256
        h = ico[e + 1] or 256
        if w != sizes[i] or h != sizes[i]:
            sys.exit(f"❌ 第 {i} 帧尺寸不对：目录说 {w}x{h}，应为 {sizes[i]}")
        size = struct.unpack_from("<I", ico, e + 8)[0]
        off = struct.unpack_from("<I", ico, e + 12)[0]
        if off != expected:
            sys.exit(f"❌ 第 {i} 帧偏移不对：目录说 {off}，应为 {expected}")
        expected += size

    if len(ico) != expected:
        sys.exit(f"❌ 文件长度不对：实际 {len(ico)}，按目录算应为 {expected}"
                 f"（多 {len(ico)-expected} 字节 —— entry 表的切片边界写错了？）")
    print(f"  自检通过：{n} 帧，偏移与长度自洽（{len(ico)} 字节）")


if __name__ == "__main__":
    main()
