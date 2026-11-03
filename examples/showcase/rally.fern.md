# Rally: a game with server authority

Two browsers share a Pong game over WebSocket. A solo game uses the same physics with a slow computer opponent.
The browser sends commands and paddle positions. The server owns ball positions and scores. Visual prediction lets local controls respond before a reply arrives.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Game data>>
<<Advance the simulation>>
<<Assign player seats>>
<<Apply a command>>
<<Manage connection lifetime>>
```

## Game data

Derived JSON readers validate the input record before game logic runs.
A `Cell` owns the room array. Records inside that array contain values, not more cells.
The server keeps at most 16 rooms and reuses empty rooms.

```fern
<<Game data>>=
// The server owns the ball, scores, paddle limits, and shared game rooms.
import "std/http";
import "std/result";
import "std/option";
import "std/array";
import "std/http/websocket";
import "std/json";
import "std/f64";
import "./common";
import "./live";

@derive(json.Json)
pub struct Game {
  x: f64,
  y: f64,
  vx: f64,
  vy: f64,
  left: f64,
  right: f64,
  left_score: i32,
  right_score: i32,
  running: boolean,
  solo: boolean,
}

@derive(json.Json, json.FromJson)
pub struct Input { kind: string, room: string, mode: string, y: f64, seq: i64 }

@derive(json.Json)
struct Frame { room: string, seat: i32, players: i32, game: Game, tick: i64, seq: i64, server_us: i64 }

pub struct Room { id: string, game: Game, seats: string[], targets: f64[], clock: i64 }

pub struct State { rooms: Cell[Room[]], messages: Cell[i64], live: live.State }

struct Joined { id: string, seat: i32, seq: i64 }
```

## Advance the simulation

Coordinates use the unit square. Clamp elapsed time to 50 milliseconds so a late message cannot move through a wall.
Paddles have a maximum speed. The computer paddle moves more slowly than a player paddle.
Collision code reflects overshoot and changes the vertical velocity with the impact position.

```fern
<<Advance the simulation>>=
pub function state(): State {
  return State { rooms: cell_new([] as Room[]), messages: cell_new(0 as i64), live: live.state() };
}

pub function initial(solo: boolean): Game {
  return Game { x: 0.5, y: 0.5, vx: 0.42, vy: 0.21, left: 0.5, right: 0.5, left_score: 0, right_score: 0, running: false, solo: solo };
}

function approach(value: f64, target: f64, amount: f64): f64 {
  return value + (target - value).max(-amount).min(amount);
}

pub function advance(own game: Game, left: f64, right: f64, seconds: f64): Game {
  let dt = seconds.max(0.0).min(0.05);
  game = Game { ...game, left: approach(game.left, left.max(0.105).min(0.895), dt * 1.6) };
  let target = if (game.solo) { game.y } else { right };
  let speed = if (game.solo) { 0.55 } else { 1.6 };
  game = Game { ...game, right: approach(game.right, target.max(0.105).min(0.895), dt * speed) };
  if (!game.running) {
    return game;
  }
  let x = game.x + game.vx * dt;
  let y = game.y + game.vy * dt;
  let vx = game.vx;
  let vy = game.vy;
  if (y < 0.014) {
    y = 0.028 - y;
    vy = vy.abs();
  }
  if (y > 0.986) {
    y = 1.972 - y;
    vy = -vy.abs();
  }
  if (vx < 0.0 && game.x >= 0.067 && x < 0.067 && (y - game.left).abs() < 0.12) {
    x = 0.134 - x;
    vx = (vx.abs() * 1.035).min(0.85);
    vy = (y - game.left) * 3.2;
  }
  if (vx > 0.0 && game.x <= 0.933 && x > 0.933 && (y - game.right).abs() < 0.12) {
    x = 1.866 - x;
    vx = -(vx.abs() * 1.035).min(0.85);
    vy = (y - game.right) * 3.2;
  }
  if (x < -0.02) {
    return Game { ...game, x: 0.5, y: 0.5, vx: 0.42, vy: 0.21, right_score: game.right_score + 1 };
  }
  if (x > 1.02) {
    return Game { ...game, x: 0.5, y: 0.5, vx: -0.42, vy: -0.21, left_score: game.left_score + 1 };
  }
  return Game { ...game, x: x, y: y, vx: vx, vy: vy };
}
```

## Assign player seats

A connection joins one room and owns one seat. The server rejects invalid names and full rooms.
A missing room is an `Option`, while a rejected join is a `Result` with a reason.

```fern
<<Assign player seats>>=
function join(state: State, input: Input, peer: string): Result[Joined, string] {
  let room: Option[Room] = None;
  if (input.room != "") {
    if (!common.room_name(input.room)) {
      return Err("invalid room code");
    }
    room = find(state, input.room);
    if (room.is_none()) {
      return Err("this room no longer exists");
    }
  } else {
    let rooms = state.rooms.get();
    if (rooms.len() >= 16) {
      rooms = rooms |> array.filter((candidate: Room): boolean => candidate.seats[0] != "" || candidate.seats[1] != "");
    }
    if (rooms.len() >= 16) {
      return Err("all game rooms are in use");
    }
    let created = Room { id: common.token(), game: initial(input.mode != "duo"), seats: ["", ""], targets: [0.5, 0.5], clock: now_ns() };
    state.rooms.set(rooms.append(created));
    room = Some(created);
  }
  let Some(found) = room else {
    return Err("room creation failed");
  };
  let seats = found.seats;
  let seat = if (seats[0] == "") { 0 } else if (!found.game.solo && seats[1] == "") { 1 } else { -1 };
  if (seat < 0) {
    return Err("this room already has its players");
  }
  store(state, Room { ...found, seats: seats.with(seat, peer) });
  return Ok(Joined { id: found.id, seat: seat, seq: input.seq });
}

