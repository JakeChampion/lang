# AAC temporal noise shaping

This module reads AAC-LC temporal noise shaping filters and applies them to spectral coefficients.
The input syntax bounds filter count, order, and coefficient resolution.
Filtering stays within the declared scalefactor bands for each window.

This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's AAC TNS implementation.
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// Previous author: Kostya Shishkov <kostya.shiskov@gmail.com>.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/aac/bits";
import "std/audio/aac/dsp";
import "std/audio/aac/tables";
import "std/audio/pcm";

pub struct Filter { window: i32, top: i32, bottom: i32, reverse: boolean, coefficients: f32[] }

function coefficients(input: [u8], reader: bits.Reader, order: i32, resolution: i32, compressed: i32): Result[(bits.Reader, f32[]), pcm.AudioError] {
  let width = resolution - compressed;
  let base = if (resolution == 3) { if (compressed == 0) { 0 } else { 8 } } else { if (compressed == 0) { 12 } else { 28 } };
  let reflection: f32[] = [];
  for _ in 0..order {
    let (next, value) = bits.read(input, reader, width)?;
    reader = next;
    reflection = reflection.append(tables.TNS_COEFFICIENTS[base + value as i32]);
  }
  let lpc = dsp.zeros(order);
  let scratch = dsp.zeros(order);
  for m in 0..order {
    for i in 0..m {
      scratch = scratch.with(i, lpc[i] + reflection[m] * lpc[m - i - 1]);
    }
    for i in 0..m {
      lpc = lpc.with(i, scratch[i]);
    }
    lpc = lpc.with(m, reflection[m]);
  }
  return Ok((reader, lpc));
}

// Read each TNS filter with its resolution and compression fields.
// fern-lint: allow cyclomatic-complexity
function filter(input: [u8], reader: bits.Reader, short: boolean, resolution: i32, window: i32, top: i32): Result[(bits.Reader, Filter), pcm.AudioError] {
  let (b1, length) = bits.read(input, reader, if (short) { 4 } else { 6 })?;
  let (b2, order) = bits.read(input, b1, if (short) { 3 } else { 5 })?;
  let maximum = if (short) { 7 } else { 12 };
  if (order > maximum as u32) {
    return Err(pcm.Header);
  }
  let bottom = if (length as i32 > top) { 0 } else { top - length as i32 };
  if (order == 0u32) {
    return Ok((b2, Filter { window: window, top: top, bottom: bottom, reverse: false, coefficients: [] }));
  }
  let (b3, reverse) = bits.read(input, b2, 1)?;
  let (b4, compressed) = bits.read(input, b3, 1)?;
  let (end, lpc) = coefficients(input, b4, order as i32, resolution, compressed as i32)?;
  return Ok((end, Filter { window: window, top: top, bottom: bottom, reverse: reverse != 0u32, coefficients: lpc }));
}

// Read per-window TNS filters with the short and long block bounds.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], reader: bits.Reader, rate: i32, short: boolean): Result[(bits.Reader, Filter[]), pcm.AudioError] {
  if (rate < 0 || rate >= 13) {
    return Err(pcm.Header);
  }
  let (b1, present) = bits.read(input, reader, 1)?;
  reader = b1;
  let filters: Filter[] = [];
  if (present == 0u32) {
    return Ok((reader, filters));
  }
  let bands = tables.bands(rate, short);
  let windows = if (short) { 8 } else { 1 };
  for window in 0..windows {
    let (b2, count) = bits.read(input, reader, if (short) { 1 } else { 2 })?;
    reader = b2;
    let resolution = 3;
    if (count != 0u32) {
      let (b3, value) = bits.read(input, reader, 1)?;
      reader = b3;
      resolution += value as i32;
    }
    let top = bands.count - 1;
    for _ in 0..count as i32 {
      let (next, value) = filter(input, reader, short, resolution, window, top)?;
      reader = next;
      top = value.bottom;
      filters = filters.append(value);
    }
  }
  return Ok((reader, filters));
}

function apply_filter(own values: f32[], f: Filter, start: i32, end: i32): f32[] {
  for processed in 0..end - start {
    let at = if (f.reverse) { end - 1 - processed } else { start + processed };
    let taps = if (processed < f.coefficients.len()) { processed } else { f.coefficients.len() };
    for j in 0..taps {
      let previous = if (f.reverse) { at + j + 1 } else { at - j - 1 };
      values = values.with(at, values[at] - values[previous] * f.coefficients[j]);
    }
  }
  return values;
}

// Check filter spans before each forward or reverse spectral filter.
// fern-lint: allow cyclomatic-complexity
pub function apply(own values: f32[], filters: Filter[], rate: i32, short: boolean, max_sfb: i32): Result[f32[], pcm.AudioError] {
  if (rate < 0 || rate > 12 || values.len() < 1024) {
    return Err(pcm.Header);
  }
  let bands = tables.bands(rate, short);
  let limit = if (short) { tables.TNS_SHORT_BANDS[rate] } else { tables.TNS_LONG_BANDS[rate] };
  limit = if (limit < max_sfb) { limit } else { max_sfb };
  for f in filters {
    if (f.window < 0 || f.window >= (if (short) { 8 } else { 1 }) || f.bottom < 0 || f.top < f.bottom || f.top >= bands.count || limit < 0 || limit >= bands.count) {
      return Err(pcm.Header);
    }
    let bottom = if (f.bottom < limit) { f.bottom } else { limit };
    let top = if (f.top < limit) { f.top } else { limit };
    let start = f.window * 128 + tables.BANDS[bands.start + bottom];
    let end = f.window * 128 + tables.BANDS[bands.start + top];
    values = apply_filter(values, f, start, end);
  }
  return Ok(values);
}
```
