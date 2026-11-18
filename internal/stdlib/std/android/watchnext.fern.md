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
import "std/android/watchnext/bindings";
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
  let Some(tv) = open(ctx, e, 64) else {
    return false;
  };
  defer close(e, tv);
  delete_where(e, tv, URI, "internal_provider_id=?", p.id);
  let Some(values) = new_values(e, tv) else {
    return false;
  };
  put_text(e, tv, values, "title", p.title);
  put_text(e, tv, values, "internal_provider_id", p.id);
  put_text(e, tv, values, "intent_uri", p.link);
  if (p.poster.len() > 0) {
    put_text(e, tv, values, "poster_art_uri", p.poster);
  }
  put_number(e, tv, values, "type", p.kind as i64);
  put_number(e, tv, values, "watch_next_type", CONTINUE as i64);
  put_number(e, tv, values, "last_playback_position_millis", p.position_ms);
  put_number(e, tv, values, "duration_millis", p.duration_ms);
  put_number(e, tv, values, "last_engagement_time_utc_millis", clock.now().unix_millis());
  return insert(e, tv, URI, values).is_some();
}

// The intent URI that starts the NativeActivity of the app with the string
// extra `key` set to `value`. The extra must not hold ";".
pub function app_link(ctx: usize, key: string, value: string): string {
  let e = android.env(ctx);
  let Some(tv) = open(ctx, e, 8) else {
    return "";
  };
  defer close(e, tv);
  let Ok(package) = tv.c.context.get_package_name(e, tv.app) else {
    return "";
  };
  return "intent:#Intent;component=" + package + "/android.app.NativeActivity;S." + key + "=" + value + ";end";
}

// Takes the program with the ID `id` away.
pub function drop(ctx: usize, id: string): void {
  if (android.sdk_version(ctx) < 26) {
    return;
  }
  let e = android.env(ctx);
  let Some(tv) = open(ctx, e, 16) else {
    return;
  };
  defer close(e, tv);
  delete_where(e, tv, URI, "internal_provider_id=?", id);
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
  let Some(tv) = open(ctx, e, 32) else {
    return -1i64;
  };
  defer close(e, tv);
  let Some(values) = new_values(e, tv) else {
    return -1i64;
  };
  put_text(e, tv, values, "type", "TYPE_PREVIEW");
  put_text(e, tv, values, "display_name", name);
  put_text(e, tv, values, "app_link_intent_uri", link);
  let Some(row) = insert(e, tv, CHANNELS, values) else {
    return -1i64;
  };
  let Ok(segment) = tv.c.uri.get_last_path_segment(e, row) else {
    return -1i64;
  };
  let Ok(id) = i64.parse_i64(segment) else {
    return -1i64;
  };
  let asked = tv.c.tv_contract.request_channel_browsable(e, tv.app, id);
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
  let Some(tv) = open(ctx, e, 16) else {
    return false;
  };
  defer close(e, tv);
  delete_where(e, tv, PREVIEWS, "channel_id=?", channel.to_string());
  for p in programs {
    if (!add_program(e, tv, channel, p)) {
      return false;
    }
  }
  return true;
}

