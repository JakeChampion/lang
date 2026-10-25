# Shared studio projects

Foundry, Patchbay, and Circuit share a bounded project protocol.
The native server validates every edit before it publishes a revision.
Each connection receives a snapshot, then small changes. A reconnect receives the current state.

Projects use process-local memory. Visitors can export their projects before a server restart.
A project has at most 65536 integers. The server retains at most 24 projects and 128 changes per project.

```fern
<<*>>=
<<Project protocol>>
```

## Project protocol

The wake mechanism sends changes without a timer poll. Slow receivers close rather than retain an unbounded queue.
The reader and producer share the WebSocket output queue. Only the producer disposes its notification descriptor.

```fern
<<Project protocol>>=
import "std/http";
import "std/http/websocket";
import "std/json";
import "std/array";
import "std/result";
import "std/option";
import "./common";
import "./live";
import "./foundry";

@derive(json.Json)
pub struct Change { kind: string, revision: i64, points: i32[], values: i32[] }

@derive(json.Json)
pub struct Snapshot { kind: string, revision: i64, cells: i32[] }

@derive(json.FromJson)
struct Input { kind: string, seq: i64, points: i32[], values: i32[] }

@derive(json.Json)
struct Ack { kind: string, seq: i64, revision: i64, server_us: i64 }

pub struct Project { name: string, revision: i64, cells: i32[], history: Change[], factory: foundry.Factory, updated: i64, running: boolean, packet: u8[], step_us: i32 }

pub struct State { projects: Cell[Project[]], live: live.State }

pub struct Room { state: State, at: i32 }

pub function state(): State {
  return State { projects: cell_new([] as Project[]), live: live.state() };
}

pub function room(state: State, app: string, name: string): Result[Room, http.HttpError] {
  if (!common.room_name(name) || app != "foundry" && app != "patchbay" && app != "circuit") {
    return Err(http.fail(400, "Invalid project name."));
  }
  let key = app + "/" + name;
  let at = 0;
  for project in state.projects.get() {
    if (project.name == key) {
      return Ok(Room { state: state, at: at });
    }
    at = at + 1;
  }
  if (at >= 24) {
    return Err(http.fail(429, "All project rooms are in use."));
  }
  let size = 65536;
  if (app == "patchbay") {
    size = 512;
  }
  let cells: i32[] = __alloc_i32(size);
  if (app == "foundry") {
    let i = 0;
    while (i < 30) {
      cells = cells.with(2570 + i, 2);
      i = i + 1;
    }
    cells = cells.with(2570, 1).with(2599, 6);
  }
  if (app == "patchbay") {
    let i = 0;
    while (i < 16) {
      cells = cells.with(i * 8 + i % 4 * 2, 1);
      i = i + 1;
    }
  }
  if (app == "patchbay") {
    cells = cells.with(128, 110).with(130, 6000).with(131, 25).with(132, 5);
  }
  if (app == "circuit") {
    cells = cells.with(0, 9).with(3, 8).with(6, 5).with(7, 0).with(8, 1).with(9, 3).with(10, 0).with(11, 1);
  }
  let factory_size = 0;
  if (app == "foundry") {
    factory_size = size;
  }
  state.projects.set(state.projects.get().append(Project { name: key, revision: 0, cells: cells, history: [], factory: foundry.factory(factory_size), updated: monotonic_ns(), running: true, packet: [], step_us: 0 }));
  return Ok(Room { state: state, at: at });
}

pub function snapshot(room: Room): Snapshot {
  let p = room.state.projects.get()[room.at];
  return Snapshot { kind: "snapshot", revision: p.revision, cells: p.cells };
}

pub function edit(room: Room, points: i32[], values: i32[]): Result[i64, http.HttpError] {
  let p = room.state.projects.get()[room.at];
  if (points.len() == 0 || points.len() > 128 || points.len() != values.len()) {
    return Err(http.fail(400, "Send one to 128 cell edits."));
  }
  for point in points {
    if (point < 0 || point >= p.cells.len()) {
      return Err(http.fail(400, "Cell is outside this project."));
    }
  }
  let changed: i32[] = [];
  let check = 0;
  while (check < points.len()) {
    if (!valid_edit(p.name, points[check], values[check])) {
      return Err(http.fail(400, "Invalid cell value for this project."));
    }
    if (p.cells[points[check]] != values[check]) {
      changed = changed.append(points[check]);
    }
    check = check + 1;
  }
  if (changed.len() == 0) {
    return Ok(p.revision);
  }
  let i = 0;
  while (i < points.len()) {
    p = Project { ...p, cells: p.cells.with(points[i], values[i]) };
    i = i + 1;
  }
  let event = Change { kind: "change", revision: p.revision + 1, points: points, values: values };
  let history = p.history.append(event);
  if (history.len() > 128) {
    history = history.drop(history.len() - 128);
  }
  if (p.factory.cargo.len() > 0) {
    p = Project { ...p, factory: foundry.clear(p.factory, changed), packet: [] };
  }
  p = Project { ...p, revision: event.revision, history: history };
  room.state.projects.set(room.state.projects.get().with(room.at, p));
  live.notify(room.state.live, p.name, "");
  return Ok(p.revision);
}

function pending(room: Room, cursor: Cell[i64], connection: websocket.Conn): void {
  let p = room.state.projects.get()[room.at];
  if (cursor.get() == p.revision) {
    return;
  }
  if (cursor.get() < 0 || p.history.len() == 0 || cursor.get() < p.history[0].revision - 1) {
    if let websocket.Dropped = connection.send_text(snapshot(room).to_json()) {
      connection.close(1013, "Project receiver is too slow.");
      return;
    }
  } else {
    for change in p.history {
      if (change.revision > cursor.get()) {
        if let websocket.Dropped = connection.send_text(change.to_json()) {
          connection.close(1013, "Project receiver is too slow.");
          return;
        }
      }
    }
  }
  cursor.set(p.revision);
}

pub function socket(state: State, app: string, req: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let room = room(state, app, common.query(req, "room", "commons"))?;
  let subscription = live.subscribe(state.live, state.projects.get()[room.at].name)?;
  let cursor = cell_new(0i64 - 1);
  let sequence = cell_new(0i64 - 1);
  let last = cell_new(0i64);
  let factory_cursor = cell_new("");
  let handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => { pending(room, cursor, connection); },
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let started = now_ns();
      match (message) {
        Text(text) => {
          let Ok(input) = Input.from_json(text) else {
            connection.close(1008, "Invalid project command.");
            return;
          };
          if (input.seq <= sequence.get() || started - last.get() < 5000000) {
            connection.close(1008, "Project commands exceed the connection limit.");
            return;
          }
          sequence.set(input.seq);
          last.set(started);
          if (input.kind != "edit") {
            if (app != "foundry" || !control(room, input.kind)) {
              connection.close(1008, "Unknown project command.");
              return;
            }
            let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: state.projects.get()[room.at].revision, server_us: (now_ns() - started) / 1000 }.to_json());
            factory_pending(room, factory_cursor, connection);
            return;
          }
          match (edit(room, input.points, input.values)) {
            Ok(revision) => {
              let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: revision, server_us: (now_ns() - started) / 1000 }.to_json());
              pending(room, cursor, connection);
            },
            Err(error) => {
              connection.close(1008, error.detail);
            }
          }
        },
        Binary(_) => {
          connection.close(1003, "Send a JSON project command.");
        }
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => live.cancel(subscription),
  };
  let period = -1;
  if (app == "foundry") {
    period = 100;
  }
  let options = websocket.Options { ...websocket.options(), max_message: 8192, backpressure: 524288, max_backpressure: 1048576 };
  match (websocket.accept_with_push(req, options, handlers, (connection: websocket.Conn): void => {
    while (live.wait_for(subscription, period)) {
      pending(room, cursor, connection);
      factory_pending(room, factory_cursor, connection);
      if let websocket.Dropped = connection.flush() {
        break;
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

function put32(bytes: u8[], at: i32, value: i32): u8[] {
  let out = bytes;
  for i in 0..4 {
    out = out.with(at + i, (value >> i * 8 & 255) as u8);
  }
  return out;
}

function put64(bytes: u8[], at: i32, value: i64): u8[] {
  let out = bytes;
  for i in 0..8 {
    out = out.with(at + i, (value >> i * 8 & 255i64) as u8);
  }
  return out;
}

function factory_pending(room: Room, cursor: Cell[string], connection: websocket.Conn): void {
  let p = room.state.projects.get()[room.at];
  if (p.factory.cargo.len() == 0) {
    return;
  }
  let now = monotonic_ns();
  let steps = 0;
  let started = now_ns();
  if (now - p.updated > foundry.PERIOD * 8) {
    p = Project { ...p, updated: now - foundry.PERIOD };
  }
  while (p.running && now - p.updated >= foundry.PERIOD && steps < 4) {
    p = Project { ...p, factory: foundry.step(p.cells, p.factory, 256), updated: p.updated + foundry.PERIOD, packet: [] };
    steps = steps + 1;
  }
  if (steps > 0) {
    p = Project { ...p, step_us: ((now_ns() - started) / 1000 / steps).min(2147483647i64) as i32 };
  }
  if (p.packet.len() == 0) {
    let bytes: u8[] = __alloc_u8(64 + p.factory.cargo.len() / 2);
    bytes = bytes.with(0, 70 as u8).with(1, 70 as u8).with(2, 1 as u8);
    bytes = put32(bytes, 4, if (p.running) { 1 } else { 0 });
    bytes = put64(bytes, 8, p.factory.tick);
    bytes = put64(bytes, 16, p.factory.produced);
    bytes = put64(bytes, 24, p.factory.consumed);
    bytes = put64(bytes, 32, p.factory.removed);
    bytes = put32(bytes, 40, p.step_us);
    bytes = put32(bytes, 44, p.revision as i32);
    bytes = put32(bytes, 48, p.factory.cargo.len());
    bytes = put32(bytes, 52, 256);
    let i = 0;
    while (i < p.factory.cargo.len()) {
      bytes = bytes.with(64 + i / 2, (p.factory.cargo[i] | p.factory.cargo[i + 1] << 4) as u8);
      i = i + 2;
    }
    p = Project { ...p, packet: bytes };
  }
  room.state.projects.set(room.state.projects.get().with(room.at, p));
  let stamp = p.factory.tick.to_string() + ":" + p.revision.to_string() + ":" + p.running.to_string();
  if (cursor.get() != stamp) {
    if let websocket.Dropped = connection.send_binary(p.packet) {
      connection.close(1013, "Factory receiver is too slow.");
      return;
    }
    cursor.set(stamp);
  }
}

function control(room: Room, command: string): boolean {
  let p = room.state.projects.get()[room.at];
  if (command == "pause") {
    p = Project { ...p, running: false };
  } else if (command == "resume") {
    p = Project { ...p, running: true, updated: monotonic_ns() };
  } else if (command == "step") {
    p = Project { ...p, running: false, factory: foundry.step(p.cells, p.factory, 256) };
  } else if (command == "reset") {
    p = Project { ...p, factory: foundry.factory(p.cells.len()), updated: monotonic_ns() };
  } else {
    return false;
  }
  room.state.projects.set(room.state.projects.get().with(room.at, Project { ...p, packet: [] }));
  live.notify(room.state.live, p.name, "");
  return true;
}

function valid_edit(name: string, point: i32, value: i32): boolean {
  if (value < 0) {
    return false;
  }
  if (name.starts_with("foundry/")) {
    return value <= 6;
  }
  if (name.starts_with("circuit/")) {
    if (point % 3 == 0) {
      return value <= 9;
    }
    return value <= 21844;
  }
  if (point < 128) {
    return value <= 1;
  }
  if (point == 128) {
    return value >= 40 && value <= 240;
  }
  if (point == 129) {
    return value <= 3;
  }
  if (point == 130) {
    return value >= 100 && value <= 12000;
  }
  if (point == 131) {
    return value <= 65;
  }
  if (point == 132) {
    return value >= 5 && value <= 40;
  }
  return value == 0;
}
```
