# Native MPEG Audio Layer II decoder

This module decodes indexed and free-format MPEG-1 and MPEG-2 Layer II frames to interleaved float PCM.
It supports mono, stereo, dual channel, and joint stereo.
Protected frames validate the MPEG CRC over the header, allocation fields, and scale-factor selection.
MPEG-2.5 and other MPEG layers remain unsupported.

A fresh free-format frame needs the compatible header that follows it, or a second complete frame at the end of the input.
The decoder then keeps the unpadded extent, so later frames and the final frame need no lookahead.
Payload errors keep the extent. `reset` clears it. Reset the decoder before a frame from a different stream.

The decoder retains only synthesis history, format information, and the free-format extent.
Frame scratch is bounded and does not remain in decoder state.

`workspace` allocates 3200 float elements and 128 bytes for repeated frame decode.
`decode_frame_with` consumes and returns this workspace on every call.
The caller can release it independently of decoder history.
Unique workspace arrays retain storage across successful frames. Payload errors can replace scratch storage.
The complete-input decoder reuses one workspace across its frames.

`decode_frame_into` consumes caller-owned PCM storage and preserves entries after the valid sample prefix on success.
That prefix contains `frame.frames * frame.channels` samples.

Header, limit, and output-size errors preserve decoder history.
Payload errors reset synthesis history and consume the supplied output buffer.
The frame APIs require the header at the supplied byte offset and perform no resynchronization.
The complete-input convenience method requires consecutive compatible frames and applies a cumulative PCM budget.

Allocation, quantization, and synthesis follow scalar minimp3 at `ea99364f61c14656440e8d77e9c233ccf3124633` under CC0.
The module executes Fern code and needs no external codec library.

`seek_index` builds a bounded header index. `read_into` reads a range in PCM frames from the same immutable bytes and preserves the output suffix. It decodes at most one prior frame to restore synthesis history.