function store(state: State, room: Room): void {
  let rooms = state.rooms.get();
  for at in 0..rooms.len() {
    if (rooms[at].id == room.id) {
      state.rooms.set(rooms.with(at, room));
      return;
    }
  }
}

function occupied(room: Room): i32 {
  return room.seats |> array.count_where((seat: string): boolean => seat != "");
}

function find(state: State, id: string): Option[Room] {
  for room in state.rooms.get() {
    if (room.id == id) {
      return Some(room);
    }
  }
  return None;
}
```

## Apply a command

The monotonic clock supplies elapsed time. A client cannot submit a score or choose the elapsed time.
Each room retains both paddle targets. Every simulation step advances both paddles, even when the other player supplies the input.
Each accepted command returns a snapshot. Clients send coalesced inputs at about 60 frames per second without a stop-and-wait exchange.

This request-driven model does no simulation work when no client sends input.

```fern
<<Apply a command>>=
function reply(state: State, joined: Joined, connection: websocket.Conn, input: Input, started: i64): void {
  let Some(room) = find(state, joined.id) else {
    connection.close(1008, "room expired");
    return;
  };
  let now = now_ns();
  let targets = room.targets.with(joined.seat, input.y);
  let game = room.game;
  game = advance(game, targets[0], targets[1], (now - room.clock) as f64 / 1000000000.0);
  let players = occupied(room);
  match (input.kind) {
    "start" => {
      if (game.solo || players == 2) {
        game = Game { ...game, running: true };
      }
    },
    "pause" => {
      game = Game { ...game, running: false };
    },
    "reset" => {
      game = initial(game.solo);
    },
    _ => {}
  }
  store(state, Room { ...room, game: game, targets: targets, clock: now });
  state.messages.set(state.messages.get() + 1);
  let frame = Frame { room: room.id, seat: joined.seat, players: players, game: game, tick: now / 1000000, seq: input.seq, server_us: (now_ns() - started) / 1000 };
  let sent = connection.send_text(frame.to_json());
}
```

## Manage connection lifetime

A socket must join before it can send commands. Invalid JSON and oversized messages close the socket.
Each connection stores its joined seat in an optional cell. Close releases that seat and pauses the game.

The browser predicts its paddle, reconciles acknowledged inputs, and extrapolates the ball for at most 100 milliseconds.
A room publication wakes the other player immediately. A background producer sends the shared state without a client poll.

```fern
<<Manage connection lifetime>>=
pub function socket(state: State, req: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let subscription = live.subscribe(state.live, "")?;
  let peer = common.token();
  let session = cell_new(None as Option[Joined]);
  let handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => live.wake(subscription),
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      let started = now_ns();
      match (message) {
        Text(text) => {
          if (text.len() > 512) {
            connection.close(1009, "input is too large");
            return;
          }
          match (Input.from_json(text)) {
            Ok(input) => {
              if (input.y != input.y || input.y < 0.0 || input.y > 1.0 || input.seq < 0) {
                connection.close(1008, "invalid paddle position");
                return;
              }
              match (session.get()) {
                Some(joined) => {
                  if (input.kind != "tick" && input.kind != "start" && input.kind != "pause" && input.kind != "reset") {
                    connection.close(1008, "unknown command");
                    return;
                  }
                  let updated = Joined { ...joined, seq: input.seq };
                  session.set(Some(updated));
                  reply(state, updated, connection, input, started);
                  live.notify(state.live, joined.id, subscription.listener.id);
                },
                None => {
                  if (input.kind != "join") {
                    connection.close(1008, "join a room first");
                    return;
                  }
                  match (join(state, input, peer)) {
                    Ok(joined) => {
                      session.set(Some(joined));
                      live.move(subscription, joined.id);
                      reply(state, joined, connection, input, started);
                      live.notify(state.live, joined.id, subscription.listener.id);
                    },
                    Err(message) => {
                      let sent = connection.send_text(f"{{\"error\":\"{json.escape(message)}\"}}");
                      connection.close(1013, message);
                    }
                  }
                }
              }
            },
            Err(_) => {
              connection.close(1008, "invalid game input");
            }
          }
        },
        Binary(_) => {
          connection.close(1003, "send JSON input");
        }
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => {
      live.cancel(subscription);
      if let Some(joined) = session.get() {
        if let Some(room) = find(state, joined.id) {
          let seats = room.seats;
          if (seats[joined.seat] == peer) {
            seats = seats.with(joined.seat, "");
          }
          store(state, Room { ...room, seats: seats, game: Game { ...room.game, running: false } });
          live.notify(state.live, room.id, subscription.listener.id);
        }
      }
    },
  };
  let options = websocket.Options { ...websocket.options(), max_message: 1024, backpressure: 8192, max_backpressure: 65536 };
  match (websocket.accept_with_push(req, options, handlers, (connection: websocket.Conn): void => {
    while (live.wait(subscription)) {
      if let Some(joined) = session.get() {
        if let Some(room) = find(state, joined.id) {
          let frame = Frame { room: room.id, seat: joined.seat, players: occupied(room), game: room.game, tick: room.clock / 1000000, seq: joined.seq, server_us: 0 };
          let sent = connection.send_text(frame.to_json());
          if let Dropped = connection.flush() {
            break;
          }
        }
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

## Check the behavior

This example compiles against the same sections as the application. Run it with `fern -doctest`.

```fern test name=rally-example
<<Game data>>
<<Advance the simulation>>
function main(): i32 {
  let moved = advance(Game { ...initial(false), running: true }, 0.5, 0.5, 10.0);
  if (moved.x <= 0.5 || moved.x > 0.522) { return 1; }
  return 0;
}
```
