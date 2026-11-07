# HPACK header compression

This module compresses and decompresses HTTP/2 headers with HPACK.

## Model

HPACK represents fields through static entries, dynamic entries, and literals. Encoder and decoder state retain the dynamic table across header blocks.

## Contracts

Table capacity, field count, and list size have separate limits. Sensitive fields carry an explicit flag so the encoder can select their representation.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Field>>

<<4 pub function field(name: string, value: string): Field>>

<<5 pub enum HpackError>>

<<6 pub function (e: HpackError) to_string(): string>>

<<7 impl error.Error for HpackError>>

<<8 pub function (e: HpackError) message(): string>>

<<9 pub struct Table>>

<<10 pub function table(max: i32): Table>>

<<11 function __entry_size(name: string, value: string): i32>>

<<12 const STATIC_LEN: i32>>

<<13 function __name_at(t: Table, index: i32): string>>

<<14 pub struct Decoder>>

<<15 pub function decoder(max_table: i32, max_list: i32): Decoder>>

<<16 pub struct Decoded>>

<<17 pub function (d: Decoder) decode(block: u8[]): Result[Decoded, HpackError]>>

<<18 function __failed(st: Decoder, e: HpackError): Decoder>>

<<19 pub struct Encoder>>

<<20 pub function encoder(max_table: i32): Encoder>>

<<21 pub struct Encoded>>

<<22 pub function (e: Encoder) encode(fields: Field[]): Encoded>>

<<23 pub struct Encoding>>

<<24 pub function (e: Encoder) begin(): Encoding>>

<<25 function __abs(n: i32): i32>>

