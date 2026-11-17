#!/usr/bin/env python3
"""Write tests/stdlib/shape_vectors.fern: a subset of Noto Naskh Arabic and
the glyphs that HarfBuzz gives for a list of Arabic words, for the tests
of std/shape.

Run:  python3 scripts/gen-shape-vectors.py NotoNaskhArabic-Regular.ttf

It needs fontTools and uharfbuzz. The font comes from the Noto project
(notofonts.github.io, fonts/NotoNaskhArabic/hinted/ttf). It is under the
SIL Open Font License 1.1, and the subset keeps each name of the font, so
it keeps the notice and the license. The subset holds the space, the
letters and the marks from U+0621 to U+0655, and the GSUB and GPOS
features that std/shape applies.

Each case is the script, the text, the glyphs in logical order, the
cluster of each glyph (the index of its first code point), and the
position of each glyph: its advance, then its x and y offsets, in font
units. HarfBuzz shapes Arabic right to left, so the script reverses its
glyphs. The cases of the script "latn" use the default shaper of
HarfBuzz, with no joining forms, left to right.

The script also builds two fonts of its own, with no outlines:
lookup_font(), with a GSUB lookup of each type and format, and
position_font(), with a GPOS lookup of each type.
"""

import base64
import io
import sys
from pathlib import Path

import uharfbuzz as hb
from fontTools import subset
from fontTools.fontBuilder import FontBuilder
from fontTools.otlLib import builder as otl
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTFont, newTable
from fontTools.ttLib.tables import otTables as ot

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "tests" / "stdlib" / "shape_vectors.fern"
FEATURES = ["ccmp", "locl", "isol", "init", "medi", "fina", "rlig", "calt", "liga", "clig", "mset",
            "kern", "mark", "mkmk", "curs", "dist"]

WORDS = [
    "ب", "بب", "ببب", "باب", "بسم", "الله", "محمد", "لا", "سلام", "العربية",
    "كتاب", "مرحبا", "في", "هذا", "يوم", "شمس", "عين", "غرفة", "ظل", "لله",
    "مُحَمَّد", "كِتَابٌ", "السَّلَامُ", "ء", "آمن", "أنا", "إلى", "ئ", "ة", "لأن",
    "ب ب", "سلام عليكم",
    # Marks out of order: the shadda goes first, and the hamza above, a
    # modifier combining mark, goes before the fatha.
    "بِّ", "بَٔ", "سَُلم", "بٓٔ",
]

LATIN = ["بب", "الله", "لا", "مُحَمَّد"]


def subsetted(path: str) -> bytes:
    opts = subset.Options()
    opts.layout_features = FEATURES
    opts.name_IDs = ["*"]
    opts.drop_tables += ["DSIG"]
    opts.hinting = False
    font = TTFont(path)
    s = subset.Subsetter(opts)
    s.populate(unicodes=[0x20] + list(range(0x621, 0x656)))
    s.subset(font)
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


LETTERS = [chr(c) for c in range(ord("a"), ord("z") + 1)]
MARKS = {0x301: "acute", 0x300: "grave"}
MARK_GLYPHS = {"acute", "grave", "acute.ctx"}
LIGATURES = {"ab", "oo", "pq"}
DERIVED = ["ab", "oo", "pq", "c.alt1", "c.alt2", "h1", "h2", "d.ctx", "j.ctx", "k.ctx", "x.ext", "b.rev",
           "r.ctx", "s1", "s2", "s2.ctx", "t.ctx", "u.ctx", "acute.ctx"]

# The text of each case of lookup_font(), and what it tests.
LOOKUP_WORDS = [
    "c",                     # type 3: the first alternate
    "ab", "áb",        # type 4, with a mark that the lookup skips
    "h", "gh",               # type 2: a sequence, and no glyph
    "de", "dd",              # type 5, format 1
    "ij", "ji",              # type 5, format 2
    "xky", "ky",             # type 6, format 2, then type 7 on "x"
    "bbb",                   # type 8: from the last glyph to the first
    "òo", "óo",  # a mark attachment type in the lookup flags
    "pqr",                   # type 6, format 1: a ligature, then the glyph after it
    "st",                    # type 6, format 3: a sequence, then its second glyph
    "uv",                    # type 5, format 3
    "́à",         # the flag that skips base glyphs
]


