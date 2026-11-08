# Page tables

This module builds the page tables of a kernel, with frames from `std/frames`, and makes them the active translation. A kernel maps its RAM as ordinary memory and the registers of its devices as device memory. An address outside both faults, so a stray pointer cannot reach a device through ordinary memory (`docs/BARE-METAL-PLAN.md`, milestone 3). The mapping is an identity mapping: each address translates to itself.

## The two formats

Both ISAs walk four levels of 512 entries, 4096 bytes each, and index them with the same bits of the address. The formats differ in the descriptor. On arm64, a table entry is the table address with the low bits 3. A block at level 2 carries the attributes alone, and a page at level 3 adds bit 1. Attribute index 1 of `MAIR_EL1` is the Normal memory of the reset entry, and index 0 is Device-nGnRnE. A device descriptor is execute-never.

On x86-64, a table entry is present and writable. A 2 MiB page at level 2 adds the page-size bit, and a device page sets the cache-disable and write-through bits.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<pub struct Tables>>

<<walk>>

<<activate>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module builds and activates the page tables of a kernel.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/frames";
import "std/option";
```

### `constants`

```fern
<<constants>>=
pub const PAGE_BYTES: usize = 4096;

pub const BLOCK_BYTES: usize = 2097152;

const ENTRY_BYTES: usize = 8;

const ENTRIES: usize = 512;

const ZERO: usize = 0;

const ONE: usize = 1;

// The descriptor of ordinary memory, and of device memory, on this ISA.
function normal_attr(): i64 {
  if (target_arch() == "arm64") {
    return 0x40000000000705i64;
  }
  return 3i64;
}

function device_attr(): i64 {
  if (target_arch() == "arm64") {
    return 0x60000000000401i64;
  }
  return 0x1bi64;
}

// A table entry, a block at level 2, and a page at level 3.
function table_entry(table: usize): i64 {
  return table as i64 | 3i64;
}

function block_entry(address: usize, attr: i64): i64 {
  if (target_arch() == "arm64") {
    return address as i64 | attr;
  }
  return address as i64 | attr | 0x80i64;
}

function page_entry(address: usize, attr: i64): i64 {
  if (target_arch() == "arm64") {
    return address as i64 | attr | 2i64;
  }
  return address as i64 | attr;
}
```

### `pub struct Tables`

```fern
<<pub struct Tables>>=
pub struct Tables { root: usize, frames: frames.Frames }

// An empty mapping, with its root table in a frame of `f`, or None when `f`
// has no frame.
pub function new(f: frames.Frames): Option[Tables] {
  let Some(root) = f.alloc() else {
    return None;
  };
  clear(root);
  return Some(Tables { root: root, frames: f });
}

// Maps [start, start + len) as ordinary memory. False when the range is not
// page aligned or when the frames ran out.
pub function (m: Tables) map_normal(start: usize, len: usize): boolean {
  return m.map_range(start, len, normal_attr());
}

// Maps [start, start + len) as device memory.
pub function (m: Tables) map_device(start: usize, len: usize): boolean {
  return m.map_range(start, len, device_attr());
}

function clear(table: usize): void {
  unsafe {
    let i = ZERO;
    while (i < ENTRIES) {
      __store_i64(table + i * ENTRY_BYTES, 0i64);
      i += ONE;
    }
  }
}
```

### `walk`

`map_range` takes a 2 MiB block wherever the address and the rest of the range allow one, and 4 KiB pages elsewhere. A level that has no table yet gets one from the frames.

```fern
<<walk>>=
function index(address: usize, level: i32): usize {
  let shift = 39 - 9 * level;
  return (address >> shift as usize) & (ENTRIES - ONE);
}

// The table at `level` + 1 under the entry of `address` in `table`, made
// when absent. 0 when the frames ran out.
function (m: Tables) next_table(table: usize, address: usize, level: i32): usize {
  unsafe {
    let at = table + index(address, level) * ENTRY_BYTES;
    let entry = __load_i64(at);
    if ((entry & 1i64) != 0i64) {
      return (entry as usize) & (0xfffffffff000 as usize);
    }
    let Some(fresh) = m.frames.alloc() else {
      return ZERO;
    };
    clear(fresh);
    __store_i64(at, table_entry(fresh));
    return fresh;
  }
}

function (m: Tables) map_range(start: usize, len: usize, attr: i64): boolean {
  if (start % PAGE_BYTES != ZERO || len % PAGE_BYTES != ZERO) {
    return false;
  }
  let end = start + len;
  let at = start;
  while (at < end) {
    let l1 = m.next_table(m.root, at, 0);
    if (l1 == ZERO) {
      return false;
    }
    let l2 = m.next_table(l1, at, 1);
    if (l2 == ZERO) {
      return false;
    }
    unsafe {
      if (at % BLOCK_BYTES == ZERO && end - at >= BLOCK_BYTES) {
        __store_i64(l2 + index(at, 2) * ENTRY_BYTES, block_entry(at, attr));
        at += BLOCK_BYTES;
      } else {
        let l3 = m.next_table(l2, at, 2);
        if (l3 == ZERO) {
          return false;
        }
        __store_i64(l3 + index(at, 3) * ENTRY_BYTES, page_entry(at, attr));
        at += PAGE_BYTES;
      }
    }
  }
  return true;
}
```

### `activate`

`activate` makes the mapping the active translation of the core. The mapping has to hold the code, the stack and the heap of the kernel, because the core keeps running from them. On arm64, a full barrier orders the table writes before the walker reads them. Then the write of `TTBR0_EL1` takes effect at once, and the TLB drops every old translation. On x86-64, the write of `CR3` does both. `active_root` reads the root of the active translation back.

```fern
<<activate>>=
pub function (m: Tables) activate(): void {
  unsafe {
    if (target_arch() == "arm64") {
      __fence();
      __sysreg_write("ttbr0_el1", m.root as i64);
      __tlb_flush_all();
    } else {
      __sysreg_write("cr3", m.root as i64);
    }
  }
}

pub function active_root(): usize {
  unsafe {
    if (target_arch() == "arm64") {
      return (__sysreg_read("ttbr0_el1") as usize) & (0xfffffffff000 as usize);
    }
    return (__sysreg_read("cr3") as usize) & (0xfffffffff000 as usize);
  }
}
```
