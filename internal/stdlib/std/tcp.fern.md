# TCP over an interface

A kernel that answers an HTTP request needs TCP. This module binds the connections of `std/tcp/conn` to a `std/ip` interface: it is the third slice of the network stack of `docs/BARE-METAL-PLAN.md` (#14259). A `Listener` owns one port and up to `CONNECTIONS` connections. It takes the TCP segments the interface receives. It accepts a SYN into a free slot, resets a segment that reaches no connection, and sends what every connection has to send.

`poll` takes the frames the interface has and gives the events of the connections: a handshake completed, bytes received, the peer finished, a connection closed. The caller answers with `write` and `close`, and the next `poll` sends. A connection is a small number, the index of its slot, from `Accepted` until `Closed`.

The clock is the caller's. Every call takes the time in milliseconds, which the retransmission timers of the connections use.

A program that serves UDP and TCP on one interface routes the packets itself: it takes them from the interface with `poll` of `std/ip` and gives the TCP segments to `deliver`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct Listener>>

<<pub enum Event>>

<<receiving>>

<<sending>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module binds TCP connections to a std/ip interface.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/ip";
import "std/option";
import "std/tcp/conn";
```

### `pub struct Listener`

`listen` takes the interface, the port and a seed for the initial sequence numbers. Every connection the listener accepts takes the next number from the seed.

```fern
<<pub struct Listener>>=
pub const CONNECTIONS: i32 = 8;

pub struct Listener { iface: ip.Interface, port: i32, connections: conn.Connection[], iss: u32 }

pub function listen(iface: ip.Interface, port: i32, iss: u32): Listener {
  let connections: conn.Connection[] = [];
  for i in 0..CONNECTIONS {
    connections = connections.append(conn.none());
  }
  return Listener { iface: iface, port: port, connections: connections, iss: iss };
}

// The connections that are open, in any state but Closed.
pub function (l: Listener) active(): i32 {
  let n = 0;
  for c in l.connections {
    if (!c.is_closed()) {
      n += 1;
    }
  }
  return n;
}

// The state of connection `id`.
pub function (l: Listener) state(id: i32): conn.State {
  return l.connections[id].state;
}
```

### `pub enum Event`

`Accepted` is a completed handshake. `Data` is bytes the peer sent, in order. `Finished` says the peer closed its side, so no more data comes. `Closed` says the connection is gone and its number is free. A connection that never completed its handshake gets no event.

```fern
<<pub enum Event>>=
pub enum Event { Accepted(i32), Data(i32, u8[]), Finished(i32), Closed(i32) }
```

### `receiving`

`poll` takes every frame the interface has and gives the TCP segments for the port to `deliver`. It then sends what the connections have to send, and gives the events. A program that polls calls it in a loop. A program that waits on the IRQ line of the device calls it once for each wake, after it acknowledges the device. `deliver` takes one segment: to its connection, to a free slot when it is a SYN, or a reset when neither.

```fern
<<receiving>>=
pub function (l: Listener) poll(now: i64): (Listener, Event[]) {
  let events: Event[] = [];
  while (true) {
    let Some(p) = l.iface.poll() else {
      break;
    };
    let Tcp(s) = p else {
      continue;
    };
    if (s.port != l.port) {
      continue;
    }
    let (delivered, found) = l.deliver(s, now);
    l = delivered;
    for e in found {
      events = events.append(e);
    }
  }
  let (flushed, more) = l.flush(now);
  for e in more {
    events = events.append(e);
  }
  return (flushed, events);
}

pub function (l: Listener) deliver(s: conn.Segment, now: i64): (Listener, Event[]) {
  let events: Event[] = [];
  for id in 0..CONNECTIONS {
    let before = l.connections[id];
    if (!before.is_closed() && before.matches(s)) {
      let (after, out) = before.receive(s, now);
      l.send_all(out);
      return l.replace(id, before, after);
    }
  }
  for id in 0..CONNECTIONS {
    if (l.connections[id].is_closed()) {
      let Some(accepted) = conn.accept(s, l.iss, now) else {
        break;
      };
      let (c, syn_ack) = accepted;
      let sent = l.iface.send_tcp(syn_ack);
      return (Listener { ...l, connections: l.connections.with(id, c), iss: l.iss + 64000 as u32 }, events);
    }
  }
  match (conn.reset(s)) {
    Some(r) => {
      let sent = l.iface.send_tcp(r);
    },
    None => {}
  }
  return (l, events);
}

// Stores the new state of connection `id` and names what changed.
function (l: Listener) replace(id: i32, before: conn.Connection, after: conn.Connection): (Listener, Event[]) {
  let events: Event[] = [];
  let known = !before.state.eq(SynReceived);
  if (before.state.eq(SynReceived) && after.state.eq(Established)) {
    events = events.append(Accepted(id));
    known = true;
  }
  if (after.received.len() > 0) {
    let (read, data) = after.read();
    after = read;
    events = events.append(Data(id, data));
  }
  if (known && !before.finished() && after.finished()) {
    events = events.append(Finished(id));
  }
  if (known && !before.is_closed() && after.is_closed()) {
    events = events.append(Closed(id));
  }
  return (Listener { ...l, connections: l.connections.with(id, after) }, events);
}
```

### `sending`

`write` queues bytes for a connection and `close` queues the end of the stream. `flush` sends what every connection has to send, and the retransmissions that are due.

```fern
<<sending>>=
pub function (l: Listener) write(id: i32, data: u8[]): Listener {
  return Listener { ...l, connections: l.connections.with(id, l.connections[id].write(data)) };
}

pub function (l: Listener) close(id: i32): Listener {
  return Listener { ...l, connections: l.connections.with(id, l.connections[id].close()) };
}

function (l: Listener) flush(now: i64): (Listener, Event[]) {
  let events: Event[] = [];
  for id in 0..CONNECTIONS {
    let before = l.connections[id];
    if (before.is_closed()) {
      continue;
    }
    let (after, out) = before.transmit(now);
    l.send_all(out);
    let (next, more) = l.replace(id, before, after);
    l = next;
    for e in more {
      events = events.append(e);
    }
  }
  return (l, events);
}

function (l: Listener) send_all(out: conn.Segment[]): void {
  for s in out {
    let sent = l.iface.send_tcp(s);
  }
}
```
