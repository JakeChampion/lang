# Brotli compression and decompression

This module encodes and decodes Brotli data under RFC 7932.

## Model

The decoder combines a bit reader, Huffman tables, a history window, and the RFC static dictionary. Input is a borrowed byte view.

## Contracts

`max_out` bounds the output size. Truncated input, malformed streams, and output limits produce distinct errors. A successful result owns its decompressed bytes.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<Compress a stream>>

<<2 pub enum BrotliError>>

<<3 pub function (e: BrotliError) to_string(): string>>

<<4 impl error.Error for BrotliError>>

<<5 pub function (e: BrotliError) message(): string>>

<<6 struct __St>>

<<7 function __bits(buf: [u8], bit: i32, n: i32): i32>>

<<8 struct __Pc>>

<<9 function __code(lens: i32[]): __Pc>>

<<10 struct __Sl>>

<<11 function __symbol_lens(buf: [u8], bit: i32, clc: __Pc, size: i32): Result[(i32[], i32), BrotliError]>>

<<12 struct __Mh>>

<<13 function __mb_header(buf: [u8], bit: i32): Result[__Mh, BrotliError]>>

<<14 struct __Cat>>

<<15 function __block_len(buf: [u8], bit: i32, pc: __Pc): Result[(i32, i32), BrotliError]>>

<<16 struct __Mb>>

<<17 function __mb_codes(buf: [u8], bit: i32): Result[__Mb, BrotliError]>>

<<18 struct __Tab>>

<<19 function __tables(): __Tab>>

<<20 struct __Cs>>

<<21 function __commands(buf: [u8], m: __Mb, s: __St, out: u8[], end: i32): Result[__Run, BrotliError]>>

<<22 struct __Step>>

<<23 function __copy_step(buf: [u8], m: __Mb, s: __St, c: __Cs, cmd: __Cmd, dst: u8[]): Result[__Step, BrotliError]>>

<<24 struct __Lits>>

