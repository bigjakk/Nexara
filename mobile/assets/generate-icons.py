#!/usr/bin/env python3
"""
Minimal Nexara icon generator — pure stdlib (no PIL/ImageMagick required).

Generates a placeholder icon set that uses the Nexara brand colors
(#22c55e green on #0a0a0a dark) with a stylized "N" glyph. The glyph is
hand-drawn as a pixel bitmap because we don't have a font renderer
available in the dev environment.

This is a **placeholder** set — it's just a solid dark square with a green
"N" shape. A real designer should replace these with a proper logo before
shipping to real users. But it's enough to stop showing the default React
Native launcher icon and give the app visual identity on the home screen.

Outputs:
  assets/icon.png               1024x1024  — full-bleed launcher icon (iOS + legacy Android)
  assets/adaptive-icon.png      1024x1024  — Android adaptive icon foreground (transparent bg)
  assets/splash.png             1242x2436  — splash screen image (letter centered)
  assets/favicon.png              64x64    — web favicon

Re-run any time the brand colors or glyph change. Commit the output PNGs.
"""

import os
import struct
import zlib

GREEN = (0x22, 0xC5, 0x5E)
DARK = (0x0A, 0x0A, 0x0A)
WHITE = (0xFA, 0xFA, 0xFA)

ASSETS = os.path.dirname(os.path.abspath(__file__))


def write_png(path: str, width: int, height: int, pixels: bytes) -> None:
    """
    Write a PNG file. pixels is a raw RGBA byte string of length width*height*4
    in row-major order.
    """
    assert len(pixels) == width * height * 4, (
        f"pixel buffer size mismatch: got {len(pixels)}, expected {width * height * 4}"
    )

    def chunk(tag: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    # PNG spec: each row must be prefixed with a filter byte (0 = None).
    raw = bytearray()
    stride = width * 4
    for y in range(height):
        raw.append(0)
        raw += pixels[y * stride : (y + 1) * stride]

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)  # 8bit, RGBA
    idat = zlib.compress(bytes(raw), 9)

    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n")
        f.write(chunk(b"IHDR", ihdr))
        f.write(chunk(b"IDAT", idat))
        f.write(chunk(b"IEND", b""))


class Canvas:
    def __init__(self, width: int, height: int, bg: tuple[int, int, int, int]):
        self.width = width
        self.height = height
        self.buf = bytearray()
        r, g, b, a = bg
        row = bytes([r, g, b, a]) * width
        for _ in range(height):
            self.buf += row

    def _idx(self, x: int, y: int) -> int:
        return (y * self.width + x) * 4

    def put(self, x: int, y: int, rgba: tuple[int, int, int, int]) -> None:
        if 0 <= x < self.width and 0 <= y < self.height:
            i = self._idx(x, y)
            self.buf[i : i + 4] = bytes(rgba)

    def fill_rect(
        self,
        x0: int,
        y0: int,
        x1: int,
        y1: int,
        rgba: tuple[int, int, int, int],
    ) -> None:
        x0 = max(0, x0)
        y0 = max(0, y0)
        x1 = min(self.width, x1)
        y1 = min(self.height, y1)
        if x0 >= x1 or y0 >= y1:
            return
        span = bytes(rgba) * (x1 - x0)
        for y in range(y0, y1):
            i = self._idx(x0, y)
            self.buf[i : i + len(span)] = span

    def fill_rounded_rect(
        self,
        x0: int,
        y0: int,
        x1: int,
        y1: int,
        r: int,
        rgba: tuple[int, int, int, int],
    ) -> None:
        # Main body
        self.fill_rect(x0 + r, y0, x1 - r, y1, rgba)
        self.fill_rect(x0, y0 + r, x0 + r, y1 - r, rgba)
        self.fill_rect(x1 - r, y0 + r, x1, y1 - r, rgba)
        # Corners — quarter circles
        for dy in range(r):
            for dx in range(r):
                dist_sq = (r - dx - 0.5) ** 2 + (r - dy - 0.5) ** 2
                if dist_sq <= r * r:
                    self.put(x0 + dx, y0 + dy, rgba)
                    self.put(x1 - 1 - dx, y0 + dy, rgba)
                    self.put(x0 + dx, y1 - 1 - dy, rgba)
                    self.put(x1 - 1 - dx, y1 - 1 - dy, rgba)

    def fill_parallelogram(
        self,
        x0: int,
        y0: int,
        width: int,
        height: int,
        shear: int,
        rgba: tuple[int, int, int, int],
    ) -> None:
        """Vertical parallelogram for the diagonal stroke of the N."""
        for dy in range(height):
            t = dy / max(1, height - 1)
            offset = int(shear * t)
            self.fill_rect(x0 + offset, y0 + dy, x0 + offset + width, y0 + dy + 1, rgba)

    def bytes(self) -> bytes:
        return bytes(self.buf)


