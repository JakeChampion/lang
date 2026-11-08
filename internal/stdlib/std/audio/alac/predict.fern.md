# ALAC adaptive prediction

This module reconstructs signed samples from residuals with adaptive predictor coefficients.
It handles direct residuals, the first-order special case, and the two-pass mode.
Arithmetic retains the format's signed 32-bit wrap behavior before the declared-width clip.
The valid prefix uses at most the supplied scratch capacity.

This module is available under MPL-2.0. See `licenses/alac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's ALAC decoder.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/alac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/alac/entropy";
import "std/audio/pcm";

function clip(value: i32, depth: i32): i32 {
  return value << 32 - depth >> 32 - depth;
}

function delta(own values: i32[], count: i32, depth: i32): i32[] {
  for i in 1..count {
    values = values.with(i, clip(values[i] + values[i - 1], depth));
  }
  return values;
}

function sign(value: i32): i32 {
  return if (value > 0) { 1 } else if (value < 0) { -1 } else { 0 };
}

function adapt(own coefficients: i32[], values: [i32], at: i32, shift: i32, residual: i32): i32[] {
  let order = coefficients.len();
  let past = values[at - order - 1];
  let direction = sign(residual);
  for j in 0..order {
    let difference = past - values[at - order + j];
    let adjustment = sign(difference);
    let slot = order - 1 - j;
    coefficients = coefficients.with(slot, coefficients[slot] - direction * adjustment << 16 >> 16);
    residual -= (j + 1) * (direction * adjustment * difference >> shift);
    if (direction > 0 && residual <= 0 || direction < 0 && residual >= 0) {
      break;
    }
  }
  return coefficients;
}

// Validate scratch and predictor extents before direct, delta, or adaptive reconstruction.
// fern-lint: allow cyclomatic-complexity
pub function restore(own values: i32[], count: i32, parameters: entropy.Parameters): Result[i32[], pcm.AudioError] {
  if (count < 1 || count > values.len() || parameters.depth < 1 || parameters.depth > 32 || parameters.shift < 0 || parameters.shift > 15 || parameters.coefficients.len() > 31 || parameters.mode != 0 && parameters.mode != 15) {
    return Err(pcm.Header);
  }
  if (parameters.mode == 15) {
    values = delta(values, count, parameters.depth);
  }
  let order = parameters.coefficients.len();
  if (order == 0) {
    return Ok(values);
  }
  if (order == 31) {
    return Ok(delta(values, count, parameters.depth));
  }
  if (order >= count) {
    return Err(pcm.Header);
  }
  for i in 1..order + 1 {
    values = values.with(i, clip(values[i] + values[i - 1], parameters.depth));
  }
  let coefficients = parameters.coefficients;
  let round = 1 << parameters.shift >> 1;
  for i in order + 1..count {
    let past = values[i - order - 1];
    let sum = 0;
    for j in 0..order {
      sum += coefficients[order - 1 - j] * (values[i - order + j] - past);
    }
    let residual = values[i];
    values = values.with(i, clip(residual + past + (sum + round >> parameters.shift), parameters.depth));
    if (residual != 0) {
      coefficients = adapt(coefficients, values, i, parameters.shift, residual);
    }
  }
  return Ok(values);
}
```
