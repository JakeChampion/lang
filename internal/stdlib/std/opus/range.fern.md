# The Opus range coder

This module is the entropy coder of Opus (RFC 6716, section 4.1). It is a port of `entcode.c`, `entenc.c` and `entdec.c` from libopus 1.4. For the same calls, it gives the same bytes and the same values as libopus.

## Model

A range coder codes each symbol as a part of a range of 32-bit integers. The caller gives three numbers for each symbol. `fl` is the cumulative frequency of the symbol, `fh` is the cumulative frequency of the next symbol, and `ft` is the total. The coder writes the range bytes from the start of the buffer.

Raw bits do not go through the range. The encoder writes them from the end of the buffer back to the start. Thus the two streams share one buffer of fixed size. The decoder reads them in the same order.

An `Encoder` and a `Decoder` are values. Each call takes the coder as `own` and gives the next coder. A call that decodes a value gives the next decoder and the value as a pair.

`tell` gives the number of bits used so far, rounded up. `tell_frac` gives the same number in eighths of a bit. After the same symbols, the encoder and the decoder give the same numbers. Thus the two sides can use them to make the same decisions.

## Contracts

- `encode` and `decode` take a total `ft` from 2 to 65535. `encode_bin` and `decode_bin` take a total of 2 to the power 1 to 15.
- `uint` takes a total of 2 or more.
- `bits` and `raw` take from 1 to 25 raw bits.
- After `done`, `bytes` gives the packet. It has `storage` bytes, with zeros between the two streams.
- `error` is true when the encoder has no more space. It is also true when the decoder finds a value that the encoder cannot make. Neither coder stops at an error, but the data after it is not valid.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<shared>>

<<encoder>>

<<decoder>>
```

### `module introduction`

```fern
<<module introduction>>=
// The Opus range coder, ported from libopus 1.4.
//
// Copyright (c) 2001-2011 Timothy B. Terriberry
// Copyright (c) 2008-2009 Xiph.Org Foundation
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
```

### `shared`

A symbol is 8 bits. The range has 32 bits, and the top bit is free to hold a carry. `CODE_SHIFT` gives the top symbol of the range. `CODE_EXTRA` is the number of bits that the decoder holds below the top symbol.

```fern
<<shared>>=
const SYM_MAX: u32 = 255u32;

const ALL: u32 = 4294967295u32;

const CODE_TOP: u32 = 2147483648u32;

const CODE_BOT: u32 = 8388608u32;

const CODE_SHIFT: i32 = 23;

const CODE_EXTRA: i32 = 7;

const UINT_BITS: i32 = 8;

const WINDOW_SIZE: i32 = 32;

const FRAC_CORRECTION: i32[] = [35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535];

// The number of bits in `v`, which must not be 0.
function ilog(v: u32): i32 {
  return 32 - __clz32(v);
}

function tell_of(nbits_total: i32, rng: u32): i32 {
  return nbits_total - ilog(rng);
}

// A linear step from the top 16 bits of the range, then a correction where
// the step is one eighth too low.
function tell_frac_of(nbits_total: i32, rng: u32): i32 {
  let l: i32 = ilog(rng);
  let r: i32 = (rng >> (l - 16) as u32) as i32;
  let b: i32 = (r >> 12) - 8;
  if (r > FRAC_CORRECTION[b]) {
    b = b + 1;
  }
  return (nbits_total << 3) - (l << 3) - b;
}
```

### `encoder`

The encoder holds the top byte of the range in `rem` until it knows that no carry can change it. `ext` counts the 255 bytes after `rem`. A carry also changes these bytes.

```fern
<<encoder>>=
pub struct Encoder { buf: u8[], storage: i32, offs: i32, end_offs: i32, end_window: u32, nend_bits: i32, nbits_total: i32, rng: u32, val: u32, rem: i32, ext: i32, error: boolean }

// An encoder that writes a packet of `size` bytes.
pub function encoder(size: i32): Encoder {
  let buf: u8[] = [];
  for i in 0..size {
    buf = buf.append(0 as u8);
  }
  return Encoder { buf: buf, storage: size, offs: 0, end_offs: 0, end_window: 0u32, nend_bits: 0, nbits_total: 33, rng: CODE_TOP, val: 0u32, rem: 0 - 1, ext: 0, error: false };
}

pub function (e: Encoder) tell(): i32 {
  return tell_of(e.nbits_total, e.rng);
}

pub function (e: Encoder) tell_frac(): i32 {
  return tell_frac_of(e.nbits_total, e.rng);
}

// The packet. It is complete only after `done`.
pub function (e: Encoder) bytes(): u8[] {
  return e.buf;
}

