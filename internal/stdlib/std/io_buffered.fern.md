# Buffered byte input and output

This module provides buffered readers and writers in memory.

## Model

Memory writers collect bytes in owned buffers. Buffered writers batch output to a writer, while line readers retain partial input across reads.

## Contracts

Reader and writer state must follow the returned value. Error accessors preserve the I/O failure that stopped progress. Byte readers avoid premature text conversion.

## Executable example

```fern test name=std-io_buffered-example
import "std/io_buffered";
import "std/option";

function main(): i32 {
  let w = io_buffered.bytes_writer_new().write_string("hello");
  if (w.len() != 5 || w.into_string().unwrap_or("") != "hello") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function bytes_writer_new(): BytesWriter>>

<<4 pub struct BufWriter>>

<<5 pub function buf_writer_new(w: Writer, cap: i32): BufWriter>>

<<6 pub(package) function buf_u64_len(n: u64): i32>>

<<7 function __buf_writer_put(b: BufWriter, s: string): BufWriter>>

<<8 pub struct ByteLineReader>>

<<9 pub function byte_line_reader_new(r: Reader, term: i32, chunk_size: i32): ByteLineReader>>

<<10 function copy_line_bytes(chunk: u8[], lo: i32, hi: i32): u8[]>>

<<11 pub function (lr: ByteLineReader) next_line_bytes(): (Option[u8[]], ByteLineReader)>>

<<12 pub struct LineReader>>

<<13 pub function line_reader_new(r: Reader, term: i32, chunk_size: i32): LineReader>>

<<14 function invalid_text_reader(raw: ByteLineReader): LineReader>>

<<15 pub function (lr: LineReader) next_line(): (Option[string], LineReader)>>

<<16 function text_scalar_width(lead: i32): i32>>

<<17 pub function (lr: LineReader) next_chunk(): (Option[string], LineReader)>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides buffered readers and writers in memory.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/mem";
import "std/string";
import "std/utf8" as utf8;
```

### `3 pub function bytes_writer_new(): BytesWriter`

```fern
<<3 pub function bytes_writer_new(): BytesWriter>>=
pub function bytes_writer_new(): BytesWriter {
  let empty: u8[] = [];
  return BytesWriter { data: empty };
}

pub function (w: BytesWriter) write_string(s: string): BytesWriter {
  let bs: [u8] = s.as_bytes();
  let data: u8[] = w.data;
  let i: i32 = 0;
  while (i < bs.len()) {
    data = data.append(bs[i]);
    i = i + 1;
  }
  return BytesWriter { ...w, data: data };
}

pub function (w: BytesWriter) write_bytes(bs: u8[]): BytesWriter {
  let data: u8[] = w.data;
  let i: i32 = 0;
  while (i < bs.len()) {
    data = data.append(bs[i]);
    i = i + 1;
  }
  return BytesWriter { ...w, data: data };
}

pub function (w: BytesWriter) write_byte(b: i32): BytesWriter {
  return BytesWriter { ...w, data: w.data.append(b as u8) };
}

pub function (w: BytesWriter) len(): i32 {
  return w.data.len();
}

pub function (w: BytesWriter) is_empty(): boolean {
  return w.data.len() == 0;
}

pub function (w: BytesWriter) into_bytes(): u8[] {
  return w.data;
}

pub function (w: BytesWriter) into_string(): Option[string] {
  return utf8.from_bytes(w.data);
}

pub function (w: BytesWriter) reset(): BytesWriter {
  let empty: u8[] = [];
  return BytesWriter { ...w, data: empty };
}
```

### `4 pub struct BufWriter`

```fern
<<4 pub struct BufWriter>>=
pub struct BufWriter { w: Writer, buf: BufBlock, cap: i32, err: Option[IoError] }

pub(package) struct BufBlock { h: usize }

impl mem.Drop for BufBlock {
  function drop(self: BufBlock): void {
    buf_free(self.h);
  }
}
```

### `5 pub function buf_writer_new(w: Writer, cap: i32): BufWriter`

