# Virtio entropy device

A virtio entropy device gives random bytes from the host, for example from `/dev/urandom` of the VMM. This module is the driver. It opens the device over `std/virtio` and reads bytes from it. A kernel uses it as the source of `getrandom` for its processes (milestone 6 of `docs/BARE-METAL-PLAN.md`, #14265). The arm64 baseline has no `RNDR` instruction, so a kernel on it has no other source of entropy.

qemu gives a guest this device with `-device virtio-rng-device`, and Firecracker with its entropy device.

The driver negotiates only virtio 1.0. The device has one queue. One request is one descriptor that the device writes: a buffer of `CHUNK_BYTES` in one `std/dma` buffer. The device writes some of the buffer, and the used ring gives the number of bytes. A read of more bytes is a sequence of requests.

The driver polls. It submits one request at a time and waits on the used ring until the device returns it.

## Contracts

`open` gives None for a device that is not an entropy device, that refuses virtio 1.0, or that has no queue. `read` gives None when the device returns a request with no bytes, and otherwise exactly the bytes that were asked for.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<pub struct Rng>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads random bytes from a virtio entropy device.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/dma";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/raw";
import "std/virtio";
```

### `constants`

```fern
<<constants>>=
pub const CHUNK_BYTES: usize = 4096;
```

### `pub struct Rng`

```fern
<<pub struct Rng>>=
pub struct Rng { dev: virtio.Device, q: virtio.Queue, buf: dma.Buffer }

pub function open(dev: virtio.Device, f: frames.Frames, tables: pagetable.Tables): Option[Rng] {
  if (dev.id != virtio.ID_ENTROPY) {
    return None;
  }
  let Some(features) = dev.negotiate(0 as u32, virtio.FEATURE_VERSION_1_HIGH) else {
    return None;
  };
  if ((features.high & virtio.FEATURE_VERSION_1_HIGH) == 0 as u32) {
    return None;
  }
  let Some(q) = dev.queue(0, 1, f, tables) else {
    return None;
  };
  let Some(buf) = dma.alloc(f, tables, CHUNK_BYTES) else {
    return None;
  };
  dev.ready();
  return Some(Rng { dev: dev, q: q, buf: buf });
}

// The bytes of one request, of at most CHUNK_BYTES.
function (r: Rng) fill(): u8[] {
  r.buf.sync();
  r.q.set(0, r.buf.physical, CHUNK_BYTES as i32, virtio.DESC_WRITE, 0);
  r.q.submit(0);
  while (r.q.pending() == 0) {}
  let Some(done) = r.q.used() else {
    return [];
  };
  let acked = r.dev.interrupt_ack();
  r.buf.sync();
  let out: u8[] = [];
  let i = 0;
  while (i < done.len && i < CHUNK_BYTES as i32) {
    out = out.append(raw.load_u8(r.buf.virtual + i as usize));
    i += 1;
  }
  return out;
}

// n random bytes from the device.
pub function (r: Rng) read(n: i32): Option[u8[]] {
  let out: u8[] = [];
  while (out.len() < n) {
    let chunk = r.fill();
    if (chunk.len() == 0) {
      return None;
    }
    for b in chunk {
      if (out.len() < n) {
        out = out.append(b);
      }
    }
  }
  return Some(out);
}
```
