# Lossless JPEG XL images

This module encodes lossless JPEG XL images with 8 bits for each sample.

## Model

The encoder writes one modular frame. The YCoCg transform of JPEG XL (RCT type 6) changes red, green and blue to Y, Co and Cg. The gradient predictor gives a residual for each sample. The decoder undoes the transform, so the pixels come back exactly.

The image is in groups of 256 by 256 pixels. An image of one group is in one section. A larger image has a section for the global data and a section for each group. Between them is an empty section for each LF group and one for the HF global data.

The residuals use prefix codes in the format of Brotli. A tree of one leaf for each channel gives each channel its own code. The tree and the codes are in the global data, and each group uses them.

A prefix code needs one bit or more for each symbol. Thus a flat area costs one bit for each sample. LZ77 removes that cost: a run of 7 or more residuals that are equal to the residual before is a copy of distance 1, as one symbol.

## Contracts

`encode_rgb8` and `encode_rgba8` accept a width and a height from 1 to 1073741824, and pixels with three or four bytes each. Other dimensions give `InvalidDimensions`, and pixels of a different length give `InvalidBufferLength`. The file has the color space sRGB. An RGBA image has an alpha channel, which is not premultiplied.

## Executable example

```fern test name=std-jxl-example
import "std/jxl";

function main(): i32 {
  let px: u8[] = [255, 0, 0, 0, 255, 0];
  let Ok(file) = jxl.encode_rgb8(2, 1, px) else { return 1; };
  if (file[0] != 255u8 || file[1] != 10u8) { return 2; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module encodes lossless JPEG XL images.
import "core/cmp";
import "core/error";
import "std/i32";

<<errors>>

<<bits>>

<<headers>>

<<prefix codes>>

<<the tree>>

<<residuals>>

<<encoding>>
```

### `errors`

```fern
<<errors>>=
pub enum EncodeError { InvalidDimensions, InvalidBufferLength }

pub function (e: EncodeError) message(): string {
  match (e) {
    InvalidDimensions => {
      return "jxl: the width and the height must be from 1 to 1073741824";
    },
    InvalidBufferLength => {
      return "jxl: the length of the pixels does not agree with the dimensions";
    }
  }
}

pub function (e: EncodeError) to_string(): string {
  return e.message();
}

impl error.Error for EncodeError {}

impl cmp.Display for EncodeError {}
```

### `bits`

The writer puts the first bit at the low end of each byte. A field of `n` bits has its low bit first.

```fern
<<bits>>=
struct Bits { out: u8[], acc: i64, n: i32 }

function bits(): Bits {
  return Bits { out: [], acc: 0i64, n: 0 };
}

function put(own b: Bits, count: i32, value: i32): Bits {
  if (count == 0) {
    return b;
  }
  let acc = b.acc | (value as i64 & (1i64 << count as i64) - 1i64) << b.n as i64;
  let n = b.n + count;
  let out = b.out;
  while (n >= 8) {
    out = out.append((acc & 255i64) as u8);
    acc >>= 8i64;
    n -= 8;
  }
  return Bits { out: out, acc: acc, n: n };
}

// The bits up to the next byte, as zeros.
function padded(own b: Bits): Bits {
  let rest = (8 - b.n) % 8;
  return put(b, rest, 0);
}

function bytes_of(own b: Bits): u8[] {
  return padded(b).out;
}
```

### `headers`

The size header writes the height and the width less one. Each has a selector of 2 bits for a field of 9, 13, 18 or 30 bits. The image metadata tells 8-bit integer samples, sRGB, no XYB, and an alpha channel for RGBA. The frame header tells a regular modular frame, groups of 256, one pass, no filters, and the last frame.

