# Watch Next of Android TV

This module puts the items that the person watches on the Watch Next row of the home screen of Android TV.

## Model

The home screen of Android TV has a row of the items to watch next. An app adds its programs to the row through the content provider of `android.media.tv`, at `TvContract.WatchNextPrograms`. Each program is one row of that provider.

`keep` adds a program that the person watched in part, or changes it. A `Program` has the ID of the app for the item, its title, its kind, the place in milliseconds where the person stopped, its length, the address of its poster, and the intent URI that opens the item in the app. The app finds its own program again by its ID, in the column `internal_provider_id`, so a second `keep` of one ID changes the program. `drop` takes the program of an ID away, for example when the person watched the item to its end. `app_link` gives the intent URI that opens the activity of the app with one string extra, such as the key of the item to play.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. The provider is on Android TV from API level 26. On a phone there is no such provider. Then `keep` and `drop` change nothing, and `keep` gives false.

A program needs no permission: the provider keeps the programs of each app apart.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<programs>>

<<calls>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module puts items on the Watch Next row of Android TV.
import "std/android";
import "std/clock";
import "std/jni";
```

### `programs`

`MOVIE` and `EPISODE` are the values of `TvContract.PreviewPrograms.TYPE_MOVIE` and `TYPE_TV_EPISODE`. `CONTINUE` is `WATCH_NEXT_TYPE_CONTINUE`.

```fern
<<programs>>=
pub const MOVIE: i32 = 0;

pub const EPISODE: i32 = 3;

const CONTINUE: i32 = 0;

const URI: string = "content://android.media.tv/watch_next_program";

pub struct Program { id: string, title: string, kind: i32, position_ms: i64, duration_ms: i64, poster: string, link: string }
```

### `calls`

```fern
<<calls>>=
// Adds the program, or changes the program with its ID. False when the
// phone has no Watch Next row.
pub function keep(ctx: usize, p: Program): boolean {
  if (android.sdk_version(ctx) < 26) {
    return false;
  }
  let e = android.env(ctx);
  if (jni.call1(e, 19, 64 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  defer pop_frame(e);
  drop_in(ctx, e, p.id);
  let values_class = jni.find_class(e, "android/content/ContentValues");
  if (failed(e) || values_class == 0 as usize) {
    return false;
  }
  let none = jni.jvalues([]);
  let values = jni.new_object_a(e, values_class, jni.get_method_id(e, values_class, "<init>", "()V"), none);
  jni.free_buffer(none);
  if (failed(e) || values == 0 as usize) {
    return false;
  }
  put_text(e, values, "title", p.title);
  put_text(e, values, "internal_provider_id", p.id);
  put_text(e, values, "intent_uri", p.link);
  if (p.poster.len() > 0) {
    put_text(e, values, "poster_art_uri", p.poster);
  }
  put_number(e, values, "type", p.kind as i64);
  put_number(e, values, "watch_next_type", CONTINUE as i64);
  put_number(e, values, "last_playback_position_millis", p.position_ms);
  put_number(e, values, "duration_millis", p.duration_ms);
  put_number(e, values, "last_engagement_time_utc_millis", clock.now().unix_millis());
  let resolver = resolver_of(ctx, e);
  let uri = parse_uri(e);
  if (resolver == 0 as usize || uri == 0 as usize) {
    return false;
  }
  let row = jni.call_object(e, resolver, "insert", "(Landroid/net/Uri;Landroid/content/ContentValues;)Landroid/net/Uri;", [uri, values]);
  return !failed(e) && row != 0 as usize;
}

// The intent URI that starts the NativeActivity of the app with the string
// extra `key` set to `value`. The extra must not hold ";".
pub function app_link(ctx: usize, key: string, value: string): string {
  let e = android.env(ctx);
  let name = jni.call_object(e, android.activity_object(ctx), "getPackageName", "()Ljava/lang/String;", []);
  if (failed(e) || name == 0 as usize) {
    return "";
  }
  let package = jni.string_of(e, name);
  let gone = jni.delete_local_ref(e, name);
  return "intent:#Intent;component=" + package + "/android.app.NativeActivity;S." + key + "=" + value + ";end";
}

// Takes the program with the ID `id` away.
pub function drop(ctx: usize, id: string): void {
  if (android.sdk_version(ctx) < 26) {
    return;
  }
  let e = android.env(ctx);
  if (jni.call1(e, 19, 16 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return;
  }
  drop_in(ctx, e, id);
  pop_frame(e);
}

// Deletes the rows of the app whose internal_provider_id is `id`.
function drop_in(ctx: usize, e: usize, id: string): void {
  let resolver = resolver_of(ctx, e);
  let uri = parse_uri(e);
  let column = jni.new_string(e, "internal_provider_id=?");
  let text = jni.new_string(e, id);
  let string_class = jni.find_class(e, "java/lang/String");
  if (failed(e) || resolver == 0 as usize || uri == 0 as usize || string_class == 0 as usize) {
    return;
  }
  let args = jni.new_object_array(e, 1, string_class, text);
  if (failed(e) || args == 0 as usize) {
    return;
  }
  let id_of_delete = jni.get_method_id(e, jni.get_object_class(e, resolver), "delete", "(Landroid/net/Uri;Ljava/lang/String;[Ljava/lang/String;)I");
  if (failed(e) || id_of_delete == 0 as usize) {
    return;
  }
  let call_args = jni.jvalues([uri, column, args]);
  let deleted = jni.call_int_method_a(e, resolver, id_of_delete, call_args);
  jni.free_buffer(call_args);
  let cleared = failed(e);
}

function resolver_of(ctx: usize, e: usize): usize {
  let activity = android.activity_object(ctx);
  let resolver = jni.call_object(e, activity, "getContentResolver", "()Landroid/content/ContentResolver;", []);
  if (failed(e)) {
    return 0 as usize;
  }
  return resolver;
}

function parse_uri(e: usize): usize {
  let class = jni.find_class(e, "android/net/Uri");
  if (failed(e) || class == 0 as usize) {
    return 0 as usize;
  }
  let id = jni.get_static_method_id(e, class, "parse", "(Ljava/lang/String;)Landroid/net/Uri;");
  if (failed(e) || id == 0 as usize) {
    return 0 as usize;
  }
  let args = jni.jvalues([jni.new_string(e, URI)]);
  let uri = jni.call_static_object_method_a(e, class, id, args);
  jni.free_buffer(args);
  if (failed(e)) {
    return 0 as usize;
  }
  return uri;
}

function put_text(e: usize, values: usize, key: string, value: string): void {
  let ok = jni.call_void(e, values, "put", "(Ljava/lang/String;Ljava/lang/String;)V", [jni.new_string(e, key), jni.new_string(e, value)]);
  let cleared = failed(e);
}

function put_number(e: usize, values: usize, key: string, value: i64): void {
  let long_class = jni.find_class(e, "java/lang/Long");
  if (failed(e) || long_class == 0 as usize) {
    return;
  }
  let id = jni.get_static_method_id(e, long_class, "valueOf", "(J)Ljava/lang/Long;");
  if (failed(e) || id == 0 as usize) {
    return;
  }
  let args = jni.jvalues([value as usize]);
  let boxed = jni.call_static_object_method_a(e, long_class, id, args);
  jni.free_buffer(args);
  if (failed(e) || boxed == 0 as usize) {
    return;
  }
  let ok = jni.call_void(e, values, "put", "(Ljava/lang/String;Ljava/lang/Long;)V", [jni.new_string(e, key), boxed]);
  let cleared = failed(e);
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
