# AAC inverse transforms and overlap

This module performs the 128-point and 1024-point inverse transforms used by AAC-LC.
It uses an in-place complex FFT and generated binary32 coefficients.
Sine and Kaiser-Bessel windows support all four AAC window sequences.

The caller supplies spectral input, channel delay storage, and reusable scratch and PCM arrays.
No transform plan is built during frame decode.
This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's AAC and IMDCT modules.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// Previous author: Kostya Shishkov <kostya.shiskov@gmail.com>.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/aac/tables";
import "std/audio/pcm" as audio;
import "std/f32";
import "std/f64";

pub struct Scratch { fft: f32[], time: f32[], short: f32[] }

pub struct Work { delay: f32[], scratch: Scratch, pcm: f32[] }

pub function zeros(count: i32): f32[] {
  let values: f32[] = [];
  for _ in 0..count {
    values = values.append(0.0f32);
  }
  return values;
}

pub function create(): Scratch {
  return Scratch { fft: zeros(1024), time: zeros(2048), short: zeros(1152) };
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
      values = values.with(2 * i, values[2 * reverse]);
      values = values.with(2 * i + 1, values[2 * reverse + 1]);
      values = values.with(2 * reverse, re);
      values = values.with(2 * reverse + 1, im);
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
  values = values.with(left, ar + re);
  values = values.with(left + 1, ai + im);
  values = values.with(right, ar - re);
  return values.with(right + 1, ai - im);
}

