#!/usr/bin/env python3
"""Writes internal/stdlib/std/android/font.fern from DejaVu Sans Mono.

Usage: gen-android-font.py [FONT.ttf]

The module holds the printable ASCII glyphs, from 32 to 126, as a raster at
24 pixels: one cell of CELL_W by CELL_H pixels for each glyph, with a 4-bit
coverage for each pixel. The glyphs are a modified copy of the font, so the
module carries the copyright and permission notice of the font, and the
raster has its own name, as the license requires.
"""

import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
SIZE = 24
NOTICE = Path("/usr/share/doc/fonts-dejavu-core/copyright")
OUT = Path(__file__).resolve().parent.parent / "internal/stdlib/std/android/font.fern"


def license_lines():
    """The copyright and the permission notice from the Debian copyright file."""
    text = NOTICE.read_text().split("\n\nFiles: debian/*")[0]
    out = []
    for line in text.splitlines():
        if line.startswith(("Format:", "Upstream-", "Source:", "Files:")) or line.startswith("                  see"):
            continue
        line = line.removeprefix("Copyright: ").strip()
        out.append("" if line == "." else line)
    while out and out[0] == "":
        out.pop(0)
    return out


def main(path):
    font = ImageFont.truetype(path, SIZE)
    ascent, descent = font.getmetrics()
    cell_w = max(font.getbbox(chr(c))[2] for c in range(32, 127))
    cell_h = ascent + descent
    hexes = []
    for c in range(32, 127):
        img = Image.new("L", (cell_w, cell_h), 0)
        ImageDraw.Draw(img).text((0, 0), chr(c), font=font, fill=255)
        hexes.append("".join("%x" % (img.getpixel((x, y)) >> 4) for y in range(cell_h) for x in range(cell_w)))
    head = [
        "// The Fern raster of DejaVu Sans Mono: the printable ASCII glyphs at " + str(SIZE) + " pixels,",
        "// for text on an Android frame. scripts/gen-android-font.py writes this file. Do",
        "// not edit it.",
        "//",
        "// The glyphs come from DejaVu Sans Mono, under this notice:",
        "//",
    ] + [("// " + line).rstrip() for line in license_lines()]
    lines = head + [
        "",
        "pub const CELL_W: i32 = " + str(cell_w) + ";",
        "",
        "pub const CELL_H: i32 = " + str(cell_h) + ";",
        "",
        "// The baseline, in pixels from the top of a cell.",
        "pub const ASCENT: i32 = " + str(ascent) + ";",
        "",
        "// The 4-bit coverage of each pixel of each glyph, as one hex digit, glyph by",
        "// glyph from character 32 and row by row.",
        "const GLYPHS: string = \"" + "".join(hexes) + "\";",
        "",
        "// The coverage of pixel x, y of the glyph of character `c`, from 0 to 15. A",
        "// character outside 32 to 126 draws as \"?\".",
        "pub function coverage(c: i32, x: i32, y: i32): i32 {",
        "  let g: i32 = c - 32;",
        "  if (g < 0 || g > 94) {",
        "    g = 31;",
        "  }",
        "  let d: u8 = GLYPHS[(g * CELL_H + y) * CELL_W + x];",
        "  if (d >= b'a') {",
        "    return (d - b'a') as i32 + 10;",
        "  }",
        "  return (d - b'0') as i32;",
        "}",
        "",
    ]
    OUT.write_text("\n".join(lines))
    print(f"{OUT}: {cell_w}x{cell_h} cells")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else FONT)
