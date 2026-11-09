# FLAC frame plans

This module selects constant, verbatim, fixed-predictor, or LPC subframes by their exact bit cost.
It compares Rice and Rice2 partitions with optional raw residual escape codes.
Residuals obey the signed 32-bit limit that excludes the most negative integer.
Verbatim coding remains available when a predictor exceeds that limit.
Fixed predictors reuse successive wide integer differences instead of repeated source predictions.
The temporary difference array uses eight bytes per frame, up to 524,280 bytes.

Optional LPC analysis uses one wide sample array and one float window.
Its integer residuals retain the same overflow checks and verbatim fallback.

For folded residuals, the Rice cost at parameter `k` is `n*(k+1) + sum(value >> k)`.
Its adjacent difference is `n - sum(ceil((value >> k)/2))`.
If `s` is the base-two integer logarithm of the mean, the minimum lies between `s-1` and `s+1`.
Below that range the adjacent difference is negative. Above it the difference is positive.
A zero integer mean uses parameter zero.

The encoder compares those exact costs and the Rice-one parameter cap in one scan.
The complete search also checks raw escapes and every permitted partition order.
Cost scans reuse one bounded parameter cache.
Only a smaller plan retains its selected parameter array.
The cache uses eight bytes per permitted partition.
Block divisibility, predictor order, and the requested partition limit cap that count.

Source samples must fit the declared PCM depth before plan selection.
The public encoder validates that range before it constructs any plan.
Plans retain bounded scalar parameters and no PCM.
The caller must supply the same immutable PCM when it writes the selected plan.
Stereo transforms use wide integer arithmetic and preserve all source bits.

