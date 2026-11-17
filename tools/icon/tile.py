#!/usr/bin/env python3
"""从产品 logo 生成「白色圆角矩形 + 黑色图标」的 tile SVG。

用法:
    python tools/icon/tile.py <源.svg> <输出.svg> [--radius 40] [--scale 0.60]

背景:
    用户的图标要求是「白色底、黑色图标的圆角矩形」。
    源 SVG 是纯路径的单色 logo（fill:#202124）。

做法:
    1. **真正解析路径**求出图标的真实包围盒。
       不能用「把数字两两配对」的土办法：SVG 路径里相对指令
       （c / l / m ...）的参数是相对当前点的增量，两两配对会把
       x 和 y 的数混在一起，算出离谱的框（实测踩过：
       156x224 明显不对，图标的实际比例不是那样）。
       这里实现一个完整的路径解析器，支持
       M m L l H h V v C c S s Q q T t A a Z z，
       并把贝塞尔控制点算进去（控制点可能落在曲线包围盒之外）。
    2. 把源 SVG 的 <path> 整段搬进新 SVG，用 <g> 的 transform
       做平移缩放，把图标居中放进 tile。
       不改写 path 的坐标：Illustrator 导出的路径混着绝对与相对指令，
       手改容易错；transform 交给渲染器算最稳。
"""

import argparse
import re
import sys


# ── 路径解析 ───────────────────────────────────────────────────────────

def tokenize_path(d):
    """把 path 的 d 属性切成 (命令, [参数...]) 序列。"""
    tokens = re.findall(r'([MmLlHhVvCcSsQqTtAaZz])|(-?\d*\.?\d+(?:[eE][-+]?\d+)?)', d)
    out = []
    cmd = None
    args = []
    for c, n in tokens:
        if c:
            if cmd:
                out.append((cmd, args))
            cmd = c
            args = []
        else:
            args.append(float(n))
    if cmd:
        out.append((cmd, args))
    return out


def path_bbox(d):
    """返回路径的包围盒 (x0, y0, x1, y1)，含曲线控制点。

    对贝塞尔曲线，用控制点的极值作保守包围（控制点一定在曲线上，
    所以控制点包围盒一定包含曲线包围盒——只是会略大一点，
    对"把图标放进 tile 里居中"这个用途完全够）。
    """
    x = y = 0.0
    minx = miny = float("inf")
    maxx = maxy = float("-inf")

    def hit(px, py):
        nonlocal minx, miny, maxx, maxy
        minx = min(minx, px)
        maxx = max(maxx, px)
        miny = min(miny, py)
        maxy = max(maxy, py)

    for cmd, args in tokenize_path(d):
        rel = cmd.islower()
        c = cmd.upper()

        if c == "Z":
            continue

        # 每个命令的参数个数
        arity = {"M": 2, "L": 2, "H": 1, "V": 1,
                 "C": 6, "S": 4, "Q": 4, "T": 2, "A": 7}[c]
        if c == "M":
            arity = 2
        if not args:
            continue

        # 把参数按 arity 分组
        groups = []
        vals = list(args)
        if c == "M":
            # M 之后的额外参数对是隐式的 L
            groups.append(("M", vals[0:2]))
            rest = vals[2:]
            for i in range(0, len(rest) - 1, 2):
                groups.append(("L", rest[i:i + 2]))
        else:
            for i in range(0, len(vals) - arity + 1, arity):
                groups.append((c, vals[i:i + arity]))

        for g, a in groups:
            gc = g
            if g == "M":
                nx, ny = (x + a[0], y + a[1]) if rel else (a[0], a[1])
                hit(nx, ny)
                x, y = nx, ny
            elif g == "L":
                nx, ny = (x + a[0], y + a[1]) if rel else (a[0], a[1])
                hit(nx, ny)
                x, y = nx, ny
            elif g == "H":
                nx = (x + a[0]) if rel else a[0]
                hit(nx, y)
                x = nx
            elif g == "V":
                ny = (y + a[0]) if rel else a[0]
                hit(x, ny)
                y = ny
            elif g == "C":
                if rel:
                    p = [(x + a[0], y + a[1]), (x + a[2], y + a[3]), (x + a[4], y + a[5])]
                else:
                    p = [(a[0], a[1]), (a[2], a[3]), (a[4], a[5])]
                for px, py in p:
                    hit(px, py)
                x, y = p[2]
            elif g in ("S", "Q"):
                if rel:
                    p = [(x + a[0], y + a[1]), (x + a[2], y + a[3])]
                else:
                    p = [(a[0], a[1]), (a[2], a[3])]
                for px, py in p:
                    hit(px, py)
                x, y = p[-1]
            elif g == "T":
                nx, ny = (x + a[0], y + a[1]) if rel else (a[0], a[1])
                hit(nx, ny)
                x, y = nx, ny
            elif g == "A":
                nx, ny = (x + a[5], y + a[6]) if rel else (a[5], a[6])
                hit(nx, ny)
                x, y = nx, ny

    if minx == float("inf"):
        return None
    return (minx, miny, maxx, maxy)


