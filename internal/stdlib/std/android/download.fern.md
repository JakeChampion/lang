# Downloads of Android

This module downloads a file through the `DownloadManager` of Android, into the external files directory of the app.

## Model

`start` gives a download to Android, and gives its ID. Android downloads the file in its own process, so a large file does not go through the memory of the app. Android continues the download after a lost connection and after the app stops, and it shows the progress in a notification. The file goes to `dir(ctx)` with the name that the app gives.

`progress` gives the state of a download: its status, the bytes that came, and the size of the file, or -1 when the server did not tell it. `remove` stops a download and deletes its file.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. The app needs the permission `android.permission.INTERNET`. The files directory needs no other permission.

A status is one of `PENDING`, `RUNNING`, `PAUSED`, `DONE` and `FAILED`, the values of `DownloadManager.STATUS_*`. `GONE` is the status of an ID that Android does not know, for example after the user deleted the download. `start` gives -1 when Android does not take the download.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<states>>

<<calls>>

<<helpers>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module downloads files through the DownloadManager of Android.
import "std/android";
import "std/jni";
```

### `states`

```fern
<<states>>=
pub const GONE: i32 = 0;

pub const PENDING: i32 = 1;

pub const RUNNING: i32 = 2;

pub const PAUSED: i32 = 4;

pub const DONE: i32 = 8;

pub const FAILED: i32 = 16;

// The status of a download, the bytes that came, and the size of the file
// or -1.
pub struct Progress { status: i32, done: i64, total: i64 }

const REQUEST: string = "android/app/DownloadManager$Request";

const QUERY: string = "android/app/DownloadManager$Query";

// DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED.
const NOTIFY_COMPLETED: i32 = 1;
```

### `calls`

```fern
<<calls>>=
// Downloads `url`, with the request headers `headers`, to the file `name`
// in `dir(ctx)`. The notification shows `title`. Gives the ID of the
// download, or -1.
pub function start(ctx: usize, url: string, headers: (string, string)[], name: string, title: string): i64 {
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return -1i64;
  }
  defer pop_frame(e);
  let manager = manager_of(ctx, e);
  let uri = parse_uri(e, url);
  let class = jni.find_class(e, REQUEST);
  if (failed(e) || manager == 0 as usize || uri == 0 as usize || class == 0 as usize) {
    return -1i64;
  }
  let args = jni.jvalues([uri]);
  let request = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "(Landroid/net/Uri;)V"), args);
  jni.free_buffer(args);
  if (failed(e) || request == 0 as usize) {
    return -1i64;
  }
  let returns = ")L" + REQUEST + ";";
  for h in headers {
    let added = jni.call_object(e, request, "addRequestHeader", "(Ljava/lang/String;Ljava/lang/String;" + returns, [jni.new_string(e, h.0), jni.new_string(e, h.1)]);
  }
  let titled = jni.call_object(e, request, "setTitle", "(Ljava/lang/CharSequence;" + returns, [jni.new_string(e, title)]);
  let shown = jni.call_object(e, request, "setNotificationVisibility", "(I" + returns, [NOTIFY_COMPLETED as usize]);
  let placed = jni.call_object(e, request, "setDestinationInExternalFilesDir", "(Landroid/content/Context;Ljava/lang/String;Ljava/lang/String;" + returns, [android.activity_object(ctx), 0 as usize, jni.new_string(e, name)]);
  if (failed(e)) {
    return -1i64;
  }
  let enqueue = jni.get_method_id(e, jni.get_object_class(e, manager), "enqueue", "(L" + REQUEST + ";)J");
  let values = jni.jvalues([request]);
  let id = jni.call_long_method_a(e, manager, enqueue, values) as i64;
  jni.free_buffer(values);
  if (failed(e)) {
    return -1i64;
  }
  return id;
}

// The state of the download `id`.
pub function progress(ctx: usize, id: i64): Progress {
  let gone = Progress { status: GONE, done: 0i64, total: -1i64 };
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return gone;
  }
  defer pop_frame(e);
  let manager = manager_of(ctx, e);
  let class = jni.find_class(e, QUERY);
  if (failed(e) || manager == 0 as usize || class == 0 as usize) {
    return gone;
  }
  let none = jni.jvalues([]);
  let query = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  let ids = ids_of(e, id);
  if (failed(e) || query == 0 as usize || ids == 0 as usize) {
    return gone;
  }
  let filtered = jni.call_object(e, query, "setFilterById", "([J)L" + QUERY + ";", [ids]);
  let cursor = jni.call_object(e, manager, "query", "(L" + QUERY + ";)Landroid/database/Cursor;", [query]);
  if (failed(e) || cursor == 0 as usize) {
    return gone;
  }
  defer close_cursor(e, cursor);
  let cursor_class = jni.get_object_class(e, cursor);
  let empty = jni.jvalues([]);
  let first = jni.call_boolean_method_a(e, cursor, jni.get_method_id(e, cursor_class, "moveToFirst", "()Z"), empty);
  jni.free_buffer(empty);
  if (failed(e) || first == 0) {
    return gone;
  }
  let status = column_int(e, cursor, cursor_class, "status");
  let done = column_long(e, cursor, cursor_class, "bytes_so_far");
  let total = column_long(e, cursor, cursor_class, "total_size");
  if (failed(e)) {
    return gone;
  }
  if (total <= 0i64) {
    total = -1i64;
  }
  return Progress { status: status, done: done, total: total };
}