def coverage(glyphs):
    c = ot.Coverage()
    c.glyphs = list(glyphs)
    return c


def classes(m):
    c = ot.ClassDef()
    c.classDefs = dict(m)
    return c


def record(seq, li):
    r = ot.SubstLookupRecord()
    r.SequenceIndex = seq
    r.LookupListIndex = li
    return r


def lookup(kind, subtables, flag=0):
    lk = ot.Lookup()
    lk.LookupType = kind
    lk.LookupFlag = flag
    lk.SubTable = subtables
    lk.SubTableCount = len(subtables)
    return lk


def subst(cls, fmt, **fields):
    st = cls()
    st.Format = fmt
    for k, v in fields.items():
        setattr(st, k, v)
    return st


def rule_set(set_name, name, rules):
    rs = getattr(ot, set_name)()
    setattr(rs, name, rules)
    setattr(rs, name + "Count", len(rules))
    return rs


def rule(name, records, **fields):
    r = getattr(ot, name)()
    for k, v in fields.items():
        setattr(r, k, v)
    r.SubstLookupRecord = records
    r.SubstCount = len(records)
    return r


def single(m):
    return lookup(1, [otl.buildSingleSubstSubtable(m)])


# The lookups in a feature, then the lookups that they apply, from 13.
def lookups():
    rev = subst(ot.ReverseChainSingleSubst, 1, Coverage=coverage(["b"]), BacktrackCoverage=[],
                LookAheadCoverage=[coverage(["b", "b.rev"])], Substitute=["b.rev"])
    ext = subst(ot.ExtensionSubst, 1, ExtensionLookupType=1, ExtSubTable=otl.buildSingleSubstSubtable({"x": "x.ext"}))
    class_sets = [None, None, rule_set("SubClassSet", "SubClassRule", [rule("SubClassRule", [record(1, 14)], GlyphCount=2, Class=[2])])]
    chain_sets = [None, rule_set("ChainSubClassSet", "ChainSubClassRule", [rule("ChainSubClassRule", [record(0, 15)], Backtrack=[1], Input=[], LookAhead=[1])])]
    top = [
        lookup(3, [otl.buildAlternateSubstSubtable({"c": ["c.alt1", "c.alt2"]})]),
        lookup(4, [otl.buildLigatureSubstSubtable({("a", "b"): "ab"})], 8),
        lookup(2, [otl.buildMultipleSubstSubtable({"h": ["h1", "h2"], "g": []})]),
        lookup(5, [subst(ot.ContextSubst, 1, Coverage=coverage(["d"]),
                         SubRuleSet=[rule_set("SubRuleSet", "SubRule", [rule("SubRule", [record(0, 13)], GlyphCount=2, Input=["e"])])])]),
        lookup(5, [subst(ot.ContextSubst, 2, Coverage=coverage(["i"]), ClassDef=classes({"i": 2, "j": 2}), SubClassSet=class_sets)]),
        lookup(6, [subst(ot.ChainContextSubst, 2, Coverage=coverage(["k"]), BacktrackClassDef=classes({"x": 1}),
                         InputClassDef=classes({"k": 1}), LookAheadClassDef=classes({"y": 1}), ChainSubClassSet=chain_sets)]),
        lookup(7, [ext]),
        lookup(8, [rev]),
        lookup(4, [otl.buildLigatureSubstSubtable({("o", "o"): "oo"})], 0x100),
        lookup(6, [subst(ot.ChainContextSubst, 1, Coverage=coverage(["p"]), ChainSubRuleSet=[rule_set("ChainSubRuleSet", "ChainSubRule", [
            rule("ChainSubRule", [record(0, 16), record(1, 17)], Backtrack=[], Input=["q", "r"], LookAhead=[])])])]),
        lookup(6, [subst(ot.ChainContextSubst, 3, BacktrackCoverage=[], InputCoverage=[coverage(["s"]), coverage(["t"])],
                         LookAheadCoverage=[], SubstLookupRecord=[record(0, 18), record(1, 19)], SubstCount=2)]),
        lookup(5, [subst(ot.ContextSubst, 3, Coverage=[coverage(["u"]), coverage(["v"])], GlyphCount=2,
                         SubstLookupRecord=[record(0, 20)], SubstCount=1)]),
        lookup(6, [subst(ot.ChainContextSubst, 3, BacktrackCoverage=[], InputCoverage=[coverage(["acute"])],
                         LookAheadCoverage=[coverage(["grave"])], SubstLookupRecord=[record(0, 21)], SubstCount=1)], 2),
    ]
    nested = [
        single({"d": "d.ctx"}),
        single({"j": "j.ctx"}),
        single({"k": "k.ctx"}),
        lookup(4, [otl.buildLigatureSubstSubtable({("p", "q"): "pq"})]),
        single({"r": "r.ctx"}),
        lookup(2, [otl.buildMultipleSubstSubtable({"s": ["s1", "s2"]})]),
        single({"s2": "s2.ctx", "t": "t.ctx"}),
        single({"u": "u.ctx"}),
        single({"acute": "acute.ctx"}),
    ]
    return top, nested


