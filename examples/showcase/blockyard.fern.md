# Blockyard: worlds, chunks, and WebSocket sessions

Blockyard is a shared creative voxel world. Fern owns movement, collision, block edits, and persistence.
The browser draws nearby chunks and predicts its own movement with the same Fern collision kernel.

Each world supports 512 participants. A directory can assign different worlds to different server processes.
One process owns each world. This example does not split one world across multiple machines.

```fern
<<*>>=
<<Directory>>
<<HTTP snapshots>>
<<WebSocket>>
```

## Directory

The default worlds are Commons, Meadow, and Harbor. They share terrain rules and retain independent edits.
`FERN_BLOCKYARD_DATA` selects a directory on a persistent volume. Without it, worlds explicitly report temporary storage.

`FERN_BLOCKYARD_SERVER` identifies this process's deployment.
An optional `FERN_BLOCKYARD_WORLDS` JSON array assigns each world a name, server identifier, and public origin.
The landing route redirects visitors to the owning server before they open a socket.

```fern
<<Directory>>=
import "core/map";
import "std/array";
import "std/string";
import "std/http";
import "std/http/websocket";
import "std/json";
import "std/result";
import "std/option";
import "std/async";
import "./common";
import "./blockyard_world" as model;
import "./blockyard_store" as store;

@derive(json.Json, json.FromJson)
pub struct WorldHost { name: string, server: string, origin: string }

pub struct State { instance: string, server: string, directory: string, hosts: WorldHost[], error: string, worlds: model.World[] }

pub function state(instance: string): State {
  let server = env("FERN_BLOCKYARD_SERVER").unwrap_or("local");
  let hosts: WorldHost[] = [WorldHost { name: "commons", server: server, origin: "" }, WorldHost { name: "meadow", server: server, origin: "" }, WorldHost { name: "harbor", server: server, origin: "" }];
  let error = "";
  if let Some(config) = env("FERN_BLOCKYARD_WORLDS") {
    match (json.decode_array[WorldHost](config)) {
      Ok(values) => {
        hosts = values;
      },
      Err(reason) => {
        error = "Invalid world directory: " + reason;
      }
    }
  }
  if (hosts.len() == 0 || hosts.len() > 8) {
    error = "The world directory needs one to eight worlds.";
  }
  let names: Map[string, boolean] = map_new(8);
  for host in hosts {
    if (!common.room_name(host.name) || names.contains(host.name) || host.server == "") {
      error = "The world directory has an invalid or duplicate world.";
    }
    if (host.server != server && !(host.origin.starts_with("https://") || host.origin.starts_with("http://"))) {
      error = "A remote world needs an HTTP or HTTPS origin.";
    }
    if (host.origin.contains("\n") || host.origin.contains("\r") || host.origin.contains("?") || host.origin.contains("#") || host.origin.ends_with("/")) {
      error = "World origins must omit paths, queries, and trailing slashes.";
    }
    names = names.insert(host.name, true);
  }
  if (error != "") {
    eprint(error);
  }
  let folder = env("FERN_BLOCKYARD_DATA").unwrap_or("");
  let worlds: model.World[] = [];
  if (error == "") {
    for entry in hosts {
      if (entry.server == server) {
        worlds = worlds.append(load_world(folder, entry.name));
      }
    }
  }
  return State { instance: instance, server: server, directory: folder, hosts: hosts, error: error, worlds: worlds };
}

function host(state: State, name: string): Result[WorldHost, http.HttpError] {
  if (state.error != "") {
    return Err(http.fail(503, "The world directory is unavailable."));
  }
  if (!common.room_name(name)) {
    return Err(http.fail(400, "Invalid world name."));
  }
  for entry in state.hosts {
    if (entry.name == name) {
      return Ok(entry);
    }
  }
  return Err(http.fail(404, "This world does not exist."));
}

pub function destination(state: State, request: HttpRequest): Result[Option[string], http.HttpError] {
  let entry = host(state, common.query(request, "world", "commons"))?;
  if (entry.server != state.server) {
    return Ok(Some(entry.origin + "/blockyard/?world=" + entry.name));
  }
  return Ok(None);
}

pub function world(state: State, name: string): Result[model.World, http.HttpError] {
  let entry = host(state, name)?;
  if (entry.server != state.server) {
    return Err(http.fail(421, "Open this world on its owning server."));
  }
  for value in state.worlds {
    if (value.name == name) {
      if (value.fault.get() != "") {
        return Err(http.fail(503, "World storage is unavailable. The server must recover it before play resumes."));
      }
      return Ok(value);
    }
  }
  return Err(http.fail(503, "This world is unavailable."));
}

function load_world(folder: string, name: string): model.World {
  match (store.recover(folder, name)) {
    Ok(recovered) => {
      let disk = store.store(folder, name, recovered.journal_bytes);
      return model.world(name, recovered.blocks, recovered.revision, (blocks: Map[i32, i32], changes: model.Change[], revision: i64): Result[void, string] => store.commit(disk, blocks, changes, revision));
    },
    Err(error) => {
      eprint("Blockyard recovery: " + error);
      let unavailable = model.world(name, map_new(4) as Map[i32, i32], 0, (blocks: Map[i32, i32], changes: model.Change[], revision: i64): Result[void, string] => Err("World storage is unavailable."));
      unavailable.fault.set(error);
      return unavailable;
    }
  }
  return model.world(name, map_new(4) as Map[i32, i32], 0, (blocks: Map[i32, i32], changes: model.Change[], revision: i64): Result[void, string] => Err("World storage is unavailable."));
}

pub function participants(state: State): i32 {
  let count: i32 = 0;
  for value in state.worlds {
    count = count + value.count.get();
  }
  return count;
}

pub function edited_blocks(state: State): i32 {
  let count: i32 = 0;
  for value in state.worlds {
    count = count + value.blocks.get().len();
  }
  return count;
}

pub function inputs(state: State): i64 {
  let count: i64 = 0;
  for value in state.worlds {
    count = count + value.inputs.get();
  }
  return count;
}

@derive(json.Json)
struct Directory { worlds: WorldHost[], server: string, persistent: boolean }

pub function directory(state: State): HttpResponse {
  return http.json_of(Directory { worlds: state.hosts, server: state.server, persistent: state.directory != "" }).with_header("Cache-Control", "no-store");
}
```