```fern
<<5 pub function buf_writer_new(w: Writer, cap: i32): BufWriter>>=
pub function buf_writer_new(w: Writer, cap: i32): BufWriter {
  return BufWriter { w: w, buf: BufBlock { h: buf_new(cap) }, cap: cap, err: None };
}

pub function (b: BufWriter) write_string(s: string): BufWriter {
  if (buf_len(b.buf.h) == 0 && s.len() >= b.cap) {
    return __buf_writer_put(b, s);
  }
  buf_push(b.buf.h, s);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_bytes(bytes: u8[]): BufWriter {
  if (buf_len(b.buf.h) == 0 && bytes.len() >= b.cap) {
    return __buf_writer_put_bytes(b, bytes);
  }
  buf_push_bytes_range(b.buf.h, bytes, 0, bytes.len());
  return b.settle();
}

pub function (b: BufWriter) write_bytes_range(bytes: u8[], lo: i32, hi: i32): BufWriter {
  if (lo <= 0 && hi >= bytes.len()) {
    return b.write_bytes(bytes);
  }
  buf_push_bytes_range(b.buf.h, bytes, lo, hi);
  return b.settle();
}

pub function (b: BufWriter) write_bytes_mapped(bytes: u8[], table: u8[]): BufWriter {
  buf_push_bytes_mapped(b.buf.h, bytes, table);
  return b.settle();
}

pub function (b: BufWriter) write_bytes_filtered(bytes: u8[], drop: u8[]): BufWriter {
  buf_push_bytes_filtered(b.buf.h, bytes, drop);
  return b.settle();
}

pub function (b: BufWriter) write_bytes_expanded(bytes: u8[], table: u8[]): BufWriter {
  buf_push_bytes_expanded(b.buf.h, bytes, table);
  return b.settle();
}

pub function (b: BufWriter) write_range(s: string, lo: i32, hi: i32): BufWriter {
  buf_push_range(b.buf.h, s, lo, hi);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_mapped(s: string, table: u8[]): BufWriter {
  buf_push_mapped(b.buf.h, s, table);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_filtered(s: string, drop: u8[]): BufWriter {
  buf_push_filtered(b.buf.h, s, drop);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_expanded(s: string, table: u8[]): BufWriter {
  buf_push_expanded(b.buf.h, s, table);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_byte(byte: i32): BufWriter {
  buf_push_byte(b.buf.h, byte);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_u64(n: u64): BufWriter {
  buf_push_u64(b.buf.h, n);
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) write_i64(n: i64): BufWriter {
  if (n < 0 as i64) {
    return b.write_string("-").write_u64(0 as u64 - n as u64);
  }
  return b.write_u64(n as u64);
}

pub(package) function buf_push_u64(h: usize, n: u64): void {
  if (n < 100 as u64) {
    if (n < 10 as u64) {
      buf_push_byte(h, 48 + n as i32);
      return;
    }
    let k: i32 = n as i32 * 2;
    buf_push_range(h, digit_pairs(), k, k + 2);
    return;
  }
  let q: u64 = n / (100 as u64);
  buf_push_u64(h, q);
  let r: i32 = (n - q * (100 as u64)) as i32 * 2;
  buf_push_range(h, digit_pairs(), r, r + 2);
}

pub(package) function buf_push_i64(h: usize, n: i64): void {
  if (n < 0 as i64) {
    buf_push_byte(h, 45);
    buf_push_u64(h, 0 as u64 - n as u64);
    return;
  }
  buf_push_u64(h, n as u64);
}
```

### `6 pub(package) function buf_u64_len(n: u64): i32`

