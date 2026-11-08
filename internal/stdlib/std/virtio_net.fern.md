# Virtio network device

A virtio-net device carries Ethernet frames between the guest and the host's network. This module is the driver. It opens the device over `std/virtio` and keeps a receive queue full of buffers. It sends a frame from a transmit buffer, and gives the frames the device returned. It is the floor of the network stack of `docs/BARE-METAL-PLAN.md` (#14259), and the first user of the DMA region (#14255).

The driver negotiates two features: the MAC address in the configuration space, and virtio 1.0. With virtio 1.0 every frame carries a 12-byte header in front of it. This driver writes a zero header on send and skips the header on receive. Each queue has 8 buffers of 2048 bytes in one `std/dma` buffer. A frame of up to 2036 bytes fits.

The driver polls. `receive` looks at the used ring once and returns the frame there, if any. A caller that waits for a frame calls it in a loop.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<pub struct Net>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a kernel a virtio network device.
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
// The MAC address is in the configuration space: feature bit 5.
const FEATURE_MAC: u32 = 32;

const HEADER_BYTES: usize = 12;

pub const SLOT_BYTES: usize = 2048;

pub const SLOTS: i32 = 8;

const RX: i32 = 0;

const TX: i32 = 1;
```

### `pub struct Net`

`open` takes a probed device of id `virtio.ID_NET` and negotiates the features. It builds the receive and the transmit queue with `SLOTS` entries each. It posts every receive buffer and starts the device. The receive buffers are the first `SLOTS` slots of the DMA buffer, and the transmit buffers the next `SLOTS`.

```fern
<<pub struct Net>>=
pub struct Net { dev: virtio.Device, rx: virtio.Queue, tx: virtio.Queue, slots: dma.Buffer, mac: u8[] }

pub function open(dev: virtio.Device, f: frames.Frames, tables: pagetable.Tables): Option[Net] {
  if (dev.id != virtio.ID_NET) {
    return None;
  }
  let Some(features) = dev.negotiate(FEATURE_MAC, virtio.FEATURE_VERSION_1_HIGH) else {
    return None;
  };
  if ((features.high & virtio.FEATURE_VERSION_1_HIGH) == 0 as u32) {
    return None;
  }
  let Some(rx) = dev.queue(RX, SLOTS, f, tables) else {
    return None;
  };
  let Some(tx) = dev.queue(TX, SLOTS, f, tables) else {
    return None;
  };
  if (rx.size < SLOTS || tx.size < SLOTS) {
    return None;
  }
  let Some(slots) = dma.alloc(f, tables, (2 * SLOTS) as usize * SLOT_BYTES) else {
    return None;
  };
  let mac: u8[] = [];
  for i in 0..6 {
    mac = mac.append(dev.config_u8(i as usize));
  }
  let n = Net { dev: dev, rx: rx, tx: tx, slots: slots, mac: mac };
  for i in 0..SLOTS {
    n.post(i);
  }
  dev.ready();
  return Some(n);
}

// Gives receive buffer `i` to the device.
function (n: Net) post(i: i32): void {
  n.rx.set(i, n.slots.physical + i as usize * SLOT_BYTES, SLOT_BYTES as i32, virtio.DESC_WRITE, 0);
  n.rx.submit(i);
}

// Sends one Ethernet frame. False when the frame does not fit a slot, or when
// every transmit slot is still with the device.
pub function (n: Net) send(frame: u8[]): boolean {
  if (frame.len() as usize + HEADER_BYTES > SLOT_BYTES) {
    return false;
  }
  // The device returns a sent buffer on the used ring; take those back first.
  while (n.tx.pending() > 0) {
    let taken = n.tx.used();
  }
  let idx = n.tx.available();
  let device = raw.load_u16(n.tx.used + 2 as usize) as i32;
  if ((idx - device & 0xffff) >= SLOTS) {
    return false;
  }
  let slot = idx % SLOTS;
  let at = n.slots.virtual + (SLOTS + slot) as usize * SLOT_BYTES;
  let i = 0 as usize;
  while (i < HEADER_BYTES) {
    raw.store_u8(at + i, 0 as u8);
    i += 1 as usize;
  }
  for k in 0..frame.len() {
    raw.store_u8(at + HEADER_BYTES + k as usize, frame[k]);
  }
  n.slots.sync();
  n.tx.set(slot, n.slots.physical + (SLOTS + slot) as usize * SLOT_BYTES, (HEADER_BYTES + frame.len() as usize) as i32, 0 as u32, 0);
  n.tx.submit(slot);
  return true;
}

// The next frame the device received, without its virtio header, or None
// when the device has returned none.
pub function (n: Net) receive(): Option[u8[]] {
  let Some(u) = n.rx.used() else {
    return None;
  };
  let at = n.slots.virtual + u.id as usize * SLOT_BYTES;
  let out: u8[] = [];
  let len = u.len as usize;
  if (len > SLOT_BYTES) {
    len = SLOT_BYTES;
  }
  let i = HEADER_BYTES;
  while (i < len) {
    out = out.append(raw.load_u8(at + i));
    i += 1 as usize;
  }
  n.post(u.id);
  return Some(out);
}
```
