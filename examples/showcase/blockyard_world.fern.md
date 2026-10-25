# Blockyard: the authoritative world

Clients submit movement controls and block requests. They cannot submit a position, velocity, or score.
Each world has 512 leased seats. A replacement seat never inherits a previous connection's authority.

The simulation advances once per 100 milliseconds. Four collision substeps prevent passage through thin walls.
Nearby snapshots use a counting-sort spatial index. Everyone in one interest cell shares the same completed binary packet.

```fern
<<*>>=
<<World state>>
<<Seats and controls>>
<<Movement>>
<<Edits>>
<<Clock and spatial index>>
<<Wire protocol>>
```

## World state

Terrain is procedural. The world stores only blocks that differ from that terrain.
Chunk indexes let a new participant request nearby edits without a scan of the whole world.
The persistence callback commits a batch before the simulation publishes it.

```fern
<<World state>>=
import "core/map";
import "std/array";
import "std/option";
import "std/result";
import "std/f64";
import "std/json";
import "./voxel";
import "./common";

pub const CAPACITY: i32 = 512;

pub const MAX_EDITS: i32 = 131072;

pub const PERIOD: i64 = 100000000;

pub struct Player {
  key: string,
  x: f64,
  y: f64,
  z: f64,
  vy: f64,
  grounded: boolean,
  mx: f64,
  mz: f64,
  up: f64,
  yaw: i32,
  pitch: i32,
  flags: i32,
  seq: i64,
  applied: i64,
  edit_seq: i64,
  edited: Option[i64],
  travelled: Option[i64],
  seen: i64,
}

pub struct Seat { at: i32, key: string }

pub struct Proposal { seat: Seat, nonce: i64, action: i32, material: i32, x: i32, y: i32, z: i32 }

@derive(json.Json, json.FromJson)
pub struct Change { x: i32, y: i32, z: i32, material: i32, actor: i32, nonce: i64 }

pub struct Event { serial: i64, lease: string, change: Change, error: string }

pub struct Hit { x: i32, y: i32, z: i32, ax: i32, ay: i32, az: i32 }

pub struct Index { starts: i32[], ids: i32[] }

pub struct World {
  name: string,
  players: Cell[Player[]],
  count: Cell[i32],
  updated: Cell[Option[i64]],
  tick: Cell[i64],
  blocks: Cell[Map[i32, i32]],
  chunks: Cell[Map[i32, (i32, i32)[]]],
  versions: Cell[Map[i32, i64]],
  revision: Cell[i64],
  pending: Cell[Proposal[]],
  history: Cell[Event[]],
  serial: Cell[i64],
  fault: Cell[string],
  index: Cell[Index],
  packets: Cell[Map[i32, u8[]]],
  deltas: Cell[Map[string, string]],
  inputs: Cell[i64],
  persist: (Map[i32, i32], Change[], i64) => Result[void, string],
}

pub function key(x: i32, y: i32, z: i32): i32 {
  return (y * 512 + z) * 512 + x;
}

pub function chunk_key(x: i32, z: i32): i32 {
  return z / 16 * 32 + x / 16;
}

pub function local_key(x: i32, y: i32, z: i32): i32 {
  return (y * 16 + z % 16) * 16 + x % 16;
}

pub function material(blocks: Map[i32, i32], x: i32, y: i32, z: i32): i32 {
  if (x < 0 || z < 0 || x >= 512 || z >= 512 || y < 0 || y >= 48) {
    return voxel.terrain(x, y, z);
  }
  if let Some(value) = blocks.get(key(x, y, z)) {
    return value;
  }
  return voxel.terrain(x, y, z);
}

pub function world(name: string, blocks: Map[i32, i32], revision: i64, persist: (Map[i32, i32], Change[], i64) => Result[void, string]): World {
  let value = World {
    name: name, players: cell_new([] as Player[]), count: cell_new(0), updated: cell_new(None as Option[i64]), tick: cell_new(0 as i64),
    blocks: cell_new(blocks), chunks: cell_new(map_new(32) as Map[i32, (i32, i32)[]]), versions: cell_new(map_new(32) as Map[i32, i64]), revision: cell_new(revision),
    pending: cell_new([] as Proposal[]), history: cell_new([] as Event[]), serial: cell_new(0 as i64), fault: cell_new(""),
    index: cell_new(Index { starts: [], ids: [] }), packets: cell_new(map_new(16) as Map[i32, u8[]]), deltas: cell_new(map_new(16) as Map[string, string]), inputs: cell_new(0 as i64), persist: persist,
  };
  for entry in blocks.entries() {
    let x = entry.0 % 512;
    let z = entry.0 / 512 % 512;
    let y = entry.0 / 262144;
    index_change(value, Change { x: x, y: y, z: z, material: entry.1, actor: 0, nonce: 0 }, revision);
  }
  return value;
}

function empty_player(): Player {
  return Player { key: "", x: 256.5, y: 14, z: 256.5, vy: 0, grounded: false, mx: 0, mz: 0, up: 0, yaw: 0, pitch: 0, flags: 0, seq: 0, applied: 0, edit_seq: 0, edited: None, travelled: None, seen: 0 };
}
```