```fern
<<*>>=
// Select exact predictor and Rice costs for one FLAC channel.

import "std/audio/flac/bitwriter";
import "std/audio/flac/lpc";
import "std/audio/pcm";
import "std/u32";
import "std/u64";

pub opaque struct Channel { frames: i32, channels: i32, channel: i32, assignment: i32, kind: i32, wasted: i32, bits: i32, cost: i64, partition: i32, rice2: boolean, parameters: i32[], model: Option[lpc.Model] }

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

struct Quotients { lower: i64, center: i64, upper: i64, limited: i64 }

function quotients(values: [u32], start: i32, end: i32, k: i32): Quotients {
  let lower = 0i64;
  let center = 0i64;
  let upper = 0i64;
  let limited = 0i64;
  let low = if (k > 0) { k - 1 } else { 0 };
  for i in start..end {
    let value = values[i];
    let q = value >> k as u32;
    lower += (value >> low as u32) as i64;
    center += q as i64;
    upper += (q >> 1u32) as i64;
    limited += (value >> 14u32) as i64;
  }
  return Quotients { lower: lower, center: center, upper: upper, limited: limited };
}

// Exact Rice minima lie within one bit of floor(log2(mean)). Ties select the lower parameter.
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
  let seed = if (average == 0u32) { 0 } else { 31 - average.leading_zeros() };
  if (seed > 30) {
    seed = 30;
  }
  let q = quotients(values, start, end, seed);
  let k = seed;
  let quotient = q.center;
  let cost = count * ((seed + 1) as i64) + quotient;
  if (seed > 0 && count * (seed as i64) + q.lower <= cost) {
    k = seed - 1;
    quotient = q.lower;
    cost = count * (seed as i64) + quotient;
  }
  if (seed < 30 && count * ((seed + 2) as i64) + q.upper < cost) {
    k = seed + 1;
    quotient = q.upper;
  }
  let first_k = if (k < 15) { k } else { 14 };
  let first_q = if (first_k == k) { quotient } else { q.limited };
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

function predictor_cost(base: Channel, order: i32): i64 {
  return 8i64 + base.wasted as i64 + order as i64 * ((base.bits - base.wasted) as i64)
    + (if (base.kind >= 32) { 9i64 + order as i64 * 15i64 } else { 0i64 });
}

function partition_parameters(candidates: [i32], partition: i32, wide: boolean): i32[] {
  let count = 1 << partition;
  let method = if (wide) { 1 } else { 0 };
  let output: i32[] = [];
  for part in 0..count {
    output = output.append(candidates[part * 2 + method]);
  }
  return output;
}

function partition_capacity(frames: i32, order: i32, maximum: i32): i32 {
  let power = (frames as u32).trailing_zeros();
  if (power > maximum) {
    power = maximum;
  }
  let count = 1 << power;
  while (frames / count < order) {
    count >>= 1;
  }
  return count * 2;
}

function partitions(values: [u32], base: Channel, order: i32, maximum: i32): Channel {
  let best = base;
  let candidates = __alloc_i32(partition_capacity(base.frames, order, maximum));
  let p = 0;
  while (p <= maximum) {
    let count = 1 << p;
    if (base.frames % count != 0 || base.frames / count < order) {
      break;
    }
    let per = base.frames / count;
    let first_cost = 6i64;
    let second_cost = 6i64;
    for part in 0..count {
      let start = part * per + (if (part == 0) { order } else { 0 });
      let selected = rice(values, start, (part + 1) * per);
      candidates = candidates.with(part * 2, selected.first).with(part * 2 + 1, selected.second);
      first_cost += selected.first_cost;
      second_cost += selected.second_cost;
    }
    let wide = second_cost < first_cost;
    let cost = predictor_cost(base, order) + (if (wide) { second_cost } else { first_cost });
    if (cost < best.cost) {
      best = Channel { ...base, kind: if (base.kind >= 32) { base.kind } else { 8 + order }, cost: cost, partition: p, rice2: wide, parameters: partition_parameters(candidates, p, wide) };
    }
    p += 1;
  }
  return best;
}

function differences(own values: i64[], order: i32): i64[] {
  let at = values.len() - 1;
  while (at >= order) {
    values = values.with(at, values[at] - values[at - 1]);
    at -= 1;
  }
  return values;
}

function predictors(input: [i32], base: Channel, max_order: i32, max_partition: i32, own scratch: u32[]): (Channel, u32[]) {
  let values = __alloc_i64(base.frames);
  for at in 0..base.frames {
    values = values.with(at, sample(input, base.channels, at, base.channel, base.assignment) >> base.wasted as i64);
  }
  let best = base;
  let order = 0;
  while (order <= max_order && order < base.frames) {
    if (order > 0) {
      values = differences(values, order);
    }
    let valid = true;
    for at in order..base.frames {
      let value = values[at];
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

function linear(input: [i32], base: Channel, maximum: i32, max_partition: i32, own scratch: u32[]): (Channel, u32[]) {
  let values = __alloc_i64(base.frames);
  for at in 0..base.frames {
    values = values.with(at, sample(input, base.channels, at, base.channel, base.assignment) >> base.wasted as i64);
  }
  let best = base;
  for model in lpc.models(values, maximum) {
    let order = model.coefficients.len();
    let valid = true;
    for at in order..base.frames {
      let sum = 0i64;
      for j in 0..order {
        sum += model.coefficients[j] as i64 * values[at - j - 1];
      }
      let value = values[at] - (sum >> model.shift as i64);
      if (value <= -2147483648i64 || value >= 2147483648i64) {
        valid = false;
        break;
      }
      scratch = scratch.with(at, folded(value));
    }
    if (valid) {
      let candidate = Channel { ...base, kind: 31 + order, model: Some(model), cost: best.cost };
      let selected = partitions(scratch, candidate, order, max_partition);
      if (selected.cost < best.cost) {
        best = selected;
      }
    }
  }
  return (best, scratch);
}

// Validate all source extents and predictor bounds before plan construction.
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
  let best = Channel { frames: frames, channels: channels, channel: channel, assignment: assignment, kind: 1, wasted: 0, bits: bits, cost: 8i64 + frames as i64 * (bits as i64), partition: 0, rice2: false, parameters: [], model: None };
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

// Preserve the fixed plan while LPC compares both valid wasted-bit widths.
// fern-lint: allow cyclomatic-complexity
pub function choose_lpc(input: [i32], channels: i32, channel: i32, assignment: i32, depth: i32, max_order: i32, max_partition: i32, max_lpc_order: i32, own scratch: u32[]): Result[(Channel, u32[]), pcm.AudioError] {
  if (max_lpc_order < 0 || max_lpc_order > 32) {
    return Err(pcm.Header);
  }
  let (best, fixed_scratch) = choose(input, channels, channel, assignment, depth, max_order, max_partition, scratch)?;
  scratch = fixed_scratch;
  if (max_lpc_order == 0 || best.kind == 0) {
    return Ok((best, scratch));
  }
  let union_bits = 0u64;
  for at in 0..best.frames {
    union_bits |= sample(input, channels, at, channel, assignment) as u64;
  }
  let wasted = union_bits.trailing_zeros();
  if (wasted >= best.bits) {
    wasted = best.bits - 1;
  }
  for shift in if (wasted > 0) { [0, wasted] } else { [0] } {
    let base = Channel { ...best, kind: 1, wasted: shift, model: None };
    let (selected, next) = linear(input, base, max_lpc_order, max_partition, scratch);
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

pub function (plan: Channel) is_constant(): boolean {
  return plan.kind == 0;
}

function planned_residual(input: [i32], plan: Channel, at: i32, order: i32): i64 {
  if let Some(model) = plan.model {
    let sum = 0i64;
    for j in 0..order {
      sum += model.coefficients[j] as i64 * (sample(input, plan.channels, at - j - 1, plan.channel, plan.assignment) >> plan.wasted as i64);
    }
    return (sample(input, plan.channels, at, plan.channel, plan.assignment) >> plan.wasted as i64) - (sum >> model.shift as i64);
  }
  return residual(input, plan.channels, at, plan.channel, plan.assignment, order, plan.wasted);
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
  let order = if (plan.kind >= 32) { plan.kind - 31 } else { plan.kind - 8 };
  for at in 0..order {
    output = output.put((sample(input, plan.channels, at, plan.channel, plan.assignment) >> plan.wasted as i64) as u64, bits);
  }
  if let Some(model) = plan.model {
    output = output.put(14u64, 4).put(model.shift as u64, 5);
    for coefficient in model.coefficients {
      output = output.put(coefficient as u64, 15);
    }
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
        output = output.put(planned_residual(input, plan, at, order) as u64, raw);
      }
    } else {
      output = output.put(param as u64, width);
      for at in start..(part + 1) * per {
        let value = folded(planned_residual(input, plan, at, order));
        output = output.zeros((value >> param as u32) as u64).put(1u64, 1).put(value as u64, param);
      }
    }
  }
  return Ok(output);
}
```
