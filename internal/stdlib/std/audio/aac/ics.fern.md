# AAC individual channel streams

This module reads AAC-LC spectral data, scalefactors, pulse data, and temporal noise shaping.
It supports long and grouped short windows, all spectral codebooks, and escape values.
Noise and intensity bands retain their explicit band metadata for channel-pair processing.

This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's AAC individual channel stream implementation.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// Previous author: Kostya Shishkov <kostya.shiskov@gmail.com>.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/aac/bits";
import "std/audio/aac/dsp";
import "std/audio/aac/tables";
import "std/audio/aac/tns";
import "std/audio/pcm";
import "std/f64";

pub struct Info { sequence: i32, shape: i32, max_sfb: i32, groups: i32[] }

pub struct Channel { info: Info, books: i32[], scales: f32[], coefficients: f32[], filters: tns.Filter[] }

struct Pulse { start: i32, offsets: i32[], amplitudes: i32[] }

struct Scales { normal: i32, intensity: i32, noise: i32, first_noise: boolean }

// Validate the public window description before any band indexing.
// fern-lint: allow cyclomatic-complexity
pub function valid_info(i: Info, rate: i32): boolean {
  if (rate < 0 || rate > 12 || i.sequence < 0 || i.sequence > 3 || i.shape < 0 || i.shape > 1 || i.groups.len() < 2 || i.groups.len() > 9) {
    return false;
  }
  let bands = tables.bands(rate, i.sequence == 2);
  if (i.max_sfb < 0 || i.max_sfb >= bands.count || i.groups[0] != 0 || i.groups[i.groups.len() - 1] != (if (i.sequence == 2) { 8 } else { 1 })) {
    return false;
  }
  for group in 1..i.groups.len() {
    if (i.groups[group] <= i.groups[group - 1]) {
      return false;
    }
  }
  return true;
}

pub function info(input: [u8], reader: bits.Reader, rate: i32): Result[(bits.Reader, Info), pcm.AudioError] {
  if (rate < 0 || rate >= 13) {
    return Err(pcm.Header);
  }
  let (b1, reserved) = bits.read(input, reader, 1)?;
  if (reserved != 0u32) {
    return Err(pcm.Header);
  }
  let (b2, sequence) = bits.read(input, b1, 2)?;
  let (b3, shape) = bits.read(input, b2, 1)?;
  let short = sequence == 2u32;
  let (b4, maximum) = bits.read(input, b3, if (short) { 4 } else { 6 })?;
  reader = b4;
  let bands = tables.bands(rate, short);
  if (maximum >= bands.count as u32) {
    return Err(pcm.Header);
  }
  let groups: i32[] = [0];
  if (short) {
    for i in 1..8 {
      let (next, grouped) = bits.read(input, reader, 1)?;
      reader = next;
      if (grouped == 0u32) {
        groups = groups.append(i);
      }
    }
    groups = groups.append(8);
  } else {
    let (next, prediction) = bits.read(input, reader, 1)?;
    reader = next;
    if (prediction != 0u32) {
      return Err(pcm.Header);
    }
    groups = groups.append(1);
  }
  return Ok((reader, Info { sequence: sequence as i32, shape: shape as i32, max_sfb: maximum as i32, groups: groups }));
}

function section_length(input: [u8], reader: bits.Reader, width: i32, maximum: i32): Result[(bits.Reader, i32), pcm.AudioError] {
  let escape = (1 << width) - 1;
  let count = 0;
  while (true) {
    let (next, value) = bits.read(input, reader, width)?;
    reader = next;
    count += value as i32;
    if (count > maximum) {
      return Err(pcm.Header);
    }
    if (value as i32 != escape) {
      break;
    }
  }
  if (count == 0) {
    return Err(pcm.Header);
  }
  return Ok((reader, count));
}

function sections(input: [u8], reader: bits.Reader, i: Info): Result[(bits.Reader, i32[]), pcm.AudioError] {
  let books: i32[] = [];
  for _ in 0..i.groups.len() - 1 {
    let band = 0;
    while (band < i.max_sfb) {
      let (b1, book) = bits.read(input, reader, 4)?;
      if (book == 12u32) {
        return Err(pcm.Header);
      }
      let (b2, count) = section_length(input, b1, if (i.sequence == 2) { 3 } else { 5 }, i.max_sfb - band)?;
      reader = b2;
      for _ in 0..count {
        books = books.append(book as i32);
      }
      band += count;
    }
  }
  return Ok((reader, books));
}

