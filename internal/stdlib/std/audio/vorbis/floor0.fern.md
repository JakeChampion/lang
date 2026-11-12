# Vorbis type-zero floors

This module reads line spectral pairs and reconstructs Vorbis type-zero floors.
Setup prepares both Bark maps once. Amplitude arithmetic uses binary64 to preserve
the full 63-bit input range without integer overflow.

Products use binary64 to bound intermediate values for all 255 coefficients.
The decoder rejects nonfinite output before it reaches the inverse transform.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis floor module.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/bits";
import "std/audio/vorbis/codebook";
import "std/audio/vorbis/types";
import "std/audio/pcm";
import "std/f32";
import "std/f64";

pub struct Setup { order: i32, amplitude_bits: i32, amplitude_offset: i32, map_size: i32, small_exponent: i32, large_exponent: i32, small: i32[], large: i32[], books: i32[] }

pub struct Data { amplitude: u64, coefficients: f32[] }

function bark(x: f64): f64 {
  return 13.1 * (0.00074 * x).atan() + 2.24 * (0.0000000185 * x * x).atan() + 0.0001 * x;
}

function map(exponent: i32, rate: i32, size: i32): i32[] {
  let n = 1 << exponent - 1;
  let scale = size as f64 / bark(rate as f64 * 0.5);
  let step = rate as f64 / (2.0 * (n as f64));
  let values: i32[] = [];
  for i in 0..n {
    let value = (bark(step * (i as f64)) * scale) as i32;
    values = values.append(if (value < size) { value } else { size - 1 });
  }
  return values;
}

// Validate all wire fields and vector-book references before Bark-map allocation.
// fern-lint: allow cyclomatic-complexity
pub function setup(input: [u8], reader: bits.Reader, small: i32, large: i32, books: [codebook.Book]): Result[(bits.Reader, Setup), pcm.AudioError] {
  if (small < 6 || large < small || large > 13 || books.len() < 1 || books.len() > 256) {
    return Err(pcm.Header);
  }
  let (b1, order) = bits.read(input, reader, 8)?;
  let (b2, rate) = bits.read(input, b1, 16)?;
  let (b3, size) = bits.read(input, b2, 16)?;
  let (b4, amplitude_bits) = bits.read(input, b3, 6)?;
  let (b5, offset) = bits.read(input, b4, 8)?;
  let (b6, count) = bits.read(input, b5, 4)?;
  if (order == 0u32 || rate == 0u32 || size == 0u32) {
    return Err(pcm.Header);
  }
  reader = b6;
  let list: i32[] = [];
  for _ in 0..count as i32 + 1 {
    let (next, index) = bits.read(input, reader, 8)?;
    reader = next;
    if (index >= books.len() as u32) {
      return Err(pcm.Header);
    }
    let book = books[index as i32];
    if (book.lookup == 0 || book.dimensions < 1 || book.vectors.len() as i64 != book.entries as i64 * (book.dimensions as i64)) {
      return Err(pcm.Header);
    }
    list = list.append(index as i32);
  }
  return Ok((reader, Setup { order: order as i32, amplitude_bits: amplitude_bits as i32, amplitude_offset: offset as i32, map_size: size as i32, small_exponent: small, large_exponent: large, small: map(small, rate as i32, size as i32), large: map(large, rate as i32, size as i32), books: list }));
}