def gsub_table():
    top, nested = lookups()
    langsys = ot.DefaultLangSys()
    langsys.ReqFeatureIndex = 0xFFFF
    langsys.LookupOrder = None
    langsys.FeatureIndex = [0]
    langsys.FeatureCount = 1
    script = ot.Script()
    script.DefaultLangSys = langsys
    script.LangSysRecord = []
    script.LangSysCount = 0
    sr = ot.ScriptRecord()
    sr.ScriptTag = "DFLT"
    sr.Script = script
    feature = ot.Feature()
    feature.FeatureParams = None
    feature.LookupListIndex = list(range(len(top)))
    feature.LookupCount = len(top)
    fr = ot.FeatureRecord()
    fr.FeatureTag = "liga"
    fr.Feature = feature
    gsub = ot.GSUB()
    gsub.Version = 0x00010000
    gsub.ScriptList = ot.ScriptList()
    gsub.ScriptList.ScriptRecord = [sr]
    gsub.ScriptList.ScriptCount = 1
    gsub.FeatureList = ot.FeatureList()
    gsub.FeatureList.FeatureRecord = [fr]
    gsub.FeatureList.FeatureCount = 1
    gsub.LookupList = ot.LookupList()
    gsub.LookupList.Lookup = top + nested
    gsub.LookupList.LookupCount = len(top) + len(nested)
    t = newTable("GSUB")
    t.table = gsub
    return t


def gdef_table(order):
    gdef = ot.GDEF()
    gdef.Version = 0x00010000
    gdef.GlyphClassDef = classes({g: 3 if g in MARK_GLYPHS else 2 if g in LIGATURES else 1 for g in order[1:]})
    gdef.MarkAttachClassDef = classes({"acute": 1, "acute.ctx": 1, "grave": 2})
    gdef.AttachList = None
    gdef.LigCaretList = None
    t = newTable("GDEF")
    t.table = gdef
    return t


# A font with the letters "a" to "z", two marks, and the lookups above in
# the feature "liga".
def lookup_font() -> bytes:
    order = [".notdef"] + LETTERS + list(MARKS.values()) + DERIVED
    fb = FontBuilder(1000, isTTF=True)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap({**{ord(c): c for c in LETTERS}, **MARKS})
    empty = TTGlyphPen(None).glyph()
    fb.setupGlyf({g: empty for g in order})
    fb.setupHorizontalMetrics({g: (0 if g in MARK_GLYPHS else 500, 0) for g in order})
    fb.setupHorizontalHeader(ascent=800, descent=-200)
    fb.setupNameTable({"familyName": "FernGSUBTest", "styleName": "Regular"})
    fb.setupOS2()
    fb.setupPost()
    fb.font["GSUB"] = gsub_table()
    fb.font["GDEF"] = gdef_table(order)
    out = io.BytesIO()
    fb.save(out)
    return out.getvalue()


