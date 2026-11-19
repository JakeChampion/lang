#!/usr/bin/env python3
"""Write tests/stdlib/shape_vectors.fern: a subset of Noto Naskh Arabic, a
subset of Noto Sans Devanagari, and the glyphs that HarfBuzz gives for a
list of Arabic and Devanagari words, for the tests of std/shape.

Run:  python3 -I scripts/gen-shape-vectors.py NotoNaskhArabic-Regular.ttf NotoSansDevanagari-Regular.ttf

It needs fontTools and uharfbuzz. The fonts come from the Noto project
(notofonts.github.io, fonts/NotoNaskhArabic/hinted/ttf and
fonts/NotoSansDevanagari/hinted/ttf). They are under the SIL Open Font
License 1.1, and each subset keeps each name of its font, so it keeps the
notice and the license. The Arabic subset holds the space, the ZWNJ, the
ZWJ, the letters and the marks from U+0621 to U+0655, and the GSUB and
GPOS features that std/shape applies.

The Devanagari subset holds the blocks Devanagari and Devanagari
Extended, the space, the no-break space, the joiners and the dotted
circle, with the Indic features. Its glyphs have no outlines, because the
shaping does not read them. The old cases shape the same subset with its
script 'dev2' renamed 'dev0', as the test renames it, so the shaper takes
the rules of the old Indic specification.

Each case is the script, the text, the glyphs in logical order, the
cluster of each glyph (the index of its first code point), and the
position of each glyph: its advance, then its x and y offsets, in font
units. HarfBuzz shapes Arabic right to left, so the script reverses its
glyphs. The cases of the script "latn" use the default shaper of
HarfBuzz, with no joining forms, left to right.

marks_font() is a second subset with the space and the marks alone.

The script also builds four fonts of its own, with no outlines:
lookup_font(), with a GSUB lookup of each type and format,
position_font(), with a GPOS lookup of each type, stage_font(), with
an rclt lookup before a calt lookup, and bare_font(), with no GDEF and no
GPOS table.
"""

import base64
import io
import sys
import unicodedata
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
DEVA_FEATURES = ["rvrn", "locl", "ccmp", "nukt", "akhn", "rphf", "rkrf", "pref", "blwf", "abvf", "half", "pstf",
                 "vatu", "cjct", "init", "pres", "abvs", "blws", "psts", "haln", "rlig", "calt", "clig", "rclt",
                 "abvm", "blwm", "curs", "dist", "kern", "mark", "mkmk"]
DEVA_UNICODES = [0x20, 0xA0, 0x200C, 0x200D, 0x25CC] + list(range(0x900, 0x980)) + list(range(0xA8E0, 0xA900))

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

# The joiners: a ZWJ joins the letters on its two sides, and stops the
# ligature of the lam and the alef. A ZWNJ stops the join.
JOINERS = ["ب\u200d", "\u200dب", "ب\u200cب", "ل\u200dا", "ب\u200d\u200dب", "ل\u200cا"]

# Spaces that the font does not have: each takes the glyph of the space,
# with the width that HarfBuzz gives it. U+2011 takes no glyph here.
SPACES = ["ب\u00a0ب", "ب\u2002ب", "ب\u2003ب", "ب\u2007ب", "ب\u2008ب", "ب\u2009ب", "ب\u202fب", "ب\u205fب",
          "ب\u3000ب", "ب\u2011ب"]