function put(own e: Encoder, v: u32): Encoder {
  if (e.offs + e.end_offs >= e.storage) {
    return Encoder { ...e, error: true };
  }
  let at: i32 = e.offs;
  return Encoder { ...e, buf: e.buf.with(at, (v & SYM_MAX) as u8), offs: at + 1 };
}

function put_at_end(own e: Encoder, v: u32): Encoder {
  if (e.offs + e.end_offs >= e.storage) {
    return Encoder { ...e, error: true };
  }
  let n: i32 = e.end_offs + 1;
  return Encoder { ...e, buf: e.buf.with(e.storage - n, (v & SYM_MAX) as u8), end_offs: n };
}

function carry_out(own e: Encoder, c: u32): Encoder {
  if (c == SYM_MAX) {
    return Encoder { ...e, ext: e.ext + 1 };
  }
  let carry: u32 = c >> 8u32;
  let rem: i32 = e.rem;
  if (rem >= 0) {
    e = put(e, rem as u32 + carry);
  }
  let sym: u32 = SYM_MAX + carry & SYM_MAX;
  for i in 0..e.ext {
    e = put(e, sym);
  }
  return Encoder { ...e, ext: 0, rem: (c & SYM_MAX) as i32 };
}

function normalize(own e: Encoder): Encoder {
  while (e.rng <= CODE_BOT) {
    let top: u32 = e.val >> CODE_SHIFT as u32;
    e = carry_out(e, top);
    e = Encoder { ...e, val: e.val << 8u32 & CODE_TOP - 1u32, rng: e.rng << 8u32, nbits_total: e.nbits_total + 8 };
  }
  return e;
}

// Codes the symbol from `fl` up to `fh` out of a total of `ft`.
pub function (own e: Encoder) encode(fl: i32, fh: i32, ft: i32): Encoder {
  let r: u32 = e.rng / (ft as u32);
  if (fl > 0) {
    e = Encoder { ...e, val: e.val + (e.rng - r * ((ft - fl) as u32)), rng: r * ((fh - fl) as u32) };
  } else {
    e = Encoder { ...e, rng: e.rng - r * ((ft - fh) as u32) };
  }
  return normalize(e);
}

// Like `encode` with a total of 2 to the power `bits`.
pub function (own e: Encoder) encode_bin(fl: i32, fh: i32, bits: i32): Encoder {
  let r: u32 = e.rng >> bits as u32;
  let ft: i32 = 1 << bits;
  if (fl > 0) {
    e = Encoder { ...e, val: e.val + (e.rng - r * ((ft - fl) as u32)), rng: r * ((fh - fl) as u32) };
  } else {
    e = Encoder { ...e, rng: e.rng - r * ((ft - fh) as u32) };
  }
  return normalize(e);
}

// Codes a bit that is true with the probability 1 / 2 to the power `logp`.
pub function (own e: Encoder) bit_logp(v: boolean, logp: i32): Encoder {
  let s: u32 = e.rng >> logp as u32;
  let r: u32 = e.rng - s;
  if (v) {
    e = Encoder { ...e, val: e.val + r, rng: s };
  } else {
    e = Encoder { ...e, rng: r };
  }
  return normalize(e);
}

// Codes the symbol `s` of an inverse cumulative table with a total of 2 to
// the power `ftb`. The table falls to 0 at its last symbol.
pub function (own e: Encoder) icdf(s: i32, table: [u8], ftb: i32): Encoder {
  let r: u32 = e.rng >> ftb as u32;
  let hi: u32 = table[s] as u32;
  if (s > 0) {
    let lo: u32 = table[s - 1] as u32;
    e = Encoder { ...e, val: e.val + (e.rng - r * lo), rng: r * (lo - hi) };
  } else {
    e = Encoder { ...e, rng: e.rng - r * hi };
  }
  return normalize(e);
}

// Codes `fl`, which is less than `ft`, with an equal probability for each value.
pub function (own e: Encoder) uint(fl: u32, ft: u32): Encoder {
  let top: u32 = ft - 1u32;
  let ftb: i32 = ilog(top);
  if (ftb <= UINT_BITS) {
    return e.encode(fl as i32, fl as i32 + 1, top as i32 + 1);
  }
  let low: i32 = ftb - UINT_BITS;
  let f: i32 = (fl >> low as u32) as i32;
  e = e.encode(f, f + 1, (top >> low as u32) as i32 + 1);
  return e.bits(fl & (1u32 << low as u32) - 1u32, low);
}

// Writes the low `n` bits of `v` as raw bits.
pub function (own e: Encoder) bits(v: u32, n: i32): Encoder {
  let window: u32 = e.end_window;
  let used: i32 = e.nend_bits;
  if (used + n > WINDOW_SIZE) {
    while (used >= 8) {
      e = put_at_end(e, window);
      window = window >> 8u32;
      used = used - 8;
    }
  }
  return Encoder { ...e, end_window: window | v << used as u32, nend_bits: used + n, nbits_total: e.nbits_total + n };
}

