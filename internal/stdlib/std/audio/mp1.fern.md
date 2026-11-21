# MPEG Layer I playback

`decode_raw` decodes consecutive indexed-bitrate MPEG-1 or MPEG-2 Layer I frames to interleaved float PCM. Mono, stereo, dual-channel and joint-stereo streams retain synthesis history between compatible frames. Protected frames check the header and allocation fields before reading scalefactors. Input and cumulative sample counts use the caller's PCM limits. Tags, free-format frame discovery and de-emphasis filtering are outside this entry point.

```fern
<<*>>=
import "std/audio/mp2/layer";
import "std/audio/mp2/tables";
import "std/audio/mp3/bits";
import "std/audio/mp3/spectral";
import "std/audio/mp3/synthesis";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Decoded { sample_rate: i32, channels: i32, samples: f32[] }

struct Header { rate: i32, channels: i32, separate: i32, bytes: i32, crc: boolean }

const RATES: i32[] = [44100, 48000, 32000];

const LOW_BITRATES: i32[] = [0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256];

function header(input: [u8]): Result[Header, pcm.AudioError] {
  if (input.len() < 4) {
    return Err(pcm.Truncated);
  }
  let a = input[1] as i32;
  let b = input[2] as i32;
  let mode = input[3] as i32 >> 6;
  let version = a >> 3 & 3;
  let rate_index = b >> 2 & 3;
  let bitrate_index = b >> 4;
  if (input[0] != 255u8 || a >> 5 != 7 || version == 1 || (a & 6) == 0 || rate_index == 3 || bitrate_index == 15 || (input[3] & 3u8) == 2u8) {
    return Err(pcm.Header);
  }
  if (version == 0 || (a & 6) != 6 || bitrate_index == 0) {
    return Err(pcm.Unsupported);
  }
  let rate = RATES[rate_index] / (if (version == 3) { 1 } else { 2 });
  let bitrate = if (version == 3) { bitrate_index * 32 } else { LOW_BITRATES[bitrate_index] };
  let bytes = 4 * (12000 * bitrate / rate + (b >> 1 & 1));
  if (bytes > input.len()) {
    return Err(pcm.Truncated);
  }
  return Ok(Header { rate: rate, channels: if (mode == 3) { 1 } else { 2 }, separate: if (mode == 1) { 4 * (1 + (input[3] as i32 >> 4 & 3)) } else { 32 }, bytes: bytes, crc: (a & 1) == 0 });
}

function allocation(data: [u8], h: Header): Result[(bits.Bits, u8[]), pcm.AudioError] {
  let b = bits.new(data.len() * 8);
  let widths = __alloc_u8(64);
  for band in 0..32 {
    let width = 0;
    for ch in 0..h.channels {
      if (ch == 0 || band < h.separate) {
        let (next, value) = bits.read(data, b, 4);
        b = next;
        if (b.bad) {
          return Err(pcm.Truncated);
        }
        if (value == 15) {
          return Err(pcm.Header);
        }
        width = if (value == 0) { 0 } else { value + 1 };
      }
      widths = widths.with(2 * band + ch, width as u8);
    }
  }
  return Ok((b, widths));
}

function subbands(frame: [u8], h: Header): Result[f32[], pcm.AudioError] {
  let data = frame[if (h.crc) { 6 } else { 4 }:];
  let (b, widths) = allocation(data, h)?;
  if (h.crc && layer.checksum(frame[2:4], data, b.pos) != wire.be(frame, 4, 2) as i32) {
    return Err(pcm.Checksum);
  }
  let scales = spectral.zeros(64);
  for i in 0..64 {
    let width = widths[i] as i32;
    if (width != 0) {
      let (next, index) = bits.read(data, b, 6);
      b = next;
      let factor = tables.DEQUANT[3 * (width - 2) + index % 3] * ((2097152 >> index / 3) as f32);
      scales = scales.with(i, factor);
    }
  }
  let samples = spectral.zeros(1152);
  for slot in 0..12 {
    for band in 0..32 {
      let value = 0;
      for ch in 0..h.channels {
        let i = 2 * band + ch;
        let width = widths[i] as i32;
        if (width > 0) {
          if (ch == 0 || band < h.separate) {
            let (next, code) = bits.read(data, b, width);
            b = next;
            value = code - ((1 << width - 1) - 1);
          }
          samples = samples.with(ch * 576 + band * 18 + slot, value as f32 * scales[i]);
        }
      }
    }
  }
  if (b.bad) {
    return Err(pcm.Truncated);
  }
  return Ok(samples);
}

pub function decode_raw(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let first = header(input)?;
  let channels = first.channels;
  let count = pcm.sample_count(384, channels, limits)?;
  let history = spectral.zeros(960);
  let frame = spectral.zeros(count);
  let samples: f32[] = [];
  let at = 0;
  while (at < input.len()) {
    let h = header(input[at:])?;
    if (h.channels != channels || h.rate != first.rate) {
      return Err(pcm.Header);
    }
    if (count > limits.max_samples - samples.len()) {
      return Err(pcm.Limit);
    }
    let bands = subbands(input[at:at + h.bytes], h)?;
    let (next, output) = synthesis.granule12(history, bands, channels, frame, 0);
    history = next;
    for sample in output {
      samples = samples.append(sample);
    }
    frame = output;
    at += h.bytes;
  }
  return Ok(Decoded { sample_rate: first.rate, channels: channels, samples: samples });
}
```
