# Android audio streams

This module captures and plays 16-bit audio through AAudio: speech and sounds in mono, and music in mono or stereo.

## Model

A `Stream` is one AAudio stream: a capture stream from the microphone, or a playback stream to the loudspeaker.

A capture stream uses the voice-communication input preset. This preset turns on the noise suppressor and the gain control of the device. A playback stream from `playback` carries speech with the media usage, so it plays on the loudspeaker at the media volume.

`playback_for` gives a stream with another usage: `USAGE_ALARM` plays at the alarm volume, also when the phone is silent, and `USAGE_NOTIFICATION` at the notification volume. These two streams carry sounds, not speech. The voice-communication usage plays on the earpiece. The earpiece is too quiet when the phone is not at the ear.

Each stream uses the full capacity of its buffer and not the low-latency mode. In the low-latency mode, a buffer can hold less than 10 ms. Then one frame of speech does not fit in it.

`read` and `write` block for at most their timeout. A sample is an `i32` from -32768 to 32767. `write` gives the number of samples that it wrote. When the buffer is full before the timeout, that number is less than the length, and the caller must write the other samples later.

The module needs API level 28, for the usage, the content type and the input preset. A capture stream needs the RECORD_AUDIO permission.

`music` gives a `Music` stream: a playback stream with the music content type and one or two channels. The samples of a stereo frame are one after the other, left then right. `pause` stops the sound and keeps the samples that the stream holds. `flush` drops them, so a player that moves to another time in a track does not play the old samples.

## Contracts

A stream with `ptr` 0 did not open. Close each other stream once.

`timestamp` returns a frame position and its `CLOCK_MONOTONIC` time in nanoseconds.
Frame zero is the first frame played or captured by the stream.
The stream's actual sample rate supplies the units for later frame-to-media-time conversion.
The query does not start, stop, read, or write the stream.

The platform supplies timestamps only while the stream is started, after an initial delay.
An unavailable timestamp or invalid platform result returns `None`.
Retry after an asynchronous start. Keep the stream open throughout the call.
This query allocates memory. Call it outside a real-time audio callback.

The video controller can use this anchor through `std/android/video/player.synchronize_audio`.
The app supplies the matching media and frame origin and controls both streams.
The timestamp query does not assign media timestamps or measure acoustic lip-sync error.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<streams>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module captures and plays 16-bit audio through AAudio.
import "std/ffi";
import "std/i32";
```

### `C functions`

```fern
<<C functions>>=
@import("libaaudio.so", "AAudio_createStreamBuilder")
function c_create_builder(out_builder: usize): i32;

