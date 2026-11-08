# ISO audio track times

This module reads movie and media clocks, track identifiers, and edit lists.
Time conversion uses integer division and checked multiplication.
Edit lists retain their media-time and movie-duration units explicitly.

Only normal-rate media edits and empty edits are supported.
Unknown-duration clock fields do not replace the packet-table duration.

```fern
<<*>>=
// Read the movie and media clocks, track identifiers and edit lists of an ISO audio track.

import "std/audio/mp4/boxes";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Clock { scale: u32, duration: i64 }

pub struct TrackId { id: u32, enabled: boolean }

pub struct Edit { duration: i64, media_time: i64 }

// Validate both clock widths and their distinct movie and media header lengths.
// fern-lint: allow cyclomatic-complexity
pub function clock(input: [u8], box: boxes.Box): Result[Clock, pcm.AudioError] {
  let version = boxes.full(input, box, 20)?;
  let at = box.data + 12;
  let width = 4;
  if (version == 1) {
    at += 8;
    width = 8;
  } else if (version != 0) {
    return Err(pcm.Unsupported);
  }
  let minimum = if (box.kind == boxes.tag("mdhd")) { 24 } else if (box.kind == boxes.tag("mvhd")) { 100 } else { return Err(pcm.Header); };
  if (box.end - box.data < minimum + (if (version == 1) { 12 } else { 0 })) {
    return Err(pcm.Truncated);
  }
  if (box.end - at < 4 + width) {
    return Err(pcm.Truncated);
  }
  let scale = wire.be(input, at, 4) as u32;
  let duration = wire.be(input, at + 4, width);
  if (scale == 0u32) {
    return Err(pcm.Header);
  }
  if (duration == (if (width == 4) { 4294967295u64 } else { 18446744073709551615u64 })) {
    duration = 0u64;
  }
  if (duration > 9223372036854775807u64) {
    return Err(pcm.Limit);
  }
  return Ok(Clock { scale: scale, duration: duration as i64 });
}

// Check both track-header versions before reading their identifier and flags.
// fern-lint: allow cyclomatic-complexity
pub function track_id(input: [u8], box: boxes.Box): Result[TrackId, pcm.AudioError] {
  if (box.data < 0 || box.end > input.len() || box.end - box.data < 20) {
    return Err(pcm.Truncated);
  }
  let version = input[box.data];
  if (box.kind != boxes.tag("tkhd")) {
    return Err(pcm.Header);
  }
  if (box.end - box.data < (if (version == 1u8) { 96 } else { 84 })) {
    return Err(pcm.Truncated);
  }
  let at = box.data + 12;
  if (version == 1u8) {
    at += 8;
  } else if (version != 0u8) {
    return Err(pcm.Unsupported);
  }
  if (box.end - at < 4) {
    return Err(pcm.Truncated);
  }
  let id = wire.be(input, at, 4) as u32;
  if (id == 0u32) {
    return Err(pcm.Header);
  }
  return Ok(TrackId { id: id, enabled: (input[box.data + 3] & 1u8) != 0u8 });
}

// Validate the optional edit hierarchy and both signed media-time encodings.
// fern-lint: allow cyclomatic-complexity
pub function edits(input: [u8], track: [boxes.Box], budget: boxes.Budget, maximum: i32): Result[(boxes.Budget, Edit[]), pcm.AudioError] {
  let Some(edts) = boxes.one(track, "edts")? else {
    return Ok((budget, []));
  };
  let (next, children) = boxes.children(input, edts.data, edts.end, budget)?;
  let elst = boxes.required(children, "elst")?;
  let version = boxes.full(input, elst, 8)?;
  let width = if (version == 0) { 4 } else if (version == 1) { 8 } else { return Err(pcm.Unsupported); };
  let count = wire.be(input, elst.data + 4, 4);
  if (maximum < 0 || count > maximum as u64) {
    return Err(pcm.Limit);
  }
  if ((elst.end - elst.data) as i64 != 8i64 + count as i64 * ((2 * width + 4) as i64)) {
    return Err(pcm.Header);
  }
  let out: Edit[] = [];
  for i in 0..count as i32 {
    let at = elst.data + 8 + i * (2 * width + 4);
    let duration = wire.be(input, at, width);
    let raw = wire.be(input, at + width, width);
    let media_time = if (width == 4) { (raw as i32) as i64 } else { raw as i64 };
    if (duration > 9223372036854775807u64) {
      return Err(pcm.Limit);
    }
    if (media_time < -1i64) {
      return Err(pcm.Header);
    }
    if (wire.be(input, at + 2 * width, 4) != 65536u64) {
      return Err(pcm.Unsupported);
    }
    out = out.append(Edit { duration: duration as i64, media_time: media_time });
  }
  return Ok((next, out));
}

pub function rescale(value: i64, to: u32, from: u32): Result[i64, pcm.AudioError] {
  if (value < 0i64 || from == 0u32 || to == 0u32) {
    return Err(pcm.Header);
  }
  let divisor = from as i64;
  let multiplier = to as i64;
  let Some(high) = value / divisor *? multiplier else {
    return Err(pcm.Limit);
  };
  let low = ((value % divisor) as u64 * (to as u64) / (from as u64)) as i64;
  let Some(total) = high +? low else {
    return Err(pcm.Limit);
  };
  return Ok(total);
}

pub function nearest(value: i64, to: u32, from: u32): Result[i64, pcm.AudioError] {
  let total = rescale(value, to, from)?;
  let remainder = (value % (from as i64)) as u64 * (to as u64) % (from as u64);
  if (remainder < (from as u64 + 1u64) / 2u64) {
    return Ok(total);
  }
  let Some(rounded) = total +? 1i64 else {
    return Err(pcm.Limit);
  };
  return Ok(rounded);
}
```
