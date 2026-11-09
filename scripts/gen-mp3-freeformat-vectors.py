#!/usr/bin/env python3
"""Generate free-format MP3 controls with LAME, minimp3, and mpg123."""
import ctypes
import hashlib
import importlib.util
import json
import re
from pathlib import Path
import struct
import subprocess
import tempfile
import zlib

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('mp3_vectors', ROOT / 'scripts/gen-mp3-vectors.py')
vectors = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vectors)


def function(library, name, arguments, result=ctypes.c_int):
    value = getattr(library, name)
    value.argtypes = arguments
    value.restype = result
    return value


def encoder():
    library = ctypes.CDLL('libmp3lame.so.0')
    function(library, 'get_lame_version', [], ctypes.c_char_p)
    assert library.get_lame_version() == b'3.100', 'use LAME 3.100'
    pointer = ctypes.c_void_p
    samples = ctypes.POINTER(ctypes.c_short)
    output = ctypes.POINTER(ctypes.c_ubyte)
    function(library, 'lame_init', [], pointer)
    for name in ['in_samplerate', 'out_samplerate', 'num_channels', 'brate', 'quality',
                 'mode', 'free_format', 'bWriteVbrTag', 'error_protection']:
        function(library, 'lame_set_' + name, [pointer, ctypes.c_int])
    function(library, 'lame_init_params', [pointer])
    function(library, 'lame_encode_buffer_interleaved', [pointer, samples, ctypes.c_int, output, ctypes.c_int])
    function(library, 'lame_encode_buffer', [pointer, samples, samples, ctypes.c_int, output, ctypes.c_int])
    function(library, 'lame_encode_flush', [pointer, output, ctypes.c_int])
    function(library, 'lame_get_lametag_frame', [pointer, output, ctypes.c_size_t], ctypes.c_size_t)
    function(library, 'lame_close', [pointer])
    return library


def encode(library, rate, channels, bitrate, mode, crc, tag, free=True):
    state = library.lame_init()
    assert state
    try:
        settings = {'in_samplerate': rate, 'out_samplerate': rate, 'num_channels': channels,
                    'brate': bitrate, 'quality': 2, 'mode': mode, 'free_format': int(free),
                    'bWriteVbrTag': int(tag), 'error_protection': int(crc)}
        for name, value in settings.items():
            assert getattr(library, 'lame_set_' + name)(state, value) == 0, (name, value)
        assert library.lame_init_params(state) == 0
        frames = rate // 8
        values = vectors.signal(rate, channels, frames)
        pcm = (ctypes.c_short * len(values))(*values)
        buffer = (ctypes.c_ubyte * (len(values) * 2 + 16384))()
        if channels == 2:
            count = library.lame_encode_buffer_interleaved(state, pcm, frames, buffer, len(buffer))
        else:
            count = library.lame_encode_buffer(state, pcm, pcm, frames, buffer, len(buffer))
        assert count >= 0
        data = bytes(buffer[:count])
        count = library.lame_encode_flush(state, buffer, len(buffer))
        assert count >= 0
        data += bytes(buffer[:count])
        if tag:
            count = library.lame_get_lametag_frame(state, buffer, len(buffer))
            assert 4 <= count <= len(data)
            data = bytes(buffer[:count]) + data[count:]
        return data, settings, frames
    finally:
        library.lame_close(state)


def alternative_decoder():
    library = ctypes.CDLL('libmpg123.so.0')
    pointer = ctypes.c_void_p
    function(library, 'mpg123_init', [])
    assert library.mpg123_init() == 0
    function(library, 'mpg123_new', [ctypes.c_char_p, ctypes.POINTER(ctypes.c_int)], pointer)
    function(library, 'mpg123_param', [pointer, ctypes.c_int, ctypes.c_long, ctypes.c_double])
    function(library, 'mpg123_open_feed', [pointer])
    function(library, 'mpg123_getformat', [pointer, ctypes.POINTER(ctypes.c_long),
                                        ctypes.POINTER(ctypes.c_int), ctypes.POINTER(ctypes.c_int)])
    function(library, 'mpg123_decode', [pointer, pointer, ctypes.c_size_t, pointer,
                                     ctypes.c_size_t, ctypes.POINTER(ctypes.c_size_t)])
    function(library, 'mpg123_delete', [pointer], None)
    return library


def decode_alternative(library, data, gapless):
    error = ctypes.c_int()
    state = library.mpg123_new(None, ctypes.byref(error))
    assert state
    try:
        # FLAGS replaces the defaults. Request quiet float output and explicit gapless policy.
        assert library.mpg123_param(state, 1, 0x20 | 0x400 | (0x40 if gapless else 0), 0) == 0
        assert library.mpg123_open_feed(state) == 0
        source = ctypes.create_string_buffer(data)
        output = ctypes.create_string_buffer(262144)
        size = ctypes.c_size_t()
        answer = bytearray()
        first = True
        while True:
            status = library.mpg123_decode(state, source if first else None, len(data) if first else 0,
                                           output, len(output), ctypes.byref(size))
            first = False
            answer += output.raw[:size.value]
            if status == -11:
                rate, channels, encoding = ctypes.c_long(), ctypes.c_int(), ctypes.c_int()
                assert library.mpg123_getformat(state, ctypes.byref(rate), ctypes.byref(channels), ctypes.byref(encoding)) == 0
                assert encoding.value == 0x200
            elif status == -10:
                return bytes(answer)
            else:
                assert status == 0, status
    finally:
        library.mpg123_delete(state)


