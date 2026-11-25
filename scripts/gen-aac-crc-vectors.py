#!/usr/bin/env python3
"""Freeze CRC-protected ADTS AAC-LC frames and their independent float PCM.

The FDK AAC encoder writes the protected single-block frames and their CRCs.
FFmpeg decodes them to float PCM: it reads past the CRC and does not check it.
The multi-block frames put three FDK blocks into one ADTS frame.
Their CRCs come from the FDK CRCs, not from a parse of the AAC syntax:
each single-channel or LFE element contributes exactly 192 CRC bits and
each channel pair exactly 320, so the block CRC is the FDK CRC with the
header contribution replaced (ISO/IEC 13818-7, adts_error_check).
The synthetic frames write their syntax directly, with data and fill elements.
"""
import argparse
import base64
import hashlib
import math
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
POLY = 0x8005
# Elements of each channel configuration: 1 is a channel pair, 0 is one channel.
# fdkaac takes raw input with one, two, three or six channels only.
ELEMENTS = {1: [0], 2: [1], 3: [0, 1], 6: [0, 1, 1, 0]}


def crc_bits(value, bits):
    for bit in bits:
        feedback = (value >> 15 & 1) ^ bit
        value = value << 1 & 0xffff
        if feedback:
            value ^= POLY
    return value


def bits_of(data):
    return [byte >> (7 - i) & 1 for byte in data for i in range(8)]


# The catalogued check value of CRC-16/CMS, the same polynomial and preset.
assert crc_bits(0xffff, bits_of(b'123456789')) == 0xaee7


def frames(stream):
    at = 0
    while at < len(stream):
        size = (stream[at + 3] & 3) << 11 | stream[at + 4] << 3 | stream[at + 5] >> 5
        yield stream[at:at + size]
        at += size


def header(template, size, blocks, protected):
    out = bytearray(template[:7])
    out[1] = out[1] & 0xfe | (0 if protected else 1)
    out[3] = out[3] & 0xfc | size >> 11
    out[4] = size >> 3 & 255
    out[5] = (size & 7) << 5 | out[5] & 31
    out[6] = out[6] & 0xfc | blocks - 1
    return bytes(out)


def multi(stream, channel_config, per_frame):
    """Repack protected single-block frames into frames of `per_frame` blocks."""
    region = sum(320 if kind else 192 for kind in ELEMENTS[channel_config])
    singles = list(frames(stream))
    out = b''
    for start in range(0, len(singles), per_frame):
        group = singles[start:start + per_frame]
        if len(group) == 1:
            out += group[0]
            continue
        blocks = []
        for frame in group:
            assert frame[1] & 1 == 0 and frame[6] & 3 == 0
            stored = frame[7] << 8 | frame[8]
            after_header = crc_bits(0xffff, bits_of(frame[:7]))
            # Remove the header from the CRC: run the difference of the presets through `region` zero bits.
            block_crc = stored ^ crc_bits(after_header ^ 0xffff, [0] * region)
            blocks.append(frame[9:] + bytes([block_crc >> 8, block_crc & 255]))
        positions = b''
        at = 0
        for block in blocks[:-1]:
            at += len(block)
            positions += bytes([at >> 8, at & 255])
        size = 7 + len(positions) + 2 + sum(len(b) for b in blocks)
        head = header(group[0], size, len(blocks), True) + positions
        value = crc_bits(0xffff, bits_of(head))
        out += head + bytes([value >> 8, value & 255]) + b''.join(blocks)
    return out


class Bits:
    def __init__(self):
        self.data = []

    def put(self, value, width):
        assert 0 <= value < 1 << width
        self.data += [value >> (width - 1 - i) & 1 for i in range(width)]

    def align(self):
        self.data += [0] * (-len(self.data) % 8)

    def bytes(self):
        self.align()
        return bytes(int(''.join(map(str, self.data[i:i + 8])), 2) for i in range(0, len(self.data), 8))


def silent_channel(bits):
    bits.put(0, 8)  # Global gain.
    bits.put(0, 11)  # Only-long ics_info with no bands and no prediction.
    bits.put(0, 3)  # No pulse, TNS, or gain control.


def synthetic(channel_config, data):
    """One silent element, a data element, a fill element, and the end."""
    bits = Bits()
    regions = []
    bits.put(channel_config - 1, 3)  # Single-channel element or channel pair.
    start = len(bits.data)
    bits.put(0, 4)
    if channel_config == 2:
        bits.put(0, 1)  # No common window.
        silent_channel(bits)
        second = len(bits.data)
        silent_channel(bits)
        regions += [(start, len(bits.data), 192), (second, len(bits.data), 128)]
    else:
        silent_channel(bits)
        regions.append((start, len(bits.data), 192))
    bits.put(4, 3)  # Data stream element.
    start = len(bits.data)
    bits.put(5, 4)
    bits.put(1, 1)  # Byte-aligned data.
    bits.put(len(data), 8)
    bits.align()
    for byte in data:
        bits.put(byte, 8)
    regions.append((start, len(bits.data), 0))
    bits.put(6, 3)  # Fill element, outside the CRC.
    bits.put(2, 4)
    bits.put(0, 8)
    bits.put(165, 8)
    bits.put(7, 3)
    payload = bits.bytes()
    template = bytes([255, 240, 80 | channel_config >> 2, (channel_config & 3) << 6, 0, 31, 252])
    head = header(template, 9 + len(payload), 1, True)
    value = crc_bits(0xffff, bits_of(head))
    for start, end, width in regions:
        span = bits.data[start:end]
        if width:
            span = (span + [0] * width)[:width]
        value = crc_bits(value, span)
    return head + bytes([value >> 8, value & 255]) + payload