GPOS_MARKS = {0x301: "acute", 0x300: "grave"}
GPOS_ORDER = [".notdef"] + LETTERS + list(GPOS_MARKS.values()) + ["fi"]

# The text of each case of position_font(), and what it tests. Each case is
# shaped left to right as "latn" and right to left as "arab".
POSITION_WORDS = [
    "v",                      # type 1, format 1: a placement and an advance
    "wx",                     # type 1, format 2
    "av",                     # type 2, format 1, with a value for each glyph
    "to", "te", "ta",         # type 2, format 2: classes
    "cd", "ccd",              # type 3: an exit and an entry
    "á", "à",     # type 4: two mark classes
    "f́i", "fí",   # type 5: the mark of each part of a ligature
    "á̀",          # type 6: a mark on a mark
    "kk", "kkk",              # type 7, format 3
    "mnm", "nm",              # type 8, format 1
    "z",                      # type 9
    "ń",                # a mark with an advance, and no base anchor
]


def value(**fields):
    return otl.buildValue(fields)


def single_pos(fmt, glyph_values, vf):
    st = ot.SinglePos()
    st.Format = fmt
    st.Coverage = coverage(glyph_values)
    st.ValueFormat = vf
    if fmt == 1:
        st.Value = list(glyph_values.values())[0]
    else:
        st.Value = list(glyph_values.values())
        st.ValueCount = len(st.Value)
    return st


def mark_mark(marks, mark2s, gids):
    st = ot.MarkMarkPos()
    st.Format = 1
    count = 1 + max(c for c, _ in marks.values())
    st.Mark1Coverage = coverage(sorted(marks, key=gids.get))
    st.Mark1Array = otl.buildMarkArray(marks, gids)
    st.ClassCount = count
    order = sorted(mark2s, key=gids.get)
    st.Mark2Coverage = coverage(order)
    arr = ot.Mark2Array()
    arr.Mark2Record = [otl.buildMark2Record([mark2s[g].get(c) for c in range(count)]) for g in order]
    arr.Mark2Count = len(order)
    st.Mark2Array = arr
    return st


def pos_record(seq, li):
    r = ot.PosLookupRecord()
    r.SequenceIndex = seq
    r.LookupListIndex = li
    return r


