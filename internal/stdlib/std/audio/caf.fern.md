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

`max_packet_frames` returns the frame capacity for one packet.
`step_into` consumes caller-owned integer PCM storage and returns a `BufferEvent`.
Each successful event returns that storage, including pending and completion events.
The decoder writes only the decoded packet prefix and preserves the remaining suffix.
Samples retain the same playable `start` and `frames` range.
The caller can return the storage on the next step.

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
import "std/audio/alac/layout";
import "std/audio/caf/chunks";
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/caf/metadata";

pub struct Limits { audio: pcm.Limits, max_chunks: i32, max_packets: i32 }

pub struct Info { format: pcm.Format, channel_mask: u32, frames: i64, priming: i64 }

pub opaque struct Index { info: Info, configuration: u8[], offsets: i32[], sizes: i32[], positions: i64[], bytes: i32, limits: Limits }

pub opaque struct Cursor { index: Index, codec: alac.Decoder, packet: i32, target: i64 }

pub struct Audio { format: pcm.Format, channel_mask: u32, frames: i32, start: i32, position: i64, packet: i32, samples: i32[] }

pub struct Decoded { info: Info, samples: i32[] }

pub enum ReadEvent { Pending, Samples(Audio), Finished }

pub enum BufferEvent { BufferPending(i32[]), BufferSamples(Audio), BufferFinished(i32[]) }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_chunks: 65536, max_packets: 1048576 };
}

// Check each packet extent and frame count before its scalar index entry.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (limits.max_packets < 1 || limits.max_chunks < 1) {
    return Err(pcm.Limit);
  }
  let file = chunks.inspect(input, limits.max_chunks)?;
  let desc = metadata.description(input[file.description.start:file.description.end])?;
  let Some(specific) = file.cookie else {
    return Err(pcm.Header);
  };
  let configuration = metadata.cookie(input[specific.start:specific.end])?;
  let info = config.read(configuration, limits.audio)?;
  metadata.check(desc, info)?;
  let mask = match (file.channels) { Some(chunk) => layout.read(input[chunk.start:chunk.end], info)?, None => info.channel_mask };
  let table_bytes = match (file.packets) { Some(chunk) => input[chunk.start:chunk.end], None => input[0:0] };
  if (table_bytes.len() < 24 && match (file.packets) { Some(_) => true, None => false }) {
    return Err(pcm.Truncated);
  }
  let plan = metadata.table(if (table_bytes.len() >= 24) { table_bytes[:24] } else { table_bytes }, match (file.packets) { Some(_) => true, None => false }, (file.data.end - file.data.start) as i64, desc, limits.max_packets)?;
  let decoder = alac.create(configuration, limits.audio)?;
  let offsets: i32[] = [];
  let sizes: i32[] = [];
  let positions: i64[] = [0i64];
  let at = file.data.start;
  let row = if (plan.declared) { 24 } else { 0 };
  let total = 0i64;
  let capacity = 0i64;
  let last_padding = 0i64;
  for _ in 0..plan.count {
    let bytes = desc.bytes;
    if (bytes == 0) {
      let (next, value) = metadata.variable(table_bytes, row, table_bytes.len())?;
      row = next;
      bytes = value;
    }
    let nominal = desc.frames;
    if (nominal == 0) {
      let (next_frames, value) = metadata.variable(table_bytes, row, table_bytes.len())?;
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
  if (at != file.data.end || row != table_bytes.len()) {
    return Err(pcm.Header);
  }
  let frames = metadata.presentation(plan, total, capacity, last_padding)?;
  return Ok(Index { info: Info { format: info.format, channel_mask: mask, frames: frames, priming: plan.priming }, configuration: configuration, offsets: offsets, sizes: sizes, positions: positions, bytes: input.len(), limits: limits });
}

pub function (value: Index) information(): Info {
  return value.info;
}

pub function (value: Index) packet_count(): i32 {
  return value.sizes.len();
}

pub function (value: Index) max_packet_frames(): i32 {
  return wire.be(value.configuration, 0, 4) as i32;
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

// One step validates one selected payload and returns its trimmed range in the same PCM buffer.
// fern-lint: allow cyclomatic-complexity
pub function (own state: Cursor) step_into(input: [u8], own output: i32[]): Result[(Cursor, BufferEvent), pcm.AudioError] {
  if (input.len() != state.index.bytes) {
    return Err(pcm.Header);
  }
  if (state.packet >= state.index.sizes.len()) {
    return Ok((state, BufferFinished(output)));
  }
  let Cursor { index, codec, packet, target } = state;
  let offset = index.offsets[packet];
  let (next_codec, frame) = codec.decode_packet_into(input[offset:offset + index.sizes[packet]], output, index.limits.audio)?;
  let alac.Frame { format, frames: raw, bytes_read, samples } = frame;
  if (raw as i64 != index.positions[packet + 1] - index.positions[packet]) {
    return Err(pcm.Header);
  }
  samples = layout.reorder(samples, format, index.info.channel_mask, raw)?;
  let next = Cursor { index: index, codec: next_codec, packet: packet + 1, target: target };
  let raw_start = index.positions[packet];
  let first = index.info.priming + target;
  let end = index.info.priming + index.info.frames;
  let begin = if (first > raw_start) { first } else { raw_start };
  let last = if (end < index.positions[packet + 1]) { end } else { index.positions[packet + 1] };
  if (last <= begin) {
    return Ok((next, BufferPending(samples)));
  }
  let audio = Audio { format: format, channel_mask: index.info.channel_mask, frames: (last - begin) as i32, start: (begin - raw_start) as i32, position: begin - index.info.priming, packet: packet, samples: samples };
  return Ok((next, BufferSamples(audio)));
}

pub function (own state: Cursor) step(input: [u8]): Result[(Cursor, ReadEvent), pcm.AudioError] {
  if (input.len() != state.index.bytes) {
    return Err(pcm.Header);
  }
  if (state.packet >= state.index.sizes.len()) {
    return Ok((state, Finished));
  }
  let frames = (state.index.positions[state.packet + 1] - state.index.positions[state.packet]) as i32;
  let count = pcm.sample_count(frames, state.index.info.format.channels, state.index.limits.audio)?;
  let (next, event) = state.step_into(input, pcm.zeros(count))?;
  let result = match (event) { BufferPending(_) => Pending, BufferSamples(audio) => Samples(audio), BufferFinished(_) => Finished };
  return Ok((next, result));
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