def run(command):
    subprocess.run(command, check=True)


def tone(channels, frames_count):
    values = []
    for i in range(frames_count):
        for channel in range(channels):
            value = .3 * math.sin(i * (.12 + channel * .017)) + .1 * math.sin(i * .73)
            values.append(int(value * 32767))
    return struct.pack('<%dh' % len(values), *values)


def provenance(name):
    binary = Path(shutil.which(name)).resolve()
    return hashlib.sha256(binary.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    ffmpeg = subprocess.check_output(['ffmpeg', '-version'], text=True).splitlines()[0]
    fdk = subprocess.run(['fdkaac', '--help'], capture_output=True, text=True).stdout.splitlines()[0]
    fdk += ' with libfdk-aac ' + subprocess.check_output(['dpkg-query', '-W', '-f', '${Version}', 'libfdk-aac2'], text=True)
    text = '// Generated by scripts/gen-aac-crc-vectors.py.\n'
    text += '// Independent encoder and CRCs: ' + fdk + ', SHA-256 ' + provenance('fdkaac') + '\n'
    text += '// Independent decoder: ' + ffmpeg + ', SHA-256 ' + provenance('ffmpeg') + '\n'
    text += 'import "aac_vectors";\n\n'
    rows = []
    with tempfile.TemporaryDirectory() as directory:
        temp = Path(directory)

        def decode(name, stream):
            (temp / 'input.aac').write_bytes(stream)
            run(['ffmpeg', '-v', 'error', '-nostdin', '-y', '-i', str(temp / 'input.aac'),
                 '-c:a', 'pcm_f32le', '-f', 'f32le', str(temp / 'output.f32')])
            pcm = (temp / 'output.f32').read_bytes()
            assert pcm, name
            return pcm

        def row(name, rate, channels, stream, pcm):
            encoded = base64.b64encode(zlib.compress(stream, 9)).decode()
            rows.append(f'    aac_vectors.Vector {{ name: "{name}", rate: {rate}, channels: {channels}, '
                        f'count: {len(pcm) // 4}, bytes: "{encoded}", pcm: {pcm_name(name)} }}')
            print(name, 'input', len(stream), 'PCM samples', len(pcm) // 4)

        consts = []

        def pcm_name(name):
            return 'PCM_' + name.split('-')[1].upper()

        for channels in sorted(ELEMENTS):
            rate = 48000 if channels > 1 else 44100
            (temp / 'input.s16').write_bytes(tone(channels, 2048))
            run(['fdkaac', '-S', '-R', '--raw-channels', str(channels), '--raw-rate', str(rate), '-f', '2', '-C',
                 '-b', str(48000 * channels), '-o', str(temp / 'fdk.aac'), str(temp / 'input.s16')])
            stream = (temp / 'fdk.aac').read_bytes()
            pcm = decode(f'fdk-{channels}', stream)
            encoded = base64.b64encode(zlib.compress(pcm, 9)).decode()
            consts.append(f'const PCM_{channels}: string = "{encoded}";\n')
            row(f'single-{channels}', rate, channels, stream, pcm)
            row(f'multi-{channels}', rate, channels, multi(stream, channels, 3), pcm)
        for channel_config in [1, 2]:
            stream = synthetic(channel_config, b'Fern')
            pcm = decode(f'data-{channel_config}', stream)
            assert pcm == bytes(len(pcm)), 'silent synthetic PCM'
            encoded = base64.b64encode(zlib.compress(pcm, 9)).decode()
            consts.append(f'const PCM_DATA{channel_config}: string = "{encoded}";\n')
            rows.append(f'    aac_vectors.Vector {{ name: "data-{channel_config}", rate: 44100, channels: {channel_config}, '
                        f'count: {len(pcm) // 4}, bytes: "{base64.b64encode(zlib.compress(stream, 9)).decode()}", '
                        f'pcm: PCM_DATA{channel_config} }}')
            print(f'data-{channel_config}', 'input', len(stream), 'PCM samples', len(pcm) // 4)
    text += '\n'.join(consts) + '\n'
    text += 'pub function vectors(): aac_vectors.Vector[] {\n  return [\n' + ',\n'.join(rows) + ',\n  ];\n}\n'
    target = ROOT / 'tests/stdlib/aac_crc_vectors.fern'
    if args.check:
        if not target.exists() or target.read_text() != text:
            parser.exit(1, 'AAC CRC vectors are stale; run scripts/gen-aac-crc-vectors.py\n')
    else:
        target.write_text(text)


if __name__ == '__main__':
    main()