```fern
<<*>>=
// Decode bounded MPEG Layer II frames with exact scalar-reference operation order.
import "std/audio/mp2/header";
import "std/audio/mp2/layer";
import "std/audio/mp3/bits";
import "std/audio/mp3/spectral";
import "std/audio/mp3/synthesis";
import "std/audio/pcm";
import "std/audio/mpeg/seek";
import "std/audio/wire";

pub opaque struct Decoder { qmf: f32[], sample_rate: i32, channels: i32, free_bytes: i32 }

pub opaque struct Workspace { scratch: f32[], lines: f32[], transform: f32[], scale: layer.Scale }

pub function workspace(): Workspace {
  return Workspace { scratch: spectral.zeros(1152), lines: spectral.zeros(1728), transform: spectral.zeros(128), scale: layer.empty() };
}

pub struct Frame { sample_rate: i32, channels: i32, frames: i32, bytes_read: i32, samples: f32[] }

pub struct Decoded { sample_rate: i32, channels: i32, samples: f32[] }

pub function decoder(): Decoder {
  return Decoder { qmf: spectral.zeros(960), sample_rate: 0, channels: 0, free_bytes: 0 };
}

function clear(own qmf: f32[]): f32[] {
  for i in 0..qmf.len() {
    qmf = qmf.with(i, 0.0f32);
  }
  return qmf;
}

pub function (own d: Decoder) reset(): Decoder {
  let Decoder { qmf, sample_rate, channels, free_bytes } = d;
  return Decoder { qmf: clear(qmf), sample_rate: 0, channels: 0, free_bytes: 0 };
}

function failed(own d: Decoder, h: header.Header): Decoder {
  let Decoder { qmf, sample_rate, channels, free_bytes } = d;
  return Decoder { qmf: clear(qmf), sample_rate: 0, channels: 0, free_bytes: header.extent(h, free_bytes) };
}

function prepared(own d: Decoder, h: header.Header): Decoder {
  let Decoder { qmf, sample_rate, channels, free_bytes } = d;
  if (sample_rate != h.sample_rate || channels != h.channels) {
    qmf = clear(qmf);
  }
  return Decoder { qmf: qmf, sample_rate: h.sample_rate, channels: h.channels, free_bytes: header.extent(h, free_bytes) };
}

pub function inspect(input: [u8], at: i32, limits: pcm.Limits): Result[header.Header, pcm.AudioError] {
  return located(input, at, 0, limits);
}

function located(input: [u8], at: i32, free_bytes: i32, limits: pcm.Limits): Result[header.Header, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let h = header.resolve(input, at, header.parse(input, at)?, free_bytes, limits.max_input_bytes)?;
  if (h.bytes > input.len() - at) {
    return Err(pcm.Truncated);
  }
  let _ = pcm.sample_count(1152, h.channels, limits)?;
  return Ok(h);
}

function reconstruct(own d: Decoder, input: [u8], b: bits.Bits, h: header.Header, own output: f32[], own work: Workspace): (Decoder, Workspace, Result[Frame, pcm.AudioError]) {
  let Decoder { qmf, sample_rate, channels, free_bytes } = d;
  let Workspace { scratch, lines, transform, scale } = work;
  for part in 0..3 {
    let decoded = layer.granule_into(input, b, scale, part, scratch);
    let (next_bits, samples) = match (decoded) {
      Ok(value) => value,
      Err(e) => { return (Decoder { qmf: clear(qmf), sample_rate: 0, channels: 0, free_bytes: free_bytes }, workspace(), Err(e)); },
    };
    b = next_bits;
    let (next_qmf, pcm, values, synthesis_lines, transform_values) = synthesis.granule12_into(qmf, samples, h.channels, output, part * 384 * h.channels, lines, transform);
    qmf = next_qmf;
    output = pcm;
    scratch = values;
    lines = synthesis_lines;
    transform = transform_values;
  }
  return (Decoder { qmf: qmf, sample_rate: sample_rate, channels: channels, free_bytes: free_bytes }, Workspace { scratch: scratch, lines: lines, transform: transform, scale: scale }, Ok(Frame { sample_rate: h.sample_rate, channels: h.channels, frames: 1152, bytes_read: h.bytes, samples: output }));
}

function reconstruct_frame(own d: Decoder, input: [u8], b: bits.Bits, scale: layer.Scale, h: header.Header, own output: f32[]): (Decoder, Result[Frame, pcm.AudioError]) {
  let Decoder { qmf, sample_rate, channels, free_bytes } = d;
  let scratch = spectral.zeros(1152);
  let lines = spectral.zeros(1728);
  let transform = spectral.zeros(128);
  for part in 0..3 {
    let decoded = layer.granule_into(input, b, scale, part, scratch);
    let (next_bits, samples) = match (decoded) {
      Ok(value) => value,
      Err(e) => { return (Decoder { qmf: clear(qmf), sample_rate: 0, channels: 0, free_bytes: free_bytes }, Err(e)); },
    };
    b = next_bits;
    let (next_qmf, pcm, values, synthesis_lines, transform_values) = synthesis.granule12_into(qmf, samples, h.channels, output, part * 384 * h.channels, lines, transform);
    qmf = next_qmf;
    output = pcm;
    scratch = values;
    lines = synthesis_lines;
    transform = transform_values;
  }
  return (Decoder { qmf: qmf, sample_rate: sample_rate, channels: channels, free_bytes: free_bytes }, Ok(Frame { sample_rate: h.sample_rate, channels: h.channels, frames: 1152, bytes_read: h.bytes, samples: output }));
}

pub function (own d: Decoder) decode_frame_into(input: [u8], at: i32, own output: f32[], limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  let h = match (located(input, at, d.free_bytes, limits)) { Ok(value) => value, Err(e) => { return (d, Err(e)); } };
  if (output.len() < 1152 * h.channels) {
    return (d, Err(pcm.Buffer));
  }
  let start = at + 4 + (if (h.crc) { 2 } else { 0 });
  let payload = input[start:at + h.bytes];
  let (b, scale) = match (layer.read(payload, h)) { Ok(value) => value, Err(e) => { return (failed(d, h), Err(e)); } };
  if (h.crc && layer.checksum(input[at + 2:at + 4], payload, scale.protected_bits()) != wire.be(input, at + 4, 2) as i32) {
    return (failed(d, h), Err(pcm.Checksum));
  }
  return reconstruct_frame(prepared(d, h), payload, b, scale, h, output);
}

pub function (own d: Decoder) decode_frame_with(input: [u8], at: i32, own output: f32[], own work: Workspace, limits: pcm.Limits): (Decoder, Workspace, Result[Frame, pcm.AudioError]) {
  let h = match (located(input, at, d.free_bytes, limits)) { Ok(value) => value, Err(e) => { return (d, work, Err(e)); } };
  if (output.len() < 1152 * h.channels) {
    return (d, work, Err(pcm.Buffer));
  }
  let start = at + 4 + (if (h.crc) { 2 } else { 0 });
  let payload = input[start:at + h.bytes];
  let Workspace { scratch, lines, transform, scale } = work;
  let (scales, read) = layer.read_into(payload, h, scale);
  let work = Workspace { scratch: scratch, lines: lines, transform: transform, scale: scales };
  let b = match (read) { Ok(value) => value, Err(e) => { return (failed(d, h), work, Err(e)); } };
  if (h.crc && layer.checksum(input[at + 2:at + 4], payload, work.scale.protected_bits()) != wire.be(input, at + 4, 2) as i32) {
    return (failed(d, h), work, Err(pcm.Checksum));
  }
  return reconstruct(prepared(d, h), payload, b, h, output, work);
}

// Decode one delimited packet with the same scratch workspace as decode_frame_with.
pub function (own d: Decoder) decode_packet_with(packet: [u8], own output: f32[], own work: Workspace, limits: pcm.Limits): (Decoder, Workspace, Result[Frame, pcm.AudioError]) {
  let h = match (header.parse(packet, 0)) { Ok(value) => value, Err(e) => { return (d, work, Err(e)); } };
  let free = if (h.bitrate == 0 && d.free_bytes == 0) { packet.len() - h.padding } else { d.free_bytes };
  let sized = match (header.resolve(packet, 0, h, free, limits.max_input_bytes)) { Ok(value) => value, Err(e) => { return (d, work, Err(e)); } };
  if (sized.bytes != packet.len()) {
    return (d, work, Err(pcm.Header));
  }
  return Decoder { ...d, free_bytes: free }.decode_frame_with(packet, 0, output, work, limits);
}

pub function (own d: Decoder) decode_frame(input: [u8], at: i32, limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  let h = match (located(input, at, d.free_bytes, limits)) { Ok(value) => value, Err(e) => { return (d, Err(e)); } };
  return d.decode_frame_into(input, at, spectral.zeros(1152 * h.channels), limits);
}

pub function decode_raw(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  let h = inspect(input, 0, limits)?;
  let state = decoder();
  let work = workspace();
  let buffer = spectral.zeros(1152 * h.channels);
  let output: f32[] = [];
  let at = 0;
  while (at < input.len()) {
    let current = located(input, at, state.free_bytes, limits)?;
    if (current.sample_rate != h.sample_rate || current.channels != h.channels) {
      return Err(pcm.Header);
    }
    let (next, scratch, result) = state.decode_frame_with(input, at, buffer, work, limits);
    state = next;
    work = scratch;
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

pub opaque struct SeekIndex { index: seek.Index }

function seek_frame(input: [u8], free: i32, limits: pcm.Limits): Result[seek.Frame, pcm.AudioError] {
  let h = located(input, 0, free, limits)?;
  return Ok(seek.Frame { rate: h.sample_rate, channels: h.channels, bytes: h.bytes, free: header.extent(h, free) });
}

pub function seek_index(input: [u8], max_entries: i32, limits: pcm.Limits): Result[SeekIndex, pcm.AudioError] {
  return Ok(SeekIndex { index: seek.build(input, 1152, max_entries, limits, seek_frame)? });
}

pub function (value: SeekIndex) frames(): i64 {
  return value.index.frames();
}

pub function (value: SeekIndex) read_into(input: [u8], position: i64, frames: i32, own output: f32[]): Result[f32[], pcm.AudioError] {
  let index = value.index;
  let range = index.range(input.len(), position, frames, output.len())?;
  if (range.count == 0) {
    return Ok(output);
  }
  let state = Decoder { ...decoder(), free_bytes: index.free };
  let work = workspace();
  let scratch = spectral.zeros(1152 * index.channels);
  let written = 0;
  for n in range.warmup..range.last + 1 {
    let start = index.offsets[n];
    let end = index.end(n);
    let (next, reused, result) = state.decode_frame_with(input[start:end], 0, scratch, work, index.limits);
    work = reused;
    let decoded = result?;
    if (decoded.bytes_read != end - start || decoded.channels != index.channels || decoded.sample_rate != index.rate) {
      return Err(pcm.Header);
    }
    state = next;
    scratch = decoded.samples;
    let (after, filled) = range.copy(n, scratch, written, output);
    written = after;
    output = filled;
  }
  return Ok(output);
}
```
