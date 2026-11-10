# The network of a user process

A user process of a kernel opens sockets with the Linux calls, and `std/user_process` answers them. This module holds the state behind those sockets: the interface, one `std/tcp` listener, the connections that the process did not accept yet, and the bytes that each connection received. It is the socket half of #15096, part of `docs/BARE-METAL-PLAN.md`.

The listener owns one port and accepts each SYN into a free slot of `tcp.CONNECTIONS`. `pump` takes the frames of the interface, sends what the connections have to send, and records the events. A completed handshake goes to the queue that `accept` takes from. The bytes of a connection wait in its slot until `read` takes them, so a connection can receive before the process accepts it.

A slot is used again after its connection closes. Thus each accept of a slot gives it a new generation, and the process names a connection by its slot and its generation. A call with an old generation finds a connection that is gone.

## Contracts

`listen` gives None when the process already listens: one listener reads every TCP segment of the interface. `read` gives None when the connection has no bytes and has not ended, so the caller waits or gives `EAGAIN`. It gives an empty array at the end of the stream, and for a connection that is gone. `write` gives false for a connection that is gone. The clock is in milliseconds, as for `std/tcp`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct Net>>

<<pub function (n: Net) pump>>

<<the connections>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module holds the sockets of a user process over std/tcp.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/array";
import "std/ip";
import "std/option";
import "std/tcp";
```

### `pub struct Net`

```fern
<<pub struct Net>>=
pub struct Net { iface: ip.Interface, iss: u32, listener: Option[tcp.Listener], queue: i32[], rx: u8[][], ended: boolean[], gone: boolean[], gen: i32[], closed: i32 }

// The network of a process on the interface. iss is the seed of the initial
// sequence numbers.
pub function new(iface: ip.Interface, iss: u32): Net {
  let rx: u8[][] = [];
  let flags: boolean[] = [];
  let gen: i32[] = [];
  for i in 0..tcp.CONNECTIONS {
    rx = rx.append([]);
    flags = flags.append(true);
    gen = gen.append(0);
  }
  return Net { iface: iface, iss: iss, listener: None, queue: [], rx: rx, ended: flags, gone: flags, gen: gen, closed: 0 };
}

// Listens on port, or None when the process already listens.
pub function (n: Net) listen(port: i32): Option[Net] {
  if (n.listener.is_some()) {
    return None;
  }
  return Some(Net { ...n, listener: Some(tcp.listen(n.iface, port, n.iss)) });
}

// Stops the listener. Each connection is then gone.
pub function (n: Net) unlisten(): Net {
  let gone = n.gone;
  for i in 0..gone.len() {
    gone = gone.with(i, true);
  }
  return Net { ...n, listener: None, queue: [], gone: gone };
}

// The connections that closed since the process started to listen.
pub function (n: Net) closed_count(): i32 {
  return n.closed;
}
```

### `pub function (n: Net) pump`

```fern
<<pub function (n: Net) pump>>=
pub function (n: Net) pump(now: i64): Net {
  let Some(l) = n.listener else {
    return n;
  };
  let (polled, events) = l.poll(now);
  let next = Net { ...n, listener: Some(polled) };
  for e in events {
    next = next.record(e);
  }
  return next;
}

function (n: Net) record(e: tcp.Event): Net {
  match (e) {
    Accepted(id) => {
      return Net { ...n, queue: n.queue.append(id), rx: n.rx.with(id, []), ended: n.ended.with(id, false), gone: n.gone.with(id, false), gen: n.gen.with(id, n.gen[id] + 1) };
    },
    Data(id, bytes) => {
      return Net { ...n, rx: n.rx.with(id, n.rx[id].concat(bytes)) };
    },
    Finished(id) => {
      return Net { ...n, ended: n.ended.with(id, true) };
    },
    Closed(id) => {
      return Net { ...n, ended: n.ended.with(id, true), gone: n.gone.with(id, true), queue: without(n.queue, id), closed: n.closed + 1 };
    }
  }
}

function without(ids: i32[], id: i32): i32[] {
  let out: i32[] = [];
  for i in ids {
    if (i != id) {
      out = out.append(i);
    }
  }
  return out;
}
```

### `the connections`

```fern
<<the connections>>=
// True when a connection waits for accept.
pub function (n: Net) pending(): boolean {
  return n.queue.len() > 0;
}

// The slot and the generation of the oldest connection that waits.
pub function (n: Net) accept(): (Net, Option[(i32, i32)]) {
  if (n.queue.len() == 0) {
    return (n, None);
  }
  let id = n.queue[0];
  let rest: i32[] = [];
  for i in 1..n.queue.len() {
    rest = rest.append(n.queue[i]);
  }
  return (Net { ...n, queue: rest }, Some((id, n.gen[id])));
}

pub function (n: Net) live(id: i32, gen: i32): boolean {
  return n.gen[id] == gen && !n.gone[id];
}

// True when read gives bytes or the end without a wait.
pub function (n: Net) readable(id: i32, gen: i32): boolean {
  return !n.live(id, gen) || n.rx[id].len() > 0 || n.ended[id];
}

// At most max of the bytes that the connection received.
pub function (n: Net) read(id: i32, gen: i32, max: i32): (Net, Option[u8[]]) {
  if (!n.live(id, gen)) {
    return (n, Some([]));
  }
  let have = n.rx[id];
  if (have.len() == 0) {
    if (n.ended[id]) {
      return (n, Some([]));
    }
    return (n, None);
  }
  if (have.len() <= max) {
    return (Net { ...n, rx: n.rx.with(id, []) }, Some(have));
  }
  return (Net { ...n, rx: n.rx.with(id, array.slice(have, max, have.len())) }, Some(array.slice(have, 0, max)));
}

pub function (n: Net) write(id: i32, gen: i32, data: u8[]): (Net, boolean) {
  let Some(l) = n.listener else {
    return (n, false);
  };
  if (!n.live(id, gen)) {
    return (n, false);
  }
  return (Net { ...n, listener: Some(l.write(id, data)) }, true);
}

// Ends the stream of the process. The connection closes when the peer ends too.
pub function (n: Net) close(id: i32, gen: i32): Net {
  let Some(l) = n.listener else {
    return n;
  };
  if (!n.live(id, gen)) {
    return n;
  }
  return Net { ...n, listener: Some(l.close(id)) };
}

// The address and the port of the peer of a connection.
pub function (n: Net) peer(id: i32): (u8[], i32) {
  let Some(l) = n.listener else {
    return ([0u8, 0u8, 0u8, 0u8], 0);
  };
  let c = l.connections[id];
  return (c.remote, c.remote_port);
}

// The port that the process listens on, or 0.
pub function (n: Net) port(): i32 {
  let Some(l) = n.listener else {
    return 0;
  };
  return l.port;
}
```
