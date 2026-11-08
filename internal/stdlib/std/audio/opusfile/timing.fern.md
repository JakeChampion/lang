# Opus granule positions and sample boundaries

This module applies the Ogg Opus timing rules from RFC 7845, section 4.
All counts use 48 kHz samples per channel.
An initial granule offset changes timestamps. It does not change the number of pre-skip samples.
The final page can discard samples from the decoded output.

Call `page` before the first packet on each page with completed audio packets.
Call `advance` once for each decoded packet on that page.
The returned range selects samples from that packet.
`Range.position` counts playable frames from the beginning of the selected logical stream.
`Range.granule` retains the absolute granule timestamp.

```fern
<<*>>=
// RFC 7845 granule offsets, pre-skip, and final sample boundaries.

import "std/audio/pcm";

pub struct Timing {
  pre_skip: i64,
  decoded: i64,
  initial: Option[i64],
  last_granule: i64,
  trim_at: Option[i64],
  page_end: i64,
}

pub struct Range { start: i32, end: i32, position: i64, granule: i64 }

pub function create(pre_skip: i32): Result[Timing, pcm.AudioError] {
  if (pre_skip < 0 || pre_skip > 65535) {
    return Err(pcm.Header);
  }
  return Ok(Timing { pre_skip: pre_skip as i64, decoded: 0i64, initial: None, last_granule: -1i64, trim_at: None, page_end: 0i64 });
}

function first(t: Timing, granule: i64, frames: i64, eos: boolean): Result[i64, pcm.AudioError] {
  if (eos) {
    if (granule < t.pre_skip) {
      return Err(pcm.Header);
    }
  } else if (granule < frames) {
    return Err(pcm.Header);
  }
  return Ok(if (granule > frames) { granule - frames } else { 0i64 });
}

function subsequent(t: Timing, granule: i64, frames: i64, eos: boolean): Result[void, pcm.AudioError] {
  let Some(expected) = t.last_granule +? frames else {
    return Err(pcm.Limit);
  };
  if (granule < t.last_granule || granule > expected || !eos && granule != expected) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function page_initial(t: Timing, granule: i64, frames: i64, eos: boolean): Result[i64, pcm.AudioError] {
  return match (t.initial) {
    None => first(t, granule, frames, eos),
    Some(value) => {
      subsequent(t, granule, frames, eos)?;
      Ok(value)
    },
  };
}

pub function page(t: Timing, granule: i64, frames: i64, eos: boolean): Result[Timing, pcm.AudioError] {
  if (granule < 0i64 || frames <= 0i64 || t.decoded != t.page_end) {
    return Err(pcm.Header);
  }
  if let Some(_) = t.trim_at {
    return Err(pcm.Header);
  }
  let initial = page_initial(t, granule, frames, eos)?;
  let Some(end) = t.decoded +? frames else {
    return Err(pcm.Limit);
  };
  let trim = ending(t, granule, initial, eos)?;
  return Ok(Timing { ...t, initial: Some(initial), last_granule: granule, trim_at: trim, page_end: end });
}

function ending(t: Timing, granule: i64, initial: i64, eos: boolean): Result[Option[i64], pcm.AudioError] {
  if (eos) {
    let kept = granule - initial;
    if (kept < t.pre_skip) {
      return Err(pcm.Header);
    }
    return Ok(Some(kept));
  }
  return Ok(None);
}

pub function advance(t: Timing, frames: i32): Result[(Timing, Range), pcm.AudioError] {
  let Some(initial) = t.initial else {
    return Err(pcm.Header);
  };
  if (frames <= 0 || frames > 5760) {
    return Err(pcm.Header);
  }
  let Some(end) = t.decoded +? frames as i64 else {
    return Err(pcm.Limit);
  };
  if (end > t.page_end) {
    return Err(pcm.Header);
  }
  return Ok((Timing { ...t, decoded: end }, samples(t, initial, end)?));
}

function samples(t: Timing, initial: i64, end: i64): Result[Range, pcm.AudioError] {
  let start = if (t.decoded < t.pre_skip) { t.pre_skip } else { t.decoded };
  let limit = match (t.trim_at) { Some(value) => if (value < end) { value } else { end }, None => end };
  let range = Range { start: 0, end: 0, position: 0i64, granule: 0i64 };
  if (start < limit) {
    let Some(granule) = initial +? start else {
      return Err(pcm.Limit);
    };
    range = Range { start: (start - t.decoded) as i32, end: (limit - t.decoded) as i32, position: start - t.pre_skip, granule: granule };
  }
  return Ok(range);
}

pub function finish(t: Timing): Result[i64, pcm.AudioError] {
  if (t.decoded != t.page_end) {
    return Err(pcm.Truncated);
  }
  let end = match (t.trim_at) { Some(value) => value, None => t.decoded };
  if (end < t.pre_skip) {
    return Err(pcm.Header);
  }
  return Ok(end - t.pre_skip);
}
```