function scale(input: [u8], reader: bits.Reader, book: i32, state: Scales): Result[(bits.Reader, Scales, f32), pcm.AudioError] {
  if (book == 0) {
    return Ok((reader, state, 0.0f32));
  }
  let delta = 0;
  if (book == 13 && state.first_noise) {
    let (next, value) = bits.read(input, reader, 9)?;
    reader = next;
    delta = value as i32 - 256;
  } else {
    let (next, value) = bits.huffman(input, reader, 0)?;
    reader = next;
    delta = value - 60;
  }
  if (book == 14 || book == 15) {
    let value = state.intensity + delta;
    if (value < 0 || value > 255) {
      return Err(pcm.Header);
    }
    return Ok((reader, Scales { ...state, intensity: value }, tables.INTENSITY_SCF[value]));
  }
  if (book == 13) {
    let value = state.noise + delta;
    if (value < 0 || value > 255) {
      return Err(pcm.Header);
    }
    return Ok((reader, Scales { ...state, noise: value, first_noise: false }, tables.NORMAL_SCF[value]));
  }
  let value = state.normal + delta;
  if (value < 0 || value > 255) {
    return Err(pcm.Header);
  }
  return Ok((reader, Scales { ...state, normal: value }, tables.NORMAL_SCF[value]));
}

function scales(input: [u8], reader: bits.Reader, books: i32[], gain: i32): Result[(bits.Reader, f32[]), pcm.AudioError] {
  let state = Scales { normal: gain, intensity: 155, noise: gain + 10, first_noise: true };
  let values: f32[] = [];
  for book in books {
    let (next, current, value) = scale(input, reader, book, state)?;
    reader = next;
    state = current;
    values = values.append(value);
  }
  return Ok((reader, values));
}

function pulse(input: [u8], reader: bits.Reader, i: Info): Result[(bits.Reader, Option[Pulse]), pcm.AudioError] {
  let (b1, present) = bits.read(input, reader, 1)?;
  if (present == 0u32) {
    return Ok((b1, None));
  }
  if (i.sequence == 2) {
    return Err(pcm.Header);
  }
  let (b2, count) = bits.read(input, b1, 2)?;
  let (b3, start) = bits.read(input, b2, 6)?;
  if (start >= i.max_sfb as u32) {
    return Err(pcm.Header);
  }
  reader = b3;
  let offsets: i32[] = [];
  let amplitudes: i32[] = [];
  for _ in 0..count as i32 + 1 {
    let (b4, offset) = bits.read(input, reader, 5)?;
    let (b5, amplitude) = bits.read(input, b4, 4)?;
    reader = b5;
    offsets = offsets.append(offset as i32);
    amplitudes = amplitudes.append(amplitude as i32);
  }
  return Ok((reader, Some(Pulse { start: start as i32, offsets: offsets, amplitudes: amplitudes })));
}

function escaped(input: [u8], reader: bits.Reader): Result[(bits.Reader, i32), pcm.AudioError] {
  let width = 4;
  while (true) {
    let (next, value) = bits.read(input, reader, 1)?;
    reader = next;
    if (value == 0u32) {
      break;
    }
    width += 1;
    if (width > 12) {
      return Err(pcm.Header);
    }
  }
  let (end, value) = bits.read(input, reader, width)?;
  return Ok((end, (1 << width) + value as i32));
}