def draw_n_glyph(
    c: Canvas,
    cx: int,
    cy: int,
    size: int,
    color: tuple[int, int, int, int],
) -> None:
    """
    Draw a stylized 'N' centered at (cx, cy) that fits in a `size`x`size` box.
    Proportions are hand-tuned to look chunky + legible at the launcher
    size. Three strokes: left bar, right bar, diagonal.
    """
    half = size // 2
    x0, y0 = cx - half, cy - half
    x1, y1 = cx + half, cy + half

    bar_w = size // 5
    # Left vertical bar
    c.fill_rect(x0, y0, x0 + bar_w, y1, color)
    # Right vertical bar
    c.fill_rect(x1 - bar_w, y0, x1, y1, color)
    # Diagonal — sheared parallelogram connecting top-left inside of left bar
    # to bottom-right inside of right bar.
    inner_w = size - 2 * bar_w
    c.fill_parallelogram(
        x0 + bar_w,
        y0,
        bar_w,
        size,
        inner_w - bar_w,
        color,
    )


def make_icon(width: int, height: int) -> Canvas:
    """Full-bleed dark square with a rounded green tile containing the N."""
    c = Canvas(width, height, DARK + (255,))
    margin = width // 16
    r = width // 8
    c.fill_rounded_rect(
        margin,
        margin,
        width - margin,
        height - margin,
        r,
        GREEN + (255,),
    )
    glyph = int(width * 0.45)
    draw_n_glyph(c, width // 2, height // 2, glyph, DARK + (255,))
    return c


def make_adaptive_foreground(width: int, height: int) -> Canvas:
    """Transparent background, centered N on a smaller tile (66% safe area)."""
    c = Canvas(width, height, (0, 0, 0, 0))
    safe = int(width * 0.60)
    x0 = (width - safe) // 2
    y0 = (height - safe) // 2
    r = safe // 8
    c.fill_rounded_rect(
        x0,
        y0,
        x0 + safe,
        y0 + safe,
        r,
        GREEN + (255,),
    )
    glyph = int(safe * 0.55)
    draw_n_glyph(c, width // 2, height // 2, glyph, DARK + (255,))
    return c


def make_splash(width: int, height: int) -> Canvas:
    """Dark background with a centered green N glyph (no tile)."""
    c = Canvas(width, height, DARK + (255,))
    glyph = int(min(width, height) * 0.20)
    draw_n_glyph(c, width // 2, height // 2, glyph, GREEN + (255,))
    return c


def main() -> None:
    os.makedirs(ASSETS, exist_ok=True)

    c = make_icon(1024, 1024)
    write_png(os.path.join(ASSETS, "icon.png"), 1024, 1024, c.bytes())

    c = make_adaptive_foreground(1024, 1024)
    write_png(os.path.join(ASSETS, "adaptive-icon.png"), 1024, 1024, c.bytes())

    c = make_splash(1242, 2436)
    write_png(os.path.join(ASSETS, "splash.png"), 1242, 2436, c.bytes())

    c = make_icon(64, 64)
    write_png(os.path.join(ASSETS, "favicon.png"), 64, 64, c.bytes())

    print("Wrote:")
    for name in ("icon.png", "adaptive-icon.png", "splash.png", "favicon.png"):
        path = os.path.join(ASSETS, name)
        print(f"  {path}  ({os.path.getsize(path)} bytes)")


if __name__ == "__main__":
    main()
