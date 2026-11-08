# SILK packet loss concealment

This module retains the fixed-point prediction and noise state used during packet loss.
It reconstructs missing internal frames and adjusts the first received frame after loss.
The caller retains separate state for the mid and side channels.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK fixed-point loss concealment, recovery gain, and comfort noise.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/silk/core";
import "std/opus/silk/entropy";
import "std/opus/silk/math";
import "std/opus/silk/noise";
import "std/opus/silk/predict";

pub struct State {
  rate: i32,
  pitch_q8: i32,
  ltp: i32[],
  lpc: i32[],
  seed: i32,
  random_scale: i32,
  last_lost: boolean,
  concealed_energy: i32,
  concealed_shift: i32,
  previous_scale: i32,
  gain0: i32,
  gain1: i32,
  subframes: i32,
  length: i32,
  count: i32,
  first: boolean,
  excitation: i32[],
  noise: noise.State,
}

pub function state(): State {
  return State {
    rate: 0, pitch_q8: 0, ltp: pcm.zeros(5), lpc: pcm.zeros(16), seed: 0,
    random_scale: 0, last_lost: false, concealed_energy: 0, concealed_shift: 0,
    previous_scale: 0, gain0: 65536, gain1: 65536, subframes: 2, length: 20,
    count: 0, first: true, excitation: pcm.zeros(320), noise: noise.state(),
  };
}

pub function reset_prediction(own previous: State): State {
  return State { ...previous, first: true };
}

function prepare(own previous: State, rate: i32, frames: i32): State {
  if (previous.rate == rate) {
    return previous;
  }
  return State { ...previous, rate: rate, pitch_q8: frames << 7, gain0: 65536, gain1: 65536, subframes: 2, length: 20 };
}

pub function recovery(control: entropy.Control, losses: i32): entropy.Control {
  if (losses == 0) {
    return control;
  }
  let lpc = control.lpc;
  let first = control.first_lpc;
  lpc = math.expand_lpc16(lpc, 63570);
  if (first.len() > 0) {
    first = math.expand_lpc16(first, 63570);
  }
  return entropy.Control { ...control, lpc: lpc, first_lpc: first };
}

function limit_ltp(own coefficients: i32[], gain: i32): i32[] {
  if (gain < 11469) {
    let scale = (11469 << 10) / (if (gain > 1) { gain } else { 1 });
    for i in 0..5 {
      coefficients = coefficients.with(i, math.s16(math.smulbb(coefficients[i], scale) >> 10));
    }
  } else if (gain > 15565) {
    let scale = (15565 << 14) / gain;
    for i in 0..5 {
      coefficients = coefficients.with(i, math.s16(coefficients[i] * scale >> 14));
    }
  }
  return coefficients;
}

function update_pitch(own previous: State, control: entropy.Control, rate: i32, duration: i32): State {
  let ltp = previous.ltp;
  let pitch = previous.pitch_q8;
  for i in 0..5 {
    ltp = ltp.with(i, 0);
  }
  if (control.signal != 3) {
    return State { ...previous, ltp: ltp, pitch_q8: rate * 18 << 8 };
  }
  let gain = 0;
  let count = duration / 5;
  for offset in 0..count {
    if (offset * rate * 5 >= control.pitch[count - 1]) {
      break;
    }
    let subframe = count - 1 - offset;
    let candidate = 0;
    for tap in 0..5 {
      candidate += control.ltp[subframe * 5 + tap] * 128;
    }
    if (candidate > gain) {
      gain = candidate;
      pitch = control.pitch[subframe] << 8;
    }
  }
  ltp = ltp.with(2, math.s16(gain));
  ltp = limit_ltp(ltp, gain);
  return State { ...previous, ltp: ltp, pitch_q8: pitch };
}

function update(own previous: State, control: entropy.Control, rate: i32, duration: i32): State {
  previous = prepare(previous, rate, rate * duration);
  previous = update_pitch(previous, control, rate, duration);
  let lpc = previous.lpc;
  for i in 0..control.lpc.len() {
    lpc = lpc.with(i, control.lpc[i]);
  }
  let count = duration / 5;
  let excitation = previous.excitation;
  for i in 0..rate * duration {
    excitation = excitation.with(i, control.excitation[i] << 6);
  }
  return State {
    ...previous, lpc: lpc, excitation: excitation,
    previous_scale: if (control.signal == 3) { control.ltp_scale } else { 0 },
    gain0: control.gains[count - 2], gain1: control.gains[count - 1], subframes: count, length: rate * 5,
  };
}