# The GPOS lookups of position_font(): the lookups of the features, then
# the lookups that the contexts apply, from 11.
def position_lookups(gids):
    a = otl.buildAnchor
    ext = subst(ot.ExtensionPos, 1, ExtensionLookupType=1, ExtSubTable=single_pos(1, {"z": value(XPlacement=5)}, 1))
    chain_rule = ot.ChainPosRule()
    chain_rule.Backtrack = ["m"]
    chain_rule.Input = []
    chain_rule.LookAhead = ["m"]
    chain_rule.PosLookupRecord = [pos_record(0, 12)]
    chain_rule.PosCount = 1
    chain_set = ot.ChainPosRuleSet()
    chain_set.ChainPosRule = [chain_rule]
    chain_set.ChainPosRuleCount = 1
    marks = {"acute": (0, a(100, 700)), "grave": (1, a(50, 650))}
    top = [
        ("kern", lookup(1, [single_pos(1, {"v": value(XPlacement=10, YPlacement=20, XAdvance=30)}, 7)])),
        ("kern", lookup(1, [single_pos(2, {"w": value(XAdvance=40), "x": value(XAdvance=-50)}, 4)])),
        ("kern", lookup(2, [otl.buildPairPosGlyphsSubtable({("a", "v"): (value(XAdvance=-60), value(XPlacement=15))}, gids)])),
        ("kern", lookup(2, [otl.buildPairPosClassesSubtable({(("t",), ("o", "e")): (value(XAdvance=-70), None)}, gids)])),
        ("curs", lookup(3, [otl.buildCursivePosSubtable({"c": (a(0, 0), a(500, 100)), "d": (a(0, 50), a(500, 0))}, gids)])),
        ("mark", lookup(4, [otl.buildMarkBasePosSubtable(marks, {g: {0: a(250, 600), 1: a(240, 620)} for g in "aeo"}, gids)])),
        ("mark", lookup(5, [otl.buildMarkLigPosSubtable(marks, {"fi": [{0: a(150, 700), 1: a(140, 710)}, {0: a(400, 720), 1: a(390, 730)}]}, gids)])),
        ("mkmk", lookup(6, [mark_mark({"grave": (0, a(0, 0))}, {"acute": {0: a(100, 900)}}, gids)])),
        ("kern", lookup(7, [subst(ot.ContextPos, 3, Coverage=[coverage(["k"]), coverage(["k"])], GlyphCount=2,
                                 PosLookupRecord=[pos_record(1, 11)], PosCount=1)])),
        ("kern", lookup(8, [subst(ot.ChainContextPos, 1, Coverage=coverage(["n"]), ChainPosRuleSet=[chain_set])])),
        ("kern", lookup(9, [ext])),
    ]
    nested = [
        lookup(1, [single_pos(1, {"k": value(XAdvance=-100)}, 4)]),
        lookup(1, [single_pos(1, {"n": value(YPlacement=77)}, 2)]),
    ]
    return top, nested


def layout_table(tag, features, lookups):
    langsys = ot.DefaultLangSys()
    langsys.ReqFeatureIndex = 0xFFFF
    langsys.LookupOrder = None
    langsys.FeatureIndex = list(range(len(features)))
    langsys.FeatureCount = len(features)
    script = ot.Script()
    script.DefaultLangSys = langsys
    script.LangSysRecord = []
    script.LangSysCount = 0
    sr = ot.ScriptRecord()
    sr.ScriptTag = "DFLT"
    sr.Script = script
    records = []
    for ftag, indices in features:
        feature = ot.Feature()
        feature.FeatureParams = None
        feature.LookupListIndex = indices
        feature.LookupCount = len(indices)
        fr = ot.FeatureRecord()
        fr.FeatureTag = ftag
        fr.Feature = feature
        records.append(fr)
    t = getattr(ot, tag)()
    t.Version = 0x00010000
    t.ScriptList = ot.ScriptList()
    t.ScriptList.ScriptRecord = [sr]
    t.ScriptList.ScriptCount = 1
    t.FeatureList = ot.FeatureList()
    t.FeatureList.FeatureRecord = records
    t.FeatureList.FeatureCount = len(records)
    t.LookupList = ot.LookupList()
    t.LookupList.Lookup = lookups
    t.LookupList.LookupCount = len(lookups)
    table = newTable(tag)
    table.table = t
    return table


# A font with the letters "a" to "z", two marks, the ligature "fi", and a
# GPOS lookup of each type. "acute" has an advance of 200, so that the
# shaper takes its advance away.
def position_font() -> bytes:
    order = GPOS_ORDER
    gids = {g: i for i, g in enumerate(order)}
    fb = FontBuilder(1000, isTTF=True)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap({**{ord(c): c for c in LETTERS}, **GPOS_MARKS})
    empty = TTGlyphPen(None).glyph()
    fb.setupGlyf({g: empty for g in order})
    fb.setupHorizontalMetrics({g: (200 if g == "acute" else 0 if g == "grave" else 600 if g == "fi" else 500, 0) for g in order})
    fb.setupHorizontalHeader(ascent=800, descent=-200)
    fb.setupNameTable({"familyName": "FernGPOSTest", "styleName": "Regular"})
    fb.setupOS2()
    fb.setupPost()
    fb.font["GSUB"] = layout_table("GSUB", [("liga", [0])], [lookup(4, [otl.buildLigatureSubstSubtable({("f", "i"): "fi"})], 8)])
    top, nested = position_lookups(gids)
    tags = []
    for tag, _ in top:
        if tag not in tags:
            tags.append(tag)
    features = [(t, [i for i, (tag, _) in enumerate(top) if tag == t]) for t in tags]
    fb.font["GPOS"] = layout_table("GPOS", features, [lk for _, lk in top] + nested)
    gdef = ot.GDEF()
    gdef.Version = 0x00010000
    gdef.GlyphClassDef = classes({g: 3 if g in GPOS_MARKS.values() else 2 if g == "fi" else 1 for g in order[1:]})
    gdef.MarkAttachClassDef = None
    gdef.AttachList = None
    gdef.LigCaretList = None
    t = newTable("GDEF")
    t.table = gdef
    fb.font["GDEF"] = t
    out = io.BytesIO()
    fb.save(out)
    return out.getvalue()


