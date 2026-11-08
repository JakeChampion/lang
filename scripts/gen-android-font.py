#!/usr/bin/env python3
"""Writes internal/stdlib/std/android/font.fern from DejaVu Sans Mono.

Usage: gen-android-font.py [FONT.ttf]

The module holds the printable ASCII glyphs, from 32 to 126, the Latin-1
Supplement and Latin Extended-A letters, from 160 to 383, and the punctuation
in EXTRA, as a raster at 24 pixels: one cell of CELL_W by CELL_H pixels for
each glyph, with a 4-bit coverage for each pixel. The glyphs are a modified copy of the font, so the
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
# The punctuation that titles and summaries use beyond Latin-1: the dashes,
# the curly quotes, the bullet and the ellipsis.
EXTRA = [0x2013, 0x2014, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2026]
CODES = list(range(32, 127)) + list(range(160, 384)) + EXTRA


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
    for c in CODES:
        img = Image.new("L", (cell_w, cell_h), 0)
        ImageDraw.Draw(img).text((0, 0), chr(c), font=font, fill=255)
        hexes.append("".join("%x" % (img.getpixel((x, y)) >> 4) for y in range(cell_h) for x in range(cell_w)))
    head = [
        "// The Fern raster of DejaVu Sans Mono: the printable ASCII, Latin-1 and Latin",
        "// Extended-A glyphs and some punctuation at " + str(SIZE) + " pixels, for text on an Android",
        "// frame. scripts/gen-android-font.py writes this file. Do not edit it.",
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
        "// glyph in the order of glyph_index and row by row.",
        "const GLYPHS: string = \"" + "".join(hexes) + "\";",
        "",
        "// The punctuation after the Latin letters, in the order of its glyphs.",
        "const EXTRA: i32[] = [" + ", ".join(str(c) for c in EXTRA) + "];",
        "",
        "// The glyph of code point `c`: printable ASCII from 0, the Latin letters from",
        "// 95, and EXTRA from 319. Any other code point has the glyph of \"?\".",
        "pub function glyph_index(c: i32): i32 {",
        "  if (c >= 32 && c <= 126) {",
        "    return c - 32;",
        "  }",
        "  if (c >= 160 && c <= 383) {",
        "    return c - 65;",
        "  }",
        "  for i in 0..EXTRA.len() {",
        "    if (EXTRA[i] == c) {",
        "      return 319 + i;",
        "    }",
        "  }",
        "  return 31;",
        "}",
        "",
        "// The coverage of pixel x, y of the glyph of code point `c`, from 0 to 15.",
        "pub function coverage(c: i32, x: i32, y: i32): i32 {",
        "  return glyph_coverage(glyph_index(c), x, y);",
        "}",
        "",
        "// The coverage of pixel x, y of glyph `g` of glyph_index, from 0 to 15.",
        "pub function glyph_coverage(g: i32, x: i32, y: i32): i32 {",
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