## Seats and controls

Control packets have a fixed length and increasing sequence numbers. Movement vectors have a maximum magnitude of one.
The server retains directions between inputs, so network timing cannot increase a player's speed.
An inactive connection expires after sixty seconds.

```fern
<<Seats and controls>>=
pub function reserve(world: World, now: i64): Result[Seat, string] {
  advance(world, now);
  if (world.fault.get() != "") {
    return Err(world.fault.get());
  }
  if (world.count.get() >= CAPACITY) {
    return Err("This world is full.");
  }
  let players = world.players.get();
  world.players.set([] as Player[]);
  let at: i32 = 0;
  while (at < players.len() && players[at].key != "") {
    at = at + 1;
  }
  let lease = common.token();
  let angle = (at % 32) as f64 * 0.196349540849362;
  let radius = 2.0 + (at / 32 % 8) as f64;
  let x = 256.5 + angle.sin() * radius;
  let z = 256.5 + angle.cos() * radius;
  let player = Player { ...empty_player(), key: lease, x: x, z: z, y: 16.0 + (at / 256) as f64 * 2.0, seen: now };
  if (at == players.len()) {
    players = players.append(player);
  } else {
    players = players.with(at, player);
  }
  world.players.set(players);
  world.count.set(world.count.get() + 1);
  return Ok(Seat { at: at, key: lease });
}

pub function owns(world: World, seat: Seat): boolean {
  let players = world.players.get();
  return seat.at >= 0 && seat.at < players.len() && players[seat.at].key == seat.key;
}

pub function release(world: World, seat: Seat): void {
  if (!owns(world, seat)) {
    return;
  }
  let players = world.players.get();
  world.players.set([] as Player[]);
  world.players.set(players.with(seat.at, empty_player()));
  world.count.set(world.count.get() - 1);
  if (world.count.get() == 0) {
    world.pending.set([] as Proposal[]);
  }
}

function signed(byte: u8): i32 {
  if (byte > 127) {
    return byte as i32 - 256;
  }

  return byte as i32;
}

pub function control(world: World, seat: Seat, bytes: u8[], now: i64): Result[i64, string] {
  if (bytes.len() != 16 || bytes[0] != 1 || bytes[1] > 3 || bytes[5] != 0 || bytes[14] != 0 || bytes[15] != 0) {
    return Err("Invalid movement packet.");
  }
  if (!owns(world, seat)) {
    return Err("This seat expired.");
  }
  let seq = read32(bytes, 10);
  let old = world.players.get()[seat.at];
  let pitch = read16(bytes, 8);
  if (pitch > 32767) {
    pitch = pitch - 65536;
  }
  if (seq <= old.seq || pitch < -15500 || pitch > 15500) {
    return Err("Invalid movement sequence or view angle.");
  }
  let mx = (signed(bytes[2]) as f64 / 127.0).max(-1.0).min(1.0);
  let mz = (signed(bytes[3]) as f64 / 127.0).max(-1.0).min(1.0);
  let length = (mx * mx + mz * mz).sqrt().max(1.0);
  let players = world.players.get();
  world.players.set([] as Player[]);
  world.players.set(players.with(seat.at, Player { ...old, seq: seq, mx: mx / length, mz: mz / length, up: (signed(bytes[4]) as f64 / 127.0).max(-1.0).min(1.0), flags: bytes[1] as i32, yaw: read16(bytes, 6), pitch: pitch, seen: now }));
  world.inputs.set(world.inputs.get() + 1);
  return Ok(seq);
}

pub function travel(world: World, seat: Seat, bytes: u8[], now: i64): Result[Player, string] {
  if (bytes.len() != 16 || bytes[0] != 3 || bytes[1] > 3) {
    return Err("Invalid landmark packet.");
  }
  for at in 2..10 {
    if (bytes[at] != 0) {
      return Err("Invalid landmark packet.");
    }
  }
  if (bytes[14] != 0 || bytes[15] != 0 || !owns(world, seat)) {
    return Err("Invalid landmark request or expired seat.");
  }
  let old = world.players.get()[seat.at];
  let seq = read32(bytes, 10);
  if (seq <= old.seq) {
    return Err("Invalid movement sequence.");
  }
  if let Some(previous) = old.travelled {
    if (now - previous < 3000000000) {
      return Err("Wait three seconds between landmark trips.");
    }
  }
  let x: f64 = 256.5;
  let z: f64 = 256.5;
  if (bytes[1] == 1 || bytes[1] == 3) {
    x = 96.5;
  }
  if (bytes[1] == 2) {
    x = 416.5;
  }
  if (bytes[1] == 1 || bytes[1] == 2) {
    z = 96.5;
  }
  if (bytes[1] == 3) {
    z = 416.5;
  }
  x = x + (seat.at % 16) as f64 * 0.4 - 3.0;
  z = z + (seat.at / 16 % 16) as f64 * 0.4 - 3.0;
  let player = Player { ...old, x: x, y: 25, z: z, vy: 0, grounded: false, mx: 0, mz: 0, up: 0, seq: seq, applied: seq, travelled: Some(now), seen: now };
  let players = world.players.get();
  world.players.set([] as Player[]);
  world.players.set(players.with(seat.at, player));
  return Ok(player);
}
```

