# Bounded MP3 frame extents

Indexed bitrates determine their frame extent directly.
Free-format discovery requires compatible headers at two successive frame boundaries.
Two complete frames at physical EOF also determine an extent.
The unpadded extent remains constant, while each frame supplies its own padding bit.

The incremental probe checks at most 256 byte positions per call.
It retains a search position across short reads and requests only the required lookahead.
The caller supplies a maximum unpadded extent and the physical EOF state.
The probe does not allocate an input window or decode samples.

The complete-input helper uses the same probe.
A fresh free-format frame needs sufficient adjacent context.
An established extent permits isolated-frame decode and the final frame of a stream.

```fern
<<*>>=
// Resolve free-format frame extents with bounded, resumable lookahead.
import "std/audio/mp3/header";
import "std/audio/pcm";

pub struct Search { next: i32, first: i32, signature: i32, rate: i32, mono: boolean, padding: i32, side: i32, eof: boolean }

pub enum Probe { Found(i32), Need(Search, i32), Pending(Search) }

pub function begin(h: header.Header): Search {
  let version = if (h.version == 1) { 3 } else if (h.version == 2) { 2 } else { 0 };
  let side = header.side_bytes(h);
  let first = 4 + side + (if (h.crc) { 2 } else { 0 });
  return Search { next: first, first: first, signature: 224 | version << 3 | 2, rate: h.rate_index % 3 << 2, mono: h.channels == 1, padding: h.padding, side: side, eof: false };
}

function compatible(input: [u8], at: i32, state: Search): boolean {
  return at >= 0 && at <= input.len() - 4 && input[at] == 255 && (input[at + 1] as i32 & 254) == state.signature && (input[at + 2] as i32 & 252) == state.rate && input[at + 3] as i32 >> 6 == 3 == state.mono;
}

function progress(state: Search, next: i32): Search {
  return if (next == state.next) { state } else { Search { ...state, next: next } };
}

function ceiling(maximum: i32): i32 {
  return if (maximum > 1073741820) { 1073741820 } else { maximum };
}

function valid_search(state: Search, eof: boolean): boolean {
  return state.first >= 4 && state.next >= state.first && state.padding >= 0 && state.padding <= 1 && (!state.eof || eof);
}

function at_eof(state: Search, eof: boolean): Search {
  // Physical EOF can identify trailing metadata and shorten the audio view.
  return if (eof && !state.eof) { Search { ...state, next: state.first, eof: true } } else { state };
}

function following(input: [u8], state: Search, next: i32, eof: boolean): Option[Probe] {
  let base = next - state.padding;
  let padding = input[next + 2] as i32 >> 1 & 1;
  let bytes = base + padding;
  let minimum = 4 + state.side + (if ((input[next + 1] as i32 & 1) == 0) { 2 } else { 0 });
  if (bytes < minimum) {
    return None;
  }
  let third = next + bytes;
  if (third > input.len() - 4) {
    if (!eof) {
      return Some(Need(progress(state, next), third + 4));
    }
    if (third == input.len()) {
      return Some(Found(base));
    }
  } else if (compatible(input, third, state)) {
    return Some(Found(base));
  }
  return None;
}

pub function probe(input: [u8], state: Search, maximum: i32, eof: boolean): Result[Probe, pcm.AudioError] {
  let maximum = ceiling(maximum);
  if (maximum < 1) {
    return Err(pcm.Limit);
  }
  if (!valid_search(state, eof)) {
    return Err(pcm.Header);
  }
  let search = at_eof(state, eof);
  let next = search.next;
  for _ in 0..256 {
    if (next > maximum + state.padding) {
      return Err(pcm.Limit);
    }
    if (next > input.len() - 4) {
      return if (eof) { Err(pcm.Truncated) } else { Ok(Need(progress(search, next), next + 4)) };
    }
    if (compatible(input, next, state)) {
      if let Some(result) = following(input, search, next, eof) {
        return Ok(result);
      }
    }
    next += 1;
  }
  return Ok(Pending(progress(search, next)));
}

pub function sized(h: header.Header, base: i32, maximum: i32): Result[header.Header, pcm.AudioError] {
  if (h.bitrate != 0) {
    return Ok(h);
  }
  if (base < 1 || base > ceiling(maximum)) {
    return Err(pcm.Limit);
  }
  let bytes = base + h.padding;
  if (bytes < 4 + header.side_bytes(h) + (if (h.crc) { 2 } else { 0 })) {
    return Err(pcm.Header);
  }
  return Ok(header.Header { ...h, bytes: bytes });
}

pub function resolve(input: [u8], at: i32, h: header.Header, base: i32, maximum: i32): Result[header.Header, pcm.AudioError] {
  if (base < 0) {
    return Err(pcm.Header);
  }
  if (h.bitrate != 0 || base > 0) {
    return sized(h, base, maximum);
  }
  if (at < 0 || at > input.len() - 4) {
    return Err(pcm.Truncated);
  }
  return complete(input[at:], h, maximum);
}

function complete(input: [u8], h: header.Header, maximum: i32): Result[header.Header, pcm.AudioError] {
  let search = begin(h);
  while (true) {
    match (probe(input, search, maximum, true)?) {
      Found(value) => {
        return sized(h, value, maximum);
      },
      Pending(next) => {
        search = next;
      },
      Need(_, _) => {
        return Err(pcm.Truncated);
      }
    }
  }
}

pub function extent(h: header.Header, previous: i32): i32 {
  return if (h.bitrate == 0) { h.bytes - h.padding } else { previous };
}
```
