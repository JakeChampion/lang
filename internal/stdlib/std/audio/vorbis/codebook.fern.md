# Bounded Vorbis codebooks

This module reads Vorbis Huffman lengths and vector lookup tables.
It validates complete prefix trees and the single-entry exception from Errata 20150226.
A nine-bit prefix table handles short codewords. Longer words use a bounded tree walk.

Setup budgets bound total entries, vector values, and tree nodes before allocation.
Vector lookup values use binary32 arithmetic after the Vorbis packed-float conversion.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis codebook module.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/bits";
import "std/audio/vorbis/types";
import "std/audio/pcm";
import "std/f64";

pub struct Book { dimensions: i32, entries: i32, lookup: i32, fast: i32[], children: i32[], vectors: f32[] }

const MISSING: i32 = 2147483647;

// Keep ordered and sparse wire encodings together with their length bounds.
// fern-lint: allow cyclomatic-complexity
function lengths(input: [u8], reader: bits.Reader, entries: i32): Result[(bits.Reader, i32[]), pcm.AudioError] {
  let (b1, ordered) = bits.read(input, reader, 1)?;
  reader = b1;
  let values: i32[] = [];
  if (ordered == 0u32) {
    let (b2, sparse) = bits.read(input, reader, 1)?;
    reader = b2;
    for _ in 0..entries {
      let used = 1u32;
      if (sparse != 0u32) {
        let (next, value) = bits.read(input, reader, 1)?;
        reader = next;
        used = value;
      }
      let length = 0;
      if (used != 0u32) {
        let (next, value) = bits.read(input, reader, 5)?;
        reader = next;
        length = value as i32 + 1;
      }
      values = values.append(length);
    }
  } else {
    let (b2, first) = bits.read(input, reader, 5)?;
    reader = b2;
    let length = first as i32 + 1;
    while (values.len() < entries) {
      if (length > 32) {
        return Err(pcm.Header);
      }
      let (next, count) = bits.read(input, reader, types.ilog(entries - values.len()))?;
      reader = next;
      if (count > (entries - values.len()) as u32) {
        return Err(pcm.Header);
      }
      for _ in 0..count as i32 {
        values = values.append(length);
      }
      length += 1;
    }
  }
  return Ok((reader, values));
}

// Update the next available branch at each depth in Vorbis entry order.
// Keep entry-order branch propagation beside complete-tree and single-entry checks.
// fern-lint: allow cyclomatic-complexity
function words(lengths: [i32]): Result[u64[], pcm.AudioError] {
  let next: u64[] = [];
  for _ in 0..33 {
    next = next.append(0u64);
  }
  let out: u64[] = [];
  let used = 0;
  let kraft = 0u64;
  let single_length = 0;
  for length in lengths {
    if (length < 0 || length > 32) {
      return Err(pcm.Header);
    }
    if (length == 0) {
      out = out.append(0u64);
      continue;
    }
    used += 1;
    single_length = length;
    kraft += 1u64 << (32 - length) as u64;
    if (kraft > 4294967296u64) {
      return Err(pcm.Header);
    }
    let code = next[length];
    if (code >= 1u64 << length as u64) {
      return Err(pcm.Header);
    }
    let level = length;
    while (level >= 0) {
      if ((next[level] & 1u64) != 0u64) {
        if (level == 0) {
          return Err(pcm.Header);
        }
        next = next.with(level, next[level - 1] << 1u64);
        break;
      }
      next = next.with(level, next[level] + 1u64);
      level -= 1;
    }
    let branch = next[length];
    for depth in length + 1..33 {
      let shift = (depth - length) as u64;
      if (next[depth] != code << shift) {
        break;
      }
      next = next.with(depth, branch << shift);
    }
    out = out.append(code);
  }
  if (used == 0 || used == 1 && single_length != 1 || used != 1 && kraft != 4294967296u64) {
    return Err(pcm.Header);
  }
  return Ok(out);
}

