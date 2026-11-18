# The rows of Android TV

This module puts the items of an app on the home screen of Android TV: on the Watch Next row, and on a channel row of the app.

## Model

The home screen of Android TV has a row of the items to watch next. An app adds its programs to the row through the content provider of `android.media.tv`, at `TvContract.WatchNextPrograms`. Each program is one row of that provider.

A channel is a row of the app on the home screen. `add_channel` adds one, with its name and the intent URI of its logo, and gives its ID. Android shows the first channel of an app with no question. The app keeps the ID, and `show_channel` gives the channel its programs in place of the programs before. A channel program goes in `TvContract.PreviewPrograms`, with the ID of its channel. `home_rows` is true on a device that has these rows.

`keep` adds a program that the person watched in part, or changes it.

A `Program` has the ID of the app for the item, its title and its kind. It also has the place in milliseconds where the person stopped, and its length. Last, it has the address of its poster, and the intent URI that opens the item in the app. The app finds its own program again by its ID, in the column `internal_provider_id`, so a second `keep` of one ID changes the program. `drop` takes the program of an ID away, for example when the person watched the item to its end. `app_link` gives the intent URI that opens the activity of the app with one string extra, such as the key of the item to play.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. The provider is on Android TV from API level 26. On a phone there is no such provider. Then `keep`, `drop` and `show_channel` change nothing, `keep` and `show_channel` give false, and `add_channel` gives -1. `show_channel` also gives false when the person removed the channel. Then add a new channel.

A program needs no permission: the provider keeps the programs of each app apart.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<programs>>

<<calls>>

<<channels>>

<<rows>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module puts items on the Watch Next row and on a channel row of
// Android TV.
import "std/android";
import "std/clock";
import "std/i64";
import "std/jni";
```

### `programs`

The kinds are the values of `TYPE_MOVIE`, `TYPE_TV_SERIES`, `TYPE_TV_SEASON`, `TYPE_TV_EPISODE`, `TYPE_TRACK`, `TYPE_ALBUM` and `TYPE_ARTIST` of `TvContract.PreviewPrograms`. `CONTINUE` is `WATCH_NEXT_TYPE_CONTINUE`, and `POSTER` is `ASPECT_RATIO_2_3`.

```fern
<<programs>>=
pub const MOVIE: i32 = 0;

pub const SERIES: i32 = 1;

pub const SEASON: i32 = 2;

pub const EPISODE: i32 = 3;

pub const TRACK: i32 = 7;

pub const ALBUM: i32 = 8;

pub const ARTIST: i32 = 9;

const CONTINUE: i32 = 0;

const POSTER: i32 = 4;

const URI: string = "content://android.media.tv/watch_next_program";

const CHANNELS: string = "content://android.media.tv/channel";

