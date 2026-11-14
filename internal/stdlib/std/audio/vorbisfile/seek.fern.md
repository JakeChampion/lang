# Indexed Ogg Vorbis seeks

This module indexes an immutable Ogg input view and returns exact PCM ranges.
Index construction validates container structure, setup headers, packet modes, and sample boundaries.
It does not decode packet floors or spectral residues. A read validates those packet payloads.
The input bytes must remain unchanged after index construction.

The index retains packet locations and sample positions, without encoded packets or PCM.
Each packet uses 32 logical bytes in scalar arrays, before capacity and runtime overhead.
The default limits allow 16 MiB of encoded input and 1,048,576 packets across all selected links.
Container, setup, metadata, per-packet PCM, and link limits also apply.

`seek` accepts a logical link and a playable frame position, including the exact end.
Vorbis requires one preceding packet to reconstruct overlap history.
Seeks within the active link reuse decoder configuration and transform storage.
A seek to the end continues with the next link on a subsequent read.

Each `step` performs at most one setup parse, one packet decode, or one link transition.
`Pending` allows cancellation and backpressure between those operations.
`next` continues until samples or the end. Sample events contain at most one packet.
Output uses unclipped float PCM in WAVE speaker order.

Errors consume the cursor. The index borrows no file handles and performs no I/O.

`inspect_with` builds the same index from a source that reads a part of the file at a time.
It reads the whole file once, but it keeps only the index.
The index then also keeps the end of the last page of each packet, in 4 more bytes for each packet.
`want` gives the bytes of the file that the next step reads.
`step_view` takes those bytes as a view that starts at the byte `base` of the file.
So a player can keep only a window of a long file.

