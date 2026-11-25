# MP3 Huffman encoding

This module writes the Huffman-coded spectrum of one MPEG-1 Layer III long-block granule.
It builds each code word from the decoder trees of `std/audio/mp3/tables`.
Thus the encoder and the decoder always use the same codes.

`codes` walks each decoder tree once and gives the code and length of each value pair and each count1 quad.
`plan` finds the zero region, the count1 region and the big-value pairs of quantized magnitudes.
It compares the bit cost of each region split and each table, and selects the least cost.
`write` appends the coded values and their sign bits to a frame buffer.
The cost from `plan` is the exact count of bits that `write` adds.

Each magnitude must be at most 8206, the largest value that table 31 can code.

```fern
<<*>>=
// Code MPEG-1 Layer III spectral values with the Huffman trees of the decoder.

import "std/audio/mp3/tables";

pub const MAX_VALUE: i32 = 8206;

pub struct Codes { code: i32[], length: i32[], quad_code: i32[], quad_length: i32[] }

pub struct Plan { bits: i32, big: i32, count1_end: i32, table0: i32, table1: i32, table2: i32, region0: i32, region1: i32, count1_table: i32 }

// The tables with no escape. Each one codes values up to its capacity.
const PLAIN: i32[] = [1, 2, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 15];

const CAPACITY: i32[] = [1, 2, 2, 3, 3, 5, 5, 5, 7, 7, 7, 15, 15];

// Tables 16 through 23 share the tree of table 16, and tables 24 through 31 share the tree of table 24.
function tree(table: i32): i32 {
  return if (table >= 24) { 24 } else if (table >= 16) { 16 } else { table };
}

function walk(own c: Codes, table: i32, base: i32, width: i32, prefix: i32, size: i32): Codes {
  let start = tables.TABINDEX[table];
  for v in 0..1 << width {
    let leaf = tables.TABS[start + base + v];
    if (leaf < 0) {
      c = walk(c, table, -(leaf >> 3), leaf & 7, prefix << width | v, size + width);
    } else {
      let n = leaf >> 8;
      let at = table * 256 + (leaf & 15) * 16 + (leaf >> 4 & 15);
      if (c.length[at] == 0) {
        c = Codes { ...c, code: c.code.with(at, prefix << n | v >> width - n), length: c.length.with(at, size + n) };
      }
    }
  }
  return c;
}

pub function codes(): Codes {
  let c = Codes { code: __alloc_i32(32 * 256), length: __alloc_i32(32 * 256), quad_code: __alloc_i32(16), quad_length: __alloc_i32(16) };
  for table in PLAIN {
    c = walk(c, table, 0, 5, 0, 0);
  }
  c = walk(c, 16, 0, 5, 0, 0);
  c = walk(c, 24, 0, 5, 0, 0);
  // Count1 table A: decode each 8-bit prefix and keep the bits that the decoder takes.
  for s in 0..256 {
    let leaf = tables.TAB32[s >> 4];
    if ((leaf & 8) == 0) {
      let width = leaf & 3;
      leaf = tables.TAB32[(leaf >> 3) + (s >> 4 - width & (1 << width) - 1)];
    }
    let n = leaf & 7;
    let quad = leaf >> 4 & 15;
    if (c.quad_length[quad] == 0) {
      c = Codes { ...c, quad_code: c.quad_code.with(quad, s >> 8 - n), quad_length: c.quad_length.with(quad, n) };
    }
  }
  return c;
}

function pair_cost(c: Codes, table: i32, x: i32, y: i32): i32 {
  let ex = if (x > 15) { 15 } else { x };
  let ey = if (y > 15) { 15 } else { y };
  return c.length[tree(table) * 256 + ex * 16 + ey] + (if (x != 0) { 1 } else { 0 }) + (if (y != 0) { 1 } else { 0 });
}

function linbits_for(family: i32, largest: i32): i32 {
  for t in family..family + 8 {
    if (largest <= 14 + (1 << tables.G_LINBITS[t])) {
      return t;
    }
  }
  return 0;
}

// Segment s is band s of `bounds`, cut at the end of the big values.
// Each of the 15 cost rows holds a prefix sum over the segments.
// Rows 13 and 14 are the trees of tables 16 and 24, without their escape bits.
struct Costs { prefix: i32[], escapes: i32[], largest: i32[], segments: i32 }

function segment_cost(c: Codes, ix: [i32], table: i32, from: i32, stop: i32): i32 {
  let sum = 0;
  let i = from;
  while (i < stop) {
    sum += pair_cost(c, table, ix[i], ix[i + 1]);
    i += 2;
  }
  return sum;
}

// The largest magnitude of lines `from` through `stop - 1`, and the count of lines that need an escape.
function segment_range(ix: [i32], from: i32, stop: i32): (i32, i32) {
  let top = 0;
  let escape = 0;
  for i in from..stop {
    if (ix[i] > top) {
      top = ix[i];
    }
    if (ix[i] >= 15) {
      escape += 1;
    }
  }
  return (top, escape);
}

function costs(c: Codes, ix: [i32], bounds: [i32], end: i32): Costs {
  let segments = 0;
  while (segments < 22 && bounds[segments] < end) {
    segments += 1;
  }
  let prefix = __alloc_i32(15 * 23);
  let escapes = __alloc_i32(23);
  let largest = __alloc_i32(23);
  for s in 0..segments {
    let stop = if (bounds[s + 1] < end) { bounds[s + 1] } else { end };
    let (top, escape) = segment_range(ix, bounds[s], stop);
    largest = largest.with(s, top);
    escapes = escapes.with(s + 1, escapes[s] + escape);
    for row in 0..15 {
      let table = if (row < 13) { PLAIN[row] } else if (row == 13) { 16 } else { 24 };
      let sum = if (row >= 13 || top <= CAPACITY[row]) { segment_cost(c, ix, table, bounds[s], stop) } else { 0 };
      prefix = prefix.with(row * 23 + s + 1, prefix[row * 23 + s] + sum);
    }
  }
  return Costs { prefix: prefix, escapes: escapes, largest: largest, segments: segments };
}

function largest(k: Costs, a: i32, b: i32): i32 {
  let top = 0;
  for s in a..b {
    if (k.largest[s] > top) {
      top = k.largest[s];
    }
  }
  return top;
}

// The least cost of segments a through b - 1, and the table that gives it.
function region(k: Costs, a: i32, b: i32): (i32, i32) {
  let top = largest(k, a, b);
  if (top == 0) {
    return (0, 0);
  }
  let best = 2147483647;
  let table = 0;
  for row in 0..13 {
    if (top <= CAPACITY[row]) {
      let cost = k.prefix[row * 23 + b] - k.prefix[row * 23 + a];
      if (cost < best) {
        best = cost;
        table = PLAIN[row];
      }
    }
  }
  for row in 13..15 {
    let t = linbits_for(if (row == 13) { 16 } else { 24 }, top);
    if (t != 0) {
      let cost = k.prefix[row * 23 + b] - k.prefix[row * 23 + a] + tables.G_LINBITS[t] * (k.escapes[b] - k.escapes[a]);
      if (cost < best) {
        best = cost;
        table = t;
      }
    }
  }
  return (best, table);
}

function smaller(a: i32, b: i32): i32 {
  return if (a < b) { a } else { b };
}

// Each region split is a count of bands in region 0 and region 1 of the side info.
// fern-lint: allow cyclomatic-complexity
pub function plan(c: Codes, ix: [i32], bounds: [i32]): Plan {
  let zero_end = 576;
  while (zero_end > 0 && ix[zero_end - 1] == 0 && ix[zero_end - 2] == 0) {
    zero_end -= 2;
  }
  let count1_start = zero_end;
  while (count1_start >= 4 && ix[count1_start - 1] <= 1 && ix[count1_start - 2] <= 1 && ix[count1_start - 3] <= 1 && ix[count1_start - 4] <= 1) {
    count1_start -= 4;
  }
  let cost_a = 0;
  let cost_b = 0;
  let i = count1_start;
  while (i < zero_end) {
    let quad = ix[i] << 3 | ix[i + 1] << 2 | ix[i + 2] << 1 | ix[i + 3];
    let signs = ix[i] + ix[i + 1] + ix[i + 2] + ix[i + 3];
    cost_a += c.quad_length[quad] + signs;
    cost_b += 4 + signs;
    i += 4;
  }
  let count1_table = if (cost_b < cost_a) { 1 } else { 0 };
  let count1_bits = smaller(cost_a, cost_b);
  let empty = Plan { bits: count1_bits, big: count1_start / 2, count1_end: zero_end, table0: 0, table1: 0, table2: 0, region0: 0, region1: 0, count1_table: count1_table };
  if (count1_start == 0) {
    return empty;
  }
  let k = costs(c, ix, bounds, count1_start);
  let n = k.segments;
  // Region 2 always ends at segment n, so its cost depends only on its start.
  let tail_bits = __alloc_i32(23);
  let tail_table = __alloc_i32(23);
  for start in 1..n + 1 {
    let (bits, table) = region(k, start, n);
    tail_bits = tail_bits.with(start, bits);
    tail_table = tail_table.with(start, table);
  }
  let best = empty;
  let best_bits = 2147483647;
  for r0 in 0..16 {
    let e0 = smaller(r0 + 1, n);
    let (b0, t0) = region(k, 0, e0);
    for r1 in 0..8 {
      if (r0 + r1 + 2 <= 22) {
        let e1 = smaller(r0 + r1 + 2, n);
        let (b1, t1) = region(k, e0, e1);
        let b2 = tail_bits[e1];
        let t2 = tail_table[e1];
        let total = b0 + b1 + b2;
        if (total < best_bits) {
          best_bits = total;
          best = Plan { ...empty, bits: count1_bits + total, table0: t0, table1: t1, table2: t2, region0: r0, region1: r1 };
        }
        if (r0 + r1 + 2 >= n) {
          break;
        }
      }
    }
    if (r0 + 1 >= n) {
      break;
    }
  }
  return best;
}

pub struct Bits { data: u8[], pos: i32 }

pub function (own b: Bits) put(value: i32, count: i32): Bits {
  let Bits { data, pos } = b;
  let left = count;
  while (left > 0) {
    let bit = pos & 7;
    let take = if (left < 8 - bit) { left } else { 8 - bit };
    let part = (value >> left - take & (1 << take) - 1) << 8 - bit - take;
    data = data.with(pos >> 3, data[pos >> 3] | part as u8);
    left -= take;
    pos += take;
  }
  return Bits { data: data, pos: pos };
}

function value(own b: Bits, magnitude: i32, negative: boolean, linbits: i32): Bits {
  if (linbits != 0 && magnitude >= 15) {
    b = b.put(magnitude - 15, linbits);
  }
  if (magnitude != 0) {
    b = b.put(if (negative) { 1 } else { 0 }, 1);
  }
  return b;
}

// `negative` holds one sign for each of the 576 lines.
pub function write(c: Codes, own b: Bits, ix: [i32], negative: [boolean], p: Plan, bounds: [i32]): Bits {
  let big = p.big * 2;
  let end0 = smaller(bounds[smaller(p.region0 + 1, 22)], big);
  let end1 = smaller(bounds[smaller(p.region0 + p.region1 + 2, 22)], big);
  let i = 0;
  while (i < big) {
    let table = if (i < end0) { p.table0 } else if (i < end1) { p.table1 } else { p.table2 };
    if (table != 0) {
      let x = ix[i];
      let y = ix[i + 1];
      let at = tree(table) * 256 + (if (x > 15) { 15 } else { x }) * 16 + (if (y > 15) { 15 } else { y });
      b = b.put(c.code[at], c.length[at]);
      b = value(b, x, negative[i], tables.G_LINBITS[table]);
      b = value(b, y, negative[i + 1], tables.G_LINBITS[table]);
    }
    i += 2;
  }
  while (i < p.count1_end) {
    let quad = ix[i] << 3 | ix[i + 1] << 2 | ix[i + 2] << 1 | ix[i + 3];
    if (p.count1_table != 0) {
      b = b.put(15 - quad, 4);
    } else {
      b = b.put(c.quad_code[quad], c.quad_length[quad]);
    }
    for k in 0..4 {
      b = value(b, ix[i + k], negative[i + k], 0);
    }
    i += 4;
  }
  return b;
}
```
