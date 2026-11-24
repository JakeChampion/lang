# ISO audio movie fragments

This module reads the packet runs of fragmented ISO audio files.
It reads track defaults from `trex` boxes and packet runs from each `moof` box in file order.
Each track fragment supplies its base data offset, its sample defaults, and zero or more `trun` boxes.

The base data offset follows ISO/IEC 14496-12.
An explicit `tfhd` offset has priority. The `default-base-is-moof` flag selects the start of the parent `moof` box.
If neither is set, the first track fragment starts at the `moof` box.
Each later track fragment starts where the data of the track fragment before it ends.
A run without a data offset starts where the run before it ends.

Packets of the indexed tracks must lie inside a media-data payload.
A `tfdt` decode time must continue the track timeline without a gap.
Nonzero composition offsets are unsupported, as in the `ctts` table.
Fragments of other tracks only advance the data offset.

The `moof` bytes can be a copy that the seekable-file scanner retains.
Each `Fragment` gives the file offset of its box, so data offsets stay absolute.

```fern
<<*>>=
// Read the packet runs of fragmented ISO audio tracks.
import "std/audio/mp4/boxes";
import "std/audio/mp4/tables";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Defaults { track: u32, description: u32, duration: u32, bytes: u32 }

pub struct Target { id: u32, descriptions: i32, packets: tables.Packet[] }

pub struct Scan { budget: boxes.Budget, packets: i32, max_packets: i32, max_bytes: i32, file_bytes: i64 }

pub struct Fragment { box: boxes.Box, at: i64 }

struct Header { track: u32, base: i64, description: u32, duration: u32, bytes: u32, empty: boolean }

struct Run { at: i64, position: i64, packets: tables.Packet[] }

const BASE_DATA_OFFSET: u64 = 0x1u64;

const DESCRIPTION_INDEX: u64 = 0x2u64;

const DEFAULT_DURATION: u64 = 0x8u64;

const DEFAULT_SIZE: u64 = 0x10u64;

const DEFAULT_FLAGS: u64 = 0x20u64;

const DURATION_IS_EMPTY: u64 = 0x10000u64;

const BASE_IS_MOOF: u64 = 0x20000u64;

const HEADER_FLAGS: u64 = 0x3003bu64;

const DATA_OFFSET: u64 = 0x1u64;

const FIRST_FLAGS: u64 = 0x4u64;

const SAMPLE_DURATION: u64 = 0x100u64;

const SAMPLE_SIZE: u64 = 0x200u64;

const SAMPLE_FLAGS: u64 = 0x400u64;

const SAMPLE_COMPOSITION: u64 = 0x800u64;

const RUN_FLAGS: u64 = 0xf05u64;

// The track defaults of the `mvex` box. Each track has at most one `trex` box.
pub function defaults(input: [u8], mvex: boxes.Box, budget: boxes.Budget, maximum: i32): Result[(boxes.Budget, Defaults[]), pcm.AudioError] {
  let (next, children) = boxes.children(input, mvex.data, mvex.end, budget)?;
  let out: Defaults[] = [];
  for box in children {
    if (box.kind == boxes.tag("trex")) {
      if (boxes.full(input, box, 24)? != 0) {
        return Err(pcm.Unsupported);
      }
      if (box.end - box.data != 24) {
        return Err(pcm.Header);
      }
      if (out.len() >= maximum) {
        return Err(pcm.Limit);
      }
      let value = Defaults { track: wire.be(input, box.data + 4, 4) as u32, description: wire.be(input, box.data + 8, 4) as u32, duration: wire.be(input, box.data + 12, 4) as u32, bytes: wire.be(input, box.data + 16, 4) as u32 };
      if let Some(_) = find(out, value.track) {
        return Err(pcm.Header);
      }
      out = out.append(value);
    }
  }
  if (out.len() == 0) {
    return Err(pcm.Header);
  }
  return Ok((next, out));
}

function find(values: [Defaults], track: u32): Option[Defaults] {
  for value in values {
    if (value.track == track) {
      return Some(value);
    }
  }
  return None;
}

function sequence(input: [u8], mfhd: boxes.Box): Result[i64, pcm.AudioError] {
  if (boxes.full(input, mfhd, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  if (mfhd.end - mfhd.data != 8) {
    return Err(pcm.Header);
  }
  return Ok(wire.be(input, mfhd.data + 4, 4) as i64);
}

function field(input: [u8], at: i32, present: boolean, fallback: u32): (i32, u32) {
  if (present) {
    return (at + 4, wire.be(input, at, 4) as u32);
  }
  return (at, fallback);
}

// Resolve every optional `tfhd` field against the `trex` defaults of its track.
// fern-lint: allow cyclomatic-complexity
function track_header(input: [u8], tfhd: boxes.Box, moof: Fragment, previous: i64, file_bytes: i64, defaults: [Defaults]): Result[Header, pcm.AudioError] {
  if (tfhd.data < 0 || tfhd.end > input.len() || tfhd.end < tfhd.data) {
    return Err(pcm.Header);
  }
  if (tfhd.end - tfhd.data < 8) {
    return Err(pcm.Truncated);
  }
  let flags = wire.be(input, tfhd.data + 1, 3);
  if (input[tfhd.data] != 0u8 || (flags & (HEADER_FLAGS ^ 0xffffffu64)) != 0u64) {
    return Err(pcm.Unsupported);
  }
  let words = 0;
  for bit in [DESCRIPTION_INDEX, DEFAULT_DURATION, DEFAULT_SIZE, DEFAULT_FLAGS] {
    if ((flags & bit) != 0u64) {
      words += 1;
    }
  }
  if (tfhd.end - tfhd.data != 8 + (if ((flags & BASE_DATA_OFFSET) != 0u64) { 8 } else { 0 }) + 4 * words) {
    return Err(pcm.Header);
  }
  let track = wire.be(input, tfhd.data + 4, 4) as u32;
  let Some(value) = find(defaults, track) else {
    return Err(pcm.Header);
  };
  let at = tfhd.data + 8;
  let base = if ((flags & BASE_IS_MOOF) != 0u64) { moof.at } else { previous };
  if ((flags & BASE_DATA_OFFSET) != 0u64) {
    let explicit = wire.be(input, at, 8);
    if (explicit > file_bytes as u64) {
      return Err(pcm.Header);
    }
    base = explicit as i64;
    at += 8;
  }
  let (a1, description) = field(input, at, (flags & DESCRIPTION_INDEX) != 0u64, value.description);
  let (a2, duration) = field(input, a1, (flags & DEFAULT_DURATION) != 0u64, value.duration);
  let (_, bytes) = field(input, a2, (flags & DEFAULT_SIZE) != 0u64, value.bytes);
  return Ok(Header { track: track, base: base, description: description, duration: duration, bytes: bytes, empty: (flags & DURATION_IS_EMPTY) != 0u64 });
}

// A `tfdt` box must start the fragment where the earlier packets of its track end.
function decode_time(input: [u8], children: [boxes.Box], position: i64): Result[void, pcm.AudioError] {
  let Some(tfdt) = boxes.one(children, "tfdt")? else {
    return Ok(());
  };
  let version = boxes.full(input, tfdt, 8)?;
  let width = if (version == 0) { 4 } else if (version == 1) { 8 } else { return Err(pcm.Unsupported); };
  if (tfdt.end - tfdt.data != 4 + width) {
    return Err(pcm.Header);
  }
  if (wire.be(input, tfdt.data + 4, width) != position as u64) {
    return Err(pcm.Unsupported);
  }
  return Ok(());
}

function signed(value: u64): i64 {
  return if (value >= 2147483648u64) { value as i64 - 4294967296i64 } else { value as i64 };
}

// Check the run layout, then build one packet per sample. Runs of tracks that
// are not indexed only advance the data offset.
// fern-lint: allow cyclomatic-complexity
function sample_run(input: [u8], trun: boxes.Box, header: Header, tracked: boolean, descriptions: i32, media: [tables.Media], ordered: boolean, own run: Run, own scan: Scan): Result[(Scan, Run), pcm.AudioError] {
  if (trun.data < 0 || trun.end > input.len() || trun.end < trun.data) {
    return Err(pcm.Header);
  }
  if (trun.end - trun.data < 8) {
    return Err(pcm.Truncated);
  }
  let version = input[trun.data];
  let flags = wire.be(input, trun.data + 1, 3);
  if (version > 1u8 || (flags & (RUN_FLAGS ^ 0xffffffu64)) != 0u64) {
    return Err(pcm.Unsupported);
  }
  if (header.empty) {
    return Err(pcm.Header);
  }
  let count = wire.be(input, trun.data + 4, 4);
  if (count > (scan.max_packets - scan.packets) as u64) {
    return Err(pcm.Limit);
  }
  let fields = 0;
  for bit in [SAMPLE_DURATION, SAMPLE_SIZE, SAMPLE_FLAGS, SAMPLE_COMPOSITION] {
    if ((flags & bit) != 0u64) {
      fields += 1;
    }
  }
  let prefix = 8 + (if ((flags & DATA_OFFSET) != 0u64) { 4 } else { 0 }) + (if ((flags & FIRST_FLAGS) != 0u64) { 4 } else { 0 });
  if ((trun.end - trun.data) as i64 != prefix as i64 + count as i64 * (4i64 * (fields as i64))) {
    return Err(pcm.Header);
  }
  let at = run.at;
  if ((flags & DATA_OFFSET) != 0u64) {
    at = header.base + signed(wire.be(input, trun.data + 8, 4));
  }
  if (tracked && (header.description == 0u32 || header.description > descriptions as u32)) {
    return Err(pcm.Header);
  }
  let position = run.position;
  let packets = run.packets;
  let p = trun.data + prefix;
  for _ in 0..count as i32 {
    let (p1, duration) = field(input, p, (flags & SAMPLE_DURATION) != 0u64, header.duration);
    let (p2, bytes) = field(input, p1, (flags & SAMPLE_SIZE) != 0u64, header.bytes);
    let p3 = if ((flags & SAMPLE_FLAGS) != 0u64) { p2 + 4 } else { p2 };
    let (p4, offset) = field(input, p3, (flags & SAMPLE_COMPOSITION) != 0u64, 0u32);
    p = p4;
    if (bytes == 0u32 || bytes > 2147483647u32) {
      return Err(pcm.Header);
    }
    if (tracked) {
      if (duration == 0u32) {
        return Err(pcm.Header);
      }
      if (offset != 0u32) {
        return Err(pcm.Unsupported);
      }
      if (bytes as i32 > scan.max_bytes) {
        return Err(pcm.Limit);
      }
      if (!tables.contained(media, ordered, at, bytes as i32)) {
        return Err(pcm.Header);
      }
      packets = packets.append(tables.Packet { offset: at, bytes: bytes as i32, description: header.description as i32 - 1, position: position, duration: duration });
      position += duration as i64;
    }
    let Some(next) = at +? bytes as i64 else {
      return Err(pcm.Header);
    };
    at = next;
  }
  return Ok((Scan { ...scan, packets: scan.packets + count as i32 }, Run { at: at, position: position, packets: packets }));
}

function target(targets: [Target], track: u32): i32 {
  for i in 0..targets.len() {
    if (targets[i].id == track) {
      return i;
    }
  }
  return -1;
}

function end(packets: [tables.Packet]): i64 {
  if (packets.len() == 0) {
    return 0i64;
  }
  let last = packets[packets.len() - 1];
  return last.position + last.duration as i64;
}

// Append the runs of one track fragment and return where its data ends.
function track_fragment(input: [u8], moof: Fragment, traf: boxes.Box, previous: i64, defaults: [Defaults], own targets: Target[], media: [tables.Media], ordered: boolean, own scan: Scan): Result[(Scan, Target[], i64), pcm.AudioError] {
  let (budget, children) = boxes.children(input, traf.data, traf.end, scan.budget)?;
  scan = Scan { ...scan, budget: budget };
  let header = track_header(input, boxes.required(children, "tfhd")?, moof, previous, scan.file_bytes, defaults)?;
  let number = target(targets, header.track);
  let tracked = number >= 0;
  let packets: tables.Packet[] = [];
  let descriptions = 0;
  if (tracked) {
    let value = targets[number];
    packets = value.packets;
    descriptions = value.descriptions;
    decode_time(input, children, end(packets))?;
  }
  let run = Run { at: header.base, position: end(packets), packets: packets };
  for box in children {
    if (box.kind == boxes.tag("trun")) {
      let (next, after) = sample_run(input, box, header, tracked, descriptions, media, ordered, run, scan)?;
      scan = next;
      run = after;
    }
  }
  if (tracked) {
    let value = targets[number];
    targets = targets.with(number, Target { ...value, packets: run.packets });
  }
  return Ok((scan, targets, run.at));
}

// Append the packets of every `moof` box, in file order, to the matching targets.
pub function append(input: [u8], moofs: [Fragment], defaults: [Defaults], own targets: Target[], media: [tables.Media], own scan: Scan): Result[(Scan, Target[]), pcm.AudioError] {
  let ordered = media.len() > 16 && tables.ordered_media(media);
  let previous_sequence = -1i64;
  for moof in moofs {
    let (budget, children) = boxes.children(input, moof.box.data, moof.box.end, scan.budget)?;
    scan = Scan { ...scan, budget: budget };
    let number = sequence(input, boxes.required(children, "mfhd")?)?;
    if (number <= previous_sequence) {
      return Err(pcm.Header);
    }
    previous_sequence = number;
    let data_end = moof.at;
    for box in children {
      if (box.kind == boxes.tag("traf")) {
        let (next, values, after) = track_fragment(input, moof, box, data_end, defaults, targets, media, ordered, scan)?;
        scan = next;
        targets = values;
        data_end = after;
      }
    }
  }
  return Ok((scan, targets));
}
```