// Stops the download `id` and deletes its file.
pub function remove(ctx: usize, id: i64): void {
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return;
  }
  defer pop_frame(e);
  let manager = manager_of(ctx, e);
  let ids = ids_of(e, id);
  if (failed(e) || manager == 0 as usize || ids == 0 as usize) {
    return;
  }
  let args = jni.jvalues([ids]);
  let removed = jni.call_int_method_a(e, manager, jni.get_method_id(e, jni.get_object_class(e, manager), "remove", "([J)I"), args);
  jni.free_buffer(args);
  let cleared = failed(e);
}

// The directory that the files of `start` go to, or "" when the storage
// is not there.
pub function dir(ctx: usize): string {
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return "";
  }
  defer pop_frame(e);
  let files = jni.call_object(e, android.activity_object(ctx), "getExternalFilesDir", "(Ljava/lang/String;)Ljava/io/File;", [0 as usize]);
  if (failed(e) || files == 0 as usize) {
    return "";
  }
  let path = jni.call_object(e, files, "getAbsolutePath", "()Ljava/lang/String;", []);
  if (failed(e) || path == 0 as usize) {
    return "";
  }
  return jni.string_of(e, path);
}
```

### `helpers`

Each call makes its local references in one frame, and the end of the call frees them all.

```fern
<<helpers>>=
function manager_of(ctx: usize, e: usize): usize {
  let manager = jni.call_object(e, android.activity_object(ctx), "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "download")]);
  if (failed(e)) {
    return 0 as usize;
  }
  return manager;
}

function parse_uri(e: usize, text: string): usize {
  let class = jni.find_class(e, "android/net/Uri");
  if (failed(e) || class == 0 as usize) {
    return 0 as usize;
  }
  let id = jni.get_static_method_id(e, class, "parse", "(Ljava/lang/String;)Landroid/net/Uri;");
  if (failed(e) || id == 0 as usize) {
    return 0 as usize;
  }
  let args = jni.jvalues([jni.new_string(e, text)]);
  let uri = jni.call_static_object_method_a(e, class, id, args);
  jni.free_buffer(args);
  if (failed(e)) {
    return 0 as usize;
  }
  return uri;
}

// A long[] that holds `id`.
function ids_of(e: usize, id: i64): usize {
  let ids = jni.new_long_array(e, 1);
  if (failed(e) || ids == 0 as usize) {
    return 0 as usize;
  }
  jni.set_long_array_region(e, ids, 0, [id]);
  return ids;
}

function column_of(e: usize, cursor: usize, class: usize, name: string): i32 {
  let args = jni.jvalues([jni.new_string(e, name)]);
  let at = jni.call_int_method_a(e, cursor, jni.get_method_id(e, class, "getColumnIndex", "(Ljava/lang/String;)I"), args);
  jni.free_buffer(args);
  return at;
}

function column_int(e: usize, cursor: usize, class: usize, name: string): i32 {
  let at = column_of(e, cursor, class, name);
  if (failed(e) || at < 0) {
    return 0;
  }
  let args = jni.jvalues([at as usize]);
  let value = jni.call_int_method_a(e, cursor, jni.get_method_id(e, class, "getInt", "(I)I"), args);
  jni.free_buffer(args);
  return value;
}

function column_long(e: usize, cursor: usize, class: usize, name: string): i64 {
  let at = column_of(e, cursor, class, name);
  if (failed(e) || at < 0) {
    return 0i64;
  }
  let args = jni.jvalues([at as usize]);
  let value = jni.call_long_method_a(e, cursor, jni.get_method_id(e, class, "getLong", "(I)J"), args) as i64;
  jni.free_buffer(args);
  return value;
}

function close_cursor(e: usize, cursor: usize): void {
  let closed = jni.call_void(e, cursor, "close", "()V", []);
  let cleared = failed(e);
}

// Starts a local frame of references. False when Java has no memory for it.
function push_frame(e: usize): boolean {
  if (jni.call1(e, 19, 32 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  return true;
}

// Ends the local frame of references that a call started.
function pop_frame(e: usize): void {
  let popped = jni.call1(e, 20, 0 as usize);
}

// True when a Java exception was pending, which this clears.
function failed(e: usize): boolean {
  let exception = jni.exception_occurred(e);
  if (exception == 0 as usize) {
    return false;
  }
  let cleared = jni.exception_clear(e);
  let gone = jni.delete_local_ref(e, exception);
  return true;
}
```
