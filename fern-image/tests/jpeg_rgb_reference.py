"""Independent rational component sampling and printed JFIF equations."""
from fractions import Fraction


def rounded(value):
    return (2 * value.numerator + value.denominator) // (2 * value.denominator)


def component_sample(plane, x, y, maximum_h, maximum_v, linear):
    u = Fraction(2 * x + 1, 2) * Fraction(plane['horizontal'], maximum_h) - Fraction(1, 2)
    v = Fraction(2 * y + 1, 2) * Fraction(plane['vertical'], maximum_v) - Fraction(1, 2)
    u = min(max(u, 0), plane['width'] - 1)
    v = min(max(v, 0), plane['height'] - 1)
    if not linear:
        return plane['pixels'][rounded(v) * plane['width'] + rounded(u)]
    left, top = int(u), int(v)
    right, bottom = min(left + 1, plane['width'] - 1), min(top + 1, plane['height'] - 1)
    a, b = u - left, v - top
    samples = plane['pixels']
    width = plane['width']
    value = ((1 - a) * samples[top * width + left] + a * samples[top * width + right]) * (1 - b)
    value += ((1 - a) * samples[bottom * width + left] + a * samples[bottom * width + right]) * b
    return rounded(value)


def assemble(reference, linear):
    planes = reference['planes']
    if len(planes) != 3 or [p['id'] for p in planes] != [1, 2, 3]:
        raise ValueError('explicit canonical component mapping required')
    maximum_h = max(p['horizontal'] for p in planes)
    maximum_v = max(p['vertical'] for p in planes)
    output = bytearray()
    for y in range(reference['height']):
        for x in range(reference['width']):
            yy, cb, cr = [component_sample(p, x, y, maximum_h, maximum_v, linear) for p in planes]
            red, blue = cr - 128, cb - 128
            channels = [yy + Fraction('1.402') * red,
                        yy - Fraction('0.34414') * blue - Fraction('0.71414') * red,
                        yy + Fraction('1.772') * blue]
            output.extend(min(255, max(0, rounded(value))) for value in channels)
    return bytes(output)