function fft(own values: f32[], n: i32): f32[] {
  values = reorder(values, n);
  let width = 2;
  while (width <= n) {
    let half = width / 2;
    let stride = 512 / width;
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

function prepare(input: [f32], at: i32, n: i32, own values: f32[]): f32[] {
  let twiddle = if (n == 1024) { tables.MDCT_LONG } else { tables.MDCT_SHORT };
  for i in 0..n / 2 {
    let even = input[at + 2 * i];
    let odd = -input[at + n - 1 - 2 * i];
    let wr = twiddle[2 * i];
    let wi = twiddle[2 * i + 1];
    values = values.with(2 * i, odd * wi - even * wr);
    values = values.with(2 * i + 1, odd * wr + even * wi);
  }
  return values;
}

function expand(values: [f32], n: i32, own output: f32[], at: i32): f32[] {
  let twiddle = if (n == 1024) { tables.MDCT_LONG } else { tables.MDCT_SHORT };
  let half = n / 2;
  let quarter = n / 4;
  for k in 0..half {
    let wr = twiddle[2 * k];
    let wi = twiddle[2 * k + 1];
    let re = wr * values[2 * k] + wi * values[2 * k + 1];
    let im = wi * values[2 * k] - wr * values[2 * k + 1];
    let i = k % quarter;
    let forward = 2 * i;
    let reverse = half - 1 - 2 * i;
    if (k < quarter) {
      output = output.with(at + reverse, -im);
      output = output.with(at + half + forward, im);
      output = output.with(at + n + reverse, re);
      output = output.with(at + n + half + forward, re);
    } else {
      output = output.with(at + forward, -re);
      output = output.with(at + half + reverse, re);
      output = output.with(at + n + forward, im);
      output = output.with(at + n + half + reverse, im);
    }
  }
  return output;
}

pub function imdct(input: [f32], at: i32, n: i32, own scratch: Scratch, out_at: i32): Result[Scratch, audio.AudioError] {
  if (n != 128 && n != 1024 || at < 0 || at > input.len() || n > input.len() - at) {
    return Err(audio.Header);
  }
  if (scratch.fft.len() < n || out_at < 0 || out_at > scratch.time.len() || 2 * n > scratch.time.len() - out_at) {
    return Err(audio.Buffer);
  }
  let Scratch { fft: values, time, short } = scratch;
  let transformed = fft(prepare(input, at, n, values), n / 2);
  let output = expand(transformed, n, time, out_at);
  return Ok(Scratch { fft: transformed, time: output, short: short });
}

function short_overlap(own scratch: Scratch, shape: i32, previous: i32): Scratch {
  let window = if (shape == 0) { tables.SINE_SHORT } else { tables.KBD_SHORT };
  let first = if (previous == 0) { tables.SINE_SHORT } else { tables.KBD_SHORT };
  let output = scratch.short;
  for i in 0..output.len() {
    output = output.with(i, 0.0f32);
  }
  for w in 0..8 {
    for i in 0..128 {
      let coefficient = if (w == 0) { first[i] } else { window[i] };
      output = output.with(w * 128 + i, output[w * 128 + i] + scratch.time[w * 256 + i] * coefficient);
      output = output.with(w * 128 + i + 128, scratch.time[w * 256 + i + 128] * window[127 - i]);
    }
  }
  return Scratch { ...scratch, short: output };
}

function new_sample(scratch: Scratch, at: i32, sequence: i32, previous: i32): f32 {
  if (sequence == 2) {
    return if (at < 448) { 0.0f32 } else { scratch.short[at - 448] };
  }
  if (sequence == 3) {
    if (at < 448) {
      return 0.0f32;
    }
    if (at >= 576) {
      return scratch.time[at];
    }
    let window = if (previous == 0) { tables.SINE_SHORT } else { tables.KBD_SHORT };
    return scratch.time[at] * window[at - 448];
  }
  let window = if (previous == 0) { tables.SINE_LONG } else { tables.KBD_LONG };
  return scratch.time[at] * window[at];
}

function delayed_sample(scratch: Scratch, at: i32, sequence: i32, shape: i32): f32 {
  if (sequence == 2) {
    return if (at < 576) { scratch.short[at + 576] } else { 0.0f32 };
  }
  if (sequence == 1) {
    if (at < 448) {
      return scratch.time[at + 1024];
    }
    if (at >= 576) {
      return 0.0f32;
    }
    let window = if (shape == 0) { tables.SINE_SHORT } else { tables.KBD_SHORT };
    return scratch.time[at + 1024] * window[575 - at];
  }
  let window = if (shape == 0) { tables.SINE_LONG } else { tables.KBD_LONG };
  return scratch.time[at + 1024] * window[1023 - at];
}

function overlap(own work: Work, channel: i32, channels: i32, out_at: i32, sequence: i32, shape: i32, previous: i32): Result[Work, audio.AudioError] {
  let Work { delay, scratch, pcm } = work;
  for i in 0..1024 {
    let value = delay[channel * 1024 + i] + new_sample(scratch, i, sequence, previous);
    if (!value.is_finite()) {
      return Err(audio.Sample);
    }
    pcm = pcm.with(out_at + i * channels + channel, value);
    delay = delay.with(channel * 1024 + i, delayed_sample(scratch, i, sequence, shape));
  }
  return Ok(Work { delay: delay, scratch: scratch, pcm: pcm });
}

// Validate caller storage before the long or eight-short transform path.
// fern-lint: allow cyclomatic-complexity
pub function synth(input: [f32], own work: Work, channel: i32, channels: i32, out_at: i32, sequence: i32, shape: i32, previous: i32): Result[Work, audio.AudioError] {
  if (channel < 0 || channel >= channels || channels < 1 || channels > 8 || sequence < 0 || sequence > 3 || shape < 0 || shape > 1 || previous < 0 || previous > 1) {
    return Err(audio.Header);
  }
  if (input.len() < 1024 || work.delay.len() < channels * 1024 || out_at < 0 || out_at > work.pcm.len() || channels * 1024 > work.pcm.len() - out_at) {
    return Err(audio.Buffer);
  }
  let Work { delay, scratch, pcm } = work;
  if (sequence == 2) {
    for w in 0..8 {
      scratch = imdct(input, w * 128, 128, scratch, w * 256)?;
    }
    scratch = short_overlap(scratch, shape, previous);
  } else {
    scratch = imdct(input, 0, 1024, scratch, 0)?;
  }
  return overlap(Work { delay: delay, scratch: scratch, pcm: pcm }, channel, channels, out_at, sequence, shape, previous);
}
```