<<26 const STATIC_NAMES: string[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module compresses and decompresses HTTP/2 headers with HPACK.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/array";
import "std/http/huffman";
import "std/string";
import "core/error";
import "core/cmp";
```

### `3 pub struct Field`

```fern
<<3 pub struct Field>>=
pub struct Field { name: string, value: string, sensitive: boolean }
```

### `4 pub function field(name: string, value: string): Field`

```fern
<<4 pub function field(name: string, value: string): Field>>=
pub function field(name: string, value: string): Field {
  return Field { name: name, value: value, sensitive: false };
}

pub function sensitive(name: string, value: string): Field {
  return Field { name: name, value: value, sensitive: true };
}
```

### `5 pub enum HpackError`

```fern
<<5 pub enum HpackError>>=
pub enum HpackError {
  Truncated,
  BadIndex(i32),
  BadInteger,
  BadHuffman,
  BadSizeUpdate(i32),
  TooLarge(i32),
  TooMany(i32),
}
```

### `6 pub function (e: HpackError) to_string(): string`

```fern
<<6 pub function (e: HpackError) to_string(): string>>=
pub function (e: HpackError) to_string(): string {
  return e.message();
}
```

### `7 impl error.Error for HpackError`

```fern
<<7 impl error.Error for HpackError>>=
impl error.Error for HpackError {}

impl cmp.Display for HpackError {}
```

### `8 pub function (e: HpackError) message(): string`

```fern
<<8 pub function (e: HpackError) message(): string>>=
pub function (e: HpackError) message(): string {
  match (e) {
    Truncated => {
      return "hpack: header block ends inside a field";
    },
    BadIndex(i) => {
      return "hpack: no table entry " + i.to_string();
    },
    BadInteger => {
      return "hpack: integer over 28 bits";
    },
    BadHuffman => {
      return "hpack: bad Huffman string";
    },
    BadSizeUpdate(n) => {
      return "hpack: dynamic table size update to " + n.to_string() + " not allowed here";
    },
    TooLarge(n) => {
      return "hpack: header list over " + n.to_string() + " bytes";
    },
    TooMany(n) => {
      return "hpack: more than " + n.to_string() + " header fields";
    }
  }
  return "hpack: error";
}
```

### `9 pub struct Table`

```fern
<<9 pub struct Table>>=
pub struct Table { names: string[], values: string[], size: i32, max: i32 }
```

### `10 pub function table(max: i32): Table`

```fern
<<10 pub function table(max: i32): Table>>=
pub function table(max: i32): Table {
  return Table { names: [], values: [], size: 0, max: max };
}

pub function (t: Table) len(): i32 {
  return t.names.len();
}

pub function (t: Table) size(): i32 {
  return t.size;
}
```

### `11 function __entry_size(name: string, value: string): i32`

```fern
<<11 function __entry_size(name: string, value: string): i32>>=
function __entry_size(name: string, value: string): i32 {
  return name.len() + value.len() + 32;
}

function __added(t: Table, name: string, value: string): Table {
  let need = __entry_size(name, value);
  let room = __evicted(t, t.max - need);
  if (need > t.max) {
    return room;
  }
  let names = [name];
  let values = [value];
  let i = 0;
  while (i < room.names.len()) {
    names = names.append(room.names[i]);
    values = values.append(room.values[i]);
    i += 1;
  }
  return Table { names: names, values: values, size: room.size + need, max: t.max };
}

function __evicted(t: Table, limit: i32): Table {
  let keep = t.names.len();
  let size = t.size;
  while (keep > 0 && size > limit) {
    keep -= 1;
    size -= __entry_size(t.names[keep], t.values[keep]);
  }
  if (keep == t.names.len()) {
    return t;
  }
  let names: string[] = [];
  let values: string[] = [];
  let i = 0;
  while (i < keep) {
    names = names.append(t.names[i]);
    values = values.append(t.values[i]);
    i += 1;
  }
  return Table { names: names, values: values, size: size, max: t.max };
}

function __resized(t: Table, max: i32): Table {
  let out = __evicted(t, max);
  return Table { ...out, max: max };
}
```

### `12 const STATIC_LEN: i32`

```fern
<<12 const STATIC_LEN: i32>>=
const STATIC_LEN: i32 = 61;
```

### `13 function __name_at(t: Table, index: i32): string`

```fern
<<13 function __name_at(t: Table, index: i32): string>>=
function __name_at(t: Table, index: i32): string {
  if (index >= 1 && index <= STATIC_LEN) {
    return STATIC_NAMES[index - 1];
  }
  let at = index - STATIC_LEN - 1;
  if (at >= 0 && at < t.names.len()) {
    return t.names[at];
  }
  return "";
}

function __find(t: Table, name: string, value: string): i32 {
  let found = __find_static(name, value);
  if (found > 0) {
    return found;
  }
  let name_only = -found;
  let i = 0;
  while (i < t.names.len()) {
    if (t.names[i] == name) {
      if (t.values[i] == value) {
        return STATIC_LEN + 1 + i;
      }
      if (name_only == 0) {
        name_only = STATIC_LEN + 1 + i;
      }
    }
    i += 1;
  }
  return -name_only;
}

function __find_static(name: string, value: string): i32 {
  let n = name.len();
  if (n + 1 >= STATIC_LENGTH_FROM.len()) {
    return 0;
  }
  let k = STATIC_LENGTH_FROM[n];
  while (k < STATIC_LENGTH_FROM[n + 1]) {
    let i = STATIC_BY_LENGTH[k];
    if (STATIC_NAMES[i] == name) {
      let first = i;
      while (i < STATIC_LEN && STATIC_NAMES[i] == name) {
        if (STATIC_VALUES[i] == value) {
          return i + 1;
        }
        i += 1;
      }
      return 0 - (first + 1);
    }
    k += 1;
  }
  return 0;
}
```

### `14 pub struct Decoder`

```fern
<<14 pub struct Decoder>>=
pub struct Decoder { table: Table, settings_max: i32, max_list: i32, max_fields: i32, at: i32, end: i32, count: i32, total: i32, started: boolean, num: i32, text: string, has: boolean, name: string, value: string, sensitive: boolean, add: boolean, failed: boolean, error: Option[HpackError] }
```

### `15 pub function decoder(max_table: i32, max_list: i32): Decoder`

```fern
<<15 pub function decoder(max_table: i32, max_list: i32): Decoder>>=
pub function decoder(max_table: i32, max_list: i32): Decoder {
  return Decoder { table: table(max_table), settings_max: max_table, max_list: max_list, max_fields: 256, at: 0, end: 0, count: 0, total: 0, started: false, num: 0, text: "", has: false, name: "", value: "", sensitive: false, add: false, failed: false, error: None };
}

pub function (d: Decoder) with_max_fields(n: i32): Decoder {
  return Decoder { ...d, max_fields: n };
}

pub function (d: Decoder) with_max_table(n: i32): Decoder {
  let t = d.table;
  if (n < t.max) {
    t = __resized(t, n);
  }
  return Decoder { ...d, table: t, settings_max: n };
}

pub function (d: Decoder) table_size(): i32 {
  return d.table.size;
}
```

### `16 pub struct Decoded`

```fern
<<16 pub struct Decoded>>=
pub struct Decoded { decoder: Decoder, fields: Field[] }
```

### `17 pub function (d: Decoder) decode(block: u8[]): Result[Decoded, HpackError]`

```fern
<<17 pub function (d: Decoder) decode(block: u8[]): Result[Decoded, HpackError]>>=
pub function (d: Decoder) decode(block: u8[]): Result[Decoded, HpackError] {
  let st = d.start(block);
  let fields: Field[] = [];
  while (st.more()) {
    st = st.next(block);
    if (st.has) {
      fields = fields.append(Field { name: st.name, value: st.value, sensitive: st.sensitive });
    }
  }
  match (st.error) {
    Some(e) => {
      return Err(e);
    },
    None => {}
  }
  return Ok(Decoded { decoder: st, fields: fields });
}

pub function (d: Decoder) start(block: u8[]): Decoder {
  return d.start_in(0, block.len());
}

pub function (d: Decoder) start_in(from: i32, end: i32): Decoder {
  return Decoder { ...d, at: from, end: end, count: 0, total: 0, started: false, text: "", has: false, name: "", value: "", failed: false, error: None };
}

pub function (st: Decoder) more(): boolean {
  return st.at < st.end && !st.failed;
}

pub function (st: Decoder) next(block: u8[]): Decoder {
  let b = block[st.at] as i32;
  if (b >= 32 && b < 64) {
    return __size_update(st, block);
  }
  if (b >= 128) {
    st = __indexed(st, block);
  } else {
    if (b >= 64) {
      st = __literal(st, block, 6, true, false);
    } else {
      st = __literal(st, block, 4, false, b >= 16);
    }
  }
  if (st.failed) {
    return st;
  }
  let total = st.total + __entry_size(st.name, st.value);
  if (total > st.max_list) {
    return __failed(st, TooLarge(st.max_list));
  }
  if (st.count >= st.max_fields) {
    return __failed(st, TooMany(st.max_fields));
  }
  if (st.add) {
    let name = st.name;
    let value = st.value;
    return Decoder { ...st, table: __added(st.table, name, value), count: st.count + 1, total: total, started: true, has: true };
  }
  return Decoder { ...st, count: st.count + 1, total: total, started: true, has: true };
}
```

### `18 function __failed(st: Decoder, e: HpackError): Decoder`

```fern
<<18 function __failed(st: Decoder, e: HpackError): Decoder>>=
function __failed(st: Decoder, e: HpackError): Decoder {
  return Decoder { ...st, failed: true, error: Some(e) };
}

function __indexed(st: Decoder, block: u8[]): Decoder {
  st = __integer(st, block, 7);
  if (st.failed) {
    return st;
  }
  let index = st.num;
  if (index >= 1 && index <= STATIC_LEN) {
    return Decoder { ...st, name: STATIC_NAMES[index - 1], value: STATIC_VALUES[index - 1], sensitive: false, add: false };
  }
  let at = index - STATIC_LEN - 1;
  if (at < 0 || at >= st.table.names.len()) {
    return __failed(st, BadIndex(index));
  }
  return Decoder { ...st, name: st.table.names[at], value: st.table.values[at], sensitive: false, add: false };
}

function __size_update(st: Decoder, block: u8[]): Decoder {
  if (st.started) {
    return __failed(st, BadSizeUpdate(-1));
  }
  st = __integer(st, block, 5);
  if (st.failed) {
    return st;
  }
  if (st.num > st.settings_max) {
    return __failed(st, BadSizeUpdate(st.num));
  }
  let size = st.num;
  return Decoder { ...st, table: __resized(st.table, size), has: false };
}

function __literal(st: Decoder, block: u8[], prefix: i32, add: boolean, sensitive: boolean): Decoder {
  st = __integer(st, block, prefix);
  if (st.failed) {
    return st;
  }
  let name = "";
  if (st.num > 0) {
    name = __name_at(st.table, st.num);
    if (name.len() == 0 && st.num > STATIC_LEN) {
      return __failed(st, BadIndex(st.num));
    }
  } else {
    st = __string(st, block);
    if (st.failed) {
      return st;
    }
    name = st.text;
  }
  let total = st.total;
  st = __string(Decoder { ...st, total: total + name.len() }, block);
  if (st.failed) {
    return st;
  }
  let value = st.text;
  return Decoder { ...st, total: total, name: name, value: value, sensitive: sensitive, add: add };
}

function __integer(st: Decoder, block: u8[], prefix: i32): Decoder {
  let at = st.at;
  if (at >= st.end) {
    return __failed(st, Truncated);
  }
  let limit = (1 << prefix) - 1;
  let value = block[at] as i32 & limit;
  let i = at + 1;
  if (value < limit) {
    return Decoder { ...st, num: value, at: i };
  }
  let shift = 0;
  while (true) {
    if (i >= st.end) {
      return __failed(st, Truncated);
    }
    let b = block[i] as i32;
    i += 1;
    if (shift > 21) {
      return __failed(st, BadInteger);
    }
    value += (b & 127) << shift;
    if (value < 0) {
      return __failed(st, BadInteger);
    }
    shift += 7;
    if (b < 128) {
      return Decoder { ...st, num: value, at: i };
    }
  }
  return __failed(st, BadInteger);
}

function __string(st: Decoder, block: u8[]): Decoder {
  if (st.at >= st.end) {
    return __failed(st, Truncated);
  }
  let coded = block[st.at] as i32 >= 128;
  st = __integer(st, block, 7);
  if (st.failed) {
    return st;
  }
  let n = st.num;
  let from = st.at;
  if (n > st.end - from) {
    return __failed(st, Truncated);
  }
  let room = st.max_list - st.total - 32;
  if (!coded) {
    if (n > room) {
      return __failed(st, TooLarge(st.max_list));
    }
    unsafe {
      return Decoder { ...st, text: string_from_bytes_range_unchecked(block, from, from + n), at: from + n };
    }
  }
  match (huffman.decode(block[from:from + n], room)) {
    Ok(out) => {
      unsafe {
        return Decoder { ...st, text: string_from_bytes_unchecked(out), at: from + n };
      }
    },
    Err(huffman.ExceedsRoom) => {
      return __failed(st, TooLarge(st.max_list));
    },
    Err(_) => {
      return __failed(st, BadHuffman);
    }
  }
  return st;
}
```

### `19 pub struct Encoder`

```fern
<<19 pub struct Encoder>>=
pub struct Encoder { table: Table, update: i32, huffman: boolean }
```

### `20 pub function encoder(max_table: i32): Encoder`

```fern
<<20 pub function encoder(max_table: i32): Encoder>>=
pub function encoder(max_table: i32): Encoder {
  return Encoder { table: table(max_table), update: 0 - 1, huffman: true };
}

pub function (e: Encoder) with_max_table(n: i32): Encoder {
  return Encoder { ...e, table: __resized(e.table, n), update: n };
}

pub function (e: Encoder) without_huffman(): Encoder {
  return Encoder { ...e, huffman: false };
}

pub function (e: Encoder) table_size(): i32 {
  return e.table.size;
}
```

### `21 pub struct Encoded`

```fern
<<21 pub struct Encoded>>=
pub struct Encoded { encoder: Encoder, bytes: u8[] }
```

### `22 pub function (e: Encoder) encode(fields: Field[]): Encoded`

```fern
<<22 pub function (e: Encoder) encode(fields: Field[]): Encoded>>=
pub function (e: Encoder) encode(fields: Field[]): Encoded {
  let en = e.begin();
  for f in fields {
    en = en.put(f.name, f.value, f.sensitive);
  }
  return en.finish();
}
```

### `23 pub struct Encoding`

```fern
<<23 pub struct Encoding>>=
pub struct Encoding { encoder: Encoder, out: u8[] }
```

### `24 pub function (e: Encoder) begin(): Encoding`

```fern
<<24 pub function (e: Encoder) begin(): Encoding>>=
pub function (e: Encoder) begin(): Encoding {
  return e.begin_in([]);
}

pub function (e: Encoder) begin_in(own out: u8[]): Encoding {
  if (e.update < 0) {
    return Encoding { encoder: e, out: out };
  }
  return Encoding { encoder: Encoder { ...e, update: -1 }, out: __put_integer(out, 32, 5, e.update) };
}

pub function (en: Encoding) put(name: string, value: string, sensitive: boolean): Encoding {
  let e = en.encoder;
  let out = en.out;
  let found = __find(e.table, name, value);
  if (found > 0 && !sensitive) {
    return Encoding { encoder: e, out: __put_integer(out, 128, 7, found) };
  }
  if (sensitive) {
    out = __put_integer(out, 16, 4, __abs(found));
    return Encoding { encoder: e, out: __put_name_value(out, e, name, value, found) };
  }
  out = __put_integer(out, 64, 6, __abs(found));
  out = __put_name_value(out, e, name, value, found);
  return Encoding { encoder: Encoder { ...e, table: __added(e.table, name, value) }, out: out };
}

pub function (en: Encoding) finish(): Encoded {
  return Encoded { encoder: en.encoder, bytes: en.out };
}
```

### `25 function __abs(n: i32): i32`

```fern
<<25 function __abs(n: i32): i32>>=
function __abs(n: i32): i32 {
  if (n < 0) {
    return -n;
  }
  return n;
}

function __put_name_value(out: u8[], e: Encoder, name: string, value: string, found: i32): u8[] {
  let o = out;
  if (found == 0) {
    o = __put_string(o, e, name);
  }
  return __put_string(o, e, value);
}

function __put_integer(out: u8[], pattern: i32, prefix: i32, value: i32): u8[] {
  let limit: i32 = (1 << prefix) - 1;
  if (value < limit) {
    return out.append((pattern | value) as u8);
  }
  let o = out.append((pattern | limit) as u8);
  let rest = value - limit;
  while (rest >= 128) {
    o = o.append((rest & 127 | 128) as u8);
    rest = rest >> 7;
  }
  return o.append(rest as u8);
}

function __put_string(out: u8[], e: Encoder, s: string): u8[] {
  let bytes = s.bytes();
  if (e.huffman) {
    let coded = huffman.encoded_len(bytes);
    if (coded <= bytes.len()) {
      return huffman.encode(__put_integer(out, 128, 7, coded), bytes);
    }
  }
  let o = __put_integer(out, 0, 7, bytes.len());
  return o.concat(bytes);
}
```

### `26 const STATIC_NAMES: string[]`

```fern
<<26 const STATIC_NAMES: string[]>>=
const STATIC_NAMES: string[] = [
  ":authority", ":method", ":method", ":path",
  ":path", ":scheme", ":scheme", ":status",
  ":status", ":status", ":status", ":status",
  ":status", ":status", "accept-charset", "accept-encoding",
  "accept-language", "accept-ranges", "accept", "access-control-allow-origin",
  "age", "allow", "authorization", "cache-control",
  "content-disposition", "content-encoding", "content-language", "content-length",
  "content-location", "content-range", "content-type", "cookie",
  "date", "etag", "expect", "expires",
  "from", "host", "if-match", "if-modified-since",
  "if-none-match", "if-range", "if-unmodified-since", "last-modified",
  "link", "location", "max-forwards", "proxy-authenticate",
  "proxy-authorization", "range", "referer", "refresh",
  "retry-after", "server", "set-cookie", "strict-transport-security",
  "transfer-encoding", "user-agent", "vary", "via",
  "www-authenticate",
];

const STATIC_BY_LENGTH: i32[] = [
  20, 59, 32, 33, 36, 37, 44, 58, 3, 21, 49, 18, 31, 34, 53, 1, 5, 7, 35, 50, 51, 38, 41, 45, 0, 54,
  57, 52, 30, 46, 17, 22, 23, 29, 40, 43, 14, 27, 15, 16, 25, 26, 28, 60, 39, 56, 47, 24, 42, 48, 55, 19,
];

const STATIC_LENGTH_FROM: i32[] = [
  0, 0, 0, 0, 2, 8, 11, 15, 21, 24, 24, 27, 28, 30, 36, 38, 40, 44, 46, 47, 50, 50, 50, 50, 50, 50,
  51, 51, 52,
];

const STATIC_VALUES: string[] = [
  "", "GET", "POST", "/",
  "/index.html", "http", "https", "200",
  "204", "206", "304", "400",
  "404", "500", "", "gzip, deflate",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "", "", "", "",
  "",
];
```