## Movement

The mask contains only nearby solid blocks. Water does not stop movement.
The browser calls the same collision and vertical functions through Wasm.

```fern
<<Movement>>=
pub function mask(blocks: Map[i32, i32], x: f64, y: f64, z: f64): (i32, i32) {
  let bits: u64 = 0;
  for at in 0..45 {
    let bx = x.floor() as i32 - 1 + at % 3;
    let by = y.floor() as i32 - 1 + at / 9;
    let bz = z.floor() as i32 - 1 + at / 3 % 3;
    if (voxel.solid(material(blocks, bx, by, bz)) != 0) {
      bits = bits | 1 as u64 << at as u64;
    }
  }
  return ((bits as u32) as i32, (bits >> 32) as i32);
}

pub function move(blocks: Map[i32, i32], own player: Player, seconds: f64): Player {
  let dt = seconds.max(0.0).min(0.1) / 4.0;
  let fly: i32 = player.flags >> 1 & 1;
  let speed = 5.5;
  if (fly != 0) {
    speed = 7.0;
  }
  for substep in 0..4 {
    let grounded: i32 = 0;
    if (player.grounded) {
      grounded = 1;
    }
    let vy = voxel.vertical(player.vy, grounded, player.flags & 1, fly, player.up, dt);
    let (lo, hi) = mask(blocks, player.x, player.y, player.z);
    let x = voxel.clip(player.x, player.y, player.z, player.mx * speed * dt, 0, lo, hi);
    let (loz, hiz) = mask(blocks, x, player.y, player.z);
    let z = voxel.clip(x, player.y, player.z, player.mz * speed * dt, 2, loz, hiz);
    let (loy, hiy) = mask(blocks, x, player.y, z);
    let y = voxel.clip(x, player.y, z, vy * dt, 1, loy, hiy);
    let clipped = (y - player.y - vy * dt).abs() > 0.000001;
    player = Player { ...player, x: x, y: y, z: z, vy: vy, grounded: clipped && vy < 0.0 };
    if (clipped) {
      player = Player { ...player, vy: 0 };
    }
  }
  return Player { ...player, applied: player.seq };
}
```