@import("libaaudio.so", "AAudioStreamBuilder_setDirection")
function c_set_direction(builder: usize, direction: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setSampleRate")
function c_set_sample_rate(builder: usize, rate: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setChannelCount")
function c_set_channel_count(builder: usize, count: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setFormat")
function c_set_format(builder: usize, format: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setPerformanceMode")
function c_set_performance_mode(builder: usize, mode: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setBufferCapacityInFrames")
function c_set_buffer_capacity(builder: usize, frames: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setUsage")
function c_set_usage(builder: usize, usage: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setContentType")
function c_set_content_type(builder: usize, content: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_setInputPreset")
function c_set_input_preset(builder: usize, preset: i32): void;

@import("libaaudio.so", "AAudioStreamBuilder_openStream")
function c_open_stream(builder: usize, out_stream: usize): i32;

@import("libaaudio.so", "AAudioStreamBuilder_delete")
function c_delete_builder(builder: usize): i32;

@import("libaaudio.so", "AAudioStream_requestStart")
function c_request_start(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_requestPause")
function c_request_pause(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_requestFlush")
function c_request_flush(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_waitForStateChange")
function c_wait_for_state_change(stream: usize, state: i32, out_next: usize, timeout_ns: i64): i32;

@import("libaaudio.so", "AAudioStream_requestStop")
function c_request_stop(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_close")
function c_close(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_read")
function c_read(stream: usize, buffer: usize, frames: i32, timeout_ns: i64): i32;

@import("libaaudio.so", "AAudioStream_write")
function c_write(stream: usize, buffer: usize, frames: i32, timeout_ns: i64): i32;

@import("libaaudio.so", "AAudioStream_getSampleRate")
function c_sample_rate(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_getBufferCapacityInFrames")
function c_buffer_capacity(stream: usize): i32;

@import("libaaudio.so", "AAudioStream_setBufferSizeInFrames")
function c_set_buffer_size(stream: usize, frames: i32): i32;

@import("libaaudio.so", "AAudioStream_getTimestamp")
function c_timestamp(stream: usize, clock: i32, position: usize, time_ns: usize): i32;
```

### `streams`

The AAudio constants: the direction is 0 for output and 1 for input, and the format 1 is 16-bit PCM. The performance mode 10 is none. The usage 1 is media, 4 is alarm and 5 is notification. The content type 1 is speech and 4 is sonification, and the input preset 7 is voice communication.

```fern
<<streams>>=
pub struct Stream { ptr: usize, rate: i32 }

@derive(ffi.CLayout)
pub struct Timestamp { frame_position: i64, monotonic_ns: i64 }

pub const USAGE_MEDIA: i32 = 1;

pub const USAGE_ALARM: i32 = 4;

pub const USAGE_NOTIFICATION: i32 = 5;

function open(input: boolean, rate: i32, usage: i32): Stream {
  let content = 4;
  if (usage == USAGE_MEDIA) {
    content = 1;
  }
  let ptr = open_stream(input, rate, usage, 1, content, 0);
  if (ptr == 0 as usize) {
    return Stream { ptr: ptr, rate: 0 };
  }
  return Stream { ptr: ptr, rate: c_sample_rate(ptr) };
}

// A stream of `channels` channels with the content type `content`, or 0
// when the device opens none. Its buffer holds `capacity` frames or more,
// or the frames that the device chooses for 0.
function open_stream(input: boolean, rate: i32, usage: i32, channels: i32, content: i32, capacity: i32): usize {
  unsafe {
    let slot: usize = __alloc(8);
    __store_ptr(slot, 0 as usize);
    if (c_create_builder(slot) != 0) {
      __free(slot, 8);
      return 0 as usize;
    }
    let builder: usize = __load_ptr(slot);
    let direction = 0;
    if (input) {
      direction = 1;
    }
    c_set_direction(builder, direction);
    c_set_sample_rate(builder, rate);
    c_set_channel_count(builder, channels);
    c_set_format(builder, 1);
    c_set_performance_mode(builder, 10);
    if (capacity > 0) {
      c_set_buffer_capacity(builder, capacity);
    }
    c_set_content_type(builder, content);
    if (input) {
      c_set_input_preset(builder, 7);
    } else {
      c_set_usage(builder, usage);
    }
    __store_ptr(slot, 0 as usize);
    let opened: i32 = c_open_stream(builder, slot);
    let stream: usize = __load_ptr(slot);
    let deleted: i32 = c_delete_builder(builder);
    __free(slot, 8);
    if (opened != 0) {
      return 0 as usize;
    }
    let sized: i32 = c_set_buffer_size(stream, c_buffer_capacity(stream));
    return stream;
  }
}

// A capture stream from the microphone at `rate` samples a second. The
// device can choose another rate, which `rate` of the stream gives.
pub function capture(rate: i32): Stream {
  return open(true, rate, USAGE_MEDIA);
}

// A playback stream of speech at `rate` samples a second.
pub function playback(rate: i32): Stream {
  return open(false, rate, USAGE_MEDIA);
}

// A playback stream of sounds at `rate` samples a second, with `usage`:
// USAGE_ALARM or USAGE_NOTIFICATION.
pub function playback_for(rate: i32, usage: i32): Stream {
  return open(false, rate, usage);
}

pub function (s: Stream) start(): boolean {
  return s.ptr != 0 as usize && c_request_start(s.ptr) == 0;
}

pub function (s: Stream) stop(): void {
  if (s.ptr != 0 as usize) {
    let stopped: i32 = c_request_stop(s.ptr);
  }
}

pub function (s: Stream) close(): void {
  if (s.ptr != 0 as usize) {
    let closed: i32 = c_close(s.ptr);
  }
}

// Query the platform audio anchor in CLOCK_MONOTONIC units.
pub function (s: Stream) timestamp(): Option[Timestamp] {
  if (s.ptr == 0 as usize) {
    return None;
  }
  unsafe {
    let bytes = Timestamp.c_size() as i32;
    let slot = __alloc(bytes);
    Timestamp { frame_position: -1i64, monotonic_ns: -1i64 }.c_store(slot);
    let status = c_timestamp(s.ptr, 1, slot + Timestamp.c_offset_frame_position(), slot + Timestamp.c_offset_monotonic_ns());
    let value = Timestamp.c_load(slot);
    __free(slot, bytes);
    if (status != 0 || value.frame_position < 0i64 || value.monotonic_ns < 0i64) {
      return None;
    }
    return Some(value);
  }
}

// Up to `n` samples from a capture stream, waiting at most `timeout_ms`.
pub function (s: Stream) read(n: i32, timeout_ms: i32): i32[] {
  let out: i32[] = [];
  if (s.ptr == 0 as usize || n <= 0) {
    return out;
  }
  unsafe {
    let buf: usize = __alloc(2 * n);
    let got: i32 = c_read(s.ptr, buf, n, timeout_ms as i64 * 1000000i64);
    for k in 0..got {
      let lo = __load_u8(buf + (2 * k) as usize) as i32;
      let hi = __load_u8(buf + (2 * k + 1) as usize) as i32;
      let v: i32 = hi << 8 | lo;
      if (v >= 32768) {
        v -= 65536;
      }
      out = out.append(v);
    }
    __free(buf, 2 * n);
  }
  return out;
}

// Writes `samples` to a playback stream, waiting at most `timeout_ms`, and
// gives the number of samples written. A sample out of range is clipped.
pub function (s: Stream) write(samples: [i32], timeout_ms: i32): i32 {
  return write_frames(s.ptr, samples, samples.len(), timeout_ms);
}

// Writes the first `frames` frames of `samples` to the stream `ptr`, and
// gives the number of frames written.
function write_frames(ptr: usize, samples: [i32], frames: i32, timeout_ms: i32): i32 {
  let n: i32 = samples.len();
  if (ptr == 0 as usize || n == 0 || frames <= 0) {
    return 0;
  }
  unsafe {
    let buf: usize = __alloc(2 * n);
    for k in 0..n {
      let v: i32 = samples[k];
      if (v > 32767) {
        v = 32767;
      } else if (v < -32768) {
        v = -32768;
      }
      __store_u8(buf + (2 * k) as usize, v & 255);
      __store_u8(buf + (2 * k + 1) as usize, v >> 8 & 255);
    }
    let wrote: i32 = c_write(ptr, buf, frames, timeout_ms as i64 * 1000000i64);
    __free(buf, 2 * n);
    return wrote.max(0);
  }
}

// A playback stream of music. `channels` is 1 or 2, and the samples of a
// frame are one after the other.
pub struct Music { ptr: usize, rate: i32, channels: i32 }

// A playback stream of music at `rate` frames a second with `channels`
// channels: 1 or 2. The device can choose another rate, which `rate` of the
// stream gives. A stream with `ptr` 0 did not open. Its buffer holds a
// quarter of a second, so the sound goes on while the app is late.
pub function music(rate: i32, channels: i32): Music {
  let count = channels.max(1).min(2);
  let ptr = open_stream(false, rate, USAGE_MEDIA, count, 2, rate / 4);
  if (ptr == 0 as usize) {
    return Music { ptr: ptr, rate: 0, channels: count };
  }
  return Music { ptr: ptr, rate: c_sample_rate(ptr), channels: count };
}

pub function (m: Music) start(): boolean {
  return m.ptr != 0 as usize && c_request_start(m.ptr) == 0;
}

// Stops the sound. The stream keeps the samples that it holds, and `start`
// plays them.
pub function (m: Music) pause(): void {
  if (m.ptr != 0 as usize) {
    let paused: i32 = c_request_pause(m.ptr);
  }
}

// AAUDIO_STREAM_STATE_PAUSING and AAUDIO_STREAM_STATE_FLUSHING.
const PAUSING: i32 = 5;

const FLUSHING: i32 = 7;

// Waits at most 200 ms while the stream `ptr` is in `state`.
function settled(ptr: usize, state: i32): void {
  unsafe {
    let slot: usize = __alloc(4);
    let waited: i32 = c_wait_for_state_change(ptr, state, slot, 200000000i64);
    __free(slot, 4);
  }
}

// Drops the samples that the stream holds. Call it after `pause`. A stream
// drops its samples only after it stopped, so `flush` waits for that, and
// then for the samples to go.
pub function (m: Music) flush(): void {
  if (m.ptr != 0 as usize) {
    settled(m.ptr, PAUSING);
    let flushed: i32 = c_request_flush(m.ptr);
    settled(m.ptr, FLUSHING);
  }
}

pub function (m: Music) close(): void {
  if (m.ptr != 0 as usize) {
    let closed: i32 = c_close(m.ptr);
  }
}

// Writes `samples` to the stream, waiting at most `timeout_ms`, and gives
// the number of samples written: a whole number of frames. A sample out of
// range is clipped.
pub function (m: Music) write(samples: [i32], timeout_ms: i32): i32 {
  return write_frames(m.ptr, samples, samples.len() / m.channels, timeout_ms) * m.channels;
}
```
