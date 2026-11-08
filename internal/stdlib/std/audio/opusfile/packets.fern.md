# Indexed Opus packet extraction

This helper reads a nonempty packet from an indexed, immutable Ogg input view.
The index must first validate the complete container with `std/audio/ogg`.
The helper checks bounds, lacing, serials, and continuation sequences as it extracts a packet.
It does not repeat checksums or search damaged input for synchronization.

`contiguous` identifies packets that a caller can borrow directly from the input.
`read` copies one packet, including packets that cross pages or interleaved streams.
The packet byte limit applies before allocation.
The same input bytes must remain unchanged after indexing.

```fern
<<*>>=
// Extract indexed Opus packets from an already validated immutable Ogg view.

import "std/audio/pcm";
import "std/audio/wire";

pub struct Location { page_offset: i32, offset: i32, bytes: i32 }

struct Page { at: i32, body: i32, end: i32, segments: i32, serial: u32, sequence: u32, continued: boolean, eos: boolean }

function page(input: [u8], at: i32): Result[Page, pcm.AudioError] {
  if (at < 0 || at > input.len() || input.len() - at < 27) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, at, "OggS") || input[at + 4] != 0u8 || (input[at + 5] as i32 & 248) != 0) {
    return Err(pcm.Header);
  }
  let count = input[at + 26] as i32;
  if (count > input.len() - at - 27) {
    return Err(pcm.Truncated);
  }
  let body = at + 27 + count;
  let bytes = 0;
  for i in 0..count {
    bytes += input[at + 27 + i] as i32;
  }
  if (bytes > input.len() - body) {
    return Err(pcm.Truncated);
  }
  let flags = input[at + 5] as i32;
  return Ok(Page { at: at, body: body, end: body + bytes, segments: count, serial: wire.le(input, at + 14, 4) as u32, sequence: wire.le(input, at + 18, 4) as u32, continued: (flags & 1) != 0, eos: (flags & 4) != 0 });
}

function validate(span: Location, value: Page): Result[void, pcm.AudioError] {
  if (span.bytes < 1 || span.offset < value.body || span.offset >= value.end) {
    return Err(pcm.Header);
  }
  return Ok(());
}

pub function contiguous(input: [u8], span: Location): Result[boolean, pcm.AudioError] {
  let value = page(input, span.page_offset)?;
  validate(span, value)?;
  return Ok(span.bytes <= value.end - span.offset);
}

function segment(input: [u8], value: Page, offset: i32): Result[i32, pcm.AudioError] {
  let body = value.body;
  for i in 0..value.segments {
    let bytes = input[value.at + 27 + i] as i32;
    // A previous packet can have a zero-length terminal lace at this offset.
    if (body == offset && bytes > 0) {
      return Ok(i);
    }
    body += bytes;
  }
  return Err(pcm.Header);
}

function append_page(input: [u8], value: Page, first: i32, at: i32, bytes: i32, own output: u8[]): Result[(u8[], boolean), pcm.AudioError] {
  let cursor = at;
  for i in first..value.segments {
    let count = input[value.at + 27 + i] as i32;
    if (count > bytes - output.len()) {
      return Err(pcm.Header);
    }
    for j in 0..count {
      output = output.append(input[cursor + j]);
    }
    cursor += count;
    if (count < 255) {
      if (output.len() != bytes) {
        return Err(pcm.Header);
      }
      return Ok((output, true));
    }
  }
  return Ok((output, false));
}

function next_page(input: [u8], previous: Page, serial: u32): Result[Page, pcm.AudioError] {
  if (previous.eos) {
    return Err(pcm.Header);
  }
  let at = previous.end;
  while (at < input.len()) {
    let value = page(input, at)?;
    if (value.serial == serial) {
      if (value.sequence != previous.sequence + 1u32 || !value.continued) {
        return Err(pcm.Header);
      }
      return Ok(value);
    }
    at = value.end;
  }
  return Err(pcm.Truncated);
}

function first_page(input: [u8], span: Location, serial: u32, max_bytes: i32): Result[Page, pcm.AudioError] {
  if (max_bytes < 0 || span.bytes > max_bytes) {
    return Err(pcm.Limit);
  }
  let value = page(input, span.page_offset)?;
  validate(span, value)?;
  if (value.serial != serial) {
    return Err(pcm.Header);
  }
  return Ok(value);
}

pub function read(input: [u8], span: Location, serial: u32, max_bytes: i32): Result[u8[], pcm.AudioError] {
  let value = first_page(input, span, serial, max_bytes)?;
  let first = segment(input, value, span.offset)?;
  let at = span.offset;
  let output: u8[] = [];
  while (true) {
    let (next, done) = append_page(input, value, first, at, span.bytes, output)?;
    output = next;
    if (done) {
      return Ok(output);
    }
    value = next_page(input, value, serial)?;
    first = 0;
    at = value.body;
  }
}
```