```fern
<<headers>>=
function size_field(own b: Bits, v: i32): Bits {
  let widths = [9, 13, 18, 30];
  let k = 0;
  while (v >= 1 << widths[k] && k < 3) {
    k += 1;
  }
  return put(put(b, 2, k), widths[k], v);
}

// The signature, the size header, the image metadata and the default
// transform data, to the end of the byte.
function headers(width: i32, height: i32, alpha: boolean): Bits {
  let b = put(put(bits(), 8, 255), 8, 10);
  b = put(b, 1, 0);
  b = size_field(b, height - 1);
  b = put(b, 3, 0);
  b = size_field(b, width - 1);
  b = put(put(put(put(b, 1, 0), 1, 0), 1, 0), 2, 0);
  b = put(b, 1, 1);
  b = if (alpha) { put(put(b, 2, 1), 1, 1) } else { put(b, 2, 0) };
  b = put(put(put(b, 1, 0), 1, 1), 2, 0);
  return padded(put(b, 1, 1));
}

function frame_header(own b: Bits, alpha: boolean): Bits {
  let extra = if (alpha) { 2 } else { 0 };
  b = put(put(put(b, 1, 0), 2, 0), 1, 1);
  b = put(put(put(b, 2, 0), 1, 0), 2, 0);
  b = put(b, extra, 0);
  b = put(put(put(put(b, 2, 1), 2, 0), 1, 0), 2, 0);
  b = put(b, extra, 0);
  b = put(put(b, 1, 1), 2, 0);
  b = put(put(put(b, 1, 0), 1, 0), 2, 0);
  return put(put(b, 2, 0), 2, 0);
}

// An entry of the table of contents: the size of a section in bytes.
function toc_entry(own b: Bits, size: i32): Bits {
  let offsets = [0, 1024, 17408, 4211712];
  let widths = [10, 14, 22, 30];
  let k = 0;
  while (k < 3 && size >= offsets[k + 1]) {
    k += 1;
  }
  return put(put(b, 2, k), widths[k], size - offsets[k]);
}
```

### `prefix codes`

A value is a token and extra bits. In the hybrid configuration with no split bits, token 0 is the value 0, and token `t` is a value of `t` bits: its high bit is in the token and the other `t - 1` bits follow.

A code length is at most 15 bits. When the Huffman code is longer, the method of Annex K.2 of JPEG moves codes to shorter lengths, and the code stays complete. A code of one to four symbols is a simple code of Brotli. A longer code writes its lengths with a code-length code of at most 5 bits, in the order of Brotli. That code needs two different lengths or more. When all the symbols have one length k, three symbols get the lengths k - 1, k + 1 and k + 1.

