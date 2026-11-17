#!/usr/bin/env python3
"""Write tests/stdlib/cff_vectors.fern: test fonts for the CFF outlines of
std/truetype, in base64.

Run:  python3 scripts/gen-cff-vectors.py

It needs fontTools. Each font has 1000 units to the em and the glyphs
"O" (U+004F) and "L" (U+004C). The charstrings of the glyphs are written by
hand, so they use most operators of the Type 2 format: each line and curve
form, flex, hints with a hint mask, and calls of local and global
subroutines.

- cff():     an OpenType font with a CFF table.
- cid():     the same glyphs in a CID-keyed CFF table, with two font dicts
             and FDSelect.
- ttf():     the same outlines as quadratic TrueType curves. fontTools runs
             the charstrings and converts each cubic curve, to within 0.05
             of a unit. A test compares the bitmaps of the two.
- cff2():    the same glyphs in a CFF2 table, with a weight axis from 100
             to 900 and the default at 400. At 900 a blend makes the counter
             of "O" 50 units wider.
- ttc():     a collection of ttf() and cff(), in that order.
"""

import base64
import io
from pathlib import Path

from fontTools.cffLib import FDArrayIndex, FDSelect, FontDict, PrivateDict, SubrsIndex
from fontTools.fontBuilder import FontBuilder
from fontTools.misc.psCharStrings import T2CharString
from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.cu2quPen import Cu2QuPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTCollection, TTFont

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "tests" / "stdlib" / "cff_vectors.fern"
ORDER = [".notdef", "O", "L"]
CMAP = {0x4F: "O", 0x4C: "L"}
WIDTHS = {".notdef": 500, "O": 700, "L": 560}

# The subroutine numbers in a charstring are biased by 107 when a set has
# fewer than 1240 subroutines.
BIAS = 107

# Local subroutine 0: the counter of "O", a square drawn with hlineto and
# vlineto from the current point.
LOCAL = [[300, "hlineto", 300, "vlineto", -300, "hlineto", "return"]]

# The outer contour of "O" is a circle of radius 350 about (350, 350), as four
# arcs. Global subroutine 0 draws the second arc, then calls global
# subroutine 1 for the third.
GLOBAL = [
    [193, 157, 157, 193, "vhcurveto", 1 - BIAS, "callgsubr", "return"],
    [193, 157, -157, -193, "hvcurveto", "return"],
]

PROGRAMS = {
    ".notdef": ["endchar"],
    # The circle starts at (350, 0), and the counter at (200, 200).
    "O": [
        WIDTHS["O"] - 600, 100, 500, "hstem", 100, 500, "vstem",
        "hintmask", bytes([0b11000000]),
        350, 0, "rmoveto", -193, -157, 157, 193, "hvcurveto",
        0 - BIAS, "callgsubr",
        0, -193, -157, -157, -193, 0, "rrcurveto",
        -150, 200, "rmoveto", 0 - BIAS, "callsubr",
        "endchar",
    ],
    # An L with rlineto, and a second contour with rlinecurve, flex,
    # hhcurveto, vvcurveto and rcurveline.
    "L": [
        WIDTHS["L"] - 600, 60, 0, "rmoveto",
        100, 0, 0, 600, -100, 0, "rlineto",
        280, 120, "rmoveto",
        200, 0, 20, 10, 10, 20, 0, 20, "rlinecurve",
        -10, 20, -20, 10, -40, 0, -20, 0, -20, -20, -20, -40, 50, "flex",
        10, -30, -20, -40, -40, "hhcurveto",
        -10, -20, 10, -20, -20, "vvcurveto",
        10, -10, 20, -10, 20, 0, -30, 30, "rcurveline",
        "endchar",
    ],
}


# CFF2 has no width, no endchar and no return. At the weight 900 the region
# of the blend has its peak.
CFF2_GLOBAL = [[op for op in p if op != "return"] for p in GLOBAL]

CFF2_PROGRAMS = {
    ".notdef": [],
    "O": [
        100, 500, "hstem", 100, 500, "vstem",
        "hintmask", bytes([0b11000000]),
        350, 0, "rmoveto", -193, -157, 157, 193, "hvcurveto",
        0 - BIAS, "callgsubr",
        0, -193, -157, -157, -193, 0, "rrcurveto",
        -150, 200, "rmoveto", 300, 50, 1, "blend", "hlineto", 300, "vlineto", -300, "hlineto",
    ],
    "L": [op for op in PROGRAMS["L"][1:] if op != "endchar"],
}


def cff2_font() -> bytes:
    fb = base(True, {g: g for g in ORDER})
    fb.setupFvar([("wght", 100, 400, 900, "Weight")], [])
    fb.setupCFF2({g: T2CharString(program=list(CFF2_PROGRAMS[g])) for g in ORDER}, regions=[{"wght": (0, 1.0, 1.0)}])
    cff = fb.font["CFF2"].cff
    top = cff.topDictIndex[0]
    for g in ORDER:
        cs, _ = top.CharStrings.getItemAndSelector(g)
        cs.globalSubrs = cff.GlobalSubrs
    for p in CFF2_GLOBAL:
        cff.GlobalSubrs.append(T2CharString(program=list(p), private=top.FDArray[0].Private, globalSubrs=cff.GlobalSubrs))
    fb.setupPost()
    return built(fb)