// Replaces the first `n` bits of the packet with `v`, after they were coded.
pub function (own e: Encoder) patch_initial_bits(v: u32, n: i32): Encoder {
  let shift: i32 = 8 - n;
  let mask: u32 = (1u32 << n as u32) - 1u32 << shift as u32;
  if (e.offs > 0) {
    let first: u32 = e.buf[0] as u32 & (mask ^ ALL) | v << shift as u32;
    return Encoder { ...e, buf: e.buf.with(0, first as u8) };
  }
  if (e.rem >= 0) {
    return Encoder { ...e, rem: (e.rem as u32 & (mask ^ ALL) | v << shift as u32) as i32 };
  }
  if (e.rng <= CODE_TOP >> n as u32) {
    let at: u32 = (CODE_SHIFT + shift) as u32;
    return Encoder { ...e, val: e.val & (mask << CODE_SHIFT as u32 ^ ALL) | v << at };
  }
  return Encoder { ...e, error: true };
}

// Makes the packet `size` bytes, which must hold what is coded so far.
pub function (own e: Encoder) shrink(size: i32): Encoder {
  let buf: u8[] = e.buf;
  for i in 0..e.end_offs {
    buf = buf.with(size - e.end_offs + i, buf[e.storage - e.end_offs + i]);
  }
  return Encoder { ...e, buf: buf.truncate(size), storage: size };
}

// Writes the fewest bits that decode as the coded symbols, whatever bits
// follow, and then the raw bits that are still in the window.
pub function (own e: Encoder) done(): Encoder {
  let l: i32 = 32 - ilog(e.rng);
  let msk: u32 = CODE_TOP - 1u32 >> l as u32;
  let end: u32 = e.val + msk & (msk ^ ALL);
  if ((end | msk) >= e.val + e.rng) {
    l = l + 1;
    msk = msk >> 1u32;
    end = e.val + msk & (msk ^ ALL);
  }
  while (l > 0) {
    e = carry_out(e, end >> CODE_SHIFT as u32);
    end = end << 8u32 & CODE_TOP - 1u32;
    l = l - 8;
  }
  if (e.rem >= 0 || e.ext > 0) {
    e = carry_out(e, 0u32);
  }
  let window: u32 = e.end_window;
  let used: i32 = e.nend_bits;
  while (used >= 8) {
    e = put_at_end(e, window);
    window = window >> 8u32;
    used = used - 8;
  }
  if (e.error) {
    return e;
  }
  let buf: u8[] = e.buf;
  for i in e.offs..e.storage - e.end_offs {
    buf = buf.with(i, 0 as u8);
  }
  e = Encoder { ...e, buf: buf };
  if (used <= 0) {
    return e;
  }
  if (e.end_offs >= e.storage) {
    return Encoder { ...e, error: true };
  }
  let room: i32 = 0 - l;
  if (e.offs + e.end_offs >= e.storage && room < used) {
    window = window & (1u32 << room as u32) - 1u32;
    e = Encoder { ...e, error: true };
  }
  let last: i32 = e.storage - e.end_offs - 1;
  return Encoder { ...e, buf: e.buf.with(last, (e.buf[last] as u32 | window & SYM_MAX) as u8) };
}
```

### `decoder`

`val` is the distance from the top of the range down to the coded value. `rem` is the last byte that the decoder read. `ext` is the size of one step of the total in the last `decode`, and `update` uses it.

```fern
<<decoder>>=
pub struct Decoder { buf: u8[], storage: i32, offs: i32, end_offs: i32, end_window: u32, nend_bits: i32, nbits_total: i32, rng: u32, val: u32, ext: u32, rem: i32, error: boolean }

// The next byte of the range data, or 0 past the end of the packet.
function read(d: Decoder): (i32, i32) {
  if (d.offs < d.storage) {
    return (d.buf[d.offs] as i32, d.offs + 1);
  }
  return (0, d.offs);
}

function renormalize(own d: Decoder): Decoder {
  while (d.rng <= CODE_BOT) {
    let (byte, offs) = read(d);
    let sym: u32 = ((d.rem << 8 | byte) >> 8 - CODE_EXTRA) as u32;
    d = Decoder { ...d, nbits_total: d.nbits_total + 8, rng: d.rng << 8u32, rem: byte, offs: offs, val: (d.val << 8u32) + (sym & SYM_MAX ^ SYM_MAX) & CODE_TOP - 1u32 };
  }
  return d;
}

