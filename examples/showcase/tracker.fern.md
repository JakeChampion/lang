# Torrent tracker

A WebTorrent tracker introduces browsers that share a file. It does not carry the file.
Each browser opens a WebSocket to the tracker and announces the info hash it wants. The tracker passes WebRTC offers and answers between the peers of one swarm. The peers then connect directly.

This tracker speaks the WebTorrent tracker protocol, so a WebTorrent client can join a swarm that the showcase page started.
It is not an open tracker. It accepts only a swarm that the torrent page registered, and it forgets a swarm one hour after the registration.
A swarm takes at most 12 peers, the tracker keeps at most 64 swarms, and a registered file is at most 64 MiB.

```fern
<<*>>=
<<Imports and limits>>
<<Registration>>
<<Binary strings>>
<<Announce>>
<<Connection>>
```

## Imports and limits

```fern
<<Imports and limits>>=
import "std/http";
import "std/http/websocket";
import "std/json";
import "std/hex";
import "std/array";
import "std/option";
import "./common";
import "./live";

const MAX_SWARMS: i32 = 64;

const MAX_PEERS: i32 = 12;

const MAX_BYTES: i64 = 67108864;

const LIFETIME_NS: i64 = 3600000000000;

const INTERVAL_S: i32 = 120;

pub struct Swarm { hash: string, name: string, length: i64, expires: i64, peers: string[] }

struct Peer { conn: string, id: string, hash: string, outbox: string[] }

pub struct State { swarms: Cell[Swarm[]], peers: Cell[Peer[]], live: live.State }

pub function state(notifications: live.State): State {
  return State { swarms: cell_new([] as Swarm[]), peers: cell_new([] as Peer[]), live: notifications };
}
```

## Registration

`POST /torrent/register` registers the info hash that the page computed, in hexadecimal, with the file name and length.
The request also removes expired swarms. A second registration of one hash keeps the first expiry, so a swarm lives one hour at most.

```fern
<<Registration>>=
@derive(json.FromJson)
struct Offer { info_hash: string, name: string, length: i64 }

@derive(json.Json)
struct Registered { info_hash: string, expires_in: i64, peers: i32 }

function live_swarms(state: State, now: i64): Swarm[] {
  let kept: Swarm[] = [];
  for swarm in state.swarms.get() {
    if (swarm.expires > now) {
      kept = kept.append(swarm);
    }
  }
  state.swarms.set(kept);
  return kept;
}

function find_swarm(state: State, hash: string): Option[Swarm] {
  for swarm in live_swarms(state, monotonic_ns()) {
    if (swarm.hash == hash) {
      return Some(swarm);
    }
  }
  return None;
}

function store_swarm(state: State, swarm: Swarm): void {
  let swarms: Swarm[] = [];
  for s in state.swarms.get() {
    if (s.hash != swarm.hash) {
      swarms = swarms.append(s);
    }
  }
  state.swarms.set(swarms.append(swarm));
}

function is_hash(text: string): boolean {
  if (text.len() != 40) {
    return false;
  }
  for c in text.bytes() {
    if (!(c >= b'0' && c <= b'9' || c >= b'a' && c <= b'f')) {
      return false;
    }
  }
  return true;
}

pub function register(state: State, req: HttpRequest): Result[HttpResponse, http.HttpError] {
  let offer = http.body_json[Offer](req).map_err((e: http.BodyError): http.HttpError => http.fail(400, "Send info_hash, name, and length as JSON."))?;
  if (!is_hash(offer.info_hash)) {
    return Err(http.fail(400, "The info hash must be 40 lowercase hexadecimal digits."));
  }
  if (offer.length <= 0 || offer.length > MAX_BYTES) {
    return Err(http.fail(413, "The showcase tracker takes files of 64 MiB or less."));
  }
  if (offer.name.len() == 0 || offer.name.len() > 200) {
    return Err(http.fail(400, "The file name must have 1 to 200 bytes."));
  }
  let now = monotonic_ns();
  let swarms = live_swarms(state, now);
  let swarm: Swarm = Swarm { hash: offer.info_hash, name: offer.name, length: offer.length, expires: now + LIFETIME_NS, peers: [] };
  match (find_swarm(state, offer.info_hash)) {
    Some(existing) => {
      swarm = existing;
    },
    None => {
      if (swarms.len() >= MAX_SWARMS) {
        return Err(http.fail(503, "The tracker holds its maximum of swarms. Try again later."));
      }
      store_swarm(state, swarm);
    }
  }
  return Ok(http.json_of(Registered { info_hash: swarm.hash, expires_in: (swarm.expires - now) / 1000000000, peers: swarm.peers.len() }).with_header("Cache-Control", "no-store"));
}
```

