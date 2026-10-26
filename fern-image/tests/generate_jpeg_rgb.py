"""Create finite JFIF observations with separate offline component and RGB oracles."""
import argparse
import fractions
import gzip
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import sys

from jpeg_rgb_reference import assemble


def sha(data):
    return hashlib.sha256(data).hexdigest()


def profile_outcome(encoded, ids):
    declared = encoded[2:11] == bytes.fromhex('ffe000104a46494600')
    return 'accepted' if declared and ids == [1, 2, 3] else 'unsupported' if declared and ids == [1] else 'profile'


def zero_image(width, height):
    def segment(marker, payload):
        return bytes([255, marker]) + (len(payload) + 2).to_bytes(2, 'big') + payload
    app = bytes.fromhex('ffe000104a46494600010100000100010000')
    quant = segment(219, bytes([0]) + bytes([1] * 64))
    dc = bytes([0, 3] + [0] * 14) + bytes([0, 1, 2])
    ac = bytes([0, 0, 3] + [0] * 13) + bytes([0, 1, 240])
    codes = segment(196, bytes([0]) + dc + bytes([16]) + ac)
    frame = segment(192, bytes([8]) + height.to_bytes(2, 'big') + width.to_bytes(2, 'big') + bytes([3, 1, 34, 0, 2, 17, 0, 3, 17, 0]))
    scan = segment(218, bytes([3, 1, 0, 2, 0, 3, 0, 0, 63, 0]))
    blocks = ((width + 15) // 16) * ((height + 15) // 16) * 6
    bits = '00000' * blocks
    bits += '1' * (-len(bits) % 8)
    entropy = bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8)).replace(b'\xff', b'\xff\0')
    return b'\xff\xd8' + app + quant + codes + frame + scan + entropy + b'\xff\xd9'


def marker_at(encoded, marker):
    at = 2
    while at + 4 <= len(encoded):
        if encoded[at + 1] == marker:
            return at
        at += 2 + int.from_bytes(encoded[at + 2:at + 4], 'big')
    raise ValueError('marker missing')


def canonical(encoded):
    data = bytearray(encoded)
    frame, scan = marker_at(data, 192), marker_at(data, 218)
    for i in range(3):
        data[frame + 10 + i * 3] = i + 1
        data[scan + 5 + i * 2] = i + 1
    app = bytes.fromhex('ffe000104a46494600010100000100010000')
    return bytes(data[:2]) + app + bytes(data[2:])


