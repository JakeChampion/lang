"""Build a short AVC/AAC fixture for the lower-level Android controller."""
import json
from pathlib import Path
import subprocess

FRAMES = 19200
RATE = 48000
CHANNELS = 2


def create(video, output):
    subprocess.run([
        'ffmpeg', '-v', 'error', '-y', '-i', str(video), '-f', 'lavfi', '-i',
        'aevalsrc=0.05*sin(2*PI*440*t)|0.05*sin(2*PI*660*t):s=48000:d=0.4',
        '-map', '0:v:0', '-map', '1:a:0', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '128k',
        '-t', '0.4', '-movflags', '+faststart', str(output),
    ], check=True, timeout=30)
    metadata = json.loads(subprocess.check_output([
        'ffprobe', '-v', 'error', '-show_streams', '-of', 'json', str(output),
    ], text=True, timeout=30))
    video, audio = metadata['streams']
    assert video['codec_name'] == 'h264' and int(video['nb_frames']) == 12
    assert audio['codec_name'] == 'aac' and audio['profile'] == 'LC'
    assert int(audio['sample_rate']) == RATE and audio['channels'] == CHANNELS
    assert audio['time_base'] == '1/48000' and int(audio['duration_ts']) == FRAMES
    assert Path(output).stat().st_size <= 2097152
    return metadata


def reference_pcm(path):
    # The native whole-track decoder applies the final presentation trim.
    return subprocess.check_output([
        'ffmpeg', '-v', 'error', '-xerror', '-i', str(path), '-map', '0:a:0',
        '-t', '0.4', '-acodec', 'pcm_s16le', '-f', 's16le', '-',
    ], timeout=30)
