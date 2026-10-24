# Gather: a shared arena for 512 participants

Players collect seeds toward a shared goal. Fern owns movement, collection, scores, and the round clock.
The browser sends destinations. It never submits a position or a score.
Each connection receives ten binary snapshots per second. Fern builds each snapshot once and shares its bytes.

The arena belongs to one server process. A deployment with multiple instances needs connection affinity.
The load test uses real WebSocket connections. The game does not populate its participant count with simulated players.

```fern
<<*>>=
<<State and seats>>
<<Movement and collection>>
<<Binary protocol>>
<<WebSocket session>>
```

## State and seats

A seat has a random lease token. A late close or input cannot change a replacement player in the same slot.
The arrays contain ordinary values. The connection itself never enters shared mutable state.

Optional clock values distinguish a missing deadline from a valid timestamp at zero.
State updates contain no suspension point. An update detaches its array from the cell before an owned mutation.

```fern
<<State and seats>>=
import "std/http";
import "std/http/websocket";
import "std/async";
import "std/array";
import "std/option";
import "std/result";
import "std/json";
import "std/f64";
import "std/rand";
import "./common";

pub const CAPACITY: i32 = 512;

pub const WIDTH: i32 = 1024;

pub const HEIGHT: i32 = 640;

pub const GOAL: i32 = 2048;

pub const PERIOD: i64 = 100000000;

pub struct Player { key: string, x: f64, y: f64, tx: f64, ty: f64, seq: i64, applied: i64, score: i32, seen: i64 }

pub struct Seed { x: f64, y: f64, ready: Option[i64] }

pub struct Seat { at: i32, key: string }

pub struct State {
  players: Cell[Player[]],
  seeds: Cell[Seed[]],
  count: Cell[i32],
  inputs: Cell[i64],
  random: Cell[i64],
  updated: Cell[Option[i64]],
  tick: Cell[i64],
  round: Cell[i32],
  pooled: Cell[i32],
  ends: Cell[i64],
  restart: Cell[Option[i64]],
  packet: Cell[u8[]],
  instance: string,
}

@derive(json.Json)
struct Welcome { kind: string, id: i32, capacity: i32, snapshot_hz: i32, speed: i32, instance: string }

@derive(json.Json)
struct Status { players: i32, capacity: i32, snapshot_hz: i32, inputs: i64, instance: string }

pub function state(instance: string): State {
  return State { players: cell_new([] as Player[]), seeds: cell_new([] as Seed[]), count: cell_new(0), inputs: cell_new(0 as i64), random: cell_new(rand.rng_seed(monotonic_ns())), updated: cell_new(None as Option[i64]), tick: cell_new(0 as i64), round: cell_new(0), pooled: cell_new(0), ends: cell_new(0 as i64), restart: cell_new(None as Option[i64]), packet: cell_new([] as u8[]), instance: instance };
}

function empty_player(): Player {
  return Player { key: "", x: 0, y: 0, tx: 0, ty: 0, seq: 0, applied: 0, score: 0, seen: 0 };
}

function new_seed(state: State, index: i32): Seed {
  let (first, x) = rand.rng_next(state.random.get());
  let (second, y) = rand.rng_next(first);
  state.random.set(second);
  return Seed { x: (index % 8 * 128 + 12) as f64 + (x % 104) as f64, y: (index / 8 * 80 + 12) as f64 + (y % 56) as f64, ready: None };
}

function start_round(state: State, now: i64): void {
  state.round.set(state.round.get() + 1);
  state.pooled.set(0);
  state.ends.set(now + 120000000000);
  state.restart.set(None);
  let seeds: Seed[] = [];
  for index in 0..64 {
    seeds = seeds.append(new_seed(state, index));
  }
  state.seeds.set(seeds);
  let players = state.players.get();
  state.players.set([] as Player[]);
  let at: i32 = 0;
  while (at < players.len()) {
    players = players.with(at, Player { ...players[at], score: 0 });
    at = at + 1;
  }
  state.players.set(players);
}

pub function reserve(state: State): Result[Seat, http.HttpError] {
  let now = monotonic_ns();
  advance(state, now);
  if (state.count.get() >= CAPACITY) {
    return Err(http.fail(503, "the arena is full"));
  }
  if (state.count.get() == 0) {
    start_round(state, now);
    state.updated.set(None);
  }
  let players = state.players.get();
  state.players.set([] as Player[]);
  let at: i32 = 0;
  while (at < players.len() && players[at].key != "") {
    at = at + 1;
  }
  let key = common.token();
  let point = new_seed(state, at % 64);
  let player = Player { key: key, x: point.x, y: point.y, tx: point.x, ty: point.y, seq: 0, applied: 0, score: 0, seen: now };
  if (at == players.len()) {
    players = players.append(player);
  } else {
    players = players.with(at, player);
  }
  state.players.set(players);
  state.count.set(state.count.get() + 1);
  return Ok(Seat { at: at, key: key });
}

pub function release(state: State, seat: Seat): void {
  let players = state.players.get();
  if (seat.at >= players.len() || players[seat.at].key != seat.key) {
    return;
  }
  state.players.set([] as Player[]);
  players = players.with(seat.at, empty_player());
  state.players.set(players);
  state.count.set(state.count.get() - 1);
}

pub function status(state: State): HttpResponse {
  advance(state, monotonic_ns());
  return http.json_of(Status { players: state.count.get(), capacity: CAPACITY, snapshot_hz: 10, inputs: state.inputs.get(), instance: state.instance }).with_header("Cache-Control", "no-store");
}
```

