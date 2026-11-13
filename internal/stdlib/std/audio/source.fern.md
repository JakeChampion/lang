# Bounded audio byte sources

This helper retains a bounded byte window for incremental audio frame readers.
Each `ensure` call performs at most one source read.
It returns the next buffer and a flag that identifies sufficient input.
Insufficient input can mean a short read or permanent EOF. The buffer records EOF explicitly.

The source callback returns at most the requested bytes.
An empty successful read means permanent EOF. I/O errors remain distinct from codec errors.
`view` borrows the remaining bytes. `advance` consumes a checked prefix.
The same source must continue from its current position across calls.

An empty buffer takes ownership of the next chunk without a second byte-array copy.
A nonempty buffer preserves its unread prefix before it appends the next chunk.
Shared chunk values retain their original bytes through the normal array ownership rules.

```fern
<<*>>=
// Retain a bounded byte window across short audio source reads.

import "core/cmp";
import "core/error";
import "std/audio/pcm";

pub enum ReadError { ReadIo(IoError), ReadAudio(pcm.AudioError), ReadSize }

impl error.Error for ReadError {}

impl cmp.Display for ReadError {}

pub function (e: ReadError) message(): string {
  return match (e) {
    ReadIo(value) => value.message(),
    ReadAudio(value) => value.message(),
    ReadSize => "audio source read size is outside the configured limit",
  };
}

pub function (e: ReadError) to_string(): string {
  return e.message();
}

pub struct Limits { chunk_size: i32, max_buffer_bytes: i32 }

pub struct Buffer { bytes: u8[], position: i32, eof: boolean, limits: Limits }

pub function default_limits(): Limits {
  return Limits { chunk_size: 16384, max_buffer_bytes: 65536 };
}

pub function create(limits: Limits): Result[Buffer, ReadError] {
  if (limits.chunk_size < 1 || limits.chunk_size > 1048576 || limits.max_buffer_bytes < 1) {
    return Err(ReadSize);
  }
  return Ok(Buffer { bytes: [], position: 0, eof: false, limits: limits });
}

pub function (buffer: Buffer) view(): [u8] {
  return buffer.bytes[buffer.position:];
}

pub function (buffer: Buffer) available(): i32 {
  return buffer.bytes.len() - buffer.position;
}

pub function (own buffer: Buffer) advance(count: i32): Result[Buffer, ReadError] {
  if (count < 0 || count > buffer.available()) {
    return Err(ReadSize);
  }
  let at = buffer.position + count;
  if (at == buffer.bytes.len()) {
    return Ok(Buffer { ...buffer, bytes: [], position: 0 });
  }
  return Ok(Buffer { ...buffer, position: at });
}

function compact(buffer: Buffer): u8[] {
  let bytes: u8[] = [];
  for byte in buffer.view() {
    bytes = bytes.append(byte);
  }
  return bytes;
}

function extend(own buffer: Buffer, own chunk: u8[]): Buffer {
  if (buffer.available() == 0) {
    let ended = chunk.len() == 0;
    return Buffer { ...buffer, bytes: chunk, position: 0, eof: ended };
  }
  let bytes = if (buffer.position == 0) { buffer.bytes } else { compact(buffer) };
  for byte in chunk {
    bytes = bytes.append(byte);
  }
  return Buffer { bytes: bytes, position: 0, eof: chunk.len() == 0, limits: buffer.limits };
}

pub function (own buffer: Buffer) ensure(needed: i32, read: (i32) => Result[u8[], IoError]): Result[(Buffer, boolean), ReadError] {
  if (needed < 0 || needed > buffer.limits.max_buffer_bytes) {
    return Err(ReadSize);
  }
  if (buffer.available() >= needed || buffer.eof) {
    let ready = buffer.available() >= needed;
    return Ok((buffer, ready));
  }
  let free = buffer.limits.max_buffer_bytes - buffer.available();
  let count = if (buffer.limits.chunk_size < free) { buffer.limits.chunk_size } else { free };
  let chunk = match (read(count)) { Ok(value) => value, Err(e) => { return Err(ReadIo(e)); } };
  if (chunk.len() > count) {
    return Err(ReadSize);
  }
  let next = extend(buffer, chunk);
  let ready = next.available() >= needed;
  return Ok((next, ready));
}

pub function (own buffer: Buffer) ensure_reader(needed: i32, reader: Reader): Result[(Buffer, boolean), ReadError] {
  return buffer.ensure(needed, (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
