# Bounded ISO media boxes

This module reads ISO base media box boundaries from a complete byte view.
It accepts ordinary, extended, and parent-to-end lengths.
Every child must fit within its parent. A shared budget bounds box counts.

Offsets in the byte-view API use signed 32-bit indices.
Extended lengths are checked before conversion. They cannot address bytes outside the supplied view.

```fern
<<*>>=
// Read bounded ISO base media box boundaries from a complete byte view.

import "std/audio/pcm";
import "std/audio/wire";

pub struct Box { kind: u32, start: i32, data: i32, end: i32 }

pub struct Budget { boxes: i32, maximum: i32 }

pub function tag(value: string): u32 {
  let bytes = value.as_bytes();
  if (bytes.len() != 4) {
    return 0u32;
  }
  return wire.be(bytes, 0, 4) as u32;
}

// Check all three length encodings before converting extended sizes to indices.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], at: i32, end: i32): Result[Box, pcm.AudioError] {
  if (at < 0 || end < at || end > input.len()) {
    return Err(pcm.Header);
  }
  if (end - at < 8) {
    return Err(pcm.Truncated);
  }
  let kind = wire.be(input, at + 4, 4) as u32;
  let size = wire.be(input, at, 4);
  let header = 8;
  if (size == 1u64) {
    if (end - at < 16) {
      return Err(pcm.Truncated);
    }
    size = wire.be(input, at + 8, 8);
    header = 16;
  } else if (size == 0u64) {
    size = (end - at) as u64;
  }
  if (size < header as u64) {
    return Err(pcm.Header);
  }
  if (size > (end - at) as u64) {
    return Err(pcm.Truncated);
  }
  if (kind == tag("uuid")) {
    header += 16;
  }
  if (size < header as u64) {
    return Err(pcm.Header);
  }
  return Ok(Box { kind: kind, start: at, data: at + header, end: at + size as i32 });
}

pub function children(input: [u8], start: i32, end: i32, budget: Budget): Result[(Budget, Box[]), pcm.AudioError] {
  if (start < 0 || end < start || end > input.len() || budget.boxes < 0 || budget.maximum < budget.boxes) {
    return Err(pcm.Header);
  }
  let at = start;
  let values: Box[] = [];
  while (at < end) {
    if (budget.boxes >= budget.maximum) {
      return Err(pcm.Limit);
    }
    let value = read(input, at, end)?;
    values = values.append(value);
    budget = Budget { ...budget, boxes: budget.boxes + 1 };
    at = value.end;
  }
  return Ok((budget, values));
}

pub function one(values: [Box], kind: string): Result[Option[Box], pcm.AudioError] {
  let found: Option[Box] = None;
  let code = tag(kind);
  for value in values {
    if (value.kind == code) {
      if let Some(_) = found {
        return Err(pcm.Header);
      }
      found = Some(value);
    }
  }
  return Ok(found);
}

pub function required(values: [Box], kind: string): Result[Box, pcm.AudioError] {
  let value = one(values, kind)?;
  return match (value) { Some(value) => Ok(value), None => Err(pcm.Header) };
}

pub function full(input: [u8], value: Box, minimum: i32): Result[i32, pcm.AudioError] {
  if (value.data < 0 || value.end < value.data || value.end > input.len() || minimum < 4) {
    return Err(pcm.Header);
  }
  if (value.end - value.data < minimum) {
    return Err(pcm.Truncated);
  }
  if (wire.be(input, value.data + 1, 3) != 0u64) {
    return Err(pcm.Unsupported);
  }
  return Ok(input[value.data] as i32);
}
```
