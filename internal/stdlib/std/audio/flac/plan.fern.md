# FLAC fixed-predictor frame plans

This module selects constant, verbatim, or fixed-predictor subframes by their exact bit cost.
It compares Rice and Rice2 partitions with optional raw residual escape codes.
Residuals obey the signed 32-bit limit that excludes the most negative integer.
Verbatim coding remains available when a predictor exceeds that limit.

Source samples must fit the declared PCM depth before plan selection.
The public encoder validates that range before it constructs any plan.
Plans retain bounded scalar parameters and no PCM.
The caller must supply the same immutable PCM when it writes the selected plan.
Stereo transforms use wide integer arithmetic and preserve all source bits.

```fern
<<*>>=
// Select exact fixed-predictor and Rice costs for one FLAC channel.

import "std/audio/flac/bitwriter";
import "std/audio/pcm";
import "std/u32";
import "std/u64";

pub opaque struct Channel { frames: i32, channels: i32, channel: i32, assignment: i32, kind: i32, wasted: i32, bits: i32, cost: i64, partition: i32, rice2: boolean, parameters: i32[] }

struct Rice { first_cost: i64, second_cost: i64, first: i32, second: i32 }

function sample(input: [i32], channels: i32, frame: i32, channel: i32, assignment: i32): i64 {
  if (assignment < 8) {
    return input[frame * channels + channel] as i64;
  }
  let left = input[frame * 2] as i64;
  let right = input[frame * 2 + 1] as i64;
  if (assignment == 8) {
    return if (channel == 0) { left } else { left - right };
  }
  if (assignment == 9) {
    return if (channel == 0) { left - right } else { right };
  }
  return if (channel == 0) { left + right >> 1i64 } else { left - right };
}

function residual(input: [i32], channels: i32, at: i32, channel: i32, assignment: i32, order: i32, wasted: i32): i64 {
  let value = sample(input, channels, at, channel, assignment) >> wasted as i64;
  if (order == 0) {
    return value;
  }
  let a = sample(input, channels, at - 1, channel, assignment) >> wasted as i64;
  if (order == 1) {
    return value - a;
  }
  let b = sample(input, channels, at - 2, channel, assignment) >> wasted as i64;
  if (order == 2) {
    return value - 2i64 * a + b;
  }
  let c = sample(input, channels, at - 3, channel, assignment) >> wasted as i64;
  if (order == 3) {
    return value - 3i64 * a + 3i64 * b - c;
  }
  let d = sample(input, channels, at - 4, channel, assignment) >> wasted as i64;
  return value - 4i64 * a + 6i64 * b - 4i64 * c + d;
}

function folded(value: i64): u32 {
  return (value << 1i64 ^ value >> 63i64) as u32;
}

function statistics(values: [u32], start: i32, end: i32, k: i32): (i64, i64) {
  let quotient = 0i64;
  let half = 0i64;
  for i in start..end {
    let q = (values[i] >> k as u32) as i64;
    quotient += q;
    half += q + 1i64 >> 1i64;
  }
  return (quotient, half);
}

// The discrete Rice cost is convex. Its adjacent difference selects the exact minimum.
// fern-lint: allow cyclomatic-complexity
function rice(values: [u32], start: i32, end: i32): Rice {
  let count = (end - start) as i64;
  if (count == 0i64) {
    return Rice { first_cost: 4i64, second_cost: 5i64, first: 0, second: 0 };
  }
  let sum = 0i64;
  let maximum = 0u32;
  for i in start..end {
    sum += values[i] as i64;
    if (values[i] > maximum) {
      maximum = values[i];
    }
  }
  let average = (sum / count) as u32;
  let k = if (average == 0u32) { 0 } else { 31 - average.leading_zeros() };
  if (k > 30) {
    k = 30;
  }
  let (quotient, half) = statistics(values, start, end, k);
  while (k > 0) {
    let (before, delta) = statistics(values, start, end, k - 1);
    if (delta > count) {
      break;
    }
    k -= 1;
    quotient = before;
    half = delta;
  }
  while (k < 30 && half > count) {
    k += 1;
    let (next, delta) = statistics(values, start, end, k);
    quotient = next;
    half = delta;
  }
  let first_k = if (k < 15) { k } else { 14 };
  let first_q = quotient;
  if (first_k != k) {
    let (q, _) = statistics(values, start, end, first_k);
    first_q = q;
  }
  let first_cost = 4i64 + count * ((first_k + 1) as i64) + first_q;
  let second_cost = 5i64 + count * ((k + 1) as i64) + quotient;
  let first = first_k;
  let second = k;
  let width = 32 - maximum.leading_zeros();
  if (width <= 31) {
    let raw = 5i64 + count * (width as i64);
    if (4i64 + raw < first_cost) {
      first_cost = 4i64 + raw;
      first = -1 - width;
    }
    if (5i64 + raw < second_cost) {
      second_cost = 5i64 + raw;
      second = -1 - width;
    }
  }
  return Rice { first_cost: first_cost, second_cost: second_cost, first: first, second: second };
}

function partitions(values: [u32], base: Channel, order: i32, maximum: i32): Channel {
  let best = base;
  let p = 0;
  while (p <= maximum) {
    let count = 1 << p;
    if (base.frames % count != 0 || base.frames / count < order) {
      break;
    }
    let per = base.frames / count;
    let first: i32[] = [];
    let second: i32[] = [];
    let first_cost = 6i64;
    let second_cost = 6i64;
    for part in 0..count {
      let start = part * per + (if (part == 0) { order } else { 0 });
      let selected = rice(values, start, (part + 1) * per);
      first = first.append(selected.first);
      second = second.append(selected.second);
      first_cost += selected.first_cost;
      second_cost += selected.second_cost;
    }
    let wide = second_cost < first_cost;
    let cost = 8i64 + base.wasted as i64 + order as i64 * ((base.bits - base.wasted) as i64)
      + (if (wide) { second_cost } else { first_cost });
    if (cost < best.cost) {
      best = Channel { ...base, kind: 8 + order, cost: cost, partition: p, rice2: wide, parameters: if (wide) { second } else { first } };
    }
    p += 1;
  }
  return best;
}

function predictors(input: [i32], base: Channel, max_order: i32, max_partition: i32, own scratch: u32[]): (Channel, u32[]) {
  let best = base;
  let order = 0;
  while (order <= max_order && order < base.frames) {
    let valid = true;
    for at in order..base.frames {
      let value = residual(input, base.channels, at, base.channel, base.assignment, order, base.wasted);
      if (value <= -2147483648i64 || value >= 2147483648i64) {
        valid = false;
        break;
      }
      scratch = scratch.with(at, folded(value));
    }
    if (valid) {
      let selected = partitions(scratch, Channel { ...base, cost: best.cost }, order, max_partition);
      if (selected.cost < best.cost) {
        best = selected;
      }
    }
    order += 1;
  }
  return (best, scratch);
}

// Validate source extents and transform selectors before every indexed sample read.
// fern-lint: allow cyclomatic-complexity
pub function choose(input: [i32], channels: i32, channel: i32, assignment: i32, depth: i32, max_order: i32, max_partition: i32, own scratch: u32[]): Result[(Channel, u32[]), pcm.AudioError] {
  if (channels < 1 || channels > 8 || channel < 0 || channel >= channels || input.len() % channels != 0
    || depth < 4 || depth > 32 || max_order < 0 || max_order > 4 || max_partition < 0 || max_partition > 15
    || assignment < 0 || assignment > 10 || assignment < 8 && assignment + 1 != channels || assignment >= 8 && channels != 2) {
    return Err(pcm.Header);
  }
  let frames = input.len() / channels;
  if (frames < 1 || frames > 65535) {
    return Err(pcm.Header);
  }
  let bits = depth + (if (assignment == 8 && channel == 1 || assignment == 9 && channel == 0 || assignment == 10 && channel == 1) { 1 } else { 0 });
  let best = Channel { frames: frames, channels: channels, channel: channel, assignment: assignment, kind: 1, wasted: 0, bits: bits, cost: 8i64 + frames as i64 * (bits as i64), partition: 0, rice2: false, parameters: [] };
  let first = sample(input, channels, 0, channel, assignment);
  let constant = true;
  let union_bits = 0u64;
  for at in 0..frames {
    let value = sample(input, channels, at, channel, assignment);
    constant = constant && value == first;
    union_bits |= value as u64;
  }
  if (constant) {
    return Ok((Channel { ...best, kind: 0, cost: 8i64 + bits as i64 }, scratch));
  }
  while (scratch.len() < frames) {
    scratch = scratch.append(0u32);
  }
  let wasted = union_bits.trailing_zeros();
  if (wasted >= bits) {
    wasted = bits - 1;
  }
  for shift in if (wasted > 0) { [0, wasted] } else { [0] } {
    let base = Channel { ...best, kind: 1, wasted: shift, parameters: [], cost: 8i64 + shift as i64 + frames as i64 * ((bits - shift) as i64) };
    if (base.cost < best.cost) {
      best = base;
    }
    let (selected, next) = predictors(input, Channel { ...base, cost: best.cost }, max_order, max_partition, scratch);
    scratch = next;
    if (selected.cost < best.cost) {
      best = selected;
    }
  }
  return Ok((best, scratch));
}

pub function (plan: Channel) bit_count(): i64 {
  return plan.cost;
}

// Emit the selected subframe, warm-up samples, and bounded residual partitions.
// fern-lint: allow cyclomatic-complexity
pub function (plan: Channel) write(input: [i32], own output: bitwriter.BitWriter): Result[bitwriter.BitWriter, pcm.AudioError] {
  if (input.len() != plan.frames * plan.channels) {
    return Err(pcm.Header);
  }
  output = output.put((plan.kind * 2 + (if (plan.wasted > 0) { 1 } else { 0 })) as u64, 8);
  if (plan.wasted > 0) {
    output = output.zeros((plan.wasted - 1) as u64).put(1u64, 1);
  }
  let bits = plan.bits - plan.wasted;
  if (plan.kind < 2) {
    let count = if (plan.kind == 0) { 1 } else { plan.frames };
    for at in 0..count {
      output = output.put((sample(input, plan.channels, at, plan.channel, plan.assignment) >> plan.wasted as i64) as u64, bits);
    }
    return Ok(output);
  }
  let order = plan.kind - 8;
  for at in 0..order {
    output = output.put((sample(input, plan.channels, at, plan.channel, plan.assignment) >> plan.wasted as i64) as u64, bits);
  }
  output = output.put(if (plan.rice2) { 1u64 } else { 0u64 }, 2).put(plan.partition as u64, 4);
  let width = if (plan.rice2) { 5 } else { 4 };
  let per = plan.frames / plan.parameters.len();
  for part in 0..plan.parameters.len() {
    let param = plan.parameters[part];
    let start = part * per + (if (part == 0) { order } else { 0 });
    if (param < 0) {
      let raw = -1 - param;
      output = output.put(((1 << width) - 1) as u64, width).put(raw as u64, 5);
      for at in start..(part + 1) * per {
        output = output.put(residual(input, plan.channels, at, plan.channel, plan.assignment, order, plan.wasted) as u64, raw);
      }
    } else {
      output = output.put(param as u64, width);
      for at in start..(part + 1) * per {
        let value = folded(residual(input, plan.channels, at, plan.channel, plan.assignment, order, plan.wasted));
        output = output.zeros((value >> param as u32) as u64).put(1u64, 1).put(value as u64, param);
      }
    }
  }
  return Ok(output);
}
```
