# AV1 Annex B ranges

This module reads one length-delimited AV1 temporal unit at a time.
It checks the nested temporal-unit, frame-unit, and OBU lengths from Annex B of the AV1 specification.
Both OBU size forms are permitted. An explicit OBU size must agree with its enclosing length.
The reader reuses the native OBU header and LEB128 checks.

The first OBU of the first frame unit must be a temporal delimiter.
Other temporal delimiters return `Header`.
Frame payload syntax, complete frame coverage, temporal layers, timestamps, and native pixel decode remain outside this API.

The packet budget applies before access. Separate limits bound frame records and OBU records per temporal unit.
Each length uses at most eight bytes. The result retains scalar ranges without input bytes or payload copies.
These limits do not define a process-memory bound.

Start at offset zero and continue at `next_offset` until the result is `None`.
Temporal and frame offsets exclude their length fields. OBU offsets identify payload bytes, as in `av1.next_obu`.
The caller preserves the immutable input through later range access.

```fern
<<*>>=
// Read bounded AV1 Annex B length tables without payload copies.
import "std/video/av1";
import "std/video/frame";

pub struct Limits { video: frame.Limits, max_frame_units: i32, max_obus: i32 }

pub struct FrameUnit { offset: i32, size: i32, units: av1.Unit[] }

pub struct TemporalUnit { offset: i32, size: i32, next_offset: i32, frames: FrameUnit[] }

pub function default_limits(): Limits {
  return Limits { video: frame.default_limits(), max_frame_units: 256, max_obus: 4096 };
}

function extent(input: [u8], at: i32, end: i32): Result[(i32, i32), frame.VideoError] {
  let (start, size) = av1.leb128(input[:end], at)?;
  if (size > (end - start) as u64) {
    return Err(frame.Truncated);
  }
  return Ok((start, start + size as i32));
}

function frame_unit(input: [u8], start: i32, end: i32, first: boolean, maximum: i32): Result[FrameUnit, frame.VideoError] {
  let at = start;
  let units: av1.Unit[] = [];
  while (at < end) {
    if (units.len() >= maximum) {
      return Err(frame.Limit);
    }
    let (payload, next) = extent(input, at, end)?;
    let value = av1.delimited(input[payload:next])?;
    if (value.kind == 2 != (first && units.len() == 0)) {
      return Err(frame.Header);
    }
    units = units.append(av1.Unit { ...value, offset: payload + value.offset, next_offset: next });
    at = next;
  }
  if (first && units.len() == 0) {
    return Err(frame.Header);
  }
  return Ok(FrameUnit { offset: start, size: end - start, units: units });
}

function temporal_unit(input: [u8], start: i32, end: i32, limits: Limits): Result[TemporalUnit, frame.VideoError] {
  let at = start;
  let frames: FrameUnit[] = [];
  let count = 0;
  while (at < end) {
    if (frames.len() >= limits.max_frame_units) {
      return Err(frame.Limit);
    }
    let (payload, next) = extent(input, at, end)?;
    let value = frame_unit(input, payload, next, frames.len() == 0, limits.max_obus - count)?;
    count += value.units.len();
    frames = frames.append(value);
    at = next;
  }
  if (frames.len() == 0) {
    return Err(frame.Header);
  }
  return Ok(TemporalUnit { offset: start, size: end - start, next_offset: end, frames: frames });
}

pub function next_temporal_unit(input: [u8], offset: i32, limits: Limits): Result[Option[TemporalUnit], frame.VideoError] {
  frame.check_packet(input, limits.video)?;
  if (limits.max_frame_units < 0 || limits.max_obus < 0) {
    return Err(frame.Limit);
  }
  if (offset < 0 || offset > input.len()) {
    return Err(frame.Header);
  }
  if (offset == input.len()) {
    return Ok(None);
  }
  let (start, end) = extent(input, offset, input.len())?;
  return Ok(Some(temporal_unit(input, start, end, limits)?));
}
```
