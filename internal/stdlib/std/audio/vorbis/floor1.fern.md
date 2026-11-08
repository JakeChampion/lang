# Vorbis type-one floors

This module reads and reconstructs piecewise-linear Vorbis floor curves.
Setup validates codebook references and all implicit and explicit x positions.
Each floor has at most 65 points. Neighbor indices and point order are prepared once.

Synthesis checks the reconstructed amplitude range before table access.
Line rendering clips to the current spectral block.
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
import "std/audio/vorbis/tables";
import "std/audio/pcm";

pub struct Class { dimensions: i32, subclass_bits: i32, master: i32, books: i32[] }

pub struct Setup { partitions: i32[], classes: Class[], multiplier: i32, x: i32[], low: i32[], high: i32[], order: i32[] }

pub struct Data { used: boolean, y: i32[] }

function class(input: [u8], reader: bits.Reader, books: i32): Result[(bits.Reader, Class), pcm.AudioError] {
  let (b1, dimensions) = bits.read(input, reader, 3)?;
  let (b2, subclasses) = bits.read(input, b1, 2)?;
  reader = b2;
  let master = -1;
  if (subclasses != 0u32) {
    let (next, value) = bits.read(input, reader, 8)?;
    reader = next;
    if (value >= books as u32) {
      return Err(pcm.Header);
    }
    master = value as i32;
  }
  let values: i32[] = [];
  for _ in 0..1 << subclasses as i32 {
    let (next, value) = bits.read(input, reader, 8)?;
    reader = next;
    let index = value as i32 - 1;
    if (index >= books) {
      return Err(pcm.Header);
    }
    values = values.append(index);
  }
  return Ok((reader, Class { dimensions: dimensions as i32 + 1, subclass_bits: subclasses as i32, master: master, books: values }));
}

// Keep implicit and explicit point validation beside neighbor and order construction.
// fern-lint: allow cyclomatic-complexity
pub function setup(input: [u8], reader: bits.Reader, books: i32): Result[(bits.Reader, Setup), pcm.AudioError] {
  if (books < 1 || books > 256) {
    return Err(pcm.Header);
  }
  let (b1, count) = bits.read(input, reader, 5)?;
  reader = b1;
  let partitions: i32[] = [];
  let maximum = -1;
  for _ in 0..count as i32 {
    let (next, value) = bits.read(input, reader, 4)?;
    reader = next;
    partitions = partitions.append(value as i32);
    if (value as i32 > maximum) {
      maximum = value as i32;
    }
  }
  let classes: Class[] = [];
  for _ in 0..maximum + 1 {
    let (next, value) = class(input, reader, books)?;
    reader = next;
    classes = classes.append(value);
  }
  let (b2, raw_multiplier) = bits.read(input, reader, 2)?;
  let (b3, range_bits) = bits.read(input, b2, 4)?;
  reader = b3;
  let x: i32[] = [0, 1 << range_bits as i32];
  for partition in partitions {
    let dimensions = classes[partition].dimensions;
    if (dimensions > 65 - x.len()) {
      return Err(pcm.Header);
    }
    for _ in 0..dimensions {
      let (next, value) = bits.read(input, reader, range_bits as i32)?;
      reader = next;
      for prior in x {
        if (prior == value as i32) {
          return Err(pcm.Header);
        }
      }
      x = x.append(value as i32);
    }
  }
  let low: i32[] = [0, 0];
  let high: i32[] = [1, 1];
  for i in 2..x.len() {
    let lo = 0;
    let hi = 1;
    for j in 0..i {
      if (x[j] < x[i] && x[j] > x[lo]) {
        lo = j;
      }
      if (x[j] > x[i] && x[j] < x[hi]) {
        hi = j;
      }
    }
    low = low.append(lo);
    high = high.append(hi);
  }
  let order: i32[] = [];
  for i in 0..x.len() {
    order = order.append(i);
  }
  for i in 1..order.len() {
    let j = i;
    while (j > 0 && x[order[j]] < x[order[j - 1]]) {
      let previous = order[j - 1];
      order = order.with(j - 1, order[j]).with(j, previous);
      j -= 1;
    }
  }
  return Ok((reader, Setup { partitions: partitions, classes: classes, multiplier: raw_multiplier as i32 + 1, x: x, low: low, high: high, order: order }));
}

function range(multiplier: i32): i32 {
  return match (multiplier) { 1 => 256, 2 => 128, 3 => 86, 4 => 64, _ => 0 };
}

// Keep subclass decoding beside range and point-count checks.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], reader: bits.Reader, setup: Setup, books: [codebook.Book]): Result[(bits.Reader, Data), pcm.AudioError] {
  let count = range(setup.multiplier);
  if (count == 0) {
    return Err(pcm.Header);
  }
  let (b1, used) = bits.read(input, reader, 1)?;
  if (used == 0u32) {
    return Ok((b1, Data { used: false, y: [] }));
  }
  let width = types.ilog(count - 1);
  let (b2, first) = bits.read(input, b1, width)?;
  let (b3, second) = bits.read(input, b2, width)?;
  if (first >= count as u32 || second >= count as u32) {
    return Err(pcm.Header);
  }
  reader = b3;
  let y: i32[] = [first as i32, second as i32];
  for partition in setup.partitions {
    if (partition < 0 || partition >= setup.classes.len()) {
      return Err(pcm.Header);
    }
    let value = setup.classes[partition];
    if (value.subclass_bits < 0 || value.subclass_bits > 3 || value.dimensions < 1 || value.dimensions > 8 || value.books.len() != 1 << value.subclass_bits) {
      return Err(pcm.Header);
    }
    let selector = 0;
    if (value.subclass_bits != 0) {
      if (value.master < 0 || value.master >= books.len()) {
        return Err(pcm.Header);
      }
      let (next, symbol) = codebook.scalar(input, reader, books[value.master])?;
      reader = next;
      selector = symbol;
    }
    for _ in 0..value.dimensions {
      let book = value.books[selector & (1 << value.subclass_bits) - 1];
      selector >>= value.subclass_bits;
      let symbol = 0;
      if (book >= 0) {
        if (book >= books.len()) {
          return Err(pcm.Header);
        }
        let (next, current) = codebook.scalar(input, reader, books[book])?;
        reader = next;
        symbol = current;
      }
      if (y.len() >= 65) {
        return Err(pcm.Header);
      }
      y = y.append(symbol);
    }
  }
  if (y.len() != setup.x.len()) {
    return Err(pcm.Header);
  }
  return Ok((reader, Data { used: true, y: y }));
}

