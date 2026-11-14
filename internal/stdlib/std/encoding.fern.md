# Text encodings of the Encoding Standard

This module decodes text from the legacy encodings of the WHATWG Encoding Standard to UTF-8.

## Model

A label names an encoding, as the standard lists them: `latin1` names windows-1252, and `sjis` names Shift_JIS. `label` gives the name of the encoding of a label. A `Decoder` holds the index of one encoding, and `decode` turns bytes of that encoding into UTF-8. Each byte sequence that is not valid becomes U+FFFD, as the decoders of the standard do.

The module decodes the single-byte encodings, gb18030 and GBK, Big5, EUC-JP, ISO-2022-JP, Shift_JIS, EUC-KR, UTF-16LE, UTF-16BE, x-user-defined and UTF-8. The encoding "replacement" is not there: `label` gives "" for its labels.

## Contracts

`label` ignores the case of ASCII letters and the ASCII white space around the label. The decoders follow the algorithms of the standard, and they give the same text as the crate encoding_rs 0.8.33. A decode reads the whole input: a sequence that the end cuts gives U+FFFD. A byte order mark is text: the caller removes it.

## Executable example

```fern test name=std-encoding-example
import "std/encoding";

function main(): i32 {
  if (encoding.label(" SJIS ") != "Shift_JIS") { return 1; }
  let d = encoding.decoder("Shift_JIS");
  if (d.decode([130u8, 160u8, 65u8]) != "あA") { return 2; }
  if (encoding.decoder("windows-1251").decode([207u8]) != "П") { return 3; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<dependencies>>

<<labels>>

<<decoders>>

<<multi-byte decoders>>
```

### `dependencies`

```fern
<<dependencies>>=
import "std/encoding/indexes";
import "std/string";
```

### Labels

`LABELS` holds each label of the standard with the name of its encoding. A search reads the list in order.

```fern
<<labels>>=
// label gives the name of the encoding of a label, or "" for a label that
// the standard does not list and for a label of the encoding "replacement".
pub function label(text: string): string {
  let key = text.trim().to_ascii_lower();
  let t = indexes.LABELS;
  let at = 0;
  while (at < t.len()) {
    let eq = __memchr(t, 61, at);
    let semi = __memchr(t, 59, eq);
    if (eq - at == key.len() && __enc_same(t, at, key)) {
      let name = slice_unchecked(t, eq + 1, semi).to_owned();
      return if (name == "replacement") { "" } else { name };
    }
    at = semi + 1;
  }
  return "";
}

function __enc_same(t: string, at: i32, key: string): boolean {
  for k in 0..key.len() {
    if (t[at + k] != key[k]) {
      return false;
    }
  }
  return true;
}
```

### Decoders

A `Decoder` holds the name of its encoding and its index: for a single-byte encoding, the code points of the bytes 0x80 to 0xFF. -1 marks a pointer with no code point. An index is a string of tokens that `__enc_index` expands, as `cmd/encodinggen/gen.py` writes it.

