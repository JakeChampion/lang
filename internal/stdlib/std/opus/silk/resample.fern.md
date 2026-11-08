# SILK output resampling

This module converts one PCM channel from an internal SILK rate to an Opus output rate.
Rates are in kilohertz. Input rates are 8, 12, and 16. Output rates are 8, 12, 16, 24, and 48.

The filters retain the fixed-point reference arithmetic and input delay.
`process_into` consumes a caller-owned output buffer and retains bounded filter scratch space.
The caller supplies signed 16-bit samples in `i32` values.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK decoder resampling with reference filters and input delay.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/silk/math";

const DELAYS: i32[] = [4, 0, 2, 0, 0, 0, 9, 4, 7, 4, 0, 3, 12, 7, 7];

const DOWN34: i32[] = [-20694, -13867, -49, 64, 17, -157, 353, -496, 163, 11047, 22205, -39, 6, 91, -170, 186, 23, -896, 6336, 19928, -19, -36, 102, -89, -24, 328, -951, 2568, 15909];

const DOWN23: i32[] = [-14457, -14019, 64, 128, -122, 36, 310, -768, 584, 9267, 17733, 12, 128, 18, -142, 288, -117, -865, 4123, 14459];

const DOWN12: i32[] = [616, -14323, -10, 39, 58, -46, -84, 120, 184, -315, -541, 1284, 5380, 9024];

const FRACTIONS: i32[] = [189, -600, 617, 30567, 117, -159, -1070, 29704, 52, 221, -2392, 28276, -4, 529, -3350, 26341, -48, 758, -3956, 23973, -80, 905, -4235, 21254, -99, 972, -4222, 18278, -107, 967, -3957, 15143, -103, 896, -3487, 11950, -91, 773, -2865, 8798, -71, 611, -2143, 5784, -46, 425, -1375, 2996];

pub struct State {
  input_rate: i32,
  output_rate: i32,
  method: i32,
  ratio_q16: i32,
  delay: i32,
  order: i32,
  fractions: i32,
  coefficients: i32[],
  iir: i32[],
  fir: i32[],
  hq_history: i32[],
  delay_buffer: i32[],
  up_buffer: i32[],
  down_buffer: i32[],
}

function output_index(rate: i32): i32 {
  if (rate == 8) {
    return 0;
  }
  if (rate == 12) {
    return 1;
  }
  if (rate == 16) {
    return 2;
  }
  if (rate == 24) {
    return 3;
  }
  if (rate == 48) {
    return 4;
  }
  return -1;
}

function down_configuration(input_rate: i32, output_rate: i32): (i32, i32, i32[]) {
  if (input_rate == 12) {
    return (18, 2, DOWN23);
  }
  if (output_rate == 12) {
    return (18, 3, DOWN34);
  }
  return (24, 1, DOWN12);
}

pub function resampler(input_rate: i32, output_rate: i32): Result[State, pcm.AudioError] {
  if (input_rate != 8 && input_rate != 12 && input_rate != 16) {
    return Err(pcm.Header);
  }
  let out_index = output_index(output_rate);
  if (out_index < 0) {
    return Err(pcm.Header);
  }
  let input_index = input_rate / 4 - 2;
  let method = 0;
  let up_factor = 0;
  let order = 0;
  let fractions = 0;
  let coefficients: i32[] = [];
  if (output_rate > input_rate) {
    method = if (output_rate == input_rate * 2) { 1 } else { 2 };
    up_factor = if (method == 2) { 1 } else { 0 };
  } else if (output_rate < input_rate) {
    method = 3;
    let (filter_order, filter_fractions, filter_coefficients) = down_configuration(input_rate, output_rate);
    order = filter_order;
    fractions = filter_fractions;
    coefficients = filter_coefficients;
  }
  let ratio = (input_rate << 14 + up_factor) / output_rate << 2;
  while (math.smulww(ratio, output_rate) < input_rate << up_factor) {
    ratio += 1;
  }
  return Ok(State {
    input_rate: input_rate, output_rate: output_rate, method: method,
    ratio_q16: ratio, delay: DELAYS[input_index * 5 + out_index], order: order,
    fractions: fractions, coefficients: coefficients, iir: pcm.zeros(6), fir: pcm.zeros(24),
    hq_history: pcm.zeros(8), delay_buffer: pcm.zeros(48), up_buffer: pcm.zeros(328), down_buffer: pcm.zeros(184),
  });
}