// Keep amplitude, LSP vector bounds, and finite accumulation checks in packet order.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], reader: bits.Reader, setup: Setup, books: [codebook.Book]): Result[(bits.Reader, Data), pcm.AudioError] {
  if (setup.order < 1 || setup.order > 255 || setup.amplitude_bits < 0 || setup.amplitude_bits > 63 || setup.books.len() < 1 || setup.books.len() > 16) {
    return Err(pcm.Header);
  }
  let (b1, amplitude) = bits.wide(input, reader, setup.amplitude_bits)?;
  if (amplitude == 0u64) {
    return Ok((b1, Data { amplitude: 0u64, coefficients: [] }));
  }
  let (b2, selector) = bits.read(input, b1, types.ilog(setup.books.len()))?;
  if (selector >= setup.books.len() as u32) {
    return Err(pcm.Header);
  }
  let index = setup.books[selector as i32];
  if (index < 0 || index >= books.len()) {
    return Err(pcm.Header);
  }
  let book = books[index];
  if (book.lookup == 0 || book.dimensions < 1 || book.vectors.len() as i64 != book.entries as i64 * (book.dimensions as i64)) {
    return Err(pcm.Header);
  }
  reader = b2;
  let coefficients: f32[] = [];
  let last = 0.0f32;
  while (coefficients.len() < setup.order) {
    let (next, entry) = codebook.scalar(input, reader, book)?;
    reader = next;
    if (entry < 0 || entry >= book.entries) {
      return Err(pcm.Header);
    }
    let remaining = setup.order - coefficients.len();
    let count = if (remaining < book.dimensions) { remaining } else { book.dimensions };
    for i in 0..count {
      let value = last + book.vectors[entry * book.dimensions + i];
      if (!value.is_finite()) {
        return Err(pcm.Header);
      }
      coefficients = coefficients.append(value);
    }
    last = coefficients[coefficients.len() - 1];
  }
  for i in 0..coefficients.len() {
    coefficients = coefficients.with(i, 2.0f32 * coefficients[i].cos());
  }
  return Ok((reader, Data { amplitude: amplitude, coefficients: coefficients }));
}

function gain(coefficients: [f32], omega: f64, amplitude: f64, offset: i32): Result[f32, pcm.AudioError] {
  let cosine = omega.cos();
  let twice = 2.0 * cosine;
  let p = 1.0;
  let q = 1.0;
  let pairs = coefficients.len() / 2;
  for i in 0..pairs {
    p *= coefficients[2 * i + 1] as f64 - twice;
    q *= coefficients[2 * i] as f64 - twice;
  }
  if ((coefficients.len() & 1) != 0) {
    q *= coefficients[coefficients.len() - 1] as f64 - twice;
    p = p * p * (1.0 - cosine * cosine);
    q = q * q * 0.25;
  } else {
    p = p * p * (1.0 - cosine) * 0.5;
    q = q * q * (1.0 + cosine) * 0.5;
  }
  if (p + q <= 0.0 || !(p + q).is_finite()) {
    return Err(pcm.Header);
  }
  let value = (0.11512925 * (amplitude * (offset as f64) / (p + q).sqrt() - offset as f64)).exp() as f32;
  if (!value.is_finite()) {
    return Err(pcm.Sample);
  }
  return Ok(value);
}

// Validate both Bark-map selection and full-width amplitude scaling before synthesis.
// fern-lint: allow cyclomatic-complexity
pub function synth(data: Data, setup: Setup, exponent: i32, own output: f32[]): Result[f32[], pcm.AudioError] {
  if (exponent < 6 || exponent > 13 || exponent != setup.small_exponent && exponent != setup.large_exponent) {
    return Err(pcm.Header);
  }
  let n = 1 << exponent - 1;
  if (output.len() < n) {
    return Err(pcm.Buffer);
  }
  if (data.amplitude == 0u64) {
    for i in 0..n {
      output = output.with(i, 0.0f32);
    }
    return Ok(output);
  }
  if (data.coefficients.len() != setup.order || setup.order < 1 || setup.order > 255 || setup.map_size < 1 || setup.amplitude_bits < 1 || setup.amplitude_bits > 63 || setup.amplitude_offset < 0 || setup.amplitude_offset > 255) {
    return Err(pcm.Header);
  }
  let map = if (exponent == setup.small_exponent) { setup.small } else { setup.large };
  if (map.len() != n) {
    return Err(pcm.Header);
  }
  let maximum = (1u64 << setup.amplitude_bits as u64) - 1u64;
  if (data.amplitude > maximum) {
    return Err(pcm.Header);
  }
  let amplitude = data.amplitude as f64 / (maximum as f64);
  let i = 0;
  while (i < n) {
    let current = map[i];
    if (current < 0 || current >= setup.map_size) {
      return Err(pcm.Header);
    }
    let value = gain(data.coefficients, 3.141592653589793 * (current as f64) / (setup.map_size as f64), amplitude, setup.amplitude_offset)?;
    while (i < n && map[i] == current) {
      output = output.with(i, value);
      i += 1;
    }
  }
  return Ok(output);
}
```