// Adds the program `p` to the channel `channel`, in a local frame of its
// own.
function add_program(e: usize, tv: Tv, channel: i64, p: Program): boolean {
  if (!push_frame(e, 32)) {
    return false;
  }
  defer pop_frame(e);
  let Some(values) = new_values(e, tv) else {
    return false;
  };
  put_number(e, tv, values, "channel_id", channel);
  put_number(e, tv, values, "type", p.kind as i64);
  put_text(e, tv, values, "title", p.title);
  put_text(e, tv, values, "internal_provider_id", p.id);
  put_text(e, tv, values, "intent_uri", p.link);
  if (p.poster.len() > 0) {
    put_text(e, tv, values, "poster_art_uri", p.poster);
    put_number(e, tv, values, "poster_art_aspect_ratio", POSTER as i64);
  }
  if (p.duration_ms > 0i64) {
    put_number(e, tv, values, "duration_millis", p.duration_ms);
  }
  return insert(e, tv, PREVIEWS, values).is_some();
}
```

### `rows`

A device has the rows of Android TV when it has the feature `android.software.leanback`, from API level 26.

Each call goes through the typed wrappers of `std/android/watchnext/bindings`, which `std/jni/bind` wrote from `bindings.spec`. `open` finds the classes, the context of the activity and its content resolver in a local frame, and `close` lets them go.

```fern
<<rows>>=
// True when the device has the rows of the home screen of Android TV.
pub function home_rows(ctx: usize): boolean {
  if (android.sdk_version(ctx) < 26) {
    return false;
  }
  let e = android.env(ctx);
  let Some(tv) = open(ctx, e, 8) else {
    return false;
  };
  defer close(e, tv);
  let Ok(manager) = tv.c.context.get_package_manager(e, tv.app) else {
    return false;
  };
  let Ok(has) = tv.c.package_manager.has_system_feature(e, manager, "android.software.leanback") else {
    return false;
  };
  return has;
}

// The classes of the calls, the context of the activity, and its content
// resolver.
struct Tv { c: bindings.Classes, app: bindings.Context, resolver: bindings.ContentResolver }

// The classes and the resolver, in a new local frame of `refs` references.
// None when Java refuses one of them.
function open(ctx: usize, e: usize, refs: i32): Option[Tv] {
  if (!push_frame(e, refs)) {
    return None;
  }
  let Ok(c) = bindings.classes(e) else {
    pop_frame(e);
    return None;
  };
  let app = bindings.Context { ref: android.activity_object(ctx) };
  let Ok(resolver) = c.context.get_content_resolver(e, app) else {
    c.release(e);
    pop_frame(e);
    return None;
  };
  return Some(Tv { c: c, app: app, resolver: resolver });
}

function close(e: usize, tv: Tv): void {
  tv.c.release(e);
  pop_frame(e);
}

// A new empty ContentValues.
function new_values(e: usize, tv: Tv): Option[bindings.ContentValues] {
  let Ok(values) = tv.c.content_values.new(e) else {
    return None;
  };
  return Some(values);
}

// Inserts `values` at the table of the URI `table`. The URI of the new row.
function insert(e: usize, tv: Tv, table: string, values: bindings.ContentValues): Option[bindings.Uri] {
  let Ok(uri) = tv.c.uri.parse(e, table) else {
    return None;
  };
  let Ok(row) = tv.c.content_resolver.insert(e, tv.resolver, uri, values) else {
    return None;
  };
  if (row.is_null()) {
    return None;
  }
  return Some(row);
}

// Deletes the rows of the app at the table of the URI `table` where
// `column`, such as "channel_id=?", has the value `value`.
function delete_where(e: usize, tv: Tv, table: string, column: string, value: string): void {
  let Ok(uri) = tv.c.uri.parse(e, table) else {
    return;
  };
  let args = jni.string_array(e, [value]);
  if (args == 0 as usize) {
    let cleared = jni.exception_clear(e);
    return;
  }
  let deleted = tv.c.content_resolver.delete(e, tv.resolver, uri, column, jni.Object { ref: args });
}

function put_text(e: usize, tv: Tv, values: bindings.ContentValues, key: string, value: string): void {
  let put = tv.c.content_values.put_string_string(e, values, key, value);
}

function put_number(e: usize, tv: Tv, values: bindings.ContentValues, key: string, value: i64): void {
  let Ok(boxed) = tv.c.long.value_of(e, value) else {
    return;
  };
  let put = tv.c.content_values.put_string_long(e, values, key, boxed);
}

// Starts a local frame of `refs` references. False when Java refuses it.
function push_frame(e: usize, refs: i32): boolean {
  if (jni.call1(e, 19, refs as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  return true;
}

// Ends the local frame of references that a call started.
function pop_frame(e: usize): void {
  let popped = jni.call1(e, 20, 0 as usize);
}
```