```fern
<<decoders>>=
// A decoder of one encoding: its name, its index, and for EUC-JP the index
// of JIS X 0212.
pub struct Decoder { name: string, table: i32[], extra: i32[] }

const __ENC_ALPHABET: string = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

// decoder gives the decoder of the encoding `name`, which `label` gives. An
// unknown name gives a decoder of UTF-8.
pub function decoder(name: string): Decoder {
  let none: i32[] = [];
  let extra = if (name == "EUC-JP") { __enc_index("jis0212") } else { none };
  return Decoder { name: name, table: __enc_index(__enc_key(name)), extra: extra };
}

// The name of the index of an encoding.
function __enc_key(name: string): string {
  let t = "gb18030=gb18030;GBK=gb18030;Big5=big5;EUC-JP=jis0208;ISO-2022-JP=jis0208;Shift_JIS=jis0208;EUC-KR=euc-kr;ISO-8859-8-I=iso-8859-8;";
  let at = __enc_find(t, name + "=");
  if (at < 0) {
    return name.to_ascii_lower();
  }
  let start = at + name.len() + 1;
  return slice_unchecked(t, start, __memchr(t, 59, start)).to_owned();
}

// The offset of `key` at the start of an entry of `t`, or -1.
function __enc_find(t: string, key: string): i32 {
  let at = 0;
  while (at < t.len()) {
    if (at + key.len() <= t.len() && __enc_same(t, at, key)) {
      return at;
    }
    at = __memchr(t, 59, at) + 1;
  }
  return -1;
}

// The expanded index of the name `key`, or [] for none.
function __enc_index(key: string): i32[] {
  let none: i32[] = [];
  let k = 0;
  while (k < indexes.INDEX_NAMES.len() && indexes.INDEX_NAMES[k] != key) {
    k += 1;
  }
  if (k == indexes.INDEX_NAMES.len()) {
    return none;
  }
  let tokens = indexes.INDEXES[k];
  let out = __alloc_i32(__enc_count(tokens));
  let at = 0;
  let prev = 0;
  let n = 0;
  while (at < tokens.len()) {
    let (v, next) = __enc_number(tokens, at);
    at = next;
    if ((v & 1) == 1) {
      for _ in 0..v >> 1 {
        out = out.with(n, -1);
        n += 1;
      }
    } else {
      let z = v >> 1;
      prev += if ((z & 1) == 0) { z >> 1 } else { -(z + 1 >> 1) };
      out = out.with(n, prev);
      n += 1;
    }
  }
  return out;
}

// The count of pointers of an index.
function __enc_count(tokens: string): i32 {
  let at = 0;
  let n = 0;
  while (at < tokens.len()) {
    let (v, next) = __enc_number(tokens, at);
    at = next;
    n += if ((v & 1) == 1) { v >> 1 } else { 1 };
  }
  return n;
}

// The number of the token at `at`, and the offset after it.
function __enc_number(tokens: string, at: i32): (i32, i32) {
  let v = 0;
  let shift = 0;
  let i = at;
  while (true) {
    let c = __memchr(__ENC_ALPHABET, tokens[i] as i32, 0);
    i += 1;
    v |= (c & 31) << shift;
    shift += 5;
    if (c < 32) {
      return (v, i);
    }
  }
  return (v, i);
}

// decode gives the text of `bytes` in the encoding of the decoder, as
// UTF-8. Each sequence that is not valid is U+FFFD.
pub function (d: Decoder) decode(bytes: u8[]): string {
  let h = buf_new(bytes.len() + bytes.len() / 2);
  match (d.name) {
    "gb18030" | "GBK" => {
      __enc_gb18030(d, h, bytes);
    },
    "Big5" => {
      __enc_big5(d, h, bytes);
    },
    "EUC-JP" => {
      __enc_euc_jp(d, h, bytes);
    },
    "ISO-2022-JP" => {
      __enc_iso_2022_jp(d, h, bytes);
    },
    "Shift_JIS" => {
      __enc_shift_jis(d, h, bytes);
    },
    "EUC-KR" => {
      __enc_euc_kr(d, h, bytes);
    },
    _ => {
      __enc_decode_other(d, h, bytes);
    }
  }
  let out = buf_take(h);
  buf_free(h);
  return out;
}

// The decode of UTF-16, x-user-defined, UTF-8 and the single-byte
// encodings.
function __enc_decode_other(d: Decoder, h: usize, bytes: u8[]): void {
  match (d.name) {
    "UTF-16LE" => {
      __enc_utf16(h, bytes, false);
    },
    "UTF-16BE" => {
      __enc_utf16(h, bytes, true);
    },
    "x-user-defined" => {
      __enc_user_defined(h, bytes);
    },
    "UTF-8" => {
      __enc_utf8(h, bytes);
    },
    _ => {
      __enc_single_byte(d, h, bytes);
    }
  }
}

// Appends the UTF-8 form of the code point `cp`, or U+FFFD for -1.
function __enc_push(h: usize, cp0: i32): void {
  let cp = if (cp0 < 0) { 65533 } else { cp0 };
  if (cp < 128) {
    buf_push_byte(h, cp);
  } else if (cp < 2048) {
    buf_push_byte(h, 192 | cp >> 6);
    buf_push_byte(h, 128 | cp & 63);
  } else if (cp < 65536) {
    buf_push_byte(h, 224 | cp >> 12);
    buf_push_byte(h, 128 | cp >> 6 & 63);
    buf_push_byte(h, 128 | cp & 63);
  } else {
    buf_push_byte(h, 240 | cp >> 18);
    buf_push_byte(h, 128 | cp >> 12 & 63);
    buf_push_byte(h, 128 | cp >> 6 & 63);
    buf_push_byte(h, 128 | cp & 63);
  }
}

function __enc_single_byte(d: Decoder, h: usize, bytes: u8[]): void {
  for b in bytes {
    let c = b as i32;
    __enc_push(h, if (c < 128) { c } else { d.table[c - 128] });
  }
}

function __enc_user_defined(h: usize, bytes: u8[]): void {
  for b in bytes {
    let c = b as i32;
    __enc_push(h, if (c < 128) { c } else { 63360 + c - 128 });
  }
}

// The code point of `pointer` in `table`, or -1 for none.
function __enc_at(table: i32[], pointer: i32): i32 {
  return if (pointer < 0 || pointer >= table.len()) { -1 } else { table[pointer] };
}

// The bytes as UTF-16, big-endian when `be`. A surrogate without its other
// half and a last odd byte are U+FFFD.
function __enc_utf16(h: usize, bytes: u8[], be: boolean): void {
  let i = 0;
  while (i + 1 < bytes.len()) {
    let u = __enc_unit(bytes, i, be);
    i += 2;
    if (u >= 55296 && u <= 56319 && i + 1 < bytes.len()) {
      let v = __enc_unit(bytes, i, be);
      if (v >= 56320 && v <= 57343) {
        __enc_push(h, 65536 + (u - 55296 << 10) + (v - 56320));
        i += 2;
        continue;
      }
    }
    __enc_push(h, if (u >= 55296 && u <= 57343) { -1 } else { u });
  }
  if (i < bytes.len()) {
    __enc_push(h, -1);
  }
}

function __enc_unit(bytes: u8[], i: i32, be: boolean): i32 {
  if (be) {
    return bytes[i] as i32 << 8 | bytes[i + 1] as i32;
  }
  return bytes[i + 1] as i32 << 8 | bytes[i] as i32;
}

// The bytes as UTF-8, with each maximal sequence that is not valid as
// U+FFFD.
function __enc_utf8(h: usize, bytes: u8[]): void {
  let i = 0;
  while (i < bytes.len()) {
    let (n, k) = __enc_utf8_run(bytes, i);
    if (n > 0 && k == n) {
      for j in 0..n {
        buf_push_byte(h, bytes[i + j] as i32);
      }
    } else {
      __enc_push(h, -1);
    }
    i += k;
  }
}

// The count of bytes of the character that starts at `i`, or 0, and the
// count of its bytes that are there and valid.
function __enc_utf8_run(bytes: u8[], i: i32): (i32, i32) {
  let (lo, hi, n) = __enc_utf8_lead(bytes[i] as i32);
  let k = 1;
  while (k < n && i + k < bytes.len()) {
    let c = bytes[i + k] as i32;
    if (c < (if (k == 1) { lo } else { 128 }) || c > (if (k == 1) { hi } else { 191 })) {
      return (n, k);
    }
    k += 1;
  }
  return (n, k);
}

// The range of the second byte after the lead byte `b`, and the count of
// bytes, or a count of 0 for a byte that leads nothing.
function __enc_utf8_lead(b: i32): (i32, i32, i32) {
  if (b < 128) {
    return (0, 0, 1);
  }
  if (b < 194 || b > 244) {
    return (0, 0, 0);
  }
  if (b < 224) {
    return (128, 191, 2);
  }
  if (b < 240) {
    return (if (b == 224) { 160 } else { 128 }, if (b == 237) { 159 } else { 191 }, 3);
  }
  return (if (b == 240) { 144 } else { 128 }, if (b == 244) { 143 } else { 191 }, 4);
}
```