# Devanagari words, and the cases of each rule of the Indic shaper.
DEVANAGARI = [
    "क", "कि", "की", "कु", "के", "को", "कं", "कः", "कँ", "क्", "क्ष", "त्र", "ज्ञ", "श्र", "द्ध", "द्व",
    "ट्ट", "ह्न", "ह्म", "स्त", "स्त्र", "स्त्री", "प्य", "न्य", "क्य", "क्र", "प्र", "ग्र", "ट्र", "ड्र",
    "र्क", "र्म", "र्कि", "र्की", "र्कं", "धर्म", "कार्य", "पूर्ण", "र्त्र", "र्ष्ट्र",
    "नमस्ते", "हिन्दी", "संस्कृत", "क्षत्रिय", "स्त्रियाँ", "श्रृंगार", "विद्यालय", "राष्ट्रीय", "द्वारा",
    "फ़िल्म", "क़", "ज़", "ड़", "ढ़", "\u0958", "\u095b", "\u0929", "\u0928\u093c", "\u0931", "\u0930\u093c",
    "ऱ्य", "र\u094d\u200dय", "क\u094d\u200dष", "क\u094d\u200cष", "क\u200d", "ट\u094d\u200cयि", "ट\u094d\u200dयि",
    "अ", "आ", "ई", "ऑ", "ऋ", "र्अ", "अं", "\u0905\u093c", "ि", "्", "ं", "कि ि", "\u25cc\u093f",
    "\u00a0ि", "१ि", "ऽ", "ऽं", "ॐ", "क।", "क॥", "\u0915\u0951", "\u0915\u0952\u0951",
    "\u0915\u0953", "क\ua8e0", "\ua8f2", "\ua8fe", "क\ua8ff", "कि\u094d", "र्क\u094b\u094d",
    "क\u094d\u0915\u093f", "स्ति", "स्त्रि", "क्षि", "त्रि", "कृ", "हृ", "रु", "रू", "हु", "दृ", "द्यु",
    "क्\u200c", "क्\u200d", "र्\u200d", "र\u200d\u094d", "\u200dर्क", "क\u093c\u094d\u0937",
    "क\u094d\u093c\u0937", "क्ष्म", "ङ्क्ष", "क्क्क्क", "र्र्र", "ड़्ड़", "ऀक", "कऀ", "क\u0900\u0901",
    # A virama on the u matra by a lookup before the one that puts the matra
    # on its base: the virama keeps the height of the matra at that time.
    "ळु्", "र्ळु्",
    # Two pre-base matras with a halant between, and a ZWNJ before the base.
    "क\u093f\u094d\u093f", "क\u200c\u094dष",
]


# The bytes of `font`, with a fixed time in its head table, so that the
# script writes the same file each time.
def saved(font: TTFont) -> bytes:
    font["head"].created = font["head"].modified = 0x7C259DC0
    font.recalcTimestamp = False
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


def subset_font(path: str, unicodes, features) -> TTFont:
    opts = subset.Options()
    opts.layout_features = features
    opts.name_IDs = ["*"]
    opts.drop_tables += ["DSIG"]
    opts.hinting = False
    font = TTFont(path)
    s = subset.Subsetter(opts)
    s.populate(unicodes=unicodes)
    s.subset(font)
    return font


def subsetted(path: str, unicodes) -> bytes:
    return saved(subset_font(path, unicodes, FEATURES))


# The Devanagari subset with no outlines, and the same bytes with the
# script 'dev2' of GSUB and GPOS renamed 'dev0'. The script lists stay in
# the order of their tags.
def devanagari(path: str):
    font = subset_font(path, DEVA_UNICODES, DEVA_FEATURES)
    empty = TTGlyphPen(None).glyph()
    for name in font.getGlyphOrder():
        font["glyf"][name] = empty
    new = saved(font)
    assert new.count(b"dev2") == 2
    return new, new.replace(b"dev2", b"dev0")


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
    "a\u200db",              # a ZWJ in the input is skipped
    "a\u200cb",              # a ZWNJ in the input stops the ligature
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
    return saved(fb.font)


# A font with the letters "a" to "z" and two GSUB lookups: "rclt" changes
# "b" to "c", then "calt" changes "a" to "b". In Arabic the two features
# share a stage, so "a" becomes "b" and not "c".
def stage_font() -> bytes:
    order = [".notdef"] + LETTERS
    fb = FontBuilder(1000, isTTF=True)
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap({ord(c): c for c in LETTERS})
    empty = TTGlyphPen(None).glyph()
    fb.setupGlyf({g: empty for g in order})
    fb.setupHorizontalMetrics({g: (500, 0) for g in order})
    fb.setupHorizontalHeader(ascent=800, descent=-200)
    fb.setupNameTable({"familyName": "FernStageTest", "styleName": "Regular"})
    fb.setupOS2()
    fb.setupPost()
    fb.font["GSUB"] = layout_table("GSUB", [("calt", [1]), ("rclt", [0])], [single({"b": "c"}), single({"a": "b"})])
    return saved(fb.font)


