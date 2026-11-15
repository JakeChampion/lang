#!/usr/bin/env python3
"""Write coreutils/lib/glibc_ctype.fern from the C.UTF-8 locale of glibc.

The ports that read UTF-8 as GNU tools do (sed) need the character classes
and the case mappings of the glibc that the reference tools run on. The
tables of Unicode in std/unicode are of a newer version, so this script asks
the libc of the host for each code point: iswctype for each class, and
towupper and towlower for the case.

Usage: scripts/gen-glibc-ctype.py > coreutils/lib/glibc_ctype.fern
"""
import ctypes
import ctypes.util
import platform
import sys

libc = ctypes.CDLL(ctypes.util.find_library('c'))
libc.setlocale.restype = ctypes.c_char_p
libc.setlocale.argtypes = [ctypes.c_int, ctypes.c_char_p]
LC_ALL = 6
if libc.setlocale(LC_ALL, b'C.UTF-8') is None:
    sys.exit('gen-glibc-ctype: the locale C.UTF-8 is not on this system')
libc.wctype.restype = ctypes.c_ulong
libc.wctype.argtypes = [ctypes.c_char_p]
libc.iswctype.restype = ctypes.c_int
libc.iswctype.argtypes = [ctypes.c_uint32, ctypes.c_ulong]
libc.towupper.restype = ctypes.c_uint32
libc.towupper.argtypes = [ctypes.c_uint32]
libc.towlower.restype = ctypes.c_uint32
libc.towlower.argtypes = [ctypes.c_uint32]
libc.gnu_get_libc_version.restype = ctypes.c_char_p

CLASSES = ['alpha', 'digit', 'alnum', 'upper', 'lower', 'space', 'blank', 'punct', 'print', 'graph', 'cntrl', 'xdigit']
TOP = 0x110000


def ranges(pred):
    out = []
    start = None
    for cp in range(TOP):
        if pred(cp):
            if start is None:
                start = cp
        elif start is not None:
            out.append((start, cp - 1))
            start = None
    if start is not None:
        out.append((start, TOP - 1))
    return out


def mapping(fn):
    """Runs of code points that a mapping moves by one delta, with a step
    of 1 or 2 between them."""
    pairs = [(cp, fn(cp)) for cp in range(TOP) if fn(cp) != cp]
    runs = []
    for cp, to in pairs:
        d = to - cp
        if runs:
            lo, hi, step, delta = runs[-1]
            if delta == d and (hi + step == cp if step else cp - hi in (1, 2)):
                runs[-1] = (lo, cp, step or cp - hi, delta)
                continue
        runs.append((cp, cp, 0, d))
    return [(lo, hi, step or 1, d) for lo, hi, step, d in runs]


def emit(name, values, per=8):
    lines = []
    for i in range(0, len(values), per):
        lines.append('    ' + ', '.join(str(v) for v in values[i:i + per]) + ',')
    return '  ' + name + ' => [\n' + '\n'.join(lines) + '\n  ],'


version = libc.gnu_get_libc_version().decode()
out = []
out.append('// The character classes and the case mappings of the locale C.UTF-8 of')
out.append('// glibc ' + version + ', on ' + platform.machine() + '. scripts/gen-glibc-ctype.py writes')
out.append('// this file from the libc of the host. Do not edit it by hand.')
out.append('')
out.append('// The code points of a class, as pairs of the first and the last of each')
out.append('// range.')
out.append('// fern-lint: allow cyclomatic-complexity')
out.append('pub function class_ranges(name: string): i32[] {')
out.append('  return match (name) {')
for c in CLASSES:
    t = libc.wctype(c.encode())
    rs = ranges(lambda cp: libc.iswctype(cp, t) != 0)
    out.append(emit('"' + c + '"', [v for r in rs for v in r]))
out.append('  _ => [],')
out.append('  };')
out.append('}')
out.append('')
for fname, fn in [('upper', libc.towupper), ('lower', libc.towlower)]:
    runs = mapping(fn)
    flat = [v for r in runs for v in r]
    out.append('// The runs of towupper: the first and the last code point, the step')
    out.append('// between them, and the difference to the result.' if fname == 'upper' else '// The runs of towlower, in the form of upper_runs.')
    out.append('function ' + fname + '_runs(): i32[] {')
    out.append('  return [')
    for i in range(0, len(flat), 8):
        out.append('    ' + ', '.join(str(v) for v in flat[i:i + 8]) + ',')
    out.append('  ];')
    out.append('}')
    out.append('')
points = set()
for cp in range(TOP):
    u, low = libc.towupper(cp), libc.towlower(cp)
    if u != cp or low != cp:
        points.update([cp, u, low])
flat = sorted(points)
out.append('// Each code point that towupper or towlower changes or gives, in order.')
out.append('pub function case_points(): i32[] {')
out.append('  return [')
for i in range(0, len(flat), 8):
    out.append('    ' + ', '.join(str(v) for v in flat[i:i + 8]) + ',')
out.append('  ];')
out.append('}')
out.append('')
out.append('''// The result of a mapping from its runs, by a binary search.
function mapped(runs: i32[], cp: i32): i32 {
  let lo = 0;
  let hi = runs.len() / 4;
  while (lo < hi) {
    let mid = (lo + hi) / 2;
    if (runs[4 * mid + 1] < cp) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  if (lo < runs.len() / 4) {
    let k = 4 * lo;
    if (runs[k] <= cp && (cp - runs[k]) % runs[k + 2] == 0) {
      return cp + runs[k + 3];
    }
  }
  return cp;
}

// glibc's towupper in C.UTF-8.
pub function upper(cp: i32): i32 {
  if (cp < 128) {
    return if (cp >= 97 && cp <= 122) { cp - 32 } else { cp };
  }
  return mapped(upper_runs(), cp);
}

// glibc's towlower in C.UTF-8.
pub function lower(cp: i32): i32 {
  if (cp < 128) {
    return if (cp >= 65 && cp <= 90) { cp + 32 } else { cp };
  }
  return mapped(lower_runs(), cp);
}''')
print('\n'.join(out))
