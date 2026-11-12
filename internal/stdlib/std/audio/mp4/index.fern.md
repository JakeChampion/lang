# Bounded ISO audio indexing

This module indexes complete, self-contained ISO audio tracks with a declared sample-entry type.
It shares movie clocks, packet tables, media extents, track identifiers, and edit lists across codecs.
Codec modules validate decoder-specific configuration and supply PCM.
The index retains byte offsets and metadata without PCM or a copy of the input.

Fragmented files, encryption, and external media references are unsupported.
Box, track, description, packet, edit, and encoded-input budgets bound parsing.

```fern
<<*>>=
// Index ISO audio tracks with bounded packet tables and edit lists.
import "std/audio/mp4/boxes";
import "std/audio/mp4/entries";
import "std/audio/mp4/tables";
import "std/audio/mp4/time";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Limits { audio: pcm.Limits, max_boxes: i32, max_tracks: i32, max_packets: i32, max_descriptions: i32, max_edits: i32 }

pub struct Track { id: u32, enabled: boolean, clock: time.Clock, configurations: boxes.Box[], packets: tables.Packet[], edits: time.Edit[] }

pub struct Index { movie: time.Clock, tracks: Track[], bytes: i64, budget: boxes.Budget }

struct Scan { budget: boxes.Budget, media: tables.Media[], limits: Limits, packets: i32, file_bytes: i64 }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_boxes: 65536, max_tracks: 32, max_packets: 1048576, max_descriptions: 64, max_edits: 64 };
}

function descriptions(input: [u8], box: boxes.Box, budget: boxes.Budget, maximum: i32, kind: u32): Result[(boxes.Budget, boxes.Box[]), pcm.AudioError] {
  if (boxes.full(input, box, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  let count = wire.be(input, box.data + 4, 4);
  if (count == 0u64) {
    return Err(pcm.Header);
  }
  if (maximum < 0 || count > maximum as u64) {
    return Err(pcm.Limit);
  }
  let (next, values) = boxes.children(input, box.data + 8, box.end, budget)?;
  if (values.len() as u64 != count) {
    return Err(pcm.Header);
  }
  for value in values {
    let _ = entries.header(input, value, kind)?;
  }
  return Ok((next, values));
}

function exclusive(values: [boxes.Box], first: string, second: string): Result[boxes.Box, pcm.AudioError] {
  let a = boxes.one(values, first)?;
  let b = boxes.one(values, second)?;
  match ((a, b)) {
    (Some(value), None()) => {
      return Ok(value);
    },
    (None(), Some(value)) => {
      return Ok(value);
    },
    _ => {
      return Err(pcm.Header);
    }
  }
}

// Validate every composition row before accepting the zero-offset audio case.
// fern-lint: allow cyclomatic-complexity
function composition(input: [u8], values: [boxes.Box], maximum: i32): Result[void, pcm.AudioError] {
  let Some(box) = boxes.one(values, "ctts")? else {
    return Ok(());
  };
  let version = boxes.full(input, box, 8)?;
  if (version != 0 && version != 1) {
    return Err(pcm.Unsupported);
  }
  let count = wire.be(input, box.data + 4, 4);
  if (count > maximum as u64) {
    return Err(pcm.Limit);
  }
  if ((box.end - box.data) as i64 != 8i64 + 8i64 * (count as i64)) {
    return Err(pcm.Header);
  }
  let total = 0u64;
  for i in 0..count as i32 {
    let entries = wire.be(input, box.data + 8 + 8 * i, 4);
    if (entries == 0u64) {
      return Err(pcm.Header);
    }
    total += entries;
    if (total > maximum as u64) {
      return Err(pcm.Header);
    }
    if (wire.be(input, box.data + 12 + 8 * i, 4) != 0u64) {
      return Err(pcm.Unsupported);
    }
  }
  if (total != maximum as u64) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// Check the dependent sample tables before constructing packet ranges.
// fern-lint: allow cyclomatic-complexity
function sample_table(input: [u8], stbl: boxes.Box, own scan: Scan, kind: u32): Result[(Scan, boxes.Box[], tables.Packet[]), pcm.AudioError] {
  let (b1, children) = boxes.children(input, stbl.data, stbl.end, scan.budget)?;
  let (b2, descriptions) = descriptions(input, boxes.required(children, "stsd")?, b1, scan.limits.max_descriptions, kind)?;
  let remaining = scan.limits.max_packets - scan.packets;
  let sizes = tables.sizes(input, exclusive(children, "stsz", "stz2")?, remaining)?;
  packet_limits(sizes, scan.limits.audio.max_input_bytes)?;
  composition(input, children, sizes.len())?;
  let offsets = tables.offsets(input, exclusive(children, "stco", "co64")?, scan.limits.max_packets, scan.file_bytes)?;
  let chunks = tables.chunks(input, boxes.required(children, "stsc")?, scan.limits.max_packets)?;
  let times = tables.times(input, boxes.required(children, "stts")?, scan.limits.max_packets)?;
  let packets = tables.packets(sizes, offsets, chunks, times, scan.media, descriptions.len())?;
  return Ok((Scan { ...scan, budget: b2, packets: scan.packets + packets.len() }, descriptions, packets));
}

// Follow the fixed audio-track hierarchy with checked reads at each level.
// fern-lint: allow cyclomatic-complexity
function track(input: [u8], box: boxes.Box, own scan: Scan, kind: u32): Result[(Scan, Option[Track]), pcm.AudioError] {
  let (b1, children) = boxes.children(input, box.data, box.end, scan.budget)?;
  let mdia = boxes.required(children, "mdia")?;
  let (b2, media) = boxes.children(input, mdia.data, mdia.end, b1)?;
  let hdlr = boxes.required(media, "hdlr")?;
  if (boxes.full(input, hdlr, 24)? != 0) {
    return Err(pcm.Unsupported);
  }
  if (wire.be(input, hdlr.data + 8, 4) as u32 != boxes.tag("soun")) {
    return Ok((Scan { ...scan, budget: b2 }, None));
  }
  let id = time.track_id(input, boxes.required(children, "tkhd")?)?;
  let clock = time.clock(input, boxes.required(media, "mdhd")?)?;
  let minf = boxes.required(media, "minf")?;
  let (b3, information) = boxes.children(input, minf.data, minf.end, b2)?;
  let b4 = entries.data_reference(input, information, b3)?;
  let (next, configurations, packets) = sample_table(input, boxes.required(information, "stbl")?, Scan { ...scan, budget: b4 }, kind)?;
  let (b5, edits) = time.edits(input, children, next.budget, next.limits.max_edits)?;
  let last = packets[packets.len() - 1];
  if (clock.duration != 0i64 && clock.duration != last.position + last.duration as i64) {
    return Err(pcm.Header);
  }
  let value = Track { id: id.id, enabled: id.enabled, clock: clock, configurations: configurations, packets: packets, edits: edits };
  return Ok((Scan { ...next, budget: b5 }, Some(value)));
}

function packet_limits(sizes: [i32], maximum: i32): Result[void, pcm.AudioError] {
  for size in sizes {
    if (size > maximum) {
      return Err(pcm.Limit);
    }
  }
  return Ok(());
}

function file_type(input: [u8], top: [boxes.Box]): Result[void, pcm.AudioError] {
  let box = boxes.required(top, "ftyp")?;
  if (box.end - box.data < 8 || (box.end - box.data) % 4 != 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

pub(package) function validate_limits(input: [u8], limits: Limits): Result[void, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (limits.max_boxes < 1 || limits.max_tracks < 1 || limits.max_packets < 1 || limits.max_descriptions < 1 || limits.max_edits < 0) {
    return Err(pcm.Limit);
  }
  return Ok(());
}

function media_ranges(top: [boxes.Box]): Result[tables.Media[], pcm.AudioError] {
  let media: tables.Media[] = [];
  for box in top {
    if (box.kind == boxes.tag("moof")) {
      return Err(pcm.Unsupported);
    }
    if (box.kind == boxes.tag("mdat")) {
      media = media.append(tables.Media { start: box.data as i64, end: box.end as i64 });
    }
  }
  return Ok(media);
}

function add_track(input: [u8], box: boxes.Box, own scan: Scan, own tracks: Track[], kind: u32): Result[(Scan, Track[]), pcm.AudioError] {
  let (next, value) = track(input, box, scan, kind)?;
  if let Some(value) = value {
    for previous in tracks {
      if (previous.id == value.id) {
        return Err(pcm.Header);
      }
    }
    return Ok((next, tracks.append(value)));
  }
  return Ok((next, tracks));
}

function movie_tracks(input: [u8], children: [boxes.Box], own scan: Scan, kind: u32): Result[Index, pcm.AudioError] {
  let movie = time.clock(input, boxes.required(children, "mvhd")?)?;
  let tracks: Track[] = [];
  let count = 0;
  for box in children {
    if (box.kind == boxes.tag("trak")) {
      count += 1;
      if (count > scan.limits.max_tracks) {
        return Err(pcm.Limit);
      }
      let (next, values) = add_track(input, box, scan, tracks, kind)?;
      scan = next;
      tracks = values;
    }
  }
  if (tracks.len() == 0) {
    return Err(pcm.Unsupported);
  }
  return Ok(Index { movie: movie, tracks: tracks, bytes: scan.file_bytes, budget: scan.budget });
}

function movie_children(input: [u8], moov: boxes.Box, budget: boxes.Budget): Result[(boxes.Budget, boxes.Box[]), pcm.AudioError] {
  let (next, children) = boxes.children(input, moov.data, moov.end, budget)?;
  if let Some(_) = boxes.one(children, "mvex")? {
    return Err(pcm.Unsupported);
  }
  return Ok((next, children));
}

pub function inspect(input: [u8], limits: Limits, kind: u32): Result[Index, pcm.AudioError] {
  validate_limits(input, limits)?;
  let (budget, top) = boxes.children(input, 0, input.len(), boxes.Budget { boxes: 0, maximum: limits.max_boxes })?;
  file_type(input, top)?;
  let (next, children) = movie_children(input, boxes.required(top, "moov")?, budget)?;
  let media = media_ranges(top)?;
  if (media.len() == 0) {
    return Err(pcm.Header);
  }
  return movie_tracks(input, children, Scan { budget: next, media: media, limits: limits, packets: 0, file_bytes: input.len() as i64 }, kind);
}

function source_ranges(media: [tables.Media], file_bytes: i64, top_boxes: i32, maximum: i32): Result[void, pcm.AudioError] {
  if (media.len() == 0 || media.len() > top_boxes || top_boxes < 1 || top_boxes > maximum) {
    return Err(pcm.Header);
  }
  let end = 0i64;
  for range in media {
    if (range.start < end || range.end < range.start || range.end > file_bytes) {
      return Err(pcm.Header);
    }
    end = range.end;
  }
  return Ok(());
}

// The shared file scanner supplies one movie box and absolute media ranges.
pub(package) function inspect_movie(input: [u8], media: tables.Media[], file_bytes: i64, top_boxes: i32, limits: Limits, kind: u32): Result[Index, pcm.AudioError] {
  validate_limits(input, limits)?;
  if (file_bytes < input.len() as i64) {
    return Err(pcm.Header);
  }
  source_ranges(media, file_bytes, top_boxes, limits.max_boxes)?;
  let moov = boxes.read(input, 0, input.len())?;
  if (moov.kind != boxes.tag("moov") || moov.end != input.len()) {
    return Err(pcm.Header);
  }
  let (next, children) = movie_children(input, moov, boxes.Budget { boxes: top_boxes, maximum: limits.max_boxes })?;
  return movie_tracks(input, children, Scan { budget: next, media: media, limits: limits, packets: 0, file_bytes: file_bytes }, kind);
}
```
