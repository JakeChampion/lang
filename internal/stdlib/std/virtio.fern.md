# Virtio over MMIO

A virtio device is the device model of qemu, Firecracker and Cloud Hypervisor: a network card, a block device or a console behind a few registers and shared rings. This module is the transport and the rings. The transport is virtio-mmio version 2 (virtio 1.0 and later), which Firecracker implements and qemu implements with `-global virtio-mmio.force-legacy=false`. The rings are split virtqueues in a `std/dma` buffer, so the device and the core see the same bytes.

A device sits at a base address with 512 bytes of registers. The driver probes the magic, the version and the device id. It negotiates features, builds its queues, and then starts the device. Every ring word goes through `std/raw`, so no pass moves an access. `__fence` orders the ring updates against the notification.

The device reads the rings on its own. The driver tells it about a new buffer with a write to the notify register. The driver learns about a used buffer from the used ring. This module polls the used ring. The IRQ line of the device, through `std/async`, comes later.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<registers>>

<<pub struct Device>>

<<pub struct Queue>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a kernel the virtio-mmio transport and split virtqueues.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/dma";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/raw";
```

### `registers`

The register offsets of virtio-mmio version 2, and the status and descriptor bits.

```fern
<<registers>>=
const MAGIC: i32 = 0x74726976;

const REG_MAGIC: usize = 0x000;

const REG_VERSION: usize = 0x004;

const REG_DEVICE_ID: usize = 0x008;

const REG_DEVICE_FEATURES: usize = 0x010;

const REG_DEVICE_FEATURES_SEL: usize = 0x014;

const REG_DRIVER_FEATURES: usize = 0x020;

const REG_DRIVER_FEATURES_SEL: usize = 0x024;

const REG_QUEUE_SEL: usize = 0x030;

const REG_QUEUE_NUM_MAX: usize = 0x034;

const REG_QUEUE_NUM: usize = 0x038;

const REG_QUEUE_READY: usize = 0x044;

const REG_QUEUE_NOTIFY: usize = 0x050;

const REG_INTERRUPT_STATUS: usize = 0x060;

const REG_INTERRUPT_ACK: usize = 0x064;

const REG_STATUS: usize = 0x070;

const REG_QUEUE_DESC_LOW: usize = 0x080;

const REG_QUEUE_DESC_HIGH: usize = 0x084;

const REG_QUEUE_DRIVER_LOW: usize = 0x090;

const REG_QUEUE_DRIVER_HIGH: usize = 0x094;

const REG_QUEUE_DEVICE_LOW: usize = 0x0a0;

const REG_QUEUE_DEVICE_HIGH: usize = 0x0a4;

const REG_CONFIG: usize = 0x100;

const STATUS_ACKNOWLEDGE: i32 = 1;

const STATUS_DRIVER: i32 = 2;

const STATUS_DRIVER_OK: i32 = 4;

const STATUS_FEATURES_OK: i32 = 8;

// Bit 0 of the high feature word: the device speaks virtio 1.0.
pub const FEATURE_VERSION_1_HIGH: u32 = 1;

// A descriptor that the device writes, and one that another descriptor follows.
pub const DESC_WRITE: u32 = 2;

pub const DESC_NEXT: u32 = 1;

// The device ids of the devices this module knows.
pub const ID_NET: u32 = 1;

pub const ID_BLOCK: u32 = 2;
```

### `pub struct Device`

`probe` reads the magic, the version and the device id of one transport. A transport with no device behind it has the id 0. qemu keeps the legacy version 1 unless the command line asks for version 2.

`negotiate` runs the status handshake: reset, acknowledge, driver, the features the driver wants of the ones the device offers, and the features-ok check. It gives the features the device accepted, or `None` when the device refused them. After the queues exist, `ready` starts the device.

```fern
<<pub struct Device>>=
pub struct Device { base: usize, id: u32 }

