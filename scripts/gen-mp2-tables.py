#!/usr/bin/env python3
"""Generate Layer II tables from the pinned CC0 minimp3 reference."""
import argparse
import hashlib
from pathlib import Path
import re
import struct
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
REV = 'ea99364f61c14656440e8d77e9c233ccf3124633'
SHA = '57e437c5c1f0e8b243885d3929c8973b5e6c778451e0100ab4251d19915cb3ad'


def f32(value):
    return struct.unpack('<f', struct.pack('<f', value))[0]


def generate(source):
    allocation = re.search(r'g_bitalloc_code_tab\[\]\s*=\s*\{(.*?)\}', source, re.S)[1]
    # The final 16 entries serve Layer I only.
    codes = [int(x) for x in re.findall(r'\d+', allocation)][:76]
    macro = re.search(r'#define DQ\(x\) ([^\n]+)', source)[1]
    numerators = [f32(float(x)) for x in re.findall(r'([\d.e+-]+)f/x', macro)]
    table = re.search(r'g_deq_L12\[18\*3\].*?\n(.*?)\n\s*\};', source, re.S)[1]
    divisors = [int(x) for x in re.findall(r'DQ\((\d+)\)', table)]
    assert len(codes) == 76 and len(numerators) == 3 and len(divisors) == 18
    values = [format(f32(value / divisor), '.9g') + 'f32'
              for divisor in divisors for value in numerators]
    return ('# MPEG Layer II quantization tables\n\n'
            'These constants follow scalar minimp3 at `' + REV + '` under CC0.\n\n'
            '```fern\n<<*>>=\n'
            '// Layer II bit-allocation codes and exact float32 dequantization constants.\n\n'
            'pub const BITALLOC: u8[] = [' + ', '.join(str(x) + 'u8' for x in codes) + '];\n\n'
            'pub const DEQUANT: f32[] = [' + ', '.join(values) + '];\n```\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, help='local pinned minimp3.h')
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    data = (args.source.read_bytes() if args.source else urllib.request.urlopen(
        f'https://raw.githubusercontent.com/lieff/minimp3/{REV}/minimp3.h', timeout=30).read())
    assert hashlib.sha256(data).hexdigest() == SHA, 'reference header hash differs'
    result = generate(data.decode())
    path = ROOT / 'internal/stdlib/std/audio/mp2/tables.fern.md'
    if args.check:
        assert path.read_text() == result, 'Layer II tables differ from the pinned source'
    else:
        path.write_text(result)


if __name__ == '__main__':
    main()