## Edits

The server traces the player's view to the first solid block within six meters.
A placement must use the adjacent face and must leave every player's body clear.
Each participant can request eight edits per second. A tick accepts at most 512 requests.

Successful batches enter the journal before they enter live state. A storage error closes the world until restart.
The bounded event history supports reconnects and slow writers. An expired history cursor requires a fresh chunk snapshot.

```fern
<<Edits>>=
pub function ray(blocks: Map[i32, i32], player: Player): Option[Hit] {
  let yaw = player.yaw as f64 * 6.283185307179586 / 65536.0;
  let pitch = player.pitch as f64 * 3.141592653589793 / 32768.0;
  let dx = 0.0 - yaw.sin() * pitch.cos();
  let dy = pitch.sin();
  let dz = 0.0 - yaw.cos() * pitch.cos();
  let ex = player.x;
  let ey = player.y + 1.55;
  let ez = player.z;
  let x = ex.floor() as i32;
  let y = ey.floor() as i32;
  let z = ez.floor() as i32;
  let ax = x;
  let ay = y;
  let az = z;
  let sx: i32 = 1;
  let sy: i32 = 1;
  let sz: i32 = 1;
  if (dx < 0.0) {
    sx = -1;
  }
  if (dy < 0.0) {
    sy = -1;
  }
  if (dz < 0.0) {
    sz = -1;
  }
  let tx: f64 = 1e30;
  let ty: f64 = 1e30;
  let tz: f64 = 1e30;
  let stepx: f64 = 1e30;
  let stepy: f64 = 1e30;
  let stepz: f64 = 1e30;
  if (dx.abs() > 0.0000001) {
    stepx = 1.0 / dx.abs();
    tx = (x as f64 + (sx + 1) as f64 / 2.0 - ex) / dx;
  }
  if (dy.abs() > 0.0000001) {
    stepy = 1.0 / dy.abs();
    ty = (y as f64 + (sy + 1) as f64 / 2.0 - ey) / dy;
  }
  if (dz.abs() > 0.0000001) {
    stepz = 1.0 / dz.abs();
    tz = (z as f64 + (sz + 1) as f64 / 2.0 - ez) / dz;
  }
  let distance: f64 = 0;
  while (distance <= 6.0) {
    if (voxel.solid(material(blocks, x, y, z)) != 0) {
      return Some(Hit { x: x, y: y, z: z, ax: ax, ay: ay, az: az });
    }
    ax = x;
    ay = y;
    az = z;
    if (tx <= ty && tx <= tz) {
      x = x + sx;
      distance = tx;
      tx = tx + stepx;
    } else if (ty <= tz) {
      y = y + sy;
      distance = ty;
      ty = ty + stepy;
    } else {
      z = z + sz;
      distance = tz;
      tz = tz + stepz;
    }
  }
  return None;
}

pub function queue_edit(world: World, seat: Seat, bytes: u8[], now: i64): Result[i64, string] {
  if (bytes.len() != 16 || bytes[0] != 2 || bytes[1] > 1 || bytes[2] < 1 || bytes[2] > 10 || bytes[3] != 0 || bytes[14] != 0 || bytes[15] != 0) {
    return Err("Invalid block packet.");
  }
  if (!owns(world, seat)) {
    return Err("This seat expired.");
  }
  let old = world.players.get()[seat.at];
  let nonce = read32(bytes, 10);
  if (nonce <= old.edit_seq) {
    return Err("Block sequence must increase.");
  }
  if let Some(last) = old.edited {
    if (now - last < 125000000) {
      return Err("Wait a moment before the next block.");
    }
  }
  if (world.pending.get().len() >= 512) {
    return Err("The world is busy. Try that block again.");
  }
  let x = read16(bytes, 4);
  let y = read16(bytes, 6);
  let z = read16(bytes, 8);
  if (x >= 512 || z >= 512 || y < 1 || y >= 47) {
    return Err("That block is outside the build area.");
  }
  let players = world.players.get();
  world.players.set([] as Player[]);
  world.players.set(players.with(seat.at, Player { ...old, edit_seq: nonce, edited: Some(now), seen: now }));
  world.pending.set(world.pending.get().append(Proposal { seat: seat, nonce: nonce, action: bytes[1] as i32, material: bytes[2] as i32, x: x, y: y, z: z }));
  return Ok(nonce);
}

pub function validate(world: World, blocks: Map[i32, i32], request: Proposal): Result[Change, string] {
  if (!owns(world, request.seat)) {
    return Err("This seat expired.");
  }
  let player = world.players.get()[request.seat.at];
  let Some(hit) = ray(blocks, player) else {
    return Err("No block is within reach.");
  };
  let value = 0;
  if (request.action == 0) {
    if (hit.x != request.x || hit.y != request.y || hit.z != request.z) {
      return Err("That block is behind another block or out of reach.");
    }
  } else {
    if (hit.ax != request.x || hit.ay != request.y || hit.az != request.z) {
      return Err("Place blocks against a visible face.");
    }
    value = request.material;
    for other in world.players.get() {
      if (other.key != "" && other.x + 0.3 > request.x as f64 && other.x - 0.3 < (request.x + 1) as f64
        && other.y + 1.7 > request.y as f64 && other.y < (request.y + 1) as f64
        && other.z + 0.3 > request.z as f64 && other.z - 0.3 < (request.z + 1) as f64) {
        return Err("A player is in the way.");
      }
    }
  }
  if (blocks.len() >= MAX_EDITS && !blocks.contains(key(request.x, request.y, request.z))) {
    return Err("This world has reached its block limit.");
  }
  return Ok(Change { x: request.x, y: request.y, z: request.z, material: value, actor: request.seat.at + 1, nonce: request.nonce });
}

function index_change(world: World, change: Change, revision: i64): void {
  let chunk = chunk_key(change.x, change.z);
  let local = local_key(change.x, change.y, change.z);
  let chunks = world.chunks.get();
  world.chunks.set(map_new(32) as Map[i32, (i32, i32)[]]);
  let entries = chunks.get(chunk).unwrap_or([] as (i32, i32)[]);
  let next: (i32, i32)[] = [];
  for entry in entries {
    if (entry.0 != local) {
      next = next.append(entry);
    }
  }
  if (change.material != voxel.terrain(change.x, change.y, change.z)) {
    next = next.append((local, change.material));
  }
  world.chunks.set(chunks.insert(chunk, next));
  let versions = world.versions.get();
  world.versions.set(map_new(32) as Map[i32, i64]);
  world.versions.set(versions.insert(chunk, revision));
}

function commit(world: World): void {
  let pending = world.pending.get();
  world.pending.set([] as Proposal[]);
  if (pending.len() == 0) {
    return;
  }
  let blocks = world.blocks.get();
  let changes: Change[] = [];
  let events: Event[] = [];
  let serial = world.serial.get();
  for request in pending {
    serial = serial + 1;
    let stub = Change { x: request.x, y: request.y, z: request.z, material: 0, actor: request.seat.at + 1, nonce: request.nonce };
    match (validate(world, blocks, request)) {
      Ok(change) => {
        let at = key(change.x, change.y, change.z);
        if (change.material == voxel.terrain(change.x, change.y, change.z)) {
          blocks = blocks.remove(at);
        } else {
          blocks = blocks.insert(at, change.material);
        }
        changes = changes.append(change);
        events = events.append(Event { serial: serial, lease: request.seat.key, change: change, error: "" });
      },
      Err(error) => {
        events = events.append(Event { serial: serial, lease: request.seat.key, change: stub, error: error });
      }
    }
  }
  if (changes.len() > 0) {
    let revision = world.revision.get() + changes.len() as i64;
    if let Err(error) = world.persist(blocks, changes, revision) {
      world.fault.set(error);
      return;
    }
    world.blocks.set(blocks);
    for change in changes {
      index_change(world, change, revision);
    }
    world.revision.set(revision);
  }
  let history = world.history.get();
  world.history.set([] as Event[]);
  for event in events {
    history = history.append(event);
  }
  if (history.len() > 4096) {
    history = history.drop(history.len() - 4096);
  }
  world.history.set(history);
  world.serial.set(serial);
}
```

