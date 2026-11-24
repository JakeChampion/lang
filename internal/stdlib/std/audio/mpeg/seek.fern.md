# MPEG Layer I and II seek ranges

The raw MPEG decoders share this frame-offset index. Their header readers supply the sample rate, channel count, byte extent and unpadded free-format extent of each frame.

```fern
<<*>>=
// Index the frame offsets of raw MPEG Layer I and II streams for seeks.
import "std/audio/pcm";
import "std/i32";

pub struct Frame { rate: i32, channels: i32, bytes: i32, free: i32 }

pub struct Index { offsets: i32[], bytes: i32, rate: i32, channels: i32, free: i32, size: i32, limits: pcm.Limits }

function valid_extent(h: Frame, remaining: i32): Result[void, pcm.AudioError] {
  if (h.bytes <= 0 || h.bytes > remaining || h.free < 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function initial_frame(input: [u8], size: i32, max_entries: i32, limits: pcm.Limits, inspect: ([u8], i32, pcm.Limits) => Result[Frame, pcm.AudioError]): Result[Frame, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (max_entries < 1) {
    return Err(pcm.Limit);
  }
  if (size != 384 && size != 1152) {
    return Err(pcm.Header);
  }
  let first = inspect(input, 0, limits)?;
  valid_extent(first, input.len())?;
  let _ = pcm.sample_count(size, first.channels, limits)?;
  return Ok(first);
}

pub function build(input: [u8], size: i32, max_entries: i32, limits: pcm.Limits, inspect: ([u8], i32, pcm.Limits) => Result[Frame, pcm.AudioError]): Result[Index, pcm.AudioError] {
  let first = initial_frame(input, size, max_entries, limits, inspect)?;
  let offsets = [0];
  let at = first.bytes;
  let free = first.free;
  while (at < input.len()) {
    if (offsets.len() >= max_entries) {
      return Err(pcm.Limit);
    }
    let h = inspect(input[at:], free, limits)?;
    valid_extent(h, input.len() - at)?;
    if (h.rate != first.rate || h.channels != first.channels) {
      return Err(pcm.Header);
    }
    offsets = offsets.append(at);
    free = h.free;
    at += h.bytes;
  }
  return Ok(Index { offsets: offsets, bytes: at, rate: first.rate, channels: first.channels, free: free, size: size, limits: limits });
}

pub function (index: Index) frames(): i64 {
  return index.offsets.len() as i64 * (index.size as i64);
}

pub struct Range { count: i32, warmup: i32, first: i32, last: i32, skip: i32 }

pub function (index: Index) range(bytes: i32, position: i64, frames: i32, capacity: i32): Result[Range, pcm.AudioError] {
  if (bytes != index.bytes || position < 0i64 || position > index.frames() || frames < 0 || frames as i64 > index.frames() - position) {
    return Err(pcm.Header);
  }
  let count = pcm.sample_count(frames, index.channels, index.limits)?;
  if (count > capacity) {
    return Err(pcm.Buffer);
  }
  let size = index.size as i64;
  let first = (position / size) as i32;
  let last = ((position + frames as i64 - 1i64) / size) as i32;
  // Fifteen prior slots of 32 samples suffice to restore the synthesis history.
  return Ok(Range { count: count, warmup: (first - (480 + index.size - 1) / index.size).max(0), first: first, last: last, skip: (position % size) as i32 * index.channels });
}

pub function (index: Index) end(n: i32): i32 {
  return if (n + 1 < index.offsets.len()) { index.offsets[n + 1] } else { index.bytes };
}

pub function (r: Range) copy(n: i32, samples: [f32], written: i32, own output: f32[]): (i32, f32[]) {
  if (n < r.first) {
    return (written, output);
  }
  let start = if (n == r.first) { r.skip } else { 0 };
  let take = (r.count - written).min(samples.len() - start);
  for i in 0..take {
    output = output.with(written + i, samples[start + i]);
  }
  return (written + take, output);
}
```
