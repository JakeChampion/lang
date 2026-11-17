#!/usr/bin/env python3
"""Write tests/stdlib/shape_vectors.fern: a subset of Noto Naskh Arabic and
the glyphs that HarfBuzz gives for a list of Arabic words, for the tests
of std/shape.

Run:  python3 scripts/gen-shape-vectors.py NotoNaskhArabic-Regular.ttf

It needs fontTools and uharfbuzz. The font comes from the Noto project
(notofonts.github.io, fonts/NotoNaskhArabic/hinted/ttf). It is under the
SIL Open Font License 1.1, and the subset keeps each name of the font, so
it keeps the notice and the license. The subset holds the space, the
letters and the marks from U+0621 to U+0655, and the GSUB features that
std/shape applies. It has no GPOS table.

Each case is the script, the text, the glyphs in logical order, and the
cluster of each glyph: the index of its first code point. HarfBuzz shapes
Arabic right to left, so the script reverses its glyphs. The cases of the
script "latn" use the default shaper of HarfBuzz, with no joining forms,
left to right.

The script also builds a font of its own, lookup_font(), with a lookup of
each type and format of GSUB, and the cases of that font. Its glyphs have
no outlines.
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
FEATURES = ["ccmp", "locl", "isol", "init", "medi", "fina", "rlig", "calt", "liga", "clig", "mset"]

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
    opts.drop_tables += ["GPOS", "DSIG"]
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


def shaped(data: bytes, text: str, script: str):
    font = hb.Font(hb.Face(data))
    buf = hb.Buffer()
    buf.add_str(text)
    buf.direction = "rtl" if script == "Arab" else "ltr"
    buf.script = script
    buf.language = "ar"
    hb.shape(font, buf, {})
    infos = list(buf.glyph_infos)
    if script == "Arab":
        infos.reverse()
    # add_str gives each code point its index as its cluster.
    return [i.codepoint for i in infos], [i.cluster for i in infos]


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    data = subsetted(sys.argv[1])
    lines = []
    for script, words in [("arab", WORDS), ("latn", LATIN)]:
        for w in words:
            glyphs, clusters = shaped(data, w, script.capitalize())
            lines.append("%s\t%s\t%s\t%s" % (script, w, ",".join(map(str, glyphs)), ",".join(map(str, clusters))))
    built = lookup_font()
    built_lines = []
    for w in LOOKUP_WORDS:
        glyphs, clusters = shaped(built, w, "Latn")
        built_lines.append("latn\t%s\t%s\t%s" % (w, ",".join(map(str, glyphs)), ",".join(map(str, clusters))))
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
        "// One case on each line: the script, the text, the glyphs that HarfBuzz",
        "// gives in logical order, and the cluster of each glyph, split by tabs.",
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
    ]
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
