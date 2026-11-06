# The PVQ codewords of CELT

This module codes the shape of a CELT band (RFC 6716, section 4.3.4.2). It is a port of the `cwrs.c` file of libopus 1.4.

## Model

A codeword is a vector `y` of `n` integers. The sum of their absolute values is `k`, the number of pulses. `v(n, k)` is the number of such codewords. Each codeword has an index from 0 to `v(n, k) - 1`, and the range coder codes that index with `uint`.

`u(n, k)` counts the codewords of `n` integers with `k` pulses, where the first integer is not negative. `pvq_table` holds `u` for each `n` and `k` that a standard Opus mode can use.

## Contracts

- `n` must be 2 or more.
- `k` must be 1 or more, and `v(n, k)` must fit in 32 bits. The CELT bit allocation only gives such pairs.
- `decode_pulses` gives the codeword and the sum of the squares of its integers.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<counts>>

<<encoding>>

<<decoding>>
```

### `module introduction`

```fern
<<module introduction>>=
// The PVQ codewords of CELT, ported from libopus 1.4.
//
// Copyright (c) 2007-2008 CSIRO
// Copyright (c) 2007-2009 Xiph.Org Foundation
// Copyright (c) 2007-2009 Timothy B. Terriberry
// Written by Timothy B. Terriberry and Jean-Marc Valin
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions
// are met:
//
// - Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//
// - Redistributions in binary form must reproduce the above copyright
// notice, this list of conditions and the following disclaimer in the
// documentation and/or other materials provided with the distribution.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// ``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER
// OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
import "std/opus/pvq_table";
import "std/opus/range";
```

### `counts`

`u` is symmetric, so the table holds it only for `n` up to `k`.

```fern
<<counts>>=
pub function u(n: i32, k: i32): u32 {
  if (n <= k) {
    return pvq_table.DATA[pvq_table.ROW[n] + k];
  }
  return pvq_table.DATA[pvq_table.ROW[k] + n];
}

// The number of codewords of `n` integers with `k` pulses.
pub function v(n: i32, k: i32): u32 {
  return u(n, k) + u(n, k + 1);
}

function magnitude(x: i32): i32 {
  if (x < 0) {
    return -x;
  }
  return x;
}

function signed(x: i32, negative: boolean): i32 {
  if (negative) {
    return 0 - x;
  }
  return x;
}
```

### `encoding`

The index counts the codewords before `y` in an order that starts from the last integer.

```fern
<<encoding>>=
// The index of the codeword `y`.
pub function index(y: [i32]): u32 {
  let n = y.len();
  let j = n - 1;
  let i = 0u32;
  if (y[j] < 0) {
    i = 1u32;
  }
  let k = magnitude(y[j]);
  while (j > 0) {
    j -= 1;
    i += u(n - j, k);
    k += magnitude(y[j]);
    if (y[j] < 0) {
      i += u(n - j, k + 1);
    }
  }
  return i;
}

// Codes the codeword `y`, which has `k` pulses.
pub function encode_pulses(own e: range.Encoder, y: [i32], k: i32): range.Encoder {
  return e.uint(index(y), v(y.len(), k));
}
```

### `decoding`

Each step finds the integer at the front of the codeword and the pulses that are left. When `k` is `n` or more, the search goes down the row of `n`. Otherwise, it goes down the column of `n`.

```fern
<<decoding>>=
struct Step { index: u32, left: i32, negative: boolean }

// The largest pulse count from `start` down whose count of codewords with
// `dims` integers is not above `index`, and that count.
function search(start: i32, dims: i32, index: u32): (i32, u32) {
  let left = start;
  let p = u(left, dims);
  while (p > index) {
    left -= 1;
    p = u(left, dims);
  }
  return (left, p);
}

// The pulses left after the front integer of a codeword with `dims` integers,
// the index within the rest, and the sign of the front integer.
function front(dims: i32, k: i32, index: u32): Step {
  let i = index;
  let start = k - 1;
  let negative = false;
  if (k >= dims) {
    let p = u(dims, k + 1);
    negative = i >= p;
    if (negative) {
      i -= p;
    }
    start = k;
    if (u(dims, dims) > i) {
      start = dims - 1;
    }
  } else {
    let p = u(k, dims);
    let q = u(k + 1, dims);
    if (p <= i && i < q) {
      return Step { index: i - p, left: k, negative: false };
    }
    negative = i >= q;
    if (negative) {
      i -= q;
    }
  }
  let (left, p) = search(start, dims, i);
  return Step { index: i - p, left: left, negative: negative };
}

// The codeword of `n` integers and `k` pulses at `index`, and the sum of the
// squares of its integers.
pub function codeword(n: i32, k: i32, index: u32): (i32[], i32) {
  let y: i32[] = [];
  let yy = 0;
  let i = index;
  let left = k;
  for dims in 0..n - 2 {
    let st = front(n - dims, left, i);
    let val = signed(left - st.left, st.negative);
    y = y.append(val);
    yy += val * val;
    i = st.index;
    left = st.left;
  }
  let p2 = (2 * left + 1) as u32;
  let negative2 = i >= p2;
  if (negative2) {
    i -= p2;
  }
  let before2 = left;
  left = (i + 1u32 >> 1u32) as i32;
  if (left > 0) {
    i -= (2 * left - 1) as u32;
  }
  let a = signed(before2 - left, negative2);
  let b = signed(left, i != 0u32);
  return (y.append(a).append(b), yy + a * a + b * b);
}

// Decodes a codeword of `n` integers and `k` pulses, and the sum of the
// squares of its integers.
pub function decode_pulses(own d: range.Decoder, n: i32, k: i32): (range.Decoder, i32[], i32) {
  let (d1, i) = d.uint(v(n, k));
  let (y, yy) = codeword(n, k, i);
  return (d1, y, yy);
}
```
