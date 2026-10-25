# QPACK field compression

This module compresses and decompresses HTTP/3 fields with QPACK.

## Model

QPACK separates field sections from encoder and decoder instruction streams. Dynamic-table references can make a field section wait for table progress.

## Contracts

Capacity, blocked-stream count, and field-list size have explicit limits. Protocol errors distinguish field decompression from the two instruction streams.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Field>>

<<4 pub function field(name: string, value: string): Field>>

<<5 pub enum QpackError>>

<<6 pub function (e: QpackError) to_string(): string>>

<<7 impl error.Error for QpackError>>

<<8 pub function (e: QpackError) code(): i64>>

<<9 const SHORT: i32>>

<<10 function __int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), i32]>>

<<11 struct Table>>

<<12 function (t: Table) __inserted(): i64>>

<<13 pub struct Decoder>>

<<14 pub function decoder(max_capacity: i64, max_blocked: i32, max_list: i64): Decoder>>

<<15 function (d: Decoder) __increment(): Decoder>>

<<16 pub function (d: Decoder) decode(stream: i64, section: [u8]): Decoder>>

<<17 function (d: Decoder) __decoded(stream: i64, section: [u8], required: i64, base: i64, at: i32): Decoder>>

<<18 pub function (d: Decoder) cancel(stream: i64): Decoder>>

<<19 function (d: Decoder) __blocked_on(stream: i64): Decoder>>

<<20 function __sec_str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), QpackError]>>

<<21 pub struct Encoder>>

<<22 pub function encoder(): Encoder>>

<<23 function (e: Encoder) __put_field(own out: u8[], f: Field): u8[]>>

<<24 pub function (e: Encoder) on_decoder_stream(bytes: [u8]): Result[Encoder, QpackError]>>

<<25 function __find_static(name: string, value: string): (i32, i32)>>