<<25 function __literals(buf: [u8], m: __Mb, t: __Tab, c: __Cs, n: i32, p1: i32, p2: i32): Result[__Lits, Brotli...>>

<<26 function __slice(w: u8[], from: i32, to: i32): u8[]>>

<<27 function __context_lut(): i32[]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module decodes Brotli data under RFC 7932.
import "std/brotli/dictionary";
import "std/brotli/encoder";
import "core/cmp";
import "std/error";
```

### `2 pub enum BrotliError`

```fern
<<2 pub enum BrotliError>>=
pub enum BrotliError { BrotliTruncated, BrotliMalformed(string), BrotliTooLarge }
```

### `3 pub function (e: BrotliError) to_string(): string`

```fern
<<3 pub function (e: BrotliError) to_string(): string>>=
pub function (e: BrotliError) to_string(): string {
  return e.message();
}
```

### `4 impl error.Error for BrotliError`

```fern
<<4 impl error.Error for BrotliError>>=
impl error.Error for BrotliError {}

impl cmp.Display for BrotliError {}
```

### `5 pub function (e: BrotliError) message(): string`

```fern
<<5 pub function (e: BrotliError) message(): string>>=
pub function (e: BrotliError) message(): string {
  match (e) {
    BrotliTruncated => {
      return "brotli: truncated";
    },
    BrotliMalformed(what) => {
      return "brotli: malformed: " + what;
    },
    BrotliTooLarge => {
      return "brotli: output past the limit";
    }
  }
  return "brotli";
}

pub function decompress(input: [u8], max_out: i32): Result[u8[], BrotliError] {
  let w: (i32, i32) = __window(input);
  if (w.0 < 0) {
    return Err(BrotliMalformed("window size"));
  }
  let s: __St = __St { bit: w.1, ring: [4, 11, 15, 16], window: (1 << w.0) - 16, t: __tables() };
  let out: u8[] = [];
  let last: boolean = false;
  while (!last) {
    let h: __Mh = __mb_header(input, s.bit)?;
    last = h.last;
    let r: __Run = __meta_block(input, h, s, out, max_out)?;
    out = r.out;
    s = r.s;
  }
  if (s.bit > input.len() * 8) {
    return Err(BrotliTruncated);
  }
  return Ok(out);
}
```

### `6 struct __St`

```fern
<<6 struct __St>>=
struct __St { bit: i32, ring: i32[], window: i32, t: __Tab }

struct __Run { out: u8[], s: __St }
```

### `7 function __bits(buf: [u8], bit: i32, n: i32): i32`

```fern
<<7 function __bits(buf: [u8], bit: i32, n: i32): i32>>=
function __bits(buf: [u8], bit: i32, n: i32): i32 {
  let v: i64 = 0;
  let first: i32 = bit >> 3;
  let i: i32 = (bit + n + 7 >> 3) - 1;
  while (i >= first) {
    let b: i64 = 0;
    if (i < buf.len()) {
      b = buf[i] as i64;
    }
    v = v << 8 | b;
    i = i - 1;
  }
  return (v >> (bit & 7) & (1 as i64 << n) - 1) as i32;
}

function __window(buf: [u8]): (i32, i32) {
  if (__bits(buf, 0, 1) == 0) {
    return (16, 1);
  }
  let n: i32 = __bits(buf, 1, 3);
  if (n != 0) {
    return (17 + n, 4);
  }
  let m: i32 = __bits(buf, 4, 3);
  if (m == 1) {
    return (0 - 1, 7);
  }
  if (m == 0) {
    return (17, 7);
  }
  return (8 + m, 7);
}

function __varlen(buf: [u8], bit: i32): (i32, i32) {
  if (__bits(buf, bit, 1) == 0) {
    return (0, bit + 1);
  }
  let n: i32 = __bits(buf, bit + 1, 3);
  if (n == 0) {
    return (1, bit + 4);
  }
  return (__bits(buf, bit + 4, n) + (1 << n), bit + 4 + n);
}
```

### `8 struct __Pc`

```fern
<<8 struct __Pc>>=
struct __Pc { count: i32[], sym: i32[], single: i32 }
```

### `9 function __code(lens: i32[]): __Pc`

```fern
<<9 function __code(lens: i32[]): __Pc>>=
function __code(lens: i32[]): __Pc {
  let count: i32[] = __zeros(16);
  let used: i32 = 0;
  let only: i32 = 0;
  let s: i32 = 0;
  while (s < lens.len()) {
    if (lens[s] > 0) {
      count = count.with(lens[s], count[lens[s]] + 1);
      used = used + 1;
      only = s;
    }
    s = s + 1;
  }
  if (used == 1) {
    return __Pc { count: count, sym: [only], single: only };
  }
  let sym: i32[] = [];
  let len: i32 = 1;
  while (len <= 15) {
    s = 0;
    while (s < lens.len()) {
      if (lens[s] == len) {
        sym = sym.append(s);
      }
      s = s + 1;
    }
    len = len + 1;
  }
  return __Pc { count: count, sym: sym, single: 0 - 1 };
}

function __decode(buf: [u8], bit: i32, pc: __Pc): (i32, i32) {
  if (pc.single >= 0) {
    return (pc.single, bit);
  }
  let code: i32 = 0;
  let first: i32 = 0;
  let index: i32 = 0;
  let len: i32 = 1;
  while (len <= 15) {
    code = code | __bits(buf, bit, 1);
    bit = bit + 1;
    let count: i32 = pc.count[len];
    if (code - first < count) {
      return (pc.sym[index + code - first], bit);
    }
    index = index + count;
    first = first + count << 1;
    code = code << 1;
    len = len + 1;
  }
  return (0 - 1, bit);
}

function __symbol(buf: [u8], bit: i32, pc: __Pc): Result[(i32, i32), BrotliError] {
  let r: (i32, i32) = __decode(buf, bit, pc);
  if (r.0 < 0) {
    return Err(BrotliMalformed("prefix code"));
  }
  return Ok(r);
}

function __read_code(buf: [u8], bit: i32, size: i32): Result[(__Pc, i32), BrotliError] {
  let hskip: i32 = __bits(buf, bit, 2);
  if (hskip == 1) {
    return __simple_code(buf, bit + 2, size);
  }
  return __complex_code(buf, bit + 2, hskip, size);
}

function __simple_code(buf: [u8], bit: i32, size: i32): Result[(__Pc, i32), BrotliError] {
  let n: i32 = __bits(buf, bit, 2) + 1;
  let width: i32 = __highbit(size - 1) + 1;
  bit = bit + 2;
  let syms: i32[] = [];
  while (syms.len() < n) {
    let s: i32 = __bits(buf, bit, width);
    if (s >= size || __has(syms, s)) {
      return Err(BrotliMalformed("simple prefix code"));
    }
    syms = syms.append(s);
    bit = bit + width;
  }
  let shape: i32[] = [[0], [1, 1], [1, 2, 2], [2, 2, 2, 2]][n - 1];
  if (n == 4) {
    if (__bits(buf, bit, 1) == 1) {
      shape = [1, 2, 3, 3];
    }
    bit = bit + 1;
  }
  let lens: i32[] = __zeros(size);
  let k: i32 = 0;
  while (k < n) {
    lens = lens.with(syms[k], shape[k]);
    k = k + 1;
  }
  if (n == 1) {
    return Ok((__Pc { count: __zeros(16), sym: syms, single: syms[0] }, bit));
  }
  return Ok((__code(lens), bit));
}

function __has(xs: i32[], x: i32): boolean {
  for y in xs {
    if (y == x) {
      return true;
    }
  }
  return false;
}

function __complex_code(buf: [u8], bit: i32, hskip: i32, size: i32): Result[(__Pc, i32), BrotliError] {
  let order: i32[] = [1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15];
  let fixed: i32[] = [0x020000, 0x020004, 0x020003, 0x030002, 0x020000, 0x020004, 0x020003, 0x040001, 0x020000, 0x020004, 0x020003, 0x030002, 0x020000, 0x020004, 0x020003, 0x040005];
  let lens: i32[] = __zeros(18);
  let space: i32 = 32;
  let used: i32 = 0;
  let i: i32 = hskip;
  while (i < 18 && space > 0) {
    let v: i32 = fixed[__bits(buf, bit, 4)];
    bit = bit + (v >> 16);
    let len: i32 = v & 0xFFFF;
    lens = lens.with(order[i], len);
    if (len != 0) {
      space = space - (32 >> len);
      used = used + 1;
    }
    i = i + 1;
  }
  if (space != 0 && used != 1) {
    return Err(BrotliMalformed("code length code"));
  }
  let read: (i32[], i32) = __symbol_lens(buf, bit, __code(lens), size)?;
  return Ok((__code(read.0), read.1));
}
```

### `10 struct __Sl`

```fern
<<10 struct __Sl>>=
struct __Sl { lens: i32[], prev: i32, repeat: i32, rlen: i32, space: i32, bit: i32 }
```

### `11 function __symbol_lens(buf: [u8], bit: i32, clc: __Pc, size: i32): Result[(i32[], i32), BrotliError]`

```fern
<<11 function __symbol_lens(buf: [u8], bit: i32, clc: __Pc, size: i32): Result[(i32[], i32), BrotliError]>>=
function __symbol_lens(buf: [u8], bit: i32, clc: __Pc, size: i32): Result[(i32[], i32), BrotliError] {
  let none: i32[] = [];
  let st: __Sl = __Sl { lens: none, prev: 8, repeat: 0, rlen: 0, space: 32768, bit: bit };
  while (st.lens.len() < size && st.space > 0) {
    let c: (i32, i32) = __symbol(buf, st.bit, clc)?;
    st = __Sl { ...st, bit: c.1 };
    if (c.0 < 16) {
      st = __literal_len(st, c.0);
    } else {
      st = __repeat_len(buf, st, c.0);
    }
    if (st.lens.len() > size) {
      return Err(BrotliMalformed("code lengths"));
    }
  }
  if (st.space != 0) {
    return Err(BrotliMalformed("incomplete prefix code"));
  }
  let lens: i32[] = st.lens;
  while (lens.len() < size) {
    lens = lens.append(0);
  }
  return Ok((lens, st.bit));
}

function __literal_len(st: __Sl, c: i32): __Sl {
  if (c == 0) {
    return __Sl { ...st, lens: st.lens.append(0), repeat: 0 };
  }
  return __Sl { ...st, lens: st.lens.append(c), repeat: 0, prev: c, space: st.space - (32768 >> c) };
}

function __repeat_len(buf: [u8], st: __Sl, c: i32): __Sl {
  let extra: i32 = c - 14;
  let len: i32 = 0;
  if (c == 16) {
    len = st.prev;
  }
  let repeat: i32 = st.repeat;
  if (st.rlen != len) {
    repeat = 0;
  }
  let old: i32 = repeat;
  if (repeat > 0) {
    repeat = repeat - 2 << extra;
  }
  repeat = repeat + __bits(buf, st.bit, extra) + 3;
  let lens: i32[] = st.lens;
  let k: i32 = old;
  while (k < repeat) {
    lens = lens.append(len);
    k = k + 1;
  }
  let space: i32 = st.space;
  if (len != 0) {
    space = space - (repeat - old << 15 - len);
  }
  return __Sl { lens: lens, prev: st.prev, repeat: repeat, rlen: len, space: space, bit: st.bit + extra };
}

function __zeros(n: i32): i32[] {
  let out: i32[] = [];
  while (out.len() < n) {
    out = out.append(0);
  }
  return out;
}

function __highbit(x: i32): i32 {
  let k: i32 = 0 - 1;
  while (x > 0) {
    x = x >> 1;
    k = k + 1;
  }
  return k;
}
```

### `12 struct __Mh`

```fern
<<12 struct __Mh>>=
struct __Mh { last: boolean, len: i32, raw: boolean, skip: i32, bit: i32 }
```

### `13 function __mb_header(buf: [u8], bit: i32): Result[__Mh, BrotliError]`

```fern
<<13 function __mb_header(buf: [u8], bit: i32): Result[__Mh, BrotliError]>>=
function __mb_header(buf: [u8], bit: i32): Result[__Mh, BrotliError] {
  let last: boolean = __bits(buf, bit, 1) == 1;
  bit = bit + 1;
  if (last) {
    bit = bit + 1;
    if (__bits(buf, bit - 1, 1) == 1) {
      return Ok(__Mh { last: true, len: 0, raw: false, skip: 0, bit: bit });
    }
  }
  let nibbles: i32 = [4, 5, 6, 0][__bits(buf, bit, 2)];
  bit = bit + 2;
  if (nibbles == 0) {
    return __metadata(buf, bit, last);
  }
  let len: i32 = __bits(buf, bit, nibbles * 4) + 1;
  if (nibbles > 4 && len - 1 >> (nibbles - 1) * 4 == 0) {
    return Err(BrotliMalformed("meta-block length"));
  }
  bit = bit + nibbles * 4;
  let raw: boolean = false;
  if (!last) {
    raw = __bits(buf, bit, 1) == 1;
    bit = bit + 1;
  }
  return Ok(__Mh { last: last, len: len, raw: raw, skip: 0, bit: bit });
}

function __metadata(buf: [u8], bit: i32, last: boolean): Result[__Mh, BrotliError] {
  if (last || __bits(buf, bit, 1) != 0) {
    return Err(BrotliMalformed("metadata block"));
  }
  let bytes: i32 = __bits(buf, bit + 1, 2);
  bit = bit + 3;
  let skip: i32 = 0;
  if (bytes > 0) {
    skip = __bits(buf, bit, bytes * 8) + 1;
    if (bytes > 1 && skip - 1 >> (bytes - 1) * 8 == 0) {
      return Err(BrotliMalformed("metadata length"));
    }
  }
  return Ok(__Mh { last: false, len: 0, raw: false, skip: skip, bit: bit + bytes * 8 });
}

function __meta_block(buf: [u8], h: __Mh, s: __St, out: u8[], max_out: i32): Result[__Run, BrotliError] {
  if (out.len() + h.len > max_out) {
    return Err(BrotliTooLarge);
  }
  if (h.len == 0) {
    let at: i32 = __aligned(buf, h.bit)?;
    return Ok(__Run { out: out, s: __St { ...s, bit: (at + h.skip) * 8 } });
  }
  if (h.raw) {
    let at: i32 = __aligned(buf, h.bit)?;
    if (at + h.len > buf.len()) {
      return Err(BrotliTruncated);
    }
    let copied: u8[] = out;
    let i: i32 = at;
    while (i < at + h.len) {
      copied = copied.append(buf[i]);
      i = i + 1;
    }
    return Ok(__Run { out: copied, s: __St { ...s, bit: (at + h.len) * 8 } });
  }
  let m: __Mb = __mb_codes(buf, h.bit)?;
  return __commands(buf, m, s, out, out.len() + h.len);
}

function __aligned(buf: [u8], bit: i32): Result[i32, BrotliError] {
  if (__bits(buf, bit, 8 - (bit & 7) & 7) != 0) {
    return Err(BrotliMalformed("padding"));
  }
  return Ok(bit + 7 >> 3);
}
```

### `14 struct __Cat`

```fern
<<14 struct __Cat>>=
struct __Cat { n: i32, types: __Pc, lens: __Pc, cur: i32, prev: i32, left: i32 }
```

### `15 function __block_len(buf: [u8], bit: i32, pc: __Pc): Result[(i32, i32), BrotliError]`

```fern
<<15 function __block_len(buf: [u8], bit: i32, pc: __Pc): Result[(i32, i32), BrotliError]>>=
function __block_len(buf: [u8], bit: i32, pc: __Pc): Result[(i32, i32), BrotliError] {
  let offset: i32[] = [1, 5, 9, 13, 17, 25, 33, 41, 49, 65, 81, 97, 113, 145, 177, 209, 241, 305, 369, 497, 753, 1265, 2289, 4337, 8433, 16625];
  let extra: i32[] = [2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 7, 8, 9, 10, 11, 12, 13, 24];
  let s: (i32, i32) = __symbol(buf, bit, pc)?;
  return Ok((offset[s.0] + __bits(buf, s.1, extra[s.0]), s.1 + extra[s.0]));
}

function __read_cat(buf: [u8], bit: i32): Result[(__Cat, i32), BrotliError] {
  let n: (i32, i32) = __varlen(buf, bit);
  let none: __Pc = __Pc { count: [], sym: [], single: 0 };
  if (n.0 == 0) {
    return Ok((__Cat { n: 1, types: none, lens: none, cur: 0, prev: 1, left: 1 << 30 }, n.1));
  }
  let types: (__Pc, i32) = __read_code(buf, n.1, n.0 + 3)?;
  let lens: (__Pc, i32) = __read_code(buf, types.1, 26)?;
  let first: (i32, i32) = __block_len(buf, lens.1, lens.0)?;
  return Ok((__Cat { n: n.0 + 1, types: types.0, lens: lens.0, cur: 0, prev: 1, left: first.0 }, first.1));
}

function __switched(buf: [u8], bit: i32, c: __Cat): Result[(__Cat, i32), BrotliError] {
  let s: (i32, i32) = __symbol(buf, bit, c.types)?;
  let t: i32 = s.0 - 2;
  if (s.0 == 0) {
    t = c.prev;
  } else if (s.0 == 1) {
    t = c.cur + 1;
  }
  if (t >= c.n) {
    t = t - c.n;
  }
  let len: (i32, i32) = __block_len(buf, s.1, c.lens)?;
  return Ok((__Cat { ...c, prev: c.cur, cur: t, left: len.0 }, len.1));
}
```

### `16 struct __Mb`

```fern
<<16 struct __Mb>>=
struct __Mb { lit: __Cat, cmd: __Cat, dist: __Cat, postfix: i32, direct: i32, modes: i32[], lmap: i32[], dmap: i32[], lcodes: __Pc[], ccodes: __Pc[], dcodes: __Pc[], bit: i32 }
```

### `17 function __mb_codes(buf: [u8], bit: i32): Result[__Mb, BrotliError]`

```fern
<<17 function __mb_codes(buf: [u8], bit: i32): Result[__Mb, BrotliError]>>=
function __mb_codes(buf: [u8], bit: i32): Result[__Mb, BrotliError] {
  let lit: (__Cat, i32) = __read_cat(buf, bit)?;
  let cmd: (__Cat, i32) = __read_cat(buf, lit.1)?;
  let dist: (__Cat, i32) = __read_cat(buf, cmd.1)?;
  bit = dist.1;
  let postfix: i32 = __bits(buf, bit, 2);
  let direct: i32 = __bits(buf, bit + 2, 4) << postfix;
  bit = bit + 6;
  let modes: i32[] = [];
  while (modes.len() < lit.0.n) {
    modes = modes.append(__bits(buf, bit, 2));
    bit = bit + 2;
  }
  let lmap: (i32[], i32, i32) = __context_map(buf, bit, 64 * lit.0.n)?;
  let dmap: (i32[], i32, i32) = __context_map(buf, lmap.2, 4 * dist.0.n)?;
  let lcodes: (__Pc[], i32) = __codes(buf, dmap.2, lmap.1, 256)?;
  let ccodes: (__Pc[], i32) = __codes(buf, lcodes.1, cmd.0.n, 704)?;
  let dcodes: (__Pc[], i32) = __codes(buf, ccodes.1, dmap.1, 16 + direct + (48 << postfix))?;
  return Ok(__Mb { lit: lit.0, cmd: cmd.0, dist: dist.0, postfix: postfix, direct: direct, modes: modes, lmap: lmap.0, dmap: dmap.0, lcodes: lcodes.0, ccodes: ccodes.0, dcodes: dcodes.0, bit: dcodes.1 });
}

function __codes(buf: [u8], bit: i32, n: i32, size: i32): Result[(__Pc[], i32), BrotliError] {
  let out: __Pc[] = [];
  while (out.len() < n) {
    let c: (__Pc, i32) = __read_code(buf, bit, size)?;
    out = out.append(c.0);
    bit = c.1;
  }
  return Ok((out, bit));
}

function __context_map(buf: [u8], bit: i32, size: i32): Result[(i32[], i32, i32), BrotliError] {
  let trees: (i32, i32) = __varlen(buf, bit);
  let n: i32 = trees.0 + 1;
  bit = trees.1;
  if (n == 1) {
    return Ok((__zeros(size), 1, bit));
  }
  let rle: (i32, i32) = __rle_prefix(buf, bit);
  let code: (__Pc, i32) = __read_code(buf, rle.1, n + rle.0)?;
  bit = code.1;
  let map: i32[] = [];
  while (map.len() < size) {
    let s: (i32, i32) = __symbol(buf, bit, code.0)?;
    bit = s.1;
    if (s.0 == 0 || s.0 > rle.0) {
      map = map.append(cmp.max(s.0 - rle.0, 0));
    } else {
      let run: i32 = (1 << s.0) + __bits(buf, bit, s.0);
      bit = bit + s.0;
      if (map.len() + run > size) {
        return Err(BrotliMalformed("context map"));
      }
      let k: i32 = 0;
      while (k < run) {
        map = map.append(0);
        k = k + 1;
      }
    }
  }
  if (__bits(buf, bit, 1) == 1) {
    map = __inverse_mtf(map);
  }
  return Ok((map, n, bit + 1));
}

function __rle_prefix(buf: [u8], bit: i32): (i32, i32) {
  if (__bits(buf, bit, 1) == 0) {
    return (0, bit + 1);
  }
  return (__bits(buf, bit + 1, 4) + 1, bit + 5);
}

function __inverse_mtf(v: i32[]): i32[] {
  let mtf: i32[] = [];
  while (mtf.len() < 256) {
    mtf = mtf.append(mtf.len());
  }
  let out: i32[] = [];
  for idx in v {
    let value: i32 = mtf[idx];
    out = out.append(value);
    let k: i32 = idx;
    while (k > 0) {
      mtf = mtf.with(k, mtf[k - 1]);
      k = k - 1;
    }
    mtf = mtf.with(0, value);
  }
  return out;
}
```

### `18 struct __Tab`

```fern
<<18 struct __Tab>>=
struct __Tab { pre: string[], kinds: i32[], suf: string[], lut: i32[], doff: i32[], dbits: i32[] }
```

### `19 function __tables(): __Tab`

```fern
<<19 function __tables(): __Tab>>=
function __tables(): __Tab {
  let dbits: i32[] = [0, 0, 0, 0, 10, 10, 11, 11, 10, 10, 10, 10, 10, 9, 9, 8, 7, 7, 8, 7, 7, 6, 6, 5, 5];
  let doff: i32[] = [0, 0, 0, 0, 0];
  let len: i32 = 4;
  while (len < 24) {
    doff = doff.append(doff[len] + len * (1 << dbits[len]));
    len = len + 1;
  }
  return __Tab { pre: __prefixes(), kinds: __kinds(), suf: __suffixes(), lut: __context_lut(), doff: doff, dbits: dbits };
}
```

### `20 struct __Cs`

```fern
<<20 struct __Cs>>=
struct __Cs { lit: __Cat, cmd: __Cat, dist: __Cat, bit: i32, ring: i32[] }

struct __Cmd { insert: i32, copy: i32, implicit: boolean, c: __Cs }
```

### `21 function __commands(buf: [u8], m: __Mb, s: __St, out: u8[], end: i32): Result[__Run, BrotliError]`

```fern
<<21 function __commands(buf: [u8], m: __Mb, s: __St, out: u8[], end: i32): Result[__Run, BrotliError]>>=
function __commands(buf: [u8], m: __Mb, s: __St, out: u8[], end: i32): Result[__Run, BrotliError] {
  let c: __Cs = __Cs { lit: m.lit, cmd: m.cmd, dist: m.dist, bit: m.bit, ring: s.ring };
  let dst: u8[] = out;
  while (dst.len() < end) {
    let cmd: __Cmd = __command(buf, m, c)?;
    let lits: __Lits = __literals(buf, m, s.t, cmd.c, cmd.insert, __back_byte(dst, 1), __back_byte(dst, 2))?;
    c = lits.c;
    dst = __joined(dst, lits.bytes);
    if (dst.len() < end) {
      let step: __Step = __copy_step(buf, m, s, c, cmd, dst)?;
      c = step.c;
      dst = __joined(dst, step.bytes);
    }
    if (dst.len() > end || c.bit > buf.len() * 8) {
      return Err(__overrun(dst.len() > end));
    }
  }
  return Ok(__Run { out: dst, s: __St { ...s, bit: c.bit, ring: c.ring } });
}

function __overrun(past_end: boolean): BrotliError {
  if (past_end) {
    return BrotliMalformed("command past the meta-block");
  }
  return BrotliTruncated;
}
```

### `22 struct __Step`

```fern
<<22 struct __Step>>=
struct __Step { bytes: u8[], c: __Cs }
```

### `23 function __copy_step(buf: [u8], m: __Mb, s: __St, c: __Cs, cmd: __Cmd, dst: u8[]): Result[__Step, BrotliError]`

```fern
<<23 function __copy_step(buf: [u8], m: __Mb, s: __St, c: __Cs, cmd: __Cmd, dst: u8[]): Result[__Step, BrotliError]>>=
function __copy_step(buf: [u8], m: __Mb, s: __St, c: __Cs, cmd: __Cmd, dst: u8[]): Result[__Step, BrotliError] {
  let d: (i32, i32, __Cs) = __distance(buf, m, c, cmd)?;
  let max: i32 = cmp.min(s.window, dst.len());
  if (d.0 > max) {
    return Ok(__Step { bytes: __dict_word(s.t, d.0 - max - 1, cmd.copy)?, c: d.2 });
  }
  let bytes: u8[] = [];
  let from: i32 = dst.len() - d.0;
  while (bytes.len() < cmd.copy) {
    let at: i32 = from + bytes.len();
    if (at < dst.len()) {
      bytes = bytes.append(dst[at]);
    } else {
      bytes = bytes.append(bytes[at - dst.len()]);
    }
  }
  let next: __Cs = d.2;
  if (d.1 != 0) {
    next = __Cs { ...next, ring: [d.0, next.ring[0], next.ring[1], next.ring[2]] };
  }
  return Ok(__Step { bytes: bytes, c: next });
}

function __back_byte(out: u8[], k: i32): i32 {
  if (out.len() < k) {
    return 0;
  }
  return out[out.len() - k] as i32;
}

function __joined(own out: u8[], more: u8[]): u8[] {
  for b in more {
    out = out.append(b);
  }
  return out;
}

function __command(buf: [u8], m: __Mb, c: __Cs): Result[__Cmd, BrotliError] {
  let cat: __Cat = c.cmd;
  let bit: i32 = c.bit;
  if (cat.left == 0) {
    let sw: (__Cat, i32) = __switched(buf, bit, cat)?;
    cat = sw.0;
    bit = sw.1;
  }
  let s: (i32, i32) = __symbol(buf, bit, m.ccodes[cat.cur])?;
  let cell: i32 = s.0 >> 6;
  let ic: i32 = [0, 0, 0, 0, 8, 8, 0, 16, 8, 16, 16][cell] + (s.0 >> 3 & 7);
  let cc: i32 = [0, 8, 0, 8, 0, 8, 16, 0, 16, 8, 16][cell] + (s.0 & 7);
  let ib: i32[] = [0, 1, 2, 3, 4, 5, 6, 8, 10, 14, 18, 26, 34, 50, 66, 98, 130, 194, 322, 578, 1090, 2114, 6210, 22594];
  let ix: i32[] = [0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 12, 14, 24];
  let cb: i32[] = [2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 14, 18, 22, 30, 38, 54, 70, 102, 134, 198, 326, 582, 1094, 2118];
  let cx: i32[] = [0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 24];
  let insert: i32 = ib[ic] + __bits(buf, s.1, ix[ic]);
  let copy: i32 = cb[cc] + __bits(buf, s.1 + ix[ic], cx[cc]);
  let next: __Cs = __Cs { ...c, cmd: __Cat { ...cat, left: cat.left - 1 }, bit: s.1 + ix[ic] + cx[cc] };
  return Ok(__Cmd { insert: insert, copy: copy, implicit: s.0 < 128, c: next });
}
```

### `24 struct __Lits`

```fern
<<24 struct __Lits>>=
struct __Lits { bytes: u8[], c: __Cs }
```

### `25 function __literals(buf: [u8], m: __Mb, t: __Tab, c: __Cs, n: i32, p1: i32, p2: i32): Result[__Lits, Brotli...`

```fern
<<25 function __literals(buf: [u8], m: __Mb, t: __Tab, c: __Cs, n: i32, p1: i32, p2: i32): Result[__Lits, Brotli...>>=
function __literals(buf: [u8], m: __Mb, t: __Tab, c: __Cs, n: i32, p1: i32, p2: i32): Result[__Lits, BrotliError] {
  let out: u8[] = [];
  let cat: __Cat = c.lit;
  let bit: i32 = c.bit;
  while (out.len() < n) {
    if (cat.left == 0) {
      let sw: (__Cat, i32) = __switched(buf, bit, cat)?;
      cat = sw.0;
      bit = sw.1;
    }
    cat = __Cat { ...cat, left: cat.left - 1 };
    let base: i32 = m.modes[cat.cur] << 9;
    let ctx: i32 = t.lut[base + p1] | t.lut[base + 256 + p2];
    let s: (i32, i32) = __symbol(buf, bit, m.lcodes[m.lmap[cat.cur * 64 + ctx]])?;
    bit = s.1;
    out = out.append(s.0 as u8);
    p2 = p1;
    p1 = s.0;
    if (bit > buf.len() * 8) {
      return Err(BrotliTruncated);
    }
  }
  return Ok(__Lits { bytes: out, c: __Cs { ...c, lit: cat, bit: bit } });
}

function __distance(buf: [u8], m: __Mb, c: __Cs, cmd: __Cmd): Result[(i32, i32, __Cs), BrotliError] {
  if (cmd.implicit) {
    return Ok((c.ring[0], 0, c));
  }
  let cat: __Cat = c.dist;
  let bit: i32 = c.bit;
  if (cat.left == 0) {
    let sw: (__Cat, i32) = __switched(buf, bit, cat)?;
    cat = sw.0;
    bit = sw.1;
  }
  let ctx: i32 = cmp.min(cmd.copy - 2, 3);
  let s: (i32, i32) = __symbol(buf, bit, m.dcodes[m.dmap[cat.cur * 4 + ctx]])?;
  let d: (i32, i32) = __distance_of(buf, s.1, s.0, m, c.ring);
  if (d.0 <= 0) {
    return Err(BrotliMalformed("distance"));
  }
  return Ok((d.0, s.0, __Cs { ...c, dist: __Cat { ...cat, left: cat.left - 1 }, bit: d.1 }));
}

function __distance_of(buf: [u8], bit: i32, code: i32, m: __Mb, ring: i32[]): (i32, i32) {
  if (code < 16) {
    let idx: i32 = [0, 1, 2, 3, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1][code];
    let delta: i32 = [0, 0, 0, 0, 0 - 1, 1, 0 - 2, 2, 0 - 3, 3, 0 - 1, 1, 0 - 2, 2, 0 - 3, 3][code];
    return (ring[idx] + delta, bit);
  }
  if (code < 16 + m.direct) {
    return (code - 15, bit);
  }
  let x: i32 = code - m.direct - 16;
  let h: i32 = x >> m.postfix;
  let nb: i32 = 1 + (h >> 1);
  let offset: i32 = (2 + (h & 1) << nb) - 4;
  let low: i32 = x & (1 << m.postfix) - 1;
  return ((offset + __bits(buf, bit, nb) << m.postfix) + low + m.direct + 1, bit + nb);
}

function __dict_word(t: __Tab, id: i32, len: i32): Result[u8[], BrotliError] {
  if (len < 4 || len > 24) {
    return Err(BrotliMalformed("dictionary word length"));
  }
  let bits: i32 = t.dbits[len];
  let tr: i32 = id >> bits;
  if (tr >= 121) {
    return Err(BrotliMalformed("dictionary transform"));
  }
  let at: i32 = t.doff[len] + (id & (1 << bits) - 1) * len;
  let word: u8[] = [];
  let k: i32 = 0;
  while (k < len) {
    word = word.append(dictionary.WORDS[at + k]);
    k = k + 1;
  }
  let out: u8[] = t.pre[tr].bytes();
  out = __joined(out, __transformed(word, t.kinds[tr]));
  return Ok(__joined(out, t.suf[tr].bytes()));
}

function __transformed(word: u8[], kind: i32): u8[] {
  let n: i32 = word.len();
  if (kind >= 1 && kind <= 9) {
    return __slice(word, 0, cmp.max(n - kind, 0));
  }
  if (kind >= 12) {
    return __slice(word, cmp.min(kind - 11, n), n);
  }
  if (kind < 10) {
    return word;
  }
  let w: u8[] = word;
  let i: i32 = 0;
  while (i < n) {
    let up: (u8[], i32) = __upper_at(w, i);
    w = up.0;
    i = i + up.1;
    if (kind == 10) {
      i = n;
    }
  }
  return w;
}

function __upper_at(w: u8[], i: i32): (u8[], i32) {
  let b: i32 = w[i] as i32;
  if (b < 0xC0) {
    if (b >= 97 && b <= 122) {
      return (w.with(i, (b ^ 32) as u8), 1);
    }
    return (w, 1);
  }
  if (b < 0xE0) {
    if (i + 1 < w.len()) {
      return (w.with(i + 1, w[i + 1] ^ 32 as u8), 2);
    }
    return (w, 2);
  }
  if (i + 2 < w.len()) {
    return (w.with(i + 2, w[i + 2] ^ 5 as u8), 3);
  }
  return (w, 3);
}
```

### `26 function __slice(w: u8[], from: i32, to: i32): u8[]`

```fern
<<26 function __slice(w: u8[], from: i32, to: i32): u8[]>>=
function __slice(w: u8[], from: i32, to: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = from;
  while (i < to) {
    out = out.append(w[i]);
    i = i + 1;
  }
  return out;
}

function __prefixes(): string[] {
  return [
    "", "", " ", "", "", "", " ", "s ", "", "", "", "", "", ", ", "", " ", "",
    "", "e ", "", "", "", "", "", "", "", "", "", "", "", " ", "", ".", " ",
    "", "", "", "", "", "", "", " the ", "", "", "", "", "", "", "", "", "",
    "", " ", "", "", "", "", "", "", "", "", "", " the ", "", "", " ", "", ".",
    "", "", "", " ", ".com/", " the ", "", "", "", ".", "", "", "", " ", "",
    " ", "", " ", "", "", "", " ", "", " ", "", "", "", "", " ", "", " ", "",
    "", "", " ", " ", "", "", "", "", "", " ", " ", " ", "", "", "", " ", "",
    " ", " ", " ", " ",
  ];
}

function __suffixes(): string[] {
  return [
    "", " ", " ", "", " ", " the ", "", " ", " of ", "", " and ", "", "", " ",
    ", ", " ", " in ", " to ", " ", "\"", ".", "\">", "\n", "", "]", " for ",
    "", "", " a ", " that ", "", ". ", "", ", ", "", " with ", "'", " from ",
    " by ", "", "", "", "", ". The ", "", " on ", " as ", " is ", "", "ing ",
    "\n\t", ":", ". ", "ed ", "", "", "", "(", ", ", "", " at ", "ly ", " of ",
    "", "", ", ", "\"", "(", " ", "\">", "=\"", ".", "", " of the ", "'",
    ". This ", ",", " ", "(", ".", " not ", "=\"", "er ", " ", "al ", "", "='",
    "\"", ". ", "(", "ful ", ". ", "ive ", "less ", "'", "est ", ".", "\">",
    "='", ",", "ize ", ".", "", ",", "=\"", "=\"", "ous ", ", ", "='", ",",
    "=\"", ", ", ",", "(", ". ", ".", "='", ". ", "=\"", "='", "='",
  ];
}

function __kinds(): i32[] {
  return [
    0, 0, 0, 12, 10, 0, 0, 0, 0, 10, 0, 13, 1, 0, 0, 10, 0, 0, 0, 0, 0, 0, 0,
    3, 0, 0, 14, 2, 0, 0, 10, 0, 0, 0, 15, 0, 0, 0, 0, 16, 17, 0, 4, 0, 11, 0,
    0, 0, 7, 1, 0, 0, 0, 0, 20, 18, 6, 0, 10, 8, 0, 0, 0, 5, 9, 10, 10, 0, 11,
    10, 0, 0, 0, 0, 10, 0, 0, 0, 10, 10, 0, 0, 0, 11, 0, 11, 0, 11, 10, 0, 0,
    10, 0, 0, 11, 0, 10, 11, 0, 10, 0, 11, 0, 0, 10, 11, 0, 11, 10, 10, 11, 11,
    11, 11, 11, 11, 11, 11, 10, 11, 10,
  ];
}
```

### `27 function __context_lut(): i32[]`

```fern
<<27 function __context_lut(): i32[]>>=
function __context_lut(): i32[] {
  return [
    0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
    21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
    40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58,
    59, 60, 61, 62, 63, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
    16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34,
    35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53,
    54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
    11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
    30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48,
    49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 0, 1, 2, 3, 4,
    5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24,
    25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43,
    44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62,
    63, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4,
    4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7, 8, 8, 8, 8, 9, 9, 9, 9, 10, 10,
    10, 10, 11, 11, 11, 11, 12, 12, 12, 12, 13, 13, 13, 13, 14, 14, 14, 14, 15,
    15, 15, 15, 16, 16, 16, 16, 17, 17, 17, 17, 18, 18, 18, 18, 19, 19, 19, 19,
    20, 20, 20, 20, 21, 21, 21, 21, 22, 22, 22, 22, 23, 23, 23, 23, 24, 24, 24,
    24, 25, 25, 25, 25, 26, 26, 26, 26, 27, 27, 27, 27, 28, 28, 28, 28, 29, 29,
    29, 29, 30, 30, 30, 30, 31, 31, 31, 31, 32, 32, 32, 32, 33, 33, 33, 33, 34,
    34, 34, 34, 35, 35, 35, 35, 36, 36, 36, 36, 37, 37, 37, 37, 38, 38, 38, 38,
    39, 39, 39, 39, 40, 40, 40, 40, 41, 41, 41, 41, 42, 42, 42, 42, 43, 43, 43,
    43, 44, 44, 44, 44, 45, 45, 45, 45, 46, 46, 46, 46, 47, 47, 47, 47, 48, 48,
    48, 48, 49, 49, 49, 49, 50, 50, 50, 50, 51, 51, 51, 51, 52, 52, 52, 52, 53,
    53, 53, 53, 54, 54, 54, 54, 55, 55, 55, 55, 56, 56, 56, 56, 57, 57, 57, 57,
    58, 58, 58, 58, 59, 59, 59, 59, 60, 60, 60, 60, 61, 61, 61, 61, 62, 62, 62,
    62, 63, 63, 63, 63, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 0,
    0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8, 12, 16, 12,
    12, 20, 12, 16, 24, 28, 12, 12, 32, 12, 36, 12, 44, 44, 44, 44, 44, 44, 44,
    44, 44, 44, 32, 32, 24, 40, 28, 12, 12, 48, 52, 52, 52, 48, 52, 52, 52, 48,
    52, 52, 52, 52, 52, 48, 52, 52, 52, 52, 52, 48, 52, 52, 52, 52, 52, 24, 12,
    28, 12, 12, 12, 56, 60, 60, 60, 56, 60, 60, 60, 56, 60, 60, 60, 60, 60, 56,
    60, 60, 60, 60, 60, 56, 60, 60, 60, 60, 60, 24, 12, 28, 12, 0, 0, 1, 0, 1,
    0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0,
    1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1,
    0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2,
    3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3,
    2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 2, 3, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2,
    2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
    2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 3, 3, 3, 3,
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 1, 1, 1,
    1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
    0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 2,
    2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
    2, 2, 2, 2, 2, 0, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 16, 16, 16,
    16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16,
    16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16,
    16, 16, 16, 16, 16, 16, 16, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
    24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
    24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
    24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 24, 32, 32, 32, 32, 32,
    32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32,
    32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32,
    32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32,
    32, 32, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40,
    40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40,
    40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 40, 48, 48, 48, 48, 48, 48, 48,
    48, 48, 48, 48, 48, 48, 48, 48, 56, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
    1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
    2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
    2, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
    4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
    4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
    4, 4, 4, 4, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
    5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
    5, 5, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 7,
  ];
}
```

## Compress a stream

The fast encoder uses LZ77 matches and fixed Huffman trees. It has bounded match history.

```fern
<<Compress a stream>>=
pub function compress(input: u8[]): u8[] {
  return encoder.compress(input);
}
```
