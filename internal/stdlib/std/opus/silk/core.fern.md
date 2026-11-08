# SILK PCM reconstruction

This module reconstructs one coded channel at the internal SILK sample rate.
It uses fixed-point long-term and short-term prediction with the reference saturation order.
The decoder retains bounded prediction history and reusable scratch arrays.
Stereo conversion, resampling, and packet loss recovery belong to the packet decoder.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK fixed-point inverse quantization and PCM reconstruction.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/silk/entropy";
import "std/opus/silk/math";

pub struct State {
  rate: i32,
  previous_gain: i32,
  lpc: i32[],
  history: i32[],
  pitch_buffer: i32[],
  pitch_q15: i32[],
  residual: i32[],
  lpc_scratch: i32[],
  output: i32[],
}

pub function state(): State {
  return State {
    rate: 0, previous_gain: 65536, lpc: pcm.zeros(16), history: pcm.zeros(480),
    pitch_buffer: pcm.zeros(320), pitch_q15: pcm.zeros(640), residual: pcm.zeros(80),
    lpc_scratch: pcm.zeros(96), output: pcm.zeros(320),
  };
}

function clear(own values: i32[]): i32[] {
  for i in 0..values.len() {
    values = values.with(i, 0);
  }
  return values;
}

pub function reset(own previous: State): State {
  return State { ...previous, lpc: clear(previous.lpc), history: clear(previous.history) };
}

function prepare(own previous: State, rate: i32): State {
  if (previous.rate != 0 && previous.rate != rate) {
    return reset(previous);
  }
  return previous;
}

function valid_pitch(control: entropy.Control, rate: i32, duration: i32): boolean {
  if (control.pitch.len() != duration / 5 || control.ltp.len() != duration) {
    return false;
  }
  for lag in control.pitch {
    if (lag < 2 * rate || lag > 18 * rate) {
      return false;
    }
  }
  return true;
}

function valid_signal(control: entropy.Control, rate: i32, duration: i32): boolean {
  if (control.signal == 1 || control.signal == 2) {
    return true;
  }
  if (control.signal != 3) {
    return false;
  }
  return valid_pitch(control, rate, duration);
}

function valid_control(control: entropy.Control, rate: i32, duration: i32): boolean {
  if (!entropy.valid_frame(rate, duration)) {
    return false;
  }
  let order = if (rate == 16) { 16 } else { 10 };
  if (control.lpc.len() != order || control.first_lpc.len() != 0 && control.first_lpc.len() != order) {
    return false;
  }
  if (control.gains.len() != duration / 5 || control.excitation.len() < rate * duration) {
    return false;
  }
  for gain in control.gains {
    if (gain <= 0) {
      return false;
    }
  }
  return valid_signal(control, rate, duration);
}

function valid_state(previous: State): boolean {
  return previous.lpc.len() == 16 && previous.history.len() == 480 && previous.pitch_buffer.len() == 320
    && previous.pitch_q15.len() == 640 && previous.residual.len() == 80 && previous.lpc_scratch.len() == 96 && previous.output.len() == 320;
}

function analysis_filter(own output: i32[], input: [i32], coefficients: [i32], start: i32, offset: i32, length: i32): i32[] {
  let order = coefficients.len();
  for i in order..length {
    let prediction = math.smulbb(input[start + offset + i - 1], coefficients[0]);
    for j in 1..order {
      prediction = math.smlabb(prediction, input[start + offset + i - 1 - j], coefficients[j]);
    }
    let residual = (input[start + offset + i] << 12) - prediction;
    output = output.with(start + i, math.sat16(math.rshift_round32(residual, 12)));
  }
  for i in 0..order {
    output = output.with(start + i, 0);
  }
  return output;
}

function rewhite(own history: i32[], own pitch_buffer: i32[], own pitch_q15: i32[], output: [i32], coefficients: [i32], lag: i32, subframe: i32, length: i32, buffer_at: i32, gain: i32, scale: i32): (i32[], i32[], i32[]) {
  let memory = 4 * length;
  if (subframe == 2) {
    for i in 0..2 * length {
      history = history.with(memory + i, output[i]);
    }
  }
  let start = memory - lag - coefficients.len() - 2;
  pitch_buffer = analysis_filter(pitch_buffer, history, coefficients, start, subframe * length, memory - start);
  let inverse = math.inverse32_varq(gain, 47);
  if (subframe == 0) {
    inverse = math.smulwb(inverse, scale) << 2;
  }
  for i in 0..lag + 2 {
    pitch_q15 = pitch_q15.with(buffer_at - i - 1, math.smulwb(inverse, pitch_buffer[memory - i - 1]));
  }
  return (history, pitch_buffer, pitch_q15);
}

function voiced_residual(own residual: i32[], own pitch: i32[], excitation: [i32], filters: [i32], subframe: i32, length: i32, lag: i32, buffer_at: i32): (i32[], i32[]) {
  let index = buffer_at - lag + 2;
  for i in 0..length {
    let prediction_q13 = 2;
    for tap in 0..5 {
      prediction_q13 = math.smlawb(prediction_q13, pitch[index + i - tap], filters[subframe * 5 + tap] * 128);
    }
    let value = (excitation[subframe * length + i] << 6) + (prediction_q13 << 1);
    residual = residual.with(i, value);
    pitch = pitch.with(buffer_at + i, value << 1);
  }
  return (residual, pitch);
}