function glue(own previous: State, own output: i32[], frames: i32): (State, i32[]) {
  if (previous.count > 0) {
    let (energy, shift) = math.sum_squares(output[0:frames]);
    return (State { ...previous, concealed_energy: energy, concealed_shift: shift, last_lost: true }, output);
  }
  if (!previous.last_lost) {
    return (previous, output);
  }
  let (value, shift) = math.sum_squares(output[0:frames]);
  let energy = value;
  let concealed = previous.concealed_energy;
  if (shift > previous.concealed_shift) {
    concealed >>= shift - previous.concealed_shift;
  } else if (shift < previous.concealed_shift) {
    energy >>= previous.concealed_shift - shift;
  }
  if (energy > concealed) {
    let leading = math.clz32(concealed) - 1;
    concealed <<= leading;
    energy >>= if (24 - leading > 0) { 24 - leading } else { 0 };
    let fraction = concealed / (if (energy > 1) { energy } else { 1 });
    let gain = math.sqrt_approx(fraction) << 4;
    let slope = (65536 - gain) / frames << 2;
    for i in 0..frames {
      output = output.with(i, math.s16(math.smulwb(gain, output[i])));
      gain += slope;
      if (gain > 65536) {
        break;
      }
    }
  }
  return (State { ...previous, concealed_energy: concealed, last_lost: false }, output);
}

pub function received(own previous: State, own synthesis: core.State, control: entropy.Control, rate: i32, duration: i32): (State, core.State, i32[]) {
  previous = update(previous, control, rate, duration);
  let noise_state = previous.noise;
  noise_state = noise.learn(noise_state, control, previous.excitation, rate, duration);
  previous = State { ...previous, count: 0, noise: noise_state, first: false };
  let samples = synthesis.output;
  let (next, output) = glue(previous, samples, rate * duration);
  return (next, core.State { ...synthesis, output: output }, output);
}

function energy(excitation: [i32], gain0: i32, gain1: i32, length: i32, count: i32): (i32, i32, i32, i32) {
  let scaled = pcm.zeros(length * 2);
  for block in 0..2 {
    let start = (block + count - 2) * length;
    let gain = if (block == 0) { gain0 >> 6 } else { gain1 >> 6 };
    for i in 0..length {
      scaled = scaled.with(block * length + i, math.sat16(math.smulww(excitation[start + i], gain) >> 8));
    }
  }
  let (a, a_shift) = math.sum_squares(scaled[0:length]);
  let (b, b_shift) = math.sum_squares(scaled[length:length * 2]);
  return (a, a_shift, b, b_shift);
}

function random_gains(previous: State, coefficients: [i32], voiced: boolean, order: i32): (i32, i32, i32) {
  let harmonic = if (previous.count > 0) { 31130 } else { 32440 };
  let random = if (voiced) { if (previous.count > 0) { 26214 } else { 31130 } } else { if (previous.count > 0) { 29491 } else { 32440 } };
  let scale = previous.random_scale;
  if (previous.count == 0) {
    scale = 16384;
    if (voiced) {
      for value in previous.ltp {
        scale = math.s16(scale - value);
      }
      if (scale < 3277) {
        scale = 3277;
      }
      scale = math.s16(scale * previous.previous_scale >> 14);
    } else {
      let inverse = predict.inverse_gain(coefficients[0:order]);
      let downscale = math.clamp(1 << 22, inverse, 1 << 27) << 3;
      random = math.smulwb(downscale, random) >> 14;
    }
  }
  return (harmonic, random, scale);
}

function whiten(own pitch: i32[], own whitened: i32[], history: [i32], coefficients: [i32], lag: i32, memory: i32, gain: i32): (i32[], i32[]) {
  let order = coefficients.len();
  let start = memory - lag - order - 2;
  whitened = core.analysis_filter(whitened, history, coefficients, start, 0, memory - start);
  let inverse = math.inverse32_varq(gain, 46);
  if (inverse > math.MAX32 >> 1) {
    inverse = math.MAX32 >> 1;
  }
  for i in start + order..memory {
    pitch = pitch.with(i, math.smulwb(inverse, whitened[i]));
  }
  return (pitch, whitened);
}

