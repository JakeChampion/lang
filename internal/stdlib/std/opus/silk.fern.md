# SILK internal-rate packets

This module decodes SILK packets to signed 16-bit PCM at their internal sample rate.
It handles packet activity flags, redundant frames, multiple internal frames, and stereo prediction.
The output contains interleaved samples. One frame contains one sample per output channel.
Resampling and the outer Opus mode transitions belong to the Opus packet facade.

The implementation derives from [Pion Opus](https://github.com/pion/opus/tree/e7b8aa55508cfd30a968dbc695d4272a8e04f2f2) and RFC 6716.
Copyright 2026 The Pion community. Copyright 2006–2011 Skype Limited.
The source uses the MIT and BSD-3-Clause licenses in `licenses/opus/`.

```fern
<<*>>=
// SILK packet decoding at the internal 8, 12, or 16 kHz sample rate.
// Copyright 2026 The Pion community. Copyright 2006-2011 Skype Limited.
// SPDX-License-Identifier: MIT AND BSD-3-Clause

import "std/audio/pcm";
import "std/opus/range";
import "std/opus/silk/core";
import "std/opus/silk/entropy";
import "std/opus/silk/stereo";
import "std/opus/silk/tables";

pub struct Channel { history: entropy.History, core: core.State }

pub struct Decoder { mid: Channel, side: Channel, stereo: stereo.State, was_stereo: boolean, only_mid: boolean }

function channel(): Channel {
  return Channel { history: entropy.history(), core: core.state() };
}

pub function decoder(): Decoder {
  return Decoder { mid: channel(), side: channel(), stereo: stereo.state(), was_stereo: false, only_mid: false };
}

function reset(own previous: Channel): Channel {
  return Channel { history: entropy.history(), core: core.reset(previous.core) };
}

function decode_channel(own previous: Channel, own range: range.Decoder, rate: i32, duration: i32, active: boolean, independent: boolean, skip_scale: boolean): Result[(Channel, range.Decoder, i32[]), pcm.AudioError] {
  if (previous.core.rate != 0 && previous.core.rate != rate) {
    previous = Channel { ...previous, history: entropy.history() };
  }
  let (next, history, control) = entropy.decode(range, previous.history, rate, duration, active, independent, skip_scale)?;
  let (state, output) = core.decode(previous.core, control, rate, duration)?;
  return Ok((Channel { history: history, core: state }, next, output));
}

function header(own range: range.Decoder, count: i32): (range.Decoder, boolean[], boolean) {
  let flags: boolean[] = [];
  for _ in 0..count {
    let (next, active) = range.bit_logp(1);
    range = next;
    flags = flags.append(active);
  }
  let (next, redundant) = range.bit_logp(1);
  return (next, flags, redundant);
}

function redundant_flags(own range: range.Decoder, count: i32, present: boolean): (range.Decoder, boolean[]) {
  let bits = 0;
  if (present) {
    bits = 1;
    if (count > 1) {
      let table = if (count == 2) { tables.ICDF_LOW_BITRATE_REDUNDANCY_FLAGS40_MS } else { tables.ICDF_LOW_BITRATE_REDUNDANCY_FLAGS60_MS };
      let (next, symbol) = range.icdf(table, 8);
      range = next;
      bits = symbol + 1;
    }
  }
  let flags: boolean[] = [];
  for i in 0..count {
    flags = flags.append((bits & 1 << i) != 0);
  }
  return (range, flags);
}

function discard_weights(own range: range.Decoder, mid_coded: boolean, side_coded: boolean, is_stereo: boolean): range.Decoder {
  if (!mid_coded || !is_stereo) {
    return range;
  }
  let (a, _, _) = stereo.weights(range);
  if (side_coded) {
    return a;
  }
  let (b, _) = a.icdf(tables.ICDF_STEREO_MID_ONLY, 8);
  return b;
}

function discard_redundancy(own range: range.Decoder, mid: [boolean], side: [boolean], rate: i32, duration: i32, is_stereo: boolean): Result[range.Decoder, pcm.AudioError] {
  let mid_history = entropy.history();
  let side_history = entropy.history();
  let previous_mid = false;
  let previous_side = false;
  for i in 0..mid.len() {
    let side_coded = is_stereo && side[i];
    range = discard_weights(range, mid[i], side_coded, is_stereo);
    if (mid[i]) {
      let (a, history, _) = entropy.decode(range, mid_history, rate, duration, true, i == 0 || !previous_mid, false)?;
      range = a;
      mid_history = history;
    }
    if (side_coded) {
      let (a, history, _) = entropy.decode(range, side_history, rate, duration, true, i == 0 || !previous_side, false)?;
      range = a;
      side_history = history;
    }
    previous_mid = mid[i];
    previous_side = side_coded;
  }
  return Ok(range);
}

function mono(own previous: Decoder, own range: range.Decoder, activity: [boolean], rate: i32, duration: i32, channels: i32): Result[(Decoder, range.Decoder, i32[]), pcm.AudioError] {
  let mid = previous.mid;
  let stereo_state = previous.stereo;
  let output: i32[] = [];
  for i in 0..activity.len() {
    let (next_mid, next_range, samples) = decode_channel(mid, range, rate, duration, activity[i], i == 0, false)?;
    mid = next_mid;
    range = next_range;
    let (next_stereo, delayed) = stereo.mono(stereo_state, samples, rate * duration, channels);
    stereo_state = next_stereo;
    for sample in delayed {
      output = output.append(sample);
    }
  }
  return Ok((Decoder { ...previous, mid: mid, stereo: stereo_state, was_stereo: false }, range, output));
}

function prepare_stereo(own previous: Decoder): Decoder {
  if (previous.was_stereo) {
    return previous;
  }
  let mixed = stereo.State { ...stereo.state(), mid0: previous.stereo.mid0, mid1: previous.stereo.mid1 };
  return Decoder { ...previous, side: channel(), stereo: mixed, only_mid: false };
}

function stereo_frames(own previous: Decoder, own range: range.Decoder, mid_active: [boolean], side_active: [boolean], rate: i32, duration: i32, channels: i32): Result[(Decoder, range.Decoder, i32[]), pcm.AudioError] {
  previous = prepare_stereo(previous);
  let mid = previous.mid;
  let side = previous.side;
  let mixed = previous.stereo;
  let first_side = true;
  let only_mid = previous.only_mid;
  let output: i32[] = [];
  for i in 0..mid_active.len() {
    let (a, prediction0, prediction1) = stereo.weights(range);
    range = a;
    let mid_only = false;
    if (!side_active[i]) {
      let (b, flag) = range.icdf(tables.ICDF_STEREO_MID_ONLY, 8);
      range = b;
      mid_only = flag == 1;
    }
    if (!mid_only && only_mid) {
      side = reset(side);
    }
    let (next_mid, b, mid_samples) = decode_channel(mid, range, rate, duration, mid_active[i], i == 0, false)?;
    mid = next_mid;
    range = b;
    let side_samples = pcm.zeros(rate * duration);
    if (!mid_only) {
      let (next_side, c, samples) = decode_channel(side, range, rate, duration, side_active[i], first_side, only_mid)?;
      side = next_side;
      range = c;
      side_samples = samples;
      first_side = false;
    }
    let (next_mixed, samples) = if (channels == 2) { stereo.unmix(mixed, mid_samples, side_samples, rate * duration, rate, prediction0, prediction1) } else { stereo.mono(mixed, mid_samples, rate * duration, 1) };
    mixed = next_mixed;
    for sample in samples {
      output = output.append(sample);
    }
    only_mid = mid_only;
  }
  return Ok((Decoder { mid: mid, side: side, stereo: mixed, was_stereo: true, only_mid: only_mid }, range, output));
}

function valid_duration(duration: i32): boolean {
  return duration == 10 || duration == 20 || duration == 40 || duration == 60;
}

function valid_channels(stream_channels: i32, output_channels: i32): boolean {
  return stream_channels >= 1 && stream_channels <= 2 && output_channels >= 1 && output_channels <= 2;
}

pub function decode_range(own previous: Decoder, own range: range.Decoder, rate: i32, duration: i32, stream_channels: i32, output_channels: i32): Result[(Decoder, range.Decoder, i32[]), pcm.AudioError] {
  let internal_duration = if (duration > 20) { 20 } else { duration };
  if (!entropy.valid_frame(rate, internal_duration)) {
    return Err(pcm.Header);
  }
  if (!valid_duration(duration)) {
    return Err(pcm.Header);
  }
  if (!valid_channels(stream_channels, output_channels)) {
    return Err(pcm.Header);
  }
  let count = duration / internal_duration;
  let (a, mid_activity, mid_redundant) = header(range, count);
  range = a;
  let side_activity: boolean[] = [];
  let side_redundant = false;
  if (stream_channels == 2) {
    let (b, flags, redundant) = header(range, count);
    range = b;
    side_activity = flags;
    side_redundant = redundant;
  }
  let (b, mid_flags) = redundant_flags(range, count, mid_redundant);
  let (c, side_flags) = redundant_flags(b, count, side_redundant);
  range = discard_redundancy(c, mid_flags, side_flags, rate, internal_duration, stream_channels == 2)?;
  if (stream_channels == 1) {
    return mono(previous, range, mid_activity, rate, internal_duration, output_channels);
  }
  return stereo_frames(previous, range, mid_activity, side_activity, rate, internal_duration, output_channels);
}
```
