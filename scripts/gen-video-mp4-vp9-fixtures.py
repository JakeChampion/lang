#!/usr/bin/env python3
"""Create VP9 MP4 references with FFmpeg, CBS, and FFprobe."""
import importlib.util
from pathlib import Path
import struct
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


vp9 = load('vp9_headers', 'gen-video-vp9-fixtures.py')
mp4 = load('mp4_ranges', 'gen-video-mp4-fixtures.py')
ivf = load('ivf_packets', 'gen-video-av1-fixtures.py')


def configuration(data):
    at = data.index(b'vpcC')
    size = struct.unpack_from('>I', data, at - 4)[0]
    record = data[at + 4:at - 4 + size]
    assert len(record) == 12 and record[:4] == b'\x01\0\0\0'
    profile, level, packed, primaries, transfer, matrix, size = struct.unpack('>BBBBBBH', record[4:])
    assert size == 0
    return record, [profile, level, packed >> 4, packed >> 1 & 7, packed & 1, primaries, transfer, matrix]


def private(values):
    return bytes([1, 1, values[0], 2, 1, values[1], 3, 1, values[2], 4, 1, values[3]])


def raw_frames(pixel_format):
    depth = 12 if '12' in pixel_format else 10 if '10' in pixel_format else 8
    luma = 64 * 36
    chroma = luma // 4 if '420' in pixel_format else luma // 2 if '422' in pixel_format else luma
    data = bytearray()
    for frame in range(3):
        for plane, count in enumerate((luma, chroma, chroma)):
            for i in range(count):
                value = (i * 13 + plane * 101 + frame * 37) % (1 << depth)
                data += struct.pack('<H', value) if depth > 8 else bytes([value])
    return bytes(data)


def generate(directory):
    files = []
    for pixel_format, profile in (('yuv420p', 0), ('yuv422p', 1), ('yuv444p', 1), ('gbrp', 1),
                                  ('yuv420p10le', 2), ('yuv420p12le', 2), ('yuv422p10le', 3), ('yuv444p12le', 3)):
        raw = raw_frames(pixel_format)
        source = directory / (pixel_format + '.raw')
        source.write_bytes(raw)
        carrier = directory / (pixel_format + '.ivf')
        rgb = pixel_format == 'gbrp'
        color = ['-colorspace', 'rgb' if rgb else 'bt709', '-color_range', 'pc' if rgb else 'tv',
                 '-color_primaries', 'bt709', '-color_trc', 'bt709']
        subprocess.run(['ffmpeg', '-v', 'error', '-f', 'rawvideo', '-pixel_format', pixel_format,
                        '-video_size', '64x36', '-framerate', '3', *color, '-i', str(source), '-frames:v', '3',
                        '-c:v', 'libvpx-vp9', '-lossless', '1', '-b:v', '0', '-crf', '0',
                        '-pix_fmt', pixel_format, '-cpu-used', '8', '-threads', '1',
                        '-lag-in-frames', '0', '-g', '3', '-profile:v', str(profile), *color,
                        '-f', 'ivf', str(carrier)], check=True)
        target = directory / (pixel_format + '.mp4')
        subprocess.run(['ffmpeg', '-v', 'error', '-i', str(carrier), '-c:v', 'copy', *color,
                        '-tag:v', 'vp09', '-movflags', '+faststart', str(target)], check=True)
        record, values = configuration(target.read_bytes())
        packets = ivf.packets(carrier.read_bytes())
        assert len(packets) == 3
        key = directory / (pixel_format + '-key.ivf')
        key.write_bytes(vp9.ivf(packets[0][1]))
        reference = vp9.reference(key)
        assert values[0] == reference[0] == profile and values[2] == reference[5]
        assert values[3] // 2 == (0 if reference[8:10] == [1, 1] else 1)
        assert values[3] == (2 if reference[8:10] == [1, 0] else 3 if reference[8:10] == [0, 0] else values[3])
        assert values[4] == reference[7]
        metadata = mp4.packets(target)
        stream = metadata['streams'][0]
        assert stream['width'] == 64 and stream['height'] == 36 and len(metadata['packets']) == 3
        assert [stream[k] for k in ('color_primaries', 'color_transfer', 'color_space', 'color_range')] == [
            'bt709', 'bt709', 'gbr' if rgb else 'bt709', 'pc' if rgb else 'tv']
        assert values[5:] == [1, 1, 0 if rgb else 1]
        files.append((pixel_format, target, record, values, metadata, carrier, raw))
    return files


def main():
    lines = ['// Fresh FFmpeg muxer records, CBS keyframes, and FFprobe packet references.',
             'pub struct Packet { offset: i64, bytes: i32, dts: i64, pts: i64, duration: u32, sync: boolean }',
             'pub struct File { name: string, bytes: string, config: string, private: string, values: i32[], scale: u32, packets: Packet[] }',
             'pub function files(): File[] {', '  return [']
    with tempfile.TemporaryDirectory() as temporary:
        for name, path, config, values, metadata, _, _ in generate(Path(temporary)):
            numerator, denominator = map(int, metadata['streams'][0]['time_base'].split('/'))
            assert numerator == 1
            lines.append(f'    File {{ name: "{name}", bytes: "{path.read_bytes().hex()}", config: "{config.hex()}", private: "{private(values).hex()}", values: {values}, scale: {denominator}u32, packets: [')
            for p in metadata['packets']:
                lines.append(f'      Packet {{ offset: {p["pos"]}i64, bytes: {p["size"]}, dts: {p["dts"]}i64, pts: {p["pts"]}i64, duration: {p["duration"]}u32, sync: {str("K" in p["flags"]).lower()} }},')
            lines.append('    ] },')
    lines += ['  ];', '}', '']
    (ROOT / 'tests/stdlib/video_mp4_vp9_vectors.fern').write_text('\n'.join(lines))


if __name__ == '__main__':
    main()
