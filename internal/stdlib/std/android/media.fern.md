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
```

### `media`

The query matches the column `_data`, which has the path of the file.

```fern
<<media>>=
// The bytes of one part of `write`.
const PART: i32 = 1048576;

// The request code of the dialog of `ask_write`.
const ASK_CODE: i32 = 7301;

// True when the last JNI call threw. It clears the exception.
function threw(e: usize): boolean {
  if (jni.exception_occurred(e) == 0 as usize) {
    return false;
  }
  jni.exception_clear(e);
  return true;
}

function sdk_int(e: usize): i32 {
  let version = jni.find_class(e, "android/os/Build$VERSION");
  return jni.get_static_int_field(e, version, jni.get_static_field_id(e, version, "SDK_INT", "I"));
}

function call_static(e: usize, class: usize, name: string, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let out: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, name, sig), values);
  jni.free_buffer(values);
  return out;
}

function drop_all(e: usize, refs: usize[]): void {
  for r in refs {
    if (r != 0 as usize) {
      let dropped: usize = jni.delete_local_ref(e, r);
    }
  }
}

// The first value of the first row of `cursor` as a long, or -1. It
// closes the cursor.
function first_long(e: usize, cursor: usize): i64 {
  let class = jni.get_object_class(e, cursor);
  let found = jni.call_boolean_method_a(e, cursor, jni.get_method_id(e, class, "moveToFirst", "()Z"), 0 as usize) != 0;
  let id = -1i64;
  if (!threw(e) && found) {
    let args: usize = jni.jvalues([0 as usize]);
    id = jni.call_long_method_a(e, cursor, jni.get_method_id(e, class, "getLong", "(I)J"), args) as i64;
    jni.free_buffer(args);
    if (threw(e)) {
      id = -1i64;
    }
  }
  let closed: boolean = jni.call_void(e, cursor, "close", "()V", []);
  let dropped: usize = jni.delete_local_ref(e, class);
  return id;
}

// The content URI of the audio file at `path` in the MediaStore, as a
// local reference, or 0.
pub function audio_uri(e: usize, context: usize, path: string): usize {
  let resolver = jni.call_object(e, context, "getContentResolver", "()Landroid/content/ContentResolver;", []);
  if (threw(e) || resolver == 0 as usize) {
    return 0 as usize;
  }
  let volume = jni.new_string(e, "external");
  let base = call_static(e, jni.find_class(e, "android/provider/MediaStore$Audio$Media"), "getContentUri", "(Ljava/lang/String;)Landroid/net/Uri;", [volume]);
  let projection = jni.string_array(e, ["_id"]);
  let selection = jni.new_string(e, "_data=?");
  let args = jni.string_array(e, [path]);
  let cursor = jni.call_object(e, resolver, "query", "(Landroid/net/Uri;[Ljava/lang/String;Ljava/lang/String;[Ljava/lang/String;Ljava/lang/String;)Landroid/database/Cursor;", [base, projection, selection, args, 0 as usize]);
  let id = -1i64;
  if (!threw(e) && cursor != 0 as usize) {
    id = first_long(e, cursor);
  }
  let uri = 0 as usize;
  if (id >= 0i64 && base != 0 as usize) {
    uri = call_static(e, jni.find_class(e, "android/content/ContentUris"), "withAppendedId", "(Landroid/net/Uri;J)Landroid/net/Uri;", [base, id as usize]);
    if (threw(e)) {
      uri = 0 as usize;
    }
  }
  drop_all(e, [cursor, args, selection, projection, base, volume, resolver]);
  return uri;
}

// Shows the dialog of the system that asks the person to let the app
// change the audio file at `path`. It is false when the dialog does not
// show: before API level 30, or for a file that is not in the MediaStore.
pub function ask_write(ctx: usize, path: string): boolean {
  let e = android.env(ctx);
  let activity = android.activity_object(ctx);
  if (sdk_int(e) < 30) {
    return false;
  }
  let uri = audio_uri(e, activity, path);
  if (uri == 0 as usize) {
    return false;
  }
  let resolver = jni.call_object(e, activity, "getContentResolver", "()Landroid/content/ContentResolver;", []);
  let list = call_static(e, jni.find_class(e, "java/util/Collections"), "singletonList", "(Ljava/lang/Object;)Ljava/util/List;", [uri]);
  let pending = call_static(e, jni.find_class(e, "android/provider/MediaStore"), "createWriteRequest", "(Landroid/content/ContentResolver;Ljava/util/Collection;)Landroid/app/PendingIntent;", [resolver, list]);
  if (threw(e) || pending == 0 as usize) {
    drop_all(e, [list, resolver, uri]);
    return false;
  }
  let sender = jni.call_object(e, pending, "getIntentSender", "()Landroid/content/IntentSender;", []);
  let shown: boolean = !threw(e) && sender != 0 as usize && jni.call_void(e, activity, "startIntentSenderForResult", "(Landroid/content/IntentSender;ILandroid/content/Intent;III)V", [sender, ASK_CODE as usize, 0 as usize, 0 as usize, 0 as usize, 0 as usize]);
  let failed = threw(e);
  drop_all(e, [sender, pending, list, resolver, uri]);
  return shown && !failed;
}

// Writes the parts of `bytes` to the stream `out`, and closes it.
function written(e: usize, out: usize, bytes: [u8]): boolean {
  let n = bytes.len().min(PART);
  let chunk = jni.new_byte_array(e, n.max(1));
  let ok = !threw(e) && chunk != 0 as usize;
  let at = 0;
  while (ok && at < bytes.len()) {
    let size = (bytes.len() - at).min(PART);
    jni.set_byte_array_region(e, chunk, 0, bytes[at:at + size]);
    ok = jni.call_void(e, out, "write", "([BII)V", [chunk, 0 as usize, size as usize]) && !threw(e);
    at += size;
  }
  let closed: boolean = jni.call_void(e, out, "close", "()V", []);
  ok = ok && closed && !threw(e);
  drop_all(e, [chunk]);
  return ok;
}

// Writes `bytes` in the place of the bytes of the audio file at `path`,
// through its content URI. It is false when Android does not let the app
// change the file.
pub function write(e: usize, context: usize, path: string, bytes: [u8]): boolean {
  let uri = audio_uri(e, context, path);
  if (uri == 0 as usize) {
    return false;
  }
  let resolver = jni.call_object(e, context, "getContentResolver", "()Landroid/content/ContentResolver;", []);
  let mode = jni.new_string(e, "wt");
  let out = jni.call_object(e, resolver, "openOutputStream", "(Landroid/net/Uri;Ljava/lang/String;)Ljava/io/OutputStream;", [uri, mode]);
  let ok = !threw(e) && out != 0 as usize && written(e, out, bytes);
  drop_all(e, [out, mode, resolver, uri]);
  return ok;
}
```
