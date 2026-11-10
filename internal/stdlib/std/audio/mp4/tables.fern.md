# ISO audio sample tables

This module validates packet sizes, chunk mappings, offsets, and decoding times.
It supports `stsz`, `stz2`, `stco`, and `co64` tables.
Table lengths must agree with their declared entry counts.

Packet ranges must lie inside a declared media-data payload.
An explicit packet limit bounds arrays before allocation.
More than 16 ordered, disjoint media ranges use one validation pass and a binary search for each packet.
The complete-view and seekable-file indexers supply these ordered ranges.
Direct callers can supply other range orders or overlaps. Those inputs retain a linear search.
The search does not copy ranges or require packet offsets in ascending order.

```fern
<<*>>=
// Validate the sample size, chunk, offset and decoding time tables of an ISO audio track.

import "std/audio/mp4/boxes";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Run { first: i32, count: i32, description: i32 }

pub struct Time { count: i32, duration: u32 }

pub struct Packet { offset: i64, bytes: i32, description: i32, position: i64, duration: u32 }

pub struct Media { start: i64, end: i64 }

function count(input: [u8], box: boxes.Box, at: i32, maximum: i32): Result[i32, pcm.AudioError] {
  if (at < box.data || at > box.end || box.end - at < 4 || maximum < 0) {
    return Err(pcm.Header);
  }
  let count = wire.be(input, at, 4);
  if (count > maximum as u64) {
    return Err(pcm.Limit);
  }
  return Ok(count as i32);
}

function extent(box: boxes.Box, header: i32, bytes: i64): Result[void, pcm.AudioError] {
  if ((box.end - box.data) as i64 != header as i64 + bytes) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// Check fixed sizes and all three compact widths before expanding packet sizes.
// fern-lint: allow cyclomatic-complexity
pub function sizes(input: [u8], box: boxes.Box, maximum: i32): Result[i32[], pcm.AudioError] {
  if (boxes.full(input, box, 12)? != 0) {
    return Err(pcm.Unsupported);
  }
  let length = count(input, box, box.data + 8, maximum)?;
  let out: i32[] = [];
  if (box.kind == boxes.tag("stsz")) {
    let fixed = wire.be(input, box.data + 4, 4);
    extent(box, 12, if (fixed == 0u64) { 4i64 * (length as i64) } else { 0i64 })?;
    for i in 0..length {
      let size = if (fixed == 0u64) { wire.be(input, box.data + 12 + 4 * i, 4) } else { fixed };
      if (size == 0u64 || size > 2147483647u64) {
        return Err(pcm.Header);
      }
      out = out.append(size as i32);
    }
    return Ok(out);
  }
  if (box.kind != boxes.tag("stz2") || wire.be(input, box.data + 4, 3) != 0u64) {
    return Err(pcm.Header);
  }
  let width = input[box.data + 7] as i32;
  if (width != 4 && width != 8 && width != 16) {
    return Err(pcm.Header);
  }
  extent(box, 12, (length as i64 * (width as i64) + 7i64) / 8i64)?;
  for i in 0..length {
    let at = box.data + 12 + (if (width == 4) { i / 2 } else { i * (width / 8) });
    let value = if (width == 4) { input[at] as i32 >> (if (i % 2 == 0) { 4 } else { 0 }) & 15 } else { wire.be(input, at, width / 8) as i32 };
    if (value == 0) {
      return Err(pcm.Header);
    }
    out = out.append(value);
  }
  return Ok(out);
}

pub function offsets(input: [u8], box: boxes.Box, maximum: i32, file_bytes: i64): Result[i64[], pcm.AudioError] {
  if (boxes.full(input, box, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  let width = if (box.kind == boxes.tag("co64")) { 8 } else if (box.kind == boxes.tag("stco")) { 4 } else { return Err(pcm.Header); };
  let length = count(input, box, box.data + 4, maximum)?;
  extent(box, 8, length as i64 * (width as i64))?;
  if (file_bytes < 0i64) {
    return Err(pcm.Header);
  }
  let out: i64[] = [];
  for i in 0..length {
    let value = wire.be(input, box.data + 8 + i * width, width);
    if (value > file_bytes as u64) {
      return Err(pcm.Truncated);
    }
    out = out.append(value as i64);
  }
  return Ok(out);
}

// Validate each ordered chunk run before narrowing its three unsigned fields.
// fern-lint: allow cyclomatic-complexity
pub function chunks(input: [u8], box: boxes.Box, maximum: i32): Result[Run[], pcm.AudioError] {
  if (boxes.full(input, box, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  let length = count(input, box, box.data + 4, maximum)?;
  extent(box, 8, 12i64 * (length as i64))?;
  let out: Run[] = [];
  let previous = 0;
  for i in 0..length {
    let at = box.data + 8 + i * 12;
    let first = wire.be(input, at, 4);
    let size = wire.be(input, at + 4, 4);
    let description = wire.be(input, at + 8, 4);
    if (first > maximum as u64 || size > maximum as u64 || description > maximum as u64) {
      return Err(pcm.Limit);
    }
    if (first <= previous as u64 || size == 0u64 || description == 0u64 || i == 0 && first != 1u64) {
      return Err(pcm.Header);
    }
    out = out.append(Run { first: first as i32, count: size as i32, description: description as i32 - 1 });
    previous = first as i32;
  }
  return Ok(out);
}

pub function times(input: [u8], box: boxes.Box, maximum: i32): Result[Time[], pcm.AudioError] {
  if (boxes.full(input, box, 8)? != 0) {
    return Err(pcm.Unsupported);
  }
  let length = count(input, box, box.data + 4, maximum)?;
  extent(box, 8, 8i64 * (length as i64))?;
  let out: Time[] = [];
  let total = 0i64;
  for i in 0..length {
    let at = box.data + 8 + i * 8;
    let n = wire.be(input, at, 4);
    let duration = wire.be(input, at + 4, 4) as u32;
    total += n as i64;
    if (total > maximum as i64) {
      return Err(pcm.Limit);
    }
    if (n == 0u64 || duration == 0u32) {
      return Err(pcm.Header);
    }
    out = out.append(Time { count: n as i32, duration: duration });
  }
  return Ok(out);
}

function ordered_media(media: [Media]): boolean {
  let end = 0i64;
  for box in media {
    if (box.start < end || box.end < box.start) {
      return false;
    }
    end = box.end;
  }
  return true;
}

function ordered_contains(media: [Media], at: i64, bytes: i32): boolean {
  let low = 0;
  let high = media.len();
  while (low < high) {
    let middle = low + (high - low) / 2;
    if (media[middle].start <= at) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }
  if (low == 0) {
    return false;
  }
  let box = media[low - 1];
  return at <= box.end && bytes as i64 <= box.end - at;
}

function contained(media: [Media], ordered: boolean, at: i64, bytes: i32): boolean {
  if (at < 0i64 || bytes < 1) {
    return false;
  }
  if (ordered) {
    return ordered_contains(media, at, bytes);
  }
  for box in media {
    if (at >= box.start && at <= box.end && bytes as i64 <= box.end - at) {
      return true;
    }
  }
  return false;
}

// Advance both run tables while checking every packet against its media payload.
// fern-lint: allow cyclomatic-complexity
pub function packets(sizes: [i32], offsets: [i64], chunks: [Run], times: [Time], media: [Media], descriptions: i32): Result[Packet[], pcm.AudioError] {
  if (sizes.len() == 0 || offsets.len() == 0 || chunks.len() == 0 || times.len() == 0 || chunks[chunks.len() - 1].first > offsets.len()) {
    return Err(pcm.Header);
  }
  let out: Packet[] = [];
  let run = 0;
  let time = 0;
  let remaining = times[0].count;
  let position = 0i64;
  let ordered = media.len() > 16 && ordered_media(media);
  for chunk in 0..offsets.len() {
    if (run + 1 < chunks.len() && chunks[run + 1].first == chunk + 1) {
      run += 1;
    }
    let entry = chunks[run];
    if (entry.description >= descriptions || entry.count > sizes.len() - out.len()) {
      return Err(pcm.Header);
    }
    let at = offsets[chunk];
    for _ in 0..entry.count {
      if (remaining == 0) {
        time += 1;
        if (time >= times.len()) {
          return Err(pcm.Header);
        }
        remaining = times[time].count;
      }
      let size = sizes[out.len()];
      if (!contained(media, ordered, at, size)) {
        return Err(pcm.Header);
      }
      let duration = times[time].duration;
      out = out.append(Packet { offset: at, bytes: size, description: entry.description, position: position, duration: duration });
      at += size as i64;
      position += duration as i64;
      remaining -= 1;
    }
  }
  if (out.len() != sizes.len() || remaining != 0 || time != times.len() - 1) {
    return Err(pcm.Header);
  }
  return Ok(out);
}
```