```fern
<<6 pub(package) function buf_u64_len(n: u64): i32>>=
pub(package) function buf_u64_len(n: u64): i32 {
  let d: i32 = 1;
  let p: u64 = 10 as u64;
  while (n >= p) {
    d = d + 1;
    if (d == 20) {
      return d;
    }
    p = p * (10 as u64);
  }
  return d;
}

pub(package) function digit_pairs(): string {
  return "0001020304050607080910111213141516171819202122232425262728293031323334353637383940414243444546474849505152535455565758596061626364656667686970717273747576777879808182838485868788899091929394959697989900";
}

pub function (b: BufWriter) flush(): BufWriter {
  if (buf_len(b.buf.h) == 0) {
    return b;
  }
  return __buf_writer_put_bytes(b, buf_take_bytes(b.buf.h));
}

pub function (b: BufWriter) close(): Result[void, IoError] {
  let pending: i32 = buf_len(b.buf.h);
  let flushed: BufWriter = b.flush();
  if let Err(e) = b.w.close() {
    return Err(e);
  }
  if (pending > 0) {
    if let Some(e) = flushed.error() {
      return Err(e);
    }
  }
  return Ok(());
}

pub function (b: BufWriter) error(): Option[IoError] {
  return b.err;
}

pub function (b: BufWriter) handle(): usize {
  return b.buf.h;
}

pub function (b: BufWriter) settle(): BufWriter {
  if (buf_len(b.buf.h) < b.cap) {
    return b;
  }
  return b.flush();
}

pub function (b: BufWriter) buffered(): i32 {
  return buf_len(b.buf.h);
}
```

### `7 function __buf_writer_put(b: BufWriter, s: string): BufWriter`

```fern
<<7 function __buf_writer_put(b: BufWriter, s: string): BufWriter>>=
function __buf_writer_put(b: BufWriter, s: string): BufWriter {
  match (b.err) {
    Some(_) => {
      return b;
    },
    None => {}
  }
  if let Err(e) = b.w.write(s) {
    return BufWriter { ...b, err: Some(e) };
  }
  return b;
}

function __buf_writer_put_bytes(b: BufWriter, bytes: u8[]): BufWriter {
  match (b.err) {
    Some(_) => {
      return b;
    },
    None => {}
  }
  if let Err(e) = b.w.write_bytes(bytes) {
    return BufWriter { ...b, err: Some(e) };
  }
  return b;
}
```

### `8 pub struct ByteLineReader`

```fern
<<8 pub struct ByteLineReader>>=
pub struct ByteLineReader { r: Reader, chunk: u8[], pos: i32, eof: boolean, term: i32, chunk_size: i32, err: Option[IoError] }
```

### `9 pub function byte_line_reader_new(r: Reader, term: i32, chunk_size: i32): ByteLineReader`

```fern
<<9 pub function byte_line_reader_new(r: Reader, term: i32, chunk_size: i32): ByteLineReader>>=
pub function byte_line_reader_new(r: Reader, term: i32, chunk_size: i32): ByteLineReader {
  return ByteLineReader { r: r, chunk: [], pos: 0, eof: false, term: term, chunk_size: chunk_size, err: None };
}

pub function (lr: ByteLineReader) error(): Option[IoError] {
  return lr.err;
}
```

### `10 function copy_line_bytes(chunk: u8[], lo: i32, hi: i32): u8[]`

```fern
<<10 function copy_line_bytes(chunk: u8[], lo: i32, hi: i32): u8[]>>=
function copy_line_bytes(chunk: u8[], lo: i32, hi: i32): u8[] {
  let out: u8[] = __alloc_u8(hi - lo);
  let i: i32 = lo;
  while (i < hi) {
    out = out.with(i - lo, chunk[i]);
    i = i + 1;
  }
  return out;
}

function byte_line_end(chunk: u8[], pos: i32, term: i32): i32 {
  let end: i32 = __memchr_bytes(chunk, term, pos);
  if (end < 0) {
    return chunk.len();
  }
  return end;
}
```

### `11 pub function (lr: ByteLineReader) next_line_bytes(): (Option[u8[]], ByteLineReader)`