## Binary strings

The WebTorrent protocol writes the 20-byte info hash and peer ID as JSON strings with one character for each byte.
JSON text is UTF-8, so a byte from 128 to 255 arrives as two bytes. These functions convert between that form and hexadecimal.
`latin1` returns an empty array for a string that is not 20 such characters.

```fern
<<Binary strings>>=
function latin1(s: string): u8[] {
  let b = s.bytes();
  let out: u8[] = [];
  let i = 0;
  while (i < b.len()) {
    let c = b[i];
    if (c < 128) {
      out = out.append(c);
      i = i + 1;
    } else if ((c == 194 as u8 || c == 195 as u8) && i + 1 < b.len()) {
      out = out.append(((c & 31) << 6 | b[i + 1] & 63) as u8);
      i = i + 2;
    } else {
      return [];
    }
  }
  if (out.len() != 20) {
    return [];
  }
  return out;
}

function binary_id(value: Option[string]): string {
  let Some(s) = value else {
    return "";
  };
  let raw = latin1(s);
  if (raw.len() == 0) {
    return "";
  }
  return hex.encode(raw);
}
```

## Announce

An announce names the swarm, the sender's peer ID, and up to ten WebRTC offers.
The tracker replies with the swarm's size and gives each offer to a different peer of the swarm. The offer carries the sender's peer ID.
An announce with an answer and `to_peer_id` goes to that one peer. The `stopped` event removes the sender from the swarm.

The tracker reads only the fields that it routes. It copies an offer or an answer as JSON and does not read the SDP.

```fern
<<Announce>>=
function reply(pairs: (string, json.JsonValue)[]): string {
  return json.json_encode(json.JObject(json.from(pairs)));
}

function failure(text: json.JsonValue, reason: string): string {
  return reply([("action", json.JString("announce")), ("info_hash", text), ("failure reason", json.JString(reason))]);
}

function deliver(state: State, conn: string, message: string): void {
  let peers: Peer[] = [];
  for peer in state.peers.get() {
    if (peer.conn == conn && peer.outbox.len() < 64) {
      peers = peers.append(Peer { ...peer, outbox: peer.outbox.append(message) });
    } else {
      peers = peers.append(peer);
    }
  }
  state.peers.set(peers);
  live.notify(state.live, conn, "");
}

// Removes and returns the messages queued for conn.
function take(state: State, conn: string): string[] {
  let queued: string[] = [];
  let peers: Peer[] = [];
  for peer in state.peers.get() {
    if (peer.conn == conn) {
      queued = peer.outbox;
      peers = peers.append(Peer { ...peer, outbox: [] as string[] });
    } else {
      peers = peers.append(peer);
    }
  }
  state.peers.set(peers);
  return queued;
}

function join(state: State, conn: string, id: string, swarm: Swarm): boolean {
  let peers: Peer[] = [];
  let outbox: string[] = [];
  for peer in state.peers.get() {
    if (peer.conn == conn) {
      if (peer.hash != swarm.hash) {
        leave(state, conn);
      }
      outbox = peer.outbox;
    }
  }
  if (!contains(swarm.peers, conn)) {
    if (swarm.peers.len() >= MAX_PEERS) {
      return false;
    }
    store_swarm(state, Swarm { ...swarm, peers: swarm.peers.append(conn) });
  }
  for peer in state.peers.get() {
    if (peer.conn != conn) {
      peers = peers.append(peer);
    }
  }
  state.peers.set(peers.append(Peer { conn: conn, id: id, hash: swarm.hash, outbox: outbox }));
  return true;
}

function contains(xs: string[], x: string): boolean {
  for y in xs {
    if (y == x) {
      return true;
    }
  }
  return false;
}

pub function leave(state: State, conn: string): void {
  let swarms: Swarm[] = [];
  for swarm in state.swarms.get() {
    let kept: string[] = [];
    for p in swarm.peers {
      if (p != conn) {
        kept = kept.append(p);
      }
    }
    swarms = swarms.append(Swarm { ...swarm, peers: kept });
  }
  state.swarms.set(swarms);
}

function peer_of(state: State, hash: string, id: string): Option[Peer] {
  for peer in state.peers.get() {
    if (peer.hash == hash && peer.id == id) {
      return Some(peer);
    }
  }
  return None;
}

function announce(state: State, conn: string, msg: json.JsonValue): string {
  let hash_text = json.json_get(msg, "info_hash").unwrap_or(json.JNull);
  let hash = binary_id(json.json_get_string(msg, "info_hash"));
  let id = binary_id(json.json_get_string(msg, "peer_id"));
  if (hash.len() == 0 || id.len() == 0) {
    return failure(hash_text, "info_hash and peer_id must each be 20 bytes");
  }
  let Some(swarm) = find_swarm(state, hash) else {
    return failure(hash_text, "this tracker serves only torrents that the Fern showcase registered in the last hour");
  };
  let peer_text = json.json_get(msg, "peer_id").unwrap_or(json.JNull);
  if (json.json_get_string(msg, "event").unwrap_or("") == "stopped") {
    leave(state, conn);
    return "";
  }
  if (!join(state, conn, id, swarm)) {
    return failure(hash_text, "this swarm already has 12 peers");
  }
  if let Some(answer) = json.json_get(msg, "answer") {
    let target = binary_id(json.json_get_string(msg, "to_peer_id"));
    if let Some(to) = peer_of(state, hash, target) {
      deliver(state, to.conn, reply([("action", json.JString("announce")), ("info_hash", hash_text), ("peer_id", peer_text), ("answer", answer), ("offer_id", json.json_get(msg, "offer_id").unwrap_or(json.JNull))]));
    }
    return "";
  }
  let current = find_swarm(state, hash).unwrap_or(swarm);
  let offers = json.json_get_array(msg, "offers").unwrap_or([]);
  let k = 0;
  for other in current.peers {
    if (other != conn && k < offers.len() && k < 10) {
      deliver(state, other, reply([("action", json.JString("announce")), ("info_hash", hash_text), ("peer_id", peer_text), ("offer", json.json_get(offers[k], "offer").unwrap_or(json.JNull)), ("offer_id", json.json_get(offers[k], "offer_id").unwrap_or(json.JNull))]));
      k = k + 1;
    }
  }
  return reply([("action", json.JString("announce")), ("info_hash", hash_text), ("interval", json.JNumber(INTERVAL_S.to_string())), ("complete", json.JNumber((current.peers.len() - 1).to_string())), ("incomplete", json.JNumber("0"))]);
}

function scrape(state: State, msg: json.JsonValue): string {
  let hash_text = json.json_get(msg, "info_hash").unwrap_or(json.JNull);
  let peers = 0;
  if let Some(swarm) = find_swarm(state, binary_id(json.json_get_string(msg, "info_hash"))) {
    peers = swarm.peers.len();
  }
  return reply([("action", json.JString("scrape")), ("info_hash", hash_text), ("complete", json.JNumber(peers.to_string())), ("incomplete", json.JNumber("0"))]);
}
```

