# Android content media sources

This module opens a granted content URI through Android's content resolver.
It returns an owned, seekable descriptor and the asset's byte range.
The descriptor works with `std/android/video/extractor` and `std/android/video/player`.
The caller must keep the URI read grant until `open` returns.

Keep each `Source` with one owner. Close the player or extractor before source close.
Close each successful source once. Copies do not duplicate ownership.
The Java asset closes before `open` returns. No Java global references remain.

`maximum_bytes` bounds the source range, separately from the decoder's packet limit.
It does not bound process memory. The adapter does not read the file into memory.
A provider can report an unknown asset length. The adapter resolves it from the seekable file's end.
Pipes and other non-seekable descriptors return `Unsupported`.

The descriptor shares its file position with the provider's descriptor.
`read_at` reads relative to the asset without changing that shared position.
It clamps each read to the asset boundary. Each request is limited to 1 MiB before allocation or I/O.
An empty read at the declared end returns empty bytes. Short physical reads are permitted.

Keep the source open during reads and close it once after all consumers finish.
These synchronous reads belong outside latency-sensitive Android callbacks.
Use the bytes with the existing native packet readers. This module does not decode media.

```fern
<<*>>=
// This module opens bounded content assets for the Android video packet path.
import "std/jni";
import "std/android/video/source/bindings";
import "std/video/frame";

pub struct Source { fd: i32, offset: i64, length: i64 }

pub struct Range { offset: i64, length: i64 }

@import("libc.so", "fcntl")
function c_duplicate(fd: i32, command: i32, minimum: i32): i32;

@import("libc.so", "lseek")
function c_seek(fd: i32, offset: i64, mode: i32): i64;

@import("libc.so", "close")
function c_close(fd: i32): i32;

@import("libc.so", "pread")
function c_read_at(fd: i32, output: usize, bytes: usize, offset: i64): i64;

const MAX_READ_BYTES: i32 = 1048576;

function range_header(offset: i64, declared_length: i64, file_length: i64, maximum_bytes: i64): Result[void, frame.VideoError] {
  if (offset < 0i64 || declared_length < -1i64 || file_length < 0i64) {
    return Err(frame.Header);
  }
  if (maximum_bytes <= 0i64) {
    return Err(frame.Limit);
  }
  if (offset > file_length) {
    return Err(frame.Truncated);
  }
  return Ok(());
}

// Validate an asset range against a known file size. A declared length of -1 means unknown.
pub function checked_range(offset: i64, declared_length: i64, file_length: i64, maximum_bytes: i64): Result[Range, frame.VideoError] {
  range_header(offset, declared_length, file_length, maximum_bytes)?;
  let length = if (declared_length == -1i64) { file_length - offset } else { declared_length };
  let Some(end) = offset +? length else {
    return Err(frame.Limit);
  };
  if (end > file_length) {
    return Err(frame.Truncated);
  }
  if (length == 0i64) {
    return Err(frame.Header);
  }
  if (length > maximum_bytes) {
    return Err(frame.Limit);
  }
  return Ok(Range { offset: offset, length: length });
}

pub function (source: Source) close(): Result[void, frame.VideoError] {
  if (c_close(source.fd) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

function read_plan(source: Source, offset: i64, count: i32): Result[(i64, i32), frame.VideoError] {
  if (source.fd < 0 || source.length < 0i64 || offset < 0i64 || count < 0) {
    return Err(frame.Header);
  }
  let range = checked_range(source.offset, source.length, 9223372036854775807i64, 9223372036854775807i64)?;
  if (count > MAX_READ_BYTES) {
    return Err(frame.Limit);
  }
  if (offset > range.length) {
    return Err(frame.Truncated);
  }
  let remaining = range.length - offset;
  let bytes = if (remaining < count as i64) { remaining as i32 } else { count };
  return Ok((range.offset + offset, bytes));
}

function read_bytes(fd: i32, offset: i64, count: i32): Result[u8[], frame.VideoError] {
  unsafe {
    let data = __alloc(count);
    let received = c_read_at(fd, data, count as usize, offset);
    if (received < 0i64 || received > count as i64) {
      __free(data, count);
      return Err(frame.Platform);
    }
    let bytes: u8[] = [];
    for i in 0..received as i32 {
      bytes = bytes.append(__load_u8(data + i as usize) as u8);
    }
    __free(data, count);
    return Ok(bytes);
  }
}

// Read at a relative asset offset without changing the descriptor position.
pub function (source: Source) read_at(offset: i64, count: i32): Result[u8[], frame.VideoError] {
  let (position, bytes) = read_plan(source, offset, count)?;
  if (bytes == 0) {
    return Ok([]);
  }
  return read_bytes(source.fd, position, bytes);
}

function cleared(e: usize): boolean {
  let exception = jni.exception_occurred(e);
  if (exception == 0 as usize) {
    return false;
  }
  let _ = jni.exception_clear(e);
  let _ = jni.delete_local_ref(e, exception);
  return true;
}

function begin(e: usize): boolean {
  if (e == 0 as usize) {
    return false;
  }
  let pending = jni.exception_occurred(e);
  if (pending != 0 as usize) {
    let _ = jni.delete_local_ref(e, pending);
    return false;
  }
  if (jni.call1(e, 19, 32 as usize) as i32 != 0) {
    let _ = cleared(e);
    return false;
  }
  return true;
}

function end(e: usize): void {
  let _ = jni.call1(e, 20, 0 as usize);
}

function duplicate(fd: i32, offset: i64, length: i64, maximum: i64): Result[Source, frame.VideoError] {
  // F_DUPFD_CLOEXEC is 1030 on both supported Android ABIs.
  let owned = c_duplicate(fd, 1030, 0);
  if (owned < 0) {
    return Err(frame.Platform);
  }
  let position = c_seek(owned, 0i64, 1);
  if (position < 0i64) {
    let _ = c_close(owned);
    return Err(frame.Unsupported);
  }
  let size = c_seek(owned, 0i64, 2);
  let restored = c_seek(owned, position, 0);
  if (size < 0i64 || restored != position) {
    let _ = c_close(owned);
    return Err(frame.Unsupported);
  }
  match (checked_range(offset, length, size, maximum)) {
    Ok(range) => {
      return Ok(Source { fd: owned, offset: range.offset, length: range.length });
    },
    Err(error) => {
      let _ = c_close(owned);
      return Err(error);
    }
  }
}

function from_asset(e: usize, c: bindings.Classes, asset: bindings.AssetFileDescriptor, maximum: i64): Result[Source, frame.VideoError] {
  let Ok(offset) = c.asset_file_descriptor.get_start_offset(e, asset) else {
    return Err(frame.Platform);
  };
  let Ok(length) = c.asset_file_descriptor.get_declared_length(e, asset) else {
    return Err(frame.Platform);
  };
  let Ok(parcel) = c.asset_file_descriptor.get_parcel_file_descriptor(e, asset) else {
    return Err(frame.Platform);
  };
  if (parcel.is_null()) {
    return Err(frame.Platform);
  }
  let Ok(fd) = c.parcel_file_descriptor.get_fd(e, parcel) else {
    return Err(frame.Platform);
  };
  return duplicate(fd, offset, length, maximum);
}

function valid_uri(uri: string): boolean {
  if (!uri.starts_with("content://") || uri.len() <= 10 || uri.len() > 8192) {
    return false;
  }
  for byte in uri.as_bytes() {
    if (byte == 0u8) {
      return false;
    }
  }
  return true;
}

pub function open(env: usize, context: usize, uri: string, maximum_bytes: i64): Result[Source, frame.VideoError] {
  if (!valid_uri(uri)) {
    return Err(frame.Header);
  }
  if (maximum_bytes <= 0i64) {
    return Err(frame.Limit);
  }
  if (context == 0 as usize || !begin(env)) {
    return Err(frame.Platform);
  }
  defer end(env);
  let Ok(c) = bindings.classes(env) else {
    return Err(frame.Platform);
  };
  defer c.release(env);
  return open_asset(env, c, bindings.Context { ref: context }, uri, maximum_bytes);
}

function open_asset(env: usize, c: bindings.Classes, context: bindings.Context, uri: string, maximum: i64): Result[Source, frame.VideoError] {
  let Ok(parsed) = c.uri.parse(env, uri) else {
    return Err(frame.Platform);
  };
  if (parsed.is_null()) {
    return Err(frame.Platform);
  }
  let Ok(resolver) = c.context.get_content_resolver(env, context) else {
    return Err(frame.Platform);
  };
  if (resolver.is_null()) {
    return Err(frame.Platform);
  }
  let Ok(asset) = c.content_resolver.open_asset_file_descriptor(env, resolver, parsed, "r") else {
    return Err(frame.Platform);
  };
  if (asset.is_null()) {
    return Err(frame.Platform);
  }
  let result = from_asset(env, c, asset, maximum);
  if let Err(_) = c.asset_file_descriptor.close(env, asset) {
    match (result) {
      Ok(source) => {
        let _ = source.close();
      },
      _ => {}
    }
    return Err(frame.Platform);
  }
  return result;
}
```