## Clock and spatial index

Any ready producer can advance the clock. A blocked socket cannot own or stop the simulation.
The counting sort visits every player once. A snapshot then visits nine adjacent spatial buckets.
Empty worlds retain no timer task.

```fern
<<Clock and spatial index>>=
pub function sector(player: Player): i32 {
  return (player.z / 32.0) as i32 * 16 + (player.x / 32.0) as i32;
}

pub function spatial(players: Player[]): Index {
  let counts: i32[] = [];
  for at in 0..256 {
    counts = counts.append(0);
  }
  for player in players {
    if (player.key != "") {
      let cell = sector(player);
      counts = counts.with(cell, counts[cell] + 1);
    }
  }
  let starts: i32[] = [0];
  for at in 0..256 {
    starts = starts.append(starts[at] + counts[at]);
    counts = counts.with(at, 0);
  }
  let ids: i32[] = [];
  for at in 0..starts[256] {
    ids = ids.append(0);
  }
  let id: i32 = 0;
  for player in players {
    if (player.key != "") {
      let cell = sector(player);
      ids = ids.with(starts[cell] + counts[cell], id);
      counts = counts.with(cell, counts[cell] + 1);
    }
    id = id + 1;
  }
  return Index { starts: starts, ids: ids };
}

pub function advance(world: World, now: i64): void {
  if (world.count.get() == 0 || world.fault.get() != "") {
    return;
  }
  let seconds: f64 = 0;
  if let Some(last) = world.updated.get() {
    if (now - last < PERIOD) {
      return;
    }
    seconds = ((now - last) as f64 / 1000000000.0).max(0.0).min(0.1);
  }
  world.updated.set(Some(now));
  world.tick.set(world.tick.get() + 1);
  let players = world.players.get();
  world.players.set([] as Player[]);
  let at: i32 = 0;
  while (at < players.len()) {
    let player = players[at];
    if (player.key != "") {
      if (now - player.seen > 60000000000) {
        players = players.with(at, empty_player());
        world.count.set(world.count.get() - 1);
      } else {
        players = players.with(at, move(world.blocks.get(), Player { ...player }, seconds));
      }
    }
    at = at + 1;
  }
  world.players.set(players);
  commit(world);
  world.index.set(spatial(world.players.get()));
  world.packets.set(map_new(16) as Map[i32, u8[]]);
  world.deltas.set(map_new(16) as Map[string, string]);
}

pub function event_start(history: Event[], cursor: i64): i32 {
  let low: i32 = 0;
  let high = history.len();
  while (low < high) {
    let mid = low + (high - low) / 2;
    if (history[mid].serial <= cursor) {
      low = mid + 1;
    } else {
      high = mid;
    }
  }
  return low;
}

@derive(json.Json)
struct Delta { kind: string, cursor: i64, revision: i64, reset: boolean, blocks: Change[] }

pub function delta(world: World, cell: i32, cursor: i64): string {
  let cache_key = cursor.to_string() + "/" + cell.to_string();
  if let Some(cached) = world.deltas.get().get(cache_key) {
    return cached;
  }
  let history = world.history.get();
  let reset = history.len() > 0 && cursor < history[0].serial - 1;
  let changes: Change[] = [];
  if (!reset) {
    let at = event_start(history, cursor);
    while (at < history.len()) {
      let event = history[at];
      let change = event.change;
      if (event.error == "" && (change.x / 32 - cell % 16).abs() <= 1 && (change.z / 32 - cell / 16).abs() <= 1) {
        changes = changes.append(change);
      }
      at = at + 1;
    }
  }
  let result = Delta { kind: "edits", cursor: world.serial.get(), revision: world.revision.get(), reset: reset, blocks: changes }.to_json();
  let cache = world.deltas.get();
  world.deltas.set(map_new(16) as Map[string, string]);
  world.deltas.set(cache.insert(cache_key, result));
  return result;
}
```