```fern
<<prefix codes>>=
// The value `v` as a signed integer packed into an unsigned one.
function packed(v: i32): i32 {
  return if (v >= 0) { 2 * v } else { -2 * v - 1 };
}

function token(v: i32): i32 {
  let t = 0;
  while (v >> t > 0) {
    t += 1;
  }
  return t;
}

function used_symbols(counts: [i64]): i32[] {
  let used: i32[] = [];
  for i in 0..counts.len() {
    if (counts[i] > 0i64) {
      used = used.append(i);
    }
  }
  return used;
}

// The count of codes of each length of a Huffman code for the counts of
// the symbols `used`.
function depths(counts: [i64], used: [i32]): i32[] {
  let weight: i64[] = [];
  let parent: i32[] = [];
  let live: boolean[] = [];
  for s in used {
    weight = weight.append(counts[s]);
    parent = parent.append(-1);
    live = live.append(true);
  }
  for step in 1..used.len() {
    let (a, b) = two_least(weight, live);
    let node = weight.len();
    weight = weight.append(weight[a] + weight[b]);
    parent = parent.append(-1).with(a, node).with(b, node);
    live = live.append(true).with(a, false).with(b, false);
  }
  let count_of = __alloc_i32(64);
  for k in 0..used.len() {
    let d = 0;
    let p = parent[k];
    while (p >= 0) {
      d += 1;
      p = parent[p];
    }
    count_of = count_of.with(d, count_of[d] + 1);
  }
  return count_of;
}

function two_least(weight: [i64], live: [boolean]): (i32, i32) {
  let a = -1;
  let b = -1;
  for i in 0..weight.len() {
    if (live[i] && (a < 0 || weight[i] < weight[a])) {
      b = a;
      a = i;
    } else if (live[i] && (b < 0 || weight[i] < weight[b])) {
      b = i;
    }
  }
  return (a, b);
}

// The counts of each length with no code longer than `most`.
function limited(own count_of: i32[], most: i32): i32[] {
  let i = count_of.len() - 1;
  while (i > most) {
    while (count_of[i] > 0) {
      let j = i - 2;
      while (count_of[j] == 0) {
        j -= 1;
      }
      count_of = count_of.with(i, count_of[i] - 2);
      count_of = count_of.with(i - 1, count_of[i - 1] + 1);
      count_of = count_of.with(j + 1, count_of[j + 1] + 2);
      count_of = count_of.with(j, count_of[j] - 1);
    }
    i -= 1;
  }
  return count_of;
}

// The symbols `used`, the most frequent first.
function by_count(own order: i32[], counts: [i64]): i32[] {
  for x in 1..order.len() {
    let y = x;
    while (y > 0 && counts[order[y - 1]] < counts[order[y]]) {
      let t = order[y - 1];
      order = order.with(y - 1, order[y]).with(y, t);
      y -= 1;
    }
  }
  return order;
}

// The code lengths of a Huffman code for the counts, none longer than
// `most`. A symbol with no count has no code.
function code_lengths(counts: [i64], most: i32): i32[] {
  let lens = __alloc_i32(counts.len());
  let used = used_symbols(counts);
  if (used.len() == 1) {
    return lens.with(used[0], 1);
  }
  let count_of = limited(depths(counts, used), most);
  let order = by_count(used, counts);
  let at = 0;
  for len in 1..most + 1 {
    for c in 0..count_of[len] {
      lens = lens.with(order[at], len);
      at += 1;
    }
  }
  return lens;
}

// The lengths, with two different values or more when the code is complex
// and every symbol has a code.
function uneven(own lens: i32[]): i32[] {
  let used = 0;
  let same = true;
  for l in lens {
    if (l > 0) {
      used += 1;
      same = same && l == lens[0];
    }
  }
  if (!same || used <= 4 || used != lens.len() || lens[0] >= 15) {
    return lens;
  }
  return lens.with(0, lens[0] - 1).with(1, lens[0] + 1).with(2, lens[0] + 1);
}

function reversed(v: i32, n: i32): i32 {
  let r = 0;
  for i in 0..n {
    r = r << 1 | v >> i & 1;
  }
  return r;
}

// The canonical codes of the lengths, by length and then by symbol, each
// reversed for the writer.
function canonical(lens: [i32]): i32[] {
  let codes = __alloc_i32(lens.len());
  let code = 0;
  for len in 1..16 {
    for s in 0..lens.len() {
      if (lens[s] == len) {
        codes = codes.with(s, reversed(code, len));
        code += 1;
      }
    }
    code <<= 1;
  }
  return codes;
}

// An alphabet size: 1, or 1 + 2^n + an n-bit number.
function put_alphabet(own b: Bits, count: i32): Bits {
  if (count <= 1) {
    return put(b, 1, 0);
  }
  let n = token(count - 1) - 1;
  return put(put(put(b, 1, 1), 4, n), n, count - 1 - (1 << n));
}

function symbol_bits(alphabet: i32): i32 {
  return token(alphabet - 1);
}

// The prefix code of the lengths `lens`, for an alphabet of `lens.len()`
// symbols.
function put_prefix_code(own b: Bits, lens: [i32]): Bits {
  let used: i32[] = [];
  for s in 0..lens.len() {
    if (lens[s] > 0) {
      used = used.append(s);
    }
  }
  if (used.len() <= 4) {
    return put_simple(b, lens, used, symbol_bits(lens.len()));
  }
  return put_complex(b, lens, used[used.len() - 1]);
}

// A simple code lists its symbols: the symbol of length 1 first, then the
// symbol of length 2, then the others. Four symbols of length 2 have the
// flag 0, and the lengths 1, 2, 3 and 3 have the flag 1.
function put_simple(own b: Bits, lens: [i32], used: [i32], width: i32): Bits {
  b = put(put(b, 2, 1), 2, used.len() - 1);
  for len in 1..4 {
    for s in used {
      if (lens[s] == len) {
        b = put(b, width, s);
      }
    }
  }
  if (used.len() == 4) {
    b = put(b, 1, if (lens[used[0]] == 2 && lens[used[1]] == 2) { 0 } else { 1 });
  }
  return b;
}

// The static code of a length of the code-length code.
function put_clen(own b: Bits, v: i32): Bits {
  return put(b, [2, 4, 3, 2, 2, 4][v], [0, 7, 3, 2, 1, 15][v]);
}

function put_complex(own b: Bits, lens: [i32], last: i32): Bits {
  let freq = __alloc_i64(16);
  for s in 0..last + 1 {
    freq = freq.with(lens[s], freq[lens[s]] + 1i64);
  }
  let cl = code_lengths(freq, 5);
  let order = [1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15];
  let last_cl = 0;
  for k in 0..18 {
    if (order[k] < 16 && cl[order[k]] > 0) {
      last_cl = k;
    }
  }
  b = put(b, 2, 0);
  for k in 0..last_cl + 1 {
    b = put_clen(b, if (order[k] < 16) { cl[order[k]] } else { 0 });
  }
  let codes = canonical(cl);
  for s in 0..last + 1 {
    b = put(b, cl[lens[s]], codes[lens[s]]);
  }
  return b;
}

// The lengths of the codes that the decoder builds: a simple code of one
// symbol has a code of 0 bits.
function data_lengths(lens: [i32]): i32[] {
  let used = 0;
  let only = 0;
  for s in 0..lens.len() {
    if (lens[s] > 0) {
      used += 1;
      only = s;
    }
  }
  let out = __alloc_i32(lens.len());
  for s in 0..lens.len() {
    out = out.with(s, if (used == 1) { 0 } else { lens[s] });
  }
  return out;
}
```

