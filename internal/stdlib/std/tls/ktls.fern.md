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

<<4 pub function attach(fd: i32, keys: handshake.KernelKeys): Attached>>

<<5 function __send_all(fd: i32, data: u8[]): i32>>

<<6 pub enum Read>>

<<7 pub function recv(fd: i32, wait: Duration): Read>>

<<8 function __ktls(fd: i32, op: i32, arg: i32, data: u8[]): i32>>
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

### `4 pub function attach(fd: i32, keys: handshake.KernelKeys): Attached`

```fern
<<4 pub function attach(fd: i32, keys: handshake.KernelKeys): Attached>>=
pub function attach(fd: i32, keys: handshake.KernelKeys): Attached {
  let none: u8[] = [];
  if (__ktls(fd, 1, 0, none) < 0) {
    return Unchanged;
  }
  if (__ktls(fd, 3, 0, keys.receive) < 0) {
    return Unchanged;
  }
  let tx: i32 = __ktls(fd, 2, 0, keys.transmit);
  if (tx < 0) {
    return Broken(0 - tx);
  }
  return Kernel;
}

pub function install(fd: i32, keys: handshake.KernelKeys): i32 {
  if (keys.receive.len() > 0) {
    let r: i32 = __ktls(fd, 3, 0, keys.receive);
    if (r < 0) {
      return r;
    }
  }
  if (keys.transmit.len() > 0) {
    let t: i32 = __ktls(fd, 2, 0, keys.transmit);
    if (t < 0) {
      return t;
    }
  }
  return 0;
}

pub function send_records(fd: i32, framed: [u8]): i32 {
  let at: i32 = 0;
  while (framed.len() - at >= 5) {
    let len: i32 = framed[at + 3] as i32 << 8 | framed[at + 4] as i32;
    let t: i32 = framed[at] as i32;
    let content: u8[] = __copy(framed[at + 5:at + 5 + len]);
    let r: i32 = 0;
    if (t == 23) {
      r = __send_all(fd, content);
    } else {
      r = __ktls(fd, 4, t, content);
    }
    if (r < 0) {
      return r;
    }
    at = at + 5 + len;
  }
  return 0;
}
```

### `5 function __send_all(fd: i32, data: u8[]): i32`

```fern
<<5 function __send_all(fd: i32, data: u8[]): i32>>=
function __send_all(fd: i32, data: u8[]): i32 {
  let at: i32 = 0;
  while (at < data.len()) {
    let n: i32 = tcp_send_bytes(fd, __copy(data[at:data.len()]));
    if (n < 0) {
      return n;
    }
    if (n == 0) {
      return 0 - 104;
    }
    at = at + n;
  }
  return 0;
}
```

### `6 pub enum Read`

```fern
<<6 pub enum Read>>=
pub enum Read { Opened(i32, u8[]), StreamEnded, NothingYet, WaitCancelled, ReadFailed(i32) }
```

### `7 pub function recv(fd: i32, wait: Duration): Read`

```fern
<<7 pub function recv(fd: i32, wait: Duration): Read>>=
pub function recv(fd: i32, wait: Duration): Read {
  let wait_ms: i32 = wait.as_millis() as i32;
  if (wait_ms < 1) {
    wait_ms = 1;
  }
  let set: i32[] = [fd, 1];
  let woke: i32 = async.wait_any(set, wait_ms);
  if (woke == async.cancelled()) {
    return Read.WaitCancelled;
  }
  if (woke != 0) {
    return Read.NothingYet;
  }
  return read_now(fd);
}

pub function read_now(fd: i32): Read {
  let buf: u8[] = __alloc_u8(16385);
  let r: i32 = __ktls(fd, 5, 0, buf);
  if (r == 0 - 11) {
    return Read.NothingYet;
  }
  if (r < 0) {
    return Read.ReadFailed(0 - r);
  }
  let n: i32 = r & 16777215;
  let t: i32 = r >> 24;
  if (t == 0) {
    return Read.StreamEnded;
  }
  return Read.Opened(t, __copy(buf[0:n]));
}

pub function framed(t: i32, content: [u8]): u8[] {
  let out: u8[] = [t as u8, 3 as u8, 3 as u8, (content.len() >> 8) as u8, (content.len() & 255) as u8];
  for b in content {
    out = out.append(b);
  }
  return out;
}

pub function alert(fd: i32, code: i32): void {
  let body: u8[] = [2 as u8, code as u8];
  __ktls(fd, 4, 21, body);
}
```

### `8 function __ktls(fd: i32, op: i32, arg: i32, data: u8[]): i32`

```fern
<<8 function __ktls(fd: i32, op: i32, arg: i32, data: u8[]): i32>>=
function __ktls(fd: i32, op: i32, arg: i32, data: u8[]): i32 {
  if (target_os() == "linux" || target_os() == "android") {
    return tcp_ktls(fd, op, arg, data);
  }
  return 0 - 38;
}

function __copy(src: [u8]): u8[] {
  let out: u8[] = __alloc_u8(src.len());
  let i: i32 = 0;
  while (i < src.len()) {
    out = out.with(i, src[i]);
    i = i + 1;
  }
  return out;
}
```
