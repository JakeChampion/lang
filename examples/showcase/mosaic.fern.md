# Mosaic: shared pixels and resumable events

Mosaic combines ordinary POST requests with server-sent events.
One connection can receive changes from every painter without a WebSocket protocol.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Board data>>
<<Create a board>>
<<Commit a stroke>>
<<Resume the event log>>
<<Export a real image>>
```

## Board data

A board has 2560 palette indices and a monotonic revision. The server keeps at most 32 boards.
JSON records distinguish a complete snapshot from a small paint change.

```fern
<<Board data>>=
// Shared pixel boards use POST mutations and a resumable server-sent event log.
import "std/http";
import "std/result";
import "std/option";
import "std/array";
import "std/http/sse";
import "std/json";
import "std/parse";
import "std/png";
import "std/time";
import "std/async";
import "./common";

pub const WIDTH: i32 = 64;

pub const HEIGHT: i32 = 40;

pub const COLORS: i32[] = [0xf4f1e9, 0x183e32, 0x397455, 0x83ab68, 0xc6d9a0, 0xf2c85a, 0xe78b55, 0xc75656, 0x8c639e, 0x5b80ac, 0x8fbcc5, 0x262a31];

@derive(json.Json, json.FromJson)
pub struct Stroke { color: i32, points: i32[] }

@derive(json.Json)
pub struct Change { revision: i64, color: i32, points: i32[] }

@derive(json.Json)
pub struct Snapshot { revision: i64, width: i32, height: i32, pixels: i32[], palette: i32[] }

pub struct Board { name: string, pixels: i32[], revision: i64, history: Change[] }

pub struct Room { state: State, at: i32 }

pub struct State { rooms: Cell[Board[]] }
```

## Create a board

Room lookup validates the identifier before it creates state. The initial image uses only palette indices.
Board indices remain stable because this example does not evict boards.

```fern
<<Create a board>>=
pub function state(): State {
  return State { rooms: cell_new([] as Board[]) };
}

pub function room(state: State, name: string): Result[Room, http.HttpError] {
  if (!common.room_name(name)) {
    return Err(http.fail(400, "room names use 1 to 32 lowercase letters, numbers, dashes, or underscores"));
  }
  let index: i32 = 0;
  for found in state.rooms.get() {
    if (found.name == name) {
      return Ok(Room { state: state, at: index });
    }
    index = index + 1;
  }
  if (state.rooms.get().len() >= 32) {
    return Err(http.fail(429, "all canvas rooms are in use"));
  }
  let pixels: i32[] = __alloc_i32(WIDTH * HEIGHT);
  let y: i32 = 7;
  while (y < 33) {
    let x: i32 = 17;
    while (x < 47) {
      let dx: i32 = x - 32;
      let dy: i32 = y - 20;
      if (dx * dx * 2 + dy * dy < 220 && (x + y) % 5 != 0) {
        pixels = pixels.with(y * WIDTH + x, 2 + (x + y) % 3);
      }
      x = x + 1;
    }
    y = y + 1;
  }
  let at: i32 = state.rooms.get().len();
  state.rooms.set(state.rooms.get().append(Board { name: name, pixels: pixels, revision: 0, history: [] }));
  return Ok(Room { state: state, at: at });
}
```

## Commit a stroke

Validate every pixel before a write so a bad stroke cannot partly change the board.
One accepted stroke increments the revision once. Retain the last 256 changes for reconnects.
The browser draws optimistically, then reconciles its pixels with server events.

```fern
<<Commit a stroke>>=
pub function snapshot(room: Room): Snapshot {
  let board: Board = room.state.rooms.get()[room.at];
  return Snapshot { revision: board.revision, width: WIDTH, height: HEIGHT, pixels: board.pixels, palette: COLORS };
}