def library_hash(name):
    paths = [Path(line.split()[-1]) for line in Path('/proc/self/maps').read_text().splitlines()
             if name in line and line.split()[-1].startswith('/')]
    return hashlib.sha256(paths[0].read_bytes()).hexdigest()


def protected_playable(data, raw, base, rate, channels):
    # The pinned minimp3 file helper misses protected LAME Info tags.
    # Trim raw PCM from the independent LAME fields.
    # Compare every sample with mpg123.
    side = (17 if channels == 1 else 32) if rate >= 32000 else (9 if channels == 1 else 17)
    tag = 4 + side
    assert data[tag:tag + 4] == b'Info'
    flags = int.from_bytes(data[tag + 4:tag + 8], 'big')
    cursor = tag + 8
    for flag, size in [(1, 4), (2, 4), (4, 100), (8, 4)]:
        if flags & flag:
            cursor += size
    assert data[cursor:cursor + 4] == b'LAME' and cursor + 36 <= base + (data[2] >> 1 & 1)
    delay = data[cursor + 21] << 4 | data[cursor + 22] >> 4
    padding = (data[cursor + 22] & 15) << 8 | data[cursor + 23]
    first = (1152 if rate >= 32000 else 576) + delay + 529
    last = len(raw) - max(0, padding - 529) * channels * 4
    return raw[first * channels * 4:last]


