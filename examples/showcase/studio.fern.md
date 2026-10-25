# Shared studio projects

Foundry, Patchbay, and Circuit share a bounded project protocol.
The native server validates every edit before it publishes a revision.
Each connection receives a snapshot, then small changes. A reconnect receives the current state.

Projects use process-local memory until a visitor saves a checkpoint.
Set `FERN_LABS_DATA` to retain checkpoints on a persistent volume. Visitors can also export a project.
A checkpoint uses schema version 1. Recovery validates the version, application, dimensions, cells, and counters.
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
import "./labs_store" as store;

@derive(json.Json)
pub struct Change { kind: string, revision: i64, points: i32[], values: i32[], sender: string }

@derive(json.Json)
pub struct Snapshot { kind: string, revision: i64, cells: i32[], persistent: boolean, client_id: string, epoch_ms: i64, server_ms: i64, program: i32[] }

@derive(json.FromJson)
struct Input { kind: string, seq: i64, points: i32[], values: i32[] }

@derive(json.Json)
struct Ack { kind: string, seq: i64, revision: i64, server_us: i64, server_ms: i64 }

pub struct Project { name: string, revision: i64, cells: i32[], history: Change[], factory: foundry.Factory, updated: i64, running: boolean, packet: u8[], step_us: i32, epoch_ms: i64, program: i32[], delta: u8[], publication: i64, base_publication: i64, sent_cargo: i32[], sent_power: i32[] }

pub struct State { directory: string, projects: Cell[Project[]], live: live.State, inputs: Cell[i64] }

pub struct Room { state: State, at: i32 }

pub function state(): State {
  return State { directory: env("FERN_LABS_DATA").unwrap_or(""), projects: cell_new([] as Project[]), live: live.state(), inputs: cell_new(0i64) };
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
    cells = cells.with(3850, 1).with(3851, 2).with(3852, 7).with(3860, 6);
    for x in 13..20 {
      cells = cells.with(3840 + x, 2);
    }
    cells = cells.with(4363, 8).with(4364, 10).with(4108, 10);
    cells = cells.with(5130, 1).with(5131, 11).with(5152, 11).with(5153, 2).with(5154, 6);
    for x in 12..32 {
      cells = cells.with(5120 + x, 9);
    }
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
  let factory = foundry.factory(factory_size);
  if (factory_size > 0) {
    factory = foundry.configure(cells, factory, 256);
  }
  let initial = Project { name: key, revision: 0, cells: cells, history: [], factory: factory, updated: monotonic_ns(), running: true, packet: [], step_us: 0, epoch_ms: now_unix_ms(), program: [17, 34, 35, 112, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128, 128], delta: [], publication: 0i64, base_publication: 0i64, sent_cargo: [], sent_power: [] };
  let project = saved_project(state, app, name, initial)?;
  state.projects.set(state.projects.get().append(project));
  return Ok(Room { state: state, at: at });
}

pub function snapshot(room: Room): Snapshot {
  let p = room.state.projects.get()[room.at];
  return Snapshot { kind: "snapshot", revision: p.revision, cells: p.cells, persistent: room.state.directory != "", client_id: "", epoch_ms: p.epoch_ms, server_ms: now_unix_ms(), program: p.program };
}

pub function edit(room: Room, points: i32[], values: i32[]): Result[i64, http.HttpError] {
  return apply_edit(room, points, values, "");
}

function apply_edit(room: Room, points: i32[], values: i32[], sender: string): Result[i64, http.HttpError] {
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
  let event = Change { kind: "change", revision: p.revision + 1, points: points, values: values, sender: sender };
  if (p.factory.cargo.len() > 0) {
    p = Project { ...p, factory: foundry.configure(p.cells, foundry.clear(p.factory, changed), 256), packet: [] };
  }
  return record(room, p, event);
}