function allpass(input: i32, state: i32, coefficient: i32, high: boolean): (i32, i32) {
  let difference = input - state;
  let delta = math.smulwb(difference, coefficient);
  if (high) {
    delta += difference;
  }
  return (input + delta, state + delta);
}

function up2(own state: i32[], own output: i32[], at: i32, input: [i32]): (i32[], i32[]) {
  let s0 = state[0];
  let s1 = state[1];
  let s2 = state[2];
  let s3 = state[3];
  let s4 = state[4];
  let s5 = state[5];
  for i in 0..input.len() {
    let value = input[i] << 10;
    let (a, x) = allpass(value, s0, 1746, false);
    s0 = a;
    let (b, y) = allpass(x, s1, 14986, false);
    s1 = b;
    let (c, z) = allpass(y, s2, -26453, true);
    s2 = c;
    output = output.with(at + 2 * i, math.sat16(math.rshift_round32(z, 10)));
    let (d, u) = allpass(value, s3, 6854, false);
    s3 = d;
    let (e, v) = allpass(u, s4, 25769, false);
    s4 = e;
    let (f, w) = allpass(v, s5, -9994, true);
    s5 = f;
    output = output.with(at + 2 * i + 1, math.sat16(math.rshift_round32(w, 10)));
  }
  return (state.with(0, s0).with(1, s1).with(2, s2).with(3, s3).with(4, s4).with(5, s5), output);
}

function interpolate_up(own output: i32[], own at: i32, buffer: [i32], count: i32, ratio: i32): (i32[], i32) {
  let index = 0;
  while (index < count << 17) {
    let row = math.smulwb(index & 65535, 12);
    let base = index >> 16;
    let value = 0;
    for tap in 0..4 {
      value = math.smlabb(value, buffer[base + tap], FRACTIONS[row * 4 + tap]);
    }
    for tap in 0..4 {
      value = math.smlabb(value, buffer[base + 4 + tap], FRACTIONS[(11 - row) * 4 + 3 - tap]);
    }
    output = output.with(at, math.sat16(math.rshift_round32(value, 15)));
    at += 1;
    index += ratio;
  }
  return (output, at);
}

function iir_fir(own previous: State, input: [i32], own output: i32[], own at: i32): (State, i32[]) {
  let buffer = previous.up_buffer;
  let iir = previous.iir;
  let history = previous.hq_history;
  for i in 0..8 {
    buffer = buffer.with(i, history[i]);
  }
  let offset = 0;
  while (offset < input.len()) {
    let count = input.len() - offset;
    if (count > previous.input_rate * 10) {
      count = previous.input_rate * 10;
    }
    let (next_iir, next_buffer) = up2(iir, buffer, 8, input[offset:offset + count]);
    iir = next_iir;
    buffer = next_buffer;
    let (next_output, next_at) = interpolate_up(output, at, buffer, count, previous.ratio_q16);
    output = next_output;
    at = next_at;
    for i in 0..8 {
      buffer = buffer.with(i, buffer[2 * count + i]);
    }
    offset += count;
  }
  for i in 0..8 {
    history = history.with(i, buffer[i]);
  }
  return (State { ...previous, iir: iir, up_buffer: buffer, hq_history: history }, output);
}

function ar2(own state: i32[], own buffer: i32[], at: i32, input: [i32], coefficients: [i32]): (i32[], i32[]) {
  let s0 = state[0];
  let s1 = state[1];
  for i in 0..input.len() {
    let value = s0 + (input[i] << 8);
    buffer = buffer.with(at + i, value);
    s0 = math.smlawb(s1, value << 2, coefficients[0]);
    s1 = math.smulwb(value << 2, coefficients[1]);
  }
  return (state.with(0, s0).with(1, s1), buffer);
}

function down_sample(buffer: [i32], coefficients: [i32], index: i32, order: i32, fractions: i32): i32 {
  let base = index >> 16;
  let value = 0;
  if (order == 18) {
    let row = math.smulwb(index & 65535, fractions);
    for tap in 0..9 {
      value = math.smlawb(value, buffer[base + tap], coefficients[2 + row * 9 + tap]);
    }
    for tap in 0..9 {
      value = math.smlawb(value, buffer[base + 17 - tap], coefficients[2 + (fractions - 1 - row) * 9 + tap]);
    }
  } else {
    for tap in 0..12 {
      value = math.smlawb(value, buffer[base + tap] + buffer[base + 23 - tap], coefficients[2 + tap]);
    }
  }
  return math.sat16(math.rshift_round32(value, 6));
}