pub function probe(base: usize): Option[Device] {
  unsafe {
    if (__mmio_load32(base + REG_MAGIC) != MAGIC || __mmio_load32(base + REG_VERSION) != 2) {
      return None;
    }
    let id = __mmio_load32(base + REG_DEVICE_ID);
    if (id == 0) {
      return None;
    }
    return Some(Device { base: base, id: id as u32 });
  }
}

// The features the device offers: the low word with `sel` 0, the high word
// with `sel` 1.
pub function (d: Device) features(sel: i32): u32 {
  unsafe {
    __mmio_store32(d.base + REG_DEVICE_FEATURES_SEL, sel);
    return __mmio_load32(d.base + REG_DEVICE_FEATURES) as u32;
  }
}

pub struct Features { low: u32, high: u32 }

pub function (d: Device) negotiate(low: u32, high: u32): Option[Features] {
  unsafe {
    __mmio_store32(d.base + REG_STATUS, 0);
    while (__mmio_load32(d.base + REG_STATUS) != 0) {}
    __mmio_store32(d.base + REG_STATUS, STATUS_ACKNOWLEDGE);
    __mmio_store32(d.base + REG_STATUS, STATUS_ACKNOWLEDGE | STATUS_DRIVER);
    let accepted_low = d.features(0) & low;
    let accepted_high = d.features(1) & high;
    __mmio_store32(d.base + REG_DRIVER_FEATURES_SEL, 0);
    __mmio_store32(d.base + REG_DRIVER_FEATURES, accepted_low as i32);
    __mmio_store32(d.base + REG_DRIVER_FEATURES_SEL, 1);
    __mmio_store32(d.base + REG_DRIVER_FEATURES, accepted_high as i32);
    __mmio_store32(d.base + REG_STATUS, STATUS_ACKNOWLEDGE | STATUS_DRIVER | STATUS_FEATURES_OK);
    if ((__mmio_load32(d.base + REG_STATUS) & STATUS_FEATURES_OK) == 0) {
      return None;
    }
    return Some(Features { low: accepted_low, high: accepted_high });
  }
}

pub function (d: Device) ready(): void {
  unsafe {
    __mmio_store32(d.base + REG_STATUS, STATUS_ACKNOWLEDGE | STATUS_DRIVER | STATUS_FEATURES_OK | STATUS_DRIVER_OK);
  }
}

// One byte of the device configuration space.
pub function (d: Device) config_u8(offset: usize): u8 {
  unsafe {
    return __mmio_load8(d.base + REG_CONFIG + offset) as u8;
  }
}

// Reads and acknowledges the interrupt status. Bit 0 is a used buffer, and
// bit 1 a configuration change.
pub function (d: Device) interrupt_ack(): i32 {
  unsafe {
    let status = __mmio_load32(d.base + REG_INTERRUPT_STATUS);
    __mmio_store32(d.base + REG_INTERRUPT_ACK, status);
    return status;
  }
}
```

### `pub struct Queue`

A split virtqueue has three parts in one page of DMA memory. The descriptor table has 16 bytes for each entry. The driver writes the available ring, and the device writes the used ring. A queue of 16 entries takes 256 bytes of descriptors, 38 bytes of available ring and 134 bytes of used ring. The queue keeps its count of consumed used entries in the tail of the page. A copy of the struct then sees the same count.

`queue` asks the device for the queue of `index`, with `size` entries or the device's maximum. It tells the device the physical addresses. `set` writes one descriptor. `submit` puts a chain head on the available ring and notifies the device. `used` takes the next used entry, if the device returned one.

```fern
<<pub struct Queue>>=
pub struct Queue { base: usize, index: i32, size: i32, desc: usize, avail: usize, used: usize, state: usize, ring: dma.Buffer }

pub struct Used { id: i32, len: i32 }