function pair(input: [u8], reader: bits.Reader, book: i32, symbol: i32): Result[(bits.Reader, i32, i32), pcm.AudioError] {
  let radix = if (book < 7) { 9 } else if (book < 9) { 8 } else if (book < 11) { 13 } else { 17 };
  let a = symbol / radix;
  let b = symbol % radix;
  if (book < 7) {
    return Ok((reader, a - 4, b - 4));
  }
  let sign_a = 1;
  let sign_b = 1;
  if (a != 0) {
    let (next, value) = bits.read(input, reader, 1)?;
    reader = next;
    sign_a -= 2 * (value as i32);
  }
  if (b != 0) {
    let (next, value) = bits.read(input, reader, 1)?;
    reader = next;
    sign_b -= 2 * (value as i32);
  }
  if (book == 11) {
    if (a == 16) {
      let (next, value) = escaped(input, reader)?;
      reader = next;
      a = value;
    }
    if (b == 16) {
      let (next, value) = escaped(input, reader)?;
      reader = next;
      b = value;
    }
  }
  return Ok((reader, sign_a * a, sign_b * b));
}

function quad(input: [u8], reader: bits.Reader, book: i32, symbol: i32, own values: i32[], at: i32): Result[(bits.Reader, i32[]), pcm.AudioError] {
  let divisor = 27;
  for j in 0..4 {
    let value = symbol / divisor % 3;
    if (book < 3) {
      value -= 1;
    } else if (value != 0) {
      let (next, sign) = bits.read(input, reader, 1)?;
      reader = next;
      if (sign != 0u32) {
        value = -value;
      }
    }
    values = values.with(at + j, value);
    divisor /= 3;
  }
  return Ok((reader, values));
}

function spectral_band(input: [u8], reader: bits.Reader, book: i32, own values: i32[], start: i32, end: i32): Result[(bits.Reader, i32[]), pcm.AudioError] {
  let at = start;
  while (at < end) {
    let (b1, symbol) = bits.huffman(input, reader, book)?;
    reader = b1;
    if (book < 5) {
      let (b2, output) = quad(input, reader, book, symbol, values, at)?;
      reader = b2;
      values = output;
      at += 4;
    } else {
      let (b2, a, b) = pair(input, reader, book, symbol)?;
      reader = b2;
      values = values.with(at, a).with(at + 1, b);
      at += 2;
    }
  }
  return Ok((reader, values));
}

function spectrum(input: [u8], reader: bits.Reader, i: Info, books: i32[], rate: i32): Result[(bits.Reader, i32[]), pcm.AudioError] {
  let values = pcm.zeros(1024);
  let bands = tables.bands(rate, i.sequence == 2);
  for group in 0..i.groups.len() - 1 {
    for band in 0..i.max_sfb {
      let book = books[group * i.max_sfb + band];
      if (book > 0 && book < 12) {
        for window in i.groups[group]..i.groups[group + 1] {
          let start = window * 128 + tables.BANDS[bands.start + band];
          let end = window * 128 + tables.BANDS[bands.start + band + 1];
          let (next, output) = spectral_band(input, reader, book, values, start, end)?;
          reader = next;
          values = output;
        }
      }
    }
  }
  return Ok((reader, values));
}

function apply_pulse(own values: i32[], p: Option[Pulse], i: Info, books: i32[], rate: i32): Result[i32[], pcm.AudioError] {
  let Some(pulse) = p else {
    return Ok(values);
  };
  let bands = tables.bands(rate, false);
  let at = tables.BANDS[bands.start + pulse.start];
  let band = pulse.start;
  for n in 0..pulse.offsets.len() {
    at += pulse.offsets[n];
    if (at >= 1024) {
      return Err(pcm.Header);
    }
    while (band + 1 < bands.count && tables.BANDS[bands.start + band + 1] <= at) {
      band += 1;
    }
    if (band >= i.max_sfb || books[band] >= 12) {
      return Err(pcm.Header);
    }
    let value = values[at] + (if (values[at] > 0) { pulse.amplitudes[n] } else { -pulse.amplitudes[n] });
    if (value < -8191 || value > 8191) {
      return Err(pcm.Header);
    }
    values = values.with(at, value);
  }
  return Ok(values);
}

function noise(own values: f32[], start: i32, end: i32, scale: f32, seed: u32): (f32[], u32) {
  let energy = 0.0f32;
  for at in start..end {
    seed = seed * 1664525u32 + 1013904223u32;
    let value = (seed as i32) as f32;
    values = values.with(at, value);
    energy += value * value;
  }
  let gain = scale / ((energy as f64).sqrt() as f32);
  for at in start..end {
    values = values.with(at, values[at] * gain);
  }
  return (values, seed);
}

