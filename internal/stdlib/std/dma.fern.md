# DMA buffers

A device reads and writes a DMA buffer on its own, so the buffer cannot be an ordinary value. The reference counting can free, move or reuse an ordinary value, and the caches of the core can hide the writes of the device. This module gives a kernel buffers that are none of those things. A buffer is a run of page frames from `std/frames`, mapped as device memory by `std/pagetable`, outside the heap (`docs/BARE-METAL-PLAN.md`, the DMA item). The heap never sees a frame of a buffer, because the heap lives in its own run of frames.

A buffer carries its virtual and its physical address. The kernel mapping is an identity mapping today, so the two are equal. A driver that gives the physical address to a device keeps working when they differ.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct Buffer>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a kernel DMA buffers outside the heap.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/frames";
import "std/option";
import "std/pagetable";
```

### `pub struct Buffer`

`alloc` rounds `bytes` up to whole frames and takes a run of them. It maps the run as device memory in the active tables and flushes the translations. `free` maps the run back as ordinary memory and returns the frames. `sync` orders the core's writes to a buffer before the write that tells the device about them.

```fern
<<pub struct Buffer>>=
pub struct Buffer { virtual: usize, physical: usize, bytes: usize }

pub function alloc(f: frames.Frames, tables: pagetable.Tables, bytes: usize): Option[Buffer] {
  let n = (bytes + frames.FRAME_BYTES - 1 as usize) / frames.FRAME_BYTES;
  if (n == 0 as usize) {
    return None;
  }
  let Some(run) = f.alloc_run(n) else {
    return None;
  };
  if (!tables.map_device(run, n * frames.FRAME_BYTES)) {
    let returned = f.free_run(run, n);
    return None;
  }
  tables.flush();
  return Some(Buffer { virtual: run, physical: run, bytes: n * frames.FRAME_BYTES });
}

// Returns the frames of `b`. False when `b` is not a buffer of `f`.
pub function free(f: frames.Frames, tables: pagetable.Tables, b: Buffer): boolean {
  let n = b.bytes / frames.FRAME_BYTES;
  if (!tables.map_normal(b.virtual, b.bytes)) {
    return false;
  }
  tables.flush();
  return f.free_run(b.virtual, n);
}

// Whether the buffer lies entirely outside [start, end).
pub function (b: Buffer) outside(start: usize, end: usize): boolean {
  return b.virtual + b.bytes <= start || b.virtual >= end;
}

pub function (b: Buffer) sync(): void {
  __fence();
}
```