# ── SVG 提取 ───────────────────────────────────────────────────────────

def extract_body(svg_text):
    """取出源 SVG 的 path 元素、填充色、viewBox。"""
    m = re.search(r'viewBox\s*=\s*"([^"]+)"', svg_text)
    if not m:
        sys.exit("源 SVG 没有 viewBox")
    viewbox = m.group(1).strip()

    fill = "#202124"
    fm = re.search(r'\.st\d+\s*\{\s*fill:\s*([^;}]+)', svg_text)
    if fm:
        fill = fm.group(1).strip()

    paths = re.findall(r'<path\b[^>]*/?>', svg_text, flags=re.S)
    if not paths:
        sys.exit("源 SVG 里没有 <path> 元素")
    return paths, fill, viewbox


def parse_viewbox(viewbox):
    parts = viewbox.replace(",", " ").split()
    if len(parts) != 4:
        sys.exit(f"viewBox 解析失败: {viewbox}")
    return [float(p) for p in parts]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src")
    ap.add_argument("out")
    ap.add_argument("--radius", type=float, default=40.0, help="圆角矩形的 rx")
    ap.add_argument("--scale", type=float, default=0.60, help="图标占 tile 高度的比例")
    ap.add_argument("--bg", default="#FFFFFF", help="底色")
    args = ap.parse_args()

    with open(args.src, "r", encoding="utf-8") as f:
        svg_text = f.read()

    paths, fill, viewbox = extract_body(svg_text)
    vx, vy, vw, vh = parse_viewbox(viewbox)

    # 所有 path 的包围盒的并集
    minx = miny = float("inf")
    maxx = maxy = float("-inf")
    for p in paths:
        dm = re.search(r'd\s*=\s*"([^"]+)"', p)
        if not dm:
            continue
        bb = path_bbox(dm.group(1))
        if not bb:
            continue
        minx = min(minx, bb[0])
        miny = min(miny, bb[1])
        maxx = max(maxx, bb[2])
        maxy = max(maxy, bb[3])

    if minx == float("inf"):
        sys.exit("解析不到路径坐标")

    bw, bh = maxx - minx, maxy - miny
    bcx, bcy = (minx + maxx) / 2, (miny + maxy) / 2

    # 目标：图标高度 = tile 高度 * scale，且水平垂直都居中。
    target_h = vh * args.scale
    s = target_h / bh

    tx = (vx + vw / 2) - s * bcx
    ty = (vy + vh / 2) - s * bcy

    out = []
    out.append('<?xml version="1.0" encoding="UTF-8"?>')
    out.append(
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vx} {vy} {vw} {vh}" '
        f'width="{vw}" height="{vh}">'
    )
    out.append(f'  <rect x="{vx}" y="{vy}" width="{vw}" height="{vh}" '
               f'rx="{args.radius}" ry="{args.radius}" fill="{args.bg}"/>')
    out.append(f'  <g transform="translate({tx:.4f},{ty:.4f}) scale({s:.6f})">')
    for p in paths:
        out.append("    " + p)
    out.append("  </g>")
    out.append("</svg>")

    with open(args.out, "w", encoding="utf-8") as f:
        f.write("\n".join(out) + "\n")

    print(f"图标包围盒: x[{minx:.1f},{maxx:.1f}] y[{miny:.1f},{maxy:.1f}] "
          f"({bw:.1f}x{bh:.1f})  比例 {bw/bh:.3f}")
    print(f"缩放系数: {s:.6f}  平移: ({tx:.2f},{ty:.2f})")
    print(f"已写入 {args.out}")


if __name__ == "__main__":
    main()