function predicted(x0: i32, y0: i32, x1: i32, y1: i32, x: i32): i32 {
  let dy = y1 - y0;
  let absolute = if (dy < 0) { -dy } else { dy };
  let offset = absolute * (x - x0) / (x1 - x0);
  return if (dy < 0) { y0 - offset } else { y0 + offset };
}

// Keep the specification unfolding cases beside neighbor and amplitude bounds.
// fern-lint: allow cyclomatic-complexity
function reconstruct(own y: i32[], setup: Setup): Result[(i32[], i32[]), pcm.AudioError] {
  let count = range(setup.multiplier);
  if (count == 0 || y.len() < 2 || y.len() > 65 || setup.x.len() != y.len() || setup.low.len() != y.len() || setup.high.len() != y.len()) {
    return Err(pcm.Header);
  }
  if (y[0] < 0 || y[0] >= count || y[1] < 0 || y[1] >= count) {
    return Err(pcm.Header);
  }
  let active: i32[] = [];
  for _ in 0..y.len() {
    active = active.append(0);
  }
  active = active.with(0, 1).with(1, 1);
  for i in 2..y.len() {
    let lo = setup.low[i];
    let hi = setup.high[i];
    if (lo < 0 || hi < 0 || lo >= i || hi >= i || setup.x[lo] >= setup.x[i] || setup.x[hi] <= setup.x[i]) {
      return Err(pcm.Header);
    }
    let prediction = predicted(setup.x[lo], y[lo], setup.x[hi], y[hi], setup.x[i]);
    let highroom = count - prediction;
    let lowroom = prediction;
    let value = y[i];
    if (value != 0) {
      active = active.with(lo, 1).with(hi, 1).with(i, 1);
      let room = 2 * (if (highroom < lowroom) { highroom } else { lowroom });
      let result = if (value >= room) { if (highroom > lowroom) { value - lowroom + prediction } else { prediction - value + highroom - 1 } } else if ((value & 1) != 0) { prediction - (value + 1) / 2 } else { prediction + value / 2 };
      if (result < 0 || result >= count) {
        return Err(pcm.Header);
      }
      y = y.with(i, result);
    } else {
      y = y.with(i, prediction);
    }
  }
  return Ok((y, active));
}

// Keep integer line steps beside clipping and inverse-decibel table bounds.
// fern-lint: allow cyclomatic-complexity
function line(own output: f32[], n: i32, x0: i32, y0: i32, x1: i32, y1: i32): Result[f32[], pcm.AudioError] {
  if (x0 < 0 || x1 <= x0 || y0 < 0 || y0 > 255 || y1 < 0 || y1 > 255) {
    return Err(pcm.Header);
  }
  if (x0 >= n) {
    return Ok(output);
  }
  let dy = y1 - y0;
  let width = x1 - x0;
  let base = dy / width;
  let step = if (dy < 0) { base - 1 } else { base + 1 };
  let absolute = if (dy < 0) { -dy } else { dy };
  let absolute_base = if (base < 0) { -base } else { base };
  let remainder = absolute - absolute_base * width;
  let error = 0;
  let y = y0;
  output = output.with(x0, tables.INVERSE_DB[y]);
  let end = if (x1 < n) { x1 } else { n };
  for x in x0 + 1..end {
    error += remainder;
    if (error >= width) {
      error -= width;
      y += step;
    } else {
      y += base;
    }
    if (y < 0 || y > 255) {
      return Err(pcm.Header);
    }
    output = output.with(x, tables.INVERSE_DB[y]);
  }
  return Ok(output);
}

// Check active-point indices and render only the valid spectral prefix.
// fern-lint: allow cyclomatic-complexity
pub function synth(own data: Data, setup: Setup, n: i32, own output: f32[]): Result[f32[], pcm.AudioError] {
  if (n < 1 || n > 4096 || output.len() < n) {
    return Err(pcm.Buffer);
  }
  let Data { used, y } = data;
  if (!used) {
    for i in 0..n {
      output = output.with(i, 0.0f32);
    }
    return Ok(output);
  }
  let (values, active) = reconstruct(y, setup)?;
  if (setup.order.len() != values.len()) {
    return Err(pcm.Header);
  }
  let x = 0;
  let value = values[0] * setup.multiplier;
  for j in 1..setup.order.len() {
    let i = setup.order[j];
    if (i < 0 || i >= values.len()) {
      return Err(pcm.Header);
    }
    if (active[i] != 0) {
      let end = setup.x[i];
      let next = values[i] * setup.multiplier;
      output = line(output, n, x, value, end, next)?;
      x = end;
      value = next;
    }
  }
  if (x < n) {
    output = line(output, n, x, value, n, value)?;
  }
  return Ok(output);
}
```