function correlated(other: Option[Channel], mask: [i32], band: i32): boolean {
  if (band >= mask.len() || mask[band] == 0) {
    return false;
  }
  return match (other) { Some(left) => band < left.books.len() && left.books[band] == 13, None => false };
}

function copied_noise(own values: f32[], other: Option[Channel], band: i32, scale: f32, start: i32, end: i32): Result[f32[], pcm.AudioError] {
  let Some(left) = other else {
    return Err(pcm.Header);
  };
  let ratio = scale / left.scales[band];
  for at in start..end {
    values = values.with(at, left.coefficients[at] * ratio);
  }
  return Ok(values);
}

function inverse_band(own values: f32[], quants: [i32], start: i32, end: i32, scale: f32): f32[] {
  for at in start..end {
    let q = quants[at];
    let absolute = if (q < 0) { -q } else { q };
    values = values.with(at, (if (q < 0) { -tables.POW43[absolute] } else { tables.POW43[absolute] }) * scale);
  }
  return values;
}

function dequantize(quants: [i32], i: Info, books: i32[], scales: f32[], rate: i32, seed: u32, other: Option[Channel], mask: [i32]): Result[(f32[], u32), pcm.AudioError] {
  let values = dsp.zeros(1024);
  let bands = tables.bands(rate, i.sequence == 2);
  for group in 0..i.groups.len() - 1 {
    for sfb in 0..i.max_sfb {
      let band = group * i.max_sfb + sfb;
      let book = books[band];
      for window in i.groups[group]..i.groups[group + 1] {
        let start = window * 128 + tables.BANDS[bands.start + sfb];
        let end = window * 128 + tables.BANDS[bands.start + sfb + 1];
        if (book == 13) {
          if (correlated(other, mask, band)) {
            values = copied_noise(values, other, band, scales[band], start, end)?;
          } else {
            let (next, state) = noise(values, start, end, scales[band], seed);
            values = next;
            seed = state;
          }
        } else if (book > 0 && book < 12) {
          values = inverse_band(values, quants, start, end, scales[band]);
        }
      }
    }
  }
  return Ok((values, seed));
}

// Read the ordered ICS syntax and retain the cursor at each checked step.
// fern-lint: allow cyclomatic-complexity
pub function decode(input: [u8], reader: bits.Reader, common: Option[Info], rate: i32, seed: u32, other: Option[Channel], mask: [i32]): Result[(bits.Reader, Channel, u32), pcm.AudioError] {
  let (b1, gain) = bits.read(input, reader, 8)?;
  reader = b1;
  let i = match (common) {
    Some(value) => value,
    None => { let (b2, value) = info(input, reader, rate)?; reader = b2; value },
  };
  if (!valid_info(i, rate)) {
    return Err(pcm.Header);
  }
  let (b3, books) = sections(input, reader, i)?;
  if let None = other {
    for book in books {
      if (book == 14 || book == 15) {
        return Err(pcm.Header);
      }
    }
  }
  let (b4, factors) = scales(input, b3, books, gain as i32)?;
  let (b5, pulses) = pulse(input, b4, i)?;
  let (b6, filters) = tns.read(input, b5, rate, i.sequence == 2)?;
  let (b7, gain_control) = bits.read(input, b6, 1)?;
  if (gain_control != 0u32) {
    return Err(pcm.Header);
  }
  let (end, quantized) = spectrum(input, b7, i, books, rate)?;
  let values = apply_pulse(quantized, pulses, i, books, rate)?;
  let (coefficients, next_seed) = dequantize(values, i, books, factors, rate, seed, other, mask)?;
  return Ok((end, Channel { info: i, books: books, scales: factors, coefficients: coefficients, filters: filters }, next_seed));
}

pub function shape(own channel: Channel, rate: i32): Result[Channel, pcm.AudioError] {
  let Channel { info, books, scales, coefficients, filters } = channel;
  let output = tns.apply(coefficients, filters, rate, info.sequence == 2, info.max_sfb)?;
  return Ok(Channel { info: info, books: books, scales: scales, coefficients: output, filters: filters });
}
```