def main():
    lame = encoder()
    mpg123 = alternative_decoder()
    cases = [
        ('mpeg1-44100-joint-500', 44100, 2, 500, 1, False, False),
        ('mpeg1-48000-mono-384', 48000, 1, 384, 3, False, False),
        ('mpeg1-32000-stereo-192', 32000, 2, 192, 0, False, False),
        ('mpeg1-44100-joint-crc', 44100, 2, 128, 1, True, False),
        ('mpeg2-22050-joint-80', 22050, 2, 80, 1, False, False),
        ('mpeg2-24000-stereo-80', 24000, 2, 80, 0, False, False),
        ('mpeg2-16000-mono-crc', 16000, 1, 96, 3, True, False),
        ('mpeg25-11025-joint-48', 11025, 2, 48, 1, False, False),
        ('mpeg25-12000-stereo-48', 12000, 2, 48, 0, False, False),
        ('mpeg25-8000-mono-48', 8000, 1, 48, 3, False, False),
        ('mpeg1-44100-gapless', 44100, 2, 128, 1, False, True),
        ('mpeg2-22050-gapless-crc', 22050, 2, 80, 1, True, True),
        ('mpeg1-44100-gapless-crc', 44100, 2, 128, 1, True, True),
        ('mpeg1-48000-mono-gapless-crc', 48000, 1, 128, 3, True, True),
        ('mpeg25-8000-mono-gapless-crc', 8000, 1, 48, 3, True, True),
    ]
    cases = [(*row, True) for row in cases]
    cases += [('indexed-mpeg2-gapless-crc', 22050, 2, 80, 1, True, True, False),
              ('indexed-mpeg1-gapless-crc', 44100, 2, 128, 1, True, True, False)]
    rows, metadata = [], []
    with tempfile.TemporaryDirectory() as directory:
        work = Path(directory)
        reference = vectors.reference(work)
        for name, rate, channels, bitrate, mode, crc, tag, free in cases:
            data, settings, frames = encode(lame, rate, channels, bitrate, mode, crc, tag, free)
            base = (144000 if rate >= 32000 else 72000) * bitrate // rate
            at, padding, reservoir = 0, [], []
            while at < len(data):
                assert data[at] == 255 and (data[at + 2] >> 4 == 0) == free, (name, at)
                pad = data[at + 2] >> 1 & 1
                padding.append(pad)
                side = at + 4 + (2 if crc else 0)
                reservoir.append((data[side] << 1 | data[side + 1] >> 7) if rate >= 32000 else data[side])
                at += base + pad
            assert at == len(data), (name, at, len(data))
            raw = vectors.pcm_of(work, reference, data)
            if tag and crc:
                playable = protected_playable(data, raw, base, rate, channels)
            else:
                playable = vectors.pcm_of(work, reference, data, gapless=True) if tag else raw
            alternative = decode_alternative(mpg123, data, tag)
            values = struct.unpack('<' + 'f' * (len(playable) // 4), playable)
            other = struct.unpack('<' + 'f' * (len(alternative) // 4), alternative)
            assert len(values) == len(other), (name, len(values), len(other))
            error = max(abs(a - b) for a, b in zip(values, other))
            assert error < 0.00002, (name, error)
            if tag:
                assert len(values) == frames * channels, (name, len(values), frames)
            rows.append((name, rate, channels, base, free, len(raw) // 4, zlib.crc32(raw),
                         len(playable) // 4, zlib.crc32(playable), data.hex()))
            metadata.append({'name': name, 'settings': settings, 'input_frames': frames,
                             'encoded_sha256': hashlib.sha256(data).hexdigest(),
                             'raw_pcm_sha256': hashlib.sha256(raw).hexdigest(),
                             'playable_pcm_sha256': hashlib.sha256(playable).hexdigest(),
                             'mpg123_pcm_sha256': hashlib.sha256(alternative).hexdigest(),
                             'mpg123_max_absolute_error': error, 'padding_bits': sorted(set(padding)),
                             'metadata_reference': 'raw minimp3 and independent LAME trim' if tag and crc else 'scalar minimp3',
                             'max_reservoir_bytes': max(reservoir), 'encoded_frames': len(padding)})
            print(name, 'verified', len(data), 'bytes', len(values), 'samples', flush=True)
        original = (ROOT / 'tests/stdlib/mp3_seek_smoke_vectors.fern').read_text()
        indexed = bytes.fromhex(re.search(r'pub const INPUT: string = "([0-9a-f]+)"', original)[1])
        assert indexed[1] & 1
        free = indexed[:2] + bytes([indexed[2] & 15]) + indexed[3:]
        smoke_input = free * 3
        indexed_pcm = vectors.pcm_of(work, reference, indexed)
        smoke_pcm = vectors.pcm_of(work, reference, smoke_input)
        assert len(indexed_pcm) == 576 * 4 and smoke_pcm[:len(indexed_pcm)] == indexed_pcm
        alternative = decode_alternative(mpg123, smoke_input, False)
        first = struct.unpack('<' + 'f' * (len(smoke_pcm) // 4), smoke_pcm)
        other = struct.unpack('<' + 'f' * (len(alternative) // 4), alternative)
        assert len(first) == len(other)
        # This smoke repeats an isolated frame. Compare its independent first frame.
        smoke_error = max(abs(a - b) for a, b in zip(first[:576], other[:576]))
        assert smoke_error < 0.00002
        position = 537
        sample_bits = struct.unpack('<' + 'I' * (576 - position), indexed_pcm[position * 4:])
        assert any(value != 0 for value in sample_bits)
        smoke = ('// Generated by scripts/gen-mp3-freeformat-vectors.py.\n\n'
                 'pub const INPUT: string = "' + smoke_input.hex() + '";\n\n'
                 'pub const FRAME_BYTES: i32 = ' + str(len(indexed)) + ';\n\n'
                 'pub const POSITION: i64 = ' + str(position) + 'i64;\n\n'
                 'pub const SAMPLE_BITS: u32[] = [' + ', '.join(str(value) + 'u32' for value in sample_bits) + '];\n')
        (ROOT / 'tests/stdlib/mp3_freeformat_smoke_vectors.fern').write_text(smoke)
    source = ['// Generated by scripts/gen-mp3-freeformat-vectors.py.',
              '',
              'pub struct Case { name: string, rate: i32, channels: i32, base: i32, free: boolean, raw_samples: i32, raw_crc: u32, samples: i32, crc: u32, bytes: string }',
              '',
              'pub function cases(): Case[] {', '  return [']
    for name, rate, channels, base, free, raw_count, raw_crc, count, crc, data in rows:
        source.append(f'    Case {{ name: "{name}", rate: {rate}, channels: {channels}, base: {base}, free: {str(free).lower()}, raw_samples: {raw_count}, raw_crc: {raw_crc}u32, samples: {count}, crc: {crc}u32, bytes: "{data}" }},')
    source += ['  ];', '}', '']
    (ROOT / 'tests/stdlib/mp3_freeformat_vectors.fern').write_text('\n'.join(source))
    manifest = {'lame': '3.100', 'lame_sha256': library_hash('libmp3lame.so'),
                'mpg123_sha256': library_hash('libmpg123.so'), 'minimp3_revision': vectors.tables.REV,
                'minimp3_header_sha256': vectors.tables.SHA, 'cases': metadata,
                'smoke': {'input_sha256': hashlib.sha256(smoke_input).hexdigest(),
                          'indexed_input_sha256': hashlib.sha256(indexed).hexdigest(),
                          'raw_pcm_sha256': hashlib.sha256(smoke_pcm).hexdigest(),
                          'first_frame_pcm_sha256': hashlib.sha256(indexed_pcm).hexdigest(),
                          'mpg123_pcm_sha256': hashlib.sha256(alternative).hexdigest(),
                          'frame_bytes': len(indexed), 'position': position,
                          'mpg123_compared_samples': 576, 'mpg123_max_absolute_error': smoke_error}}
    (ROOT / 'scripts/audio-reference/mp3-freeformat.json').write_text(json.dumps(manifest, indent=2) + '\n')


if __name__ == '__main__':
    main()