## HTTP snapshots

Chunk responses contain only edited blocks. A worker fills the unchanged terrain through the Fern Wasm kernel.
An ETag identifies each chunk revision. Every new socket receives an event cursor before it requests chunks.
That order prevents a block change from falling between the initial snapshot and live updates.

```fern
<<HTTP snapshots>>=
@derive(json.Json)
struct Status { world: string, players: i32, capacity: i32, revision: i64, edited_blocks: i32, persistent: boolean, instance: string }

@derive(json.Json)
struct Block { at: i32, material: i32 }

@derive(json.Json)
struct Chunk { x: i32, z: i32, revision: i64, blocks: Block[] }

pub function status(state: State, request: HttpRequest): Result[HttpResponse, http.HttpError] {
  let value = world(state, common.query(request, "world", "commons"))?;
  model.advance(value, monotonic_ns());
  return Ok(http.json_of(Status { world: value.name, players: value.count.get(), capacity: model.CAPACITY, revision: value.revision.get(), edited_blocks: value.blocks.get().len(), persistent: state.directory != "", instance: state.instance }).with_header("Cache-Control", "no-store"));
}

pub function chunk(state: State, request: HttpRequest): Result[HttpResponse, http.HttpError] {
  let value = world(state, common.query(request, "world", "commons"))?;
  let x = common.integer(request, "x", 16, 0, 31)?;
  let z = common.integer(request, "z", 16, 0, 31)?;
  let at = z * 32 + x;
  let revision = value.versions.get().get(at).unwrap_or(0 as i64);
  let etag = "\"blockyard-1-" + value.name + "-" + at.to_string() + "-" + revision.to_string() + "\"";
  if (request.headers.get("If-None-Match").unwrap_or("") == etag) {
    return Ok(http.text(304, "").with_header("ETag", etag));
  }
  let blocks: Block[] = [];
  for entry in value.chunks.get().get(at).unwrap_or([] as (i32, i32)[]) {
    blocks = blocks.append(Block { at: entry.0, material: entry.1 });
  }
  return Ok(http.json_of(Chunk { x: x, z: z, revision: revision, blocks: blocks }).with_header("ETag", etag).with_header("Cache-Control", "no-cache"));
}
```

## WebSocket

The producer uses the shared world deadline. Slow writers skip old movement snapshots.
They recover block changes from the bounded event journal, or refresh chunks when their cursor expires.
Socket backpressure has a hard bound. An overloaded participant cannot create an unbounded queue.

