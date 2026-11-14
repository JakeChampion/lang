# Ogg Opus playback and indexed seeks

This module decodes Ogg Opus files into 48 kHz interleaved float samples.
It selects the first Opus logical stream and supports sequential chains.
It ignores other logical streams while the selected stream is active.
The generic Ogg reader still checks those streams and applies its storage limits.

The decoder applies header gain, pre-skip, initial granule offsets, and final sample boundaries.
Channel mapping families 0, 1, and 255 use the multistream decoder.
Input sample rate remains metadata. Decoder output can exceed the device sample range.

Each `feed` call returns the next decoder, the consumed input count, and an optional event.
Continue with the unconsumed input. An event can consume zero input bytes.
An empty sample event records a packet that pre-skip or final trimming removes.
After the final input, call `feed([])` until it returns no event.
Then call `finish` to check the complete stream.

An error consumes the decoder. Create a new decoder after an error.

The decoder retains at most one page of completed packets, plus bounded Ogg partial packets and codec state.
A completed packet can start on an earlier page. The Ogg packet limit bounds its size.
Each sample event contains at most one decoded packet.
`Audio.position` counts playable frames from the start of its logical stream.
`Audio.granule` retains the corresponding absolute Ogg timestamp.

`inspect_seek` validates a complete input view and builds a packet index without PCM decode.
`seek_reader` consumes that index and creates a cursor at the first playable frame.
Each indexed packet requires 24 logical bytes, plus array capacity and link metadata.
The default index limits allow 16 MiB of encoded input and 1,048,576 packets.
The existing container, comment, codec, output, and link limits also apply.
The input bytes must remain unchanged throughout the lifetime of the index and cursor.

`inspect_seek_with` builds the same index from a source that reads a part of the file at a time.
It reads the whole file once, but it keeps only the index.
The index also keeps the end of the last page of each packet, in 4 more bytes for each packet.
`SeekCursor.want` gives the bytes of the file that the next step reads.
`SeekCursor.step_view` takes those bytes as a view that starts at the byte `base` of the file.
So a player can keep only a window of a long file.

`seek(link, position)` resets codec state and selects a playable frame within that logical link.
The default preroll is 3,840 frames, or 80 ms at 48 kHz.
`SeekLimits.preroll` accepts 3,840 through 480,000 frames.
Preroll starts on a packet boundary and includes at least the requested history when available.
Sample positions and final boundaries are exact, but finite preroll can differ from full-history PCM.
The default policy matches independent cold seeks with libopusfile.

`seek_exact(link, position)` instead decodes from the start of that link.
It reproduces full-history PCM without retained PCM or decoder checkpoints.
Its decode cost increases with the target position.
Both seek methods accept the exact end position and reset state on every call.
Subsequent reads continue through later links, including changes in channel count.

`step(input)` performs at most one packet decode or one link transition.
It returns `SeekPending`, `SeekSamples(audio)`, or `SeekEnd` with the next cursor.
An application can cancel or apply backpressure between calls.
`next(input)` continues through pending steps until samples or the end.
Both methods borrow the same complete encoded input view that created the index.
They retain codec state and return at most one packet of PCM at a time.

Packets within one page borrow the input directly for decode.
Packets across pages require a bounded copy of that packet.
`std/audio/opusfile/packets` also exposes this extraction helper.
An error consumes the cursor. Create a new cursor after an error.

