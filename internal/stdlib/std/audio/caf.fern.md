# Native CAF ALAC playback

This module indexes complete Core Audio Format byte views and decodes ALAC packets.
It supports version-one files, bounded packet tables, initial and final sample trim, and exact byte-view seeks.
The caller must supply the same unchanged input view after inspection.
Other CAF codecs remain unsupported.

The index retains configuration and scalar packet locations and positions.
Logical packet storage uses sixteen bytes per packet, plus an eight-byte terminal position.
Array capacity and runtime overhead add to that count.
Inspection checks packet headers and sample extents. Reads validate selected packet payloads.

Each cursor step decodes at most one packet. ALAC needs no earlier packet after a seek.
An audio event retains the decoded packet buffer and declares its playable range with `start` and `frames`.
Both values count frames. The playable samples are `samples[start * channels:(start + frames) * channels]`.
This avoids a PCM copy for initial or final trim.

CAF can declare nominal final-packet capacity beyond the actual partial ALAC packet.
The playable end cannot exceed the actual decoded packet extents.
Explicit CAF channel layouts control the output speaker mask and WAVE channel order.
The byte-view API performs no file I/O and retains no encoded input.
Errors consume cursors.

```fern
<<*>>=
// Index CAF ALAC packets and decode bounded sample ranges without a platform codec.

import "std/audio/alac";
import "std/audio/alac/config";
import "std/audio/caf/chunks";
import "std/audio/pcm";
import "std/audio/wire";
import "std/f64";

pub struct Limits { audio: pcm.Limits, max_chunks: i32, max_packets: i32 }

pub struct Info { format: pcm.Format, channel_mask: u32, frames: i64, priming: i64 }

pub opaque struct Index { info: Info, configuration: u8[], offsets: i32[], sizes: i32[], positions: i64[], bytes: i32, limits: Limits }

pub opaque struct Cursor { index: Index, codec: alac.Decoder, packet: i32, target: i64 }

pub struct Audio { format: pcm.Format, channel_mask: u32, frames: i32, start: i32, position: i64, packet: i32, samples: i32[] }

pub struct Decoded { info: Info, samples: i32[] }

pub enum ReadEvent { Pending, Samples(Audio), Finished }

struct Description { rate: i32, channels: i32, bytes: i32, frames: i32, depth: i32 }

struct Table { count: i32, valid: i64, priming: i64, remainder: i64, at: i32, end: i32, declared: boolean }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_chunks: 65536, max_packets: 1048576 };
}

// Check the complete fixed description before any packet or PCM allocation.
// fern-lint: allow cyclomatic-complexity
function description(input: [u8], chunk: chunks.Chunk): Result[Description, pcm.AudioError] {
  if (chunk.end - chunk.start != 32) {
    return Err(pcm.Header);
  }
  let at = chunk.start;
  let rate = f64_from_bits(wire.be(input, at, 8) as i64);
  if (!rate.is_finite() || rate < 1.0 || rate > 16777215.0 || (rate as i32) as f64 != rate) {
    return Err(pcm.Header);
  }
  if (!wire.tag(input, at + 8, "alac") || wire.be(input, at + 12, 4) != 0u64) {
    return Err(pcm.Unsupported);
  }
  let bytes = wire.be(input, at + 16, 4);
  let frames = wire.be(input, at + 20, 4);
  let channels = wire.be(input, at + 24, 4);
  let depth = wire.be(input, at + 28, 4);
  if (bytes > 2147483647u64 || frames > 2147483647u64 || depth > 32u64 || channels < 1u64 || channels > 8u64) {
    return Err(pcm.Header);
  }
  return Ok(Description { rate: rate as i32, channels: channels as i32, bytes: bytes as i32, frames: frames as i32, depth: depth as i32 });
}

// Accept raw configuration and the historical frma/alac atom wrappers.
// fern-lint: allow cyclomatic-complexity
function cookie(input: [u8], chunk: chunks.Chunk): Result[u8[], pcm.AudioError] {
  let at = chunk.start;
  if (chunk.end - at > 72) {
    return Err(pcm.Header);
  }
  if (chunk.end - at >= 12 && wire.tag(input, at + 4, "frma")) {
    if (wire.be(input, at, 4) != 12u64 || !wire.tag(input, at + 8, "alac")) {
      return Err(pcm.Header);
    }
    at += 12;
  }
  if (chunk.end - at >= 12 && wire.tag(input, at + 4, "alac")) {
    if (wire.be(input, at, 4) != 36u64 || wire.be(input, at + 8, 4) != 0u64) {
      return Err(pcm.Header);
    }
    at += 12;
  }
  if (chunk.end - at != 24 && chunk.end - at != 48) {
    return Err(pcm.Header);
  }
  let output: u8[] = [];
  for byte in input[at:chunk.end] {
    output = output.append(byte);
  }
  return Ok(output);
}

function back_mask(channels: i32): u32 {
  return match (channels) { 5 => 55u32, 6 => 63u32, 7 => 319u32, 8 => 255u32, _ => config.mask(channels) };
}

// The bounded Core Audio layout table identifies the supported speaker sets.
// fern-lint: allow cyclomatic-complexity
function layout_tag(tag: u32, channels: i32): Result[u32, pcm.AudioError] {
  if ((tag & 65535u32) != channels as u32) {
    return Err(pcm.Header);
  }
  let kind = tag >> 16u32;
  let supported = match (channels) {
    1 => kind == 100u32, 2 => kind == 101u32, 3 => kind == 112u32 || kind == 113u32,
    4 => kind == 115u32 || kind == 116u32, 5 => kind >= 117u32 && kind <= 120u32,
    6 => kind >= 121u32 && kind <= 124u32, 7 => kind == 125u32 || kind == 142u32,
    _ => kind == 127u32,
  };
  if (!supported) {
    return Err(pcm.Unsupported);
  }
  return Ok(back_mask(channels));
}

function channel_mask(input: [u8], value: Option[chunks.Chunk], info: config.Info): Result[u32, pcm.AudioError] {
  let Some(chunk) = value else {
    return Ok(info.channel_mask);
  };
  if (chunk.end - chunk.start != 12) {
    return Err(pcm.Unsupported);
  }
  let tag = wire.be(input, chunk.start, 4) as u32;
  let mask = wire.be(input, chunk.start + 4, 4) as u32;
  if (wire.be(input, chunk.start + 8, 4) != 0u64) {
    return Err(pcm.Unsupported);
  }
  if (tag != 65536u32) {
    if (mask != 0u32) {
      return Err(pcm.Header);
    }
    return layout_tag(tag, info.format.channels);
  }
  if (mask != info.channel_mask && mask != back_mask(info.format.channels)) {
    return Err(pcm.Unsupported);
  }
  return Ok(mask);
}

function variable(input: [u8], at: i32, end: i32): Result[(i32, i32), pcm.AudioError] {
  let value = 0u64;
  for _ in 0..5 {
    if (at >= end) {
      return Err(pcm.Truncated);
    }
    let byte = input[at];
    at += 1;
    value = value << 7u64 | (byte & 127u8) as u64;
    if (value > 2147483647u64) {
      return Err(pcm.Limit);
    }
    if ((byte & 128u8) == 0u8) {
      return Ok((at, value as i32));
    }
  }
  return Err(pcm.Header);
}

// Packet counts and signed presentation fields share the same bounded table extent.
// fern-lint: allow cyclomatic-complexity
function table(input: [u8], file: chunks.File, desc: Description, maximum: i32): Result[Table, pcm.AudioError] {
  let Some(chunk) = file.packets else {
    if (desc.bytes < 1 || (file.data.end - file.data.start) % desc.bytes != 0) {
      return Err(pcm.Header);
    }
    let count = (file.data.end - file.data.start) / desc.bytes;
    if (count > maximum) {
      return Err(pcm.Limit);
    }
    return Ok(Table { count: count, valid: 0i64, priming: 0i64, remainder: 0i64, at: 0, end: 0, declared: false });
  };
  if (chunk.end - chunk.start < 24) {
    return Err(pcm.Truncated);
  }
  let count = wire.be(input, chunk.start, 8);
  let valid = wire.be(input, chunk.start + 8, 8);
  let priming = wire.be(input, chunk.start + 16, 4);
  let remainder = wire.be(input, chunk.start + 20, 4);
  if (count > maximum as u64) {
    return Err(pcm.Limit);
  }
  if (valid > 9223372036854775807u64 || priming > 2147483647u64 || remainder > 2147483647u64) {
    return Err(pcm.Header);
  }
  return Ok(Table { count: count as i32, valid: valid as i64, priming: priming as i64, remainder: remainder as i64, at: chunk.start + 24, end: chunk.end, declared: true });
}

function presentation(value: Table, raw: i64, capacity: i64, last_padding: i64): Result[i64, pcm.AudioError] {
  if (!value.declared) {
    return Ok(raw);
  }
  let Some(end) = value.priming +? value.valid else {
    return Err(pcm.Limit);
  };
  let Some(total) = end +? value.remainder else {
    return Err(pcm.Limit);
  };
  if (total > capacity || value.priming > raw || end > raw && end - raw > last_padding) {
    return Err(pcm.Header);
  }
  return Ok((if (end < raw) { end } else { raw }) - value.priming);
}

// Check each packet extent and frame count before its scalar index entry.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (limits.max_packets < 1 || limits.max_chunks < 1) {
    return Err(pcm.Limit);
  }
  let file = chunks.inspect(input, limits.max_chunks)?;
  let desc = description(input, file.description)?;
  let Some(specific) = file.cookie else {
    return Err(pcm.Header);
  };
  let configuration = cookie(input, specific)?;
  let info = config.read(configuration, limits.audio)?;
  if (desc.rate != info.format.sample_rate || desc.channels != info.format.channels || desc.depth != 0 && desc.depth != info.format.bits_per_sample || desc.frames != 0 && desc.frames != info.frame_length) {
    return Err(pcm.Header);
  }
  let mask = channel_mask(input, file.channels, info)?;
  let plan = table(input, file, desc, limits.max_packets)?;
  let decoder = alac.create(configuration, limits.audio)?;
  let offsets: i32[] = [];
  let sizes: i32[] = [];
  let positions: i64[] = [0i64];
  let at = file.data.start;
  let row = plan.at;
  let total = 0i64;
  let capacity = 0i64;
  let last_padding = 0i64;
  for _ in 0..plan.count {
    let bytes = desc.bytes;
    if (bytes == 0) {
      let (next, value) = variable(input, row, plan.end)?;
      row = next;
      bytes = value;
    }
    let nominal = desc.frames;
    if (nominal == 0) {
      let (next_frames, value) = variable(input, row, plan.end)?;
      row = next_frames;
      nominal = value;
    }
    if (bytes < 1 || bytes > file.data.end - at || nominal < 1 || nominal > info.frame_length) {
      return Err(pcm.Header);
    }
    let frames = decoder.packet_frames(input[at:at + bytes], limits.audio)?;
    if (frames > nominal || desc.frames == 0 && frames != nominal) {
      return Err(pcm.Header);
    }
    offsets = offsets.append(at);
    sizes = sizes.append(bytes);
    let Some(next_total) = total +? frames as i64 else {
      return Err(pcm.Limit);
    };
    let Some(next_capacity) = capacity +? nominal as i64 else {
      return Err(pcm.Limit);
    };
    total = next_total;
    capacity = next_capacity;
    positions = positions.append(total);
    last_padding = (nominal - frames) as i64;
    at += bytes;
  }
  if (at != file.data.end || row != plan.end) {
    return Err(pcm.Header);
  }
  let frames = presentation(plan, total, capacity, last_padding)?;
  return Ok(Index { info: Info { format: info.format, channel_mask: mask, frames: frames, priming: plan.priming }, configuration: configuration, offsets: offsets, sizes: sizes, positions: positions, bytes: input.len(), limits: limits });
}

pub function (value: Index) information(): Info {
  return value.info;
}

pub function (value: Index) packet_count(): i32 {
  return value.sizes.len();
}

pub function cursor(index: Index): Result[Cursor, pcm.AudioError] {
  let codec = alac.create(index.configuration, index.limits.audio)?;
  return Ok(Cursor { index: index, codec: codec, packet: 0, target: 0i64 });
}

pub function (own value: Cursor) seek(position: i64): Result[Cursor, pcm.AudioError] {
  if (position < 0i64 || position > value.index.info.frames) {
    return Err(pcm.Header);
  }
  let target = value.index.info.priming + position;
  let lo = 0;
  let hi = value.index.sizes.len();
  if (position == value.index.info.frames) {
    lo = hi;
  }
  while (lo < hi) {
    let middle = lo + (hi - lo) / 2;
    if (value.index.positions[middle + 1] <= target) {
      lo = middle + 1;
    } else {
      hi = middle;
    }
  }
  return Ok(Cursor { ...value, packet: lo, target: position });
}

function ordered(own samples: i32[], format: pcm.Format, mask: u32, frames: i32): i32[] {
  if (format.channels == 7 && mask == 319u32) {
    for i in 0..frames {
      let at = i * 7;
      let center = samples[at + 4];
      let left = samples[at + 5];
      let right = samples[at + 6];
      samples = samples.with(at + 4, left).with(at + 5, right).with(at + 6, center);
    }
  } else if (format.channels == 8 && mask == 255u32) {
    for i in 0..frames {
      let at = i * 8;
      let near_left = samples[at + 4];
      let near_right = samples[at + 5];
      let surround_left = samples[at + 6];
      let surround_right = samples[at + 7];
      samples = samples.with(at + 4, surround_left).with(at + 5, surround_right).with(at + 6, near_left).with(at + 7, near_right);
    }
  }
  return samples;
}

// One step validates one selected payload and returns its trimmed range in the same PCM buffer.
// fern-lint: allow cyclomatic-complexity
pub function (own state: Cursor) step(input: [u8]): Result[(Cursor, ReadEvent), pcm.AudioError] {
  if (input.len() != state.index.bytes) {
    return Err(pcm.Header);
  }
  if (state.packet >= state.index.sizes.len()) {
    return Ok((state, Finished));
  }
  let Cursor { index, codec, packet, target } = state;
  let offset = index.offsets[packet];
  let (next_codec, frame) = codec.decode_packet(input[offset:offset + index.sizes[packet]], index.limits.audio)?;
  let alac.Frame { format, frames: raw, bytes_read, samples } = frame;
  if (raw as i64 != index.positions[packet + 1] - index.positions[packet]) {
    return Err(pcm.Header);
  }
  samples = ordered(samples, format, index.info.channel_mask, raw);
  let next = Cursor { index: index, codec: next_codec, packet: packet + 1, target: target };
  let raw_start = index.positions[packet];
  let first = index.info.priming + target;
  let end = index.info.priming + index.info.frames;
  let begin = if (first > raw_start) { first } else { raw_start };
  let last = if (end < index.positions[packet + 1]) { end } else { index.positions[packet + 1] };
  if (last <= begin) {
    return Ok((next, Pending));
  }
  let audio = Audio { format: format, channel_mask: index.info.channel_mask, frames: (last - begin) as i32, start: (begin - raw_start) as i32, position: begin - index.info.priming, packet: packet, samples: samples };
  return Ok((next, Samples(audio)));
}

pub function (own state: Cursor) next(input: [u8]): Result[(Cursor, Option[Audio]), pcm.AudioError] {
  while (true) {
    let (next, event) = state.step(input)?;
    state = next;
    match (event) {
      Pending => {},
      Samples(value) => {
        return Ok((state, Some(value)));
      },
      Finished => {
        return Ok((state, None));
      }
    }
  }
}

pub function decode(input: [u8], limits: Limits): Result[Decoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  let info = index.information();
  if (info.frames > 2147483647i64) {
    return Err(pcm.Limit);
  }
  let count = pcm.sample_count(info.frames as i32, info.format.channels, limits.audio)?;
  let state = cursor(index)?;
  let output: i32[] = [];
  while (true) {
    let (next, event) = state.next(input)?;
    state = next;
    let Some(audio) = event else {
      break;
    };
    let start = audio.start * info.format.channels;
    let end = start + audio.frames * info.format.channels;
    for sample in audio.samples[start:end] {
      output = output.append(sample);
    }
  }
  if (output.len() != count) {
    return Err(pcm.Header);
  }
  return Ok(Decoded { info: info, samples: output });
}
```