### `the tree`

The tree has its own stream: six contexts in one cluster, and a simple code of the tokens 0 to 3. The decoder reads the nodes breadth first. A node gives a property and a value, and its first child is the branch where the property is above the value. A leaf gives the gradient predictor (5), an offset of 0 and a multiplier of 1. With the property of the channel (0), the leaves are the channels from the last one down.

```fern
<<the tree>>=
// A value of the tree: the canonical codes 00, 01, 10 and 11 of the tokens 0
// to 3, low bit first, then the extra bits.
function put_tree_value(own b: Bits, v: i32): Bits {
  let t = token(v);
  b = put(b, 2, [0, 2, 1, 3][t]);
  return put(b, (t - 1).max(0), v - (1 << t - 1));
}

function put_leaf(own b: Bits): Bits {
  for v in [0, 5, 0, 0, 0] {
    b = put_tree_value(b, v);
  }
  return b;
}

// A node that asks whether the channel is above `split`.
function put_channel_node(own b: Bits, split: i32): Bits {
  return put_tree_value(put_tree_value(b, 1), packed(split));
}

function put_tree(own b: Bits, channels: i32): Bits {
  b = put(put(put(b, 1, 0), 1, 1), 2, 0);
  b = put(put(b, 1, 1), 4, 0);
  b = put_alphabet(b, 4);
  b = put(put(b, 2, 1), 2, 3);
  for s in 0..4 {
    b = put(b, 2, s);
  }
  b = put(b, 1, 0);
  if (channels == 3) {
    return put_leaf(put_leaf(put_channel_node(put_leaf(put_channel_node(b, 1)), 0)));
  }
  b = put_channel_node(put_channel_node(put_channel_node(b, 1), 2), 0);
  for k in 0..4 {
    b = put_leaf(b);
  }
  return b;
}

// The context of the leaf of channel `c`.
function context_of(c: i32, channels: i32): i32 {
  return channels - 1 - c;
}
```

### `residuals`

The YCoCg transform is the inverse of the one that the decoder applies: `tmp = Y - (Cg >> 1)`, `G = Cg + tmp`, `B = tmp - (Co >> 1)` and `R = B + Co`. The gradient predictor clamps `W + N - NW` between `W` and `N`. On the first row and the first column of a group, some neighbors are not in the group. They take the values that the specification gives them.