def charstring(name, private, gsubrs):
    return T2CharString(program=list(PROGRAMS[name]), private=private, globalSubrs=gsubrs)


def subrs(programs, private, gsubrs):
    out = SubrsIndex()
    for p in programs:
        out.append(T2CharString(program=list(p), private=private, globalSubrs=gsubrs))
    return out


# The glyph names of a CID-keyed font are its CIDs, after ".notdef".
CIDS = {g: (g if i == 0 else "cid%05d" % i) for i, g in enumerate(ORDER)}


def base(cff_outlines: bool, names) -> FontBuilder:
    fb = FontBuilder(1000, isTTF=not cff_outlines)
    fb.setupGlyphOrder([names[g] for g in ORDER])
    fb.setupCharacterMap({cp: names[g] for cp, g in CMAP.items()})
    fb.setupHorizontalMetrics({names[g]: (WIDTHS[g], 0) for g in ORDER})
    fb.setupHorizontalHeader(ascent=800, descent=-200)
    fb.setupNameTable({"familyName": "FernCFFTest", "styleName": "Regular"})
    fb.setupOS2(sTypoAscender=800, usWinAscent=800, usWinDescent=200)
    return fb


def built(fb: FontBuilder) -> bytes:
    out = io.BytesIO()
    fb.save(out)
    return out.getvalue()


def cff_font(cid: bool) -> bytes:
    names = CIDS if cid else {g: g for g in ORDER}
    fb = base(True, names)
    placeholders = {names[g]: T2CharString(program=["endchar"]) for g in ORDER}
    fb.setupCFF("FernCFFTest", {"FullName": "FernCFFTest"}, placeholders, {"nominalWidthX": 600, "defaultWidthX": 500})
    fb.setupPost()
    cff = fb.font["CFF "].cff
    top = cff.topDictIndex[0]
    gsubrs = cff.GlobalSubrs
    if cid:
        # Two font dicts: ".notdef" and "L" use dict 0, and "O" uses dict 1,
        # the dict with the local subroutines.
        privates = []
        top.FDArray = FDArrayIndex()
        for _ in range(2):
            fd = FontDict()
            fd.setCFF2(False)
            pd = PrivateDict(strings=top.strings)
            pd.nominalWidthX = 600
            pd.defaultWidthX = 500
            fd.Private = pd
            top.FDArray.append(fd)
            privates.append(pd)
        top.ROS = ("Adobe", "Identity", 0)
        top.CIDCount = len(ORDER)
        top.FDSelect = FDSelect()
        top.FDSelect.format = 3
        top.FDSelect.gidArray = [0, 1, 0]
        del top.Private
        privates[1].Subrs = subrs(LOCAL, privates[1], gsubrs)
        chosen = {".notdef": privates[0], "O": privates[1], "L": privates[0]}
        cs = top.CharStrings
        cs.fdSelect = top.FDSelect
        cs.fdArray = top.FDArray
    else:
        private = top.Private
        private.Subrs = subrs(LOCAL, private, gsubrs)
        chosen = {g: private for g in ORDER}
        cs = top.CharStrings
    for p in GLOBAL:
        gsubrs.append(T2CharString(program=list(p), private=chosen["O"], globalSubrs=gsubrs))
    for g in ORDER:
        cs[names[g]] = charstring(g, chosen[g], gsubrs)
    return built(fb)


def ttf_font(cff_bytes: bytes) -> bytes:
    src = TTFont(io.BytesIO(cff_bytes))
    glyphs = src.getGlyphSet()
    fb = base(False, {g: g for g in ORDER})
    outlines = {}
    for g in ORDER:
        pen = TTGlyphPen(None)
        glyphs[g].draw(Cu2QuPen(pen, 0.05, reverse_direction=True))
        outlines[g] = pen.glyph()
    # The left side bearing of each glyph is the left edge of its outline,
    # so that fontBuilder keeps the outline where the charstring put it.
    lefts = {}
    for g in ORDER:
        bounds = BoundsPen(glyphs)
        glyphs[g].draw(bounds)
        lefts[g] = bounds.bounds[0] if bounds.bounds else 0
    fb.setupHorizontalMetrics({g: (WIDTHS[g], lefts[g]) for g in ORDER})
    fb.setupGlyf(outlines)
    fb.setupPost()
    return built(fb)


def ttc_font(fonts) -> bytes:
    coll = TTCollection()
    coll.fonts = [TTFont(io.BytesIO(f)) for f in fonts]
    out = io.BytesIO()
    coll.save(out)
    return out.getvalue()


def main() -> None:
    cff = cff_font(False)
    cid = cff_font(True)
    ttf = ttf_font(cff)
    cff2 = cff2_font()
    ttc = ttc_font([ttf, cff])
    parts = [
        "// Generated by scripts/gen-cff-vectors.py -- do not edit by hand.",
        "// Test fonts for the CFF outlines of std/truetype, in base64.",
        "",
    ]
    for name, data in [("cff", cff), ("cid", cid), ("ttf", ttf), ("cff2", cff2), ("ttc", ttc)]:
        parts.append("pub function %s(): string {" % name)
        parts.append('  return "%s";' % base64.b64encode(data).decode("ascii"))
        parts.append("}")
        parts.append("")
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
