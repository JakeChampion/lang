#!/usr/bin/env python3
"""Write tests/stdlib/color_vectors.fern: test fonts for the color glyphs
of std/truetype, in base64, and what each glyph must show.

Run:  python3 scripts/gen-color-vectors.py NotoColorEmoji-Regular.ttf

It needs fontTools. The fonts have 1000 units to the em and two outlines:
"sq", a square from 0 to 1000, and "half", its left half.

- v0():     a COLR table of version 0: glyph "A" is "sq" in red under
            "half" in blue.
- v1():     a COLR table of version 1, with a glyph for each kind of paint.
            CASES names the letter of each one.
- boxes():  for each glyph of v1() that moves "half", the box that the
            moved "half" covers, in font units, from the transforms that
            fontTools gives.
- emoji():  a subset of Noto Color Emoji with U+1F600 alone. Noto Color
            Emoji is under the SIL Open Font License 1.1, and the subset
            keeps each name of the font.
"""

import base64
import io
import sys
from pathlib import Path

from fontTools import subset
from fontTools.colorLib import builder as cb
from fontTools.fontBuilder import FontBuilder
from fontTools.misc.transform import Transform
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTFont

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "tests" / "stdlib" / "color_vectors.fern"

RED, BLUE, GREEN, GRAY, WHITE, BLACK = range(6)
PALETTE = [(1, 0, 0, 1), (0, 0, 1, 1), (0, 1, 0, 1), (0.5, 0.5, 0.5, 1), (1, 1, 1, 1), (0, 0, 0, 1)]


def solid(index, alpha=1.0):
    return {"Format": 2, "PaletteIndex": index, "Alpha": alpha}


def glyph(name, paint):
    return {"Format": 10, "Glyph": name, "Paint": paint}


def line(*stops, extend="pad"):
    return {"Extend": extend, "ColorStop": [{"StopOffset": o, "PaletteIndex": i, "Alpha": 1.0} for o, i in stops]}


GREEN_HALF = glyph("half", solid(GREEN))

# The letter of each glyph of v1(), and its paint.
CASES = {
    "B": glyph("sq", solid(RED)),
    "C": {"Format": 1, "Layers": [glyph("sq", solid(BLUE)), glyph("half", solid(RED, 0.5))]},
    "D": glyph("sq", {"Format": 4, "ColorLine": line((0, RED), (1, BLUE)), "x0": 0, "y0": 0, "x1": 1000, "y1": 0, "x2": 0, "y2": 1000}),
    "E": glyph("sq", {"Format": 6, "ColorLine": line((0, WHITE), (1, BLACK)), "x0": 500, "y0": 500, "r0": 0, "x1": 500, "y1": 500, "r1": 500}),
    "F": glyph("sq", {"Format": 8, "ColorLine": line((0, RED), (1, BLUE)), "centerX": 500, "centerY": 500, "startAngle": 0, "endAngle": 360}),
    "G": {"Format": 14, "dx": 500, "dy": 0, "Paint": GREEN_HALF},
    "H": {"Format": 18, "scaleX": 0.5, "scaleY": 0.5, "centerX": 500, "centerY": 500, "Paint": GREEN_HALF},
    "I": {"Format": 26, "angle": 90, "centerX": 500, "centerY": 500, "Paint": GREEN_HALF},
    "J": {"Format": 30, "xSkewAngle": 20, "ySkewAngle": 0, "centerX": 0, "centerY": 0, "Paint": GREEN_HALF},
    "K": {"Format": 12, "Transform": {"xx": 1, "yx": 0, "xy": 0, "yy": 0.5, "dx": 0, "dy": 500}, "Paint": GREEN_HALF},
    "L": {"Format": 32, "CompositeMode": "src_in", "SourcePaint": glyph("sq", solid(RED)), "BackdropPaint": glyph("half", solid(BLUE))},
    "M": {"Format": 32, "CompositeMode": "multiply", "SourcePaint": glyph("sq", solid(GRAY)), "BackdropPaint": glyph("sq", solid(RED))},
    "N": {"Format": 11, "Glyph": "B"},
    "O": glyph("sq", solid(0xFFFF)),
    "P": {"Format": 32, "CompositeMode": "soft_light", "SourcePaint": glyph("sq", solid(GRAY)), "BackdropPaint": glyph("sq", solid(RED))},
    "Q": {"Format": 32, "CompositeMode": "dest_out", "SourcePaint": glyph("half", solid(RED)), "BackdropPaint": glyph("sq", solid(BLUE))},
    "R": glyph("sq", {"Format": 4, "ColorLine": line((0, RED), (1, BLUE), extend="repeat"), "x0": 0, "y0": 0, "x1": 500, "y1": 0, "x2": 0, "y2": 1000}),
    "S": glyph("sq", {"Format": 4, "ColorLine": line((0, RED), (1, BLUE), extend="reflect"), "x0": 0, "y0": 0, "x1": 500, "y1": 0, "x2": 0, "y2": 1000}),
}
MOVED = "GHIJK"