```fern
<<WebSocket>>=
@derive(json.Json)
struct Welcome { kind: string, id: i32, capacity: i32, instance: string, world: string, cursor: i64, revision: i64, persistent: boolean, snapshot_hz: i32, x: f64, y: f64, z: f64 }

@derive(json.Json)
struct Rejection { kind: string, nonce: i64, message: string }

@derive(json.Json)
struct Travel { kind: string, seq: i64, x: f64, y: f64, z: f64 }

function rejected(connection: websocket.Conn, nonce: i64, message: string): void {
  let result = connection.send_text(Rejection { kind: "reject", nonce: nonce, message: message }.to_json());
  if let websocket.Dropped = result {
    connection.close(1013, "Cannot receive block results.");
  }
}

pub function socket(state: State, request: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let value = world(state, common.query(request, "world", "commons"))?;
  let seat = model.reserve(value, monotonic_ns()).map_err((reason: string): http.HttpError => http.fail(503, reason))?;
  let alive = cell_new(true);
  let opened = cell_new(false);
  let sent = cell_new(0 as i64);
  let cursor = cell_new(value.serial.get());
  let handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {
      model.advance(value, monotonic_ns());
      let player = value.players.get()[seat.at];
      cursor.set(value.serial.get());
      let welcome = connection.send_text(Welcome { kind: "welcome", id: seat.at + 1, capacity: model.CAPACITY, instance: state.instance, world: value.name, cursor: cursor.get(), revision: value.revision.get(), persistent: state.directory != "", snapshot_hz: 10, x: player.x, y: player.y, z: player.z }.to_json());
      let initial = connection.send_binary(model.packet(value, model.sector(player)));
      sent.set(value.tick.get());
      opened.set(true);
    },
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let started = monotonic_ns();
      match (message) {
        Binary(bytes) => {
          if (bytes.len() != 16) {
            connection.close(1008, "Send a sixteen-byte control or block packet.");
            return;
          }
          if (bytes[0] == 1) {
            match (model.control(value, seat, bytes, started)) {
              Ok(seq) => {
                if let websocket.Dropped = connection.send_binary(model.acknowledgement(seq, started)) {
                  connection.close(1013, "Cannot receive input acknowledgements.");
                }
              },
              Err(error) => {
                connection.close(1008, error);
              }
            }
          } else if (bytes[0] == 2) {
            if let Err(error) = model.queue_edit(value, seat, bytes, started) {
              rejected(connection, model.read32(bytes, 10), error);
            }
          } else if (bytes[0] == 3) {
            match (model.travel(value, seat, bytes, started)) {
              Ok(player) => {
                let result = connection.send_text(Travel { kind: "travel", seq: player.seq, x: player.x, y: player.y, z: player.z }.to_json());
              },
              Err(error) => {
                rejected(connection, 0, error);
              }
            }
          } else {
            connection.close(1008, "Unknown control packet.");
          }
        },
        Text(_) => {
          connection.close(1003, "Send binary controls.");
        }
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => {
      alive.set(false);
      model.release(value, seat);
    },
  };
  let options = websocket.Options { ...websocket.options(), compression: false, max_message: 16, backpressure: 131072, max_backpressure: 262144 };
  match (websocket.accept_with_push(request, options, handlers, (connection: websocket.Conn): void => {
    while (alive.get() && !async.task_cancelled()) {
      if (!opened.get()) {
        let waited = async.wait_any([] as i32[], 1);
        continue;
      }
      let now = monotonic_ns();
      if (!model.owns(value, seat) || now - value.players.get()[seat.at].seen > 60000000000) {
        connection.close(1001, "Participant inactive.");
        let result = connection.flush();
        break;
      }
      model.advance(value, now);
      if (value.fault.get() != "") {
        connection.close(1011, "World storage is unavailable.");
        let result = connection.flush();
        break;
      }
      let player = value.players.get()[seat.at];
      if (sent.get() != value.tick.get()) {
        if let websocket.Dropped = connection.send_binary(model.packet(value, model.sector(player))) {
          connection.close(1013, "Cannot receive world snapshots.");
          break;
        }
        sent.set(value.tick.get());
      }
      if (cursor.get() != value.serial.get()) {
        let history = value.history.get();
        let at = model.event_start(history, cursor.get());
        while (at < history.len()) {
          let event = history[at];
          if (event.lease == seat.key && event.error != "") {
            rejected(connection, event.change.nonce, event.error);
          }
          at = at + 1;
        }
        if let websocket.Dropped = connection.send_text(model.delta(value, model.sector(player), cursor.get())) {
          connection.close(1013, "Cannot receive block updates.");
          break;
        }
        cursor.set(value.serial.get());
      }
      if let websocket.Dropped = connection.flush() {
        break;
      }
      let delay = ((value.updated.get().unwrap_or(now) + model.PERIOD - monotonic_ns() + 999999) / 1000000).max(1).min(100) as i32;
      let waited = async.wait_any([] as i32[], delay);
    }
    alive.set(false);
    model.release(value, seat);
  })) {
    Ok(response) => {
      return Ok(response);
    },
    Err(error) => {
      alive.set(false);
      model.release(value, seat);
      return Err(error);
    }
  }
}
```
