# Rally: a game with server authority

Two browsers share a Pong game over WebSocket. A solo game uses the same physics with a slow computer opponent.
The browser sends commands and paddle positions. Only the server changes ball positions and scores.

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
pub struct Input { kind: string, room: string, mode: string, y: f64 }

@derive(json.Json)
struct Frame { room: string, seat: i32, players: i32, game: Game, tick: i64 }

pub struct Room { id: string, game: Game, seats: string[], clock: i64 }

pub struct State { rooms: Cell[Room[]], messages: Cell[i64] }

struct Joined { id: string, seat: i32 }
```

## Advance the simulation

Coordinates use the unit square. Clamp elapsed time to 50 milliseconds so a late message cannot move through a wall.
Paddles have a maximum speed. The computer paddle moves more slowly than a player paddle.
Collision code reflects overshoot and changes the vertical velocity with the impact position.

```fern
<<Advance the simulation>>=
pub function state(): State {
  return State { rooms: cell_new([] as Room[]), messages: cell_new(0 as i64) };
}

pub function initial(solo: boolean): Game {
  return Game { x: 0.5, y: 0.5, vx: 0.42, vy: 0.21, left: 0.5, right: 0.5, left_score: 0, right_score: 0, running: false, solo: solo };
}

function approach(value: f64, target: f64, amount: f64): f64 {
  return value + (target - value).max(-amount).min(amount);
}

pub function advance(own game: Game, left: f64, right: f64, seconds: f64): Game {
  let dt: f64 = seconds.max(0.0).min(0.05);
  game = Game { ...game, left: approach(game.left, left.max(0.105).min(0.895), dt * 1.6) };
  let target: f64 = right;
  let speed: f64 = 1.6;
  if (game.solo) {
    target = game.y;
    speed = 0.55;
  }
  game = Game { ...game, right: approach(game.right, target.max(0.105).min(0.895), dt * speed) };
  if (!game.running) {
    return game;
  }
  let x: f64 = game.x + game.vx * dt;
  let y: f64 = game.y + game.vy * dt;
  let vx: f64 = game.vx;
  let vy: f64 = game.vy;
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
    for candidate in state.rooms.get() {
      if (candidate.id == input.room) {
        room = Some(candidate);
      }
    }
    if (room.is_none()) {
      return Err("this room no longer exists");
    }
  } else {
    let rooms: Room[] = state.rooms.get();
    if (rooms.len() >= 16) {
      let kept: Room[] = [];
      for candidate in rooms {
        if (candidate.seats[0] != "" || candidate.seats[1] != "") {
          kept = kept.append(candidate);
        }
      }
      rooms = kept;
    }
    if (rooms.len() >= 16) {
      return Err("all game rooms are in use");
    }
    let created: Room = Room { id: common.token(), game: initial(input.mode != "duo"), seats: ["", ""], clock: now_ns() };
    state.rooms.set(rooms.append(created));
    room = Some(created);
  }
  match (room) {
    Some(found) => {
      let seats: string[] = found.seats;
      let seat: i32 = -1;
      if (seats[0] == "") {
        seat = 0;
      } else if (!found.game.solo && seats[1] == "") {
        seat = 1;
      }
      if (seat < 0) {
        return Err("this room already has its players");
      }
      store(state, Room { ...found, seats: seats.with(seat, peer) });
      return Ok(Joined { id: found.id, seat: seat });
    },
    None => {
      return Err("room creation failed");
    }
  }
}

function store(state: State, room: Room): void {
  let rooms: Room[] = state.rooms.get();
  let at: i32 = 0;
  while (at < rooms.len()) {
    if (rooms[at].id == room.id) {
      state.rooms.set(rooms.with(at, room));
      return;
    }
    at = at + 1;
  }
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
Each accepted command returns a snapshot. Clients request snapshots at about 30 frames per second.
This request-driven model does no simulation work when no client sends input.

```fern
<<Apply a command>>=
function reply(state: State, joined: Joined, connection: websocket.Conn, input: Input): void {
  let Some(room) = find(state, joined.id) else {
    connection.close(1008, "room expired");
    return;
  };
  let game: Game = room.game;
  let now: i64 = now_ns();
  let left: f64 = game.left;
  let right: f64 = game.right;
  if (joined.seat == 0) {
    left = input.y;
  } else {
    right = input.y;
  }
  game = advance(game, left, right, (now - room.clock) as f64 / 1000000000.0);
  let seats: string[] = room.seats;
  let players: i32 = 0;
  if (seats[0] != "") {
    players = players + 1;
  }
  if (seats[1] != "") {
    players = players + 1;
  }
  if (input.kind == "start" && (game.solo || players == 2)) {
    game = Game { ...game, running: true };
  }
  if (input.kind == "pause") {
    game = Game { ...game, running: false };
  }
  if (input.kind == "reset") {
    game = initial(game.solo);
  }
  store(state, Room { ...room, game: game, clock: now });
  state.messages.set(state.messages.get() + 1);
  let frame: Frame = Frame { room: room.id, seat: joined.seat, players: players, game: game, tick: now / 1000000 };
  let sent: websocket.SendResult = connection.send_text(frame.to_json());
}
```

## Manage connection lifetime

A socket must join before it can send commands. Invalid JSON and oversized messages close the socket.
Each connection stores its joined seat in an optional cell. Close releases that seat and pauses the game.
The browser interpolates snapshots for smooth display without control of the authoritative state.

```fern
<<Manage connection lifetime>>=
pub function socket(state: State, req: HttpRequest): HttpResponse {
  let peer: string = common.token();
  let session: Cell[Option[Joined]] = cell_new(None as Option[Joined]);
  let handlers: websocket.Handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {},
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      match (message) {
        Text(text) => {
          if (text.len() > 512) {
            connection.close(1009, "input is too large");
            return;
          }
          match (Input.from_json(text)) {
            Ok(input) => {
              if (input.y != input.y || input.y < 0.0 || input.y > 1.0) {
                connection.close(1008, "invalid paddle position");
                return;
              }
              match (session.get()) {
                Some(joined) => {
                  if (input.kind != "tick" && input.kind != "start" && input.kind != "pause" && input.kind != "reset") {
                    connection.close(1008, "unknown command");
                    return;
                  }
                  reply(state, joined, connection, input);
                },
                None => {
                  if (input.kind != "join") {
                    connection.close(1008, "join a room first");
                    return;
                  }
                  match (join(state, input, peer)) {
                    Ok(joined) => {
                      session.set(Some(joined));
                      reply(state, joined, connection, input);
                    },
                    Err(message) => {
                      let sent: websocket.SendResult = connection.send_text("{\"error\":\"" + json.json_escape(message) + "\"}");
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
      match (session.get()) {
        Some(joined) => {
          match (find(state, joined.id)) {
            Some(room) => {
              let seats: string[] = room.seats;
              if (seats[joined.seat] == peer) {
                seats = seats.with(joined.seat, "");
              }
              store(state, Room { ...room, seats: seats, game: Game { ...room.game, running: false } });
            },
            None => {}
          }
        },
        None => {}
      }
    },
  };
  return http.respond(websocket.accept(req, websocket.options(), handlers));
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