```fern
<<*>>=
// Incremental Ogg Opus files with bounded packets and exact sample boundaries.

import "std/audio/ogg";
import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";
import "std/array";
import "std/audio/opusfile/headers";
import "std/audio/opusfile/timing";
import "std/audio/opusfile/packets";
import "std/opus/multistream";
import "std/opus/packet";
import "std/f32";
import "std/f64";

pub struct Limits { ogg: ogg.Limits, tags: headers.TagLimits, audio: multistream.Limits, max_links: i32 }

pub struct Info { index: i32, serial: u32, header: headers.Header }

pub struct Metadata { info: Info, tags: headers.Tags }

pub struct Audio {
  info: Info,
  samples: f32[],
  frames: i32,
  position: i64,
  granule: i64,
  packet_offset: i64,
  page_offset: i64,
}

pub struct Summary { info: Info, frames: i64, initial_granule: i64 }

pub enum Event { Begin(Info), Tags(Metadata), Samples(Audio), End(Summary) }

struct Link { info: Info, codec: multistream.Decoder, time: timing.Timing, gain: f32, stage: i32, tag_page: i64 }

struct Control { limits: Limits, links: i32 }

pub struct Decoder { control: Control, container: ogg.Decoder, link: Option[Link], queue: Option[ogg.Page], index: i32 }

pub function default_limits(): Limits {
  return Limits { ogg: ogg.default_limits(), tags: headers.default_tag_limits(), audio: multistream.default_limits(), max_links: 4096 };
}

function valid_limits(limits: Limits): boolean {
  return limits.max_links > 0 && limits.tags.max_bytes >= 0 && limits.tags.max_comments >= 0
    && limits.audio.max_streams > 0 && limits.audio.max_streams <= 255
    && limits.audio.max_input_bytes >= 0 && limits.audio.max_samples >= 0;
}

pub function decoder(limits: Limits): Result[Decoder, pcm.AudioError] {
  if (!valid_limits(limits)) {
    return Err(pcm.Limit);
  }
  return Ok(Decoder { control: Control { limits: limits, links: 0 }, container: ogg.decoder(limits.ogg)?, link: None, queue: None, index: 0 });
}

function incomplete(container: ogg.Decoder, serial: u32): boolean {
  for stream in container.streams {
    if (stream.serial == serial) {
      return stream.incomplete;
    }
  }
  return false;
}

function head_page(page: ogg.Page, container: ogg.Decoder): boolean {
  return page.bos && !page.eos && !page.continued && page.granule == 0i64
    && page.sequence == 0u32 && page.packets.len() == 1 && !incomplete(container, page.serial);
}

function gain(value: i32): f32 {
  let exponent = 0.000648814081f32 * (value as f32);
  return (0.6931471805599453094f64 * (exponent as f64)).exp() as f32;
}

function begin(control: Control, own container: ogg.Decoder, page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (page.packets.len() == 0 || !wire.tag(page.packets[0].bytes, 0, "OpusHead")) {
    return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, None));
  }
  if (!head_page(page, container)) {
    return Err(pcm.Header);
  }
  if (control.links >= control.limits.max_links) {
    return Err(pcm.Limit);
  }
  let header = headers.inspect(page.packets[0].bytes)?;
  let config = multistream.Config { sample_rate: 48000, channels: header.channels, streams: header.streams, coupled: header.coupled, mapping: header.mapping };
  let info = Info { index: control.links, serial: page.serial, header: header };
  let link = Link { info: info, codec: multistream.decoder(config, control.limits.audio)?, time: timing.create(header.pre_skip)?, gain: gain(header.output_gain), stage: 0, tag_page: -1i64 };
  let next = Decoder { control: Control { ...control, links: control.links + 1 }, container: container, link: Some(link), queue: None, index: 0 };
  return Ok((next, Some(Begin(info))));
}

function tag_page(page: ogg.Page, link: Link, container: ogg.Decoder): boolean {
  return page.packets.len() == 1 && !page.eos && page.granule == 0i64
    && page.packets[0].page_offset == link.tag_page && !incomplete(container, page.serial);
}

function metadata(control: Control, own container: ogg.Decoder, own link: Link, page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (link.tag_page < 0i64) {
    if (page.sequence != 1u32) {
      return Err(pcm.Header);
    }
    link = Link { ...link, tag_page: page.offset };
  }
  if (page.packets.len() == 0) {
    if (page.granule != -1i64 || !incomplete(container, page.serial)) {
      return Err(pcm.Header);
    }
    return Ok((Decoder { control: control, container: container, link: Some(link), queue: None, index: 0 }, None));
  }
  if (!tag_page(page, link, container)) {
    return Err(pcm.Header);
  }
  let tags = headers.tags(page.packets[0].bytes, control.limits.tags)?;
  let info = link.info;
  return Ok((Decoder { control: control, container: container, link: Some(Link { ...link, stage: 1 }), queue: None, index: 0 }, Some(Tags(Metadata { info: info, tags: tags }))));
}

function duration(packets: [ogg.Packet]): Result[i64, pcm.AudioError] {
  let frames = 0i64;
  for encoded in packets {
    let count = match (packet.sample_count(encoded.bytes, 48000)) { Ok(value) => value, Err(_) => { return Err(pcm.Header); } };
    if (count <= 0) {
      return Err(pcm.Header);
    }
    frames += count as i64;
  }
  return Ok(frames);
}

function audio_page(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  if (page.packets.len() == 0) {
    if (page.granule != -1i64) {
      return Err(pcm.Header);
    }
    if (!page.eos) {
      return Ok((Decoder { control: control, container: container, link: Some(link), queue: None, index: 0 }, None));
    }
  } else {
    link = Link { ...link, time: timing.page(link.time, page.granule, duration(page.packets)?, page.eos)? };
  }
  return Ok((Decoder { control: control, container: container, link: Some(link), queue: Some(page), index: 0 }, None));
}

function accept(own d: Decoder, own page: ogg.Page): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = d;
  match (link) {
    None => {
      return begin(control, container, page);
    },
    Some(selected) => {
      if (selected.info.serial != page.serial) {
        return Ok((Decoder { control: control, container: container, link: Some(selected), queue: None, index: 0 }, None));
      }
      if (selected.stage == 0) {
        return metadata(control, container, selected, page);
      }
      return audio_page(control, container, selected, page);
    }
  }
}

function amplified(value: f32, scale: f32): Result[f32, pcm.AudioError] {
  let output = value * scale;
  if (!output.is_finite()) {
    return Err(pcm.Sample);
  }
  return Ok(output);
}

function clipped(own input: f32[], range: timing.Range, channels: i32, scale: f32): Result[f32[], pcm.AudioError] {
  let start = range.start * channels;
  let end = range.end * channels;
  if (start == 0 && end == input.len()) {
    if (scale != 1.0f32) {
      for i in 0..input.len() {
        input = input.with(i, amplified(input[i], scale)?);
      }
    }
    return Ok(input);
  }
  let output: f32[] = [];
  for i in start..end {
    output = output.append(amplified(input[i], scale)?);
  }
  return Ok(output);
}

function decoded(control: Control, own container: ogg.Decoder, own link: Link, own page: ogg.Page, index: i32): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Link { info, codec, time, gain, stage, tag_page } = link;
  let encoded = page.packets[index];
  let (next, audio) = codec.decode(encoded.bytes)?;
  let (next_time, range) = timing.advance(time, audio.frames)?;
  let samples = clipped(audio.samples, range, audio.channels, gain)?;
  let value = Audio { info: info, samples: samples, frames: range.end - range.start, position: range.position, granule: range.granule, packet_offset: encoded.offset, page_offset: encoded.page_offset };
  let next_link = Link { info: info, codec: next, time: next_time, gain: gain, stage: stage, tag_page: tag_page };
  return Ok((Decoder { control: control, container: container, link: Some(next_link), queue: Some(page), index: index + 1 }, Some(Samples(value))));
}

function drain(own d: Decoder): Result[(Decoder, Option[Event]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = d;
  let Some(page) = queue else {
    return Err(pcm.Header);
  };
  let Some(selected) = link else {
    return Err(pcm.Header);
  };
  if (index < page.packets.len()) {
    return decoded(control, container, selected, page, index);
  }
  if (!page.eos) {
    return Ok((Decoder { control: control, container: container, link: Some(selected), queue: None, index: 0 }, None));
  }
  let frames = timing.finish(selected.time)?;
  let initial = match (selected.time.initial) { Some(value) => value, None => 0i64 };
  let summary = Summary { info: selected.info, frames: frames, initial_granule: initial };
  return Ok((Decoder { control: control, container: container, link: None, queue: None, index: 0 }, Some(End(summary))));
}

pub function (own d: Decoder) feed(input: [u8]): Result[(Decoder, i32, Option[Event]), pcm.AudioError] {
  if let Some(_) = d.queue {
    let (next, event) = drain(d)?;
    if let Some(_) = event {
      return Ok((next, 0, event));
    }
    return next.feed(input);
  }
  return read_page(d, input);
}

function read_page(own d: Decoder, input: [u8]): Result[(Decoder, i32, Option[Event]), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = d;
  let (next_container, used, page) = container.feed(input)?;
  let next = Decoder { control: control, container: next_container, link: link, queue: None, index: 0 };
  match (page) {
    None => {
      return Ok((next, used, None));
    },
    Some(value) => {
      let (next, event) = accept(next, value)?;
      return Ok((next, used, event));
    }
  }
}

pub function (d: Decoder) finish(): Result[void, pcm.AudioError] {
  if let Some(_) = d.queue {
    return Err(pcm.Buffer);
  }
  d.container.finish()?;
  if let Some(_) = d.link {
    return Err(pcm.Truncated);
  }
  if (d.control.links == 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

pub struct SeekLimits { file: Limits, max_input_bytes: i32, max_packets: i32, preroll: i32 }

pub struct SeekTrack { info: Info, first: i32, packets: i32, frames: i64, initial_granule: i64 }

pub struct SeekIndex { tracks: SeekTrack[], locations: i32[], positions: i64[], durations: i32[], ends: i32[], input_bytes: i32, limits: SeekLimits }

pub struct SeekCursor { index: SeekIndex, codec: Option[multistream.Decoder], link: i32, packet: i32, target: i64, scale: f32 }

pub enum SeekEvent { SeekPending, SeekSamples(Audio), SeekEnd }

struct IndexedPacket { location: packets.Location, position: i64, frames: i32 }

enum IndexEvent { IndexWait, IndexBegin(Info), IndexPacket(IndexedPacket), IndexEnd(Summary) }

pub function default_seek_limits(): SeekLimits {
  return SeekLimits { file: default_limits(), max_input_bytes: 16777216, max_packets: 1048576, preroll: 3840 };
}

function index_event(own event: Option[Event]): Result[IndexEvent, pcm.AudioError] {
  match (event) {
    None => {
      return Ok(IndexWait);
    },
    Some(Begin(info)) => {
      return Ok(IndexBegin(info));
    },
    Some(Tags(_)) => {
      return Ok(IndexWait);
    },
    Some(End(summary)) => {
      return Ok(IndexEnd(summary));
    },
    _ => {
      return Err(pcm.Header);
    }
  }
}

function indexed_packet(encoded: ogg.Packet, link: Link, limits: Limits): Result[IndexedPacket, pcm.AudioError] {
  if (encoded.bytes.len() > limits.audio.max_input_bytes) {
    return Err(pcm.Limit);
  }
  let count = match (packet.sample_count(encoded.bytes, 48000)) { Ok(value) => value, Err(_) => { return Err(pcm.Header); } };
  if (count * link.info.header.channels > limits.audio.max_samples) {
    return Err(pcm.Limit);
  }
  let location = packets.Location { offset: encoded.offset as i32, page_offset: encoded.page_offset as i32, bytes: encoded.bytes.len() };
  return Ok(IndexedPacket { location: location, position: link.time.decoded, frames: count });
}

function index_drain(own d: Decoder): Result[(Decoder, i32, IndexEvent), pcm.AudioError] {
  let Decoder { control, container, link, queue, index } = d;
  let Some(page) = queue else {
    return Err(pcm.Header);
  };
  let Some(selected) = link else {
    return Err(pcm.Header);
  };
  if (index >= page.packets.len()) {
    let (next, event) = drain(Decoder { control: control, container: container, link: Some(selected), queue: Some(page), index: index })?;
    return Ok((next, 0, index_event(event)?));
  }
  let value = indexed_packet(page.packets[index], selected, control.limits)?;
  let (time, _) = timing.advance(selected.time, value.frames)?;
  let next = Decoder { control: control, container: container, link: Some(Link { ...selected, time: time }), queue: Some(page), index: index + 1 };
  return Ok((next, 0, IndexPacket(value)));
}

function index_step(own d: Decoder, input: [u8]): Result[(Decoder, i32, IndexEvent), pcm.AudioError] {
  if let Some(_) = d.queue {
    return index_drain(d);
  }
  let (next, used, event) = read_page(d, input)?;
  return Ok((next, used, index_event(event)?));
}

// The index after one event. `end` is the end of the scanned bytes, which
// is the end of the page that completes the packet of the event.
function append_index(own index: SeekIndex, own event: IndexEvent, end: i32): Result[SeekIndex, pcm.AudioError] {
  match (event) {
    IndexBegin(info) => {
      let track = SeekTrack { info: info, first: index.positions.len(), packets: 0, frames: -1i64, initial_granule: 0i64 };
      return Ok(SeekIndex { ...index, tracks: index.tracks.append(track) });
    },
    IndexPacket(value) => {
      if (index.positions.len() >= index.limits.max_packets) {
        return Err(pcm.Limit);
      }
      let at = value.location;
      if (end < at.offset + at.bytes) {
        return Err(pcm.Header);
      }
      return Ok(SeekIndex { ...index, locations: index.locations.append(at.offset).append(at.page_offset).append(at.bytes), positions: index.positions.append(value.position), durations: index.durations.append(value.frames), ends: index.ends.append(end) });
    },
    IndexEnd(summary) => {
      let at = index.tracks.len() - 1;
      let old = index.tracks[at];
      let track = SeekTrack { ...old, packets: index.positions.len() - old.first, frames: summary.frames, initial_granule: summary.initial_granule };
      return Ok(SeekIndex { ...index, tracks: index.tracks.with(at, track) });
    },
    IndexWait => {
      return Ok(index);
    }
  }
}

function valid_seek_limits(size: i32, limits: SeekLimits): boolean {
  return limits.max_input_bytes >= 0 && size <= limits.max_input_bytes
    && limits.max_packets > 0 && limits.max_packets <= 715827882
    && limits.preroll >= 3840 && limits.preroll <= 480000;
}

function index_finished(event: IndexEvent, used: i32, at: i32, total: i32): boolean {
  return used == 0 && at == total && match (event) { IndexWait => true, _ => false };
}

function seek_index(size: i32, limits: SeekLimits): SeekIndex {
  return SeekIndex { tracks: [], locations: [], positions: [], durations: [], ends: [], input_bytes: size, limits: limits };
}

pub function inspect_seek(input: [u8], limits: SeekLimits): Result[SeekIndex, pcm.AudioError] {
  if (!valid_seek_limits(input.len(), limits)) {
    return Err(pcm.Limit);
  }
  let state = decoder(limits.file)?;
  let index = seek_index(input.len(), limits);
  let at = 0;
  while (true) {
    let (next, used, event) = index_step(state, input[at:])?;
    state = next;
    at += used;
    let finished = index_finished(event, used, at, input.len());
    index = append_index(index, event, at)?;
    if (finished) {
      break;
    }
  }
  state.finish()?;
  return Ok(index);
}

// The index of a file of `size` bytes that `read` gives a part at a time:
// `read(offset, count)` gives at most `count` bytes from `offset`, and at
// least one before the end of the file. The read goes once through the
// file.
pub function inspect_seek_with(size: i64, read: (i64, i32) => Result[u8[], IoError], limits: SeekLimits): Result[SeekIndex, source.ReadError] {
  if (size < 0i64 || size > limits.max_input_bytes as i64 || !valid_seek_limits(size as i32, limits)) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let n = size as i32;
  let scan = Scan { state: audio_result(decoder(limits.file))?, index: seek_index(n, limits), chunk: Chunk { bytes: [], base: 0, at: 0 }, done: false };
  while (!scan.done) {
    scan = scan_step(scan, read, n)?;
  }
  let Scan { state, index, chunk, done } = scan;
  audio_result(state.finish())?;
  return Ok(index);
}

// The state of `inspect_seek_with`: the decoder, the index so far, and the
// bytes of the file that the decoder did not take yet.
struct Scan { state: Decoder, index: SeekIndex, chunk: Chunk, done: boolean }

// The scan after one step of the decoder on the bytes of the file.
function scan_step(own s: Scan, read: (i64, i32) => Result[u8[], IoError], n: i32): Result[Scan, source.ReadError] {
  let Scan { state, index, chunk, done } = s;
  let c = refilled(chunk, read, n)?;
  let reads = match (state.queue) { None => true, _ => false };
  let (next, used, event) = audio_result(index_step(state, c.bytes[c.at:]))?;
  let at = c.base + c.at + used;
  let finished = index_finished(event, used, at, n);
  let waiting = wants_bytes(reads, used, event);
  let grown = audio_result(append_index(index, event, at))?;
  let moved = Chunk { ...c, at: c.at + used };
  if (waiting && !finished) {
    moved = extended(moved, read, n)?;
  }
  return Ok(Scan { state: next, index: grown, chunk: moved, done: finished });
}

// Whether a step that read a page took no bytes and gave no event: the
// page goes on past the bytes that the step had.
function wants_bytes(reads: boolean, used: i32, event: IndexEvent): boolean {
  return reads && used == 0 && match (event) { IndexWait => true, _ => false };
}

// The bytes of a file from the byte `base`, with the first `at` of them
// taken.
struct Chunk { bytes: u8[], base: i32, at: i32 }

// The next bytes of the file when `c` has no more, and `c` itself when it
// has some or the file ends.
function refilled(own c: Chunk, read: (i64, i32) => Result[u8[], IoError], n: i32): Result[Chunk, source.ReadError] {
  if (c.at < c.bytes.len() || c.base + c.at >= n) {
    return Ok(c);
  }
  let base = c.base + c.at;
  return Ok(Chunk { bytes: read_more(read, base, n)?, base: base, at: 0 });
}

// The bytes that `c` did not take, and the next bytes of the file after
// them: a page goes on past the bytes of `c`.
function extended(own c: Chunk, read: (i64, i32) => Result[u8[], IoError], n: i32): Result[Chunk, source.ReadError] {
  let rest = array.slice(c.bytes, c.at, c.bytes.len());
  let base = c.base + c.at;
  let more = read_more(read, base + rest.len(), n)?;
  return Ok(Chunk { bytes: rest.concat(more), base: base, at: 0 });
}

// At most SEEK_CHUNK bytes of the file from `at`, and at least one.
function read_more(read: (i64, i32) => Result[u8[], IoError], at: i32, n: i32): Result[u8[], source.ReadError] {
  let bytes = match (read(at as i64, SEEK_CHUNK.min(n - at))) { Ok(value) => value, Err(error) => { return Err(source.ReadIo(error)); } };
  if (bytes.len() == 0) {
    return Err(source.ReadAudio(pcm.Header));
  }
  return Ok(bytes);
}

// The bytes that `inspect_seek_with` asks for at a time.
const SEEK_CHUNK: i32 = 65536;

function audio_result[T](own value: Result[T, pcm.AudioError]): Result[T, source.ReadError] {
  return match (value) { Ok(v) => Ok(v), Err(error) => Err(source.ReadAudio(error)) };
}

pub function seek_reader(own index: SeekIndex): SeekCursor {
  let track = index.tracks[0];
  return SeekCursor { index: index, codec: None, link: 0, packet: track.first, target: track.info.header.pre_skip as i64, scale: gain(track.info.header.output_gain) };
}

function preroll_packet(index: SeekIndex, track: SeekTrack, target: i64): i32 {
  let low = track.first;
  let high = track.first + track.packets;
  let start = if (target > index.limits.preroll as i64) { target - index.limits.preroll as i64 } else { 0i64 };
  while (low < high) {
    let middle = low + (high - low) / 2;
    if (index.positions[middle] <= start) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }
  return if (low > track.first) { low - 1 } else { track.first };
}

function seek_to(own cursor: SeekCursor, link: i32, position: i64, exact: boolean): Result[SeekCursor, pcm.AudioError] {
  if (link < 0 || link >= cursor.index.tracks.len() || position < 0i64) {
    return Err(pcm.Header);
  }
  let track = cursor.index.tracks[link];
  if (position > track.frames) {
    return Err(pcm.Header);
  }
  let target = position + track.info.header.pre_skip as i64;
  let at = if (position == track.frames) { track.first + track.packets } else if (exact) { track.first } else { preroll_packet(cursor.index, track, target) };
  return Ok(SeekCursor { ...cursor, codec: None, link: link, packet: at, target: target, scale: gain(track.info.header.output_gain) });
}

pub function (own cursor: SeekCursor) seek(link: i32, position: i64): Result[SeekCursor, pcm.AudioError] {
  return seek_to(cursor, link, position, false);
}

pub function (own cursor: SeekCursor) seek_exact(link: i32, position: i64): Result[SeekCursor, pcm.AudioError] {
  return seek_to(cursor, link, position, true);
}

function seek_codec(own value: Option[multistream.Decoder], header: headers.Header, limits: Limits): Result[multistream.Decoder, pcm.AudioError] {
  match (value) {
    Some(codec) => {
      return Ok(codec);
    },
    None => {
      let config = multistream.Config { sample_rate: 48000, channels: header.channels, streams: header.streams, coupled: header.coupled, mapping: header.mapping };
      return multistream.decoder(config, limits.audio);
    }
  }
}

function seek_packet(own codec: multistream.Decoder, input: [u8], base: i32, place: packets.Location, track: SeekTrack, limits: Limits): Result[(multistream.Decoder, multistream.Audio), pcm.AudioError] {
  let location = packets.Location { offset: place.offset - base, page_offset: place.page_offset - base, bytes: place.bytes };
  if (packets.contiguous(input, location)?) {
    return codec.decode(input[location.offset:location.offset + location.bytes]);
  }
  let encoded = packets.read(input, location, track.info.serial, limits.audio.max_input_bytes)?;
  return codec.decode(encoded);
}

function seek_next_link(own cursor: SeekCursor): SeekCursor {
  let next = cursor.link + 1;
  if (next == cursor.index.tracks.len()) {
    return SeekCursor { ...cursor, codec: None, link: next };
  }
  let track = cursor.index.tracks[next];
  return SeekCursor { ...cursor, codec: None, link: next, packet: track.first, target: track.info.header.pre_skip as i64, scale: gain(track.info.header.output_gain) };
}

function seek_location(index: SeekIndex, at: i32): packets.Location {
  return packets.Location { offset: index.locations[3 * at], page_offset: index.locations[3 * at + 1], bytes: index.locations[3 * at + 2] };
}

function seek_audio(own audio: multistream.Audio, track: SeekTrack, location: packets.Location, begin: i64, start: i64, end: i64, scale: f32): Result[Audio, pcm.AudioError] {
  let Some(granule) = track.initial_granule +? start else {
    return Err(pcm.Limit);
  };
  let position = start - track.info.header.pre_skip as i64;
  let range = timing.Range { start: (start - begin) as i32, end: (end - begin) as i32, position: position, granule: granule };
  let samples = clipped(audio.samples, range, audio.channels, scale)?;
  return Ok(Audio { info: track.info, samples: samples, frames: range.end - range.start, position: position, granule: granule, packet_offset: location.offset as i64, page_offset: location.page_offset as i64 });
}

function seek_step(own cursor: SeekCursor, input: [u8], base: i32, track: SeekTrack): Result[(SeekCursor, Option[Audio]), pcm.AudioError] {
  let SeekCursor { index, codec, link, packet: at, target, scale } = cursor;
  let location = seek_location(index, at);
  let state = seek_codec(codec, track.info.header, index.limits.file)?;
  let (next, audio) = seek_packet(state, input, base, location, track, index.limits.file)?;
  if (audio.frames != index.durations[at]) {
    return Err(pcm.Header);
  }
  let begin = index.positions[at];
  let start = if (target > begin) { target } else { begin };
  let raw_end = begin + audio.frames as i64;
  let limit = track.frames + track.info.header.pre_skip as i64;
  let end = if (raw_end > limit) { limit } else { raw_end };
  let next_cursor = SeekCursor { index: index, codec: Some(next), link: link, packet: at + 1, target: target, scale: scale };
  if (start < end) {
    return Ok((next_cursor, Some(seek_audio(audio, track, location, begin, start, end, scale)?)));
  }
  return Ok((next_cursor, None));
}

function seek_link_ended(cursor: SeekCursor, track: SeekTrack): boolean {
  let limit = track.frames + track.info.header.pre_skip as i64;
  return cursor.packet >= track.first + track.packets || cursor.target >= limit || cursor.index.positions[cursor.packet] >= limit;
}

// The bytes of the file that the next step reads, as an offset and a
// count. A count of 0 means that the step reads no bytes.
pub function (cursor: SeekCursor) want(): (i64, i32) {
  if (cursor.link >= cursor.index.tracks.len()) {
    return (0i64, 0);
  }
  let track = cursor.index.tracks[cursor.link];
  if (seek_link_ended(cursor, track)) {
    return (0i64, 0);
  }
  let from = seek_location(cursor.index, cursor.packet).page_offset;
  return (from as i64, cursor.index.ends[cursor.packet] - from);
}

pub function (own cursor: SeekCursor) step(input: [u8]): Result[(SeekCursor, SeekEvent), pcm.AudioError] {
  if (input.len() != cursor.index.input_bytes) {
    return Err(pcm.Header);
  }
  return cursor.step_view(input, 0i64);
}

// A step on the view `input` of the file from the byte `base`. The view
// holds the bytes that `want` gives.
pub function (own cursor: SeekCursor) step_view(input: [u8], base: i64): Result[(SeekCursor, SeekEvent), pcm.AudioError] {
  let (from, count) = cursor.want();
  if (base < 0i64 || count > 0 && (base > from || from + count as i64 > base + input.len() as i64)) {
    return Err(pcm.Header);
  }
  if (cursor.link >= cursor.index.tracks.len()) {
    return Ok((cursor, SeekEnd));
  }
  let track = cursor.index.tracks[cursor.link];
  if (seek_link_ended(cursor, track)) {
    return Ok((seek_next_link(cursor), SeekPending));
  }
  let (next, audio) = seek_step(cursor, input, base as i32, track)?;
  return Ok((next, match (audio) { Some(value) => SeekSamples(value), None => SeekPending }));
}

pub function (own cursor: SeekCursor) next(input: [u8]): Result[(SeekCursor, Option[Audio]), pcm.AudioError] {
  while (true) {
    let (next, event) = cursor.step(input)?;
    cursor = next;
    match (event) {
      SeekSamples(audio) => {
        return Ok((cursor, Some(audio)));
      },
      SeekEnd => {
        return Ok((cursor, None));
      },
      SeekPending => {}
    }
  }
}
```