## Movement and collection

A simulation step covers at most 100 milliseconds. A player moves at 180 world units per second.
Collection checks the whole movement segment, so a seed cannot fall between two simulation steps.
An eight-by-eight spatial grid limits each player to nine seed checks.
The iteration order rotates each tick to resolve simultaneous collections without a permanent slot advantage.

Any ready connection can advance the shared clock after its deadline. No connection owns the clock.
A slow socket cannot stop the simulation for the other participants. The last disconnect leaves no clock task behind.

```fern
<<Movement and collection>>=
pub function move(own player: Player, seconds: f64): Player {
  let dx = player.tx - player.x;
  let dy = player.ty - player.y;
  let distance = (dx * dx + dy * dy).sqrt();
  let amount = (seconds.max(0.0).min(0.1) * 180.0).min(distance);
  if (distance > 0.0) {
    return Player { ...player, x: player.x + dx * amount / distance, y: player.y + dy * amount / distance, applied: player.seq };
  }
  return Player { ...player, applied: player.seq };
}

pub function collected(seed: Seed, before: Player, after: Player): boolean {
  let dx = after.x - before.x;
  let dy = after.y - before.y;
  let length = dx * dx + dy * dy;
  let fraction: f64 = 0;
  if (length > 0.0) {
    fraction = ((seed.x - before.x) * dx + (seed.y - before.y) * dy) / length;
  }
  fraction = fraction.max(0.0).min(1.0);
  let x = before.x + dx * fraction - seed.x;
  let y = before.y + dy * fraction - seed.y;
  return x * x + y * y <= 144.0;
}

pub function advance(state: State, now: i64): void {
  if (state.count.get() == 0) {
    return;
  }
  let previous = state.updated.get();
  if let Some(last) = previous {
    if (now - last < PERIOD) {
      return;
    }
  }
  let seconds: f64 = 0.0;
  if let Some(last) = previous {
    seconds = ((now - last) as f64 / 1000000000.0).min(0.1);
  }
  state.updated.set(Some(now));
  state.tick.set(state.tick.get() + 1);
  if let Some(restart) = state.restart.get() {
    if (now >= restart) {
      start_round(state, now);
    }
  }
  if (state.restart.get().is_none() && now >= state.ends.get()) {
    state.restart.set(Some(now + 10000000000));
  }
  let seeds = state.seeds.get();
  state.seeds.set([] as Seed[]);
  let index: i32 = 0;
  while (index < seeds.len()) {
    if let Some(ready) = seeds[index].ready {
      if (ready <= now) {
        seeds = seeds.with(index, new_seed(state, index));
      }
    }
    index = index + 1;
  }
  let players = state.players.get();
  state.players.set([] as Player[]);
  let visited: i32 = 0;
  while (visited < players.len()) {
    let at = (visited + (state.tick.get() % (players.len() as i64)) as i32) % players.len();
    let before = players[at];
    if (before.key != "" && now - before.seen > 60000000000) {
      players = players.with(at, empty_player());
      state.count.set(state.count.get() - 1);
      visited = visited + 1;
      continue;
    }
    if (before.key != "" && state.restart.get().is_none()) {
      let player = move(Player { ...before }, seconds);
      let column = (player.x / 128.0) as i32;
      let row = (player.y / 80.0) as i32;
      let cy = (row - 1).max(0);
      while (cy <= (row + 1).min(7)) {
        let cx = (column - 1).max(0);
        while (cx <= (column + 1).min(7)) {
          let pos = cy * 8 + cx;
          let seed = seeds[pos];
          if (seed.ready.is_none() && collected(seed, before, player)) {
            seeds = seeds.with(pos, Seed { ...seed, ready: Some(now + 1200000000) });
            player = Player { ...player, score: player.score + 1 };
            state.pooled.set(state.pooled.get() + 1);
          }
          cx = cx + 1;
        }
        cy = cy + 1;
      }
      players = players.with(at, player);
    }
    visited = visited + 1;
  }
  state.players.set(players);
  state.seeds.set(seeds);
  if (state.pooled.get() >= GOAL && state.restart.get().is_none()) {
    state.restart.set(Some(now + 10000000000));
  }
  state.packet.set(snapshot(state, now));
}
```

