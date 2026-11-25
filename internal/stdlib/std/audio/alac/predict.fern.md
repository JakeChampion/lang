# ALAC adaptive prediction

This module reconstructs signed samples from residuals with adaptive predictor coefficients.
It handles direct residuals, the first-order special case, and the two-pass mode.
Orders 4 and 8, the two orders that Apple's encoder writes, have their own loops.
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

@inline
function sign(value: i32): i32 {
  return if (value > 0) { 1 } else if (value < 0) { -1 } else { 0 };
}

// Order 4 keeps its coefficients in locals, as Apple's decoder does.
// fern-lint: allow cyclomatic-complexity
function restore4(own values: i32[], count: i32, coefficients: [i32], shift: i32, unused: i32): i32[] {
  let a0 = coefficients[0];
  let a1 = coefficients[1];
  let a2 = coefficients[2];
  let a3 = coefficients[3];
  let round = 1 << shift >> 1;
  for i in 5..count {
    let top = values[i - 5];
    let b0 = top - values[i - 1];
    let b1 = top - values[i - 2];
    let b2 = top - values[i - 3];
    let b3 = top - values[i - 4];
    let residual = values[i];
    values = values.with(i, residual + top + (round - a0 * b0 - a1 * b1 - a2 * b2 - a3 * b3 >> shift) << unused >> unused);
    let direction = sign(residual);
    if (direction != 0) {
      let s3 = direction * sign(b3);
      a3 = a3 - s3 << 16 >> 16;
      residual -= s3 * b3 >> shift;
      if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
        let s2 = direction * sign(b2);
        a2 = a2 - s2 << 16 >> 16;
        residual -= 2 * (s2 * b2 >> shift);
        if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
          let s1 = direction * sign(b1);
          a1 = a1 - s1 << 16 >> 16;
          residual -= 3 * (s1 * b1 >> shift);
          if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
            let s0 = direction * sign(b0);
            a0 = a0 - s0 << 16 >> 16;
          }
        }
      }
    }
  }
  return values;
}

// Order 8 keeps its coefficients in locals, as Apple's decoder does.
// fern-lint: allow cyclomatic-complexity
function restore8(own values: i32[], count: i32, coefficients: [i32], shift: i32, unused: i32): i32[] {
  let a0 = coefficients[0];
  let a1 = coefficients[1];
  let a2 = coefficients[2];
  let a3 = coefficients[3];
  let a4 = coefficients[4];
  let a5 = coefficients[5];
  let a6 = coefficients[6];
  let a7 = coefficients[7];
  let round = 1 << shift >> 1;
  for i in 9..count {
    let top = values[i - 9];
    let b0 = top - values[i - 1];
    let b1 = top - values[i - 2];
    let b2 = top - values[i - 3];
    let b3 = top - values[i - 4];
    let b4 = top - values[i - 5];
    let b5 = top - values[i - 6];
    let b6 = top - values[i - 7];
    let b7 = top - values[i - 8];
    let residual = values[i];
    values = values.with(i, residual + top + (round - a0 * b0 - a1 * b1 - a2 * b2 - a3 * b3 - a4 * b4 - a5 * b5 - a6 * b6 - a7 * b7 >> shift) << unused >> unused);
    let direction = sign(residual);
    if (direction != 0) {
      let s7 = direction * sign(b7);
      a7 = a7 - s7 << 16 >> 16;
      residual -= s7 * b7 >> shift;
      if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
        let s6 = direction * sign(b6);
        a6 = a6 - s6 << 16 >> 16;
        residual -= 2 * (s6 * b6 >> shift);
        if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
          let s5 = direction * sign(b5);
          a5 = a5 - s5 << 16 >> 16;
          residual -= 3 * (s5 * b5 >> shift);
          if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
            let s4 = direction * sign(b4);
            a4 = a4 - s4 << 16 >> 16;
            residual -= 4 * (s4 * b4 >> shift);
            if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
              let s3 = direction * sign(b3);
              a3 = a3 - s3 << 16 >> 16;
              residual -= 5 * (s3 * b3 >> shift);
              if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
                let s2 = direction * sign(b2);
                a2 = a2 - s2 << 16 >> 16;
                residual -= 6 * (s2 * b2 >> shift);
                if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
                  let s1 = direction * sign(b1);
                  a1 = a1 - s1 << 16 >> 16;
                  residual -= 7 * (s1 * b1 >> shift);
                  if (direction > 0 && residual > 0 || direction < 0 && residual < 0) {
                    let s0 = direction * sign(b0);
                    a0 = a0 - s0 << 16 >> 16;
                  }
                }
              }
            }
          }
        }
      }
    }
  }
  return values;
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
  let shift = parameters.shift;
  let unused = 32 - parameters.depth;
  if (order == 8) {
    return Ok(restore8(values, count, parameters.coefficients, shift, unused));
  }
  if (order == 4) {
    return Ok(restore4(values, count, parameters.coefficients, shift, unused));
  }
  let coefficients = parameters.coefficients;
  let round = 1 << shift >> 1;
  for i in order + 1..count {
    let top = values[i - order - 1];
    let sum = 0;
    for k in 0..order {
      sum += coefficients[k] * (values[i - 1 - k] - top);
    }
    let residual = values[i];
    values = values.with(i, residual + top + (sum + round >> shift) << unused >> unused);
    // Adapt from the oldest tap until the residual changes sign.
    let direction = sign(residual);
    if (direction != 0) {
      let k = order - 1;
      while (k >= 0) {
        let difference = top - values[i - 1 - k];
        let adjustment = direction * sign(difference);
        coefficients = coefficients.with(k, coefficients[k] - adjustment << 16 >> 16);
        residual -= (order - k) * (adjustment * difference >> shift);
        if (direction > 0 && residual <= 0 || direction < 0 && residual >= 0) {
          break;
        }
        k -= 1;
      }
    }
  }
  return Ok(values);
}
```
