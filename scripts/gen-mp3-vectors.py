#!/usr/bin/env python3
"""Regenerate independent MP3 golden vectors with FFmpeg and scalar minimp3."""
import hashlib
import importlib.util
import math
import os
from pathlib import Path
import shutil
import struct
import subprocess
import tempfile
import urllib.request
import wave
import zlib

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('tables', ROOT / 'scripts/gen-mp3-tables.py')
tables = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tables)
EX_SHA = '8437f3fc1d4d8ab2269a1624f5380a08df1967a048f8887789bae6e25db7db79'
ISO = {
    'l3-si_block.bit': '1d12956840638bf6b81164fc510c3e8269eeb038a51dc4548469e21a71e52b69',
    'l3-test45.bit': '384ccca2d558a3505bb2e7c2860e12aa3b651473ace655cda3a2c8cfb1b2fc53',
    'l3-test46.bit': 'c44f835c6efbfad67359aa327d78f0feaf22c443e191d68f80e6d2fefd6d8af5',
}
ISO_PCM = {
    'l3-si_block.bit': '7ed181c7dad40e7033d73303131345ac57b914fab9eb1ae8c00a263aa9b0b7cb',
    'l3-test45.bit': 'bd79ae04a9437ffa9794df9540c119b45c89b29488c6b6584cc32c3ccde21fe0',
    'l3-test46.bit': '5038a9c90f640474261da37833d3ad9c172301938a8e75c06d39e7ef1610edf1',
}


def download(name, expected):
    data = urllib.request.urlopen(f'https://raw.githubusercontent.com/lieff/minimp3/{tables.REV}/{name}', timeout=30).read()
    assert hashlib.sha256(data).hexdigest() == expected, name
    return data


def reference(temp):
    (temp / 'minimp3.h').write_bytes(download('minimp3.h', tables.SHA))
    (temp / 'minimp3_ex.h').write_bytes(download('minimp3_ex.h', EX_SHA))
    shutil.copy(ROOT / 'scripts/audio-reference/mp3.c', temp / 'mp3.c')
    subprocess.run(['cc', '-O3', '-ffp-contract=off', '-o', str(temp / 'reference'), str(temp / 'mp3.c')], check=True)
    return temp / 'reference'


def frame_offsets(data):
    at = 0
    if data[:3] == b'ID3':
        size = 0
        for b in data[6:10]:
            assert b < 128
            size = size * 128 + b
        at = 10 + size
    offsets = []
    while at + 4 <= len(data):
        assert data[at] == 255
        a, b, c = data[at + 1:at + 4]
        assert a & 6 == 2
        version = 1 if a & 8 else 2 if a & 16 else 25
        rates = [44100, 48000, 32000]
        rate = rates[b >> 2 & 3] // (1 if version == 1 else 2 if version == 2 else 4)
        kbps = ([0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320] if version == 1 else
                [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160])[b >> 4]
        assert kbps
        size = (144000 if version == 1 else 72000) * kbps // rate + (b >> 1 & 1)
        if size > len(data) - at:
            break
        offsets.append((at, size, rate, 1 if c >> 6 == 3 else 2))
        at += size
    return offsets


def signal(rate, cc, count):
    values = []
    for i in range(count):
        for ch in range(cc):
            value = int(17000 * math.sin(i * (0.14 + ch * 0.03)) + 4000 * math.sin(i * 1.41))
            if i % 301 < 12:
                value = (i * 179 + ch * 311) % 50000 - 25000
            values.append(value)
    return values


def write_wave(path, rate, cc, values):
    with wave.open(str(path), 'wb') as output:
        output.setnchannels(cc)
        output.setsampwidth(2)
        output.setframerate(rate)
        output.writeframes(struct.pack('<' + 'h' * len(values), *values))


def encoded(temp, rate, cc, *, gapless=False, vbr=False, values=None, joint=True, bitrate=None):
    if values is None:
        values = signal(rate, cc, int(rate * 0.025))
    write_wave(temp / 'source.wav', rate, cc, values)
    mode = ['-q:a', '1'] if vbr else ['-b:a', bitrate or ('32k' if rate < 32000 else '128k')]
    subprocess.run(['ffmpeg', '-v', 'error', '-y', '-i', str(temp / 'source.wav'), '-c:a', 'libmp3lame',
                    *mode, '-joint_stereo', '1' if joint else '0', '-write_xing', str(int(gapless)),
                    '-map_metadata', '-1', '-fflags', '+bitexact', '-flags:a', '+bitexact', str(temp / 'encoded.mp3')], check=True)
    data = (temp / 'encoded.mp3').read_bytes()
    offsets = frame_offsets(data)
    return data[offsets[0][0]:]


def pcm_of(temp, ref, data, gapless=False):
    path = temp / 'case.mp3'
    path.write_bytes(data)
    subprocess.run([str(ref), str(path), str(temp / 'reference.f32'), 'gapless' if gapless else 'raw'], check=True)
    return (temp / 'reference.f32').read_bytes()


def remuxed(temp, data):
    (temp / 'raw.mp3').write_bytes(data)
    subprocess.run(['ffmpeg', '-v', 'error', '-y', '-f', 'mp3', '-i', str(temp / 'raw.mp3'),
                    '-c:a', 'copy', '-map_metadata', '-1', '-write_xing', '1', '-fflags', '+bitexact',
                    str(temp / 'remux.mp3')], check=True)
    data = (temp / 'remux.mp3').read_bytes()
    data = data[frame_offsets(data)[0][0]:]
    tag = data.find(b'Lavf', 0, frame_offsets(data)[0][1])
    assert tag >= 0 and data[tag + 21:tag + 24] == bytes(3), 'zero delay and padding extension'
    return data


