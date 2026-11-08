# Video presentation clock

The clock maps media timestamps in microseconds to monotonic nanoseconds.
Pause and resume preserve the media position.
The caller supplies monotonic time. This module reads no system clock.

```fern
<<*>>=
// This module maps video timestamps to a checked monotonic playback timeline.
import "std/video/frame";

pub struct Clock { media_us: i64, monotonic_ns: i64, paused_ns: i64, paused: boolean }

pub function start(media_us: i64, monotonic_ns: i64): Result[Clock, frame.VideoError] {
  if (media_us < 0i64 || monotonic_ns < 0i64) {
    return Err(frame.Header);
  }
  return Ok(Clock { media_us: media_us, monotonic_ns: monotonic_ns, paused_ns: 0i64, paused: false });
}

pub function (clock: Clock) deadline(media_us: i64): Result[i64, frame.VideoError] {
  if (media_us < 0i64) {
    return Err(frame.Header);
  }
  let Some(delta) = media_us -? clock.media_us else {
    return Err(frame.Limit);
  };
  let Some(ns) = delta *? 1000i64 else {
    return Err(frame.Limit);
  };
  let Some(result) = clock.monotonic_ns +? ns else {
    return Err(frame.Limit);
  };
  if (result < 0i64) {
    return Err(frame.Limit);
  }
  return Ok(result);
}

pub function (clock: Clock) position(now_ns: i64): Result[i64, frame.VideoError] {
  let instant = if (clock.paused) { clock.paused_ns } else { now_ns };
  if (instant < clock.monotonic_ns) {
    return Err(frame.Header);
  }
  let delta = (instant - clock.monotonic_ns) / 1000i64;
  let Some(result) = clock.media_us +? delta else {
    return Err(frame.Limit);
  };
  return Ok(result);
}

pub function (clock: Clock) pause(now_ns: i64): Result[Clock, frame.VideoError] {
  if (clock.paused) {
    return Ok(clock);
  }
  if (now_ns < clock.monotonic_ns) {
    return Err(frame.Header);
  }
  return Ok(Clock { ...clock, paused: true, paused_ns: now_ns });
}

pub function (clock: Clock) resume(now_ns: i64): Result[Clock, frame.VideoError] {
  if (!clock.paused) {
    return Ok(clock);
  }
  if (now_ns < clock.paused_ns) {
    return Err(frame.Header);
  }
  let Some(anchor) = clock.monotonic_ns +? (now_ns - clock.paused_ns) else {
    return Err(frame.Limit);
  };
  return Ok(Clock { ...clock, monotonic_ns: anchor, paused: false });
}
```
