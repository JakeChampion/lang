# Virtio block device

A virtio-blk device is a disk: a sequence of 512-byte sectors that the host keeps in a file. This module is the driver. It opens the device over `std/virtio`, and it reads and writes whole sectors. A kernel reads its filesystem image from the disk, for example a `std/cpio` archive, and writes its results back (milestone 5 of `docs/BARE-METAL-PLAN.md`, #14264).

qemu and Firecracker give a guest this device on both ISAs. A disk also reaches a kernel that qemu-virt loads as an ELF image, which gets no initramfs.

The driver negotiates three features: virtio 1.0, the read-only flag and the flush command. The capacity of the disk, in sectors, is the first field of the configuration space.

One request is a chain of three descriptors: a 16-byte header with the request type and the first sector, the data, and a status byte that the device writes. The header and the status byte are in the first page of one `std/dma` buffer, and the data are in the `CHUNK_BYTES` after it. A read or a write of more than `CHUNK_BYTES` is a sequence of requests.

The driver polls. It submits one request at a time and waits on the used ring until the device returns it.

## Contracts

`open` gives None for a device that is not a block device, that refuses virtio 1.0, or whose queue has fewer than three entries. `read` and `write` give None and false for a range that does not fit the disk, and for a request that the device fails. `write` also gives false on a read-only disk and for data that is not whole sectors. `flush` asks the device to make the writes durable, and it gives true at once when the device does not offer the command.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<pub struct Block>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a kernel a virtio block device.
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
// The disk is read-only: feature bit 5. The device takes a flush: bit 9.
const FEATURE_RO: u32 = 32;

const FEATURE_FLUSH: u32 = 512;

const T_IN: u32 = 0;

const T_OUT: u32 = 1;

const T_FLUSH: u32 = 4;

pub const SECTOR_BYTES: usize = 512;

// The data of one request, after the page of the header and the status byte.
pub const CHUNK_BYTES: usize = 65536;

const HEADER_PAGE: usize = 4096;
```

### `pub struct Block`

```fern
<<pub struct Block>>=
pub struct Block { dev: virtio.Device, q: virtio.Queue, buf: dma.Buffer, sectors: u64, read_only: boolean, can_flush: boolean }

pub function open(dev: virtio.Device, f: frames.Frames, tables: pagetable.Tables): Option[Block] {
  if (dev.id != virtio.ID_BLOCK) {
    return None;
  }
  let Some(features) = dev.negotiate(FEATURE_RO | FEATURE_FLUSH, virtio.FEATURE_VERSION_1_HIGH) else {
    return None;
  };
  if ((features.high & virtio.FEATURE_VERSION_1_HIGH) == 0 as u32) {
    return None;
  }
  let Some(q) = dev.queue(0, 4, f, tables) else {
    return None;
  };
  if (q.size < 3) {
    return None;
  }
  let Some(buf) = dma.alloc(f, tables, HEADER_PAGE + CHUNK_BYTES) else {
    return None;
  };
  let sectors = dev.config_u32(0 as usize) as u64 | dev.config_u32(4 as usize) as u64 << 32 as u64;
  dev.ready();
  return Some(Block {
    dev: dev,
    q: q,
    buf: buf,
    sectors: sectors,
    read_only: (features.low & FEATURE_RO) != 0 as u32,
    can_flush: (features.low & FEATURE_FLUSH) != 0 as u32,
  });
}

// The size of the disk in bytes.
pub function (b: Block) bytes(): u64 {
  return b.sectors * (SECTOR_BYTES as u64);
}

// Whether count sectors from sector lie on the disk.
function (b: Block) fits(sector: u64, count: u64): boolean {
  return sector <= b.sectors && count <= b.sectors - sector;
}

// Sends one request of kind with len data bytes at the start of the chunk, and
// waits for the device. True when the device reports success.
function (b: Block) request(kind: u32, sector: u64, len: usize): boolean {
  let h = b.buf.virtual;
  raw.store_u32(h, kind);
  raw.store_u32(h + 4 as usize, 0 as u32);
  raw.store_u64(h + 8 as usize, sector);
  raw.store_u8(h + 16 as usize, 0xff as u8);
  b.buf.sync();
  let p = b.buf.physical;
  b.q.set(0, p, 16, virtio.DESC_NEXT, 1);
  if (len == 0 as usize) {
    b.q.set(1, p + 16 as usize, 1, virtio.DESC_WRITE, 0);
  } else {
    let flags = virtio.DESC_NEXT;
    if (kind == T_IN) {
      flags |= virtio.DESC_WRITE;
    }
    b.q.set(1, p + HEADER_PAGE, len as i32, flags, 2);
    b.q.set(2, p + 16 as usize, 1, virtio.DESC_WRITE, 0);
  }
  b.q.submit(0);
  while (b.q.pending() == 0) {}
  let done = b.q.used();
  let acked = b.dev.interrupt_ack();
  return raw.load_u8(h + 16 as usize) == 0 as u8;
}

// The bytes of count sectors from sector.
pub function (b: Block) read(sector: u64, count: i32): Option[u8[]] {
  if (count < 0 || !b.fits(sector, count as u64)) {
    return None;
  }
  let out: u8[] = [];
  let at = sector;
  let left = count as usize * SECTOR_BYTES;
  let data = b.buf.virtual + HEADER_PAGE;
  while (left > 0 as usize) {
    let n = left;
    if (n > CHUNK_BYTES) {
      n = CHUNK_BYTES;
    }
    if (!b.request(T_IN, at, n)) {
      return None;
    }
    let i = 0 as usize;
    while (i < n) {
      out = out.append(raw.load_u8(data + i));
      i += 1 as usize;
    }
    at += (n / SECTOR_BYTES) as u64;
    left -= n;
  }
  return Some(out);
}

// Writes data, whole sectors, from sector.
pub function (b: Block) write(sector: u64, data: u8[]): boolean {
  let len = data.len() as usize;
  if (b.read_only || len % SECTOR_BYTES != 0 as usize || !b.fits(sector, (len / SECTOR_BYTES) as u64)) {
    return false;
  }
  let at = sector;
  let done = 0 as usize;
  let chunk = b.buf.virtual + HEADER_PAGE;
  while (done < len) {
    let n = len - done;
    if (n > CHUNK_BYTES) {
      n = CHUNK_BYTES;
    }
    let i = 0 as usize;
    while (i < n) {
      raw.store_u8(chunk + i, data[(done + i) as i32]);
      i += 1 as usize;
    }
    if (!b.request(T_OUT, at, n)) {
      return false;
    }
    at += (n / SECTOR_BYTES) as u64;
    done += n;
  }
  return true;
}

// Asks the device to make every finished write durable.
pub function (b: Block) flush(): boolean {
  if (!b.can_flush) {
    return true;
  }
  return b.request(T_FLUSH, 0 as u64, 0 as usize);
}
```
