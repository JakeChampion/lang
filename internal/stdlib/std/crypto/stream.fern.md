# Chunked authenticated encryption

A stream begins with a random 16-byte prefix. HKDF-SHA256 takes the prefix
as salt, the 32-byte key as input, and `Fern STREAM v1` as info. Each frame
starts with a byte (0 for continuation, 1 for last), then XChaCha20-Poly1305
ciphertext and its 16-byte tag. The nonce consists of the prefix, seven
big-endian counter bytes starting at zero, and the frame's flag. A continuation
holds 65536 plaintext bytes. A final frame holds up to 65536 bytes.

Write the prefix from `writer` once. Pass each chunk to `write_chunk` and use
the returned state for the next chunk. At EOF pass a final chunk, including
an empty one if needed. For reading, give the prefix to `reader`, then pass
one frame at a time to `read_chunk`. Call `finish` at EOF before committing
the output. Both operations authenticate the frame flag with the nonce.

An envelope starts with version 1, a four-byte big-endian wrapped-key length,
and the wrapped key, followed by the stream. Each frame authenticates SHA-256
of this envelope header as associated data. The wrapping function must protect
the confidentiality and integrity of the fresh data key. The unwrapping
function must reject invalid wrapped keys.

```fern test name=crypto-stream-round-trip
import "std/crypto/stream";
import "std/rand";

function main(): i32 {
  let key = rand.bytes(32);
  let Ok(ciphertext) = stream.seal_all(key, [4u8, 2u8]) else { return 1; };
  let Ok(message) = stream.open_all(key, ciphertext) else { return 2; };
  if (message != [4u8, 2u8]) { return 3; }
  return 0;
}
```