// A decoder that reads `packet`.
pub function decoder(packet: [u8]): Decoder {
  let buf: u8[] = [];
  for b in packet {
    buf = buf.append(b);
  }
  let d: Decoder = Decoder { buf: buf, storage: packet.len(), offs: 0, end_offs: 0, end_window: 0u32, nend_bits: 0, nbits_total: 9, rng: 128u32, val: 0u32, ext: 0u32, rem: 0, error: false };
  let (first, offs) = read(d);
  d = Decoder { ...d, rem: first, offs: offs, val: (127 - (first >> 1)) as u32 };
  return renormalize(d);
}

pub function (d: Decoder) tell(): i32 {
  return tell_of(d.nbits_total, d.rng);
}

pub function (d: Decoder) tell_frac(): i32 {
  return tell_frac_of(d.nbits_total, d.rng);
}

// The cumulative frequency of the next symbol, out of a total of `ft`. The
// caller finds the symbol and then calls `update` with the same `ft`.
pub function (own d: Decoder) decode(ft: i32): (Decoder, i32) {
  let ext: u32 = d.rng / (ft as u32);
  let s: u32 = d.val / ext + 1u32;
  let f: i32 = ft;
  if (s < ft as u32) {
    f = s as i32;
  }
  return (Decoder { ...d, ext: ext }, ft - f);
}

// Like `decode` with a total of 2 to the power `bits`.
pub function (own d: Decoder) decode_bin(bits: i32): (Decoder, i32) {
  let ext: u32 = d.rng >> bits as u32;
  let s: u32 = d.val / ext + 1u32;
  let ft: i32 = 1 << bits;
  let f: i32 = ft;
  if (s < ft as u32) {
    f = s as i32;
  }
  return (Decoder { ...d, ext: ext }, ft - f);
}

// Moves past the symbol from `fl` up to `fh` that `decode` found.
pub function (own d: Decoder) update(fl: i32, fh: i32, ft: i32): Decoder {
  let s: u32 = d.ext * ((ft - fh) as u32);
  let rng: u32 = d.rng - s;
  if (fl > 0) {
    rng = d.ext * ((fh - fl) as u32);
  }
  return renormalize(Decoder { ...d, val: d.val - s, rng: rng });
}

// Decodes a bit that is true with the probability 1 / 2 to the power `logp`.
pub function (own d: Decoder) bit_logp(logp: i32): (Decoder, boolean) {
  let s: u32 = d.rng >> logp as u32;
  if (d.val < s) {
    return (renormalize(Decoder { ...d, rng: s }), true);
  }
  return (renormalize(Decoder { ...d, val: d.val - s, rng: d.rng - s }), false);
}

// Decodes a symbol of an inverse cumulative table with a total of 2 to the
// power `ftb`.
pub function (own d: Decoder) icdf(table: [u8], ftb: i32): (Decoder, i32) {
  let r: u32 = d.rng >> ftb as u32;
  let t: u32 = d.rng;
  let s: u32 = r * (table[0] as u32);
  let k: i32 = 0;
  while (d.val < s) {
    k = k + 1;
    t = s;
    s = r * (table[k] as u32);
  }
  return (renormalize(Decoder { ...d, val: d.val - s, rng: t - s }), k);
}

// Decodes a value less than `ft` that has an equal probability for each value.
pub function (own d: Decoder) uint(ft: u32): (Decoder, u32) {
  let top: u32 = ft - 1u32;
  let ftb: i32 = ilog(top);
  if (ftb <= UINT_BITS) {
    let (d1, s) = d.decode(ft as i32);
    return (d1.update(s, s + 1, ft as i32), s as u32);
  }
  let low: i32 = ftb - UINT_BITS;
  let f: i32 = (top >> low as u32) as i32 + 1;
  let (d2, s) = d.decode(f);
  let (d3, rest) = d2.update(s, s + 1, f).raw(low);
  let t: u32 = s as u32 << low as u32 | rest;
  if (t <= top) {
    return (d3, t);
  }
  return (Decoder { ...d3, error: true }, top);
}

// Reads `n` raw bits from the end of the packet.
pub function (own d: Decoder) raw(n: i32): (Decoder, u32) {
  let window: u32 = d.end_window;
  let available: i32 = d.nend_bits;
  let end_offs: i32 = d.end_offs;
  if (available < n) {
    while (available <= WINDOW_SIZE - 8) {
      let byte: u32 = 0u32;
      if (end_offs < d.storage) {
        end_offs = end_offs + 1;
        byte = d.buf[d.storage - end_offs] as u32;
      }
      window = window | byte << available as u32;
      available = available + 8;
    }
  }
  let v: u32 = window & (1u32 << n as u32) - 1u32;
  return (Decoder { ...d, end_offs: end_offs, end_window: window >> n as u32, nend_bits: available - n, nbits_total: d.nbits_total + n }, v);
}
```