## Binary protocol

All integers use little-endian order. A destination has an unsigned 32-bit sequence number and two 16-bit coordinates.
Coordinates use sixteenth-world-unit precision. Invalid lengths and destinations close the connection.

A snapshot starts with a 32-byte header: `FC`, version, phase, tick, round, participant count, remaining milliseconds, team score, goal, seed count, width, and height.

Each participant uses twelve bytes: identifier, x, y, score, and applied input sequence.
Each seed uses five bytes: x, y, and availability. A separate twelve-byte `FA` frame acknowledges input and reports server time.

The output buffer has its final length before the first write. Owned `fip` helpers update it in place.
Every connection reads the same completed buffer. Slow writers skip superseded snapshots while their transport flushes.

```fern
<<Binary protocol>>=
fip function put16(own out: u8[], at: i32, value: i32): u8[] {
  return out.with(at, (value & 255) as u8).with(at + 1, (value >> 8 & 255) as u8);
}

fip function put32(own out: u8[], at: i32, value: i64): u8[] {
  out = put16(out, at, (value & 65535) as i32);
  return put16(out, at + 2, (value >> 16 & 65535) as i32);
}

function read16(bytes: u8[], at: i32): i32 {
  return bytes[at] as i32 | bytes[at + 1] as i32 << 8;
}

function read32(bytes: u8[], at: i32): i64 {
  return read16(bytes, at) as i64 | read16(bytes, at + 2) as i64 << 16;
}

pub function input(state: State, seat: Seat, bytes: u8[]): Result[i64, string] {
  if (bytes.len() != 8) {
    return Err("send an eight-byte destination");
  }
  let seq = read32(bytes, 0);
  let x = read16(bytes, 4) as f64 / 16.0;
  let y = read16(bytes, 6) as f64 / 16.0;
  if (seq == 0 || x > WIDTH as f64 || y > HEIGHT as f64) {
    return Err("invalid destination");
  }
  let players = state.players.get();
  if (seat.at >= players.len() || players[seat.at].key != seat.key) {
    return Err("seat expired");
  }
  let old = players[seat.at];
  if (seq <= old.seq) {
    return Err("input sequence must increase");
  }
  state.players.set([] as Player[]);
  players = players.with(seat.at, Player { ...old, tx: x, ty: y, seq: seq, seen: monotonic_ns() });
  state.players.set(players);
  state.inputs.set(state.inputs.get() + 1);
  return Ok(seq);
}

pub function snapshot(state: State, now: i64): u8[] {
  let out = __alloc_u8(32 + state.count.get() * 12 + 64 * 5);
  let phase: u8 = 0;
  if (state.restart.get().is_some()) {
    phase = 1;
  }
  out = out.with(0, 70).with(1, 67).with(2, 1).with(3, phase).with(30, 0).with(31, 0);
  out = put32(out, 4, state.tick.get());
  out = put16(out, 8, state.round.get());
  out = put16(out, 10, state.count.get());
  let end = state.restart.get().unwrap_or(state.ends.get());
  out = put32(out, 12, ((end - now) / 1000000).max(0));
  out = put32(out, 16, state.pooled.get() as i64);
  out = put32(out, 20, GOAL as i64);
  out = put16(out, 24, 64);
  out = put16(out, 26, WIDTH);
  out = put16(out, 28, HEIGHT);
  let at: i32 = 32;
  let id: i32 = 1;
  for player in state.players.get() {
    if (player.key != "") {
      out = put16(out, at, id);
      out = put16(out, at + 2, (player.x * 16.0) as i32);
      out = put16(out, at + 4, (player.y * 16.0) as i32);
      out = put16(out, at + 6, player.score);
      out = put32(out, at + 8, player.applied);
      at = at + 12;
    }
    id = id + 1;
  }
  for seed in state.seeds.get() {
    out = put16(out, at, (seed.x * 16.0) as i32);
    out = put16(out, at + 2, (seed.y * 16.0) as i32);
    let available: u8 = 0;
    if (seed.ready.is_none()) {
      available = 1;
    }
    out = out.with(at + 4, available);
    at = at + 5;
  }
  return out;
}

function acknowledgement(seq: i64, started: i64): u8[] {
  let out = __alloc_u8(12);
  out = out.with(0, 70).with(1, 65).with(2, 1).with(3, 0);
  out = put32(out, 4, seq);
  return put32(out, 8, (monotonic_ns() - started) / 1000);
}
```

