# Android media browsing

This module lets a media browser, such as Android Auto, browse and play the media of a NativeActivity app of `std/android`.

## Model

The app declares a @jni class that extends `android.service.media.MediaBrowserService`, with three native methods: `onGetRoot`, `onLoadChildren` and `onDestroy`. They call `root`, `children` and `destroyed` of this module. `fern -apk` declares that service with the intent filter of a media browser, and it marks the app as a media app of Android Auto.

A browser can bind the service while no activity of the app runs. So `root` starts the loop of the app with no activity, or holds the live loop. Then it sends `Connected` with the service. The app gives the service the token of its media session with `connect`. Android Auto shows the app only after it has the token.

`children` runs when a browser asks for the items under an item. It detaches the result, so the answer can come later, and sends `Asked` with the request. The loop reads the id of the parent with `parent`, and it gives the items with `answer`. Each request gets one answer. The items under the root have the parent `ROOT`.

An item has an id, a title and a subtitle. A browser opens an item that is not playable, and it plays a playable item. To play, it calls the `onPlayFromMediaId` of the media session. That native method of the app's callback calls `play_from`, which sends `PlayFrom`, and `media_id` gives its id.

`destroyed` runs when the last browser goes. It lets the loop go, and then it calls the `onDestroy` of `MediaBrowserService`.

## Contracts

`root`, `children`, `destroyed` and `play_from` run on the UI thread, in the native methods of the app. Call each other function on the loop thread. Answer each `Asked` request one time, and read each `PlayFrom` id one time: each one holds a global reference until then.

The kinds 39 to 41 of `Posted` events belong to this module.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<the service>>

<<the loop>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module lets a media browser, such as Android Auto, browse the media
// of the app.
import "std/android";
import "std/android/browse/bindings";
import "std/jni";
import "std/log";
```

### `events`

Each event carries a global reference as its argument. `Connected` holds the service, `Asked` holds an `Object[]` of the id of the parent and the result, and `PlayFrom` holds the id to play.

```fern
<<events>>=
const CONNECTED: i32 = 39;

const ASKED: i32 = 40;

const PLAY_FROM: i32 = 41;

// The id of the root of the items.
pub const ROOT: string = "root";

// MediaBrowser.MediaItem.FLAG_BROWSABLE and FLAG_PLAYABLE.
const BROWSABLE: i32 = 1;

const PLAYABLE: i32 = 2;

// A request of a browser for the items under one item.
pub struct Request { held: usize }

// The media browser service of the app.
pub struct Service { held: usize }

// The id of an item that a browser plays.
pub struct MediaId { held: usize }

pub enum Event { Connected(Service), Asked(Request), PlayFrom(MediaId) }

// The event of this module in the event `e` of the loop, or None.
pub function event(e: android.Event): Option[Event] {
  let android.Posted(kind, arg) = e else {
    return None;
  };
  if (kind == CONNECTED) {
    return Some(Connected(Service { held: arg }));
  }
  if (kind == ASKED) {
    return Some(Asked(Request { held: arg }));
  }
  if (kind == PLAY_FROM) {
    return Some(PlayFrom(MediaId { held: arg }));
  }
  return None;
}

// An item that a browser shows. A playable item plays, and an item that
// is not playable opens to the items under it.
pub struct Item { id: string, title: string, subtitle: string, playable: boolean }
```

### `the service`

A call that no loop takes deletes the reference that it made. A request that no loop takes gets an empty answer at once, so the browser does not wait.

```fern
<<the service>>=
// Starts a local frame, or is false when an exception waits or the frame
// cannot start.
function begin(e: usize): boolean {
  if (e == 0 as usize) {
    return false;
  }
  let pending = jni.exception_occurred(e);
  if (pending != 0 as usize) {
    jni.delete_local_ref(e, pending);
    return false;
  }
  if (!jni.push_local_frame(e, 64)) {
    return false;
  }
  return true;
}

function end(e: usize): void {
  jni.pop_local_frame(e, 0 as usize);
}

// The onGetRoot of the service: a browser connects. `thread_main` is the
// entry of the loop of the app, as `launch.start` takes it. It gives the
// root, or 0.
pub function root(e: usize, service: usize, thread_main: usize): usize {
  android.start_alone(e, service, thread_main);
  let held = jni.new_global_ref(e, service);
  if (!android.tell(e, CONNECTED, held)) {
    let dropped: usize = jni.delete_global_ref(e, held);
  }
  let Ok(c) = bindings.media_browser_service_browser_root_class(e) else {
    return 0 as usize;
  };
  defer c.release(e);
  let Ok(r) = c.new(e, ROOT, jni.Object { ref: 0 as usize }) else {
    return 0 as usize;
  };
  return r.ref;
}