## Wire protocol

Integers use little-endian order. A sixteen-byte `BP` header precedes sixteen bytes per nearby player.
Coordinates use sixty-fourths of a meter. Input acknowledgements use twelve-byte `BA` frames.
Owned `fip` writers fill a buffer at its final size. No writer can change another socket's published packet.

```fern
<<Wire protocol>>=
pub fip function put16(own out: u8[], at: i32, value: i32): u8[] {
  return out.with(at, (value & 255) as u8).with(at + 1, (value >> 8 & 255) as u8);
}

pub fip function put32(own out: u8[], at: i32, value: i64): u8[] {
  out = put16(out, at, (value & 65535) as i32);
  return put16(out, at + 2, (value >> 16 & 65535) as i32);
}

pub function read16(bytes: u8[], at: i32): i32 {
  return bytes[at] as i32 | bytes[at + 1] as i32 << 8;
}

pub function read32(bytes: u8[], at: i32): i64 {
  return read16(bytes, at) as i64 | read16(bytes, at + 2) as i64 << 16;
}

pub function packet(world: World, cell: i32): u8[] {
  if let Some(cached) = world.packets.get().get(cell) {
    return cached;
  }
  let index = world.index.get();
  if (index.starts.len() != 257) {
    return [] as u8[];
  }
  let count: i32 = 0;
  let z = (cell / 16 - 1).max(0);
  while (z <= (cell / 16 + 1).min(15)) {
    let x = (cell % 16 - 1).max(0);
    while (x <= (cell % 16 + 1).min(15)) {
      let bucket = z * 16 + x;
      count = count + index.starts[bucket + 1] - index.starts[bucket];
      x = x + 1;
    }
    z = z + 1;
  }
  let out = __alloc_u8(16 + count * 16);
  out = out.with(0, 66).with(1, 80).with(2, 1).with(3, 0).with(12, (cell % 16) as u8).with(13, (cell / 16) as u8).with(14, 0).with(15, 0);
  out = put32(out, 4, world.tick.get());
  out = put16(out, 8, count);
  out = put16(out, 10, world.count.get());
  let at: i32 = 16;
  z = (cell / 16 - 1).max(0);
  while (z <= (cell / 16 + 1).min(15)) {
    let x = (cell % 16 - 1).max(0);
    while (x <= (cell % 16 + 1).min(15)) {
      let bucket = z * 16 + x;
      let row = index.starts[bucket];
      while (row < index.starts[bucket + 1]) {
        let id = index.ids[row];
        let player = world.players.get()[id];
        out = put16(out, at, id + 1);
        out = put16(out, at + 2, (player.x * 64.0) as i32);
        out = put16(out, at + 4, (player.y * 64.0) as i32);
        out = put16(out, at + 6, (player.z * 64.0) as i32);
        out = put16(out, at + 8, player.yaw);
        let state = (player.vy * 256.0) as i32 << 2 | player.flags >> 1 & 1;
        if (player.grounded) {
          state = state | 2;
        }
        out = put16(out, at + 10, state);
        out = put32(out, at + 12, player.applied);
        at = at + 16;
        row = row + 1;
      }
      x = x + 1;
    }
    z = z + 1;
  }
  let cache = world.packets.get();
  world.packets.set(map_new(16) as Map[i32, u8[]]);
  world.packets.set(cache.insert(cell, out));
  return out;
}

pub function acknowledgement(seq: i64, started: i64): u8[] {
  let out = __alloc_u8(12);
  out = out.with(0, 66).with(1, 65).with(2, 1).with(3, 0);
  out = put32(out, 4, seq);
  return put32(out, 8, (monotonic_ns() - started) / 1000);
}
```