const PREVIEWS: string = "content://android.media.tv/preview_program";

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
  let values = new_values(e);
  if (values == 0 as usize) {
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
  return insert(ctx, e, URI, values) != 0 as usize;
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

// Deletes the Watch Next rows of the app whose internal_provider_id is `id`.
function drop_in(ctx: usize, e: usize, id: string): void {
  delete_where(ctx, e, URI, "internal_provider_id=?", id);
}
```

### `channels`

The ID of a new row is the last segment of its URI. `requestChannelBrowsable` asks Android to show the channel. Android approves the first request of an app with no question.

```fern
<<channels>>=
// Adds a channel row of the app with the name `name`. A tap on its logo
// opens the intent URI `link`. The ID of the channel, or -1.
pub function add_channel(ctx: usize, name: string, link: string): i64 {
  if (!home_rows(ctx)) {
    return -1i64;
  }
  let e = android.env(ctx);
  if (jni.call1(e, 19, 32 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return -1i64;
  }
  defer pop_frame(e);
  let values = new_values(e);
  if (values == 0 as usize) {
    return -1i64;
  }
  put_text(e, values, "type", "TYPE_PREVIEW");
  put_text(e, values, "display_name", name);
  put_text(e, values, "app_link_intent_uri", link);
  let row = insert(ctx, e, CHANNELS, values);
  if (row == 0 as usize) {
    return -1i64;
  }
  let segment = jni.call_object(e, row, "getLastPathSegment", "()Ljava/lang/String;", []);
  if (failed(e) || segment == 0 as usize) {
    return -1i64;
  }
  let Ok(id) = i64.parse_i64(jni.string_of(e, segment)) else {
    return -1i64;
  };
  request_browsable(ctx, e, id);
  return id;
}

// Gives the channel `channel` the programs `programs`, in their order, in
// place of its programs before. False when a program does not go in, as
// when the person removed the channel.
pub function show_channel(ctx: usize, channel: i64, programs: Program[]): boolean {
  if (!home_rows(ctx)) {
    return false;
  }
  let e = android.env(ctx);
  if (jni.call1(e, 19, 16 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  delete_where(ctx, e, PREVIEWS, "channel_id=?", channel.to_string());
  pop_frame(e);
  for p in programs {
    if (!add_program(ctx, e, channel, p)) {
      return false;
    }
  }
  return true;
}

// Adds the program `p` to the channel `channel`.
function add_program(ctx: usize, e: usize, channel: i64, p: Program): boolean {
  if (jni.call1(e, 19, 32 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  defer pop_frame(e);
  let values = new_values(e);
  if (values == 0 as usize) {
    return false;
  }
  put_number(e, values, "channel_id", channel);
  put_number(e, values, "type", p.kind as i64);
  put_text(e, values, "title", p.title);
  put_text(e, values, "internal_provider_id", p.id);
  put_text(e, values, "intent_uri", p.link);
  if (p.poster.len() > 0) {
    put_text(e, values, "poster_art_uri", p.poster);
    put_number(e, values, "poster_art_aspect_ratio", POSTER as i64);
  }
  if (p.duration_ms > 0i64) {
    put_number(e, values, "duration_millis", p.duration_ms);
  }
  return insert(ctx, e, PREVIEWS, values) != 0 as usize;
}

function request_browsable(ctx: usize, e: usize, channel: i64): void {
  let class = jni.find_class(e, "android/media/tv/TvContract");
  if (failed(e) || class == 0 as usize) {
    return;
  }
  let id = jni.get_static_method_id(e, class, "requestChannelBrowsable", "(Landroid/content/Context;J)V");
  if (failed(e) || id == 0 as usize) {
    return;
  }
  let args = jni.jvalues([android.activity_object(ctx), channel as usize]);
  let done = jni.call_static_void_method_a(e, class, id, args);
  jni.free_buffer(args);
  let cleared = failed(e);
}
```

### `rows`

A device has the rows of Android TV when it has the feature `android.software.leanback`, from API level 26.

```fern
<<rows>>=
// True when the device has the rows of the home screen of Android TV.
pub function home_rows(ctx: usize): boolean {
  if (android.sdk_version(ctx) < 26) {
    return false;
  }
  let e = android.env(ctx);
  if (jni.call1(e, 19, 8 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  defer pop_frame(e);
  let manager = jni.call_object(e, android.activity_object(ctx), "getPackageManager", "()Landroid/content/pm/PackageManager;", []);
  if (failed(e) || manager == 0 as usize) {
    return false;
  }
  let id = jni.get_method_id(e, jni.get_object_class(e, manager), "hasSystemFeature", "(Ljava/lang/String;)Z");
  if (failed(e) || id == 0 as usize) {
    return false;
  }
  let args = jni.jvalues([jni.new_string(e, "android.software.leanback")]);
  let has = jni.call_boolean_method_a(e, manager, id, args);
  jni.free_buffer(args);
  return !failed(e) && (has & 255) != 0;
}

// A new empty ContentValues, or 0.
function new_values(e: usize): usize {
  let class = jni.find_class(e, "android/content/ContentValues");
  if (failed(e) || class == 0 as usize) {
    return 0 as usize;
  }
  let none = jni.jvalues([]);
  let values = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  if (failed(e)) {
    return 0 as usize;
  }
  return values;
}

// Inserts `values` at the table of the URI `table`. The URI of the new
// row, or 0.
function insert(ctx: usize, e: usize, table: string, values: usize): usize {
  let resolver = resolver_of(ctx, e);
  let uri = parse_uri(e, table);
  if (resolver == 0 as usize || uri == 0 as usize) {
    return 0 as usize;
  }
  let row = jni.call_object(e, resolver, "insert", "(Landroid/net/Uri;Landroid/content/ContentValues;)Landroid/net/Uri;", [uri, values]);
  if (failed(e)) {
    return 0 as usize;
  }
  return row;
}

// Deletes the rows of the app at the table of the URI `table` where
// `column`, such as "channel_id=?", has the value `value`.
function delete_where(ctx: usize, e: usize, table: string, column: string, value: string): void {
  let resolver = resolver_of(ctx, e);
  let uri = parse_uri(e, table);
  let where = jni.new_string(e, column);
  let text = jni.new_string(e, value);
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
  let call_args = jni.jvalues([uri, where, args]);
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
