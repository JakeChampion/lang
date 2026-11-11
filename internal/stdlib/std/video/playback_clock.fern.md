# Video presentation clock

The clock maps media timestamps in microseconds to monotonic nanoseconds.
Pause and resume preserve the media position.
The caller supplies monotonic time. This module reads no system clock.

`from_audio` maps an audio frame position and its monotonic timestamp onto an explicit media origin.
The origin identifies one stream frame and its media time in microseconds.
The sample rate must be the actual stream rate. Frame-to-time conversion rounds down to whole microseconds.
The function reuses the checked track-time rescaler, without an intermediate frame-count multiplication overflow.
The caller must replace the origin after a stream reset or a media discontinuity.

```fern
<<*>>=
// This module maps video timestamps to a checked monotonic playback timeline.
import "std/video/frame";
import "std/audio/mp4/time" as sample_time;
import "std/audio/pcm";

pub struct Clock { media_us: i64, monotonic_ns: i64, paused_ns: i64, paused: boolean }

pub struct AudioOrigin { frame_position: i64, media_us: i64 }

function audio_elapsed(origin: i64, position: i64, rate: i32): Result[i64, frame.VideoError] {
  if (origin < 0i64 || position < origin || rate <= 0) {
    return Err(frame.Header);
  }
  return match (sample_time.rescale(position - origin, 1000000u32, rate as u32)) {
    Ok(value) => Ok(value),
    Err(pcm.Limit) => Err(frame.Limit),
    _ => Err(frame.Header),
  };
}

pub function from_audio(origin: AudioOrigin, frame_position: i64, sample_rate: i32, monotonic_ns: i64): Result[Clock, frame.VideoError] {
  if (origin.media_us < 0i64 || monotonic_ns < 0i64) {
    return Err(frame.Header);
  }
  let elapsed = audio_elapsed(origin.frame_position, frame_position, sample_rate)?;
  let Some(media) = origin.media_us +? elapsed else {
    return Err(frame.Limit);
  };
  return start(media, monotonic_ns);
}

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
