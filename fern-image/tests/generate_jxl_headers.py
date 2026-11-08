"""Generate offline JPEG XL dimension fixtures with an explicit encoder."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def dependencies(binary):
    result, pending = {}, [(binary.resolve(), [])]
    while pending:
        path, inherited = pending.pop()
        if str(path) in result:
            continue
        loads = subprocess.check_output(['/usr/bin/otool', '-l', path], text=True)
        roots = re.findall(r'cmd LC_RPATH\s+cmdsize \d+\s+path (.*?) \(offset', loads)
        def expand(name):
            return name.replace('@loader_path', str(path.parent)).replace('@executable_path', str(binary.parent))
        roots = [Path(expand(name)) for name in roots] + inherited
        links = []
        lines = subprocess.check_output(['/usr/bin/otool', '-L', path], text=True).splitlines()[1:]
        for line in lines:
            name = line.strip().split(' (', 1)[0]
            if name.startswith(('/usr/lib/', '/System/')):
                continue
            if name.startswith('@rpath/'):
                candidates = [root / name.removeprefix('@rpath/') for root in roots]
            else:
                candidates = [Path(expand(name))]
            matches = [p.resolve() for p in candidates if p.is_absolute() and p.exists()]
            if not matches:
                raise ValueError('reference dependency needs an absolute path: ' + name)
            links.append(dict(install_name=name, resolved=str(matches[0])))
            pending.append((matches[0], roots))
        result[str(path)] = dict(sha256=digest(path), links=links)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--encoder', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    encoder = args.encoder.resolve()
    version = subprocess.check_output([encoder, '--version'], stderr=subprocess.STDOUT, text=True).strip()
    if 'v0.12.0' not in version:
        raise ValueError('use the pinned libjxl 0.12.0 fixture encoder')
    frozen = dependencies(encoder)
    record = dict(generator_sha256=digest(Path(__file__)), reference_version=version, reference_dependencies=frozen, images=[])
    with tempfile.TemporaryDirectory(prefix='jxl-header-fixtures-') as directory:
        folder = Path(directory)
        for width, height in [(1, 1), (9, 8), (13, 17), (64, 33)]:
            pixels = bytes(channel for y in range(height) for x in range(width)
                           for channel in [(17 * x + 31 * y) % 256, (7 * x + 11 * y) % 256, (3 * x + 5 * y) % 256])
            ppm = f'P6\n{width} {height}\n255\n'.encode() + pixels
            source, encoded = folder / 'input.ppm', folder / 'output.jxl'
            source.write_bytes(ppm)
            options = ['--distance=0', '--effort=1', '--container=0', '--num_threads=1']
            subprocess.run([encoder, source, encoded, *options], capture_output=True, check=True, timeout=60)
            data = encoded.read_bytes()
            if not data.startswith(b'\xff\x0a'):
                raise ValueError('reference output is not a raw codestream')
            record['images'].append(dict(width=width, height=height, input_ppm_sha256=digest(source),
                                         options=options, codestream_sha256=digest(encoded),
                                         codestream_base64=base64.b64encode(data).decode()))
    if dependencies(encoder) != frozen:
        raise ValueError('reference dependencies changed during fixture generation')
    with args.output.open('x') as stream:
        stream.write(json.dumps(record, indent=2) + '\n')
    print(json.dumps(dict(images=len(record['images']), dependencies=len(frozen))))


if __name__ == '__main__':
    main()
