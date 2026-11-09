# WavPack decorrelation

Each block supplies independent prediction terms, weights, and sample history.
This module restores mono and stereo samples in the caller array.
It supports delay terms 1 through 8, terms 17 and 18, and the three stereo cross terms.

```fern
<<*>>=
// Derived from WavPack 5.8.1, decorr_utils.c and unpack.c.
// Copyright (c) 1998 - 2025 David Bryant.
// Copyright (c) 1998 - 2013 Conifer Software.
// BSD-3-Clause. See licenses/wavpack/BSD-3-Clause.txt.
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/wavpack/entropy";

pub opaque struct Pass { term: i32, delta: i32, wa: i32, wb: i32, history: i32[] }

function weight(value: u8): i32 {
  let signed = if (value >= 128u8) { value as i32 - 256 } else { value as i32 };
  let restored = signed * 8;
  return if (restored > 0) { restored + (restored + 64 >> 7) } else { restored };
}

function signed_log(input: [u8], at: i32): Result[i32, pcm.AudioError] {
  let raw = wire.le(input, at, 2) as i32;
  let value = if (raw >= 32768) { raw - 65536 } else { raw };
  if (value < -8192 || value > 8192) {
    return Err(pcm.Header);
  }
  return Ok(entropy.expand(value));
}

function valid_term(term: i32, channels: i32): boolean {
  return term != 0 && term >= -3 && (term <= 8 || term >= 17) && term <= 18 && (channels != 1 || term > 0);
}

pub function terms(input: [u8], channels: i32): Result[Pass[], pcm.AudioError] {
  if (input.len() > 16 || channels != 1 && channels != 2) {
    return Err(pcm.Header);
  }
  let out: Pass[] = [];
  for i in 0..input.len() {
    let value = input[input.len() - 1 - i] as i32;
    let term = (value & 31) - 5;
    if (!valid_term(term, channels)) {
      return Err(pcm.Header);
    }
    out = out.append(Pass { term: term, delta: value >> 5, wa: 0, wb: 0, history: pcm.zeros(16) });
  }
  return Ok(out);
}

pub function weights(input: [u8], channels: i32, own passes: Pass[]): Result[Pass[], pcm.AudioError] {
  if (input.len() % channels != 0 || input.len() / channels > passes.len()) {
    return Err(pcm.Header);
  }
  for i in 0..input.len() / channels {
    let index = passes.len() - 1 - i;
    let pass = passes[index];
    passes = passes.with(index, Pass { ...pass, wa: weight(input[i * channels]), wb: if (channels == 2) { weight(input[i * channels + 1]) } else { 0 } });
  }
  return Ok(passes);
}

function history_slot(term: i32, channels: i32, index: i32): i32 {
  if (term > 8) {
    return index / 2 * 8 + index % 2;
  }
  if (term < 0) {
    return index * 8;
  }
  return index % channels * 8 + index / channels;
}

function history(input: [u8], at: i32, channels: i32, pass: Pass): Result[(i32, Pass), pcm.AudioError] {
  let count = if (pass.term > 8) { channels * 2 } else { if (pass.term < 0) { 2 } else { channels * pass.term } };
  if (at < 0 || count * 2 > input.len() - at) {
    return Err(pcm.Header);
  }
  let saved = pass.history;
  for i in 0..count {
    saved = saved.with(history_slot(pass.term, channels, i), signed_log(input, at + i * 2)?);
  }
  return Ok((at + count * 2, Pass { ...pass, history: saved }));
}

pub function samples(input: [u8], channels: i32, own passes: Pass[]): Result[Pass[], pcm.AudioError] {
  let at = 0;
  let i = passes.len();
  while (i > 0 && at < input.len()) {
    i -= 1;
    let (next, pass) = history(input, at, channels, passes[i])?;
    at = next;
    passes = passes.with(i, pass);
  }
  if (at != input.len()) {
    return Err(pcm.Header);
  }
  return Ok(passes);
}

function apply(weight: i32, source: i32): i32 {
  // The reference split-word path wraps its i32 intermediates before rounding.
  // A signed 16-bit weight keeps the low-word product in range. The cast
  // preserves the reference wrap before the final rounding and shift.
  if (weight == (weight << 16 >> 16)) {
    let scaled = (weight as i64 * (source as i64) >> 9i64) as i32;
    return scaled + 1 >> 1;
  }
  if (source == (source << 16 >> 16)) {
    return weight * source + 512 >> 10;
  }
  let lower = (source & 65535) * weight >> 9;
  let upper = ((source & -65536) >> 9) * weight;
  return lower + upper + 1 >> 1;
}

function update(weight: i32, delta: i32, source: i32, residual: i32, clipped: boolean): i32 {
  let result = weight;
  if (source != 0 && residual != 0) {
    result += if ((source ^ residual) < 0) { -delta } else { delta };
    if (clipped) {
      if (result > 1024) {
        result = 1024;
      }
      if (result < -1024) {
        result = -1024;
      }
    }
  }
  return result;
}

function positive_mono(own output: i32[], frames: i32, pass: Pass): i32[] {
  let shift = if (pass.term == 17) { 0 } else { 1 };
  let factor = 2 + shift;
  let a = pass.history[0];
  let b = pass.history[1];
  let weight = pass.wa;
  for i in 0..frames {
    let source = factor * a - b >> shift;
    let residual = output[i];
    let value = residual + apply(weight, source);
    weight = update(weight, pass.delta, source, residual, false);
    b = a;
    a = value;
    output = output.with(i, value);
  }
  return output;
}

function positive_stereo(own output: i32[], frames: i32, pass: Pass): i32[] {
  let shift = if (pass.term == 17) { 0 } else { 1 };
  let a0 = pass.history[0];
  let a1 = pass.history[1];
  let b0 = pass.history[8];
  let b1 = pass.history[9];
  let wa = pass.wa;
  let wb = pass.wb;
  for i in 0..frames {
    let sa = a0 + (a0 - a1 >> shift);
    let sb = b0 + (b0 - b1 >> shift);
    let left = output[i * 2];
    let right = output[i * 2 + 1];
    let a = left + apply(wa, sa);
    let b = right + apply(wb, sb);
    wa = update(wa, pass.delta, sa, left, false);
    wb = update(wb, pass.delta, sb, right, false);
    a1 = a0;
    a0 = a;
    b1 = b0;
    b0 = b;
    output = output.with(i * 2, a).with(i * 2 + 1, b);
  }
  return output;
}

function positive_delay(own output: i32[], frames: i32, channels: i32, pass: Pass): i32[] {
  let saved = pass.history;
  let wa = pass.wa;
  let wb = pass.wb;
  let read_at = 0;
  let write_at = pass.term & 7;
  for i in 0..frames {
    for channel in 0..channels {
      let base = channel * 8;
      let source = saved[base + read_at];
      let residual = output[i * channels + channel];
      let w = if (channel == 0) { wa } else { wb };
      let value = residual + apply(w, source);
      let changed = update(w, pass.delta, source, residual, false);
      if (channel == 0) {
        wa = changed;
      } else {
        wb = changed;
      }
      saved = saved.with(base + write_at, value);
      output = output.with(i * channels + channel, value);
    }
    read_at = read_at + 1 & 7;
    write_at = write_at + 1 & 7;
  }
  return output;
}

function positive(own output: i32[], frames: i32, channels: i32, pass: Pass): i32[] {
  if (pass.term <= 8) {
    return positive_delay(output, frames, channels, pass);
  }
  return if (channels == 1) { positive_mono(output, frames, pass) } else { positive_stereo(output, frames, pass) };
}

function negative(own output: i32[], frames: i32, pass: Pass): i32[] {
  let a = pass.history[0];
  let b = pass.history[8];
  let wa = pass.wa;
  let wb = pass.wb;
  for i in 0..frames {
    let left = output[i * 2];
    let right = output[i * 2 + 1];
    let l = left;
    let r = right;
    if (pass.term == -1) {
      l += apply(wa, a);
      wa = update(wa, pass.delta, a, left, true);
      r += apply(wb, l);
      wb = update(wb, pass.delta, l, right, true);
      a = r;
    } else if (pass.term == -2) {
      r += apply(wb, b);
      wb = update(wb, pass.delta, b, right, true);
      l += apply(wa, r);
      wa = update(wa, pass.delta, r, left, true);
      b = l;
    } else {
      l += apply(wa, a);
      r += apply(wb, b);
      wa = update(wa, pass.delta, a, left, true);
      wb = update(wb, pass.delta, b, right, true);
      a = r;
      b = l;
    }
    output = output.with(i * 2, l).with(i * 2 + 1, r);
  }
  return output;
}

pub function restore(own output: i32[], frames: i32, channels: i32, passes: Pass[]): Result[i32[], pcm.AudioError] {
  if (channels != 1 && channels != 2 || frames < 0 || frames as i64 * (channels as i64) > output.len() as i64) {
    return Err(pcm.Buffer);
  }
  for pass in passes {
    output = if (pass.term > 0) { positive(output, frames, channels, pass) } else { negative(output, frames, pass) };
  }
  return Ok(output);
}
```