pub function (d: Device) queue(index: i32, size: i32, f: frames.Frames, tables: pagetable.Tables): Option[Queue] {
  let n = size;
  unsafe {
    __mmio_store32(d.base + REG_QUEUE_SEL, index);
    let max = __mmio_load32(d.base + REG_QUEUE_NUM_MAX);
    if (max == 0) {
      return None;
    }
    if (max < n) {
      n = max;
    }
  }
  if (n > 128) {
    return None;
  }
  let Some(ring) = dma.alloc(f, tables, 4096 as usize) else {
    return None;
  };
  let p = ring.virtual;
  let i = 0 as usize;
  while (i < ring.bytes) {
    raw.store_u64(p + i, 0 as u64);
    i += 8 as usize;
  }
  let desc = p;
  let avail = p + (16 * n) as usize;
  let used = p + ((16 * n + 6 + 2 * n + 3) / 4 * 4) as usize;
  let q = Queue { base: d.base, index: index, size: n, desc: desc, avail: avail, used: used, state: p + 4088 as usize, ring: ring };
  unsafe {
    __mmio_store32(d.base + REG_QUEUE_NUM, n);
    let physical = ring.physical;
    __mmio_store32(d.base + REG_QUEUE_DESC_LOW, physical as i32);
    __mmio_store32(d.base + REG_QUEUE_DESC_HIGH, (physical >> 32 as usize) as i32);
    __mmio_store32(d.base + REG_QUEUE_DRIVER_LOW, (avail - p + physical) as i32);
    __mmio_store32(d.base + REG_QUEUE_DRIVER_HIGH, (avail - p + physical >> 32 as usize) as i32);
    __mmio_store32(d.base + REG_QUEUE_DEVICE_LOW, (used - p + physical) as i32);
    __mmio_store32(d.base + REG_QUEUE_DEVICE_HIGH, (used - p + physical >> 32 as usize) as i32);
    __mmio_store32(d.base + REG_QUEUE_READY, 1);
  }
  return Some(q);
}

// Writes descriptor `i`: the physical address and the length of a buffer, the
// DESC_* flags, and the next descriptor of the chain when DESC_NEXT is set.
pub function (q: Queue) set(i: i32, address: usize, len: i32, flags: u32, next: i32): void {
  let d = q.desc + (16 * i) as usize;
  raw.store_u64(d, address as u64);
  raw.store_u32(d + 8 as usize, len as u32);
  raw.store_u16(d + 12 as usize, flags);
  raw.store_u16(d + 14 as usize, next as u32);
}

// The index the driver has made available so far.
pub function (q: Queue) available(): i32 {
  return raw.load_u16(q.avail + 2 as usize) as i32;
}

// Puts the chain at `head` on the available ring and notifies the device.
pub function (q: Queue) submit(head: i32): void {
  let idx = q.available();
  raw.store_u16(q.avail + (4 + 2 * (idx % q.size)) as usize, head as u32);
  __fence();
  raw.store_u16(q.avail + 2 as usize, (idx + 1 & 0xffff) as u32);
  __fence();
  unsafe {
    __mmio_store32(q.base + REG_QUEUE_NOTIFY, q.index);
  }
}

// The number of buffers the device has returned and the driver has not taken.
pub function (q: Queue) pending(): i32 {
  let device = raw.load_u16(q.used + 2 as usize) as i32;
  let taken = raw.load_u16(q.state) as i32;
  return device - taken & 0xffff;
}

// Takes the next used buffer: the head descriptor the driver submitted and
// the bytes the device wrote.
pub function (q: Queue) used(): Option[Used] {
  if (q.pending() == 0) {
    return None;
  }
  __fence();
  let taken = raw.load_u16(q.state) as i32;
  let e = q.used + (4 + 8 * (taken % q.size)) as usize;
  let id = raw.load_u32(e) as i32;
  let len = raw.load_u32(e + 4 as usize) as i32;
  raw.store_u16(q.state, (taken + 1 & 0xffff) as u32);
  return Some(Used { id: id, len: len });
}
```