// The onLoadChildren of the service, with the id of the parent and the
// result.
pub function children(e: usize, service: usize, parent: usize, result: usize): void {
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let Ok(c) = bindings.media_browser_service_result_class(e) else {
    return;
  };
  defer c.release(e);
  if (c.detach(e, bindings.MediaBrowserService_Result { ref: result }).is_err()) {
    return;
  }
  let object = jni.find_class(e, "java/lang/Object");
  let pair = jni.new_object_array(e, 2, object, 0 as usize);
  if (jni.check(e).is_err() || pair == 0 as usize) {
    return;
  }
  jni.set_object_array_element(e, pair, 0, parent);
  jni.set_object_array_element(e, pair, 1, result);
  let held = jni.new_global_ref(e, pair);
  if (!android.tell(e, ASKED, held)) {
    let sent = send(e, Request { held: held }, []);
  }
}

// The onPlayFromMediaId of the app's callback of its media session.
pub function play_from(e: usize, id: usize): void {
  let held = jni.new_global_ref(e, id);
  if (!android.tell(e, PLAY_FROM, held)) {
    let dropped: usize = jni.delete_global_ref(e, held);
  }
}

// The onDestroy of the service: it lets the loop go, and calls the
// onDestroy of MediaBrowserService.
pub function destroyed(e: usize, service: usize): void {
  android.hold_loop(e, false);
  let class = jni.find_class(e, "android/service/media/MediaBrowserService");
  if (jni.check(e).is_err()) {
    return;
  }
  let done: usize = jni.call_nonvirtual_void_method_a(e, service, class, jni.get_method_id(e, class, "onDestroy", "()V"), 0 as usize);
  let checked = jni.check(e).is_ok();
  let dropped: usize = jni.delete_local_ref(e, class);
}
```

### `the loop`

`send` builds a `MediaItem` for each item, sends the list, and deletes the reference of the request.

```fern
<<the loop>>=
// Gives the service the token of the media session, and lets the service
// go. A browser that waits connects then.
pub function connect(ctx: usize, s: Service, token: usize): boolean {
  let e = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  defer jni.delete_global_ref(e, s.held);
  let Ok(c) = bindings.media_browser_service_class(e) else {
    return false;
  };
  defer c.release(e);
  return c.set_session_token(e, bindings.MediaBrowserService { ref: s.held }, bindings.MediaSession_Token { ref: token }).is_ok();
}

// The id of the item that the request `r` asks under.
pub function parent(ctx: usize, r: Request): string {
  let e = android.env(ctx);
  if (!begin(e)) {
    return "";
  }
  defer end(e);
  let id = jni.get_object_array_element(e, r.held, 0);
  if (jni.check(e).is_err()) {
    return "";
  }
  return jni.string_of(e, id);
}

// Answers the request `r` with `items`. It is false when the browser did
// not get them.
pub function answer(ctx: usize, r: Request, items: Item[]): boolean {
  let e = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  return send(e, r, items);
}

function send(e: usize, r: Request, items: Item[]): boolean {
  defer jni.delete_global_ref(e, r.held);
  match (bindings.classes(e)) {
    Ok(c) => {
      let sent = sent_list(e, c, r, items);
      c.release(e);
      if let Err(err) = sent {
        log.warn("browse: no answer: " + err.message());
        return false;
      }
      return true;
    },
    Err(err) => {
      log.warn("browse: no classes: " + err.message());
      return false;
    }
  }
}

function sent_list(e: usize, c: bindings.Classes, r: Request, items: Item[]): Result[void, jni.JavaError] {
  let list = list_of(e, c, items)?;
  let result = jni.get_object_array_element(e, r.held, 1);
  jni.check(e)?;
  return c.media_browser_service_result.send_result(e, bindings.MediaBrowserService_Result { ref: result }, list.object());
}

function list_of(e: usize, c: bindings.Classes, items: Item[]): Result[bindings.ArrayList, jni.JavaError] {
  let list = c.array_list.new(e)?;
  for it in items {
    let item = media_item(e, c, it)?;
    let added = c.array_list.add(e, list, item.object())?;
    let dropped: usize = jni.delete_local_ref(e, item.ref);
  }
  return Ok(list);
}

function media_item(e: usize, c: bindings.Classes, it: Item): Result[bindings.MediaBrowser_MediaItem, jni.JavaError] {
  let b = c.media_description_builder.new(e)?;
  let with_id = c.media_description_builder.set_media_id(e, b, it.id)?;
  let with_title = c.media_description_builder.set_title(e, with_id, jni.Object { ref: jni.new_string(e, it.title) })?;
  let with_subtitle = c.media_description_builder.set_subtitle(e, with_title, jni.Object { ref: jni.new_string(e, it.subtitle) })?;
  let d = c.media_description_builder.build(e, with_subtitle)?;
  let flags = if (it.playable) { PLAYABLE } else { BROWSABLE };
  return c.media_browser_media_item.new(e, d, flags);
}

// The id of the item to play, which deletes the reference.
pub function media_id(ctx: usize, m: MediaId): string {
  let e = android.env(ctx);
  defer jni.delete_global_ref(e, m.held);
  return jni.string_of(e, m.held);
}
```
