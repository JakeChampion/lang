# Downloads of Android

This module downloads a file through the `DownloadManager` of Android, into the external files directory of the app.

## Model

`start` gives a download to Android, and gives its ID. Android downloads the file in its own process, so a large file does not go through the memory of the app. Android continues the download after a lost connection and after the app stops, and it shows the progress in a notification. The file goes to `dir(ctx)` with the name that the app gives.

`start_with_network` can limit a download to Wi-Fi.

`progress` gives the state of a download: its status, the bytes that came, and the size of the file, or -1 when the server did not tell it. `remove` stops a download and deletes its file. `remove_many` removes several downloads in one request.

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
import "std/android/download/bindings";
import "std/result";
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

// DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED.
const NOTIFY_COMPLETED: i32 = 1;
```

### `calls`

```fern
<<calls>>=
// Downloads the file, or gives -1 if Android refuses it.
pub function start(ctx: usize, url: string, headers: (string, string)[], name: string, title: string): i64 {
  return start_with_network(ctx, url, headers, name, title, false);
}

// When wifi_only is true, allow the file to download on Wi-Fi.
pub function start_with_network(ctx: usize, url: string, headers: (string, string)[], name: string, title: string, wifi_only: boolean): i64 {
  let Some(s) = session(ctx) else {
    return -1i64;
  };
  defer close(s);
  return enqueue(s, url, headers, name, title, wifi_only).unwrap_or(-1i64);
}

function enqueue(s: Session, url: string, headers: (string, string)[], name: string, title: string, wifi_only: boolean): Result[i64, jni.JavaError] {
  let manager = manager_of(s)?;
  let uri = s.c.uri.parse(s.e, url)?;
  let r = s.c.download_manager_request;
  let request = r.new(s.e, uri)?;
  if (wifi_only) {
    // DownloadManager.Request.NETWORK_WIFI.
    let allowed = r.set_allowed_network_types(s.e, request, 2)?;
    jni.delete_local_ref(s.e, allowed.ref);
  }
  for h in headers {
    let added = r.add_request_header(s.e, request, h.0, h.1)?;
    jni.delete_local_ref(s.e, added.ref);
  }
  let label = jni.new_string(s.e, title);
  jni.check(s.e)?;
  r.set_title(s.e, request, jni.Object { ref: label })?;
  r.set_notification_visibility(s.e, request, NOTIFY_COMPLETED)?;
  r.set_destination_in_external_files_dir(s.e, request, s.app, None, name)?;
  return s.c.download_manager.enqueue(s.e, manager, request);
}

pub function progress(ctx: usize, id: i64): Progress {
  let gone = Progress { status: GONE, done: 0i64, total: -1i64 };
  let Some(s) = session(ctx) else {
    return gone;
  };
  defer close(s);
  let Ok(cursor) = query(s, id) else {
    return gone;
  };
  if (cursor.is_null()) {
    return gone;
  }
  defer close_cursor(s, cursor);
  if (!s.c.cursor.move_to_first(s.e, cursor).unwrap_or(false)) {
    return gone;
  }
  return row(s, cursor).unwrap_or(gone);
}

function query(s: Session, id: i64): Result[bindings.Cursor, jni.JavaError] {
  let manager = manager_of(s)?;
  let q = s.c.download_manager_query.new(s.e)?;
  s.c.download_manager_query.set_filter_by_id(s.e, q, ids_of(s.e, [id])?)?;
  return s.c.download_manager.query(s.e, manager, q);
}

function row(s: Session, cursor: bindings.Cursor): Result[Progress, jni.JavaError] {
  let c = s.c.cursor;
  let at = c.get_column_index(s.e, cursor, "status")?;
  let status = if (at < 0) { 0 } else { c.get_int(s.e, cursor, at)? };
  let done = long_column(s, cursor, "bytes_so_far")?;
  let total = long_column(s, cursor, "total_size")?;
  return Ok(Progress { status: status, done: done, total: if (total > 0i64) { total } else { -1i64 } });
}

function long_column(s: Session, cursor: bindings.Cursor, name: string): Result[i64, jni.JavaError] {
  let at = s.c.cursor.get_column_index(s.e, cursor, name)?;
  if (at < 0) {
    return Ok(0i64);
  }
  return s.c.cursor.get_long(s.e, cursor, at);
}

pub function remove(ctx: usize, id: i64): void {
  let removed = remove_many(ctx, [id]);
}

// Removes the files for the IDs in one request. A missing ID is already removed.
pub function remove_many(ctx: usize, ids: i64[]): boolean {
  if (ids.len() == 0) {
    return true;
  }
  let Some(s) = session(ctx) else {
    return false;
  };
  defer close(s);
  let Ok(manager) = manager_of(s) else {
    return false;
  };
  let Ok(array) = ids_of(s.e, ids) else {
    return false;
  };
  return s.c.download_manager.remove(s.e, manager, array).is_ok();
}

pub function dir(ctx: usize): string {
  let Some(s) = session(ctx) else {
    return "";
  };
  defer close(s);
  let Ok(files) = s.c.context.get_external_files_dir(s.e, s.app, None) else {
    return "";
  };
  if (files.is_null()) {
    return "";
  }
  return s.c.file.get_absolute_path(s.e, files).unwrap_or("");
}
```

### Helpers

Each operation holds its local references in one frame and releases the global class references before popping that frame.

```fern
<<helpers>>=
struct Session { e: usize, app: bindings.Context, c: bindings.Classes }

function session(ctx: usize): Option[Session] {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 64)) {
    return None;
  }
  let Ok(c) = bindings.classes(e) else {
    jni.pop_local_frame(e, 0 as usize);
    return None;
  };
  return Some(Session { e: e, app: bindings.Context { ref: android.activity_object(ctx) }, c: c });
}

function close(s: Session): void {
  s.c.release(s.e);
  jni.call1(s.e, 20, 0 as usize);
}

function close_cursor(s: Session, cursor: bindings.Cursor): void {
  let closed = s.c.cursor.close(s.e, cursor);
}

function manager_of(s: Session): Result[bindings.DownloadManager, jni.JavaError] {
  let service = s.c.context.get_system_service(s.e, s.app, "download")?;
  if (service.is_null()) {
    return Err(jni.absent("download service"));
  }
  return Ok(bindings.DownloadManager { ref: service.ref });
}

function ids_of(e: usize, input: i64[]): Result[jni.Object, jni.JavaError] {
  let ids = jni.new_long_array(e, input.len());
  jni.check(e)?;
  jni.set_long_array_region(e, ids, 0, input);
  jni.check(e)?;
  return Ok(jni.Object { ref: ids });
}
```