### Multi-byte decoders

Each decoder reads a lead byte, then one or more trail bytes. A trail byte that does not give a code point gives U+FFFD. Then a trail byte in ASCII is read again as the start of the next character, as the standard says.

```fern
<<multi-byte decoders>>=
// The code point of a four-byte pointer of gb18030, or -1.
function __enc_gb_ranges(p: i32): i32 {
  if (p > 39419 && p < 189000 || p > 1237575) {
    return -1;
  }
  if (p == 7457) {
    return 59335;
  }
  let r = indexes.GB18030_RANGES;
  let k = 0;
  while (k + 2 < r.len() && r[k + 2] <= p) {
    k += 2;
  }
  return r[k + 1] + p - r[k];
}

function __enc_gb18030(d: Decoder, h: usize, b: u8[]): void {
  let i = 0;
  while (i < b.len()) {
    let x = b[i] as i32;
    if (x < 128 || x == 128 || x == 255) {
      __enc_push(h, if (x < 128) { x } else if (x == 128) { 8364 } else { -1 });
      i += 1;
    } else {
      i = __enc_gb_char(d, h, b, i);
    }
  }
}

// The character of gb18030 that starts with the lead byte at `i`, and the
// offset after it.
function __enc_gb_char(d: Decoder, h: usize, b: u8[], i: i32): i32 {
  let lead = b[i] as i32;
  if (i + 1 >= b.len()) {
    __enc_push(h, -1);
    return i + 1;
  }
  let x = b[i + 1] as i32;
  if (x >= 48 && x <= 57) {
    return __enc_gb_four(h, b, i);
  }
  let off = if (x < 127) { 64 } else { 65 };
  let cp = if (__enc_in(x, 64, 126) || __enc_in(x, 128, 254)) { __enc_at(d.table, (lead - 129) * 190 + x - off) } else { -1 };
  return __enc_trail(h, cp, x, i);
}

// Whether `x` is from `lo` to `hi`.
function __enc_in(x: i32, lo: i32, hi: i32): boolean {
  return x >= lo && x <= hi;
}

// Writes the code point of a lead byte at `i` and a trail byte `x`, and
// gives the offset after them. With no code point, a trail byte in ASCII is
// read again.
function __enc_trail(h: usize, cp: i32, x: i32, i: i32): i32 {
  __enc_push(h, cp);
  return if (cp < 0 && x < 128) { i + 1 } else { i + 2 };
}

// A four-byte sequence of gb18030 at `i`, and the offset after it.
function __enc_gb_four(h: usize, b: u8[], i: i32): i32 {
  if (i + 2 >= b.len() || b[i + 2] < 129u8 || b[i + 2] > 254u8) {
    __enc_push(h, -1);
    return i + (if (i + 2 >= b.len()) { 2 } else { 1 });
  }
  if (i + 3 >= b.len() || b[i + 3] < 48u8 || b[i + 3] > 57u8) {
    __enc_push(h, -1);
    return i + (if (i + 3 >= b.len()) { 3 } else { 1 });
  }
  let p = (b[i] as i32 - 129) * 12600 + (b[i + 1] as i32 - 48) * 1260 + (b[i + 2] as i32 - 129) * 10 + b[i + 3] as i32 - 48;
  __enc_push(h, __enc_gb_ranges(p));
  return i + 4;
}

function __enc_big5(d: Decoder, h: usize, b: u8[]): void {
  let i = 0;
  while (i < b.len()) {
    let x = b[i] as i32;
    if (x < 128 || x == 128 || x == 255) {
      __enc_push(h, if (x < 128) { x } else { -1 });
      i += 1;
    } else if (i + 1 >= b.len()) {
      __enc_push(h, -1);
      i += 1;
    } else {
      i = __enc_big5_char(d, h, x, b[i + 1] as i32, i);
    }
  }
}

function __enc_big5_char(d: Decoder, h: usize, lead: i32, x: i32, i: i32): i32 {
  let off = if (x < 127) { 64 } else { 98 };
  let p = if (__enc_in(x, 64, 126) || __enc_in(x, 161, 254)) { (lead - 129) * 157 + x - off } else { -1 };
  let pair = [1133, 202, 772, 1135, 202, 780, 1164, 234, 772, 1166, 234, 780];
  for k in 0..4 {
    if (p == pair[3 * k]) {
      __enc_push(h, pair[3 * k + 1]);
      __enc_push(h, pair[3 * k + 2]);
      return i + 2;
    }
  }
  return __enc_trail(h, __enc_at(d.table, p), x, i);
}

function __enc_euc_kr(d: Decoder, h: usize, b: u8[]): void {
  let i = 0;
  while (i < b.len()) {
    let x = b[i] as i32;
    if (x < 128 || x == 128 || x == 255 || i + 1 >= b.len()) {
      __enc_push(h, if (x < 128) { x } else { -1 });
      i += 1;
    } else {
      let t = b[i + 1] as i32;
      i = __enc_trail(h, if (__enc_in(t, 65, 254)) { __enc_at(d.table, (x - 129) * 190 + t - 65) } else { -1 }, t, i);
    }
  }
}

function __enc_shift_jis(d: Decoder, h: usize, b: u8[]): void {
  let i = 0;
  while (i < b.len()) {
    let x = b[i] as i32;
    if (x <= 128 || x >= 161 && x <= 223) {
      __enc_push(h, if (x <= 128) { x } else { 65377 - 161 + x });
      i += 1;
    } else if (x == 160 || x >= 253 || i + 1 >= b.len()) {
      __enc_push(h, -1);
      i += 1;
    } else {
      i = __enc_sjis_char(d, h, x, b[i + 1] as i32, i);
    }
  }
}

function __enc_sjis_char(d: Decoder, h: usize, lead: i32, x: i32, i: i32): i32 {
  let off = if (x < 127) { 64 } else { 65 };
  let loff = if (lead < 160) { 129 } else { 193 };
  let p = if (__enc_in(x, 64, 126) || __enc_in(x, 128, 252)) { (lead - loff) * 188 + x - off } else { -1 };
  return __enc_trail(h, if (__enc_in(p, 8836, 10715)) { 57344 - 8836 + p } else { __enc_at(d.table, p) }, x, i);
}

function __enc_euc_jp(d: Decoder, h: usize, b: u8[]): void {
  let i = 0;
  while (i < b.len()) {
    let x = b[i] as i32;
    if (x < 128) {
      __enc_push(h, x);
      i += 1;
    } else if (x != 142 && x != 143 && (x < 161 || x > 254) || i + 1 >= b.len()) {
      __enc_push(h, -1);
      i += 1;
    } else {
      i = __enc_euc_jp_char(d, h, b, i);
    }
  }
}

// The character of EUC-JP at `i`: a half-width katakana after 0x8E, a
// character of JIS X 0212 after 0x8F, or one of JIS X 0208.
function __enc_euc_jp_char(d: Decoder, h: usize, b: u8[], i: i32): i32 {
  let lead = b[i] as i32;
  let x = b[i + 1] as i32;
  if (lead == 142 && __enc_in(x, 161, 223)) {
    __enc_push(h, 65377 - 161 + x);
    return i + 2;
  }
  if (lead == 143 && __enc_in(x, 161, 254)) {
    return __enc_jis0212(d, h, b, i);
  }
  let valid = __enc_in(lead, 161, 254) && __enc_in(x, 161, 254);
  return __enc_trail(h, if (valid) { __enc_at(d.table, (lead - 161) * 94 + x - 161) } else { -1 }, x, i);
}

// A character of JIS X 0212 after the 0x8F at `i`, and the offset after it.
function __enc_jis0212(d: Decoder, h: usize, b: u8[], i: i32): i32 {
  if (i + 2 >= b.len()) {
    __enc_push(h, -1);
    return i + 2;
  }
  let x = b[i + 1] as i32;
  let t = b[i + 2] as i32;
  return 1 + __enc_trail(h, if (__enc_in(t, 161, 254)) { __enc_at(d.extra, (x - 161) * 94 + t - 161) } else { -1 }, t, i);
}

// The states of ISO-2022-JP: ASCII, Roman, katakana, a lead byte and a
// trail byte of JIS X 0208.
const __ENC_ASCII: i32 = 0;

const __ENC_ROMAN: i32 = 1;

const __ENC_KATAKANA: i32 = 2;

const __ENC_LEAD: i32 = 3;

function __enc_iso_2022_jp(d: Decoder, h: usize, b: u8[]): void {
  let state = __ENC_ASCII;
  let fresh = false;
  let i = 0;
  while (i < b.len()) {
    let x = b[i] as i32;
    if (x == 27) {
      let (next, to) = __enc_escape(b, i);
      if (to < 0) {
        __enc_push(h, -1);
        fresh = false;
        i = next;
        continue;
      }
      if (fresh) {
        __enc_push(h, -1);
      }
      state = to;
      fresh = true;
      i = next;
      continue;
    }
    fresh = false;
    i = __enc_iso_char(d, h, b, i, state);
  }
}

// The escape sequence at `i`: the offset after it, and its state, or -1 for
// a sequence that is not valid and the offset to read next.
function __enc_escape(b: u8[], i: i32): (i32, i32) {
  if (i + 2 >= b.len()) {
    return (i + 1, -1);
  }
  let pair = unsafe { string_from_bytes_unchecked([b[i + 1], b[i + 2]]) };
  let at = __enc_find("(B;(J;(I;$@;$B;", pair);
  return if (at < 0) { (i + 1, -1) } else { (i + 3, if (at / 3 > 3) { __ENC_LEAD } else { at / 3 }) };
}

// One character at `i` in the state `state`, and the offset after it.
function __enc_iso_char(d: Decoder, h: usize, b: u8[], i: i32, state: i32): i32 {
  let x = b[i] as i32;
  if (state == __ENC_LEAD) {
    return __enc_iso_jis(d, h, b, i);
  }
  if (state == __ENC_KATAKANA) {
    __enc_push(h, if (__enc_in(x, 33, 95)) { 65377 - 33 + x } else { -1 });
  } else {
    __enc_push(h, __enc_roman(x, state == __ENC_ROMAN));
  }
  return i + 1;
}

// The code point of a byte in the state ASCII, or with `roman` in the state
// Roman, or -1.
function __enc_roman(x: i32, roman: boolean): i32 {
  if (x == 14 || x == 15 || x > 127) {
    return -1;
  }
  if (roman && x == 92) {
    return 165;
  }
  return if (roman && x == 126) { 8254 } else { x };
}

// A character of JIS X 0208 in ISO-2022-JP at `i`, and the offset after
// it. An escape or the end after the lead byte gives U+FFFD.
function __enc_iso_jis(d: Decoder, h: usize, b: u8[], i: i32): i32 {
  let x = b[i] as i32;
  if (!__enc_in(x, 33, 126) || i + 1 >= b.len() || b[i + 1] == 27u8) {
    __enc_push(h, -1);
    return i + 1;
  }
  let t = b[i + 1] as i32;
  __enc_push(h, if (__enc_in(t, 33, 126)) { __enc_at(d.table, (x - 33) * 94 + t - 33) } else { -1 });
  return i + 2;
}
```