# The characters of bare_font() and their glyphs.
BARE_CMAP = {0x20: "space", 0x47: "G", 0x61: "a", 0x64: "d", 0x69: "i", 0x78: "x", 0x1F4: "Gacute", 0xEF: "idieresis",
             0x300: "gravecomb", 0x301: "acutecomb", 0x306: "brevecomb", 0x308: "dieresiscomb", 0x313: "commaabovecomb",
             0x315: "commaaboverightcomb", 0x316: "gravebelowcomb", 0x34F: "cgj"}
BARE_ORDER = [".notdef"] + list(BARE_CMAP.values()) + ["acute.alt", "dieresis.alt"]

# The text of each case of bare_font(), and what it tests.
BARE_WORDS = [
    "G\u0341", "a\u0343", "\u0343",     # a decomposition into one character
    "d\u034f\u0308",                     # a CGJ that keeps no marks apart
    "d\u0315\u034f\u0308",              # a CGJ that keeps two marks apart
    "\u0301", "a\u0301",                 # a mark filtering set that GDEF does not have
    "\u00ef\u0306", "i\u0308\u0306",   # a ligature of the parts of a multiple substitution
    "a\u0300\u0316", "G\u0306\u0306",  # marks above and below, and stacked
    "xa", "d x\u0315",
]


# A font with no GDEF and no GPOS table, and empty glyphs. Its marks take
# their classes from Unicode, and their places from the fallback
# positioning. Its GSUB lookups: a single substitution of the acute with a
# mark filtering set that GDEF does not have, a context of "d" before the
# dieresis, and a multiple substitution of "idieresis" whose parts make a
# ligature again.
def bare_font() -> bytes:
    fb = FontBuilder(1000, isTTF=True)
    fb.setupGlyphOrder(BARE_ORDER)
    fb.setupCharacterMap(BARE_CMAP)
    empty = TTGlyphPen(None).glyph()
    fb.setupGlyf({g: empty for g in BARE_ORDER})
    fb.setupHorizontalMetrics({g: (150 if g.endswith("comb") or g.endswith(".alt") else 301 if g == "idieresis" else 500, 0)
                               for g in BARE_ORDER})
    fb.setupHorizontalHeader(ascent=800, descent=-200)
    fb.setupNameTable({"familyName": "FernBareTest", "styleName": "Regular"})
    fb.setupOS2()
    fb.setupPost()
    filtered = single({"acutecomb": "acute.alt"})
    filtered.LookupFlag = 0x10
    filtered.MarkFilteringSet = 3
    context = lookup(6, [subst(ot.ChainContextSubst, 3, BacktrackCoverage=[coverage(["d"])], InputCoverage=[coverage(["dieresiscomb"])],
                               LookAheadCoverage=[], SubstLookupRecord=[record(0, 4)], SubstCount=1)])
    split = lookup(2, [otl.buildMultipleSubstSubtable({"idieresis": ["i", "dieresiscomb"]})])
    join = lookup(4, [otl.buildLigatureSubstSubtable({("i", "dieresiscomb"): "idieresis"})])
    fb.font["GSUB"] = layout_table("GSUB", [("ccmp", [0, 1, 2, 3])], [filtered, context, split, join, single({"dieresiscomb": "dieresis.alt"})])
    return saved(fb.font)


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
    return saved(fb.font)


def shaped(data: bytes, text: str, script: str):
    font = hb.Font(hb.Face(data))
    buf = hb.Buffer()
    buf.add_str(text)
    buf.direction = "rtl" if script == "Arab" else "ltr"
    buf.script = script
    buf.language = "hi" if script == "Deva" else "ar"
    hb.shape(font, buf, {})
    infos = list(buf.glyph_infos)
    positions = list(buf.glyph_positions)
    if script == "Arab":
        infos.reverse()
        positions.reverse()
    # add_str gives each code point its index as its cluster.
    places = ["%d:%d:%d" % (p.x_advance, p.x_offset, p.y_offset) for p in positions]
    return [i.codepoint for i in infos], [i.cluster for i in infos], places


# `line` as a Fern string literal holds it: a tab and a newline as escapes,
# and a format or space character other than the space as \u{...}, as
# `fern -fmt` writes them.
def literal(line: str) -> str:
    out = []
    for c in line:
        if c == "\t":
            out.append("\\t")
        elif c != " " and unicodedata.category(c) in ("Cc", "Cf", "Zs", "Zl", "Zp"):
            out.append("\\u{%04X}" % ord(c))
        else:
            out.append(c)
    return "".join(out)


