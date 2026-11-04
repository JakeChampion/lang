# Android audio streams

This module captures and plays mono 16-bit audio through AAudio.

## Model

A `Stream` is one AAudio stream: a capture stream from the microphone, or a playback stream to the speaker or the earpiece. Both streams are set up for speech: the low-latency mode, the voice-communication usage and the voice-communication input preset, which turns on the echo canceller of the device where it has one.

`read` and `write` block for at most their timeout. A sample is an `i32` from -32768 to 32767.

The module needs API level 28, for the usage, the content type and the input preset. A capture stream needs the RECORD_AUDIO permission.

## Contracts

A stream with `ptr` 0 did not open. Each other stream must be closed once.

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
// This module captures and plays mono 16-bit audio through AAudio.
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
```

### `streams`

The AAudio constants: the direction is 0 for output and 1 for input, the format 1 is 16-bit PCM, the performance mode 12 is low latency, the usage 2 is voice communication, the content type 1 is speech, and the input preset 7 is voice communication.

```fern
<<streams>>=
pub struct Stream { ptr: usize, rate: i32 }

function open(input: boolean, rate: i32): Stream {
  unsafe {
    let slot: usize = __alloc(8);
    __store_ptr(slot, 0 as usize);
    if (c_create_builder(slot) != 0) {
      __free(slot, 8);
      return Stream { ptr: 0 as usize, rate: 0 };
    }
    let builder: usize = __load_ptr(slot);
    let direction = 0;
    if (input) {
      direction = 1;
    }
    c_set_direction(builder, direction);
    c_set_sample_rate(builder, rate);
    c_set_channel_count(builder, 1);
    c_set_format(builder, 1);
    c_set_performance_mode(builder, 12);
    c_set_usage(builder, 2);
    c_set_content_type(builder, 1);
    if (input) {
      c_set_input_preset(builder, 7);
    }
    __store_ptr(slot, 0 as usize);
    let opened: i32 = c_open_stream(builder, slot);
    let stream: usize = __load_ptr(slot);
    let deleted: i32 = c_delete_builder(builder);
    __free(slot, 8);
    if (opened != 0) {
      return Stream { ptr: 0 as usize, rate: 0 };
    }
    return Stream { ptr: stream, rate: c_sample_rate(stream) };
  }
}

// A capture stream from the microphone at `rate` samples a second. The
// device can choose another rate, which `rate` of the stream gives.
pub function capture(rate: i32): Stream {
  return open(true, rate);
}

// A playback stream at `rate` samples a second.
pub function playback(rate: i32): Stream {
  return open(false, rate);
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
  let n: i32 = samples.len();
  if (s.ptr == 0 as usize || n == 0) {
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
    let wrote: i32 = c_write(s.ptr, buf, n, timeout_ms as i64 * 1000000i64);
    __free(buf, 2 * n);
    return wrote;
  }
}
```
