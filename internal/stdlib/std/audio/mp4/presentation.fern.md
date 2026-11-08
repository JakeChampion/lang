# ISO audio presentation spans

This module maps packet clocks and normal-rate edit lists onto bounded PCM output spans.
It inserts silence for empty edits and removes media outside each declared edit.
Codec modules supply the sample rate, channel count, and physical decoded extent.

```fern
<<*>>=
import "std/audio/mp4/tables";
import "std/audio/mp4/time";
import "std/audio/pcm";

pub struct Span { start: i64, end: i64, output: i32 }

pub struct Plan { frames: i32, spans: Span[] }

function add_span(own plan: Plan, start: i64, end: i64, channels: i32, limits: pcm.Limits): Result[Plan, pcm.AudioError] {
  if (start < 0i64 || end < start) {
    return Err(pcm.Header);
  }
  let length = end - start;
  let maximum = limits.max_samples / channels;
  if (length > (maximum - plan.frames) as i64) {
    return Err(pcm.Limit);
  }
  let span = Span { start: start, end: end, output: plan.frames * channels };
  return Ok(Plan { frames: plan.frames + length as i32, spans: plan.spans.append(span) });
}

// Resolve media edits and empty edits before allocating presentation PCM.
// fern-lint: allow cyclomatic-complexity
pub function create(packets: [tables.Packet], edits: [time.Edit], clock: time.Clock, movie: time.Clock, rate: i32, channels: i32, decoded_frames: i64, limits: pcm.Limits): Result[Plan, pcm.AudioError] {
  pcm.check_input([], limits)?;
  if (packets.len() == 0 || channels < 1 || channels > 8 || rate < 1 || rate > 16777215 || decoded_frames < 0i64) {
    return Err(pcm.Header);
  }
  let last = packets[packets.len() - 1];
  let media_frames = time.nearest(last.position + last.duration as i64, rate as u32, clock.scale)?;
  let plan = Plan { frames: 0, spans: [] };
  if (edits.len() == 0) {
    if (media_frames > decoded_frames) {
      return Err(pcm.Header);
    }
    return add_span(plan, 0i64, media_frames, channels, limits);
  }
  let elapsed = 0i64;
  for edit in edits {
    let before = time.nearest(elapsed, rate as u32, movie.scale)?;
    if (edit.duration < 0i64) {
      return Err(pcm.Header);
    }
    let Some(next_time) = elapsed +? edit.duration else {
      return Err(pcm.Limit);
    };
    elapsed = next_time;
    let after = time.nearest(elapsed, rate as u32, movie.scale)?;
    let frames = after - before;
    if (edit.media_time == -1i64) {
      if (frames > (limits.max_samples / channels - plan.frames) as i64) {
        return Err(pcm.Limit);
      }
      plan = Plan { ...plan, frames: plan.frames + frames as i32 };
    } else {
      let start = time.nearest(edit.media_time, rate as u32, clock.scale)?;
      let Some(stop) = start +? frames else {
        return Err(pcm.Limit);
      };
      let end = if (stop > media_frames) { media_frames } else { stop };
      if (end > decoded_frames) {
        return Err(pcm.Header);
      }
      plan = add_span(plan, start, end, channels, limits)?;
    }
  }
  return Ok(plan);
}
```