// Validate each prefix-tree transition before node allocation or access.
// fern-lint: allow cyclomatic-complexity
function tree(lengths: [i32], codes: [u64], maximum: i32): Result[i32[], pcm.AudioError] {
  if (maximum < 1) {
    return Err(pcm.Limit);
  }
  let children: i32[] = [MISSING, MISSING];
  let used = 0;
  let single = 0;
  for symbol in 0..lengths.len() {
    let length = lengths[symbol];
    if (length == 0) {
      continue;
    }
    used += 1;
    single = symbol;
    let node = 0;
    for depth in 0..length {
      let at = 2 * node + (codes[symbol] >> (length - depth - 1) as u64 & 1u64) as i32;
      let child = children[at];
      if (depth + 1 == length) {
        if (child != MISSING) {
          return Err(pcm.Header);
        }
        children = children.with(at, -symbol - 1);
      } else {
        if (child == MISSING) {
          if (children.len() / 2 >= maximum || children.len() > 2147483645) {
            return Err(pcm.Limit);
          }
          let next = children.len() / 2;
          children = children.with(at, next).append(MISSING).append(MISSING);
          node = next;
        } else {
          if (child < 0) {
            return Err(pcm.Header);
          }
          node = child;
        }
      }
    }
  }
  if (used == 1) {
    children = children.with(0, -single - 1).with(1, -single - 1);
  }
  return Ok(children);
}

function prefix(children: [i32]): i32[] {
  let out: i32[] = [];
  for pattern in 0..512 {
    let node = 0;
    let entry = MISSING;
    for depth in 0..9 {
      let child = children[2 * node + (pattern >> depth & 1)];
      if (child == MISSING) {
        break;
      }
      if (child < 0) {
        entry = depth + 1 << 24 | -child - 1;
        break;
      }
      node = child;
      if (depth == 8) {
        entry = -node - 1;
      }
    }
    out = out.append(entry);
  }
  return out;
}

pub function unpack_float(value: u32): Result[f32, pcm.AudioError] {
  let mantissa = value & 2097151u32;
  let exponent = (value >> 21u32 & 1023u32) as i32 - 788;
  let scale = f64_from_bits(((exponent + 1023) as u64 << 52u64) as i64);
  let absolute = (mantissa as f64 * scale) as f32;
  let result = if ((value & 2147483648u32) != 0u32) { -absolute } else { absolute };
  if (!result.is_finite()) {
    return Err(pcm.Header);
  }
  return Ok(result);
}

function power_fits(base: i32, exponent: i32, maximum: i32): boolean {
  let value = 1;
  for _ in 0..exponent {
    if (value > maximum / base) {
      return false;
    }
    value *= base;
  }
  return true;
}

pub function lookup_count(entries: i32, dimensions: i32): Result[i32, pcm.AudioError] {
  if (entries < 1 || dimensions < 1) {
    return Err(pcm.Header);
  }
  let low = 1;
  let high = entries;
  while (low < high) {
    let middle = low + (high - low + 1) / 2;
    if (power_fits(middle, dimensions, entries)) {
      low = middle;
    } else {
      high = middle - 1;
    }
  }
  return Ok(low);
}

// Keep lookup types, sequence arithmetic, and finite-value checks in one bounded expansion.
// fern-lint: allow cyclomatic-complexity
function lookup(input: [u8], reader: bits.Reader, entries: i32, dimensions: i32, kind: i32, maximum: i32): Result[(bits.Reader, f32[]), pcm.AudioError] {
  if (kind == 0) {
    return Ok((reader, []));
  }
  if (kind != 1 && kind != 2) {
    return Err(pcm.Header);
  }
  let count = entries as i64 * (dimensions as i64);
  if (maximum < 0 || count > maximum as i64) {
    return Err(pcm.Limit);
  }
  let (b1, raw_minimum) = bits.read(input, reader, 32)?;
  let (b2, raw_delta) = bits.read(input, b1, 32)?;
  let minimum = unpack_float(raw_minimum)?;
  let delta = unpack_float(raw_delta)?;
  let (b3, raw_width) = bits.read(input, b2, 4)?;
  let (b4, sequence) = bits.read(input, b3, 1)?;
  reader = b4;
  let values = if (kind == 1) { lookup_count(entries, dimensions)? } else { count as i32 };
  let multiplicands: i32[] = [];
  for _ in 0..values {
    let (next, value) = bits.read(input, reader, raw_width as i32 + 1)?;
    reader = next;
    multiplicands = multiplicands.append(value as i32);
  }
  let vectors: f32[] = [];
  for entry in 0..entries {
    let index = entry;
    let last = 0.0f32;
    for dimension in 0..dimensions {
      let at = if (kind == 1) { index % values } else { entry * dimensions + dimension };
      let value = multiplicands[at] as f32 * delta + minimum + last;
      if (!value.is_finite()) {
        return Err(pcm.Header);
      }
      vectors = vectors.append(value);
      if (sequence != 0u32) {
        last = value;
      }
      if (kind == 1) {
        index /= values;
      }
    }
  }
  return Ok((reader, vectors));
}

