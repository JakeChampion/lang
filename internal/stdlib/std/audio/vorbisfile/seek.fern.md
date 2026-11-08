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

```fern
<<*>>=
// Index immutable Ogg Vorbis input and decode exact sample ranges with bounded work.

import "std/audio/vorbis";
import "std/audio/vorbisfile";
import "std/audio/opusfile/packets";
import "std/audio/pcm";

pub struct Limits { file: vorbisfile.Limits, max_input_bytes: i32, max_packets: i32 }

pub struct Link { info: vorbisfile.Info, identification: packets.Location, setup: packets.Location, first: i32, packets: i32, frames: i64, initial_granule: i64 }

pub opaque struct Index { links: Link[], locations: i32[], raw_frames: i32[], starts: i32[], frames: i32[], positions: i64[], bytes: i32, limits: Limits }

pub opaque struct Cursor { index: Index, codec: Option[vorbis.Decoder], link: i32, packet: i32, target: i64, cold: boolean }

pub enum ReadEvent { Pending, Samples(vorbisfile.Audio), Finished }

pub function default_limits(): Limits {
  return Limits { file: vorbisfile.default_limits(), max_input_bytes: 16777216, max_packets: 1048576 };
}

function location(value: vorbisfile.Location, input: [u8]): Result[packets.Location, pcm.AudioError] {
  if (value.page_offset < 0i64 || value.page_offset > input.len() as i64 || value.offset < value.page_offset || value.offset > input.len() as i64 || value.bytes < 1 || value.bytes > input.len() - value.offset as i32) {
    return Err(pcm.Header);
  }
  return Ok(packets.Location { page_offset: value.page_offset as i32, offset: value.offset as i32, bytes: value.bytes });
}

// Append each scalar column only after packet and extent limits pass.
// fern-lint: allow cyclomatic-complexity
function append(own index: Index, value: vorbisfile.Indexed, input: [u8]): Result[Index, pcm.AudioError] {
  if (index.frames.len() >= index.limits.max_packets) {
    return Err(pcm.Limit);
  }
  let at = location(value.location, input)?;
  if (value.raw_frames < 0 || value.raw_frames > 4096 || value.start < 0 || value.frames < 0 || value.start > value.raw_frames || value.frames > value.raw_frames - value.start || value.position < 0i64) {
    return Err(pcm.Header);
  }
  let Index { links, locations, raw_frames, starts, frames, positions, bytes, limits } = index;
  return Ok(Index { links: links, locations: locations.append(at.page_offset).append(at.offset).append(at.bytes), raw_frames: raw_frames.append(value.raw_frames), starts: starts.append(value.start), frames: frames.append(value.frames), positions: positions.append(value.position), bytes: bytes, limits: limits });
}

// Keep scan event order, shared packet budgets, and link completion together.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  if (limits.max_input_bytes < 0 || input.len() > limits.max_input_bytes || limits.max_packets < 1 || limits.max_packets > 715827882) {
    return Err(pcm.Limit);
  }
  let scanner = vorbisfile.scan(limits.file)?;
  let result = Index { links: [], locations: [], raw_frames: [], starts: [], frames: [], positions: [], bytes: input.len(), limits: limits };
  let ready: Option[vorbisfile.Prepared] = None;
  let first = 0;
  let at = 0;
  while (true) {
    let eof = at == input.len();
    let (next, used, event) = scanner.feed(input[at:])?;
    scanner = next;
    at += used;
    match (event) {
      Some(vorbisfile.ScanSetup(value)) => {
        ready = Some(value);
        first = result.frames.len();
      },
      Some(vorbisfile.ScanPacket(value)) => {
        let Some(config) = ready else {
          return Err(pcm.Header);
        };
        if (value.info.index != config.info.index || config.info.index != result.links.len()) {
          return Err(pcm.Header);
        }
        result = append(result, value, input)?;
      },
      Some(vorbisfile.ScanEnd(value)) => {
        let Some(config) = ready else {
          return Err(pcm.Header);
        };
        if (value.info.index != config.info.index || first >= result.frames.len()) {
          return Err(pcm.Header);
        }
        let link = Link { info: value.info, identification: location(config.identification, input)?, setup: location(config.setup, input)?, first: first, packets: result.frames.len() - first, frames: value.frames, initial_granule: value.initial_granule };
        result = Index { ...result, links: result.links.append(link) };
        ready = None;
      },
      None => {
        if (eof) {
          break;
        }
        if (used == 0 && at < input.len()) {
          return Err(pcm.Header);
        }
      },
      _ => {}
    }
  }
  scanner.finish()?;
  return Ok(result);
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

function load(input: [u8], index: Index, link: Link): Result[vorbis.Decoder, pcm.AudioError] {
  let identification = packets.read(input, link.identification, link.info.serial, index.limits.file.ogg.max_packet_bytes)?;
  let setup = packets.read(input, link.setup, link.info.serial, index.limits.file.ogg.max_packet_bytes)?;
  return vorbis.create(identification, setup, index.limits.file.codec);
}

function decode(own codec: vorbis.Decoder, input: [u8], index: Index, packet: i32, serial: u32): Result[(vorbis.Decoder, vorbis.Frame), pcm.AudioError] {
  let at = 3 * packet;
  let location = packets.Location { page_offset: index.locations[at], offset: index.locations[at + 1], bytes: index.locations[at + 2] };
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

// A step parses configuration or decodes one packet, including one cold overlap primer.
// fern-lint: allow cyclomatic-complexity
pub function (own state: Cursor) step(input: [u8]): Result[(Cursor, ReadEvent), pcm.AudioError] {
  if (input.len() != state.index.bytes) {
    return Err(pcm.Header);
  }
  if (state.link >= state.index.links.len()) {
    return Ok((state, Finished));
  }
  let link = state.index.links[state.link];
  if (state.packet >= link.first + link.packets) {
    return complete(state);
  }
  let Cursor { index, codec, link: current, packet, target, cold } = state;
  let Some(value) = codec else {
    let created = load(input, index, link)?;
    return Ok((Cursor { index: index, codec: Some(created), link: current, packet: packet, target: target, cold: true }, Pending));
  };
  let (next, frame) = decode(value, input, index, packet, link.info.serial)?;
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