pub function paint(room: Room, stroke: Stroke): Result[Change, http.HttpError] {
  if (stroke.color < 0 || stroke.color >= COLORS.len() || stroke.points.len() < 1 || stroke.points.len() > 64) {
    return Err(http.fail(400, "a stroke needs 1 to 64 pixels and a palette color"));
  }
  for point in stroke.points {
    if (point < 0 || point >= WIDTH * HEIGHT) {
      return Err(http.fail(400, "pixel is outside the board"));
    }
  }
  let board: Board = room.state.rooms.get()[room.at];
  let pixels: i32[] = board.pixels;
  for point in stroke.points {
    pixels = pixels.with(point, stroke.color);
  }
  let change: Change = Change { revision: board.revision + 1, color: stroke.color, points: stroke.points };
  let history: Change[] = board.history.append(change);
  if (history.len() > 256) {
    history = history.drop(1);
  }
  room.state.rooms.set(room.state.rooms.get().with(room.at, Board { ...board, pixels: pixels, revision: change.revision, history: history }));
  return Ok(change);
}
```

## Resume the event log

`Last-Event-ID` identifies the last applied revision. A missing or malformed cursor becomes `None`.
Send a full snapshot when the cursor is outside the retained journal. Otherwise send the next change.
The producer yields between checks and exits on task cancellation. SSE heartbeats keep idle streams active.

```fern
<<Resume the event log>>=
pub function changes(room: Room, after: Option[i64]): Option[sse.Event] {
  let board: Board = room.state.rooms.get()[room.at];
  let Some(cursor) = after else {
    return Some(sse.event(snapshot(room).to_json()).with_name("snapshot").with_id(board.revision.to_string()).with_retry(1000));
  };
  if (cursor < 0 || cursor > board.revision || cursor < board.revision - board.history.len() as i64) {
    return Some(sse.event(snapshot(room).to_json()).with_name("snapshot").with_id(board.revision.to_string()).with_retry(1000));
  }
  for change in board.history {
    if (change.revision > cursor) {
      return Some(sse.event(change.to_json()).with_name("paint").with_id(change.revision.to_string()));
    }
  }
  return None;
}

pub function events(room: Room, req: HttpRequest): HttpResponse {
  let cursor: Cell[Option[i64]] = cell_new(parse.int64(sse.last_event_id(req).unwrap_or("")).ok());
  return sse.respond_with(time.duration_seconds(15), (index: i32): Option[sse.Event] => {
    while (!async.task_cancelled()) {
      match (changes(room, cursor.get())) {
        Some(event) => {
          cursor.set(parse.int64(event.id).ok());
          return Some(event);
        },
        None => {}
      }
      let ready: i32 = async.wait_any([], 100);
    }
    return None;
  }).with_header("X-Accel-Buffering", "no");
}
```

## Export a real image

Map palette indices to RGB bytes and call the standard PNG encoder.
The response supplies an attachment name so the browser can save the current board.

```fern
<<Export a real image>>=
pub function export_png(room: Room): Result[HttpResponse, http.HttpError] {
  let pixels: i32[] = room.state.rooms.get()[room.at].pixels;
  let rgb: u8[] = __alloc_u8(WIDTH * HEIGHT * 3);
  let at: i32 = 0;
  while (at < pixels.len()) {
    let color: i32 = COLORS[pixels[at]];
    rgb = rgb.with(at * 3, (color >> 16 & 255) as u8).with(at * 3 + 1, (color >> 8 & 255) as u8).with(at * 3 + 2, (color & 255) as u8);
    at = at + 1;
  }
  let bytes: u8[] = png.encode_rgb8(WIDTH, HEIGHT, rgb).map_err((e: png.EncodeError): http.HttpError => http.fail(500, "image encoder failed"))?;
  return Ok(http.bytes(200, bytes).with_content_type("image/png").with_header("Content-Disposition", "attachment; filename=fern-mosaic.png").with_header("Cache-Control", "no-store"));
}
```

## Check the behavior

This example compiles against the same sections as the application. Run it with `fern -doctest`.

```fern test name=mosaic-example
<<Board data>>
<<Create a board>>
<<Commit a stroke>>
function main(): i32 {
  let Ok(board) = room(state(), "lesson") else { return 1; };
  if (paint(board, Stroke { color: 3, points: [0, 2560] }).is_ok()) { return 1; }
  if (snapshot(board).revision != 0) { return 2; }
  let Ok(change) = paint(board, Stroke { color: 3, points: [0] }) else { return 4; };
  if (change.revision != 1 || snapshot(board).pixels[0] != 3) { return 3; }
  return 0;
}
```
