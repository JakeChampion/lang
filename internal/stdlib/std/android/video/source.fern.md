# Android content video sources

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
Use positional reads or the extractor, and do not change that position concurrently.

```fern
<<*>>=
// This module opens bounded content assets for the Android video packet path.
import "std/jni";
import "std/video/frame";

pub struct Source { fd: i32, offset: i64, length: i64 }

pub struct Range { offset: i64, length: i64 }

@import("libc.so", "fcntl")
function c_duplicate(fd: i32, command: i32, minimum: i32): i32;

@import("libc.so", "lseek")
function c_seek(fd: i32, offset: i64, mode: i32): i64;

@import("libc.so", "close")
function c_close(fd: i32): i32;

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

function method(e: usize, obj: usize, name: string, sig: string): Result[usize, frame.VideoError] {
  if (obj == 0 as usize) {
    return Err(frame.Platform);
  }
  let class = jni.get_object_class(e, obj);
  if (cleared(e) || class == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_method_id(e, class, name, sig);
  if (cleared(e) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(id);
}

function object(e: usize, obj: usize, name: string, sig: string, args: usize[]): Result[usize, frame.VideoError] {
  let id = method(e, obj, name, sig)?;
  let values = jni.jvalues(args);
  let result = jni.call_object_method_a(e, obj, id, values);
  jni.free_buffer(values);
  if (cleared(e) || result == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(result);
}

function long(e: usize, obj: usize, name: string): Result[i64, frame.VideoError] {
  let id = method(e, obj, name, "()J")?;
  let result = jni.call_long_method_a(e, obj, id, 0 as usize) as i64;
  if (cleared(e)) {
    return Err(frame.Platform);
  }
  return Ok(result);
}

function text(e: usize, value: string): Result[usize, frame.VideoError] {
  let result = jni.new_string(e, value);
  if (cleared(e) || result == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(result);
}

function parse(e: usize, uri: string): Result[usize, frame.VideoError] {
  let value = text(e, uri)?;
  let class = jni.find_class(e, "android/net/Uri");
  if (cleared(e) || class == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_static_method_id(e, class, "parse", "(Ljava/lang/String;)Landroid/net/Uri;");
  if (cleared(e) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  let values = jni.jvalues([value]);
  let result = jni.call_static_object_method_a(e, class, id, values);
  jni.free_buffer(values);
  if (cleared(e) || result == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(result);
}

function close_method(e: usize): Result[usize, frame.VideoError] {
  let class = jni.find_class(e, "android/content/res/AssetFileDescriptor");
  if (cleared(e) || class == 0 as usize) {
    return Err(frame.Platform);
  }
  let id = jni.get_method_id(e, class, "close", "()V");
  if (cleared(e) || id == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(id);
}

function close_asset(e: usize, asset: usize, id: usize): boolean {
  let _ = jni.call_void_method_a(e, asset, id, 0 as usize);
  return !cleared(e);
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

function from_asset(e: usize, asset: usize, maximum: i64): Result[Source, frame.VideoError] {
  let offset = long(e, asset, "getStartOffset")?;
  let length = long(e, asset, "getDeclaredLength")?;
  let parcel = object(e, asset, "getParcelFileDescriptor", "()Landroid/os/ParcelFileDescriptor;", [])?;
  let id = method(e, parcel, "getFd", "()I")?;
  let fd = jni.call_int_method_a(e, parcel, id, 0 as usize);
  if (cleared(e)) {
    return Err(frame.Platform);
  }
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
  let close_id = close_method(env)?;
  let parsed = parse(env, uri)?;
  let resolver = object(env, context, "getContentResolver", "()Landroid/content/ContentResolver;", [])?;
  let mode = text(env, "r")?;
  let asset = object(env, resolver, "openAssetFileDescriptor", "(Landroid/net/Uri;Ljava/lang/String;)Landroid/content/res/AssetFileDescriptor;", [parsed, mode])?;
  return consume_asset(env, asset, close_id, maximum_bytes);
}

function consume_asset(env: usize, asset: usize, close_id: usize, maximum_bytes: i64): Result[Source, frame.VideoError] {
  let result = from_asset(env, asset, maximum_bytes);
  if (!close_asset(env, asset, close_id)) {
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
