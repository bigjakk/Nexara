#!/usr/bin/env python3
"""
PWA icon generator for the web frontend — pure stdlib, no PIL required.

Adapted from mobile/assets/generate-icons.py (same placeholder brand
design: #22c55e green tile + stylized dark "N"). Like that one, this is a
placeholder set until a real logo exists — but it satisfies the manifest
icon requirements (192 + 512 + maskable) for add-to-home-screen installs.

Outputs (committed, referenced by public/manifest.webmanifest):
  public/icons/icon-192.png            192x192  dark square, green tile, N
  public/icons/icon-512.png            512x512  same
  public/icons/icon-maskable-512.png   512x512  full-bleed green (crop-safe)
  public/icons/apple-touch-icon.png    180x180  same as icon (iOS rounds it)

Re-run from frontend/: python3 scripts/generate-pwa-icons.py
"""

import os
import struct
import zlib

GREEN = (0x22, 0xC5, 0x5E)
DARK = (0x0A, 0x0A, 0x0A)

OUT_DIR = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "public", "icons"
)


def write_png(path: str, width: int, height: int, pixels: bytes) -> None:
    def chunk(tag: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    raw = bytearray()
    stride = width * 4
    for y in range(height):
        raw.append(0)  # filter byte: None
        raw += pixels[y * stride : (y + 1) * stride]

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)  # 8bit RGBA
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
        self.buf = bytearray(bytes(bg) * (width * height))

    def _idx(self, x: int, y: int) -> int:
        return (y * self.width + x) * 4

    def put(self, x: int, y: int, rgba: tuple[int, int, int, int]) -> None:
        if 0 <= x < self.width and 0 <= y < self.height:
            i = self._idx(x, y)
            self.buf[i : i + 4] = bytes(rgba)

    def fill_rect(self, x0: int, y0: int, x1: int, y1: int, rgba) -> None:
        x0, y0 = max(0, x0), max(0, y0)
        x1, y1 = min(self.width, x1), min(self.height, y1)
        if x0 >= x1 or y0 >= y1:
            return
        span = bytes(rgba) * (x1 - x0)
        for y in range(y0, y1):
            i = self._idx(x0, y)
            self.buf[i : i + len(span)] = span

    def fill_rounded_rect(self, x0, y0, x1, y1, r, rgba) -> None:
        self.fill_rect(x0 + r, y0, x1 - r, y1, rgba)
        self.fill_rect(x0, y0 + r, x0 + r, y1 - r, rgba)
        self.fill_rect(x1 - r, y0 + r, x1, y1 - r, rgba)
        for dy in range(r):
            for dx in range(r):
                if (r - dx - 0.5) ** 2 + (r - dy - 0.5) ** 2 <= r * r:
                    self.put(x0 + dx, y0 + dy, rgba)
                    self.put(x1 - 1 - dx, y0 + dy, rgba)
                    self.put(x0 + dx, y1 - 1 - dy, rgba)
                    self.put(x1 - 1 - dx, y1 - 1 - dy, rgba)

    def fill_parallelogram(self, x0, y0, width, height, shear, rgba) -> None:
        for dy in range(height):
            t = dy / max(1, height - 1)
            offset = int(shear * t)
            self.fill_rect(x0 + offset, y0 + dy, x0 + offset + width, y0 + dy + 1, rgba)

    def bytes(self) -> bytes:
        return bytes(self.buf)


def draw_n_glyph(c: Canvas, cx: int, cy: int, size: int, color) -> None:
    half = size // 2
    x0, y0 = cx - half, cy - half
    x1, y1 = cx + half, cy + half
    bar_w = size // 5
    c.fill_rect(x0, y0, x0 + bar_w, y1, color)
    c.fill_rect(x1 - bar_w, y0, x1, y1, color)
    inner_w = size - 2 * bar_w
    c.fill_parallelogram(x0 + bar_w, y0, bar_w, size, inner_w - bar_w, color)


def make_icon(size: int) -> Canvas:
    """Dark square with a rounded green tile containing the N."""
    c = Canvas(size, size, DARK + (255,))
    margin = size // 16
    c.fill_rounded_rect(margin, margin, size - margin, size - margin, size // 8, GREEN + (255,))
    draw_n_glyph(c, size // 2, size // 2, int(size * 0.45), DARK + (255,))
    return c


def make_maskable(size: int) -> Canvas:
    """Full-bleed green so circular/squircle masks never crop into the tile;
    glyph stays inside the 80% safe zone."""
    c = Canvas(size, size, GREEN + (255,))
    draw_n_glyph(c, size // 2, size // 2, int(size * 0.40), DARK + (255,))
    return c


def main() -> None:
    os.makedirs(OUT_DIR, exist_ok=True)
    for name, canvas in [
        ("icon-192.png", make_icon(192)),
        ("icon-512.png", make_icon(512)),
        ("icon-maskable-512.png", make_maskable(512)),
        ("apple-touch-icon.png", make_icon(180)),
    ]:
        path = os.path.join(OUT_DIR, name)
        write_png(path, canvas.width, canvas.height, canvas.bytes())
        print(f"wrote {path}")


if __name__ == "__main__":
    main()
