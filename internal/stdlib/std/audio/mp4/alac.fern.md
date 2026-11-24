# Native ALAC in MP4 and M4A

This module decodes self-contained ALAC tracks from a complete ISO media byte view.
It shares MP4 packet tables, clocks, edit lists, and input budgets with AAC playback.
PCM uses signed, right-aligned integers and an explicit WAVE speaker mask.
No float conversion or quantization occurs.

The decoder reuses packet scratch and a bounded PCM buffer across the track.
Normal media edits remove priming and padding. Empty edits insert silence.
The complete view accepts fragmented files: each `moof` box adds packets to its track.
Encryption, external references, and configuration changes are unsupported.
The ALAC packet decoder retains its declared format and predictor limits.
A retained index applies to the same immutable input bytes used during inspection.

For repeated reads in presentation time, build a `SeekIndex` with `seek_index`.
Its `frames` includes edited silence. Pass the original bytes to `read_into` along with
a starting frame, frame count, and interleaved output buffer. Retain the returned
index for the next read. Samples beyond the requested range are preserved.

A `Stream` decodes one packet at a time for a player, without edits.
A player can read each packet from a source, at the place that `packet` gives.
`std/audio/mp4/file` makes an index from the boxes of a file in a source, with no bytes of the sound.

```fern
<<*>>=
// Decode ALAC tracks from bounded ISO media byte views.
import "std/audio/alac" as codec;
import "std/audio/alac/config";
import "std/audio/alac/layout";
import "std/audio/mp4/boxes";
import "std/audio/mp4/entries";
import "std/audio/mp4/index" as container;
import "std/audio/mp4/presentation";
import "std/audio/mp4/tables";
import "std/audio/mp4/time";
import "std/audio/pcm";
import "std/audio/file" as packet_file;
import "std/audio/source";
import "std/audio/wire";
import "std/i32";
pub use "std/audio/mp4/index".{Limits};

struct Configuration { raw: u8[], info: config.Info, mask: u32 }

struct Track { source: container.Track, configuration: Configuration }

pub opaque struct Index { tracks: Track[], movie: time.Clock, bytes: i64 }

pub struct Info { id: u32, enabled: boolean, format: pcm.Format, channel_mask: u32, frame_length: i32, packets: i32 }

pub struct Decoded { format: pcm.Format, channel_mask: u32, track_id: u32, frames: i64, samples: i32[] }

// One track that a player decodes one packet at a time. Packet numbers
// follow the time of the track, and edits do not apply.
pub opaque struct Stream { id: u32, enabled: boolean, decoder: codec.Decoder, configuration: Configuration, packets: tables.Packet[] }

pub function default_limits(): container.Limits {
  return container.default_limits();
}

function equal(first: Configuration, second: Configuration): boolean {
  if (first.mask != second.mask || first.raw.len() != second.raw.len()) {
    return false;
  }
  for i in 0..first.raw.len() {
    if (first.raw[i] != second.raw[i]) {
      return false;
    }
  }
  return true;
}

function payload(input: [u8], children: [boxes.Box], budget: boxes.Budget): Result[(boxes.Budget, boxes.Box[], boxes.Box), pcm.AudioError] {
  if let Some(value) = boxes.one(children, "alac")? {
    return Ok((budget, [], value));
  }
  let wave = boxes.required(children, "wave")?;
  let (next, nested) = boxes.children(input, wave.data, wave.end, budget)?;
  return Ok((next, nested, boxes.required(nested, "alac")?));
}

function channel_layout(input: [u8], children: [boxes.Box], nested: [boxes.Box], info: config.Info): Result[u32, pcm.AudioError] {
  let outer = boxes.one(children, "chan")?;
  let inner = boxes.one(nested, "chan")?;
  let channels = match ((outer, inner)) {
    (Some(value), None()) => Some(value),
    (None(), Some(value)) => Some(value),
    (None(), None()) => None,
    _ => { return Err(pcm.Header); },
  };
  let mask = info.channel_mask;
  if let Some(value) = channels {
    if (boxes.full(input, value, 16)? != 0) {
      return Err(pcm.Unsupported);
    }
    mask = layout.read(input[value.data + 4:value.end], info)?;
  }
  return Ok(mask);
}

function read_configuration(input: [u8], entry: boxes.Box, budget: boxes.Budget, limits: pcm.Limits): Result[(boxes.Budget, Configuration), pcm.AudioError] {
  let start = entries.header(input, entry, boxes.tag("alac"))?;
  let (b1, children) = boxes.children(input, start, entry.end, budget)?;
  let (b2, nested, box) = payload(input, children, b1)?;
  if (boxes.full(input, box, 28)? != 0) {
    return Err(pcm.Unsupported);
  }
  if (box.end - box.data != 28) {
    return Err(pcm.Header);
  }
  let raw: u8[] = [];
  for byte in input[box.data + 4:box.end] {
    raw = raw.append(byte);
  }
  let info = config.read(raw, limits)?;
  let mask = channel_layout(input, children, nested, info)?;
  return Ok((b2, Configuration { raw: raw, info: info, mask: mask }));
}

pub function inspect(input: [u8], limits: container.Limits): Result[Index, pcm.AudioError] {
  return configured(input, container.inspect(input, limits, boxes.tag("alac"))?, limits);
}

// The index of the movie boxes `input` of a file of `file_bytes` bytes,
// with no bytes of the sound.
pub(package) function inspect_movie(input: [u8], moof_bytes: [u8], moof_starts: [i64], media: tables.Media[], file_bytes: i64, top_boxes: i32, limits: container.Limits): Result[Index, pcm.AudioError] {
  return configured(input, container.inspect_movie(input, moof_bytes, moof_starts, media, file_bytes, top_boxes, limits, boxes.tag("alac"))?, limits);
}

function configured(input: [u8], raw: container.Index, limits: container.Limits): Result[Index, pcm.AudioError] {
  let budget = raw.budget;
  let tracks: Track[] = [];
  for source in raw.tracks {
    let first: Option[Configuration] = None;
    for entry in source.configurations {
      let (next, current) = read_configuration(input, entry, budget, limits.audio)?;
      budget = next;
      if let Some(value) = first {
        if (!equal(value, current)) {
          return Err(pcm.Unsupported);
        }
      } else {
        first = Some(current);
      }
    }
    let Some(configuration) = first else {
      return Err(pcm.Header);
    };
    tracks = tracks.append(Track { source: source, configuration: configuration });
  }
  return Ok(Index { tracks: tracks, movie: raw.movie, bytes: raw.bytes });
}

pub function (value: Index) track_count(): i32 {
  return value.tracks.len();
}

pub function (value: Index) information(track_number: i32): Result[Info, pcm.AudioError] {
  if (track_number < 0 || track_number >= value.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = value.tracks[track_number];
  let info = track.configuration.info;
  return Ok(Info { id: track.source.id, enabled: track.source.enabled, format: info.format, channel_mask: track.configuration.mask, frame_length: info.frame_length, packets: track.source.packets.len() });
}

// Validate packet timing and copy its intersections into the exact presentation spans.
// fern-lint: allow cyclomatic-complexity
function render(input: [u8], track: Track, plan: presentation.Plan, limits: pcm.Limits): Result[i32[], pcm.AudioError] {
  let info = track.configuration.info;
  let decoder = codec.create(track.configuration.raw, limits)?;
  let buffer = pcm.zeros(pcm.sample_count(info.frame_length, info.format.channels, limits)?);
  let output = pcm.zeros(pcm.sample_count(plan.frames, info.format.channels, limits)?);
  let decoded = 0i64;
  for packet_number in 0..track.source.packets.len() {
    let packet = track.source.packets[packet_number];
    if (packet.description < 0 || packet.description >= track.source.configurations.len() || packet.bytes < 1 || packet.offset < 0i64 || packet.offset > input.len() as i64 || packet.bytes as i64 > input.len() as i64 - packet.offset) {
      return Err(pcm.Header);
    }
    let position = time.nearest(packet.position, info.format.sample_rate as u32, track.source.clock.scale)?;
    if (position < decoded - 1i64 || position > decoded + 1i64) {
      return Err(pcm.Unsupported);
    }
    let at = packet.offset as i32;
    let (next, frame) = decoder.decode_packet_into(input[at:at + packet.bytes], buffer, limits)?;
    decoder = next;
    buffer = layout.reorder(frame.samples, info.format, track.configuration.mask, frame.frames)?;
    let end = decoded + frame.frames as i64;
    for span in plan.spans {
      let first = if (span.start > decoded) { span.start } else { decoded };
      let last = if (span.end < end) { span.end } else { end };
      if (last > first) {
        let from = ((first - decoded) * (info.format.channels as i64)) as i32;
        let to = span.output + ((first - span.start) * (info.format.channels as i64)) as i32;
        let count = ((last - first) * (info.format.channels as i64)) as i32;
        for i in 0..count {
          output = output.with(to + i, buffer[from + i]);
        }
      }
    }
    decoded = end;
  }
  for span in plan.spans {
    if (span.end > decoded) {
      return Err(pcm.Header);
    }
  }
  check_duration(track, decoded)?;
  return Ok(output);
}

function check_duration(track: Track, decoded: i64): Result[void, pcm.AudioError] {
  let info = track.configuration.info;
  let last = track.source.packets[track.source.packets.len() - 1];
  let declared = time.nearest(last.position + last.duration as i64, info.format.sample_rate as u32, track.source.clock.scale)?;
  if (declared > decoded || decoded - declared >= info.frame_length as i64) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function check_limits(track: container.Track, tracks: i32, limits: container.Limits): Result[void, pcm.AudioError] {
  if (limits.max_boxes < 1 || limits.max_tracks < 1 || limits.max_packets < 1 || limits.max_descriptions < 1 || limits.max_edits < 0) {
    return Err(pcm.Limit);
  }
  if (tracks > limits.max_tracks || track.packets.len() > limits.max_packets || track.configurations.len() > limits.max_descriptions || track.edits.len() > limits.max_edits) {
    return Err(pcm.Limit);
  }
  return Ok(());
}

pub function decode_track(input: [u8], index: Index, track_number: i32, limits: container.Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (index.bytes != input.len() as i64 || track_number < 0 || track_number >= index.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = index.tracks[track_number];
  check_limits(track.source, index.tracks.len(), limits)?;
  let info = track.configuration.info;
  let plan = presentation.create(track.source.packets, track.source.edits, track.source.clock, index.movie, info.format.sample_rate, info.format.channels, track.source.packets.len() as i64 * (info.frame_length as i64), limits.audio)?;
  let samples = render(input, track, plan, limits.audio)?;
  return Ok(Decoded { format: info.format, channel_mask: track.configuration.mask, track_id: track.source.id, frames: plan.frames as i64, samples: samples });
}

// A stream of the track `track_number`.
pub function (value: Index) stream(track_number: i32, limits: pcm.Limits): Result[Stream, pcm.AudioError] {
  if (track_number < 0 || track_number >= value.tracks.len()) {
    return Err(pcm.Header);
  }
  let track = value.tracks[track_number];
  let decoder = codec.create(track.configuration.raw, limits)?;
  return Ok(Stream { id: track.source.id, enabled: track.source.enabled, decoder: decoder, configuration: track.configuration, packets: track.source.packets });
}

pub function (value: Stream) information(): Info {
  let info = value.configuration.info;
  return Info { id: value.id, enabled: value.enabled, format: info.format, channel_mask: value.configuration.mask, frame_length: info.frame_length, packets: value.packets.len() };
}

// The place of the packet `number` in the file: its offset and its bytes.
pub function (value: Stream) packet(number: i32): Result[tables.Packet, pcm.AudioError] {
  if (number < 0 || number >= value.packets.len()) {
    return Err(pcm.Header);
  }
  let packet = value.packets[number];
  if (packet.bytes < 1 || packet.offset < 0i64) {
    return Err(pcm.Header);
  }
  return Ok(packet);
}

// The stream after the packet `number` of `input`, and the samples of that
// packet in WAVE speaker order.
pub function (own value: Stream) decode_packet(input: [u8], number: i32, limits: pcm.Limits): Result[(Stream, i32[]), pcm.AudioError] {
  let packet = value.packet(number)?;
  if (packet.bytes as i64 > input.len() as i64 - packet.offset) {
    return Err(pcm.Header);
  }
  let at = packet.offset as i32;
  return value.decode_bytes(input[at:at + packet.bytes], limits);
}

// The stream after the packet whose bytes are `bytes`, and the samples of
// that packet in WAVE speaker order. The packets go in their order.
pub function (own value: Stream) decode_bytes(bytes: [u8], limits: pcm.Limits): Result[(Stream, i32[]), pcm.AudioError] {
  let frames = value.decoder.packet_frames(bytes, limits)?;
  let count = pcm.sample_count(frames, value.configuration.info.format.channels, limits)?;
  let (next, frame) = value.decode_bytes_into(bytes, pcm.zeros(count), limits)?;
  return Ok((next, frame.samples));
}

// Decode a packet into the caller's buffer. Only frames * channels samples
// in the returned buffer belong to this packet; its remaining samples survive.
pub function (own value: Stream) decode_bytes_into(bytes: [u8], own output: i32[], limits: pcm.Limits): Result[(Stream, codec.Frame), pcm.AudioError] {
  let Stream { id, enabled, decoder, configuration, packets } = value;
  let (next, frame) = decoder.decode_packet_into(bytes, output, limits)?;
  let samples = layout.reorder(frame.samples, configuration.info.format, configuration.mask, frame.frames)?;
  return Ok((Stream { id: id, enabled: enabled, decoder: next, configuration: configuration, packets: packets }, codec.Frame { ...frame, samples: samples }));
}

// The physical start of each packet comes from its ALAC frame count.
// Rounded container timestamps can differ by one sample at packet boundaries.
pub opaque struct SeekIndex {
  stream: Stream,
  plan: presentation.Plan,
  starts: i64[],
  file_bytes: i64,
  scratch: i32[],
}

// A file supplies a bounded packet window and the remaining operation budget.
// Byte views take a direct slice without allocating an encoded packet window.
pub(package) struct Fetch { load: (i64, i32, i32) => Result[(packet_file.Window, i32), source.ReadError] }

function audio[T](value: Result[T, pcm.AudioError]): Result[T, source.ReadError] {
  return match (value) { Ok(v) => Ok(v), Err(e) => Err(source.ReadAudio(e)) };
}

function memory[T](value: Result[T, source.ReadError]): Result[T, pcm.AudioError] {
  return match (value) { Ok(v) => Ok(v), Err(source.ReadAudio(e)) => Err(e), Err(_) => Err(pcm.Header) };
}

function packet_length(input: [u8], fetch: Option[Fetch], remaining: i32, packet: tables.Packet, stream: Stream, limits: pcm.Limits): Result[(i32, i32), source.ReadError] {
  if let Some(file) = fetch {
    let (window, left) = file.load(packet.offset, packet.bytes, remaining)?;
    return Ok((audio(stream.decoder.packet_frames(window.view(), limits))?, left));
  }
  let at = packet.offset as i32;
  return Ok((audio(stream.decoder.packet_frames(input[at:at + packet.bytes], limits))?, remaining));
}

function packet_starts(input: [u8], fetch: Option[Fetch], remaining: i32, file_bytes: i64, stream: Stream, track: Track, limits: pcm.Limits): Result[i64[], source.ReadError] {
  let info = track.configuration.info;
  let starts: i64[] = [0i64];
  let total = 0i64;
  for n in 0..track.source.packets.len() {
    let packet = audio(stream.packet(n))?;
    if (packet.offset > file_bytes - packet.bytes as i64) {
      return Err(source.ReadAudio(pcm.Header));
    }
    let position = audio(time.nearest(packet.position, info.format.sample_rate as u32, track.source.clock.scale))?;
    if (position < total - 1i64 || position > total + 1i64) {
      return Err(source.ReadAudio(pcm.Unsupported));
    }
    let (frames, left) = packet_length(input, fetch, remaining, packet, stream, limits)?;
    total += frames as i64;
    remaining = left;
    starts = starts.append(total);
  }
  return Ok(starts);
}

pub(package) function build_seek(input: [u8], fetch: Option[Fetch], remaining: i32, bytes: i64, index: Index, track_number: i32, limits: container.Limits): Result[SeekIndex, source.ReadError] {
  if (index.bytes != bytes || track_number < 0 || track_number >= index.tracks.len()) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let track = index.tracks[track_number];
  audio(check_limits(track.source, index.tracks.len(), limits))?;
  let stream = audio(index.stream(track_number, limits.audio))?;
  let info = track.configuration.info;
  let starts = packet_starts(input, fetch, remaining, bytes, stream, track, limits.audio)?;
  let total = starts[starts.len() - 1];
  let plan = audio(presentation.create(track.source.packets, track.source.edits, track.source.clock, index.movie, info.format.sample_rate, info.format.channels, total, limits.audio))?;
  audio(check_duration(track, total))?;
  let scratch = pcm.zeros(audio(pcm.sample_count(info.frame_length, info.format.channels, limits.audio))?);
  return Ok(SeekIndex { stream: stream, plan: plan, starts: starts, file_bytes: bytes, scratch: scratch });
}

pub function seek_index(input: [u8], index: Index, track_number: i32, limits: container.Limits): Result[SeekIndex, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  return memory(build_seek(input, None, 0, input.len() as i64, index, track_number, limits));
}

pub function (index: SeekIndex) frames(): i32 {
  return index.plan.frames;
}

function containing(starts: [i64], frame: i64): i32 {
  let lo = 0;
  let hi = starts.len() - 1;
  while (hi - lo > 1) {
    let mid = lo + (hi - lo) / 2;
    if (starts[mid] <= frame) {
      lo = mid;
    } else {
      hi = mid;
    }
  }
  return lo;
}

function decode_range_packet(input: [u8], fetch: Option[Fetch], remaining: i32, own stream: Stream, n: i32, own scratch: i32[], limits: pcm.Limits): Result[(Stream, codec.Frame, i32), source.ReadError] {
  let packet = audio(stream.packet(n))?;
  if let Some(file) = fetch {
    let (window, left) = file.load(packet.offset, packet.bytes, remaining)?;
    let (next, block) = audio(stream.decode_bytes_into(window.view(), scratch, limits))?;
    return Ok((next, block, left));
  }
  let offset = packet.offset as i32;
  let (next, block) = audio(stream.decode_bytes_into(input[offset:offset + packet.bytes], scratch, limits))?;
  return Ok((next, block, remaining));
}

// Read a range in edited presentation time. Samples after the output prefix
// are preserved, including for an empty read at the end of the presentation.
// The span and packet loops thread ownership of one decoder and both reusable buffers.
// fern-lint: allow cyclomatic-complexity
pub(package) function (own index: SeekIndex) read_range(input: [u8], fetch: Option[Fetch], operations: i32, position: i32, frames: i32, own output: i32[], limits: pcm.Limits): Result[(SeekIndex, i32[]), source.ReadError] {
  if (position < 0 || position > index.plan.frames || frames < 0 || frames > index.plan.frames - position) {
    return Err(source.ReadAudio(pcm.Header));
  }
  audio(pcm.check_input([], limits))?;
  let SeekIndex { stream, plan, starts, file_bytes, scratch } = index;
  let channels = stream.information().format.channels;
  let count = audio(pcm.sample_count(frames, channels, limits))?;
  if (count > output.len()) {
    return Err(source.ReadAudio(pcm.Buffer));
  }
  for i in 0..count {
    output = output.with(i, 0);
  }
  for nspan in plan.first_span(channels, position)..plan.spans.len() {
    let span = plan.spans[nspan];
    let span_position = span.output / channels;
    if (span_position >= position + frames) {
      break;
    }
    let first = position.max(span_position);
    let end = (position + frames).min(span_position + (span.end - span.start) as i32);
    if (end <= first) {
      continue;
    }
    let at = span.start + (first - span_position) as i64;
    let n = containing(starts, at);
    let written = first - position;
    let remaining = end - first;
    while (remaining > 0) {
      let (next, block, left) = decode_range_packet(input, fetch, operations, stream, n, scratch, limits)?;
      operations = left;
      if (block.frames as i64 != starts[n + 1] - starts[n]) {
        return Err(source.ReadAudio(pcm.Header));
      }
      stream = next;
      scratch = block.samples;
      let skip = (at - starts[n]) as i32;
      let take = remaining.min(block.frames - skip);
      for i in 0..take * channels {
        output = output.with(written * channels + i, scratch[skip * channels + i]);
      }
      at += take as i64;
      written += take;
      remaining -= take;
      n += 1;
    }
  }
  return Ok((SeekIndex { stream: stream, plan: plan, starts: starts, file_bytes: file_bytes, scratch: scratch }, output));
}

pub function (own index: SeekIndex) read_into(input: [u8], position: i32, frames: i32, own output: i32[], limits: pcm.Limits): Result[(SeekIndex, i32[]), pcm.AudioError] {
  if (input.len() as i64 != index.file_bytes) {
    return Err(pcm.Header);
  }
  pcm.check_input(input, limits)?;
  return memory(index.read_range(input, None, 0, position, frames, output, limits));
}

pub function decode(input: [u8], limits: container.Limits): Result[Decoded, pcm.AudioError] {
  let index = inspect(input, limits)?;
  for i in 0..index.tracks.len() {
    if (index.tracks[i].source.enabled) {
      return decode_track(input, index, i, limits);
    }
  }
  return Err(pcm.Unsupported);
}
```