```fern
<<*>>=
// Index immutable Ogg Vorbis input and decode exact sample ranges with bounded work.

import "std/audio/vorbis";
import "std/audio/vorbisfile";
import "std/audio/opusfile/packets";
import "std/audio/pcm";
import "std/audio/source";
import "std/array";
import "std/option";

pub struct Limits { file: vorbisfile.Limits, max_input_bytes: i32, max_packets: i32 }

pub struct Link { info: vorbisfile.Info, identification: packets.Location, setup: packets.Location, first: i32, packets: i32, frames: i64, initial_granule: i64 }

pub opaque struct Index { links: Link[], locations: i32[], raw_frames: i32[], starts: i32[], frames: i32[], positions: i64[], ends: i32[], header_ends: i32[], bytes: i32, limits: Limits }

pub opaque struct Cursor { index: Index, codec: Option[vorbis.Decoder], link: i32, packet: i32, target: i64, cold: boolean }

pub enum ReadEvent { Pending, Samples(vorbisfile.Audio), Finished }

pub function default_limits(): Limits {
  return Limits { file: vorbisfile.default_limits(), max_input_bytes: 16777216, max_packets: 1048576 };
}

function location(value: vorbisfile.Location, size: i32): Result[packets.Location, pcm.AudioError] {
  if (value.page_offset < 0i64 || value.page_offset > size as i64 || value.offset < value.page_offset || value.offset > size as i64 || value.bytes < 1 || value.bytes > size - value.offset as i32) {
    return Err(pcm.Header);
  }
  return Ok(packets.Location { page_offset: value.page_offset as i32, offset: value.offset as i32, bytes: value.bytes });
}

// Append each scalar column only after packet and extent limits pass.
// fern-lint: allow cyclomatic-complexity
function append(own index: Index, value: vorbisfile.Indexed, end: i32): Result[Index, pcm.AudioError] {
  if (index.frames.len() >= index.limits.max_packets) {
    return Err(pcm.Limit);
  }
  let at = location(value.location, index.bytes)?;
  if (end < at.offset + at.bytes) {
    return Err(pcm.Header);
  }
  if (value.raw_frames < 0 || value.raw_frames > 4096 || value.start < 0 || value.frames < 0 || value.start > value.raw_frames || value.frames > value.raw_frames - value.start || value.position < 0i64) {
    return Err(pcm.Header);
  }
  let Index { links, locations, raw_frames, starts, frames, positions, ends, header_ends, bytes, limits } = index;
  return Ok(Index { links: links, locations: locations.append(at.page_offset).append(at.offset).append(at.bytes), raw_frames: raw_frames.append(value.raw_frames), starts: starts.append(value.start), frames: frames.append(value.frames), positions: positions.append(value.position), ends: ends.append(end), header_ends: header_ends, bytes: bytes, limits: limits });
}

// An index under construction: the setup of the current link, its first
// packet, and the end of the page of its setup.
struct Build { result: Index, ready: Option[vorbisfile.Prepared], first: i32, header_end: i32 }

function begin(size: i32, limits: Limits): Result[Build, pcm.AudioError] {
  if (limits.max_input_bytes < 0 || size > limits.max_input_bytes || limits.max_packets < 1 || limits.max_packets > 715827882) {
    return Err(pcm.Limit);
  }
  let result = Index { links: [], locations: [], raw_frames: [], starts: [], frames: [], positions: [], ends: [], header_ends: [], bytes: size, limits: limits };
  return Ok(Build { result: result, ready: None, first: 0, header_end: 0 });
}

// The index after one scan event. `at` is the end of the scanned bytes,
// which is the end of the page that completes the packet of the event.
// Keep scan event order, shared packet budgets, and link completion together.
// fern-lint: allow cyclomatic-complexity
function take(own build: Build, event: vorbisfile.ScanEvent, at: i32): Result[Build, pcm.AudioError] {
  let Build { result, ready, first, header_end } = build;
  match (event) {
    vorbisfile.ScanSetup(value) => {
      return Ok(Build { result: result, ready: Some(value), first: result.frames.len(), header_end: at });
    },
    vorbisfile.ScanPacket(value) => {
      let Some(config) = ready else {
        return Err(pcm.Header);
      };
      if (value.info.index != config.info.index || config.info.index != result.links.len()) {
        return Err(pcm.Header);
      }
      return Ok(Build { result: append(result, value, at)?, ready: ready, first: first, header_end: header_end });
    },
    vorbisfile.ScanEnd(value) => {
      let Some(config) = ready else {
        return Err(pcm.Header);
      };
      if (value.info.index != config.info.index || first >= result.frames.len()) {
        return Err(pcm.Header);
      }
      let size = result.bytes;
      let link = Link { info: value.info, identification: location(config.identification, size)?, setup: location(config.setup, size)?, first: first, packets: result.frames.len() - first, frames: value.frames, initial_granule: value.initial_granule };
      return Ok(Build { result: Index { ...result, links: result.links.append(link), header_ends: result.header_ends.append(header_end) }, ready: None, first: first, header_end: header_end });
    },
    _ => {
      return Ok(Build { result: result, ready: ready, first: first, header_end: header_end });
    }
  }
}

pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  let build = begin(input.len(), limits)?;
  let scanner = vorbisfile.scan(limits.file)?;
  let at = 0;
  while (true) {
    let eof = at == input.len();
    let (next, used, event) = scanner.feed(input[at:])?;
    scanner = next;
    at += used;
    if let Some(value) = event {
      build = take(build, value, at)?;
    } else if (eof) {
      break;
    } else if (used == 0 && at < input.len()) {
      return Err(pcm.Header);
    }
  }
  scanner.finish()?;
  return Ok(build.result);
}

// The index of a file of `size` bytes that `read` gives a part at a time:
// `read(offset, count)` gives at most `count` bytes from `offset`, and at
// least one before the end of the file. The read goes once through the
// file.
pub function inspect_with(size: i64, read: (i64, i32) => Result[u8[], IoError], limits: Limits): Result[Index, source.ReadError] {
  if (size < 0i64 || size > limits.max_input_bytes as i64) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let n = size as i32;
  let build = audio(begin(n, limits))?;
  let scanner = audio(vorbisfile.scan(limits.file))?;
  let chunk: u8[] = [];
  let base = 0;
  let used_in = 0;
  while (true) {
    if (used_in == chunk.len() && base + used_in < n) {
      base += used_in;
      chunk = match (read(base as i64, CHUNK.min(n - base))) { Ok(value) => value, Err(error) => { return Err(source.ReadIo(error)); } };
      used_in = 0;
      if (chunk.len() == 0) {
        return Err(source.ReadAudio(pcm.Header));
      }
    }
    let eof = base + used_in == n;
    let (next, used, event) = audio(scanner.feed(chunk[used_in:]))?;
    scanner = next;
    used_in += used;
    if let Some(value) = event {
      build = audio(take(build, value, base + used_in))?;
    } else if (eof) {
      break;
    } else if (used == 0) {
      // The page goes on past this chunk: read the rest of it with the next one.
      let rest = array.slice(chunk, used_in, chunk.len());
      base += used_in;
      let more = match (read((base + rest.len()) as i64, CHUNK.min(n - base - rest.len()))) { Ok(value) => value, Err(error) => { return Err(source.ReadIo(error)); } };
      if (more.len() == 0) {
        return Err(source.ReadAudio(pcm.Header));
      }
      chunk = rest.concat(more);
      used_in = 0;
    }
  }
  audio(scanner.finish())?;
  return Ok(build.result);
}

// The bytes that `inspect_with` asks for at a time.
const CHUNK: i32 = 65536;

function audio[T](own value: Result[T, pcm.AudioError]): Result[T, source.ReadError] {
  return match (value) { Ok(v) => Ok(v), Err(error) => Err(source.ReadAudio(error)) };
}

pub function (value: Index) links(): [Link] {
  return value.links[:];
}

pub function (value: Index) packet_count(): i32 {
  return value.frames.len();
}

pub function cursor(index: Index): Cursor {
  return Cursor { index: index, codec: None, link: 0, packet: 0, target: 0i64, cold: true };
}

function selected(index: Index, link: Link, position: i64): i32 {
  let lo = link.first;
  let hi = link.first + link.packets;
  while (lo < hi) {
    let middle = lo + (hi - lo) / 2;
    if (index.positions[middle] + index.frames[middle] as i64 <= position) {
      lo = middle + 1;
    } else {
      hi = middle;
    }
  }
  return lo;
}

function valid_position(index: Index, link: i32, position: i64): boolean {
  return link >= 0 && link < index.links.len() && position >= 0i64 && position <= index.links[link].frames;
}

pub function (own state: Cursor) seek(link: i32, position: i64): Result[Cursor, pcm.AudioError] {
  let Cursor { index, codec, link: previous, packet, target, cold } = state;
  if (!valid_position(index, link, position)) {
    return Err(pcm.Header);
  }
  let track = index.links[link];
  let start_packet = if (position == track.frames) { track.first + track.packets } else { selected(index, track, position) - 1 };
  if (start_packet < track.first || start_packet > track.first + track.packets) {
    return Err(pcm.Header);
  }
  let next = if (previous == link) { match (codec) { Some(value) => Some(value.reset()), None => None } } else { None };
  return Ok(Cursor { index: index, codec: next, link: link, packet: start_packet, target: position, cold: true });
}

// The location `value` in a view that starts at the byte `base` of the file.
function moved(value: packets.Location, base: i32): packets.Location {
  return packets.Location { page_offset: value.page_offset - base, offset: value.offset - base, bytes: value.bytes };
}

function load(input: [u8], base: i32, index: Index, link: Link): Result[vorbis.Decoder, pcm.AudioError] {
  let identification = packets.read(input, moved(link.identification, base), link.info.serial, index.limits.file.ogg.max_packet_bytes)?;
  let setup = packets.read(input, moved(link.setup, base), link.info.serial, index.limits.file.ogg.max_packet_bytes)?;
  return vorbis.create(identification, setup, index.limits.file.codec);
}

function decode(own codec: vorbis.Decoder, input: [u8], base: i32, index: Index, packet: i32, serial: u32): Result[(vorbis.Decoder, vorbis.Frame), pcm.AudioError] {
  let at = 3 * packet;
  let location = moved(packets.Location { page_offset: index.locations[at], offset: index.locations[at + 1], bytes: index.locations[at + 2] }, base);
  if (packets.contiguous(input, location)?) {
    return codec.decode_packet(input[location.offset:location.offset + location.bytes], index.limits.file.codec.audio);
  }
  let encoded = packets.read(input, location, serial, index.limits.file.ogg.max_packet_bytes)?;
  return codec.decode_packet(encoded, index.limits.file.codec.audio);
}

function clipped(own samples: f32[], first: i32, end: i32): f32[] {
  if (first == 0 && end == samples.len()) {
    return samples;
  }
  let output: f32[] = [];
  for i in first..end {
    output = output.append(samples[i]);
  }
  return output;
}

function complete(own state: Cursor): Result[(Cursor, ReadEvent), pcm.AudioError] {
  let Cursor { index, codec, link, packet, target, cold } = state;
  let next_link = link + 1;
  let first = if (next_link < index.links.len()) { index.links[next_link].first } else { index.frames.len() };
  let next = Cursor { index: index, codec: None, link: next_link, packet: first, target: 0i64, cold: true };
  return Ok((next, if (next_link < index.links.len()) { Pending } else { Finished }));
}

// The bytes of the file that the next step reads, as an offset and a
// count. A count of 0 means that the step reads no bytes.
pub function (state: Cursor) want(): (i64, i32) {
  let index = state.index;
  if (state.link >= index.links.len()) {
    return (0i64, 0);
  }
  let link = index.links[state.link];
  if (state.packet >= link.first + link.packets) {
    return (0i64, 0);
  }
  if (state.codec.is_none()) {
    let from = link.identification.page_offset;
    return (from as i64, index.header_ends[state.link] - from);
  }
  let from = index.locations[3 * state.packet];
  return (from as i64, index.ends[state.packet] - from);
}

pub function (own state: Cursor) step(input: [u8]): Result[(Cursor, ReadEvent), pcm.AudioError] {
  if (input.len() != state.index.bytes) {
    return Err(pcm.Header);
  }
  return state.step_view(input, 0i64);
}

// A step on the view `input` of the file from the byte `base`. The view
// holds the bytes that `want` gives.
// A step parses configuration or decodes one packet, including one cold overlap primer.
// fern-lint: allow cyclomatic-complexity
pub function (own state: Cursor) step_view(input: [u8], base: i64): Result[(Cursor, ReadEvent), pcm.AudioError] {
  let (from, count) = state.want();
  if (base < 0i64 || count > 0 && (base > from || from + count as i64 > base + input.len() as i64)) {
    return Err(pcm.Header);
  }
  let at = base as i32;
  if (state.link >= state.index.links.len()) {
    return Ok((state, Finished));
  }
  let link = state.index.links[state.link];
  if (state.packet >= link.first + link.packets) {
    return complete(state);
  }
  let Cursor { index, codec, link: current, packet, target, cold } = state;
  let Some(value) = codec else {
    let created = load(input, at, index, link)?;
    return Ok((Cursor { index: index, codec: Some(created), link: current, packet: packet, target: target, cold: true }, Pending));
  };
  let (next, frame) = decode(value, input, at, index, packet, link.info.serial)?;
  if (!cold && frame.frames != index.raw_frames[packet]) {
    return Err(pcm.Header);
  }
  let cursor = Cursor { index: index, codec: Some(next), link: current, packet: packet + 1, target: target, cold: false };
  if (cold) {
    return Ok((cursor, Pending));
  }
  let begin = cursor.index.positions[packet];
  let frames = cursor.index.frames[packet];
  if (frames == 0 || begin + frames as i64 <= target) {
    return Ok((cursor, Pending));
  }
  let skip = if (target > begin) { (target - begin) as i32 } else { 0 };
  let first = cursor.index.starts[packet] + skip;
  let end = cursor.index.starts[packet] + frames;
  let count = end - first;
  let channels = link.info.audio.channels;
  if (first < 0 || end > frame.frames || count < 1) {
    return Err(pcm.Header);
  }
  let samples = clipped(frame.samples, first * channels, end * channels);
  let position = begin + skip as i64;
  let Some(granule) = link.initial_granule +? position else {
    return Err(pcm.Limit);
  };
  let audio = vorbisfile.Audio { info: link.info, samples: samples, frames: count, position: position, granule: granule, packet_offset: cursor.index.locations[3 * packet + 1] as i64, page_offset: cursor.index.locations[3 * packet] as i64 };
  return Ok((cursor, Samples(audio)));
}

pub function (own state: Cursor) next(input: [u8]): Result[(Cursor, Option[vorbisfile.Audio]), pcm.AudioError] {
  while (true) {
    let (next, event) = state.step(input)?;
    state = next;
    match (event) {
      Pending => {},
      Samples(audio) => {
        return Ok((state, Some(audio)));
      },
      Finished => {
        return Ok((state, None));
      }
    }
  }
}
```
