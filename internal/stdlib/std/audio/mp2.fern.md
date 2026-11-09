# Native MPEG Audio Layer II decoder

This module decodes indexed MPEG-1 and MPEG-2 Layer II frames to interleaved float PCM.
It supports mono, stereo, dual channel, and joint stereo.
Protected frames validate the MPEG CRC over the header, allocation fields, and scale-factor selection.
Free-format frames and other MPEG layers remain unsupported.

The decoder retains only synthesis history and format information.
Frame scratch is bounded and does not remain in decoder state.
`decode_frame_into` consumes caller-owned PCM storage and preserves entries after the valid sample prefix on success.
That prefix contains `frame.frames * frame.channels` samples.

Header, limit, and output-size errors preserve decoder history.
Payload errors reset synthesis history and consume the supplied output buffer.
The frame APIs require the header at the supplied byte offset and perform no resynchronization.
The complete-input convenience method requires consecutive compatible frames and applies a cumulative PCM budget.

Allocation, quantization, and synthesis follow scalar minimp3 at `ea99364f61c14656440e8d77e9c233ccf3124633` under CC0.
The module executes Fern code and needs no external codec library.

```fern
<<*>>=
// Decode bounded MPEG Layer II frames with exact scalar-reference operation order.
import "std/audio/mp2/header";
import "std/audio/mp2/layer";
import "std/audio/mp3/bits";
import "std/audio/mp3/spectral";
import "std/audio/mp3/synthesis";
import "std/audio/pcm";
import "std/audio/wire";

pub opaque struct Decoder { qmf: f32[], sample_rate: i32, channels: i32 }

pub struct Frame { sample_rate: i32, channels: i32, frames: i32, bytes_read: i32, samples: f32[] }

pub struct Decoded { sample_rate: i32, channels: i32, samples: f32[] }

pub function decoder(): Decoder {
  return Decoder { qmf: spectral.zeros(960), sample_rate: 0, channels: 0 };
}

function clear(own qmf: f32[]): f32[] {
  for i in 0..qmf.len() {
    qmf = qmf.with(i, 0.0f32);
  }
  return qmf;
}

pub function (own d: Decoder) reset(): Decoder {
  let Decoder { qmf, sample_rate, channels } = d;
  return Decoder { qmf: clear(qmf), sample_rate: 0, channels: 0 };
}

function prepared(own d: Decoder, h: header.Header): Decoder {
  let Decoder { qmf, sample_rate, channels } = d;
  if (sample_rate != h.sample_rate || channels != h.channels) {
    qmf = clear(qmf);
  }
  return Decoder { qmf: qmf, sample_rate: h.sample_rate, channels: h.channels };
}

pub function inspect(input: [u8], at: i32, limits: pcm.Limits): Result[header.Header, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let h = header.parse(input, at)?;
  if (h.bytes > input.len() - at) {
    return Err(pcm.Truncated);
  }
  let _ = pcm.sample_count(1152, h.channels, limits)?;
  return Ok(h);
}

function reconstruct(own d: Decoder, input: [u8], b: bits.Bits, scale: layer.Scale, h: header.Header, own output: f32[]): (Decoder, Result[Frame, pcm.AudioError]) {
  let Decoder { qmf, sample_rate, channels } = d;
  let scratch = spectral.zeros(1152);
  let lines = spectral.zeros(1728);
  let transform = spectral.zeros(128);
  for part in 0..3 {
    let decoded = layer.granule_into(input, b, scale, part, scratch);
    let (next_bits, samples) = match (decoded) {
      Ok(value) => value,
      Err(e) => { return (Decoder { qmf: clear(qmf), sample_rate: 0, channels: 0 }, Err(e)); },
    };
    b = next_bits;
    let (next_qmf, pcm, values, synthesis_lines, transform_values) = synthesis.granule12_into(qmf, samples, h.channels, output, part * 384 * h.channels, lines, transform);
    qmf = next_qmf;
    output = pcm;
    scratch = values;
    lines = synthesis_lines;
    transform = transform_values;
  }
  return (Decoder { qmf: qmf, sample_rate: sample_rate, channels: channels }, Ok(Frame { sample_rate: h.sample_rate, channels: h.channels, frames: 1152, bytes_read: h.bytes, samples: output }));
}

pub function (own d: Decoder) decode_frame_into(input: [u8], at: i32, own output: f32[], limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  let h = match (inspect(input, at, limits)) { Ok(value) => value, Err(e) => { return (d, Err(e)); } };
  if (output.len() < 1152 * h.channels) {
    return (d, Err(pcm.Buffer));
  }
  let start = at + 4 + (if (h.crc) { 2 } else { 0 });
  let payload = input[start:at + h.bytes];
  let (b, scale) = match (layer.read(payload, h)) { Ok(value) => value, Err(e) => { return (d.reset(), Err(e)); } };
  if (h.crc && layer.checksum(input[at + 2:at + 4], payload, scale.protected_bits()) != wire.be(input, at + 4, 2) as i32) {
    return (d.reset(), Err(pcm.Checksum));
  }
  return reconstruct(prepared(d, h), payload, b, scale, h, output);
}

pub function (own d: Decoder) decode_frame(input: [u8], at: i32, limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  let h = match (inspect(input, at, limits)) { Ok(value) => value, Err(e) => { return (d, Err(e)); } };
  return d.decode_frame_into(input, at, spectral.zeros(1152 * h.channels), limits);
}

pub function decode_raw(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  let h = inspect(input, 0, limits)?;
  let state = decoder();
  let buffer = spectral.zeros(1152 * h.channels);
  let output: f32[] = [];
  let at = 0;
  while (at < input.len()) {
    let current = inspect(input, at, limits)?;
    if (current.sample_rate != h.sample_rate || current.channels != h.channels) {
      return Err(pcm.Header);
    }
    let (next, result) = state.decode_frame_into(input, at, buffer, limits);
    state = next;
    let frame = result?;
    let count = frame.frames * frame.channels;
    if (count > limits.max_samples - output.len()) {
      return Err(pcm.Limit);
    }
    for i in 0..count {
      output = output.append(frame.samples[i]);
    }
    at += frame.bytes_read;
    buffer = frame.samples;
  }
  return Ok(Decoded { sample_rate: h.sample_rate, channels: h.channels, samples: output });
}
```
