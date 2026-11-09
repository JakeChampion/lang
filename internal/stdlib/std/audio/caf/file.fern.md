# Indexed CAF ALAC file playback

This module indexes ALAC packets from a borrowed seekable CAF source.
File offsets use signed 64-bit counts.
The builder retains scalar packet entries, small metadata, and bounded byte windows.
It retains no complete encoded file or PCM.
Unknown chunk payloads require no reads.

Each step performs at most one seek, one bounded read, or one packet inspection or decode.
The caller supplies the same immutable source and preserves its position during pending reads.
The caller closes the source after success or failure.
Errors consume builder and cursor state.

The cursor reuses decoder scratch and caller-owned integer PCM storage.
Audio events use the CAF `start` and `frames` trim contract.
Output entries after the decoded packet prefix remain unchanged.

```fern
<<*>>=
// Index CAF ALAC packets and reuse PCM storage over a bounded seekable source.

import "std/audio/alac";
import "std/audio/alac/config";
import "std/audio/alac/layout";
import "std/audio/caf";
import "std/audio/caf/metadata";
import "std/audio/caf/source_chunks" as chunks;
import "std/audio/file" as window;
import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";

pub struct Limits { container: caf.Limits, input: source.Limits, max_file_bytes: i64 }

pub opaque struct Index { info: caf.Info, configuration: u8[], offsets: i64[], sizes: i32[], positions: i64[], limits: Limits }

struct Progress { row: i64, at: i64, capacity: i64, padding: i64, bytes: i32, nominal: i32 }

pub opaque struct Builder { scanner: chunks.Scanner, file: Option[chunks.File], phase: i32, parts: u8[][], window: Option[window.Window], table_window: Option[window.Window], index: Option[Index], table: Option[metadata.Table], description: Option[metadata.Description], progress: Progress, limits: Limits }

pub enum BuildEvent { BuildPending, BuildReady(Index), BuildFinished }

pub opaque struct Cursor { index: Index, codec: alac.Decoder, packet: i32, target: i64, window: Option[window.Window] }

pub function default_limits(): Limits {
  return Limits { container: caf.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 1048576 }, max_file_bytes: 1099511627776i64 };
}

function audio(e: pcm.AudioError): source.ReadError {
  return source.ReadAudio(e);
}

pub function create(bytes: i64, limits: Limits): Result[Builder, source.ReadError] {
  if (limits.container.max_packets < 1 || limits.input.max_buffer_bytes < 72 || limits.max_file_bytes < 8i64 || bytes > limits.max_file_bytes) {
    return Err(audio(pcm.Limit));
  }
  let scanner = chunks.create(bytes, limits.container.max_chunks, limits.input)?;
  return Ok(Builder { scanner: scanner, file: None, phase: -1, parts: [], window: None, table_window: None, index: None, table: None, description: None, progress: Progress { row: 0i64, at: 0i64, capacity: 0i64, padding: 0i64, bytes: 0, nominal: 0 }, limits: limits });
}

function copy(input: [u8]): u8[] {
  let bytes: u8[] = [];
  for byte in input {
    bytes = bytes.append(byte);
  }
  return bytes;
}

function scan(own builder: Builder, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  let (scanner, event) = builder.scanner.step_with(seek, read)?;
  let next = Builder { ...builder, scanner: scanner };
  if let chunks.Ready(file) = event {
    return Ok((Builder { ...next, file: Some(file), phase: 0 }, BuildPending));
  }
  return Ok((next, BuildPending));
}

function metadata_range(file: chunks.File, phase: i32): Option[chunks.Range] {
  return match (phase) {
    0 => Some(file.description), 1 => file.cookie, 2 => file.channels,
    3 => file.packets, _ => Some(file.data),
  };
}

function check_metadata_size(bytes: i64, phase: i32): Result[void, source.ReadError] {
  if (phase == 0 && bytes != 32i64 || phase == 1 && bytes > 72i64) {
    return Err(audio(pcm.Header));
  }
  if (phase == 2 && bytes != 12i64) {
    return Err(audio(pcm.Unsupported));
  }
  return Ok(());
}

function metadata_size(range: chunks.Range, phase: i32): Result[i32, source.ReadError] {
  let bytes = range.end - range.start;
  check_metadata_size(bytes, phase)?;
  let required = if (phase == 3) { 24 } else if (phase == 4) { 4 } else { bytes as i32 };
  if (required < 1 || bytes < required as i64) {
    return Err(audio(pcm.Truncated));
  }
  return Ok(required);
}

function take_window(own builder: Builder): (Builder, Option[window.Window]) {
  let Builder { scanner, file, phase, parts, window, table_window, index, table, description, progress, limits } = builder;
  return (Builder { scanner: scanner, file: file, phase: phase, parts: parts, window: None, table_window: table_window, index: index, table: table, description: description, progress: progress, limits: limits }, window);
}

function read_metadata(own builder: Builder, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  let Some(file) = builder.file else {
    return Err(audio(pcm.Header));
  };
  let (rest, current_window) = take_window(builder);
  if let Some(current) = current_window {
    if (!current.ready) {
      let next = current.step_with(seek, read)?;
      return Ok((Builder { ...rest, window: Some(next) }, BuildPending));
    }
    let parts = rest.parts.append(copy(current.view()));
    return Ok((Builder { ...rest, parts: parts, phase: rest.phase + 1, window: None }, BuildPending));
  }
  let range = metadata_range(file, rest.phase);
  let Some(value) = range else {
    if (rest.phase == 1) {
      return Err(audio(pcm.Header));
    }
    return Ok((Builder { ...rest, parts: rest.parts.append([]), phase: rest.phase + 1 }, BuildPending));
  };
  let bytes = metadata_size(value, rest.phase)?;
  let next = window.create(value.start, bytes, rest.limits.input)?;
  return Ok((Builder { ...rest, window: Some(next) }, BuildPending));
}

function read_channel_mask(input: [u8], info: config.Info): Result[u32, source.ReadError] {
  if (input.len() == 0) {
    return Ok(info.channel_mask);
  }
  return match (layout.read(input, info)) { Ok(value) => Ok(value), Err(e) => Err(audio(e)) };
}

function read_configuration(parts: [u8[]], limits: pcm.Limits): Result[(metadata.Description, u8[], config.Info, u32), source.ReadError] {
  let desc = match (metadata.description(parts[0])) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  let configuration = match (metadata.cookie(parts[1])) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  let info = match (config.read(configuration, limits)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  if let Err(e) = metadata.check(desc, info) {
    return Err(audio(e));
  }
  let mask = read_channel_mask(parts[2], info)?;
  return Ok((desc, configuration, info, mask));
}

function initialize(own builder: Builder): Result[(Builder, BuildEvent), source.ReadError] {
  let Some(file) = builder.file else {
    return Err(audio(pcm.Header));
  };
  let (desc, configuration, info, mask) = read_configuration(builder.parts, builder.limits.container.audio)?;
  if (wire.be(builder.parts[4], 0, 4) != 0u64) {
    return Err(audio(pcm.Unsupported));
  }
  let declared = match (file.packets) { Some(_) => true, None => false };
  let table = match (metadata.table(builder.parts[3], declared, file.data.end - file.data.start - 4i64, desc, builder.limits.container.max_packets)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  let row = match (file.packets) { Some(range) => range.start + 24i64, None => 0i64 };
  let index = Index { info: caf.Info { format: info.format, channel_mask: mask, frames: 0i64, priming: table.priming }, configuration: configuration, offsets: [], sizes: [], positions: [0i64], limits: builder.limits };
  let progress = Progress { ...builder.progress, row: row, at: file.data.start + 4i64 };
  return Ok((Builder { ...builder, parts: [], index: Some(index), table: Some(table), description: Some(desc), progress: progress, phase: 6 }, BuildPending));
}

function table_end(file: chunks.File): i64 {
  return match (file.packets) { Some(range) => range.end, None => 0i64 };
}

function ready_row(current: window.Window, row: i64, end: i64): boolean {
  let need = if (end - row < 10i64) { end - row } else { 10i64 };
  return current.ready && current.offset <= row && row - current.offset + need <= current.bytes as i64;
}

function read_table(own builder: Builder, end: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  if (builder.progress.row >= end) {
    return Err(audio(pcm.Truncated));
  }
  if let Some(current) = builder.table_window {
    if (!current.ready) {
      let next = current.step_with(seek, read)?;
      return Ok((Builder { ...builder, table_window: Some(next) }, BuildPending));
    }
  }
  let size = if (builder.limits.input.chunk_size > 10) { builder.limits.input.chunk_size } else { 10 };
  size = if (size > builder.limits.input.max_buffer_bytes) { builder.limits.input.max_buffer_bytes } else { size };
  size = if (end - builder.progress.row < size as i64) { (end - builder.progress.row) as i32 } else { size };
  let next = window.create(builder.progress.row, size, builder.limits.input)?;
  return Ok((Builder { ...builder, table_window: Some(next) }, BuildPending));
}

function packet_window(own builder: Builder, bytes: i32, nominal: i32, row: i64, end: i64, maximum: i32): Result[(Builder, BuildEvent), source.ReadError] {
  if (bytes < 1 || bytes as i64 > end - builder.progress.at || nominal < 1 || nominal > maximum) {
    return Err(audio(pcm.Header));
  }
  if (bytes > builder.limits.input.max_buffer_bytes || bytes > builder.limits.container.audio.max_input_bytes) {
    return Err(audio(pcm.Limit));
  }
  let current = window.create(builder.progress.at, bytes, builder.limits.input)?;
  let progress = Progress { ...builder.progress, row: row, bytes: bytes, nominal: nominal };
  return Ok((Builder { ...builder, window: Some(current), progress: progress, phase: 7 }, BuildPending));
}

function finish(own builder: Builder, index: Index, table: metadata.Table, file: chunks.File): Result[(Builder, BuildEvent), source.ReadError] {
  if (builder.progress.at != file.data.end || builder.progress.row != table_end(file)) {
    return Err(audio(pcm.Header));
  }
  let raw = index.positions[index.positions.len() - 1];
  let frames = match (metadata.presentation(table, raw, builder.progress.capacity, builder.progress.padding)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  let info = caf.Info { ...index.info, frames: frames };
  let result = Index { ...index, info: info };
  return Ok((Builder { ...builder, index: None, table_window: None, phase: 8 }, BuildReady(result)));
}

function packet_sizes(current: window.Window, desc: metadata.Description, row: i64): Result[(i64, i32, i32), source.ReadError] {
  let local = (row - current.offset) as i32;
  let bytes = desc.bytes;
  let nominal = desc.frames;
  if (bytes == 0) {
    let (next, value) = match (metadata.variable(current.view(), local, current.bytes)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
    local = next;
    bytes = value;
  }
  if (nominal == 0) {
    let (next, value) = match (metadata.variable(current.view(), local, current.bytes)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
    local = next;
    nominal = value;
  }
  return Ok((current.offset + local as i64, bytes, nominal));
}

function row_context(builder: Builder): Result[(chunks.File, Index, metadata.Table, metadata.Description), source.ReadError] {
  let Some(file) = builder.file else {
    return Err(audio(pcm.Header));
  };
  let Some(index) = builder.index else {
    return Err(audio(pcm.Header));
  };
  let Some(table) = builder.table else {
    return Err(audio(pcm.Header));
  };
  let Some(desc) = builder.description else {
    return Err(audio(pcm.Header));
  };
  return Ok((file, index, table, desc));
}

function cached_row(builder: Builder, at: i64, end: i64): boolean {
  return match (builder.table_window) { Some(current) => ready_row(current, at, end), None => false };
}

function row(own builder: Builder, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  let (file, index, table, desc) = row_context(builder)?;
  if (index.sizes.len() == table.count) {
    return finish(builder, index, table, file);
  }
  let at = builder.progress.row;
  let bytes = desc.bytes;
  let nominal = desc.frames;
  if (bytes == 0 || nominal == 0) {
    let end = table_end(file);
    let ready = cached_row(builder, at, end);
    if (!ready) {
      return read_table(builder, end, seek, read);
    }
    let Some(current) = builder.table_window else {
      return Err(audio(pcm.Header));
    };
    let (next_row, next_bytes, next_nominal) = packet_sizes(current, desc, at)?;
    at = next_row;
    bytes = next_bytes;
    nominal = next_nominal;
  }
  return packet_window(builder, bytes, nominal, at, file.data.end, wire.be(index.configuration, 0, 4) as i32);
}

function take_index(own builder: Builder): (Builder, Option[Index]) {
  let Builder { scanner, file, phase, parts, window, table_window, index, table, description, progress, limits } = builder;
  return (Builder { scanner: scanner, file: file, phase: phase, parts: parts, window: window, table_window: table_window, index: None, table: table, description: description, progress: progress, limits: limits }, index);
}

function inspect_packet(own builder: Builder, input: [u8]): Result[(Builder, BuildEvent), source.ReadError] {
  let (rest, current_index) = take_index(builder);
  let Some(index) = current_index else {
    return Err(audio(pcm.Header));
  };
  let Some(desc) = rest.description else {
    return Err(audio(pcm.Header));
  };
  let frames = match (alac.inspect_packet(input, index.configuration, rest.limits.container.audio)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  if (frames > rest.progress.nominal || desc.frames == 0 && frames != rest.progress.nominal) {
    return Err(audio(pcm.Header));
  }
  let Some(total) = index.positions[index.positions.len() - 1] +? frames as i64 else {
    return Err(audio(pcm.Limit));
  };
  let Some(capacity) = rest.progress.capacity +? rest.progress.nominal as i64 else {
    return Err(audio(pcm.Limit));
  };
  let next = Index { ...index, offsets: index.offsets.append(rest.progress.at), sizes: index.sizes.append(rest.progress.bytes), positions: index.positions.append(total) };
  let progress = Progress { ...rest.progress, at: rest.progress.at + rest.progress.bytes as i64, capacity: capacity, padding: (rest.progress.nominal - frames) as i64 };
  return Ok((Builder { ...rest, index: Some(next), progress: progress, window: None, phase: 6 }, BuildPending));
}

function read_packet(own builder: Builder, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  let (rest, current_window) = take_window(builder);
  let Some(current) = current_window else {
    return Err(audio(pcm.Header));
  };
  if (!current.ready) {
    let next = current.step_with(seek, read)?;
    return Ok((Builder { ...rest, window: Some(next) }, BuildPending));
  }
  return inspect_packet(rest, current.view());
}

pub function (own builder: Builder) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  if (builder.phase < 0) {
    return scan(builder, seek, read);
  }
  if (builder.phase < 5) {
    return read_metadata(builder, seek, read);
  }
  if (builder.phase == 5) {
    return initialize(builder);
  }
  if (builder.phase == 6) {
    return row(builder, seek, read);
  }
  if (builder.phase == 7) {
    return read_packet(builder, seek, read);
  }
  return Ok((builder, BuildFinished));
}

pub function (own builder: Builder) step(reader: Reader): Result[(Builder, BuildEvent), source.ReadError] {
  return builder.step_with((at: i64): Result[i64, IoError] => reader.seek(at, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function (index: Index) information(): caf.Info {
  return index.info;
}

pub function (index: Index) packet_count(): i32 {
  return index.sizes.len();
}

pub function (index: Index) max_packet_frames(): i32 {
  return wire.be(index.configuration, 0, 4) as i32;
}

pub function cursor(index: Index): Result[Cursor, source.ReadError] {
  let codec = match (alac.create(index.configuration, index.limits.container.audio)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  return Ok(Cursor { index: index, codec: codec, packet: 0, target: 0i64, window: None });
}

pub function (own state: Cursor) seek(position: i64): Result[Cursor, source.ReadError] {
  if (position < 0i64 || position > state.index.info.frames) {
    return Err(audio(pcm.Header));
  }
  let target = state.index.info.priming + position;
  let lo = 0;
  let hi = state.index.sizes.len();
  if (position == state.index.info.frames) {
    lo = hi;
  }
  while (lo < hi) {
    let middle = lo + (hi - lo) / 2;
    if (state.index.positions[middle + 1] <= target) {
      lo = middle + 1;
    } else {
      hi = middle;
    }
  }
  return Ok(Cursor { ...state, packet: lo, target: position, window: None });
}

function decode(own state: Cursor, input: [u8], own output: i32[]): Result[(Cursor, caf.BufferEvent), source.ReadError] {
  let Cursor { index, codec, packet, target, window } = state;
  let (next_codec, frame) = match (codec.decode_packet_into(input, output, index.limits.container.audio)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  let alac.Frame { format, frames: raw, bytes_read, samples } = frame;
  if (raw as i64 != index.positions[packet + 1] - index.positions[packet]) {
    return Err(audio(pcm.Header));
  }
  samples = match (layout.reorder(samples, format, index.info.channel_mask, raw)) { Ok(value) => value, Err(e) => { return Err(audio(e)); } };
  let next = Cursor { index: index, codec: next_codec, packet: packet + 1, target: target, window: None };
  let raw_start = index.positions[packet];
  let first = index.info.priming + target;
  let end = index.info.priming + index.info.frames;
  let begin = if (first > raw_start) { first } else { raw_start };
  let last = if (end < index.positions[packet + 1]) { end } else { index.positions[packet + 1] };
  if (last <= begin) {
    return Ok((next, caf.BufferPending(samples)));
  }
  let result = caf.Audio { format: format, channel_mask: index.info.channel_mask, frames: (last - begin) as i32, start: (begin - raw_start) as i32, position: begin - index.info.priming, packet: packet, samples: samples };
  return Ok((next, caf.BufferSamples(result)));
}

function take_cursor_window(own state: Cursor): (Cursor, Option[window.Window]) {
  let Cursor { index, codec, packet, target, window } = state;
  return (Cursor { index: index, codec: codec, packet: packet, target: target, window: None }, window);
}

pub function (own state: Cursor) step_into_with(own output: i32[], seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Cursor, caf.BufferEvent), source.ReadError] {
  if (state.packet >= state.index.sizes.len()) {
    return Ok((state, caf.BufferFinished(output)));
  }
  let (rest, current_window) = take_cursor_window(state);
  let Some(current) = current_window else {
    let next = window.create(rest.index.offsets[rest.packet], rest.index.sizes[rest.packet], rest.index.limits.input)?;
    return Ok((Cursor { ...rest, window: Some(next) }, caf.BufferPending(output)));
  };
  if (!current.ready) {
    let next = current.step_with(seek, read)?;
    return Ok((Cursor { ...rest, window: Some(next) }, caf.BufferPending(output)));
  }
  return decode(rest, current.view(), output);
}

pub function (own state: Cursor) step_into(own output: i32[], reader: Reader): Result[(Cursor, caf.BufferEvent), source.ReadError] {
  return state.step_into_with(output, (at: i64): Result[i64, IoError] => reader.seek(at, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