function synthesize(own output: i32[], own scratch: i32[], residual: [i32], coefficients: [i32], gain: i32, subframe: i32, length: i32): (i32[], i32[]) {
  let order = coefficients.len();
  for i in 0..length {
    let prediction_q10 = order >> 1;
    for j in 0..order {
      prediction_q10 = math.smlawb(prediction_q10, scratch[16 + i - j - 1], coefficients[j]);
    }
    let current = math.add_sat32(residual[i], math.lshift_sat32(prediction_q10, 4));
    scratch = scratch.with(16 + i, current);
    output = output.with(subframe * length + i, math.sat16(math.rshift_round32(math.smulww(current, gain >> 6), 8)));
  }
  for i in 0..16 {
    scratch = scratch.with(i, scratch[length + i]);
  }
  return (output, scratch);
}

function adjust_gain(own scratch: i32[], previous: i32, gain: i32): (i32[], i32) {
  if (previous == gain) {
    return (scratch, 65536);
  }
  let adjustment = math.div32_varq(previous, gain, 16);
  for i in 0..16 {
    scratch = scratch.with(i, math.smulww(adjustment, scratch[i]));
  }
  return (scratch, adjustment);
}

function long_term(own history: i32[], own pitch_buffer: i32[], own pitch_q15: i32[], own residual: i32[], output: [i32], coefficients: [i32], control: entropy.Control, subframe: i32, length: i32, buffer_at: i32, adjustment: i32): (i32[], i32[], i32[], i32[]) {
  let lag = control.pitch[subframe];
  if (subframe == 0 || subframe == 2 && control.interpolation < 4) {
    let (h, p, q) = rewhite(history, pitch_buffer, pitch_q15, output, coefficients, lag, subframe, length, buffer_at, control.gains[subframe], control.ltp_scale);
    history = h;
    pitch_buffer = p;
    pitch_q15 = q;
  } else if (adjustment != 65536) {
    for i in 0..lag + 2 {
      let at = buffer_at - i - 1;
      pitch_q15 = pitch_q15.with(at, math.smulww(adjustment, pitch_q15[at]));
    }
  }
  let (r, q) = voiced_residual(residual, pitch_q15, control.excitation, control.ltp, subframe, length, lag, buffer_at);
  return (history, pitch_buffer, q, r);
}

function retain(own history: i32[], output: [i32], frames: i32, memory: i32): i32[] {
  let retained = memory - frames;
  for i in 0..retained {
    history = history.with(i, history[frames + i]);
  }
  for i in 0..frames {
    history = history.with(retained + i, output[i]);
  }
  return history;
}

pub function decode(own previous: State, control: entropy.Control, rate: i32, duration: i32): Result[(State, i32[]), pcm.AudioError] {
  if (!valid_control(control, rate, duration) || !valid_state(previous)) {
    return Err(pcm.Header);
  }
  previous = prepare(previous, rate);
  let length = rate * 5;
  let memory = 4 * length;
  let frames = rate * duration;
  let count = duration / 5;
  let pitch_buffer = clear(previous.pitch_buffer);
  let pitch_q15 = clear(previous.pitch_q15);
  let history = previous.history;
  let residual = previous.residual;
  let scratch = previous.lpc_scratch;
  let output = previous.output;
  let previous_gain = previous.previous_gain;
  let lpc_history = previous.lpc;
  for i in 0..16 {
    scratch = scratch.with(i, lpc_history[i]);
  }
  let buffer_at = memory;
  for subframe in 0..count {
    let coefficients = if (subframe < 2 && control.first_lpc.len() > 0) { control.first_lpc } else { control.lpc };
    let gain = control.gains[subframe];
    let (scaled, adjustment) = adjust_gain(scratch, previous_gain, gain);
    scratch = scaled;
    previous_gain = gain;
    if (control.signal == 3) {
      let (h, p, q, r) = long_term(history, pitch_buffer, pitch_q15, residual, output, coefficients, control, subframe, length, buffer_at, adjustment);
      history = h;
      pitch_buffer = p;
      pitch_q15 = q;
      residual = r;
      buffer_at += length;
    } else {
      for i in 0..length {
        residual = residual.with(i, control.excitation[subframe * length + i] << 6);
      }
    }
    let (p, s) = synthesize(output, scratch, residual, coefficients, gain, subframe, length);
    output = p;
    scratch = s;
  }
  for i in 0..16 {
    lpc_history = lpc_history.with(i, scratch[i]);
  }
  history = retain(history, output, frames, memory);
  let state = State {
    rate: rate, previous_gain: previous_gain, lpc: lpc_history, history: history,
    pitch_buffer: pitch_buffer, pitch_q15: pitch_q15, residual: residual, lpc_scratch: scratch, output: output,
  };
  return Ok((state, output));
}
```