def observe(oracle, path):
    result = subprocess.run([str(oracle.resolve()), str(path)], capture_output=True, timeout=20)
    if result.returncode or result.stderr:
        raise ValueError('offline reference failed: ' + path.name + ': ' + result.stderr.decode(errors='replace'))
    return json.loads(result.stdout), sha(result.stdout)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('directory', type=Path)
    parser.add_argument('--raw-oracle', type=Path, required=True)
    parser.add_argument('--rgb-oracle', type=Path, required=True)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--fern-fixtures', type=Path)
    args = parser.parse_args()
    here = Path(__file__).resolve().parent
    source = here / 'fixtures/jpeg-planes-reference.json.gz'
    paths = {'generator': Path(__file__).resolve(), 'math_source': here / 'jpeg_rgb_reference.py',
             'oracle_source': here / 'reference_jpeg_rgb.c', 'raw_oracle_source': here / 'reference_jpeg_planes.c',
             'source_bundle': source, 'raw_oracle': args.raw_oracle.resolve(), 'rgb_oracle': args.rgb_oracle.resolve(),
             'Python': Path(sys.executable).resolve(), 'fraction_source': Path(fractions.__file__).resolve()}
    hashes = {name: sha(path.read_bytes()) for name, path in paths.items()}
    old = json.loads(gzip.decompress(source.read_bytes()))
    candidates = []
    for entry in old['cases']:
        encoded = bytes.fromhex(entry['encoded_hex'])
        reference = entry['reference']
        ids = [p['id'] for p in reference['planes']]
        outcome = profile_outcome(encoded, ids)
        candidates.append((entry['name'], encoded, outcome, entry))
    for name in ['arbitrary-identifiers', 'sampling-3-2-1']:
        entry = next(x for x in old['cases'] if x['name'] == name)
        candidates.append(('canonical-' + name, canonical(bytes.fromhex(entry['encoded_hex'])), 'accepted', None))
    entry = next(x for x in old['cases'] if x['name'] == 'RGB-0-17x9-o0-r1')
    base = bytes.fromhex(entry['encoded_hex'])
    candidates.append(('comment', base[:20] + bytes.fromhex('fffe0009636f6d6d656e74') + base[20:], 'accepted', None))
    for marker, data in [(224, b'JFXX\0\x10'), (225, b'Exif\0\0'), (226, b'ICC_PROFILE\0\x01\x01'), (238, b'Adobe\0\x64\0\0\0\0\x01'), (239, b'unknown')]:
        segment = bytes([255, marker]) + (len(data) + 2).to_bytes(2, 'big') + data
        candidates.append(('unsupported-app-' + str(marker), base[:20] + segment + base[20:], 'profile', None))
    candidates.append(('missing-jfif', base[:2] + base[20:], 'profile', None))
    changed = bytearray(base)
    frame, scan = marker_at(changed, 192), marker_at(changed, 218)
    for i, value in enumerate([17, 201, 55]):
        changed[frame + 10 + i * 3] = value
        changed[scan + 5 + i * 2] = value
    candidates.append(('wrong-jfif-identifiers', bytes(changed), 'profile', None))
    candidates.append(('wide-neutral-257x9', zero_image(257, 9), 'accepted', None))
    args.directory.mkdir(parents=True, exist_ok=True)
    cases = []
    for name, encoded, outcome, previous in candidates:
        path = args.directory / (name + '.jpg')
        path.write_bytes(encoded)
        case = {'name': name, 'encoded_hex': encoded.hex(), 'input_sha256': sha(encoded), 'outcome': outcome}
        if previous or outcome == 'accepted':
            raw, raw_hash = observe(args.raw_oracle, path)
            if previous and (raw != previous['reference'] or raw_hash != previous['oracle_stdout_sha256']):
                raise ValueError('published raw component reference changed')
            case['raw_reference'], case['raw_stdout_sha256'] = raw, raw_hash
        if outcome == 'accepted':
            case['nearest_hex'] = assemble(raw, False).hex()
            case['bilinear_hex'] = assemble(raw, True).hex()
            maximum_h = max(p['horizontal'] for p in raw['planes'])
            maximum_v = max(p['vertical'] for p in raw['planes'])
            if all(maximum_h % p['horizontal'] == 0 and maximum_v % p['vertical'] == 0 for p in raw['planes']):
                rgb, rgb_hash = observe(args.rgb_oracle, path)
                if rgb['width'] != raw['width'] or rgb['height'] != raw['height'] or len(rgb['pixels']) != raw['width'] * raw['height'] * 3:
                    raise ValueError('offline RGB reference shape')
                case['codec_reference'], case['codec_stdout_sha256'] = rgb, rgb_hash
        cases.append(case)
    if hashes != {name: sha(path.read_bytes()) for name, path in paths.items()}:
        raise ValueError('reference source or tool changed')
    document = {'schema': 1, 'reference': {'scope': 'Finite restricted JFIF samples only. No broad compatibility, codec parity, physical bounds, performance, or production claim.',
                'codec': 'libjpeg-turbo 2.1.5 JDCT_ISLOW non-fancy RGB and raw components', 'Python': platform.python_version(), 'sha256': hashes}, 'cases': cases}
    args.bundle.parent.mkdir(parents=True, exist_ok=True)
    args.bundle.write_bytes(gzip.compress(json.dumps(document, separators=(',', ':')).encode(), mtime=0))
    if args.fern_fixtures:
        names = [f'RGB-{i}-17x9-o0-r1' for i in range(3)] + ['canonical-sampling-3-2-1', 'RGB-0-1x1-o0-r0', 'wide-neutral-257x9']
        text = 'import "std/hex";\n\nfunction bytes(text: string): u8[] {\n return match (hex.decode(text)) { Ok(v) => v, Err(_) => [] };\n}\n\n'
        for function, key in [('input', 'encoded_hex'), ('nearest', 'nearest_hex'), ('bilinear', 'bilinear_hex')]:
            text += f'pub function {function}(index: i32): u8[] {{\n return match (index) {{\n'
            for i, name in enumerate(names):
                case = next(x for x in cases if x['name'] == name)
                text += f'  {i} => bytes("{case[key]}"),\n'
            text += '  _ => [],\n };\n}\n\n'
        args.fern_fixtures.write_text(text)
    print('created', len(cases), 'cases,', sum(c['outcome'] == 'accepted' for c in cases), 'accepted,', sum('codec_reference' in c for c in cases), 'supplemental C RGB observations')


if __name__ == '__main__':
    main()