```fern
<<residuals>>=
// The symbols of a group in the order that the decoder reads them: the
// context of each, the symbol, and its extra bits.
struct Stream { ctx: i32[], sym: i32[], nbits: i32[], extra: i32[] }

// LZ77: a symbol from 224 is a copy of the value before, whose length less
// 7 is a value of the hybrid configuration 4/0/0.
const COPY_SYMBOL: i32 = 224;

const COPY_MIN: i32 = 7;

function push(own s: Stream, ctx: i32, sym: i32, nbits: i32, extra: i32): Stream {
  return Stream { ctx: s.ctx.append(ctx), sym: s.sym.append(sym), nbits: s.nbits.append(nbits), extra: s.extra.append(extra) };
}

// The value `r` as its token and extra bits.
function push_value(own s: Stream, ctx: i32, r: i32): Stream {
  let t = token(r);
  return push(s, ctx, t, (t - 1).max(0), if (t == 0) { 0 } else { r - (1 << t - 1) });
}

// A run of the value before, with the context of each of its first
// COPY_MIN samples: values one by one when the run is short, else one copy
// of distance 1 in the context of its first sample.
function push_run(own s: Stream, ctxs: [i32], value: i32, run: i32): Stream {
  if (run < COPY_MIN) {
    for k in 0..run {
      s = push_value(s, ctxs[k], value);
    }
    return s;
  }
  let ctx = ctxs[0];
  let v = run - COPY_MIN;
  if (v < 16) {
    return push(s, ctx, COPY_SYMBOL + v, 0, 0);
  }
  let n = token(v) - 1;
  return push(s, ctx, COPY_SYMBOL + 12 + n, n, v - (1 << n));
}

function channels_of(px: [u8], n: i32, bpp: i32): i32[][] {
  let y = __alloc_i32(n);
  let co = __alloc_i32(n);
  let cg = __alloc_i32(n);
  for i in 0..n {
    let r = px[i * bpp] as i32;
    let b = px[i * bpp + 2] as i32;
    let o = r - b;
    let tmp = b + (o >> 1);
    let g = px[i * bpp + 1] as i32 - tmp;
    y = y.with(i, tmp + (g >> 1));
    co = co.with(i, o);
    cg = cg.with(i, g);
  }
  if (bpp == 3) {
    return [y, co, cg];
  }
  let a = __alloc_i32(n);
  for i in 0..n {
    a = a.with(i, px[i * 4 + 3] as i32);
  }
  return [y, co, cg, a];
}

// The prediction of the sample at `at`, which is at x and y in its group.
function gradient(p: [i32], at: i32, x: i32, y: i32, width: i32): i32 {
  let left = if (x > 0) { p[at - 1] } else if (y > 0) { p[at - width] } else { 0 };
  let top = if (y > 0) { p[at - width] } else { left };
  let corner = if (x > 0 && y > 0) { p[at - width - 1] } else { left };
  return (left + top - corner).max(left.min(top)).min(left.max(top));
}

// The symbols of each channel of a group. A run of the value before
// becomes a copy. A copy has the context of its first sample.
function residuals(planes: [i32[]], width: i32, x0: i32, y0: i32, w: i32, h: i32): Stream {
  let s = Stream { ctx: [], sym: [], nbits: [], extra: [] };
  let last = -1;
  let run = 0;
  let run_ctxs: i32[] = [];
  for c in 0..planes.len() {
    let p = planes[c];
    let ctx = context_of(c, planes.len());
    for y in 0..h {
      for x in 0..w {
        let at = (y0 + y) * width + x0 + x;
        let r = packed(p[at] - gradient(p, at, x, y, width));
        if (r == last) {
          if (run < COPY_MIN) {
            run_ctxs = run_ctxs.append(ctx);
          }
          run += 1;
        } else {
          s = push_value(push_run(s, run_ctxs, last, run), ctx, r);
          run = 0;
          run_ctxs = [];
          last = r;
        }
      }
    }
  }
  return push_run(s, run_ctxs, last, run);
}

// The symbols of a group, with the code of the context of each. The
// distance of a copy has a code of one symbol, which takes no bits.
function put_stream(own b: Bits, s: Stream, lens: [i32[]], codes: [i32[]]): Bits {
  for i in 0..s.sym.len() {
    let k = s.ctx[i];
    let t = s.sym[i];
    b = put(put(b, lens[k][t], codes[k][t]), s.nbits[i], s.extra[i]);
  }
  return b;
}
```

### `encoding`

The global data is the default dequantization, the flag of the global tree, the tree, and the stream of the residuals.

The stream starts with the LZ77 parameters: copies from the symbol 224, a length of 7 or more, and the hybrid configuration 4/0/0 for the length. The context map gives each context its own cluster, and the distances the last one. Then come the flag of prefix codes, a hybrid configuration of 0 bits for each cluster, the alphabet sizes and the codes.

The distance code has the one symbol 1, which is the special distance of the sample before. Then the header of the global image has the YCoCg transform. The header of each group has none.

