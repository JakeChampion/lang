# TLS 1.3 record protection

This module encrypts and decrypts TLS 1.3 records.

## Model

Record state applies authenticated encryption to TLS content. The frame parser separates complete records from a partial remainder.

## Contracts

Read and write sequence numbers belong to their traffic keys. Authentication must succeed before plaintext is accepted. Partial input is distinct from a malformed record.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum ContentType>>

<<4 pub function (t: ContentType) code(): i32>>

<<5 pub enum RecordError>>

<<6 pub function (e: RecordError) to_string(): string>>

<<7 impl error.Error for RecordError>>

<<8 pub function (e: RecordError) message(): string>>

<<9 const MAX_FRAGMENT: i32>>

<<10 function __content_type(b: i32): Result[ContentType, RecordError]>>

<<11 pub struct Record>>

<<12 pub function deframe(buf: [u8]): Result[Deframed, RecordError]>>

<<13 pub struct Protection>>

<<14 pub function protection(suite: keyschedule.CipherSuite, traffic_secret: [u8]): Protection>>

<<15 function __gcm(suite: keyschedule.CipherSuite, key: [u8]): Option[aes_gcm.Key]>>

<<16 pub function (p: Protection) kernel_info(): u8[]>>

<<17 function __seal(p: Protection, own out: u8[], at: i32, n: i32): Result[u8[], RecordError]>>

<<18 pub struct Sealed>>

<<19 pub function (p: Protection) seal(t: ContentType, content: [u8], padding: i32): Result[Sealed, RecordError]>>

<<20 pub struct Opened>>

<<21 pub function (p: Protection) open(own r: Record): Result[Opened, RecordError]>>