## WebSocket session

Each producer wakes at the shared snapshot deadline. It checks the shared clock before any socket write.
There is no per-participant notification descriptor. A blocked writer does not create a queue of old snapshots.

A close releases the seat immediately. Its producer ends within one snapshot period.
The lease check also closes an inactive connection after sixty seconds without input.

```fern
<<WebSocket session>>=
function connected(state: State, seat: Seat, now: i64): boolean {
  let players = state.players.get();
  return seat.at < players.len() && players[seat.at].key == seat.key && now - players[seat.at].seen <= 60000000000;
}

pub function socket(state: State, request: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let seat = reserve(state)?;
  let opened = cell_new(false);
  let alive = cell_new(true);
  let sent = cell_new(0 as i64);
  let handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {
      advance(state, monotonic_ns());
      let welcome = connection.send_text(Welcome { kind: "welcome", id: seat.at + 1, capacity: CAPACITY, snapshot_hz: 10, speed: 180, instance: state.instance }.to_json());
      let packet = connection.send_binary(state.packet.get());
      sent.set(state.tick.get());
      opened.set(true);
    },
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let started = monotonic_ns();
      match (message) {
        Binary(bytes) => {
          match (input(state, seat, bytes)) {
            Ok(seq) => {
              let result = connection.send_binary(acknowledgement(seq, started));
              if let websocket.Dropped = result {
                connection.close(1013, "participant cannot receive acknowledgements");
              }
            },
            Err(error) => {
              connection.close(1008, error);
            }
          }
        },
        Text(_) => {
          connection.close(1003, "send binary destinations");
        }
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => {
      alive.set(false);
      release(state, seat);
    },
  };
  let options = websocket.Options { ...websocket.options(), compression: false, max_message: 8, backpressure: 65536, max_backpressure: 262144 };
  match (websocket.accept_with_push(request, options, handlers, (connection: websocket.Conn): void => {
    while (alive.get() && !async.task_cancelled()) {
      if (!opened.get()) {
        let waited = async.wait_any([] as i32[], 1);
        continue;
      }
      let now = monotonic_ns();
      if (!connected(state, seat, now)) {
        connection.close(1001, "participant inactive");
        let result = connection.flush();
        break;
      }
      advance(state, now);
      if (state.tick.get() != sent.get()) {
        let queued = connection.send_binary(state.packet.get());
        if let websocket.Dropped = queued {
          connection.close(1013, "participant cannot receive snapshots");
          break;
        }
        sent.set(state.tick.get());
        if let websocket.Dropped = connection.flush() {
          break;
        }
      }
      let delay = ((state.updated.get().unwrap_or(now) + PERIOD - monotonic_ns() + 999999) / 1000000).max(1).min(100) as i32;
      let waited = async.wait_any([] as i32[], delay);
    }
    alive.set(false);
    release(state, seat);
  })) {
    Ok(response) => {
      return Ok(response);
    },
    Err(error) => {
      alive.set(false);
      release(state, seat);
      return Err(error);
    }
  }
}
```

## Check movement and segment collection

```fern test name=gather-example
<<State and seats>>
<<Movement and collection>>
<<Binary protocol>>
function main(): i32 {
  let player = Player { ...empty_player(), key: "test", x: 0, y: 0, tx: 100, ty: 0 };
  let moved = move(Player { ...player }, 10.0);
  if (moved.x > 18.001 || moved.x < 17.999) { return 1; }
  if (!collected(Seed { x: 9, y: 0, ready: None }, player, moved)) { return 2; }
  if (collected(Seed { x: 9, y: 20, ready: None }, player, moved)) { return 3; }
  return 0;
}
```