```fern
<<encoding>>=
pub function encode_rgb8(width: i32, height: i32, pixels: [u8]): Result[u8[], EncodeError] {
  return encode(width, height, pixels, 3);
}

pub function encode_rgba8(width: i32, height: i32, pixels: [u8]): Result[u8[], EncodeError] {
  return encode(width, height, pixels, 4);
}

const GROUP: i32 = 256;

// The residuals of the groups, the samples of each channel of each group,
// and the counts of the tokens of each context.
struct Groups { streams: Stream[], counts: i64[] }

function groups_of(px: [u8], width: i32, height: i32, bpp: i32): Groups {
  let planes = channels_of(px, width * height, bpp);
  let gx = (width + GROUP - 1) / GROUP;
  let out: Stream[] = [];
  let counts = __alloc_i64(ALPHABET * bpp);
  for g in 0..gx * ((height + GROUP - 1) / GROUP) {
    let x0 = g % gx * GROUP;
    let y0 = g / gx * GROUP;
    let s = residuals(planes, width, x0, y0, (width - x0).min(GROUP), (height - y0).min(GROUP));
    for i in 0..s.sym.len() {
      let k = s.ctx[i] * ALPHABET + s.sym[i];
      counts = counts.with(k, counts[k] + 1i64);
    }
    out = out.append(s);
  }
  return Groups { streams: out, counts: counts };
}

// The symbols of a context: the values, and the copies from 224.
const ALPHABET: i32 = 256;

// The code lengths of context `k`.
function context_lengths(counts: [i64], k: i32): i32[] {
  let alphabet = 2;
  for t in 0..ALPHABET {
    if (counts[k * ALPHABET + t] > 0i64) {
      alphabet = alphabet.max(t + 1);
    }
  }
  let freq = __alloc_i64(alphabet);
  let any = false;
  for t in 0..alphabet {
    freq = freq.with(t, counts[k * ALPHABET + t]);
    any = any || freq[t] > 0i64;
  }
  // A copy can cover each sample of a context. Its code still needs a
  // symbol.
  if (!any) {
    freq = freq.with(0, 1i64);
  }
  return uneven(code_lengths(freq, 15));
}

// The global data, and the lengths of the codes of the data of each
// context.
function global_data(counts: [i64], channels: i32): (Bits, i32[][]) {
  let b = put_tree(put(put(bits(), 1, 1), 1, 1), channels);
  b = put(put(put(b, 1, 1), 2, 0), 4, 10);
  b = put(put(put(b, 4, 4), 3, 0), 3, 0);
  b = put(put(b, 1, 1), 2, 3);
  for k in 0..channels + 1 {
    b = put(b, 3, k);
  }
  b = put(b, 1, 1);
  for k in 0..channels + 1 {
    b = put(b, 4, 0);
  }
  let all: i32[][] = [];
  for k in 0..channels {
    let lens = context_lengths(counts, k);
    b = put_alphabet(b, lens.len());
    all = all.append(lens);
  }
  b = put_alphabet(b, 2);
  let data: i32[][] = [];
  for lens in all {
    b = put_prefix_code(b, lens);
    data = data.append(data_lengths(lens));
  }
  b = put_prefix_code(b, [0, 1]);
  b = put(put(put(b, 1, 1), 1, 1), 2, 1);
  return (put(put(put(b, 2, 0), 5, 0), 2, 0), data);
}

// The modular header of a group: the global tree, the default weighted
// predictor and no transforms.
function group_header(): Bits {
  return put(put(put(bits(), 1, 1), 1, 1), 2, 0);
}

function encode(width: i32, height: i32, px: [u8], bpp: i32): Result[u8[], EncodeError] {
  if (width < 1 || height < 1 || width > 1073741824 || height > 1073741824) {
    return Err(InvalidDimensions);
  }
  if (px.len() as i64 != width as i64 * (height as i64) * (bpp as i64)) {
    return Err(InvalidBufferLength);
  }
  let gs = groups_of(px, width, height, bpp);
  let (global, lens) = global_data(gs.counts, bpp);
  let codes: i32[][] = [];
  for l in lens {
    codes = codes.append(canonical(l));
  }
  let sections: u8[][] = [];
  if (gs.streams.len() == 1) {
    sections = [bytes_of(put_stream(global, gs.streams[0], lens, codes))];
  } else {
    sections = [bytes_of(global)];
    for i in 0..(width + 2047) / 2048 * ((height + 2047) / 2048) + 1 {
      sections = sections.append([]);
    }
    for g in 0..gs.streams.len() {
      sections = sections.append(bytes_of(put_stream(group_header(), gs.streams[g], lens, codes)));
    }
  }
  return Ok(assembled(width, height, bpp == 4, sections));
}

// The headers, the table of contents and the sections.
function assembled(width: i32, height: i32, alpha: boolean, sections: [u8[]]): u8[] {
  let b = padded(put(frame_header(headers(width, height, alpha), alpha), 1, 0));
  for s in sections {
    b = toc_entry(b, s.len());
  }
  let out = bytes_of(b);
  for s in sections {
    for x in s {
      out = out.append(x);
    }
  }
  return out;
}
```
