from collections import Counter
import argparse
import json
from pathlib import Path
import re

parser = argparse.ArgumentParser()
parser.add_argument('root', type=Path, help='directory containing before/after-target.s assembly files')
root = parser.parse_args().root
result = {}
for target in ['arm64-linux', 'x86-64-linux']:
    counts = {}
    for label in ['before', 'after']:
        lines = (root / f'{label}-{target}.s').read_text().splitlines()
        current = '<outside function>'
        symbols = Counter()
        for line in lines:
            m = re.match(r'\.type\s+([^,]+),\s*[@%]function', line)
            if m:
                current = m[1]
            if line[:1].isspace():
                symbols[current] += 1
            if line.startswith('.size '):
                current = '<outside function>'
        assert sum(symbols.values()) == sum(bool(l[:1].isspace()) for l in lines)
        counts[label] = symbols
    changed = []
    for symbol in sorted(counts['before'].keys() | counts['after'].keys()):
        before, after = counts['before'][symbol], counts['after'][symbol]
        if before != after:
            changed.append({'symbol': symbol, 'before': before, 'after': after, 'delta': after-before})
    result[target] = {'before': sum(counts['before'].values()), 'after': sum(counts['after'].values()), 'changed': changed}
print(json.dumps(result, indent=2))