```fern
<<*>>=
// Chunked XChaCha20-Poly1305 streams and envelopes for wrapped data keys.

import "core/cmp";
import "core/error";
import "std/crypto";
import "std/crypto/chacha20poly1305";
import "std/binary";
import "std/rand";
import "std/result";
import "std/i32";

pub const CHUNK_BYTES: i32 = 65536;

const FRAME_BYTES: i32 = CHUNK_BYTES + 17;

const LAST_COUNTER: u64 = 72057594037927935u64;

pub enum StreamError { KeyLength, Format, Authentication, Limit, Wrap }

pub function (e: StreamError) to_string(): string {
  return e.message();
}

pub function (e: StreamError) message(): string {
  match (e) {
    KeyLength => {
      return "stream: the key is not 32 bytes";
    },
    Format => {
      return "stream: the data is not in the stream format";
    },
    Authentication => {
      return "stream: a chunk does not verify";
    },
    Limit => {
      return "stream: the data is larger than the stream format allows";
    },
    Wrap => {
      return "stream: the wrap or unwrap of the data key failed";
    }
  }
}

impl error.Error for StreamError {}

impl cmp.Display for StreamError {}

@secret
opaque struct State { key: chacha20poly1305.Key, prefix: u8[], aad: u8[], counter: u64, done: boolean }

pub opaque struct StreamWriter { state: State }

pub opaque struct StreamReader { state: State }

function start(key: [u8], prefix: [u8], aad: [u8]): Result[State, StreamError] {
  if (key.len() != 32) {
    return Err(KeyLength);
  }
  if (prefix.len() != 16) {
    return Err(Format);
  }
  let Ok(subkey) = crypto.hkdf_sha256(prefix, key, "Fern STREAM v1".as_bytes(), 32) else {
    return Err(KeyLength);
  };
  let cipher = chacha20poly1305.key(subkey);
  crypto.erase(subkey);
  let Ok(k) = cipher else {
    return Err(KeyLength);
  };
  return Ok(State { key: k, prefix: __bytes_owned(prefix), aad: __bytes_owned(aad), counter: 0u64, done: false });
}

pub function writer(key: [u8]): Result[(u8[], StreamWriter), StreamError] {
  let prefix = rand.bytes(16);
  let s = start(key, prefix, [])?;
  return Ok((prefix, StreamWriter { state: s }));
}

pub function reader(key: [u8], prefix: [u8]): Result[StreamReader, StreamError] {
  return Ok(StreamReader { state: start(key, prefix, [])? });
}

function nonce(s: State, last: boolean): u8[] {
  let n = __bytes_into(__alloc_u8(24), 0, s.prefix, 0, 16);
  for i in 0..7 {
    n = n.with(22 - i, (s.counter >> (8 * i) as u64) as u8);
  }
  return n.with(23, if (last) { 1u8 } else { 0u8 });
}

function valid_size(n: i32, last: boolean): boolean {
  return n <= CHUNK_BYTES && (last || n == CHUNK_BYTES);
}

pub function (own w: StreamWriter) write_chunk(plain: [u8], last: boolean): Result[(u8[], StreamWriter), StreamError] {
  let s = w.state;
  if (s.done || !valid_size(plain.len(), last)) {
    return Err(Format);
  }
  if (s.counter == LAST_COUNTER && !last) {
    return Err(Limit);
  }
  let iv = nonce(s, last);
  let Ok(cipher) = s.key.seal_xchacha(iv, plain, s.aad) else {
    return Err(Authentication);
  };
  let frame = __bytes_into(__alloc_u8(1 + cipher.len()).with(0, iv[23]), 1, cipher, 0, cipher.len());
  return Ok((frame, StreamWriter { state: State { ...s, counter: s.counter + 1u64, done: last } }));
}

pub function (own r: StreamReader) read_chunk(frame: [u8]): Result[(u8[], StreamReader), StreamError] {
  let s = r.state;
  if (s.done || frame.len() < 17 || frame[0] > 1u8) {
    return Err(Format);
  }
  let last = frame[0] == 1u8;
  if (!valid_size(frame.len() - 17, last)) {
    return Err(Format);
  }
  if (s.counter == LAST_COUNTER && !last) {
    return Err(Limit);
  }
  let Ok(plain) = s.key.open_xchacha(nonce(s, last), frame[1:], s.aad) else {
    return Err(Authentication);
  };
  return Ok((plain, StreamReader { state: State { ...s, counter: s.counter + 1u64, done: last } }));
}

pub function (own r: StreamReader) finish(): Result[void, StreamError] {
  if (!r.state.done) {
    return Err(Format);
  }
  return Ok(());
}

function seal_buffer(key: [u8], plain: [u8], aad: [u8]): Result[u8[], StreamError] {
  let n = plain.len();
  let frames = if (n == 0) { 1 } else { (n - 1) / CHUNK_BYTES + 1 };
  let size = 16i64 + n as i64 + 17i64 * (frames as i64);
  if (size > crypto.MAX_OUTPUT_BYTES as i64) {
    return Err(Limit);
  }
  let prefix = rand.bytes(16);
  let w = StreamWriter { state: start(key, prefix, aad)? };
  let out = __bytes_into(__alloc_u8(size as i32), 0, prefix, 0, 16);
  let at = 0;
  let dest = 16;
  for i in 0..frames {
    let end = at + (n - at).min(CHUNK_BYTES);
    let (frame, next) = w.write_chunk(plain[at:end], i == frames - 1)?;
    out = __bytes_into(out, dest, frame, 0, frame.len());
    dest += frame.len();
    at = end;
    w = next;
  }
  return Ok(out);
}

function open_buffer(key: [u8], data: [u8], aad: [u8]): Result[u8[], StreamError] {
  if (data.len() < 33) {
    return Err(Format);
  }
  let r = StreamReader { state: start(key, data[0:16], aad)? };
  let frames = (data.len() - 17) / FRAME_BYTES + 1;
  if (data.len() - 16 - (frames - 1) * FRAME_BYTES < 17) {
    return Err(Format);
  }
  let size = data.len() - 16 - frames * 17;
  let out = __alloc_u8(size);
  let at = 16;
  let dest = 0;
  while (at < data.len()) {
    let end = at + (data.len() - at).min(FRAME_BYTES);
    let (plain, next) = r.read_chunk(data[at:end])?;
    out = __bytes_into(out, dest, plain, 0, plain.len());
    dest += plain.len();
    at = end;
    r = next;
  }
  r.finish()?;
  return Ok(out);
}

pub function seal_all(key: [u8], plain: [u8]): Result[u8[], StreamError] {
  return seal_buffer(key, plain, []);
}

pub function open_all(key: [u8], data: [u8]): Result[u8[], StreamError] {
  return open_buffer(key, data, []);
}

pub function seal_envelope(plain: [u8], wrap: ([u8]) => Result[u8[], StreamError]): Result[u8[], StreamError] {
  let key = rand.bytes(32);
  let result = wrap(key);
  let Ok(wrapped) = result else {
    crypto.erase(key);
    return Err(Wrap);
  };
  if (wrapped.len() == 0 || wrapped.len() > crypto.MAX_OUTPUT_BYTES - 5) {
    crypto.erase(key);
    return Err(Limit);
  }
  let header = binary.append_u32_be([1u8], wrapped.len() as u32).concat(wrapped);
  let sealed = seal_buffer(key, plain, crypto.sha256(header));
  crypto.erase(key);
  let body = sealed?;
  if (body.len() > crypto.MAX_OUTPUT_BYTES - header.len()) {
    return Err(Limit);
  }
  return Ok(header.concat(body));
}

pub function open_envelope(data: [u8], unwrap: ([u8]) => Result[u8[], StreamError]): Result[u8[], StreamError] {
  if (data.len() < 39 || data[0] != 1u8) {
    return Err(Format);
  }
  let n = binary.read_u32_be(data, 1).unwrap_or(0u32);
  if (n == 0u32 || n > (data.len() - 38) as u32) {
    return Err(Format);
  }
  let end = 5 + n as i32;
  let Ok(key) = unwrap(data[5:end]) else {
    return Err(Wrap);
  };
  let opened = open_buffer(key, data[end:], crypto.sha256(data[0:end]));
  crypto.erase(key);
  return opened;
}
```
