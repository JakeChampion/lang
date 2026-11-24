# Android media files

This module finds an audio file of the MediaStore of the phone, and changes its bytes, for a NativeActivity app of `std/android`.

## Model

Android lets an app change only its own files and the files that the person gives it. An audio file of another app has a content URI in the MediaStore. `audio_uri` finds that URI from the path of the file.

`ask_write` shows the dialog of the system that asks the person to let the app change the file. The app does not get the answer of the dialog. It tries the write again when its activity comes back, and the write works when the person said yes.

`write` writes all the bytes of the file through the content resolver. It writes a part of 1 MiB at a time, so the Java heap holds only one part.

`ask_write` needs API level 30. The other functions need API level 29.

## Contracts

Call each function on the loop thread, while the activity is valid. `context` is the activity, or another Context of the app. Each function gives 0 or false when Android has no such file or does not let the app do it, and clears the Java exception.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<media>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module finds an audio file of the MediaStore and changes its bytes.
import "std/android";
import "std/jni";
import "std/android/media/bindings";
import "std/result";
```

### `media`

The query matches the column `_data`, which has the path of the file.

```fern
<<media>>=
// The bytes of one part of `write`.
const PART: i32 = 1048576;

// The request code of the dialog of `ask_write`.
const ASK_CODE: i32 = 7301;

function resolver_of(e: usize, c: bindings.Classes, context: bindings.Context): Result[bindings.ContentResolver, jni.JavaError] {
  let resolver = c.context.get_content_resolver(e, context)?;
  if (resolver.is_null()) {
    return Err(jni.JavaError { text: "content resolver unavailable" });
  }
  return Ok(resolver);
}

function first_id(e: usize, c: bindings.Classes, cursor: bindings.Cursor): Result[i64, jni.JavaError] {
  let found = c.cursor.move_to_first(e, cursor)?;
  if (!found) {
    return Ok(-1i64);
  }
  return c.cursor.get_long(e, cursor, 0);
}

function string_array(e: usize, value: string): Result[jni.Object, jni.JavaError] {
  let text = jni.checked_string(e, value)?;
  let class = jni.get_object_class(e, text);
  jni.check(e)?;
  let array = jni.new_object_array(e, 1, class, text);
  jni.check(e)?;
  return Ok(jni.Object { ref: array });
}

function query_id(e: usize, c: bindings.Classes, resolver: bindings.ContentResolver, base: bindings.Uri, path: string): Result[i64, jni.JavaError] {
  let columns = string_array(e, "_id")?;
  let paths = string_array(e, path)?;
  let cursor = c.content_resolver.query(e, resolver, base, columns, "_data=?", paths, None)?;
  if (cursor.is_null()) {
    return Ok(-1i64);
  }
  let result = first_id(e, c, cursor);
  c.cursor.close(e, cursor)?;
  return result;
}

function locate(e: usize, c: bindings.Classes, resolver: bindings.ContentResolver, path: string): Result[bindings.Uri, jni.JavaError] {
  let base = c.media_store_audio_media.get_content_uri(e, "external")?;
  if (!base.is_null()) {
    let id = query_id(e, c, resolver, base, path)?;
    if (id >= 0i64) {
      return c.content_uris.with_appended_id(e, base, id);
    }
  }
  return Ok(bindings.Uri { ref: 0 as usize });
}

function lookup(e: usize, context: usize, path: string): Result[usize, jni.JavaError] {
  let c = bindings.classes(e)?;
  defer c.release(e);
  let resolver = resolver_of(e, c, bindings.Context { ref: context })?;
  let uri = locate(e, c, resolver, path)?;
  return Ok(uri.ref);
}

// Returns a local reference in the caller's frame.
pub function audio_uri(e: usize, context: usize, path: string): usize {
  if (!jni.push_local_frame(e, 48)) {
    return 0 as usize;
  }
  return jni.pop_local_frame(e, lookup(e, context, path).unwrap_or(0 as usize));
}

function request(e: usize, c: bindings.Classes, activity: bindings.Activity, resolver: bindings.ContentResolver, uri: bindings.Uri): Result[boolean, jni.JavaError] {
  let list = c.collections.singleton_list(e, uri.object())?;
  let pending = c.media_store.create_write_request(e, resolver, list.as_collection())?;
  if (pending.is_null()) {
    return Ok(false);
  }
  let sender = c.pending_intent.get_intent_sender(e, pending)?;
  if (sender.is_null()) {
    return Ok(false);
  }
  c.activity.start_intent_sender_for_result(e, activity, sender, ASK_CODE, bindings.Intent { ref: 0 as usize }, 0, 0, 0)?;
  return Ok(true);
}

function ask(e: usize, activity: bindings.Activity, path: string): Result[boolean, jni.JavaError] {
  let c = bindings.classes(e)?;
  defer c.release(e);
  let resolver = resolver_of(e, c, activity.as_context())?;
  let uri = locate(e, c, resolver, path)?;
  if (uri.is_null()) {
    return Ok(false);
  }
  return request(e, c, activity, resolver, uri);
}

pub function ask_write(ctx: usize, path: string): boolean {
  if (android.sdk_version(ctx) < 30) {
    return false;
  }
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 48)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  return ask(e, bindings.Activity { ref: android.activity_object(ctx) }, path).unwrap_or(false);
}

function write_parts(e: usize, c: bindings.Classes, out: bindings.OutputStream, bytes: [u8]): Result[boolean, jni.JavaError] {
  let chunk = jni.new_byte_array(e, bytes.len().min(PART).max(1));
  jni.check(e)?;
  if (chunk == 0 as usize) {
    return Ok(false);
  }
  let at = 0;
  while (at < bytes.len()) {
    let size = (bytes.len() - at).min(PART);
    jni.set_byte_array_region(e, chunk, 0, bytes[at:at + size]);
    jni.check(e)?;
    c.output_stream.write(e, out, jni.Object { ref: chunk }, 0, size)?;
    at += size;
  }
  return Ok(true);
}

function replace(e: usize, context: bindings.Context, path: string, bytes: [u8]): Result[boolean, jni.JavaError] {
  let c = bindings.classes(e)?;
  defer c.release(e);
  let resolver = resolver_of(e, c, context)?;
  let uri = locate(e, c, resolver, path)?;
  if (uri.is_null()) {
    return Ok(false);
  }
  let out = c.content_resolver.open_output_stream(e, resolver, uri, "wt")?;
  if (out.is_null()) {
    return Ok(false);
  }
  let result = write_parts(e, c, out, bytes);
  c.output_stream.close(e, out)?;
  return result;
}

pub function write(e: usize, context: usize, path: string, bytes: [u8]): boolean {
  if (!jni.push_local_frame(e, 48)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  return replace(e, bindings.Context { ref: context }, path, bytes).unwrap_or(false);
}
```
