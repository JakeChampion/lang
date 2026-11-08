# MP3 bit fields

This helper reads bounded fields and supplies zero-padded Huffman lookahead.
The decoder validates offsets and widths before each call. A failed field read sets `bad`.
These functions are implementation details of `std/audio/mp3`.

```fern
<<*>>=
// Bounded MP3 bit fields and zero-padded Huffman lookahead.

pub struct Bits { pos: i32, limit: i32, bad: boolean }

pub function new(limit: i32): Bits {
  return Bits { pos: 0, limit: limit, bad: false };
}

pub function peek(input: [u8], pos: i32, n: i32): u32 {
  let value = 0u32;
  let left = n;
  let at = pos;
  while (left > 0) {
    let take = 8 - (at & 7);
    if (take > left) {
      take = left;
    }
    let byte = if (at / 8 < input.len()) { input[at / 8] as i32 } else { 0 };
    let chunk = byte >> 8 - (at & 7) - take & (1 << take) - 1;
    value = value << take as u32 | chunk as u32;
    at += take;
    left -= take;
  }
  return value;
}

pub function read(input: [u8], b: Bits, n: i32): (Bits, i32) {
  if (n < 0 || n > 24 || b.pos < 0 || b.pos > b.limit || n > b.limit - b.pos) {
    return (Bits { ...b, bad: true }, 0);
  }
  let value = peek(input, b.pos, n) as i32;
  return (Bits { ...b, pos: b.pos + n }, value);
}
```