def crc16(data):
    c = 65535
    for byte in data:
        c ^= byte << 8
        for _ in range(8):
            c = ((c << 1) ^ (0x8005 if c & 0x8000 else 0)) & 65535
    return c


def protected_silence():
    header = bytes.fromhex('fffa9000')
    side = bytes(32)
    crc = crc16(header[2:] + side)
    return header + crc.to_bytes(2, 'big') + side + bytes(417 - 38)


def mixed8k():
    def field(value, width):
        return format(value, f'0{width}b')
    def granule():
        return ''.join(field(v, n) for v, n in [(45, 12), (43, 9), (180, 8), (0, 9), (1, 1), (2, 2), (1, 1), (33, 10), (0, 9), (0, 1), (0, 1)])
    side = '0' * 10 + granule() + granule()
    assert len(side) == 136
    payload = '1' * 42 + '010' + '1' * 42 + '011'
    data = int(payload.ljust(96, '0'), 2).to_bytes(12, 'big')
    frame = bytes.fromhex('ffe31800') + int(side, 2).to_bytes(17, 'big') + data + bytes(39)
    assert len(frame) == 72
    return frame * 2


def main():
    cases = []
    mixed = mixed8k()
    with tempfile.TemporaryDirectory() as temp:
        temp = Path(temp)
        ref = reference(temp)
        for index, rate in enumerate([8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000]):
            cc = 1 + index % 2
            data = encoded(temp, rate, cc)
            cases.append((f'mpeg-{rate}-{cc}', data, rate, cc, False, pcm_of(temp, ref, data)))
        for rate, cc in [(16000, 2), (48000, 2)]:
            data = encoded(temp, rate, cc, gapless=True, vbr=True)
            cases.append((f'gapless-vbr-{rate}', data, rate, cc, True, pcm_of(temp, ref, data, True)))
        for name, count in [('l3-si_block.bit', 20), ('l3-test45.bit', 8), ('l3-test46.bit', 8)]:
            data = download('vectors/' + name, ISO[name])
            offsets = frame_offsets(data)
            end = offsets[count - 1][0] + offsets[count - 1][1]
            data = data[:end]
            rate, cc = offsets[0][2:]
            cases.append((name, data, rate, cc, False, pcm_of(temp, ref, data)))
        data = remuxed(temp, encoded(temp, 48000, 2))
        cases.append(('remux-zero-padding', data, 48000, 2, True, pcm_of(temp, ref, data, True)))
        data = protected_silence() * 2
        cases.append(('protected-silence', data, 44100, 2, False, pcm_of(temp, ref, data)))
        # ISO PCM checks cover the intensity-stereo cases. FFmpeg checks the other cases.
        for name, data, rate, cc, gapless, pcm in cases:
            path = temp / 'verify.mp3'
            path.write_bytes(data)
            b = struct.unpack('<' + 'f' * (len(pcm) // 4), pcm)
            if name in ISO_PCM:
                source = download('vectors/' + name.removesuffix('.bit') + '.pcm', ISO_PCM[name])
                values = source[:len(pcm)//2]
                a = [v / 32768 for v in struct.unpack('<' + 'h' * (len(values)//2), values)]
                assert len(a) == len(b), name
                assert max(abs(x - y) for x, y in zip(a, b)) <= 1 / 32768, name
            else:
                actual = subprocess.run(['ffmpeg', '-v', 'error', '-f', 'mp3', '-i', str(path), '-f', 'f32le', 'pipe:1'], capture_output=True, check=True).stdout
                assert len(actual) == len(pcm), (name, len(actual), len(pcm))
                a = struct.unpack('<' + 'f' * (len(actual) // 4), actual)
                assert max(abs(x - y) for x, y in zip(a, b)) < 0.0001, name
        (temp / 'mixed8k.mp3').write_bytes(mixed)
        mixed_pcm = subprocess.run(['ffmpeg', '-v', 'error', '-f', 'mp3', '-i', str(temp / 'mixed8k.mp3'), '-f', 'f32le', 'pipe:1'], capture_output=True, check=True).stdout
        assert len(mixed_pcm) == 2304 * 4
    lines = ['// Generated by scripts/gen-mp3-vectors.py.',
             '// minimp3 reference: ' + tables.REV + ' (CC0).',
             'pub struct Vector { name: string, bytes: string, rate: i32, channels: i32, gapless: boolean, samples: i32, crc: u32 }',
             'pub function cases(): Vector[] {', '  return [']
    for name, data, rate, cc, gapless, pcm in cases:
        lines.append(f'    Vector {{ name: "{name}", bytes: "{data.hex()}", rate: {rate}, channels: {cc}, gapless: {str(gapless).lower()}, samples: {len(pcm)//4}, crc: {zlib.crc32(pcm)}u32 }},')
    lines += ['  ];', '}', '', 'pub struct Independent { bytes: string, pcm: string }',
              'pub function mixed8k(): Independent {',
              f'  return Independent {{ bytes: \"{mixed.hex()}\", pcm: \"{mixed_pcm.hex()}\" }};', '}', '']
    path = ROOT / 'tests/stdlib/mp3_vectors.fern'
    path.write_text('\n'.join(lines))
    compiler = os.environ.get('FERN') or subprocess.check_output([str(ROOT / 'bootstrap/bootstrap.sh'), 'stage0'], text=True).strip()
    subprocess.run([compiler, '-fmt', '-w', str(path)], check=True)
    print(f'{len(cases)} scalar golden vectors and one independent 8 kHz mixed-block vector')


if __name__ == '__main__':
    main()