// Check aggregate setup budgets before each allocation.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], reader: bits.Reader, budget: types.Budget, limits: types.Limits): Result[(bits.Reader, Book, types.Budget), pcm.AudioError] {
  if (!types.valid(limits) || budget.entries < 0 || budget.entries > limits.max_entries || budget.vectors < 0 || budget.vectors > limits.max_vectors || budget.nodes < 0 || budget.nodes > limits.max_nodes) {
    return Err(pcm.Limit);
  }
  let (b1, sync) = bits.read(input, reader, 24)?;
  if (sync != 5653314u32) {
    return Err(pcm.Header);
  }
  let (b2, dimensions) = bits.read(input, b1, 16)?;
  let (b3, entries) = bits.read(input, b2, 24)?;
  if (dimensions == 0u32 || entries == 0u32) {
    return Err(pcm.Header);
  }
  if (entries > (limits.max_entries - budget.entries) as u32) {
    return Err(pcm.Limit);
  }
  let (b4, sizes) = lengths(input, b3, entries as i32)?;
  let codes = words(sizes)?;
  let children = tree(sizes, codes, limits.max_nodes - budget.nodes)?;
  let fast = prefix(children);
  let (b5, kind) = bits.read(input, b4, 4)?;
  let (end, vectors) = lookup(input, b5, entries as i32, dimensions as i32, kind as i32, limits.max_vectors - budget.vectors)?;
  let book = Book { dimensions: dimensions as i32, entries: entries as i32, lookup: kind as i32, fast: fast, children: children, vectors: vectors };
  return Ok((end, book, types.Budget { entries: budget.entries + entries as i32, vectors: budget.vectors + vectors.len(), nodes: budget.nodes + children.len() / 2 }));
}

// Keep short-prefix and long-tree decoding beside their cursor and node bounds.
// fern-lint: allow cyclomatic-complexity
pub function scalar(input: [u8], reader: bits.Reader, book: Book): Result[(bits.Reader, i32), pcm.AudioError] {
  if (!bits.valid(input, reader) || book.fast.len() != 512 || book.children.len() < 2) {
    return Err(pcm.Header);
  }
  let available = bits.left(input, reader);
  let count = if (available < 9i64) { available as i32 } else { 9 };
  let (_, pattern) = bits.read(input, reader, count)?;
  let entry = book.fast[pattern as i32];
  if (entry == MISSING) {
    return Err(if (count < 9) { pcm.Truncated } else { pcm.Header });
  }
  if (entry >= 0) {
    let length = entry >> 24;
    if (length > count) {
      return Err(pcm.Truncated);
    }
    return Ok((bits.skip(input, reader, length)?, entry & 16777215));
  }
  reader = bits.skip(input, reader, 9)?;
  let node = -entry - 1;
  for _ in 9..32 {
    if (node < 0 || node >= book.children.len() / 2) {
      return Err(pcm.Header);
    }
    let (next, value) = bits.read(input, reader, 1)?;
    reader = next;
    let child = book.children[2 * node + value as i32];
    if (child == MISSING) {
      return Err(pcm.Header);
    }
    if (child < 0) {
      return Ok((reader, -child - 1));
    }
    node = child;
  }
  return Err(pcm.Header);
}
```