def case(data: bytes, text: str, script: str) -> str:
    glyphs, clusters, places = shaped(data, text, script.capitalize())
    return "%s\t%s\t%s\t%s\t%s" % (script, text, ",".join(map(str, glyphs)), ",".join(map(str, clusters)), ",".join(places))


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    data = subsetted(sys.argv[1], [0x20, 0x200C, 0x200D] + list(range(0x621, 0x656)))
    marks = subsetted(sys.argv[1], [0x20] + list(range(0x64B, 0x656)))
    deva, deva_old = devanagari(sys.argv[2])
    lines = []
    for script, words in [("arab", WORDS + JOINERS + SPACES), ("latn", LATIN + JOINERS + SPACES)]:
        for w in words:
            lines.append(case(data, w, script))
    deva_lines = [case(deva, w, "deva") for w in DEVANAGARI]
    old_lines = [case(deva_old, w, "deva") for w in DEVANAGARI]
    built = lookup_font()
    built_lines = [case(built, w, "latn") for w in LOOKUP_WORDS] + [case(built, w, "arab") for w in ["ab", "a\u200db", "a\u200cb"]]
    staged = stage_font()
    staged_lines = [case(staged, w, script) for script in ["arab", "latn"] for w in ["a", "b", "ab"]]
    placed = position_font()
    placed_lines = [case(placed, w, script) for script in ["latn", "arab"] for w in POSITION_WORDS]
    bare = bare_font()
    bare_lines = [case(bare, w, "latn") for w in BARE_WORDS]
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
        "// The same font with the space and the marks from U+064B to U+0655 alone.",
        "pub function marks_font(): string {",
        '  return "%s";' % base64.b64encode(marks).decode("ascii"),
        "}",
        "",
        "// One case on each line, split by tabs: the script, the text, and the",
        "// glyphs, the clusters and the positions that HarfBuzz gives, in logical",
        "// order. A position is the advance, the x offset and the y offset.",
        "pub function cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in lines),
        "}",
        "",
        "// A subset of Noto Sans Devanagari, Copyright 2022 The Noto Project",
        "// Authors (https://github.com/notofonts/devanagari), under the SIL Open",
        "// Font License 1.1, with no outlines. The names of the font hold the",
        "// notice and the license.",
        "pub function deva_font(): string {",
        '  return "%s";' % base64.b64encode(deva).decode("ascii"),
        "}",
        "",
        "// The cases of deva_font(), as those of cases().",
        "pub function deva_cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in deva_lines),
        "}",
        "",
        "// The cases of deva_font() with its script dev2 renamed dev0, for the",
        "// old Indic specification.",
        "pub function deva_old_cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in old_lines),
        "}",
        "",
        "// A font with a GSUB lookup of each type and format, and no outlines.",
        "pub function lookup_font(): string {",
        '  return "%s";' % base64.b64encode(built).decode("ascii"),
        "}",
        "",
        "// The cases of lookup_font(), as those of cases().",
        "pub function lookup_cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in built_lines),
        "}",
        "",
        "// A font with an rclt lookup before a calt lookup.",
        "pub function stage_font(): string {",
        '  return "%s";' % base64.b64encode(staged).decode("ascii"),
        "}",
        "",
        "// The cases of stage_font(), as those of cases().",
        "pub function stage_cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in staged_lines),
        "}",
        "",
        "// A font with a GPOS lookup of each type, and no outlines.",
        "pub function position_font(): string {",
        '  return "%s";' % base64.b64encode(placed).decode("ascii"),
        "}",
        "",
        "// The cases of position_font(), as those of cases().",
        "pub function position_cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in placed_lines),
        "}",
        "",
        "// A font with no GDEF and no GPOS table, a few GSUB lookups, and no",
        "// outlines.",
        "pub function bare_font(): string {",
        '  return "%s";' % base64.b64encode(bare).decode("ascii"),
        "}",
        "",
        "// The cases of bare_font(), as those of cases().",
        "pub function bare_cases(): string {",
        '  return "%s";' % "\\n".join(literal(l) for l in bare_lines),
        "}",
        "",
    ]
    OUT.write_text("\n".join(parts))


if __name__ == "__main__":
    main()
