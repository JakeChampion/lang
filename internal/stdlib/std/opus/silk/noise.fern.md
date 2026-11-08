# SILK comfort noise

This module learns the noise spectrum and excitation from inactive SILK frames.
It adds fixed-point comfort noise during packet loss and retains synthesis history.
The caller supplies the valid sample count separately from the reusable PCM buffer.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK fixed-point comfort noise with persistent excitation and synthesis state.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/silk/entropy";
import "std/opus/silk/math";
import "std/opus/silk/predict";

pub struct State { rate: i32, excitation: i32[], lsf: i32[], synthesis: i32[], gain: i32, seed: i32, scratch: i32[] }

pub function state(): State {
  return State { rate: 0, excitation: pcm.zeros(320), lsf: pcm.zeros(16), synthesis: pcm.zeros(16), gain: 0, seed: 3176576, scratch: pcm.zeros(336) };
}

function prepare(own previous: State, rate: i32): State {
  if (previous.rate == rate) {
    return previous;
  }
  let order = if (rate == 16) { 16 } else { 10 };
  let step = 32767 / (order + 1);
  let lsf = previous.lsf;
  for i in 0..order {
    lsf = lsf.with(i, (i + 1) * step);
  }
  return State { ...previous, rate: rate, lsf: lsf, gain: 0, seed: 3176576 };
}

pub function learn(own previous: State, control: entropy.Control, excitation: [i32], rate: i32, duration: i32): State {
  previous = prepare(previous, rate);
  let lsf = previous.lsf;
  let synthesis = previous.synthesis;
  let samples = previous.excitation;
  let gain = previous.gain;
  if (control.signal == 1) {
    for i in 0..control.lsf.len() {
      lsf = lsf.with(i, math.s16(lsf[i] + math.smulwb(control.lsf[i] - lsf[i], 16348)));
    }
    let maximum = 0;
    let subframe = 0;
    for i in 0..control.gains.len() {
      if (control.gains[i] > maximum) {
        maximum = control.gains[i];
        subframe = i;
      }
    }
    let length = rate * 5;
    let i = (duration / 5 - 1) * length - 1;
    while (i >= 0) {
      samples = samples.with(length + i, samples[i]);
      i -= 1;
    }
    for k in 0..length {
      samples = samples.with(k, excitation[subframe * length + k]);
    }
    for value in control.gains {
      gain += math.smulwb(value - gain, 4634);
      if (math.smulww(gain, 46396) > value) {
        gain = value;
      }
    }
  }
  for i in 0..control.lsf.len() {
    synthesis = synthesis.with(i, 0);
  }
  return State { ...previous, lsf: lsf, synthesis: synthesis, excitation: samples, gain: gain };
}

function noise_gain(smoothed: i32, scale: i32, previous_gain: i32): i32 {
  let gain = math.smulww(scale, previous_gain);
  if (gain >= 1 << 21 || smoothed > 1 << 23) {
    gain = (gain >> 16) * (gain >> 16);
    gain = (smoothed >> 16) * (smoothed >> 16) - (gain << 5);
    return math.sqrt_approx(gain) << 16;
  }
  gain = math.smulww(gain, gain);
  gain = math.smulww(smoothed, smoothed) - (gain << 5);
  return math.sqrt_approx(gain) << 8;
}

pub function add(own previous: State, own output: i32[], rate: i32, frames: i32, random_scale: i32, previous_gain: i32): Result[(State, i32[]), pcm.AudioError] {
  previous = prepare(previous, rate);
  let order = if (rate == 16) { 16 } else { 10 };
  let coefficients = predict.to_lpc(previous.lsf[0:order], rate == 16)?;
  let gain = noise_gain(previous.gain, random_scale, previous_gain) >> 6;
  let scratch = previous.scratch;
  let synthesis = previous.synthesis;
  for i in 0..16 {
    scratch = scratch.with(i, synthesis[i]);
  }
  let mask = 255;
  while (mask > frames) {
    mask >>= 1;
  }
  let seed = previous.seed;
  for i in 0..frames {
    seed = math.silk_rand(seed);
    scratch = scratch.with(16 + i, previous.excitation[seed >> 24 & mask]);
  }
  for i in 0..frames {
    let prediction = order >> 1;
    for j in 0..order {
      prediction = math.smlawb(prediction, scratch[16 + i - j - 1], coefficients[j]);
    }
    let value = math.add_sat32(scratch[16 + i], math.lshift_sat32(prediction, 4));
    scratch = scratch.with(16 + i, value);
    let sample = math.sat16(math.rshift_round32(math.smulww(value, gain), 8));
    output = output.with(i, math.sat16(output[i] + sample));
  }
  for i in 0..16 {
    synthesis = synthesis.with(i, scratch[frames + i]);
  }
  return Ok((State { ...previous, seed: seed, scratch: scratch, synthesis: synthesis }, output));
}
```