## Connection

Each WebSocket gets a live subscription in a room of its own, so a message for one peer wakes only that peer.
The push loop sends the queued messages. Close removes the peer from its swarm.

```fern
<<Connection>>=
pub function socket(state: State, req: HttpRequest): Result[HttpResponse, dyn http.ToResponse] {
  let conn: string = common.token();
  let subscription: live.Subscription = live.subscribe(state.live, conn)?;
  let handlers: websocket.Handlers = websocket.Handlers {
    open: (connection: websocket.Conn): void => {},
    message: (connection: websocket.Conn, message: websocket.Message): void => {
      match (message) {
        Text(text) => {
          let Ok(msg) = json.parse(text) else {
            connection.close(1008, "send JSON");
            return;
          };
          let action = json.json_get_string(msg, "action").unwrap_or("");
          let answer = "";
          if (action == "announce") {
            answer = announce(state, conn, msg);
          } else if (action == "scrape") {
            answer = scrape(state, msg);
          }
          if (answer.len() > 0) {
            let sent = connection.send_text(answer);
          }
        },
        Binary(_) => {
          connection.close(1003, "send JSON");
        }
      }
    },
    drain: (connection: websocket.Conn): void => {},
    close: (connection: websocket.Conn, code: i32, reason: string): void => {
      leave(state, conn);
      let peers: Peer[] = [];
      for peer in state.peers.get() {
        if (peer.conn != conn) {
          peers = peers.append(peer);
        }
      }
      state.peers.set(peers);
      live.cancel(subscription);
    },
  };
  let options: websocket.Options = websocket.Options { ...websocket.options(), max_message: 65536, backpressure: 65536, max_backpressure: 262144 };
  match (websocket.accept_with_push(req, options, handlers, (connection: websocket.Conn): void => {
    while (live.wait(subscription)) {
      for message in take(state, conn) {
        let sent = connection.send_text(message);
      }
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