function long_term(own previous: State, own pitch: i32[], source: [i32], length: i32, count: i32, harmonic: i32, random: i32, own scale: i32): (State, i32[], i32) {
  let pitch_q8 = previous.pitch_q8;
  let lag = math.rshift_round32(pitch_q8, 8);
  let ltp = previous.ltp;
  let seed = previous.seed;
  let at = 4 * length;
  for _ in 0..count {
    let index = at - lag + 2;
    for i in 0..length {
      let prediction = 2;
      for tap in 0..5 {
        prediction = math.smlawb(prediction, pitch[index + i - tap], ltp[tap]);
      }
      seed = math.silk_rand(seed);
      let sample = source[seed >> 25 & 127];
      pitch = pitch.with(at + i, math.smlawb(prediction, sample, scale) << 2);
    }
    at += length;
    for tap in 0..5 {
      ltp = ltp.with(tap, math.s16(harmonic * ltp[tap] >> 15));
    }
    scale = math.s16(scale * random >> 15);
    pitch_q8 = math.smlawb(pitch_q8, pitch_q8, 655);
    if (pitch_q8 > 18 * previous.rate << 8) {
      pitch_q8 = 18 * previous.rate << 8;
    }
    lag = math.rshift_round32(pitch_q8, 8);
  }
  return (State { ...previous, pitch_q8: pitch_q8, ltp: ltp, seed: seed, random_scale: scale }, pitch, lag);
}

function short_term(own synthesis: core.State, own pitch: i32[], coefficients: [i32], frames: i32, memory: i32, gain: i32): (core.State, i32[]) {
  let lpc = synthesis.lpc;
  for i in 0..16 {
    pitch = pitch.with(memory - 16 + i, lpc[i]);
  }
  let output = synthesis.output;
  let order = coefficients.len();
  for i in 0..frames {
    let prediction = order >> 1;
    for j in 0..order {
      prediction = math.smlawb(prediction, pitch[memory + i - j - 1], coefficients[j]);
    }
    let value = math.add_sat32(pitch[memory + i], math.lshift_sat32(prediction, 4));
    pitch = pitch.with(memory + i, value);
    output = output.with(i, math.sat16(math.rshift_round32(math.smulww(value, gain >> 6), 8)));
  }
  for i in 0..16 {
    lpc = lpc.with(i, pitch[memory + frames - 16 + i]);
  }
  let history = synthesis.history;
  history = core.retain(history, output, frames, memory);
  return (core.State { ...synthesis, lpc: lpc, history: history, output: output, pitch_q15: pitch }, output);
}

pub function conceal(own previous: State, own synthesis: core.State, voiced: boolean, rate: i32, duration: i32): Result[(State, core.State, i32[], i32), pcm.AudioError] {
  if (!entropy.valid_frame(rate, duration)) {
    return Err(pcm.Header);
  }
  let length = rate * 5;
  let count = duration / 5;
  let frames = rate * duration;
  let memory = 4 * length;
  let order = if (rate == 16) { 16 } else { 10 };
  previous = prepare(previous, rate, frames);
  let coefficients = previous.lpc;
  if (previous.first) {
    for i in 0..16 {
      coefficients = coefficients.with(i, 0);
    }
  }
  let (a, a_shift, b, b_shift) = energy(previous.excitation, previous.gain0, previous.gain1, length, count);
  let random_start = previous.subframes * previous.length - 128;
  if (a >> b_shift < b >> a_shift) {
    random_start = (previous.subframes - 1) * previous.length - 128;
  }
  if (random_start < 0) {
    random_start = 0;
  }
  coefficients = math.expand_lpc16(coefficients, 64881);
  let (harmonic, random, scale) = random_gains(previous, coefficients, voiced, order);
  let pitch = synthesis.pitch_q15;
  for i in 0..memory + frames {
    pitch = pitch.with(i, 0);
  }
  let lag = math.rshift_round32(previous.pitch_q8, 8);
  let whitened = synthesis.pitch_buffer;
  let (p, w) = whiten(pitch, whitened, synthesis.history, coefficients[0:order], lag, memory, previous.gain1);
  previous = State { ...previous, lpc: coefficients };
  let source = previous.excitation;
  let (next, predicted, final_lag) = long_term(previous, p, source[random_start:], length, count, harmonic, random, scale);
  let (next_synthesis, samples) = short_term(synthesis, predicted, coefficients[0:order], frames, memory, next.gain1);
  next_synthesis = core.State { ...next_synthesis, pitch_buffer: w };
  let noise_state = next.noise;
  let (next_noise, noisy) = noise.add(noise_state, samples, rate, frames, next.random_scale, next.gain1)?;
  next = State { ...next, noise: next_noise, count: next.count + 1 };
  let (state, output) = glue(next, noisy, frames);
  return Ok((state, core.State { ...next_synthesis, rate: rate, output: output }, output, final_lag));
}
```