def shaped(data: bytes, text: str, script: str):
    font = hb.Font(hb.Face(data))
    buf = hb.Buffer()
    buf.add_str(text)
    buf.direction = "rtl" if script == "Arab" else "ltr"
    buf.script = script
    buf.language = "ar"
    hb.shape(font, buf, {})
    infos = list(buf.glyph_infos)
    positions = list(buf.glyph_positions)
    if script == "Arab":
        infos.reverse()
        positions.reverse()
    # add_str gives each code point its index as its cluster.
    places = ["%d:%d:%d" % (p.x_advance, p.x_offset, p.y_offset) for p in positions]
    return [i.codepoint for i in infos], [i.cluster for i in infos], places


def case(data: bytes, text: str, script: str) -> str:
    glyphs, clusters, places = shaped(data, text, script.capitalize())
    return "%s\t%s\t%s\t%s\t%s" % (script, text, ",".join(map(str, glyphs)), ",".join(map(str, clusters)), ",".join(places))


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    data = subsetted(sys.argv[1])
    lines = []
    for script, words in [("arab", WORDS), ("latn", LATIN)]:
        for w in words:
            lines.append(case(data, w, script))
    built = lookup_font()
    built_lines = [case(built, w, "latn") for w in LOOKUP_WORDS]
    placed = position_font()
    placed_lines = [case(placed, w, script) for script in ["latn", "arab"] for w in POSITION_WORDS]
    parts = [
        "// Generated by scripts/gen-shape-vectors.py -- do not edit by hand.",
        "// A subset of Noto Naskh Arabic, Copyright 2022 The Noto Project Authors",
        "// (https://github.com/notofonts/arabic), under the SIL Open Font License",
        "// 1.1. The names of the font hold the notice and the license.",
        "",
        "pub function font(): string {",
        '  return "%s";' % base64.b64encode(data).decode("ascii"),
        "}",
        "",
        "// One case on each line, split by tabs: the script, the text, and the",
        "// glyphs, the clusters and the positions that HarfBuzz gives, in logical",
        "// order. A position is the advance, the x offset and the y offset.",
        "pub function cases(): string {",
        '  return "%s";' % "\\n".join(l.replace("\t", "\\t") for l in lines),
        "}",
        "",
        "// A font with a GSUB lookup of each type and format, and no outlines.",
        "pub function lookup_font(): string {",
        '  return "%s";' % base64.b64encode(built).decode("ascii"),
        "}",
        "",
        "// The cases of lookup_font(), as those of cases().",
        "pub function lookup_cases(): string {",
        '  return "%s";' % "\\n".join(l.replace("\t", "\\t") for l in built_lines),
        "}",
        "",
        "// A font with a GPOS lookup of each type, and no outlines.",
        "pub function position_font(): string {",
        '  return "%s";' % base64.b64encode(placed).decode("ascii"),
        "}",
        "",
        "// The cases of position_font(), as those of cases().",
        "pub function position_cases(): string {",
        '  return "%s";' % "\\n".join(l.replace("\t", "\\t") for l in placed_lines),
        "}",
        "",
    ]
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
