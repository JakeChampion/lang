# Native database and archive routes

Each named room owns a database and an inverted search index. The two applications share storage rules and explicit limits.
Transactions compare the submitted revision before publication. A stale writer receives HTTP 409 and retains its draft.

Set `FERN_LABS_DATA` to retain checkpoints on a persistent volume. Storage errors block later writes until explicit recovery.
Temporary rooms start again from the sample archive and an empty database after recovery or process restart.

The browser sends commands through one persistent WebSocket. This connection keeps its process active on request-driven container hosts.
The HTTP APIs remain available. Multiple owners still require connection affinity or separate public origins.

```fern
<<*>>=
<<Application routes>>
```

## Application routes

Request parsing returns typed HTTP errors. The outer router supplies compression and process metrics.
The server admits twelve rooms. File, document, query, and transaction limits bound their memory use.

```fern
<<Application routes>>=
import "std/http";
import "std/http/websocket";
import "std/png";
import "std/f64";
import "std/json";
import "std/array";
import "std/result";
import "std/option";
import "./common";
import "./prism" as trace;
import "./fernbase";
import "./lantern";
import "./labs_store" as store;

@derive(json.Json, json.FromJson)
struct SavedArchive { revision: i64, documents: lantern.Document[] }

pub struct Room { name: string, database: fernbase.Database, archive: lantern.Index, revision: i64, fault: string }

pub struct State { directory: string, rooms: Cell[Room[]], preview: Cell[Option[u8[]]], connections: Cell[i32] }

@derive(json.FromJson)
struct Query { sql: string, revision: i64, preview: boolean }

@derive(json.Json)
struct QueryResult { revision: i64, result: fernbase.Output, rows: i32, persistent: boolean, server_us: i64 }

@derive(json.Json)
struct DatabaseStatus { revision: i64, rows: fernbase.Row[], total: i32, persistent: boolean, fault: string }

@derive(json.FromJson)
struct Import { title: string, text: string, revision: i64 }

@derive(json.Json)
struct ArchiveStatus { revision: i64, documents: i32, terms: i32, words: i32, persistent: boolean, fault: string }

@derive(json.Json)
struct SearchResult { revision: i64, result: lantern.Results, server_us: i64 }

pub function state(): State {
  return State { directory: env("FERN_LABS_DATA").unwrap_or(""), rooms: cell_new([] as Room[]), preview: cell_new(None as Option[u8[]]), connections: cell_new(0) };
}

function failure(message: string): http.HttpError {
  return http.fail(503, message);
}

function restore(directory: string, name: string): Result[Room, string] {
  let db = fernbase.initial();
  if let Some(saved) = store.load(directory, "fernbase-" + name)? {
    db = json.decode[fernbase.Database](saved)?;
    if (db.rows.len() > 10000 || db.revision < 0) {
      return Err("Invalid database checkpoint.");
    }
    let previous = "";
    let first = true;
    for row in db.rows {
      if (row.key.len() == 0 || row.key.len() > 128 || row.value.len() > 4096 || !first && row.key <= previous) {
        return Err("Invalid primary index in checkpoint.");
      }
      previous = row.key;
      first = false;
    }
  }
  let archive = lantern.empty();
  let revision = 0i64;
  if let Some(saved) = store.load(directory, "lantern-" + name)? {
    let data = json.decode[SavedArchive](saved)?;
    if (data.revision < 0) {
      return Err("Invalid archive revision.");
    }
    for document in data.documents {
      archive = lantern.add(archive, document)?;
    }
    revision = data.revision;
  } else {
    for entry in [
      ("Fern on the server", "Fern compiles native web servers. HTTP, WebSocket, and server-sent events connect shared applications."),
      ("A garden of sound", "Audio synthesis connects oscillators, envelopes, and filters. A musical performance depends on sample timing."),
      ("Build a world", "Voxel terrain uses chunks. Factories connect conveyors and machines. Multiplayer worlds retain shared edits."),
      ("東京の庭", "東京 Fern のサーバー。Unicode text belongs in a search index."),
      ("Café and music", "CAFÉ visitors share music, stories, and small experiments."),
    ] {
      archive = lantern.add(archive, lantern.Document { id: archive.documents.len(), title: entry.0, text: entry.1 })?;
    }
  }
  return Ok(Room { name: name, database: db, archive: archive, revision: revision, fault: "" });
}

function room(state: State, request: HttpRequest): Result[i32, http.HttpError] {
  let name = common.query(request, "room", "commons");
  if (!common.room_name(name)) {
    return Err(http.fail(400, "Invalid room name."));
  }
  let at = 0;
  for found in state.rooms.get() {
    if (found.name == name) {
      return Ok(at);
    }
    at = at + 1;
  }
  if (at >= 12) {
    return Err(http.fail(429, "All database and archive rooms are in use."));
  }
  let found = restore(state.directory, name).map_err((e: string): http.HttpError => failure(e))?;
  state.rooms.set(state.rooms.get().append(found));
  return Ok(at);
}

function persist(state: State, at: i32, name: string, value: string): Result[void, http.HttpError] {
  match (store.save(state.directory, name, value)) {
    Ok(_) => {
      return Ok(());
    },
    Err(error) => {
      let rooms = state.rooms.get();
      state.rooms.set(rooms.with(at, Room { ...rooms[at], fault: error }));
      return Err(failure(error));
    }
  }
}

pub function handle(state: State, application: string, action: string, request: HttpRequest): Result[HttpResponse, http.HttpError] {
  if (application == "fernbase" && action != "state" && action != "query" && action != "recover" && action != "export") {
    return Err(http.fail(404, "Unknown database operation."));
  }
  if (application == "lantern" && action != "state" && action != "search" && action != "import" && action != "recover" && action != "export") {
    return Err(http.fail(404, "Unknown archive operation."));
  }
  let started = monotonic_ns();
  let at = room(state, request)?;
  let current = state.rooms.get()[at];
  if (action == "recover") {
    let restored = restore(state.directory, current.name).map_err((e: string): http.HttpError => failure(e))?;
    if (application == "fernbase") {
      current = Room { ...current, database: restored.database, fault: "" };
    } else {
      current = Room { ...current, archive: restored.archive, revision: restored.revision, fault: "" };
    }
    state.rooms.set(state.rooms.get().with(at, current));
  }
  if (application == "fernbase") {
    if (action == "export") {
      return Ok(http.json_of(current.database).with_header("Cache-Control", "no-store"));
    }
    if (action == "query") {
      let input = http.body_json[Query](request).map_err((e: http.BodyError): http.HttpError => http.fail(400, "Invalid query JSON."))?;
      if (input.preview && input.revision != current.database.revision) {
        return Err(http.fail(409, "The database changed during this draft. Roll back or retry from a new revision."));
      }
      let result = fernbase.execute(current.database, input.sql).map_err((e: string): http.HttpError => http.fail(400, e))?;
      if (result.output.changed && !input.preview) {
        if (current.fault != "") {
          return Err(failure("Recover this project before another write."));
        }
        if (input.revision != current.database.revision) {
          return Err(http.fail(409, "Another transaction committed. Refresh and retry."));
        }
        let checkpoint = result.database.to_json();
        if (checkpoint.len() > 8388608) {
          return Err(http.fail(413, "The database exceeds its 8 MiB checkpoint limit."));
        }
        persist(state, at, "fernbase-" + current.name, checkpoint)?;
        state.rooms.set(state.rooms.get().with(at, Room { ...current, database: result.database }));
      }
      return Ok(http.json_of(QueryResult { revision: result.database.revision, result: result.output, rows: result.database.rows.len(), persistent: state.directory != "", server_us: (monotonic_ns() - started) / 1000 }).with_header("Cache-Control", "no-store"));
    }
    return Ok(http.json_of(DatabaseStatus { revision: current.database.revision, rows: current.database.rows.take(200), total: current.database.rows.len(), persistent: state.directory != "", fault: current.fault }).with_header("Cache-Control", "no-store"));
  }
  if (action == "export") {
    let text = "";
    for document in current.archive.documents {
      text = text + document.to_json() + "\n";
    }
    return Ok(http.ok(text).with_header("Content-Type", "application/x-ndjson").with_header("Cache-Control", "no-store"));
  }
  if (action == "import") {
    if (current.fault != "") {
      return Err(failure("Recover this project before another write."));
    }
    let input = http.body_json[Import](request).map_err((e: http.BodyError): http.HttpError => http.fail(400, "Invalid document JSON."))?;
    if (input.revision != current.revision) {
      return Err(http.fail(409, "The archive changed. Refresh and retry."));
    }
    let index = lantern.add(current.archive, lantern.Document { id: current.archive.documents.len(), title: input.title, text: input.text }).map_err((e: string): http.HttpError => http.fail(400, e))?;
    let revision = current.revision + 1;
    persist(state, at, "lantern-" + current.name, SavedArchive { revision: revision, documents: index.documents }.to_json())?;
    current = Room { ...current, archive: index, revision: revision };
    state.rooms.set(state.rooms.get().with(at, current));
  }
  if (action == "search") {
    let results = lantern.search(current.archive, common.query(request, "q", ""));
    return Ok(http.json_of(SearchResult { revision: current.revision, result: results, server_us: (monotonic_ns() - started) / 1000 }).with_header("Cache-Control", "no-store"));
  }
  return Ok(http.json_of(ArchiveStatus { revision: current.revision, documents: current.archive.documents.len(), terms: current.archive.terms.len(), words: current.archive.words, persistent: state.directory != "", fault: current.fault }).with_header("Cache-Control", "no-store"));
}

@derive(json.FromJson)
struct Command { seq: i64, path: string, body: string, post: boolean }

@derive(json.Json)
struct Reply { seq: i64, status: i32, body: string }

pub function socket(state: State, application: string, req: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let at = room(state, req)?;
  if (state.connections.get() >= 64) {
    return Err(http.fail(503, "All database and archive connections are in use."));
  }
  let name = state.rooms.get()[at].name;
  let sequence = cell_new(0i64);
  let credits = cell_new(32i64);
  let last = cell_new(monotonic_ns());
  let handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {},
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let Text(text) = message else {
        connection.close(1003, "Send JSON commands.");
        return;
      };
      let Ok(input) = Command.from_json(text) else {
        connection.close(1008, "Invalid command.");
        return;
      };
      let now = monotonic_ns();
      let refill = (now - last.get()) / 5000000;
      let available = (credits.get() + refill).min(32i64);
      if (input.seq <= sequence.get() || available <= 0 || input.path.len() > 16384 || input.body.len() > 16384) {
        connection.close(1008, "Command exceeds this connection limit.");
        return;
      }
      sequence.set(input.seq);
      credits.set(available - 1);
      if (refill > 0) {
        last.set(last.get() + refill * 5000000);
      }
      let method = "GET";
      if (input.post) {
        method = "POST";
      }
      let request = http.request(method, input.path).with_json(input.body);
      let prefix = "/" + application + "/";
      if (!common.path(request).starts_with(prefix) || common.query(request, "room", "commons") != name) {
        connection.close(1008, "Command belongs to another application or room.");
        return;
      }
      let action = common.path(request).drop(prefix.len());
      let mutates = action == "query" || action == "import" || action == "recover";
      let response = http.text(405, "Use the operation method.");
      if (input.post == mutates) {
        response = http.respond(handle(state, application, action, request));
      }
      let reply = Reply { seq: input.seq, status: response.status, body: response.body_string() };
      if let websocket.Dropped = connection.send_text(reply.to_json()) {
        connection.close(1013, "Command receiver is too slow.");
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => state.connections.set(state.connections.get() - 1),
  };
  state.connections.set(state.connections.get() + 1);
  let options = websocket.Options { ...websocket.options(), max_message: 65536, backpressure: 16777216, max_backpressure: 33554432 };
  match (websocket.accept(req, options, handlers)) {
    Ok(response) => {
      return Ok(response);
    },
    Err(error) => {
      state.connections.set(state.connections.get() - 1);
      return Err(error);
    }
  }
}

@derive(json.Json)
struct Reference { pixels: i32[], width: i32, height: i32, server_us: i64 }

pub function reference(request: HttpRequest): Result[HttpResponse, http.HttpError] {
  let width = common.integer(request, "w", 32, 1, 64)?;
  let height = common.integer(request, "h", 20, 1, 40)?;
  let sample = common.integer(request, "sample", 0, 0, 127)?;
  let scene = common.integer(request, "scene", 6324352, 0, 16777215)?;
  let angle = common.integer(request, "angle", 0, -180, 180)?;
  let started = monotonic_ns();
  let pixels: i32[] = [];
  for y in 0..height {
    for x in 0..width {
      pixels = pixels.append(trace.pixel(x, y, width, height, sample, scene, angle as f64 * 3.141592653589793 / 180.0));
    }
  }
  return Ok(http.json_of(Reference { pixels: pixels, width: width, height: height, server_us: (monotonic_ns() - started) / 1000 }).with_header("Cache-Control", "no-store"));
}

pub function preview(state: State): Result[HttpResponse, http.HttpError] {
  let bytes: u8[] = [];
  if let Some(cached) = state.preview.get() {
    bytes = cached;
  } else {
    bytes = thumbnail()?;
    state.preview.set(Some(bytes));
  }
  return Ok(http.bytes(200, bytes).with_content_type("image/png").with_header("Cache-Control", "public, max-age=3600"));
}

pub function thumbnail(): Result[u8[], http.HttpError] {
  let width = 320;
  let height = 200;
  let rgb: u8[] = __alloc_u8(width * height * 3);
  for y in 0..height {
    for x in 0..width {
      let red = 0.0;
      let green = 0.0;
      let blue = 0.0;
      for sample in 0..8 {
        let value = trace.pixel(x, y, width, height, sample, 6324352, 0.0);
        red = red + (value >> 20 & 1023) as f64;
        green = green + (value >> 10 & 1023) as f64;
        blue = blue + (value & 1023) as f64;
      }
      let r = red / 8.0 / 255.0 * 1.5;
      let g = green / 8.0 / 255.0 * 1.5;
      let b = blue / 8.0 / 255.0 * 1.5;
      let at = (y * width + x) * 3;
      rgb = rgb.with(at, ((r / (1.0 + r)).sqrt() * 255.0) as u8).with(at + 1, ((g / (1.0 + g)).sqrt() * 255.0) as u8).with(at + 2, ((b / (1.0 + b)).sqrt() * 255.0) as u8);
    }
  }
  let bytes = png.encode_rgb8(width, height, rgb).map_err((e: png.EncodeError): http.HttpError => http.fail(500, "Cannot encode the scene preview."))?;
  return Ok(bytes);
}
```