<<26 const STATIC_LEN_AT: i32[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module compresses and decompresses HTTP/3 fields with QPACK.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/http/huffman";
import "std/string";
import "std/error";
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

### `5 pub enum QpackError`

```fern
<<5 pub enum QpackError>>=
pub enum QpackError { DecompressionFailed(string), EncoderStreamError(string), DecoderStreamError(string) }
```

### `6 pub function (e: QpackError) to_string(): string`

```fern
<<6 pub function (e: QpackError) to_string(): string>>=
pub function (e: QpackError) to_string(): string {
  return e.message();
}
```

### `7 impl error.Error for QpackError`

```fern
<<7 impl error.Error for QpackError>>=
impl error.Error for QpackError {}

impl cmp.Display for QpackError {}

pub const QPACK_DECOMPRESSION_FAILED: i64 = 512;

pub const QPACK_ENCODER_STREAM_ERROR: i64 = 513;

pub const QPACK_DECODER_STREAM_ERROR: i64 = 514;
```

### `8 pub function (e: QpackError) code(): i64`

```fern
<<8 pub function (e: QpackError) code(): i64>>=
pub function (e: QpackError) code(): i64 {
  match (e) {
    DecompressionFailed(_) => {
      return QPACK_DECOMPRESSION_FAILED;
    },
    EncoderStreamError(_) => {
      return QPACK_ENCODER_STREAM_ERROR;
    },
    DecoderStreamError(_) => {
      return QPACK_DECODER_STREAM_ERROR;
    }
  }
}

pub function (e: QpackError) message(): string {
  match (e) {
    DecompressionFailed(why) => {
      return "qpack: " + why;
    },
    EncoderStreamError(why) => {
      return "qpack: encoder stream: " + why;
    },
    DecoderStreamError(why) => {
      return "qpack: decoder stream: " + why;
    }
  }
}
```

### `9 const SHORT: i32`

```fern
<<9 const SHORT: i32>>=
const SHORT: i32 = 0;

const TOO_BIG: i32 = 1;

const BAD_HUFFMAN: i32 = 2;

const TOO_LONG: i32 = 3;

const ENTRY_TOO_BIG: string = "an entry larger than the table's capacity";
```

### `10 function __int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), i32]`

```fern
<<10 function __int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), i32]>>=
function __int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), i32] {
  if (at >= buf.len()) {
    return Err(SHORT);
  }
  let limit: i64 = ((1 << prefix) - 1) as i64;
  let value: i64 = buf[at] as i64 & limit;
  let i: i32 = at + 1;
  if (value < limit) {
    return Ok((value, i));
  }
  let shift: i32 = 0;
  while (i < buf.len()) {
    let b: i64 = buf[i] as i64;
    i = i + 1;
    if (shift > 56) {
      return Err(TOO_BIG);
    }
    let part: i64 = (b & 127) << shift;
    if (part > 4611686018427387903 as i64 - value) {
      return Err(TOO_BIG);
    }
    value = value + part;
    shift = shift + 7;
    if (b < 128) {
      return Ok((value, i));
    }
  }
  return Err(SHORT);
}

function __str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), i32] {
  if (at >= buf.len()) {
    return Err(SHORT);
  }
  let coded: boolean = (buf[at] as i32 & 1 << prefix) != 0;
  let (n, from) = __int(buf, at, prefix)?;
  if (n > __most_coded(room, coded)) {
    return Err(TOO_LONG);
  }
  if (n > (buf.len() - from) as i64) {
    return Err(SHORT);
  }
  let end: i32 = from + n as i32;
  if (!coded) {
    return Ok((string_from_bytes_unchecked(__bytes_range(buf, from, end)), end));
  }
  let cap: i32 = 2147483647;
  if (room < cap as i64) {
    cap = room as i32;
  }
  match (huffman.decode(buf[from:end], cap)) {
    Ok(bytes) => {
      return Ok((string_from_bytes_unchecked(bytes), end));
    },
    Err(huffman.ExceedsRoom()) => {
      return Err(TOO_LONG);
    },
    Err(_) => {
      return Err(BAD_HUFFMAN);
    }
  }
}

function __most_coded(room: i64, coded: boolean): i64 {
  if (!coded) {
    return room;
  }
  if (room >= 1 << 56) {
    return 4611686018427387903 as i64;
  }
  return (30 * room + 7) / 8;
}

function __why(code: i32): string {
  if (code == SHORT) {
    return "truncated";
  }
  if (code == TOO_BIG) {
    return "an integer over 2^62";
  }
  if (code == BAD_HUFFMAN) {
    return "a bad Huffman string";
  }
  return "a field section larger than allowed";
}
```

### `11 struct Table`

```fern
<<11 struct Table>>=
struct Table { names: string[], values: string[], dropped: i64, size: i64, capacity: i64, max_capacity: i64 }
```

### `12 function (t: Table) __inserted(): i64`

```fern
<<12 function (t: Table) __inserted(): i64>>=
function (t: Table) __inserted(): i64 {
  return t.dropped + t.names.len() as i64;
}

function __entry_size(name: string, value: string): i64 {
  return (name.len() + value.len() + 32) as i64;
}

function (t: Table) __evicted(room: i64): Table {
  let cur: Table = t;
  let k: i32 = 0;
  let size: i64 = t.size;
  while (k < t.names.len() && size + room > t.capacity) {
    size = size - __entry_size(t.names[k], t.values[k]);
    k = k + 1;
  }
  return Table { ...cur, names: __from(t.names, k), values: __from(t.values, k), dropped: t.dropped + k as i64, size: size };
}

function (t: Table) __insert(name: string, value: string): Result[Table, QpackError] {
  let n: i64 = __entry_size(name, value);
  if (n > t.capacity) {
    return Err(EncoderStreamError(ENTRY_TOO_BIG));
  }
  let room: Table = t.__evicted(n);
  return Ok(Table { ...room, names: room.names.append(name), values: room.values.append(value), size: room.size + n });
}

function (t: Table) __entry(a: i64): Result[Field, QpackError] {
  if (a < t.dropped || a >= t.__inserted()) {
    return Err(DecompressionFailed("a reference to a dynamic table entry that is not there"));
  }
  let k: i32 = (a - t.dropped) as i32;
  return Ok(field(t.names[k], t.values[k]));
}

function __static(i: i64): Result[Field, QpackError] {
  if (i < 0 || i >= STATIC.len() as i64) {
    return Err(DecompressionFailed("no static table entry " + i.to_string()));
  }
  return Ok(STATIC[i as i32]);
}
```

### `13 pub struct Decoder`

```fern
<<13 pub struct Decoder>>=
pub struct Decoder { table: Table, max_blocked: i32, blocked: i64[], max_list: i64, pending: u8[], out: u8[], reported: i64, fields: Field[], waiting: boolean, failed: Option[QpackError] }
```

### `14 pub function decoder(max_capacity: i64, max_blocked: i32, max_list: i64): Decoder`

```fern
<<14 pub function decoder(max_capacity: i64, max_blocked: i32, max_list: i64): Decoder>>=
pub function decoder(max_capacity: i64, max_blocked: i32, max_list: i64): Decoder {
  let t: Table = Table { names: [], values: [], dropped: 0 as i64, size: 0 as i64, capacity: 0 as i64, max_capacity: max_capacity };
  return Decoder { table: t, max_blocked: max_blocked, blocked: [], max_list: max_list, pending: [], out: [], reported: 0 as i64, fields: [], waiting: false, failed: None };
}

pub function (d: Decoder) insert_count(): i64 {
  return d.table.__inserted();
}

pub function (d: Decoder) table_size(): i64 {
  return d.table.size;
}

pub function (d: Decoder) instructions_taken(): Decoder {
  return Decoder { ...d, out: [] };
}

pub function (d: Decoder) on_encoder_stream(bytes: [u8]): Result[Decoder, QpackError] {
  let buf: u8[] = d.pending.concat(__copy(bytes));
  let t: Table = d.table;
  let at: i32 = 0;
  while (at < buf.len()) {
    match (__instruction(t, buf, at)) {
      Ok(step) => {
        t = step.0;
        at = step.1;
      },
      Err(Some(e)) => {
        return Err(e);
      },
      Err(None()) => {
        return Ok(Decoder { ...d, table: t, pending: __bytes_range(buf, at, buf.len()) }.__increment());
      }
    }
  }
  return Ok(Decoder { ...d, table: t, pending: [] }.__increment());
}
```

### `15 function (d: Decoder) __increment(): Decoder`

```fern
<<15 function (d: Decoder) __increment(): Decoder>>=
function (d: Decoder) __increment(): Decoder {
  let n: i64 = d.table.__inserted() - d.reported;
  if (n <= 0) {
    return d;
  }
  return Decoder { ...d, out: __put_int(d.out, 0, 6, n), reported: d.table.__inserted() };
}

function __instruction(t: Table, buf: [u8], at: i32): Result[(Table, i32), Option[QpackError]] {
  let b: i32 = buf[at] as i32;
  if (b >= 128) {
    return __insert_named(t, buf, at);
  }
  if (b >= 64) {
    return __insert_literal(t, buf, at);
  }
  if (b >= 32) {
    let (cap, next) = __enc_int(buf, at, 5)?;
    if (cap > t.max_capacity) {
      return Err(Some(EncoderStreamError("a capacity over the maximum advertised")));
    }
    let shrunk: Table = Table { ...t, capacity: cap }.__evicted(0 as i64);
    return Ok((shrunk, next));
  }
  let (rel, after) = __enc_int(buf, at, 5)?;
  let e: Field = __enc_entry(t, t.__inserted() - 1 - rel)?;
  return Ok((__enc_insert(t, e.name, e.value)?, after));
}

function __insert_named(t: Table, buf: [u8], at: i32): Result[(Table, i32), Option[QpackError]] {
  let is_static: boolean = (buf[at] as i32 & 64) != 0;
  let (i, next) = __enc_int(buf, at, 6)?;
  let name: string = "";
  if (is_static) {
    name = __enc_static(i)?.name;
  } else {
    name = __enc_entry(t, t.__inserted() - 1 - i)?.name;
  }
  let (value, end) = __enc_str(buf, next, 7, t.__room(name))?;
  return Ok((__enc_insert(t, name, value)?, end));
}

function __insert_literal(t: Table, buf: [u8], at: i32): Result[(Table, i32), Option[QpackError]] {
  let (name, next) = __enc_str(buf, at, 5, t.__room(""))?;
  let (value, end) = __enc_str(buf, next, 7, t.__room(name))?;
  return Ok((__enc_insert(t, name, value)?, end));
}

function (t: Table) __room(name: string): i64 {
  return t.capacity - __entry_size(name, "");
}

function __enc_int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), Option[QpackError]] {
  match (__int(buf, at, prefix)) {
    Ok(r) => {
      return Ok(r);
    },
    Err(code) => {
      return Err(__enc_error(code));
    }
  }
}

function __enc_str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), Option[QpackError]] {
  match (__str(buf, at, prefix, room)) {
    Ok(r) => {
      return Ok(r);
    },
    Err(code) => {
      return Err(__enc_error(code));
    }
  }
}

function __enc_error(code: i32): Option[QpackError] {
  if (code == SHORT) {
    return None;
  }
  if (code == TOO_LONG) {
    return Some(EncoderStreamError(ENTRY_TOO_BIG));
  }
  return Some(EncoderStreamError(__why(code)));
}

function __enc_static(i: i64): Result[Field, Option[QpackError]] {
  if (i < 0 || i >= STATIC.len() as i64) {
    return Err(Some(EncoderStreamError("no static table entry " + i.to_string())));
  }
  return Ok(field(STATIC[i as i32].name, ""));
}

function __enc_entry(t: Table, a: i64): Result[Field, Option[QpackError]] {
  match (t.__entry(a)) {
    Ok(f) => {
      return Ok(f);
    },
    Err(_) => {
      return Err(Some(EncoderStreamError("a reference to a dynamic table entry that is not there")));
    }
  }
}

function __enc_insert(t: Table, name: string, value: string): Result[Table, Option[QpackError]] {
  match (t.__insert(name, value)) {
    Ok(next) => {
      return Ok(next);
    },
    Err(e) => {
      return Err(Some(e));
    }
  }
}
```

### `16 pub function (d: Decoder) decode(stream: i64, section: [u8]): Decoder`

```fern
<<16 pub function (d: Decoder) decode(stream: i64, section: [u8]): Decoder>>=
pub function (d: Decoder) decode(stream: i64, section: [u8]): Decoder {
  let cur: Decoder = Decoder { ...d, fields: [], waiting: false, failed: None };
  match (cur.__prefix(section)) {
    Ok(p) => {
      return cur.__decoded(stream, section, p.0, p.1, p.2);
    },
    Err(e) => {
      return Decoder { ...cur, failed: Some(e) };
    }
  }
}
```

### `17 function (d: Decoder) __decoded(stream: i64, section: [u8], required: i64, base: i64, at: i32): Decoder`

```fern
<<17 function (d: Decoder) __decoded(stream: i64, section: [u8], required: i64, base: i64, at: i32): Decoder>>=
function (d: Decoder) __decoded(stream: i64, section: [u8], required: i64, base: i64, at: i32): Decoder {
  if (required > d.table.__inserted()) {
    return d.__blocked_on(stream);
  }
  match (d.__lines(section, at, required, base)) {
    Ok(fields) => {
      let cur: Decoder = Decoder { ...d, blocked: __without(d.blocked, stream), fields: fields };
      if (required > 0) {
        cur = Decoder { ...cur, out: __put_int(cur.out, 128, 7, stream), reported: __max(cur.reported, required) };
      }
      return cur;
    },
    Err(e) => {
      return Decoder { ...d, failed: Some(e) };
    }
  }
}
```

### `18 pub function (d: Decoder) cancel(stream: i64): Decoder`

```fern
<<18 pub function (d: Decoder) cancel(stream: i64): Decoder>>=
pub function (d: Decoder) cancel(stream: i64): Decoder {
  if (d.table.max_capacity == 0) {
    return d;
  }
  return Decoder { ...d, blocked: __without(d.blocked, stream), out: __put_int(d.out, 64, 6, stream) };
}
```

### `19 function (d: Decoder) __blocked_on(stream: i64): Decoder`

```fern
<<19 function (d: Decoder) __blocked_on(stream: i64): Decoder>>=
function (d: Decoder) __blocked_on(stream: i64): Decoder {
  if (d.blocked.contains(stream)) {
    return Decoder { ...d, waiting: true };
  }
  if (d.blocked.len() >= d.max_blocked) {
    return Decoder { ...d, failed: Some(DecompressionFailed("more streams blocked than allowed")) };
  }
  return Decoder { ...d, blocked: d.blocked.append(stream), waiting: true };
}

function (d: Decoder) __prefix(section: [u8]): Result[(i64, i64, i32), QpackError] {
  let (encoded, next) = __sec_int(section, 0, 8)?;
  let required: i64 = d.__required(encoded)?;
  if (next >= section.len()) {
    return Err(DecompressionFailed("truncated"));
  }
  let negative: boolean = section[next] as i32 >= 128;
  let (delta, at) = __sec_int(section, next, 7)?;
  if (!negative) {
    return Ok((required, required + delta, at));
  }
  if (delta >= required) {
    return Err(DecompressionFailed("a Base below zero"));
  }
  return Ok((required, required - delta - 1, at));
}

function (d: Decoder) __required(encoded: i64): Result[i64, QpackError] {
  if (encoded == 0) {
    return Ok(0 as i64);
  }
  let max_entries: i64 = d.table.max_capacity / 32;
  let full: i64 = 2 * max_entries;
  if (encoded > full) {
    return Err(DecompressionFailed("a Required Insert Count out of range"));
  }
  let max_value: i64 = d.table.__inserted() + max_entries;
  let required: i64 = max_value / full * full + encoded - 1;
  if (required > max_value) {
    if (required <= full) {
      return Err(DecompressionFailed("a Required Insert Count out of range"));
    }
    required = required - full;
  }
  if (required == 0) {
    return Err(DecompressionFailed("a Required Insert Count out of range"));
  }
  return Ok(required);
}

function (d: Decoder) __lines(section: [u8], from: i32, required: i64, base: i64): Result[Field[], QpackError] {
  let fields: Field[] = [];
  let total: i64 = 0;
  let at: i32 = from;
  while (at < section.len()) {
    let (f, next) = d.__line(section, at, required, base, d.max_list - total)?;
    total = total + __entry_size(f.name, f.value);
    if (total > d.max_list) {
      return Err(DecompressionFailed(__why(TOO_LONG)));
    }
    fields = fields.append(f);
    at = next;
  }
  return Ok(fields);
}

function (d: Decoder) __line(section: [u8], at: i32, required: i64, base: i64, room: i64): Result[(Field, i32), QpackError] {
  let b: i32 = section[at] as i32;
  if (b >= 128 || b >= 16 && b < 32) {
    return d.__line_indexed(section, at, required, base);
  }
  if (b >= 32 && b < 64) {
    let (name, next) = __sec_str(section, at, 3, room)?;
    return __valued(name, (b & 16) != 0, section, next, room);
  }
  let (name, next) = d.__line_name(section, at, required, base)?;
  return __valued(name, (b & 32) != 0 || b < 16 && (b & 8) != 0, section, next, room);
}

function (d: Decoder) __line_indexed(section: [u8], at: i32, required: i64, base: i64): Result[(Field, i32), QpackError] {
  let b: i32 = section[at] as i32;
  if (b >= 128) {
    let (i, next) = __sec_int(section, at, 6)?;
    return Ok((d.__indexed((b & 64) != 0, i, required, base)?, next));
  }
  let (i, next) = __sec_int(section, at, 4)?;
  return Ok((d.__dynamic(base + i, required)?, next));
}

function (d: Decoder) __line_name(section: [u8], at: i32, required: i64, base: i64): Result[(string, i32), QpackError] {
  let b: i32 = section[at] as i32;
  if (b >= 64) {
    let (i, next) = __sec_int(section, at, 4)?;
    return Ok((d.__indexed((b & 16) != 0, i, required, base)?.name, next));
  }
  let (i, next) = __sec_int(section, at, 3)?;
  return Ok((d.__dynamic(base + i, required)?.name, next));
}

function (d: Decoder) __indexed(is_static: boolean, i: i64, required: i64, base: i64): Result[Field, QpackError] {
  if (is_static) {
    return __static(i);
  }
  return d.__dynamic(base - 1 - i, required);
}

function (d: Decoder) __dynamic(a: i64, required: i64): Result[Field, QpackError] {
  if (a < 0 || a >= required) {
    return Err(DecompressionFailed("a reference past the Required Insert Count"));
  }
  return d.table.__entry(a);
}

function __valued(name: string, never: boolean, section: [u8], at: i32, room: i64): Result[(Field, i32), QpackError] {
  let (value, next) = __sec_str(section, at, 7, room - name.len() as i64)?;
  return Ok((Field { name: name, value: value, sensitive: never }, next));
}

function __sec_int(buf: [u8], at: i32, prefix: i32): Result[(i64, i32), QpackError] {
  match (__int(buf, at, prefix)) {
    Ok(r) => {
      return Ok(r);
    },
    Err(code) => {
      return Err(DecompressionFailed(__why(code)));
    }
  }
}
```

### `20 function __sec_str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), QpackError]`

```fern
<<20 function __sec_str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), QpackError]>>=
function __sec_str(buf: [u8], at: i32, prefix: i32, room: i64): Result[(string, i32), QpackError] {
  match (__str(buf, at, prefix, room)) {
    Ok(r) => {
      return Ok(r);
    },
    Err(code) => {
      return Err(DecompressionFailed(__why(code)));
    }
  }
}
```

### `21 pub struct Encoder`

```fern
<<21 pub struct Encoder>>=
pub struct Encoder { huffman: boolean, pending: u8[] }
```

### `22 pub function encoder(): Encoder`

```fern
<<22 pub function encoder(): Encoder>>=
pub function encoder(): Encoder {
  return Encoder { huffman: true, pending: [] };
}

pub function (e: Encoder) without_huffman(): Encoder {
  return Encoder { ...e, huffman: false };
}

pub function (e: Encoder) encode(fields: Field[]): u8[] {
  let room: i32 = 0;
  for f in fields {
    room = room + f.name.len() + f.value.len() + 4;
  }
  let out: u8[] = e.section(room);
  for f in fields {
    out = e.__put_field(out, f);
  }
  return out;
}

// section starts a field section that references no dynamic entry, with
// room for about n bytes of field lines after its prefix.
pub function (e: Encoder) section(n: i32): u8[] {
  let out: u8[] = __alloc_u8(n + 2);
  __arr_set_len(out, 0);
  return out.append(0 as u8).append(0 as u8);
}

// put appends the field line for name and value.
pub function (e: Encoder) put(own out: u8[], name: string, value: string): u8[] {
  return e.__put_line(out, name, value, false);
}

// put_status appends the :status line for code, the static table's own
// entry when the table has one.
pub function (e: Encoder) put_status(own out: u8[], code: i32): u8[] {
  for i in 0..STATUS_CODES.len() {
    if (STATUS_CODES[i] == code) {
      return __put_int(out, 192, 6, STATUS_AT[i] as i64);
    }
  }
  return e.__put_line(out, ":status", int.int_to_string(code), false);
}

// put_length appends the content-length line for n >= 0, spelling n's
// digits straight into out.
pub function (e: Encoder) put_length(own out: u8[], n: i64): u8[] {
  if (n == 0) {
    return __put_int(out, 192, 6, CONTENT_LENGTH as i64);
  }
  let o: u8[] = __put_int(out, 80, 4, CONTENT_LENGTH as i64);
  let digits: i32 = 0;
  let rest: i64 = n;
  while (rest > 0) {
    digits = digits + 1;
    rest = rest / 10;
  }
  if (e.huffman) {
    let coded: i32 = huffman.encoded_len_digits(n);
    if (coded < digits) {
      return huffman.encode_digits(__put_int(o, 128, 7, coded as i64), n);
    }
  }
  o = __put_int(o, 0, 7, digits as i64);
  let p: i64 = 1;
  for _ in 1..digits {
    p = p * 10;
  }
  while (p > 0) {
    o = o.append((48 as i64 + n / p % 10) as u8);
    p = p / 10;
  }
  return o;
}

const STATUS_CODES: i32[] = [200, 204, 304, 404, 103, 503, 100, 206, 302, 400, 403, 421, 425, 500];

const STATUS_AT: i32[] = [25, 64, 26, 27, 24, 28, 63, 65, 66, 67, 68, 69, 70, 71];

const CONTENT_LENGTH: i32 = 4;
```

### `23 function (e: Encoder) __put_field(own out: u8[], f: Field): u8[]`

```fern
<<23 function (e: Encoder) __put_field(own out: u8[], f: Field): u8[]>>=
function (e: Encoder) __put_field(own out: u8[], f: Field): u8[] {
  return e.__put_line(out, f.name, f.value, f.sensitive);
}

function (e: Encoder) __put_line(own out: u8[], name: string, value: string, sensitive: boolean): u8[] {
  let (exact, named) = __find_static(name, value);
  if (exact >= 0 && !sensitive) {
    return __put_int(out, 192, 6, exact as i64);
  }
  let never: i32 = 0;
  if (sensitive) {
    never = 32;
  }
  if (named >= 0) {
    return e.__put_str(__put_int(out, 80 | never, 4, named as i64), 0, 7, value);
  }
  let o: u8[] = e.__put_str(out, 32 | never / 2, 3, name);
  return e.__put_str(o, 0, 7, value);
}
```

### `24 pub function (e: Encoder) on_decoder_stream(bytes: [u8]): Result[Encoder, QpackError]`

```fern
<<24 pub function (e: Encoder) on_decoder_stream(bytes: [u8]): Result[Encoder, QpackError]>>=
pub function (e: Encoder) on_decoder_stream(bytes: [u8]): Result[Encoder, QpackError] {
  let buf: u8[] = e.pending.concat(__copy(bytes));
  let at: i32 = 0;
  while (at < buf.len()) {
    let b: i32 = buf[at] as i32;
    if (b >= 128) {
      return Err(DecoderStreamError("a Section Acknowledgment for a section with no dynamic references"));
    }
    if (b < 64) {
      return Err(DecoderStreamError("an Insert Count Increment past the entries inserted"));
    }
    match (__int(buf, at, 6)) {
      Ok(r) => {
        at = r.1;
      },
      Err(code) => {
        if (code != SHORT) {
          return Err(DecoderStreamError(__why(code)));
        }
        return Ok(Encoder { ...e, pending: __bytes_range(buf, at, buf.len()) });
      }
    }
  }
  return Ok(Encoder { ...e, pending: [] });
}
```

### `25 function __find_static(name: string, value: string): (i32, i32)`

```fern
<<25 function __find_static(name: string, value: string): (i32, i32)>>=
function __find_static(name: string, value: string): (i32, i32) {
  let n: i32 = name.len();
  if (n + 1 >= STATIC_LEN_AT.len()) {
    return (0 - 1, 0 - 1);
  }
  let named: i32 = 0 - 1;
  let g: i32 = STATIC_LEN_AT[n];
  let end: i32 = STATIC_LEN_AT[n + 1];
  while (g < end) {
    let i: i32 = STATIC_GROUP_FROM[g];
    if (STATIC[i].name == name) {
      if (named < 0) {
        named = i;
      }
      let to: i32 = STATIC_GROUP_TO[g];
      while (i < to) {
        if (STATIC[i].value == value) {
          return (i, i);
        }
        i = i + 1;
      }
    }
    g = g + 1;
  }
  return (0 - 1, named);
}

function __put_int(out: u8[], pattern: i32, prefix: i32, value: i64): u8[] {
  let limit: i64 = ((1 << prefix) - 1) as i64;
  if (value < limit) {
    return out.append((pattern as i64 | value) as u8);
  }
  let o: u8[] = out.append((pattern as i64 | limit) as u8);
  let rest: i64 = value - limit;
  while (rest >= 128) {
    o = o.append((rest & 127 | 128) as u8);
    rest = rest >> 7;
  }
  return o.append(rest as u8);
}

function (e: Encoder) __put_str(out: u8[], pattern: i32, prefix: i32, s: string): u8[] {
  let bytes: [u8] = s.as_bytes();
  if (e.huffman) {
    let coded: i32 = huffman.encoded_len(bytes);
    if (coded < bytes.len()) {
      return huffman.encode(__put_int(out, pattern | 1 << prefix, prefix, coded as i64), bytes);
    }
  }
  let o: u8[] = __put_int(out, pattern, prefix, bytes.len() as i64);
  for b in bytes {
    o = o.append(b);
  }
  return o;
}

function __without(xs: i64[], x: i64): i64[] {
  let out: i64[] = [];
  for y in xs {
    if (y != x) {
      out = out.append(y);
    }
  }
  return out;
}

function __from(xs: string[], k: i32): string[] {
  let out: string[] = [];
  let i: i32 = k;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

function __max(a: i64, b: i64): i64 {
  if (a > b) {
    return a;
  }
  return b;
}

function __copy(b: [u8]): u8[] {
  return __bytes_owned(b);
}
```

### `26 const STATIC_LEN_AT: i32[]`

```fern
<<26 const STATIC_LEN_AT: i32[]>>=
const STATIC_LEN_AT: i32[] = [0, 0, 0, 0, 1, 5, 7, 11, 18, 20, 22, 26, 26, 27, 32, 33, 37, 39, 40, 40, 42, 42, 42, 43, 44, 44, 46, 46, 47, 50, 52, 53, 53, 54];

const STATIC_GROUP_FROM: i32[] = [2, 6, 7, 11, 59, 1, 55, 5, 29, 90, 92, 13, 15, 22, 24, 63, 83, 91, 12, 89, 87, 88, 0, 14, 86, 95, 44, 9, 10, 32, 36, 84, 4, 31, 72, 96, 97, 42, 62, 8, 3, 93, 61, 85, 56, 94, 35, 33, 75, 76, 79, 81, 80, 73];

const STATIC_GROUP_TO: i32[] = [3, 7, 8, 12, 61, 2, 56, 6, 31, 91, 93, 14, 22, 24, 29, 72, 84, 92, 13, 90, 88, 89, 1, 15, 87, 96, 55, 10, 11, 33, 42, 85, 5, 32, 73, 97, 99, 44, 63, 9, 4, 94, 62, 86, 59, 95, 36, 35, 76, 79, 80, 83, 81, 75];

const STATIC: Field[] = [
  Field { name: ":authority", value: "", sensitive: false },
  Field { name: ":path", value: "/", sensitive: false },
  Field { name: "age", value: "0", sensitive: false },
  Field { name: "content-disposition", value: "", sensitive: false },
  Field { name: "content-length", value: "0", sensitive: false },
  Field { name: "cookie", value: "", sensitive: false },
  Field { name: "date", value: "", sensitive: false },
  Field { name: "etag", value: "", sensitive: false },
  Field { name: "if-modified-since", value: "", sensitive: false },
  Field { name: "if-none-match", value: "", sensitive: false },
  Field { name: "last-modified", value: "", sensitive: false },
  Field { name: "link", value: "", sensitive: false },
  Field { name: "location", value: "", sensitive: false },
  Field { name: "referer", value: "", sensitive: false },
  Field { name: "set-cookie", value: "", sensitive: false },
  Field { name: ":method", value: "CONNECT", sensitive: false },
  Field { name: ":method", value: "DELETE", sensitive: false },
  Field { name: ":method", value: "GET", sensitive: false },
  Field { name: ":method", value: "HEAD", sensitive: false },
  Field { name: ":method", value: "OPTIONS", sensitive: false },
  Field { name: ":method", value: "POST", sensitive: false },
  Field { name: ":method", value: "PUT", sensitive: false },
  Field { name: ":scheme", value: "http", sensitive: false },
  Field { name: ":scheme", value: "https", sensitive: false },
  Field { name: ":status", value: "103", sensitive: false },
  Field { name: ":status", value: "200", sensitive: false },
  Field { name: ":status", value: "304", sensitive: false },
  Field { name: ":status", value: "404", sensitive: false },
  Field { name: ":status", value: "503", sensitive: false },
  Field { name: "accept", value: "*/*", sensitive: false },
  Field { name: "accept", value: "application/dns-message", sensitive: false },
  Field { name: "accept-encoding", value: "gzip, deflate, br", sensitive: false },
  Field { name: "accept-ranges", value: "bytes", sensitive: false },
  Field { name: "access-control-allow-headers", value: "cache-control", sensitive: false },
  Field { name: "access-control-allow-headers", value: "content-type", sensitive: false },
  Field { name: "access-control-allow-origin", value: "*", sensitive: false },
  Field { name: "cache-control", value: "max-age=0", sensitive: false },
  Field { name: "cache-control", value: "max-age=2592000", sensitive: false },
  Field { name: "cache-control", value: "max-age=604800", sensitive: false },
  Field { name: "cache-control", value: "no-cache", sensitive: false },
  Field { name: "cache-control", value: "no-store", sensitive: false },
  Field { name: "cache-control", value: "public, max-age=31536000", sensitive: false },
  Field { name: "content-encoding", value: "br", sensitive: false },
  Field { name: "content-encoding", value: "gzip", sensitive: false },
  Field { name: "content-type", value: "application/dns-message", sensitive: false },
  Field { name: "content-type", value: "application/javascript", sensitive: false },
  Field { name: "content-type", value: "application/json", sensitive: false },
  Field { name: "content-type", value: "application/x-www-form-urlencoded", sensitive: false },
  Field { name: "content-type", value: "image/gif", sensitive: false },
  Field { name: "content-type", value: "image/jpeg", sensitive: false },
  Field { name: "content-type", value: "image/png", sensitive: false },
  Field { name: "content-type", value: "text/css", sensitive: false },
  Field { name: "content-type", value: "text/html; charset=utf-8", sensitive: false },
  Field { name: "content-type", value: "text/plain", sensitive: false },
  Field { name: "content-type", value: "text/plain;charset=utf-8", sensitive: false },
  Field { name: "range", value: "bytes=0-", sensitive: false },
  Field { name: "strict-transport-security", value: "max-age=31536000", sensitive: false },
  Field { name: "strict-transport-security", value: "max-age=31536000; includesubdomains", sensitive: false },
  Field { name: "strict-transport-security", value: "max-age=31536000; includesubdomains; preload", sensitive: false },
  Field { name: "vary", value: "accept-encoding", sensitive: false },
  Field { name: "vary", value: "origin", sensitive: false },
  Field { name: "x-content-type-options", value: "nosniff", sensitive: false },
  Field { name: "x-xss-protection", value: "1; mode=block", sensitive: false },
  Field { name: ":status", value: "100", sensitive: false },
  Field { name: ":status", value: "204", sensitive: false },
  Field { name: ":status", value: "206", sensitive: false },
  Field { name: ":status", value: "302", sensitive: false },
  Field { name: ":status", value: "400", sensitive: false },
  Field { name: ":status", value: "403", sensitive: false },
  Field { name: ":status", value: "421", sensitive: false },
  Field { name: ":status", value: "425", sensitive: false },
  Field { name: ":status", value: "500", sensitive: false },
  Field { name: "accept-language", value: "", sensitive: false },
  Field { name: "access-control-allow-credentials", value: "FALSE", sensitive: false },
  Field { name: "access-control-allow-credentials", value: "TRUE", sensitive: false },
  Field { name: "access-control-allow-headers", value: "*", sensitive: false },
  Field { name: "access-control-allow-methods", value: "get", sensitive: false },
  Field { name: "access-control-allow-methods", value: "get, post, options", sensitive: false },
  Field { name: "access-control-allow-methods", value: "options", sensitive: false },
  Field { name: "access-control-expose-headers", value: "content-length", sensitive: false },
  Field { name: "access-control-request-headers", value: "content-type", sensitive: false },
  Field { name: "access-control-request-method", value: "get", sensitive: false },
  Field { name: "access-control-request-method", value: "post", sensitive: false },
  Field { name: "alt-svc", value: "clear", sensitive: false },
  Field { name: "authorization", value: "", sensitive: false },
  Field { name: "content-security-policy", value: "script-src 'none'; object-src 'none'; base-uri 'none'", sensitive: false },
  Field { name: "early-data", value: "1", sensitive: false },
  Field { name: "expect-ct", value: "", sensitive: false },
  Field { name: "forwarded", value: "", sensitive: false },
  Field { name: "if-range", value: "", sensitive: false },
  Field { name: "origin", value: "", sensitive: false },
  Field { name: "purpose", value: "prefetch", sensitive: false },
  Field { name: "server", value: "", sensitive: false },
  Field { name: "timing-allow-origin", value: "*", sensitive: false },
  Field { name: "upgrade-insecure-requests", value: "1", sensitive: false },
  Field { name: "user-agent", value: "", sensitive: false },
  Field { name: "x-forwarded-for", value: "", sensitive: false },
  Field { name: "x-frame-options", value: "deny", sensitive: false },
  Field { name: "x-frame-options", value: "sameorigin", sensitive: false },
];
```