function down_fir(own previous: State, input: [i32], own output: i32[], own at: i32): (State, i32[]) {
  let buffer = previous.down_buffer;
  let iir = previous.iir;
  let history = previous.fir;
  let order = previous.order;
  for i in 0..order {
    buffer = buffer.with(i, history[i]);
  }
  let offset = 0;
  while (offset < input.len()) {
    let count = input.len() - offset;
    if (count > previous.input_rate * 10) {
      count = previous.input_rate * 10;
    }
    let (next_iir, next_buffer) = ar2(iir, buffer, order, input[offset:offset + count], previous.coefficients);
    iir = next_iir;
    buffer = next_buffer;
    let index = 0;
    while (index < count << 16) {
      output = output.with(at, down_sample(buffer, previous.coefficients, index, order, previous.fractions));
      index += previous.ratio_q16;
      at += 1;
    }
    for i in 0..order {
      buffer = buffer.with(i, buffer[count + i]);
    }
    offset += count;
  }
  for i in 0..order {
    history = history.with(i, buffer[i]);
  }
  return (State { ...previous, iir: iir, down_buffer: buffer, fir: history }, output);
}

function run(own previous: State, input: [i32], own output: i32[], at: i32): (State, i32[]) {
  if (previous.method == 1) {
    let filter = previous.iir;
    let (iir, samples) = up2(filter, output, at, input);
    return (State { ...previous, iir: iir }, samples);
  }
  if (previous.method == 2) {
    return iir_fir(previous, input, output, at);
  }
  if (previous.method == 3) {
    return down_fir(previous, input, output, at);
  }
  for i in 0..input.len() {
    output = output.with(at + i, input[i]);
  }
  return (previous, output);
}

function valid_storage(previous: State): boolean {
  return previous.iir.len() == 6 && previous.fir.len() == 24 && previous.hq_history.len() == 8
    && previous.delay_buffer.len() == 48 && previous.up_buffer.len() == 328 && previous.down_buffer.len() == 184;
}

function valid_configuration(previous: State): boolean {
  let input = previous.input_rate;
  let output = previous.output_rate;
  if (input != 8 && input != 12 && input != 16) {
    return false;
  }
  if (output_index(output) < 0) {
    return false;
  }
  let expected_method = if (input == output) { 0 } else if (output == 2 * input) { 1 } else if (output > input) { 2 } else { 3 };
  if (previous.method != expected_method) {
    return false;
  }
  let up = if (expected_method == 2) { 1 } else { 0 };
  return previous.ratio_q16 == ((input << 16 + up) + output - 1) / output;
}

function valid_filter(previous: State): boolean {
  if (previous.delay < 0 || previous.delay >= previous.input_rate) {
    return false;
  }
  if (previous.method != 3) {
    return true;
  }
  let (order, fractions, coefficients) = down_configuration(previous.input_rate, previous.output_rate);
  return previous.order == order && previous.fractions == fractions && previous.coefficients.len() == coefficients.len();
}

function valid_input(input: [i32]): boolean {
  for sample in input {
    if (sample < -32768 || sample > 32767) {
      return false;
    }
  }
  return true;
}

function valid_state(previous: State): boolean {
  return valid_configuration(previous) && valid_storage(previous) && valid_filter(previous);
}

pub function process_into(own previous: State, input: [i32], own output: i32[]): Result[(State, i32[]), pcm.AudioError] {
  if (!valid_state(previous)) {
    return Err(pcm.Header);
  }
  if (input.len() < previous.input_rate || input.len() > previous.input_rate * 60) {
    return Err(pcm.Limit);
  }
  let numerator = input.len() * previous.output_rate;
  if (numerator % previous.input_rate != 0) {
    return Err(pcm.Header);
  }
  let count = numerator / previous.input_rate;
  if (output.len() < count) {
    return Err(pcm.Buffer);
  }
  if (!valid_input(input)) {
    return Err(pcm.Sample);
  }
  let input_rate = previous.input_rate;
  let output_rate = previous.output_rate;
  let delay_length = previous.delay;
  let first = input_rate - delay_length;
  let delay = previous.delay_buffer;
  for i in 0..first {
    delay = delay.with(delay_length + i, input[i]);
  }
  let (a, samples) = run(previous, delay[0:input_rate], output, 0);
  let (b, result) = run(a, input[first:input.len() - delay_length], samples, output_rate);
  for i in 0..delay_length {
    delay = delay.with(i, input[input.len() - delay_length + i]);
  }
  return Ok((State { ...b, delay_buffer: delay }, result));
}
```