function record(room: Room, p: Project, event: Change): Result[i64, http.HttpError] {
  let history = p.history.append(event);
  if (history.len() > 128) {
    history = history.drop(history.len() - 128);
  }
  let next = Project { ...p, revision: event.revision, history: history };
  room.state.projects.set(room.state.projects.get().with(room.at, next));
  live.notify(room.state.live, next.name, "");
  return Ok(next.revision);
}

function pending(room: Room, cursor: Cell[i64], connection: websocket.Conn, client_id: string): void {
  let p = room.state.projects.get()[room.at];
  if (cursor.get() == p.revision) {
    return;
  }
  if (cursor.get() < 0 || p.history.len() == 0 || cursor.get() < p.history[0].revision - 1) {
    let value = snapshot(room);
    let welcome = Snapshot { ...value, client_id: client_id };
    if let websocket.Dropped = connection.send_text(welcome.to_json()) {
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
  let last = cell_new(monotonic_ns());
  let credits = cell_new(32i64);
  let factory_cursor = cell_new(0i64 - 1);
  let handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {
      pending(room, cursor, connection, subscription.listener.id);
      factory_pending(room, factory_cursor, connection);
    },
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let started = monotonic_ns();
      match (message) {
        Text(text) => {
          let Ok(input) = Input.from_json(text) else {
            connection.close(1008, "Invalid project command.");
            return;
          };
          let refill = (started - last.get()) / 5000000;
          let available = (credits.get() + refill).min(32i64);
          if (input.seq <= sequence.get() || available <= 0) {
            connection.close(1008, "Project commands exceed the connection limit.");
            return;
          }
          sequence.set(input.seq);
          state.inputs.set(state.inputs.get() + 1);
          credits.set(available - 1);
          if (refill > 0) {
            last.set(last.get() + refill * 5000000);
          }
          if (input.kind == "sync") {
            let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: state.projects.get()[room.at].revision, server_us: (monotonic_ns() - started) / 1000, server_ms: now_unix_ms() }.to_json());
            return;
          }
          if (input.kind == "program") {
            if (app != "circuit" || input.points.len() != 1 || input.values.len() != 1 || input.points[0] < 0 || input.points[0] > 15 || input.values[0] < 0 || input.values[0] > 143) {
              connection.close(1008, "Invalid processor instruction.");
              return;
            }
            let old = state.projects.get()[room.at];
            let p = Project { ...old, program: old.program.with(input.points[0], input.values[0]) };
            let saved = record(room, p, Change { kind: "program", revision: p.revision + 1, points: input.points, values: input.values, sender: subscription.listener.id });
            let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: state.projects.get()[room.at].revision, server_us: (monotonic_ns() - started) / 1000, server_ms: now_unix_ms() }.to_json());
            pending(room, cursor, connection, subscription.listener.id);
            return;
          }
          if (input.kind == "note") {
            if (app != "patchbay" || input.points.len() != 1 || input.values.len() != 1 || input.points[0] < 36 || input.points[0] > 96 || input.values[0] < 0 || input.values[0] > 1) {
              connection.close(1008, "Invalid live note.");
              return;
            }
            let p = state.projects.get()[room.at];
            let played = record(room, p, Change { kind: "note", revision: p.revision + 1, points: input.points, values: input.values, sender: subscription.listener.id });
            let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: state.projects.get()[room.at].revision, server_us: (monotonic_ns() - started) / 1000, server_ms: now_unix_ms() }.to_json());
            pending(room, cursor, connection, subscription.listener.id);
            return;
          }
          if (input.kind != "edit") {
            if (app != "foundry" || !control(room, input.kind)) {
              connection.close(1008, "Unknown project command.");
              return;
            }
            let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: state.projects.get()[room.at].revision, server_us: (monotonic_ns() - started) / 1000, server_ms: now_unix_ms() }.to_json());
            factory_pending(room, factory_cursor, connection);
            return;
          }
          match (apply_edit(room, input.points, input.values, subscription.listener.id)) {
            Ok(revision) => {
              let sent = connection.send_text(Ack { kind: "ack", seq: input.seq, revision: revision, server_us: (monotonic_ns() - started) / 1000, server_ms: now_unix_ms() }.to_json());
              pending(room, cursor, connection, subscription.listener.id);
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
  let options = websocket.Options { ...websocket.options(), max_message: 8192, backpressure: 524288, max_backpressure: 1048576 };
  match (websocket.accept_with_push(req, options, handlers, (connection: websocket.Conn): void => {
    while (live.wait_for(subscription, push_delay(room, app))) {
      pending(room, cursor, connection, subscription.listener.id);
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

function push_delay(room: Room, app: string): i32 {
  if (app != "foundry") {
    return -1;
  }
  let p = room.state.projects.get()[room.at];
  if (!p.running) {
    return -1;
  }
  return ((p.updated + foundry.PERIOD - monotonic_ns() + 999999) / 1000000).max(1i64).min(100i64) as i32;
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

function header(bytes: u8[], p: Project, publication: i64, base: i64, count: i32, delta: boolean): u8[] {
  let out = bytes.with(0, 70 as u8).with(1, 70 as u8).with(2, 2 as u8);
  if (delta) {
    out = out.with(3, 1 as u8);
  }
  out = put32(out, 4, if (p.running) { 1 } else { 0 });
  out = put64(out, 8, p.factory.tick);
  out = put64(out, 16, p.factory.produced);
  out = put64(out, 24, p.factory.consumed);
  out = put64(out, 32, p.factory.removed);
  out = put64(out, 40, p.factory.refined);
  out = put64(out, 48, p.revision);
  out = put64(out, 56, publication);
  out = put64(out, 64, base);
  out = put32(out, 72, p.step_us);
  out = put32(out, 76, p.factory.cargo.len());
  out = put32(out, 80, 256);
  out = put32(out, 84, count);
  return out;
}

function packets(p: Project): Project {
  let size = p.factory.cargo.len();
  let publication = p.publication + 1;
  let full: u8[] = __alloc_u8(96 + size * 3);
  let changed: i32[] = [];
  let i = 0;
  while (i < size) {
    let state = p.factory.cargo[i];
    let power = p.factory.powered[i];
    full = full.with(96 + i * 3, (state & 255) as u8).with(97 + i * 3, (state >> 8 & 255) as u8).with(98 + i * 3, power as u8);
    if (p.sent_cargo.len() != size || p.sent_cargo[i] != state || p.sent_power[i] != power) {
      changed = changed.append(i);
    }
    i = i + 1;
  }
  full = header(full, p, publication, p.publication, size, false);
  let delta = full;
  if (changed.len() * 5 < size * 3) {
    delta = __alloc_u8(96 + changed.len() * 5);
    i = 0;
    for at in changed {
      let state = p.factory.cargo[at];
      delta = delta.with(96 + i * 5, (at & 255) as u8).with(97 + i * 5, (at >> 8 & 255) as u8);
      delta = delta.with(98 + i * 5, (state & 255) as u8).with(99 + i * 5, (state >> 8 & 255) as u8).with(100 + i * 5, p.factory.powered[at] as u8);
      i = i + 1;
    }
    delta = header(delta, p, publication, p.publication, changed.len(), true);
  }
  return Project { ...p, packet: full, delta: delta, publication: publication, base_publication: p.publication, sent_cargo: p.factory.cargo, sent_power: p.factory.powered };
}

function factory_pending(room: Room, cursor: Cell[i64], connection: websocket.Conn): void {
  let p = room.state.projects.get()[room.at];
  if (p.factory.cargo.len() == 0) {
    return;
  }
  let now = monotonic_ns();
  let steps = 0;
  let started = monotonic_ns();
  if (now - p.updated > foundry.PERIOD * 8) {
    p = Project { ...p, updated: now - foundry.PERIOD };
  }
  while (p.running && now - p.updated >= foundry.PERIOD && steps < 4) {
    p = Project { ...p, factory: foundry.step(p.cells, p.factory, 256), updated: p.updated + foundry.PERIOD, packet: [] };
    steps = steps + 1;
  }
  if (steps > 0) {
    p = Project { ...p, step_us: ((monotonic_ns() - started) / 1000 / steps).min(2147483647i64) as i32 };
  }
  if (p.packet.len() == 0) {
    p = packets(p);
  }
  room.state.projects.set(room.state.projects.get().with(room.at, p));
  if (steps > 0) {
    live.notify(room.state.live, p.name, "");
  }
  if (cursor.get() != p.publication) {
    let bytes = p.packet;
    if (cursor.get() == p.base_publication) {
      bytes = p.delta;
    }
    if let websocket.Dropped = connection.send_binary(bytes) {
      connection.close(1013, "Factory receiver is too slow.");
      return;
    }
    cursor.set(p.publication);
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
    p = Project { ...p, factory: foundry.configure(p.cells, foundry.factory(p.cells.len()), 256), updated: monotonic_ns() };
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
    return value <= 11;
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

@derive(json.Json, json.FromJson)
struct SavedProject { version: i32, application: string, revision: i64, cells: i32[], factory: foundry.Factory, running: boolean, program: i32[] }

@derive(json.Json)
struct Checkpoint { revision: i64, persistent: boolean }

pub function checkpoint(state: State, app: string, request: HttpRequest): Result[HttpResponse, http.HttpError] {
  let target = room(state, app, common.query(request, "room", "commons"))?;
  if (state.directory == "") {
    return Err(http.fail(409, "This project uses temporary memory. Export a copy instead."));
  }
  let p = state.projects.get()[target.at];
  let saved = SavedProject { version: 1, application: app, revision: p.revision, cells: p.cells, factory: p.factory, running: p.running, program: p.program };
  store.save(state.directory, app + "-" + common.query(request, "room", "commons"), saved.to_json()).map_err((e: string): http.HttpError => http.fail(503, e))?;
  return Ok(http.json_of(Checkpoint { revision: p.revision, persistent: true }));
}

function saved_project(state: State, app: string, name: string, initial: Project): Result[Project, http.HttpError] {
  let Some(value) = store.load(state.directory, app + "-" + name).map_err((e: string): http.HttpError => http.fail(503, e))? else {
    return Ok(initial);
  };
  let saved = json.decode[SavedProject](value).map_err((e: string): http.HttpError => http.fail(503, e))?;
  if (saved.version != 1 || saved.program.len() != 16 || saved.application != app || saved.revision < 0 || saved.cells.len() != initial.cells.len() || saved.factory.cargo.len() != initial.factory.cargo.len() || saved.factory.powered.len() != initial.factory.powered.len()) {
    return Err(http.fail(503, "The saved project has an unsupported schema."));
  }
  let i = 0;
  for value in saved.cells {
    if (!valid_edit(initial.name, i, value)) {
      return Err(http.fail(503, "The saved project contains invalid cells."));
    }
    i = i + 1;
  }
  for instruction in saved.program {
    if (instruction < 0 || instruction > 143) {
      return Err(http.fail(503, "The saved processor contains an invalid instruction."));
    }
  }
  for value in saved.factory.cargo {
    if (value < 0 || value > 2047) {
      return Err(http.fail(503, "The saved factory contains invalid cargo."));
    }
  }
  if (saved.factory.tick < 0 || saved.factory.produced < 0 || saved.factory.consumed < 0 || saved.factory.removed < 0 || saved.factory.refined < 0) {
    return Err(http.fail(503, "The saved factory contains invalid counters."));
  }
  let factory = saved.factory;
  if (factory.cargo.len() > 0) {
    factory = foundry.configure(saved.cells, factory, 256);
  }
  return Ok(Project { ...initial, cells: saved.cells, revision: saved.revision, factory: factory, running: saved.running, program: saved.program });
}
```
