# Opus playback modes

This module combines SILK and CELT at the output sample rate.
It handles hybrid packets, mode transitions, CELT redundancy, packet loss, and in-band error correction.
The caller supplies complete Opus packets. Output samples are interleaved floats.

SILK state starts when the first SILK or hybrid packet arrives.
The decoder retains bounded prediction and resampler histories between packets.
After an error, create a new decoder before the next packet.

The mode rules follow RFC 6716 and libopus commit `22244de5a79bd1d6d623c32e72bf1954b56235be`.
Copyright 2010 Xiph.Org Foundation and Skype Limited. Copyright 2024 Arm Limited.
Jean-Marc Valin and Koen Vos wrote the reference decoder.
The BSD license is in `licenses/opus/PLAYBACK-BSD.txt`.

```fern
<<*>>=
// Opus playback with shared SILK/CELT entropy and mode transition state.
// Copyright 2010 Xiph.Org Foundation and Skype Limited. Copyright 2024 Arm Limited.
// Written by Jean-Marc Valin and Koen Vos. SPDX-License-Identifier: BSD-2-Clause

import "std/audio/pcm";
import "std/opus/celt";
import "std/opus/modes";
import "std/opus/packet";
import "std/opus/range";
import "std/opus/silk/output";

pub struct State { celt: celt.Decoder, lowpass: Option[output.State], control: Control }

pub struct Control {
  sample_rate: i32,
  channels: i32,
  mode: i32,
  previous_mode: i32,
  previous_redundancy: boolean,
  bandwidth: i32,
  celt_bandwidth: i32,
  stream_channels: i32,
  frame_size: i32,
  final_range: u32,
}

pub function decoder(rate: i32, channels: i32): Result[State, pcm.AudioError] {
  let state = match (celt.decoder(rate, channels)) { Ok(value) => value, Err(_) => { return Err(pcm.Unsupported); } };
  return Ok(State {
    celt: state, lowpass: None, control: Control {
      sample_rate: rate, channels: channels,
      mode: 0, previous_mode: 0, previous_redundancy: false,
      bandwidth: packet.FULLBAND, celt_bandwidth: packet.FULLBAND, stream_channels: channels,
      frame_size: rate / 400, final_range: 0u32,
    },
  });
}

function zeros(count: i32): f32[] {
  let values: f32[] = [];
  for _ in 0..count {
    values = values.append(0.0f32);
  }
  return values;
}

function append(own left: f32[], right: [f32]): f32[] {
  for value in right {
    left = left.append(value);
  }
  return left;
}

function mode(toc: u8): i32 {
  return match (packet.mode(toc)) { Silk => 1, Hybrid => 2, Celt => 3 };
}

function end_band(bandwidth: i32): i32 {
  if (bandwidth == packet.NARROWBAND) {
    return 13;
  }
  if (bandwidth <= packet.WIDEBAND) {
    return 17;
  }
  return if (bandwidth == packet.SUPERWIDEBAND) { 19 } else { 21 };
}

function configure(own state: State, toc: u8): State {
  return State {
    ...state, control: Control {
      ...state.control, mode: mode(toc), bandwidth: packet.bandwidth(toc),
      stream_channels: if (packet.stereo(toc)) { 2 } else { 1 },
      frame_size: packet.samples_per_frame(toc, state.control.sample_rate),
    },
  };
}

function prepare_lowpass(own state: State): Result[State, pcm.AudioError] {
  if (state.control.mode == 3) {
    return Ok(state);
  }
  let low = if (state.control.previous_mode == 3) { output.decoder(state.control.sample_rate, state.control.channels)? } else {
    match (state.lowpass) { Some(value) => value, None => output.decoder(state.control.sample_rate, state.control.channels)? }
  };
  let rate = if (state.control.bandwidth == packet.NARROWBAND) { 8 } else if (state.control.bandwidth == packet.MEDIUMBAND) { 12 } else { 16 };
  let prepared = output.begin_packet(low, rate, state.control.stream_channels)?;
  return Ok(State { ...state, lowpass: Some(prepared) });
}

function celt_decode(own state: celt.Decoder, own entropy: range.Decoder, size: i32, frame: celt.Frame): Result[(celt.Decoder, f32[]), pcm.AudioError] {
  let (next, result) = state.decode_with(entropy, size, frame);
  let samples = match (result) { Ok(value) => value, Err(_) => { return Err(pcm.Header); } };
  return Ok((next, samples));
}

function highpass(own state: State, own entropy: range.Decoder, size: i32, frame: celt.Frame): Result[(State, f32[]), pcm.AudioError] {
  let State { celt, lowpass, control } = state;
  let (next, samples) = celt_decode(celt, entropy, size, frame)?;
  return Ok((State { celt: next, lowpass: lowpass, control: control }, samples));
}

function add(own low: f32[], high: [f32]): f32[] {
  for i in 0..high.len() {
    low = low.with(i, low[i] + high[i]);
  }
  return low;
}

function fade(own samples: f32[], start: i32, old: [f32], offset: i32, rate: i32, channels: i32): f32[] {
  for i in 0..rate / 400 {
    let window = modes.WINDOW[i * (48000 / rate)];
    let weight = window * window;
    for c in 0..channels {
      let target = (start + i) * channels + c;
      let prior = (offset + i) * channels + c;
      samples = samples.with(target, weight * samples[target] + (1.0f32 - weight) * old[prior]);
    }
  }
  return samples;
}

function transition(own samples: f32[], old: [f32], rate: i32, channels: i32): f32[] {
  let overlap = rate / 400;
  if (samples.len() < overlap * 2 * channels) {
    return fade(samples, 0, old, 0, rate, channels);
  }
  for i in 0..overlap * channels {
    samples = samples.with(i, old[i]);
  }
  return fade(samples, overlap, old, overlap, rate, channels);
}

function lost_size(state: State, requested: i32, current_mode: i32): i32 {
  let bounded = if (requested > state.control.frame_size) { state.control.frame_size } else { requested };
  let size = if (bounded > state.control.sample_rate / 50) { state.control.sample_rate / 50 } else { bounded };
  if (size > state.control.sample_rate / 100 && size < state.control.sample_rate / 50) {
    return state.control.sample_rate / 100;
  }
  if (current_mode != 1 && size > state.control.sample_rate / 200 && size < state.control.sample_rate / 100) {
    return state.control.sample_rate / 200;
  }
  return size;
}

function lost_lowpass(own state: State, size: i32): Result[(State, f32[]), pcm.AudioError] {
  let Some(low) = state.lowpass else {
    return Err(pcm.Header);
  };
  let duration = if (size < state.control.sample_rate / 100) { 10 } else { size * 1000 / state.control.sample_rate };
  let (next, values) = output.decode_lost(low, duration)?;
  let samples: f32[] = [];
  for i in 0..size * state.control.channels {
    samples = samples.append(values[i]);
  }
  return Ok((State { ...state, lowpass: Some(next) }, samples));
}

function lost_frame(own state: State, size: i32, current_mode: i32): Result[(State, f32[]), pcm.AudioError] {
  let samples = zeros(size * state.control.channels);
  if (current_mode != 3) {
    let (next, values) = lost_lowpass(state, size)?;
    state = next;
    samples = values;
  }
  if (current_mode != 1) {
    let frame = celt.Frame { frame_size: size, stream_channels: state.control.stream_channels, start: if (current_mode == 2) { 17 } else { 0 }, end: end_band(state.control.celt_bandwidth) };
    let (next, values) = highpass(state, range.decoder([]), 0, frame)?;
    state = next;
    if (current_mode == 3) {
      samples = values;
    } else {
      samples = add(samples, values);
    }
  }
  return Ok((State { ...state, control: Control { ...state.control, previous_mode: current_mode, previous_redundancy: false, final_range: 0u32 } }, samples));
}

function valid_loss_duration(control: Control, frames: i32): boolean {
  return frames > 0 && frames <= control.sample_rate * 120 / 1000 && frames % (control.sample_rate / 400) == 0;
}

pub function decode_lost(own state: State, frames: i32): Result[(State, f32[]), pcm.AudioError] {
  if (!valid_loss_duration(state.control, frames)) {
    return Err(pcm.Header);
  }
  let remaining = frames;
  let samples: f32[] = [];
  while (remaining > 0) {
    let current_mode = if (state.control.previous_redundancy) { 3 } else { state.control.previous_mode };
    if (current_mode == 0) {
      for value in zeros(remaining * state.control.channels) {
        samples = samples.append(value);
      }
      return Ok((State { ...state, control: Control { ...state.control, final_range: 0u32 } }, samples));
    }
    let size = lost_size(state, remaining, current_mode);
    let (next, values) = lost_frame(state, size, current_mode)?;
    state = next;
    for value in values {
      samples = samples.append(value);
    }
    remaining -= size;
  }
  return Ok((state, samples));
}

struct Redundancy { present: boolean, to_silk: boolean, bytes: i32, primary: i32 }

function redundancy(own entropy: range.Decoder, size: i32, current_mode: i32, fec: boolean): (range.Decoder, Redundancy) {
  let empty = Redundancy { present: false, to_silk: false, bytes: 0, primary: size };
  let extra = if (current_mode == 2) { 20 } else { 0 };
  if (fec || current_mode == 3 || entropy.tell() + 17 + extra > size * 8) {
    return (entropy, empty);
  }
  if (current_mode == 2) {
    let (next, present) = entropy.bit_logp(12);
    entropy = next;
    if (!present) {
      return (entropy, empty);
    }
  }
  let (next, to_silk) = entropy.bit_logp(1);
  entropy = next;
  let bytes = size - (entropy.tell() + 7) / 8;
  if (current_mode == 2) {
    let (last, value) = entropy.uint(256u32);
    entropy = last;
    bytes = value as i32 + 2;
  }
  let primary = size - bytes;
  if (primary * 8 < entropy.tell()) {
    return (entropy, Redundancy { ...empty, primary: 0 });
  }
  return (range.Decoder { ...entropy, storage: entropy.storage - bytes }, Redundancy { present: true, to_silk: to_silk, bytes: bytes, primary: primary });
}

function silk_decode(own low: output.State, own entropy: range.Decoder, duration: i32, fec: boolean): Result[(output.State, range.Decoder, f32[]), pcm.AudioError] {
  if (fec) {
    return output.decode_fec(low, entropy, duration);
  }
  return output.decode_frame(low, entropy, duration);
}

function lowpass(own state: State, own entropy: range.Decoder, fec: boolean): Result[(State, range.Decoder, f32[]), pcm.AudioError] {
  if (state.control.mode == 3) {
    let samples = zeros(state.control.frame_size * state.control.channels);
    return Ok((state, entropy, samples));
  }
  let Some(low) = state.lowpass else {
    return Err(pcm.Header);
  };
  let (next, last, samples) = silk_decode(low, entropy, state.control.frame_size * 1000 / state.control.sample_rate, fec)?;
  return Ok((State { ...state, lowpass: Some(next) }, last, samples));
}

function redundant_audio(own state: State, bytes: [u8], reset: boolean): Result[(State, f32[]), pcm.AudioError] {
  if (reset) {
    state = State { ...state, celt: state.celt.reset() };
  }
  let frame = celt.Frame { frame_size: state.control.sample_rate / 200, stream_channels: state.control.stream_channels, start: 0, end: end_band(state.control.bandwidth) };
  return highpass(state, range.decoder(bytes), bytes.len(), frame);
}

function primary_celt(own state: State, own entropy: range.Decoder, size: i32, frame: celt.Frame, fec: boolean): Result[(State, f32[]), pcm.AudioError] {
  if (fec) {
    return highpass(state, range.decoder([]), 0, frame);
  }
  return highpass(state, entropy, size, frame);
}

function silk_audio(own state: State, final_range: u32, own low: f32[], red: Redundancy): Result[(State, f32[]), pcm.AudioError] {
  if (state.control.previous_mode == 2 && !(red.present && red.to_silk && state.control.previous_redundancy)) {
    let frame = celt.Frame { frame_size: state.control.sample_rate / 400, stream_channels: state.control.stream_channels, start: 0, end: end_band(state.control.bandwidth) };
    let (next, tail) = highpass(state, range.decoder([255 as u8, 255 as u8]), 2, frame)?;
    state = next;
    low = add(low, tail);
  }
  return Ok((State { ...state, control: Control { ...state.control, final_range: final_range } }, low));
}

function primary_audio(own state: State, own entropy: range.Decoder, size: i32, own low: f32[], fec: boolean, red: Redundancy): Result[(State, f32[]), pcm.AudioError] {
  if (state.control.mode == 1) {
    return silk_audio(state, entropy.rng, low, red);
  }
  if (state.control.mode != state.control.previous_mode && state.control.previous_mode > 0 && !state.control.previous_redundancy) {
    state = State { ...state, celt: state.celt.reset() };
  }
  let frame = celt.Frame { frame_size: state.control.frame_size, stream_channels: state.control.stream_channels, start: if (state.control.mode == 2) { 17 } else { 0 }, end: end_band(state.control.bandwidth) };
  let (next, values) = primary_celt(state, entropy, size, frame, fec)?;
  state = next;
  let final_range = state.celt.rng;
  let samples = if (state.control.mode == 3) { values } else { add(low, values) };
  return Ok((State { ...state, control: Control { ...state.control, final_range: final_range } }, samples));
}

function has_transition(state: State): boolean {
  if (state.control.previous_mode == 0) {
    return false;
  }
  if (state.control.mode == 3) {
    return state.control.previous_mode != 3 && !state.control.previous_redundancy;
  }
  return state.control.previous_mode == 3;
}

function transition_before(own state: State, change: boolean): Result[(State, f32[]), pcm.AudioError] {
  if (change && state.control.mode == 3) {
    let size = if (state.control.frame_size < state.control.sample_rate / 200) { state.control.frame_size } else { state.control.sample_rate / 200 };
    return decode_lost(state, size);
  }
  return Ok((state, []));
}

function transition_after(own state: State, change: boolean, red: Redundancy): Result[(State, f32[]), pcm.AudioError] {
  if (change && state.control.mode != 3 && !red.present) {
    let size = state.control.sample_rate / 200;
    return decode_lost(state, size);
  }
  return Ok((state, []));
}

function leading_redundancy(own state: State, bytes: [u8], red: Redundancy): Result[(State, f32[], u32), pcm.AudioError] {
  if (red.present && red.to_silk) {
    let (next, values) = redundant_audio(state, bytes[red.primary:], false)?;
    let final_range = next.celt.rng;
    return Ok((next, values, final_range));
  }
  return Ok((state, [], 0u32));
}

function trailing_redundancy(own state: State, bytes: [u8], own samples: f32[], red: Redundancy): Result[(State, f32[], u32), pcm.AudioError] {
  if (!red.present || red.to_silk) {
    return Ok((state, samples, 0u32));
  }
  let (next, values) = redundant_audio(state, bytes[red.primary:], true)?;
  state = next;
  let final_range = state.celt.rng;
  let start = state.control.frame_size - state.control.sample_rate / 400;
  let old = samples[start * state.control.channels:];
  for i in 0..old.len() {
    samples = samples.with(start * state.control.channels + i, values[state.control.sample_rate / 400 * state.control.channels + i]);
  }
  samples = fade(samples, start, old, 0, state.control.sample_rate, state.control.channels);
  return Ok((state, samples, final_range));
}

function blend(state: State, own samples: f32[], prior: [f32], redundant: [f32], red: Redundancy, change: boolean): f32[] {
  if (red.present && red.to_silk && (state.control.previous_mode != 1 || state.control.previous_redundancy)) {
    return transition(samples, redundant, state.control.sample_rate, state.control.channels);
  }
  if (change && !red.present) {
    return transition(samples, prior, state.control.sample_rate, state.control.channels);
  }
  return samples;
}

function finish_frame(own state: State, own samples: f32[], red: Redundancy, redundant_range: u32): Result[(State, f32[]), pcm.AudioError] {
  let final_range = if (red.primary <= 1) { 0u32 } else { state.control.final_range ^ redundant_range };
  return Ok((State { ...state, control: Control { ...state.control, previous_mode: state.control.mode, previous_redundancy: red.present && !red.to_silk, final_range: final_range } }, samples));
}

function received(own state: State, bytes: [u8], fec: boolean): Result[(State, f32[]), pcm.AudioError] {
  if (bytes.len() <= 1) {
    let size = state.control.frame_size;
    return decode_lost(state, size);
  }
  let change = has_transition(state);
  let (before_low, first_prior) = transition_before(state, change)?;
  state = before_low;
  let (after_low, entropy, low) = lowpass(state, range.decoder(bytes), fec)?;
  state = after_low;
  let (last, red) = redundancy(entropy, bytes.len(), state.control.mode, fec);
  let (after_transition, second_prior) = transition_after(state, change, red)?;
  state = after_transition;
  let prior = if (state.control.mode == 3) { first_prior } else { second_prior };
  state = State { ...state, control: Control { ...state.control, celt_bandwidth: state.control.bandwidth } };
  let (after_red, redundant, leading_range) = leading_redundancy(state, bytes, red)?;
  state = after_red;
  let (next, samples) = primary_audio(state, last, red.primary, low, fec, red)?;
  state = next;
  let (last_state, faded, trailing_range) = trailing_redundancy(state, bytes, samples, red)?;
  state = last_state;
  samples = blend(state, faded, prior, redundant, red, change);
  let redundant_range = leading_range ^ trailing_range;
  return finish_frame(state, samples, red, redundant_range);
}

function parse(input: [u8]): Result[packet.Packet, pcm.AudioError] {
  // Frame sizes and duration bound copied data and PCM. Padding is borrowed.
  return match (packet.parse(input)) { Ok(value) => Ok(value), Err(_) => Err(pcm.Header) };
}

pub function decode(own state: State, input: [u8]): Result[(State, f32[]), pcm.AudioError] {
  let parsed = parse(input)?;
  state = configure(state, parsed.toc);
  let prepared = false;
  let samples: f32[] = [];
  for frame in parsed.frames {
    if (!prepared && frame.len() > 1) {
      state = prepare_lowpass(state)?;
      prepared = true;
    }
    let (next, values) = received(state, frame, false)?;
    state = next;
    for value in values {
      samples = samples.append(value);
    }
  }
  return Ok((state, samples));
}

function can_recover(control: Control, toc: u8, frames: i32): boolean {
  return frames >= packet.samples_per_frame(toc, control.sample_rate) && mode(toc) != 3 && control.mode != 3;
}

pub function decode_fec(own state: State, input: [u8], frames: i32): Result[(State, f32[]), pcm.AudioError] {
  if (!valid_loss_duration(state.control, frames)) {
    return Err(pcm.Header);
  }
  if (input.len() == 0) {
    return decode_lost(state, frames);
  }
  let parsed = parse(input)?;
  let size = packet.samples_per_frame(parsed.toc, state.control.sample_rate);
  if (!can_recover(state.control, parsed.toc, frames)) {
    return decode_lost(state, frames);
  }
  let samples: f32[] = [];
  if (frames > size) {
    let (next, values) = decode_lost(state, frames - size)?;
    state = next;
    samples = values;
  }
  state = configure(state, parsed.toc);
  if (parsed.frames[0].len() > 1) {
    state = prepare_lowpass(state)?;
  }
  let (next, values) = received(state, parsed.frames[0], true)?;
  return Ok((next, append(samples, values)));
}
```