<<22 function __content_end(inner: [u8]): Result[i32, RecordError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encrypts and decrypts TLS 1.3 records.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/crypto/aead";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/tls/keyschedule";
import "std/error";
import "core/cmp";
```

### `3 pub enum ContentType`

```fern
<<3 pub enum ContentType>>=
pub enum ContentType {
  ChangeCipherSpec,
  Alert,
  Handshake,
  ApplicationData,
}
```

### `4 pub function (t: ContentType) code(): i32`

```fern
<<4 pub function (t: ContentType) code(): i32>>=
pub function (t: ContentType) code(): i32 {
  match (t) {
    ChangeCipherSpec => {
      return 20;
    },
    Alert => {
      return 21;
    },
    Handshake => {
      return 22;
    },
    ApplicationData => {
      return 23;
    }
  }
  return 0;
}
```

### `5 pub enum RecordError`

```fern
<<5 pub enum RecordError>>=
pub enum RecordError {
  RecordOverflow(i32),
  UnexpectedContentType(i32),
  EmptyFragment,
  NoContentType,
  BadRecordMac,
  SequenceExhausted,
  BadKeys(i32, i32),
}
```

### `6 pub function (e: RecordError) to_string(): string`

```fern
<<6 pub function (e: RecordError) to_string(): string>>=
pub function (e: RecordError) to_string(): string {
  return e.message();
}
```

### `7 impl error.Error for RecordError`

```fern
<<7 impl error.Error for RecordError>>=
impl error.Error for RecordError {}

impl cmp.Display for RecordError {}
```

### `8 pub function (e: RecordError) message(): string`

```fern
<<8 pub function (e: RecordError) message(): string>>=
pub function (e: RecordError) message(): string {
  match (e) {
    RecordOverflow(n) => {
      return "tls record: " + int.int_to_string(n) + " bytes is over the record size limit";
    },
    UnexpectedContentType(t) => {
      return "tls record: unexpected content type " + int.int_to_string(t);
    },
    EmptyFragment => {
      return "tls record: a handshake or alert record with no content";
    },
    NoContentType => {
      return "tls record: the decrypted record has no content type";
    },
    BadRecordMac => {
      return "tls record: the record does not verify";
    },
    SequenceExhausted => {
      return "tls record: the sequence number is exhausted";
    },
    BadKeys(key, iv) => {
      return "tls record: a " + int.int_to_string(key) + "-byte key and a " + int.int_to_string(iv) + "-byte iv do not fit the cipher suite";
    }
  }
  return "";
}
```

### `9 const MAX_FRAGMENT: i32`

```fern
<<9 const MAX_FRAGMENT: i32>>=
const MAX_FRAGMENT: i32 = 16384;

const MAX_PROTECTED: i32 = 16640;

const TAG_LEN: i32 = 16;
```

### `10 function __content_type(b: i32): Result[ContentType, RecordError]`

```fern
<<10 function __content_type(b: i32): Result[ContentType, RecordError]>>=
function __content_type(b: i32): Result[ContentType, RecordError] {
  if (b == 20) {
    return Ok(ChangeCipherSpec);
  }
  if (b == 21) {
    return Ok(Alert);
  }
  if (b == 22) {
    return Ok(Handshake);
  }
  if (b == 23) {
    return Ok(ApplicationData);
  }
  return Err(UnexpectedContentType(b));
}

function __needs_content(t: ContentType): boolean {
  return t.code() == 21 || t.code() == 22;
}

function __copy(src: [u8], from: i32, len: i32): u8[] {
  return __bytes_range(src, from, from + len);
}

// A protected fragment is copied with room behind it for the five header bytes open authenticates.
function __fragment(src: [u8], from: i32, len: i32, code: i32): u8[] {
  if (code != 23) {
    return __copy(src, from, len);
  }
  let out: u8[] = __bytes_into(__alloc_u8(len + 5), 0, src, from, len);
  __arr_set_len(out, len);
  return out;
}

function __put(own dst: u8[], at: i32, src: [u8]): u8[] {
  return __bytes_into(dst, at, src, 0, src.len());
}

function __header(own dst: u8[], at: i32, t: i32, version: i32, len: i32): u8[] {
  return dst.with(at, t as u8).with(at + 1, (version >> 8 & 255) as u8).with(at + 2, (version & 255) as u8).with(at + 3, (len >> 8 & 255) as u8).with(at + 4, (len & 255) as u8);
}
```

### `11 pub struct Record`

```fern
<<11 pub struct Record>>=
pub struct Record { content_type: ContentType, version: i32, fragment: u8[] }

pub struct Deframed { records: Record[], rest: u8[] }
```

### `12 pub function deframe(buf: [u8]): Result[Deframed, RecordError]`

```fern
<<12 pub function deframe(buf: [u8]): Result[Deframed, RecordError]>>=
pub function deframe(buf: [u8]): Result[Deframed, RecordError] {
  let whole: i32 = framed_end(buf)?;
  let records: Record[] = [];
  let at: i32 = 0;
  while (at < whole) {
    records = records.append(record_at(buf, at)?);
    at = record_end(buf, at);
  }
  if (at == buf.len()) {
    return Ok(Deframed { records: records, rest: [] });
  }
  return Ok(Deframed { records: records, rest: __copy(buf, at, buf.len() - at) });
}

// The end of the last whole record in buf, every header up to it checked.
pub function framed_end(buf: [u8]): Result[i32, RecordError] {
  let at: i32 = 0;
  while (at + 5 <= buf.len()) {
    let code: i32 = buf[at] as i32;
    let t: ContentType = __content_type(code)?;
    let len: i32 = buf[at + 3] as i32 << 8 | buf[at + 4] as i32;
    let limit: i32 = MAX_FRAGMENT;
    if (code == 23) {
      limit = MAX_PROTECTED;
    }
    if (len > limit) {
      return Err(RecordOverflow(len));
    }
    if (len == 0 && __needs_content(t)) {
      return Err(EmptyFragment);
    }
    if (at + 5 + len > buf.len()) {
      break;
    }
    at = at + 5 + len;
  }
  return Ok(at);
}

pub function record_end(buf: [u8], at: i32): i32 {
  return at + 5 + (buf[at + 3] as i32 << 8 | buf[at + 4] as i32);
}

// The whole record at buf[at], its fragment copied out; a protected one with room for open's header.
pub function record_at(buf: [u8], at: i32): Result[Record, RecordError] {
  let code: i32 = buf[at] as i32;
  return Ok(Record { content_type: __content_type(code)?, version: buf[at + 1] as i32 << 8 | buf[at + 2] as i32, fragment: __fragment(buf, at + 5, record_end(buf, at) - at - 5, code) });
}

// r with a fragment of its own, for a record still held where another holds its fragment.
pub function (r: Record) owned(): Record {
  return Record { content_type: r.content_type, version: r.version, fragment: __fragment(r.fragment, 0, r.fragment.len(), r.content_type.code()) };
}

pub function frame(t: ContentType, legacy_version: i32, data: [u8]): u8[] {
  let n: i32 = (data.len() + MAX_FRAGMENT - 1) / MAX_FRAGMENT;
  let out: u8[] = __alloc_u8(data.len() + 5 * n);
  let at: i32 = 0;
  let from: i32 = 0;
  while (from < data.len()) {
    let len: i32 = data.len() - from;
    if (len > MAX_FRAGMENT) {
      len = MAX_FRAGMENT;
    }
    out = __header(out, at, t.code(), legacy_version, len);
    out = __put(out, at + 5, data[from:from + len]);
    at = at + 5 + len;
    from = from + len;
  }
  return out;
}
```

### `13 pub struct Protection`

```fern
<<13 pub struct Protection>>=
pub struct Protection { suite: keyschedule.CipherSuite, key: u8[], iv: u8[], seq: u64, gcm: Option[aes_gcm.Key] }
```

### `14 pub function protection(suite: keyschedule.CipherSuite, traffic_secret: [u8]): Protection`

```fern
<<14 pub function protection(suite: keyschedule.CipherSuite, traffic_secret: [u8]): Protection>>=
pub function protection(suite: keyschedule.CipherSuite, traffic_secret: [u8]): Protection {
  let k: keyschedule.TrafficKeys = keyschedule.traffic_keys(suite, traffic_secret);
  return Protection { suite: suite, key: k.key, iv: k.iv, seq: 0 as u64, gcm: __gcm(suite, k.key) };
}
```

### `15 function __gcm(suite: keyschedule.CipherSuite, key: [u8]): Option[aes_gcm.Key]`

```fern
<<15 function __gcm(suite: keyschedule.CipherSuite, key: [u8]): Option[aes_gcm.Key]>>=
function __gcm(suite: keyschedule.CipherSuite, key: [u8]): Option[aes_gcm.Key] {
  match (suite.aead()) {
    keyschedule.ChaCha20Poly1305 => {
      return None;
    },
    _ => {}
  }
  match (aes_gcm.key(key)) {
    Ok(k) => {
      return Some(k);
    },
    Err(_) => {
      return None;
    }
  }
}
```

### `16 pub function (p: Protection) kernel_info(): u8[]`

```fern
<<16 pub function (p: Protection) kernel_info(): u8[]>>=
pub function (p: Protection) kernel_info(): u8[] {
  let cipher: i32 = 54;
  let salt: i32 = 0;
  match (p.suite) {
    keyschedule.Aes128GcmSha256 => {
      cipher = 51;
      salt = 4;
    },
    keyschedule.Aes256GcmSha384 => {
      cipher = 52;
      salt = 4;
    },
    keyschedule.ChaCha20Poly1305Sha256 => {}
  }
  let out: u8[] = __alloc_u8(4 + p.iv.len() + p.key.len() + 8);
  out = out.with(0, 4 as u8).with(1, 3 as u8).with(2, cipher as u8).with(3, 0 as u8);
  out = __put(out, 4, p.iv[salt:p.iv.len()]);
  out = __put(out, 4 + p.iv.len() - salt, p.key);
  out = __put(out, 4 + p.iv.len() - salt + p.key.len(), p.iv[0:salt]);
  let at: i32 = out.len() - 8;
  let i: i32 = 0;
  while (i < 8) {
    out = out.with(at + i, (p.seq >> (8 * (7 - i)) as u64 & 255 as u64) as u8);
    i = i + 1;
  }
  return out;
}
```

### `17 function __seal(p: Protection, own out: u8[], at: i32, n: i32): Result[u8[], RecordError]`

```fern
<<17 function __seal(p: Protection, own out: u8[], at: i32, n: i32): Result[u8[], RecordError]>>=
// Seals out[at:at + n] in place, the record header out[0:5] its additional data and the tag behind it.
function __seal(p: Protection, own out: u8[], at: i32, n: i32): Result[u8[], RecordError] {
  match (p.suite.aead()) {
    keyschedule.Aes128Gcm | keyschedule.Aes256Gcm => {
      let Some(g) = p.gcm else {
        return Err(BadKeys(p.key.len(), p.iv.len()));
      };
      match (g.seal_in_place(p.iv, p.seq, out, 0, 5, at, n)) {
        Ok(sealed) => {
          return Ok(sealed);
        },
        Err(e) => {
          return Err(__seal_failed(p, e));
        }
      }
    },
    keyschedule.ChaCha20Poly1305 => {
      match (chacha20poly1305.seal_in_place(p.key, p.iv, p.seq, out, 0, 5, at, n)) {
        Ok(sealed) => {
          return Ok(sealed);
        },
        Err(e) => {
          return Err(__seal_failed(p, e));
        }
      }
    }
  }
  return Err(BadKeys(p.key.len(), p.iv.len()));
}

function __seal_failed(p: Protection, e: aead.AeadError): RecordError {
  match (e) {
    aead.KeyLength(n) => {
      return BadKeys(n, p.iv.len());
    },
    aead.NonceLength(n) => {
      return BadKeys(p.key.len(), n);
    },
    aead.Forged => {
      return BadRecordMac;
    }
  }
  return BadRecordMac;
}

// Opens buf[0:n] in place, the tag behind it and the header, the additional data, behind the tag.
function __open(p: Protection, own buf: u8[], n: i32): Result[u8[], RecordError] {
  match (p.suite.aead()) {
    keyschedule.Aes128Gcm | keyschedule.Aes256Gcm => {
      let Some(g) = p.gcm else {
        return Err(BadKeys(p.key.len(), p.iv.len()));
      };
      let Ok(inner) = g.open_in_place(p.iv, p.seq, buf, n + TAG_LEN, 5, 0, n) else {
        return Err(BadRecordMac);
      };
      return Ok(inner);
    },
    keyschedule.ChaCha20Poly1305 => {
      let Ok(inner) = chacha20poly1305.open_in_place(p.key, p.iv, p.seq, buf, n + TAG_LEN, 5, 0, n) else {
        return Err(BadRecordMac);
      };
      return Ok(inner);
    }
  }
  return Err(BadRecordMac);
}

function __advanceable(p: Protection): Result[void, RecordError] {
  if (p.key.len() != p.suite.aead().key_len() || p.iv.len() != 12) {
    return Err(BadKeys(p.key.len(), p.iv.len()));
  }
  if (p.seq == 0xffffffffffffffff as u64) {
    return Err(SequenceExhausted);
  }
  return Ok(());
}

function __advanced(p: Protection): Protection {
  return Protection { ...p, seq: p.seq + 1 as u64 };
}
```

### `18 pub struct Sealed`

```fern
<<18 pub struct Sealed>>=
pub struct Sealed { record: u8[], next: Protection }
```

### `19 pub function (p: Protection) seal(t: ContentType, content: [u8], padding: i32): Result[Sealed, RecordError]`

```fern
<<19 pub function (p: Protection) seal(t: ContentType, content: [u8], padding: i32): Result[Sealed, RecordError]>>=
pub function (p: Protection) seal(t: ContentType, content: [u8], padding: i32): Result[Sealed, RecordError] {
  let pad: i32 = padding;
  if (pad < 0) {
    pad = 0;
  }
  if (content.len() + pad > MAX_FRAGMENT) {
    return Err(RecordOverflow(content.len() + pad));
  }
  let checked: ContentType = __inner_type(t.code(), content.len())?;
  __advanceable(p)?;
  let n: i32 = content.len() + 1 + pad;
  let end: i32 = 5 + n + TAG_LEN;
  let out: u8[] = __header(__alloc_u8(end), 0, 23, 0x0303, n + TAG_LEN);
  out = __put(out, 5, content);
  out = out.with(5 + content.len(), t.code() as u8);
  let i: i32 = 5 + content.len() + 1;
  while (i < 5 + n) {
    out = out.with(i, 0 as u8);
    i = i + 1;
  }
  return Ok(Sealed { record: __seal(p, out, 5, n)?, next: __advanced(p) });
}
```

Where a writer has built the content into a buffer with room around it, the record is sealed in that buffer: five bytes ahead of the content for the header, and behind it one for the content type and sixteen for the tag.

```fern
<<19 pub function (p: Protection) seal(t: ContentType, content: [u8], padding: i32): Result[Sealed, RecordError]>>=
pub const FRAMED_HEAD: i32 = 5;

pub const FRAMED_TAIL: i32 = 17;

pub function (p: Protection) seal_framed(t: ContentType, own buf: u8[]): Result[Sealed, RecordError] {
  let n: i32 = buf.len() - FRAMED_HEAD - FRAMED_TAIL;
  if (n < 0) {
    return Err(EmptyFragment);
  }
  if (n > MAX_FRAGMENT) {
    return Err(RecordOverflow(n));
  }
  __inner_type(t.code(), n)?;
  __advanceable(p)?;
  buf = __header(buf, 0, 23, 0x0303, n + 1 + TAG_LEN).with(5 + n, t.code() as u8);
  return Ok(Sealed { record: __seal(p, buf, 5, n + 1)?, next: __advanced(p) });
}
```

### `20 pub struct Opened`

```fern
<<20 pub struct Opened>>=
pub struct Opened { content_type: ContentType, content: u8[], next: Protection }
```

### `21 pub function (p: Protection) open(own r: Record): Result[Opened, RecordError]`

```fern
<<21 pub function (p: Protection) open(own r: Record): Result[Opened, RecordError]>>=
pub function (p: Protection) open(own r: Record): Result[Opened, RecordError] {
  if (r.content_type.code() != 23) {
    return Err(UnexpectedContentType(r.content_type.code()));
  }
  let version: i32 = r.version;
  let fragment: u8[] = r.fragment;
  if (fragment.len() > MAX_PROTECTED) {
    return Err(RecordOverflow(fragment.len()));
  }
  __advanceable(p)?;
  let n: i32 = fragment.len() - TAG_LEN;
  if (n < 0) {
    return Err(BadRecordMac);
  }
  let inner: u8[] = __open(p, __header_behind(fragment, version), n)?;
  let end: i32 = __content_end(inner[0:n])?;
  let t: ContentType = __inner_type(inner[end] as i32, end)?;
  __arr_set_len(inner, end);
  return Ok(Opened { content_type: t, content: inner, next: __advanced(p) });
}

// Appends the record header behind the fragment, into the room deframe left there.
function __header_behind(own fragment: u8[], version: i32): u8[] {
  let n: i32 = fragment.len();
  return fragment.append(23 as u8).append((version >> 8 & 255) as u8).append((version & 255) as u8).append((n >> 8 & 255) as u8).append((n & 255) as u8);
}
```

### `22 function __content_end(inner: [u8]): Result[i32, RecordError]`

```fern
<<22 function __content_end(inner: [u8]): Result[i32, RecordError]>>=
function __content_end(inner: [u8]): Result[i32, RecordError] {
  if (inner.len() > MAX_FRAGMENT + 1) {
    return Err(RecordOverflow(inner.len()));
  }
  let end: i32 = inner.len() - 1;
  while (end >= 0 && inner[end] == 0) {
    end = end - 1;
  }
  if (end < 0) {
    return Err(NoContentType);
  }
  return Ok(end);
}

function __inner_type(code: i32, len: i32): Result[ContentType, RecordError] {
  let t: ContentType = __content_type(code)?;
  if (code == 20) {
    return Err(UnexpectedContentType(20));
  }
  if (len == 0 && __needs_content(t)) {
    return Err(EmptyFragment);
  }
  return Ok(t);
}
```