def outlines():
    def rect(x0, y0, x1, y1):
        pen = TTGlyphPen(None)
        pen.moveTo((x0, y0))
        pen.lineTo((x0, y1))
        pen.lineTo((x1, y1))
        pen.lineTo((x1, y0))
        pen.closePath()
        return pen.glyph()
    return {"sq": rect(0, 0, 1000, 1000), "half": rect(0, 0, 500, 1000)}


def base(letters) -> FontBuilder:
    order = [".notdef", "sq", "half"] + list(letters)
    fb = FontBuilder(1000, isTTF=True)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap({ord(c): c for c in letters})
    shapes = outlines()
    empty = TTGlyphPen(None).glyph()
    fb.setupGlyf({g: shapes.get(g, empty) for g in order})
    fb.setupHorizontalMetrics({g: (1000, 0) for g in order})
    fb.setupHorizontalHeader(ascent=1000, descent=0)
    fb.setupNameTable({"familyName": "FernColorTest", "styleName": "Regular"})
    fb.setupOS2(sTypoAscender=1000, usWinAscent=1000, usWinDescent=0)
    fb.setupPost()
    fb.font["CPAL"] = cb.buildCPAL([PALETTE])
    return fb


def saved(font: TTFont) -> bytes:
    font["head"].created = font["head"].modified = 0x7C259DC0
    font.recalcTimestamp = False
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


def v0() -> bytes:
    fb = base("A")
    fb.font["COLR"] = cb.buildCOLR({"A": [("sq", RED), ("half", BLUE)]}, version=0)
    return saved(fb.font)


def v1():
    fb = base(CASES.keys())
    fb.font["COLR"] = cb.buildCOLR(CASES, version=1, glyphMap=fb.font.getReverseGlyphMap(), clipBoxes={c: (0, 0, 1000, 1000) for c in CASES})
    data = saved(fb.font)
    font = TTFont(io.BytesIO(data))
    boxes = []
    for rec in font["COLR"].table.BaseGlyphList.BaseGlyphPaintRecord:
        if rec.BaseGlyph in MOVED:
            t = rec.Paint.getTransform()
            pts = [t.transformPoint(p) for p in [(0, 0), (500, 0), (0, 1000), (500, 1000)]]
            xs, ys = [p[0] for p in pts], [p[1] for p in pts]
            boxes.append("%s %d %d %d %d" % (rec.BaseGlyph, min(xs), min(ys), max(xs), max(ys)))
    return data, boxes


def emoji(path: str) -> bytes:
    opts = subset.Options()
    opts.name_IDs = ["*"]
    opts.drop_tables += ["DSIG"]
    opts.hinting = False
    font = TTFont(path)
    for tag in ["SVG ", "GSUB"]:
        if tag in font:
            del font[tag]
    s = subset.Subsetter(opts)
    s.populate(unicodes=[0x1F600])
    s.subset(font)
    return saved(font)


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    data, boxes = v1()
    parts = [
        "// Generated by scripts/gen-color-vectors.py -- do not edit by hand.",
        "// emoji() is a subset of Noto Color Emoji, Copyright 2021 Google Inc.,",
        "// under the SIL Open Font License 1.1. Its names hold the notice and the",
        "// license.",
        "",
    ]
    for name, value in [("v0", v0()), ("v1", data), ("emoji", emoji(sys.argv[1]))]:
        parts += ["pub function %s(): string {" % name, '  return "%s";' % base64.b64encode(value).decode("ascii"), "}", ""]
    parts += [
        "// One glyph of v1() on each line: its letter and the box of the moved",
        "// half in font units: x0 y0 x1 y1.",
        "pub function boxes(): string {",
        '  return "%s";' % "\\n".join(boxes),
        "}",
        "",
    ]
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
