# Mosaic: shared pixels over WebSocket

Mosaic sends strokes and revision updates through a WebSocket.
The browser displays a stroke immediately. The server validates it and pushes the change to every subscriber.
A bounded revision journal also supports the HTTP mutation and resumable event APIs.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Board data>>
<<Create a board>>
<<Commit a stroke>>
<<Resume the event log>>
<<Push changes over WebSocket>>
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
import "./live";
import "std/http/websocket";

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

pub struct State { rooms: Cell[Board[]], live: live.State }
```

## Create a board

Room lookup validates the identifier before it creates state. The initial image uses only palette indices.
Board indices remain stable because this example does not evict boards.

```fern
<<Create a board>>=
pub function state(): State {
  return State { rooms: cell_new([] as Board[]), live: live.state() };
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
  live.notify(room.state.live, board.name, "");
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

## Push changes over WebSocket

The reader accepts strokes and acknowledges each sequence number. It does not wait for earlier acknowledgements.
A separate producer pushes journal changes as soon as a publication wakes its subscription.
Both writers share the connection output queue. The WebSocket flush method serializes their writes.

```fern
<<Push changes over WebSocket>>=
@derive(json.FromJson)
struct Input { kind: string, seq: i64, color: i32, points: i32[] }

@derive(json.Json)
struct Ack { kind: string, seq: i64, revision: i64, server_us: i64 }

@derive(json.Json)
struct Failure { kind: string, seq: i64, message: string }

function send_pending(room: Room, cursor: Cell[Option[i64]], connection: websocket.Conn): void {
  while (true) {
    let Some(event) = changes(room, cursor.get()) else {
      return;
    };
    let sent = connection.send_text("{\"kind\":\"" + event.name + "\",\"data\":" + event.data + "}");
    if let websocket.Dropped = sent {
      connection.close(1013, "canvas receiver is too slow");
      return;
    }
    cursor.set(parse.int64(event.id).ok());
  }
}

pub function socket(state: State, req: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let room: Room = room(state, common.query(req, "room", "commons"))?;
  let subscription: live.Subscription = live.subscribe(state.live, state.rooms.get()[room.at].name)?;
  let cursor: Cell[Option[i64]] = cell_new(None as Option[i64]);
  let handlers: websocket.Handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {
      send_pending(room, cursor, connection);
      live.wake(subscription);
    },
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let started: i64 = now_ns();
      match (message) {
        Text(text) => {
          let Ok(input) = Input.from_json(text) else {
            connection.close(1008, "invalid canvas input");
            return;
          };
          if (input.seq < 0 || input.kind != "paint" && input.kind != "ping") {
            connection.close(1008, "unknown canvas command");
            return;
          }
          let kind: string = "pong";
          if (input.kind == "paint") {
            match (paint(room, Stroke { color: input.color, points: input.points })) {
              Ok(_) => {
                kind = "ack";
              },
              Err(error) => {
                let sent = connection.send_text(Failure { kind: "error", seq: input.seq, message: error.detail }.to_json());
                return;
              }
            }
          }
          let sent = connection.send_text(Ack { kind: kind, seq: input.seq, revision: snapshot(room).revision, server_us: (now_ns() - started) / 1000 }.to_json());
          send_pending(room, cursor, connection);
        },
        Binary(_) => {
          connection.close(1003, "send JSON canvas commands");
        }
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => live.cancel(subscription),
  };
  let options: websocket.Options = websocket.Options { ...websocket.options(), max_message: 4096, backpressure: 32768, max_backpressure: 131072 };
  match (websocket.accept_with_push(req, options, handlers, (connection: websocket.Conn): void => {
    while (live.wait(subscription)) {
      send_pending(room, cursor, connection);
      match (connection.flush()) {
        Dropped => {
          break;
        },
        _ => {}
      }
    }
    live.dispose(subscription);
  })) {
    Ok(response) => {
      return Ok(response);
    },
    Err(error) => {
      live.dispose(subscription);
      return Err(error);
    }
  }
}
```