```fern
<<11 pub function (lr: ByteLineReader) next_line_bytes(): (Option[u8[]], ByteLineReader)>>=
pub function (lr: ByteLineReader) next_line_bytes(): (Option[u8[]], ByteLineReader) {
  let s: ByteLineReader = lr;
  let b: usize = 0 as usize;
  while (true) {
    let end: i32 = byte_line_end(s.chunk, s.pos, s.term);
    if (end < s.chunk.len()) {
      end = end + 1;
      if (b == 0 as usize) {
        return (Some(copy_line_bytes(s.chunk, s.pos, end)), ByteLineReader { ...s, pos: end });
      }
      buf_push_bytes_range(b, s.chunk, s.pos, end);
      let line: u8[] = buf_take_bytes(b);
      buf_free(b);
      return (Some(line), ByteLineReader { ...s, pos: end });
    }
    if (s.pos < s.chunk.len()) {
      if (b == 0 as usize) {
        b = buf_new(s.chunk.len() - s.pos);
      }
      buf_push_bytes_range(b, s.chunk, s.pos, s.chunk.len());
    }
    if (s.eof) {
      s = ByteLineReader { ...s, chunk: [], pos: 0 };
      if (b == 0 as usize) {
        return (None, s);
      }
      let tail: u8[] = buf_take_bytes(b);
      buf_free(b);
      return (Some(tail), s);
    }
    match (s.r.read_chunk_bytes(s.chunk_size)) {
      Ok(chunk) => {
        s = ByteLineReader { ...s, chunk: chunk, pos: 0, eof: chunk.len() == 0 };
      },
      Err(e) => {
        s = ByteLineReader { ...s, chunk: [], pos: 0, eof: true, err: Some(e) };
      }
    }
  }
  return (None, s);
}

pub function (lr: ByteLineReader) skip_line(): (boolean, ByteLineReader) {
  let s: ByteLineReader = lr;
  let partial: boolean = false;
  while (true) {
    let end: i32 = byte_line_end(s.chunk, s.pos, s.term);
    if (end < s.chunk.len()) {
      return (true, ByteLineReader { ...s, pos: end + 1 });
    }
    if (s.pos < s.chunk.len()) {
      partial = true;
    }
    if (s.eof) {
      return (partial, ByteLineReader { ...s, chunk: [], pos: 0 });
    }
    match (s.r.read_chunk_bytes(s.chunk_size)) {
      Ok(chunk) => {
        s = ByteLineReader { ...s, chunk: chunk, pos: 0, eof: chunk.len() == 0 };
      },
      Err(e) => {
        s = ByteLineReader { ...s, chunk: [], pos: 0, eof: true, err: Some(e) };
      }
    }
  }
  return (false, s);
}

pub function (lr: ByteLineReader) next_chunk_bytes(): (Option[u8[]], ByteLineReader) {
  let s: ByteLineReader = lr;
  if (s.pos < s.chunk.len()) {
    let rest: u8[] = copy_line_bytes(s.chunk, s.pos, s.chunk.len());
    return (Some(rest), ByteLineReader { ...s, chunk: [], pos: 0 });
  }
  if (s.eof) {
    return (None, s);
  }
  match (s.r.read_chunk_bytes(s.chunk_size)) {
    Ok(chunk) => {
      if (chunk.len() == 0) {
        return (None, ByteLineReader { ...s, eof: true });
      }
      return (Some(chunk), s);
    },
    Err(e) => {
      return (None, ByteLineReader { ...s, eof: true, err: Some(e) });
    }
  }
}
```

### `12 pub struct LineReader`

```fern
<<12 pub struct LineReader>>=
pub struct LineReader { raw: ByteLineReader }
```

### `13 pub function line_reader_new(r: Reader, term: i32, chunk_size: i32): LineReader`

```fern
<<13 pub function line_reader_new(r: Reader, term: i32, chunk_size: i32): LineReader>>=
pub function line_reader_new(r: Reader, term: i32, chunk_size: i32): LineReader {
  return LineReader { raw: byte_line_reader_new(r, term, chunk_size) };
}

pub function (lr: LineReader) error(): Option[IoError] {
  return lr.raw.error();
}
```

### `14 function invalid_text_reader(raw: ByteLineReader): LineReader`

