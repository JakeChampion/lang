# Vorbis granule positions and sample boundaries

This module maps decoded packet frames to Ogg Vorbis sample positions.
The first nonfinal audio page can discard initial samples or establish a timestamp offset.
The final audio page can discard samples from its end.

Call `page` before the completed audio packets on each page.
Call `advance` for every packet, including the first packet with zero output frames.
Counts use samples per channel at the stream sample rate.

```fern
<<*>>=
// This module maps Vorbis granules to playable sample ranges.
import "std/audio/pcm";

pub struct Timing { decoded: i64, page_end: i64, start: i64, initial: i64, seen: boolean, last_granule: i64, trim_at: Option[i64] }

pub struct Range { start: i32, end: i32, position: i64, granule: i64 }

pub function create(): Timing {
  return Timing { decoded: 0i64, page_end: 0i64, start: 0i64, initial: 0i64, seen: false, last_granule: -1i64, trim_at: None };
}

// Validate page timestamps before returning any PCM from that page.
// fern-lint: allow cyclomatic-complexity
pub function page(t: Timing, granule: i64, frames: i64, eos: boolean): Result[Timing, pcm.AudioError] {
  if (granule < 0i64 || frames < 0i64 || t.decoded != t.page_end) {
    return Err(pcm.Header);
  }
  if let Some(_) = t.trim_at {
    return Err(pcm.Header);
  }
  let Some(end) = t.decoded +? frames else {
    return Err(pcm.Limit);
  };
  let start = t.start;
  let initial = t.initial;
  if (!t.seen) {
    if (granule > frames) {
      initial = granule - frames;
    } else if (!eos) {
      start = frames - granule;
    }
  } else {
    let Some(expected) = t.last_granule +? frames else {
      return Err(pcm.Limit);
    };
    if (granule < t.last_granule || granule > expected || !eos && granule != expected) {
      return Err(pcm.Header);
    }
  }
  let trim: Option[i64] = None;
  if (eos) {
    let Some(limit) = granule - initial +? start else {
      return Err(pcm.Limit);
    };
    if (limit < t.decoded || limit > end) {
      return Err(pcm.Header);
    }
    trim = Some(limit);
  }
  return Ok(Timing { decoded: t.decoded, page_end: end, start: start, initial: initial, seen: true, last_granule: granule, trim_at: trim });
}

pub function advance(t: Timing, frames: i32): Result[(Timing, Range), pcm.AudioError] {
  if (!t.seen || frames < 0 || frames > 4096) {
    return Err(pcm.Header);
  }
  let Some(end) = t.decoded +? frames as i64 else {
    return Err(pcm.Limit);
  };
  if (end > t.page_end) {
    return Err(pcm.Header);
  }
  return Ok((Timing { ...t, decoded: end }, samples(t, end)?));
}

function samples(t: Timing, end: i64): Result[Range, pcm.AudioError] {
  let first = if (t.start > t.decoded) { t.start } else { t.decoded };
  let last = match (t.trim_at) { Some(value) => if (value < end) { value } else { end }, None => end };
  let range = Range { start: 0, end: 0, position: 0i64, granule: 0i64 };
  if (first < last) {
    let position = first - t.start;
    let Some(granule) = t.initial +? position else {
      return Err(pcm.Limit);
    };
    range = Range { start: (first - t.decoded) as i32, end: (last - t.decoded) as i32, position: position, granule: granule };
  }
  return Ok(range);
}

pub function finish(t: Timing): Result[i64, pcm.AudioError] {
  if (!t.seen || t.decoded != t.page_end) {
    return Err(pcm.Truncated);
  }
  let end = match (t.trim_at) { Some(value) => value, None => t.decoded };
  if (end < t.start) {
    return Err(pcm.Header);
  }
  return Ok(end - t.start);
}
```
