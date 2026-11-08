# ALAC adaptive Rice residuals

This module decodes bounded Rice residuals and zero runs.
Every zero run must fit the current frame.
The predictor uses the valid output prefix and retains scratch capacity.
This module is available under MPL-2.0. See `licenses/alac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's ALAC decoder.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/alac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/alac/bits";
import "std/audio/alac/config";
import "std/audio/pcm";

pub struct Parameters { depth: i32, mode: i32, shift: i32, factor: u32, coefficients: i32[] }

// Keep predictor fields, coefficient count, and coefficient reads within one packet extent.
// fern-lint: allow cyclomatic-complexity
pub function parameters(input: [u8], at: i64, info: config.Info, depth: i32): Result[(i64, Parameters), pcm.AudioError] {
  if (depth < 1 || depth > 32) {
    return Err(pcm.Unsupported);
  }
  if (info.pb < 0 || info.pb > 255) {
    return Err(pcm.Header);
  }
  let (p1, mode) = bits.read(input, at, 4)?;
  let (p2, shift) = bits.read(input, p1, 4)?;
  let (p3, factor) = bits.read(input, p2, 3)?;
  let (p4, order) = bits.read(input, p3, 5)?;
  at = p4;
  if (mode != 0u32 && mode != 15u32) {
    return Err(pcm.Header);
  }
  let coefficients: i32[] = [];
  for _ in 0..order as i32 {
    let (next, value) = bits.signed(input, at, 16)?;
    at = next;
    coefficients = coefficients.append(value);
  }
  return Ok((at, Parameters { depth: depth, mode: mode as i32, shift: shift as i32, factor: factor * (info.pb as u32) >> 2u32, coefficients: coefficients }));
}

// Capped unary, escape, and shortened suffix rules form one bounded Rice code.
// fern-lint: allow cyclomatic-complexity
pub function rice(input: [u8], at: i64, k: i32, escape: i32): Result[(i64, u32), pcm.AudioError] {
  if (k < 0 || k > 31 || escape < 1 || escape > 32) {
    return Err(pcm.Header);
  }
  let prefix = 0u32;
  while (prefix < 9u32) {
    let (next, bit) = bits.read(input, at, 1)?;
    at = next;
    if (bit == 0u32) {
      break;
    }
    prefix += 1u32;
  }
  if (prefix == 9u32) {
    return bits.read(input, at, escape);
  }
  if (k < 2) {
    return Ok((at, if (k == 1) { prefix } else { 0u32 }));
  }
  let base = (prefix as u64 << k as u64) - prefix as u64;
  let (after_suffix, suffix) = bits.read(input, at, k - 1)?;
  at = after_suffix;
  let value = base;
  if (suffix != 0u32) {
    let (next, bit) = bits.read(input, at, 1)?;
    at = next;
    value += (suffix as u64 << 1u64) + bit as u64 - 1u64;
  }
  if (value > 4294967295u64) {
    return Err(pcm.Header);
  }
  return Ok((at, value as u32));
}

function log2(value: u32): i32 {
  let result = 0;
  while (value > 1u32) {
    value >>= 1u32;
    result += 1;
  }
  return result;
}

// Adaptive history and zero-run syntax share one bounded frame walk.
// fern-lint: allow cyclomatic-complexity
pub function residuals(input: [u8], at: i64, info: config.Info, parameters: Parameters, own output: i32[], count: i32): Result[(i64, i32[]), pcm.AudioError] {
  if (count < 1 || count > output.len() || parameters.depth < 1 || parameters.depth > 32 || parameters.factor > 446u32 || info.kb < 0 || info.kb > 31 || info.mb < 0 || info.mb > 255) {
    return Err(pcm.Header);
  }
  let history = info.mb as u32;
  let toggle = 0u32;
  let i = 0;
  while (i < count) {
    let k = log2((history >> 9u32) + 3u32);
    let (next, raw) = rice(input, at, if (k < info.kb) { k } else { info.kb }, parameters.depth)?;
    at = next;
    let Some(value) = raw +? toggle else {
      return Err(pcm.Header);
    };
    output = output.with(i, (value >> 1u32) as i32 ^ -((value & 1u32) as i32));
    history = if (value > 65535u32) { 65535u32 } else { history - (parameters.factor * history >> 9u32) + parameters.factor * value };
    i += 1;
    toggle = 0u32;
    if (history < 128u32 && i < count) {
      let run_k = (if (history == 0u32) { 8 } else { 7 - log2(history) }) + (history + 16u32 >> 6u32) as i32;
      let (next_run, zeros) = rice(input, at, if (run_k < info.kb) { run_k } else { info.kb }, 16)?;
      at = next_run;
      if (zeros > (count - i) as u32) {
        return Err(pcm.Header);
      }
      for _ in 0..zeros as i32 {
        output = output.with(i, 0);
        i += 1;
      }
      toggle = if (zeros < 65535u32) { 1u32 } else { 0u32 };
      history = 0u32;
    }
  }
  return Ok((at, output));
}
```
