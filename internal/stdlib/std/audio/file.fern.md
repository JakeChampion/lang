# Bounded audio file windows

This helper reads one indexed byte range from a borrowed seekable source.
File offsets use signed 64-bit counts. The retained window has an explicit byte limit.
The source must identify the same immutable file for all calls.
Do not reposition it during a pending window read.

Each `step` performs at most one seek or one bounded read.
A successful empty read before the range is complete is truncated input.
Once the window is ready, further steps perform no source operations.
The caller retains and closes the source after success or failure.

```fern
<<*>>=
// Load one bounded indexed file range without retaining a complete track.

import "std/audio/pcm";
import "std/audio/source";

pub struct Window { offset: i64, bytes: i32, buffer: source.Buffer, positioned: boolean, ready: boolean }

pub function create(offset: i64, bytes: i32, limits: source.Limits): Result[Window, source.ReadError] {
  if (offset < 0i64 || bytes < 1 || bytes > limits.max_buffer_bytes) {
    return Err(source.ReadSize);
  }
  let Some(_) = offset +? bytes as i64 else {
    return Err(source.ReadSize);
  };
  let buffer = source.create(source.Limits { ...limits, max_buffer_bytes: bytes })?;
  return Ok(Window { offset: offset, bytes: bytes, buffer: buffer, positioned: false, ready: false });
}

pub function (window: Window) view(): [u8] {
  return window.buffer.view();
}

function positioned(own window: Window, result: Result[i64, IoError]): Result[Window, source.ReadError] {
  let offset = match (result) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
  if (offset != window.offset) {
    return Err(source.ReadSize);
  }
  return Ok(Window { ...window, positioned: true });
}

function after_read(offset: i64, bytes: i32, buffer: source.Buffer, ready: boolean): Result[Window, source.ReadError] {
  if (!ready && buffer.eof) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  return Ok(Window { offset: offset, bytes: bytes, buffer: buffer, positioned: true, ready: ready });
}

pub function (own window: Window) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[Window, source.ReadError] {
  if (window.ready) {
    return Ok(window);
  }
  if (!window.positioned) {
    let offset = seek(window.offset);
    return positioned(window, offset);
  }
  let (buffer, ready) = window.buffer.ensure(window.bytes, read)?;
  return after_read(window.offset, window.bytes, buffer, ready);
}

pub function (own window: Window) step(reader: Reader): Result[Window, source.ReadError] {
  if (window.ready) {
    return Ok(window);
  }
  if (!window.positioned) {
    let offset = reader.seek(window.offset, 0);
    return positioned(window, offset);
  }
  let (buffer, ready) = window.buffer.ensure_reader(window.bytes, reader)?;
  return after_read(window.offset, window.bytes, buffer, ready);
}
```