```fern
<<14 function invalid_text_reader(raw: ByteLineReader): LineReader>>=
function invalid_text_reader(raw: ByteLineReader): LineReader {
  let err: Option[IoError] = raw.err;
  match (err) {
    None => {
      err = Some(InvalidUtf8(""));
    },
    Some(_) => {}
  }
  return LineReader { raw: ByteLineReader { ...raw, chunk: [], pos: 0, eof: true, err: err } };
}
```

### `15 pub function (lr: LineReader) next_line(): (Option[string], LineReader)`

```fern
<<15 pub function (lr: LineReader) next_line(): (Option[string], LineReader)>>=
pub function (lr: LineReader) next_line(): (Option[string], LineReader) {
  let (line, raw) = lr.raw.next_line_bytes();
  match (line) {
    None => {
      return (None, LineReader { raw: raw });
    },
    Some(bytes) => {
      match (utf8.from_bytes(bytes)) {
        Some(text) => {
          return (Some(text), LineReader { raw: raw });
        },
        None => {
          return (None, invalid_text_reader(raw));
        }
      }
    }
  }
  return (None, LineReader { raw: raw });
}
```

### `16 function text_scalar_width(lead: i32): i32`

```fern
<<16 function text_scalar_width(lead: i32): i32>>=
function text_scalar_width(lead: i32): i32 {
  if (lead >= 194 && lead <= 223) {
    return 2;
  }
  if (lead >= 224 && lead <= 239) {
    return 3;
  }
  if (lead >= 240 && lead <= 244) {
    return 4;
  }
  return 0;
}

function text_second_byte_valid(lead: i32, second: i32): boolean {
  match (lead) {
    224 => {
      return second >= 160;
    },
    237 => {
      return second <= 159;
    },
    240 => {
      return second >= 144;
    },
    244 => {
      return second <= 143;
    },
    _ => {
      return true;
    }
  }
  return true;
}

function text_chunk_end(bytes: u8[]): i32 {
  let n: i32 = bytes.len();
  if (n == 0) {
    return 0;
  }
  let start: i32 = n - 1;
  while (start > 0 && n - start <= 3 && bytes[start] >= 128 as u8 && bytes[start] < 192 as u8) {
    start = start - 1;
  }
  let lead: i32 = bytes[start] as i32;
  let width: i32 = text_scalar_width(lead);
  if (width == 0 || n - start >= width) {
    return n;
  }
  if (n - start > 1) {
    let second: i32 = bytes[start + 1] as i32;
    if (!text_second_byte_valid(lead, second)) {
      return n;
    }
  }
  return start;
}
```

### `17 pub function (lr: LineReader) next_chunk(): (Option[string], LineReader)`

```fern
<<17 pub function (lr: LineReader) next_chunk(): (Option[string], LineReader)>>=
pub function (lr: LineReader) next_chunk(): (Option[string], LineReader) {
  let (first, raw) = lr.raw.next_chunk_bytes();
  let bytes: u8[] = [];
  match (first) {
    None => {
      return (None, LineReader { raw: raw });
    },
    Some(chunk) => {
      bytes = chunk;
    }
  }
  while (true) {
    let end: i32 = text_chunk_end(bytes);
    if (end > 0) {
      let prefix: u8[] = bytes;
      if (end < bytes.len()) {
        prefix = copy_line_bytes(bytes, 0, end);
      }
      match (utf8.from_bytes(prefix)) {
        None => {
          return (None, invalid_text_reader(raw));
        },
        Some(text) => {
          if (end < bytes.len()) {
            raw = ByteLineReader { ...raw, chunk: bytes, pos: end };
          }
          return (Some(text), LineReader { raw: raw });
        }
      }
    }
    let (more, next) = raw.next_chunk_bytes();
    raw = next;
    match (more) {
      None => {
        return (None, invalid_text_reader(raw));
      },
      Some(chunk) => {
        let b: usize = buf_new(bytes.len() + chunk.len());
        buf_push_bytes_range(b, bytes, 0, bytes.len());
        buf_push_bytes_range(b, chunk, 0, chunk.len());
        bytes = buf_take_bytes(b);
        buf_free(b);
      }
    }
  }
  return (None, LineReader { raw: raw });
}
```
