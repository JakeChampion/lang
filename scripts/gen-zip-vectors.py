#!/usr/bin/env python3
"""Write tests/stdlib/zip_vectors.fern: the archives of zip_test.fern.

The zipfile module of Python writes each archive. Run it from the root of
the repository.
"""
import io
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'tests/stdlib/zip_vectors.fern'
DATE = (1980, 1, 1, 0, 0, 0)
# 570 bytes that DEFLATE makes smaller.
TEXT = b''.join(b'line %d of the file\n' % (i % 7) for i in range(30))


def info(name, method):
    zi = zipfile.ZipInfo(name, DATE)
    zi.compress_type = method
    return zi


def mixed():
    """A directory, a stored file, a compressed file and an empty file, with
    a comment that holds the signature of the end record."""
    b = io.BytesIO()
    with zipfile.ZipFile(b, 'w') as z:
        z.writestr(info('dir/', zipfile.ZIP_STORED), b'')
        z.writestr(info('dir/stored.txt', zipfile.ZIP_STORED), b'stored bytes\n')
        z.writestr(info('dir/packed.txt', zipfile.ZIP_DEFLATED), TEXT)
        z.writestr(info('empty', zipfile.ZIP_STORED), b'')
        z.writestr(info('café.txt', zipfile.ZIP_DEFLATED), b'utf-8 name\n')
        z.comment = b'PK\x05\x06 is not the record' + bytes(20)
    return b.getvalue()


def none():
    b = io.BytesIO()
    with zipfile.ZipFile(b, 'w'):
        pass
    return b.getvalue()


def rows(data):
    out = []
    for i in range(0, len(data), 32):
        out.append('    ' + ', '.join(f'{x}u8' for x in data[i:i + 32]) + ',')
    return '\n'.join(out)


def main():
    parts = ['// The archives of zip_test.fern. scripts/gen-zip-vectors.py writes this\n// file. Do not edit it.\n']
    for fn, comment, data in [('mixed', 'A directory, a stored file, a compressed file, an empty file and a file\n// with a name that is not ASCII. The archive has a comment.', mixed()),
                              ('none', 'An archive with no entry.', none())]:
        parts.append(f'// {comment}\npub function {fn}(): u8[] {{\n  return [\n{rows(data)}\n  ];\n}}\n')
    OUT.write_text('\n'.join(parts))
    m = mixed()
    print('len', len(m), 'eocd', m.rfind(b'PK\x05\x06', 0, len(m) - 30), 'cd', m.find(b'PK\x01\x02'), 'local2', m.find(b'PK\x03\x04', 1), 'packed data', m.find(b'dir/packed.txt') + 14)


if __name__ == '__main__':
    main()
