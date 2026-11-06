# Kernel TLS socket operations

This module configures kernel TLS for TCP sockets.

## Model

Kernel TLS helpers attach traffic keys to a TCP socket and transfer framed records. Read results distinguish progress, end of input, waits, cancellation, and failure.

## Contracts

An attachment result distinguishes kernel activation from an unchanged socket and a broken socket. The caller must handle each result before further socket use.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Attached>>

<<4 pub function attach(sock: Socket, keys: handshake.KernelKeys): Attached>>

<<5 function __send_all(sock: Socket, data: u8[]): i32>>

<<6 pub enum Read>>

<<7 pub function read(sock: Socket, wait: Duration): Read>>

<<8 function __ktls(sock: Socket, op: i32, arg: i32, data: u8[]): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module configures kernel TLS for TCP sockets.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/async";
import "std/time";
import "std/tls/handshake";
```

### `3 pub enum Attached`

```fern
<<3 pub enum Attached>>=
pub enum Attached { Kernel, Unchanged, Broken(i32) }
```

### `4 pub function attach(sock: Socket, keys: handshake.KernelKeys): Attached`

```fern
<<4 pub function attach(sock: Socket, keys: handshake.KernelKeys): Attached>>=
pub function attach(sock: Socket, keys: handshake.KernelKeys): Attached {
  let none: u8[] = [];
  if (__ktls(sock, 1, 0, none) < 0) {
    return Unchanged;
  }
  if (__ktls(sock, 3, 0, keys.receive) < 0) {
    return Unchanged;
  }
  let tx = __ktls(sock, 2, 0, keys.transmit);
  if (tx < 0) {
    return Broken(-tx);
  }
  return Kernel;
}

pub function install(sock: Socket, keys: handshake.KernelKeys): i32 {
  if (keys.receive.len() > 0) {
    let r = __ktls(sock, 3, 0, keys.receive);
    if (r < 0) {
      return r;
    }
  }
  if (keys.transmit.len() > 0) {
    let t = __ktls(sock, 2, 0, keys.transmit);
    if (t < 0) {
      return t;
    }
  }
  return 0;
}

pub function send_records(sock: Socket, framed: [u8]): i32 {
  let at = 0;
  while (framed.len() - at >= 5) {
    let len = framed[at + 3] as i32 << 8 | framed[at + 4] as i32;
    let t = framed[at] as i32;
    let content = __copy(framed[at + 5:at + 5 + len]);
    let r = 0;
    if (t == 23) {
      r = __send_all(sock, content);
    } else {
      r = __ktls(sock, 4, t, content);
    }
    if (r < 0) {
      return r;
    }
    at = at + 5 + len;
  }
  return 0;
}
```

### `5 function __send_all(sock: Socket, data: u8[]): i32`

```fern
<<5 function __send_all(sock: Socket, data: u8[]): i32>>=
function __send_all(sock: Socket, data: u8[]): i32 {
  let at = 0;
  while (at < data.len()) {
    let n = tcp_send_bytes(sock, __copy(data[at:data.len()]));
    if (n < 0) {
      return n;
    }
    if (n == 0) {
      return -104;
    }
    at += n;
  }
  return 0;
}
```

### `6 pub enum Read`

```fern
<<6 pub enum Read>>=
pub enum Read { Opened(i32, u8[]), StreamEnded, NothingYet, WaitCancelled, ReadFailed(i32) }
```

### `7 pub function read(sock: Socket, wait: Duration): Read`

```fern
<<7 pub function read(sock: Socket, wait: Duration): Read>>=
pub function read(sock: Socket, wait: Duration): Read {
  let limit = wait;
  if (limit.cmp(time.duration_millis(1)) < 0) {
    limit = time.duration_millis(1);
  }
  let set: i32[] = [sock.fd, 1];
  match (async.wait_any(set, Some(time.instant_from_nanos(monotonic_ns()).add(limit)))) {
    Woke(i) => {
      return read_now(sock);
    },
    TimedOut => {
      return Read.NothingYet;
    },
    Cancelled => {
      return Read.WaitCancelled;
    }
  }
  return Read.NothingYet;
}

pub function read_now(sock: Socket): Read {
  let buf = __alloc_u8(16385);
  let r = __ktls(sock, 5, 0, buf);
  if (r == -11) {
    return Read.NothingYet;
  }
  if (r < 0) {
    return Read.ReadFailed(-r);
  }
  let n = r & 16777215;
  let t = r >> 24;
  if (t == 0) {
    return Read.StreamEnded;
  }
  return Read.Opened(t, __copy(buf[0:n]));
}

pub function framed(t: i32, content: [u8]): u8[] {
  let out = [t as u8, 3 as u8, 3 as u8, (content.len() >> 8) as u8, (content.len() & 255) as u8];
  for b in content {
    out = out.append(b);
  }
  return out;
}

pub function alert(sock: Socket, code: i32): void {
  let body = [2 as u8, code as u8];
  __ktls(sock, 4, 21, body);
}
```

### `8 function __ktls(sock: Socket, op: i32, arg: i32, data: u8[]): i32`

```fern
<<8 function __ktls(sock: Socket, op: i32, arg: i32, data: u8[]): i32>>=
function __ktls(sock: Socket, op: i32, arg: i32, data: u8[]): i32 {
  if (target_os() == "linux" || target_os() == "android") {
    return tcp_ktls(sock, op, arg, data);
  }
  return -38;
}

function __copy(src: [u8]): u8[] {
  let out = __alloc_u8(src.len());
  let i = 0;
  while (i < src.len()) {
    out = out.with(i, src[i]);
    i += 1;
  }
  return out;
}
```
