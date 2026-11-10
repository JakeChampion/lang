#!/usr/bin/env python3
"""Write tests/stdlib/truetype_var_vectors.fern: a small variable font, and
the static fonts that fontTools makes from it at the test weights.

The font has one axis, the weight, from 100 to 900 with the default at 400.
It has a master at each end, one between the default and the heaviest, and a
segment map. Each test weight gives outlines with whole coordinates, so the
static fonts hold the exact outlines and the test compares pixels for
equality.

Needs fontTools. Run it from the root of the repository.
"""
import io
from pathlib import Path

from fontTools.designspaceLib import AxisDescriptor, DesignSpaceDocument, SourceDescriptor
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.recordingPen import RecordingPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTFont
from fontTools.varLib import build
from fontTools.varLib.instancer import instantiateVariableFont

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'tests/stdlib/truetype_var_vectors.fern'
ORDER = ['.notdef', 'space', 'A', 'B', 'C']
CMAP = {32: 'space', 65: 'A', 66: 'B', 67: 'C'}
# The weights of the test, in the units of the axis.
WEIGHTS = [250, 525, 650, 900]


def stem(grow):
    """An "L" of ten points. A heavier master moves only the right edges."""
    return [(0, 0), (200 + grow, 0), (200 + grow, 100), (100 + grow // 2, 100), (100 + grow // 2, 400),
            (100 + grow // 2, 700), (0, 700), (0, 400), (0, 200), (0, 100)]


def master(grow, lift):
    """One master. `grow` widens the stems and `lift` moves the dot of "C"."""
    pens = {}
    pen = TTGlyphPen(None)
    pens['.notdef'] = pen.glyph()
    pens['space'] = pen.glyph()
    pen = TTGlyphPen(None)
    points = stem(grow)
    pen.moveTo(points[0])
    for p in points[1:]:
        pen.lineTo(p)
    pen.closePath()
    pens['A'] = pen.glyph()
    pen = TTGlyphPen(None)
    pen.moveTo((0, 0))
    pen.qCurveTo((160 + grow, 0), (160 + grow, 160))
    pen.lineTo((0, 160))
    pen.closePath()
    pen.moveTo((40, 40))
    pen.lineTo((40, 120))
    pen.lineTo((80 + grow // 2, 120))
    pen.lineTo((80 + grow // 2, 40))
    pen.closePath()
    pens['B'] = pen.glyph()
    pen = TTGlyphPen(ORDER)
    pen.addComponent('A', (1, 0, 0, 1, 0, 0))
    pen.addComponent('B', (1, 0, 0, 1, 320 + grow, 480 + lift))
    pens['C'] = pen.glyph()
    fb = FontBuilder(1000, isTTF=True)
    fb.setupGlyphOrder(ORDER)
    fb.setupCharacterMap(CMAP)
    fb.setupGlyf(pens)
    fb.setupHorizontalMetrics({'.notdef': (500, 0), 'space': (240 + grow, 0), 'A': (320 + grow, 0),
                               'B': (240 + grow, 0), 'C': (600 + 2 * grow, 0)})
    fb.setupHorizontalHeader(ascent=800, descent=-200)
    fb.setupNameTable({'familyName': 'Fern Test', 'styleName': 'Regular'})
    fb.setupOS2()
    fb.setupPost()
    return fb.font


def variable():
    doc = DesignSpaceDocument()
    axis = AxisDescriptor()
    axis.name = 'Weight'
    axis.tag = 'wght'
    axis.minimum, axis.default, axis.maximum = 100, 400, 900
    # The segment map: the weight 650 is at the design weight 775.
    axis.map = [(100, 100), (400, 400), (650, 775), (900, 900)]
    doc.addAxis(axis)
    # The design weight 650 is half of the way to the heaviest master, and
    # its outlines are not on the line between its neighbours.
    for weight, grow, lift in [(400, 0, 0), (100, -64, -32), (650, 96, 64), (900, 128, 32)]:
        source = SourceDescriptor()
        source.font = master(grow, lift)
        source.location = {'Weight': weight}
        doc.addSource(source)
    font, _, _ = build(doc)
    data = io.BytesIO()
    font.save(data)
    return data.getvalue()


def check(data):
    """Refuse a font that does not test what the test names."""
    font = TTFont(io.BytesIO(data))
    assert all(t in font for t in ['fvar', 'avar', 'gvar', 'HVAR'])
    tuples = [t for name in ORDER for t in font['gvar'].variations.get(name, [])]
    assert any(None in t.coordinates for t in tuples), 'no point gets its delta from its neighbours'
    assert any(t.axes['wght'][0] != min(0, t.axes['wght'][1]) for t in tuples), 'no intermediate region'
    for weight in WEIGHTS:
        glyphs = font.getGlyphSet(location={'wght': weight})
        for name in ORDER:
            pen = RecordingPen()
            glyphs[name].draw(pen)
            for _, args in pen.value:
                for point in args:
                    if isinstance(point, tuple) and len(point) == 2:
                        assert all(float(v).is_integer() for v in point), (weight, name, point)
            assert float(glyphs[name].width).is_integer(), (weight, name)


def instance(data, weight):
    font = instantiateVariableFont(TTFont(io.BytesIO(data)), {'wght': weight})
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


def without(data, tag):
    font = TTFont(io.BytesIO(data))
    del font[tag]
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


def fern_bytes(name, comment, data):
    rows = []
    for at in range(0, len(data), 32):
        rows.append('    ' + ', '.join(f'{b}u8' for b in data[at:at + 32]) + ',')
    return f'// {comment}\npub function {name}(): u8[] {{\n  return [\n' + '\n'.join(rows) + '\n  ];\n}\n'


def main():
    data = variable()
    check(data)
    parts = ['// The fonts of truetype_var_test.fern. scripts/gen-truetype-var-vectors.py\n'
             '// writes this file. Do not edit it.\n',
             fern_bytes('variable', 'A variable font with the axis "wght" from 100 to 900.', data),
             fern_bytes('variable_no_hvar', 'The same font with no HVAR table.', without(data, 'HVAR'))]
    for weight in WEIGHTS:
        parts.append(fern_bytes(f'static_{weight}', f'The static font that fontTools makes at the weight {weight}.',
                                instance(data, weight)))
    OUT.write_text('\n'.join(parts))


if __name__ == '__main__':
    main()
