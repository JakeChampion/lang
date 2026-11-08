# Vorbis inverse transforms and overlap

This module supports every standard Vorbis block size, from 64 through 8,192 samples.
It uses reusable FFT and transform scratch with fixed generated coefficients.
The overlap stage handles equal block sizes and both size transitions.

Output retains unclipped float PCM. Non-finite transform or output values return `Sample`.
The first packet primes overlap storage and produces no PCM.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis DSP and inverse-MDCT modules.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/tables";
import "std/audio/pcm" as audio;
import "std/f64";

pub struct Scratch { fft: f32[], time: f32[] }

pub struct Work { scratch: Scratch, delay: f32[], pcm: f32[] }

pub function zeros(count: i32): f32[] {
  let out: f32[] = [];
  for _ in 0..count {
    out = out.append(0.0f32);
  }
  return out;
}

pub function create(exponent: i32): Result[Scratch, audio.AudioError] {
  if (exponent < 6 || exponent > 13) {
    return Err(audio.Header);
  }
  let n = 1 << exponent;
  return Ok(Scratch { fft: zeros(n / 2), time: zeros(n) });
}

pub function frames(current: i32, previous: i32): Result[i32, audio.AudioError] {
  if (current < 6 || current > 13) {
    return Err(audio.Header);
  }
  if (previous == 0) {
    return Ok(0);
  }
  if (previous < 6 || previous > 13) {
    return Err(audio.Header);
  }
  return Ok(((1 << current) + (1 << previous)) / 4);
}

function reorder(own values: f32[], n: i32): f32[] {
  let reverse = 0;
  for i in 1..n {
    let bit = n >> 1;
    while ((reverse & bit) != 0) {
      reverse ^= bit;
      bit >>= 1;
    }
    reverse ^= bit;
    if (i < reverse) {
      let re = values[2 * i];
      let im = values[2 * i + 1];
      values = values.with(2 * i, values[2 * reverse]).with(2 * i + 1, values[2 * reverse + 1]);
      values = values.with(2 * reverse, re).with(2 * reverse + 1, im);
    }
  }
  return values;
}

function butterfly(own values: f32[], left: i32, right: i32, twiddle: i32): f32[] {
  let wr = tables.FFT[2 * twiddle];
  let wi = tables.FFT[2 * twiddle + 1];
  let re = values[right] * wr - values[right + 1] * wi;
  let im = values[right] * wi + values[right + 1] * wr;
  let ar = values[left];
  let ai = values[left + 1];
  return values.with(left, ar + re).with(left + 1, ai + im).with(right, ar - re).with(right + 1, ai - im);
}

function fft(own values: f32[], n: i32): f32[] {
  values = reorder(values, n);
  let width = 2;
  while (width <= n) {
    let half = width / 2;
    let stride = 2048 / width;
    let base = 0;
    while (base < n) {
      for i in 0..half {
        values = butterfly(values, 2 * (base + i), 2 * (base + i + half), i * stride);
      }
      base += width;
    }
    width *= 2;
  }
  return values;
}

function prepare(input: [f32], n: i32, twiddle: i32, own values: f32[]): f32[] {
  for i in 0..n / 2 {
    let wr = tables.MDCT[twiddle + 2 * i];
    let wi = tables.MDCT[twiddle + 2 * i + 1];
    let even = input[2 * i];
    let odd = -input[n - 1 - 2 * i];
    values = values.with(2 * i, odd * wi - even * wr).with(2 * i + 1, odd * wr + even * wi);
  }
  return values;
}

function expand(values: [f32], n: i32, twiddle: i32, own output: f32[]): f32[] {
  let half = n / 2;
  for i in 0..half {
    let wr = tables.MDCT[twiddle + 2 * i];
    let wi = tables.MDCT[twiddle + 2 * i + 1];
    let re = wr * values[2 * i] + wi * values[2 * i + 1];
    let im = wi * values[2 * i] - wr * values[2 * i + 1];
    let local = if (i < n / 4) { i } else { i - n / 4 };
    let forward = 2 * local;
    let reverse = half - 1 - forward;
    if (i < n / 4) {
      output = output.with(reverse, -im).with(half + forward, im).with(n + reverse, re).with(n + half + forward, re);
    } else {
      output = output.with(forward, -re).with(half + reverse, re).with(n + forward, im).with(n + half + reverse, im);
    }
  }
  return output;
}

pub function imdct(input: [f32], exponent: i32, own scratch: Scratch): Result[Scratch, audio.AudioError] {
  if (exponent < 6 || exponent > 13) {
    return Err(audio.Header);
  }
  let n = 1 << exponent - 1;
  if (input.len() < n || scratch.fft.len() < n || scratch.time.len() < 2 * n) {
    return Err(audio.Buffer);
  }
  let Scratch { fft: values, time } = scratch;
  let start = tables.STARTS[exponent - 6];
  let prepared = prepare(input, n, start, values);
  let transformed = fft(prepared, n / 2);
  let output = expand(transformed, n, start, time);
  return Ok(Scratch { fft: transformed, time: output });
}

function sample(time: [f32], delay: [f32], offset: i32, at: i32, current: i32, previous: i32): f32 {
  let n = 1 << current;
  let old = 1 << previous;
  let window = tables.STARTS[(if (current < previous) { current } else { previous }) - 6];
  if (current == previous) {
    return delay[offset + at] * tables.WINDOW[window + n / 2 - 1 - at] + time[at] * tables.WINDOW[window + at];
  }
  if (previous > current) {
    let start = (old - n) / 4;
    if (at < start) {
      return delay[offset + at];
    }
    let i = at - start;
    return delay[offset + at] * tables.WINDOW[window + n / 2 - 1 - i] + time[i] * tables.WINDOW[window + i];
  }
  let start = (n - old) / 4;
  if (at >= old / 2) {
    return time[start + at];
  }
  return delay[offset + at] * tables.WINDOW[window + old / 2 - 1 - at] + time[start + at] * tables.WINDOW[window + at];
}

// Check every overlap dimension and finite sample before reusable-buffer writes.
// fern-lint: allow cyclomatic-complexity
pub function synth(input: [f32], own work: Work, channel: i32, channels: i32, maximum: i32, current: i32, previous: i32): Result[Work, audio.AudioError] {
  let count = frames(current, previous)?;
  if (maximum < 6 || maximum > 13 || current > maximum || previous > maximum || channels < 1 || channels > 8 || channel < 0 || channel >= channels) {
    return Err(audio.Header);
  }
  let stride = 1 << maximum - 1;
  if (work.delay.len() < stride * channels || work.pcm.len() < count * channels) {
    return Err(audio.Buffer);
  }
  let Work { scratch, delay, pcm } = work;
  let transformed = imdct(input, current, scratch)?;
  let n = 1 << current;
  for i in 0..n {
    if (!transformed.time[i].is_finite()) {
      return Err(audio.Sample);
    }
  }
  let offset = channel * stride;
  if (previous != 0) {
    for i in 0..count {
      let value = sample(transformed.time, delay, offset, i, current, previous);
      if (!value.is_finite()) {
        return Err(audio.Sample);
      }
      pcm = pcm.with(i * channels + channel, value);
    }
  }
  for i in 0..n / 2 {
    delay = delay.with(offset + i, transformed.time[n / 2 + i]);
  }
  return Ok(Work { scratch: transformed, delay: delay, pcm: pcm });
}
```
