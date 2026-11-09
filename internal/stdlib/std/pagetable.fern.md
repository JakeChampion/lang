# Page tables

This module builds the page tables of a kernel, with frames from `std/frames`, and makes them the active translation. A kernel maps its RAM as ordinary memory and the registers of its devices as device memory. An address outside both faults, so a stray pointer cannot reach a device through ordinary memory (`docs/BARE-METAL-PLAN.md`, milestone 3). The mapping is an identity mapping: each address translates to itself.

## The two formats

Both ISAs walk four levels of 512 entries, 4096 bytes each, and index them with the same bits of the address. The formats differ in the descriptor. On arm64, a table entry is the table address with the low bits 3. A block at level 2 carries the attributes alone, and a page at level 3 adds bit 1. Attribute index 1 of `MAIR_EL1` is the Normal memory of the reset entry, and index 0 is Device-nGnRnE. A device descriptor is execute-never.

On x86-64, a table entry is present, writable and user-accessible. The leaf decides the access: a 2 MiB page at level 2 adds the page-size bit, and a device page sets the cache-disable and write-through bits.

## User mappings

`map_user` maps a range of a process. Each virtual page translates to a page of physical frames, and user code at EL0 or ring 3 can reach it. The kernel's own mapping stays kernel-only. Thus a process that loads a kernel address takes a fault.

On arm64 a user page has AP 01 (read and write at both levels) or AP 11 (read-only at both levels), and it is PXN, so the kernel never runs user code. A user page that is not executable is also UXN. On x86-64 a user page sets the U/S bit, and the R/W bit when it is writable.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<pub struct Tables>>

<<walk>>

<<split>>

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

// A table entry, a block at level 2, and a page at level 3. On x86-64 a table
// entry lets a user page below it be reached: the leaf decides the access.
function table_entry(table: usize): i64 {
  if (target_arch() == "arm64") {
    return table as i64 | 3i64;
  }
  return table as i64 | 7i64;
}

// The descriptor of a user page of normal memory.
function user_attr(write: boolean, exec: boolean): i64 {
  if (target_arch() == "arm64") {
    // AF, inner shareable, Normal (index 1), PXN; AP 01 or AP 11; UXN unless exec.
    let attr = 0x20000000000705i64;
    if (write) {
      attr |= 0x40i64;
    } else {
      attr |= 0xc0i64;
    }
    if (!exec) {
      attr |= 0x40000000000000i64;
    }
    return attr;
  }
  if (write) {
    return 7i64;
  }
  return 5i64;
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

// Maps the user pages [va, va + len) to the physical pages from pa, for code
// at EL0 or ring 3. False when a bound is not page aligned or when the frames
// ran out.
pub function (m: Tables) map_user(va: usize, pa: usize, len: usize, write: boolean, exec: boolean): boolean {
  if (va % PAGE_BYTES != ZERO || pa % PAGE_BYTES != ZERO || len % PAGE_BYTES != ZERO) {
    return false;
  }
  let attr = user_attr(write, exec);
  let at = ZERO;
  while (at < len) {
    if (!m.map_page(va + at, pa + at, attr)) {
      return false;
    }
    at += PAGE_BYTES;
  }
  return true;
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
  return address >> shift as usize & ENTRIES - ONE;
}

// The table at `level` + 1 under the entry of `address` in `table`, made
// when absent. A block at level 2 splits into a table of its pages, so a
// page inside it can change on its own. 0 when the frames ran out.
function (m: Tables) next_table(table: usize, address: usize, level: i32): usize {
  unsafe {
    let at = table + index(address, level) * ENTRY_BYTES;
    let entry = __load_i64(at);
    if (is_table(entry)) {
      return entry as usize & 0xfffffffff000 as usize;
    }
    let Some(fresh) = m.frames.alloc() else {
      return ZERO;
    };
    if ((entry & 1i64) != 0i64) {
      split(fresh, entry);
    } else {
      clear(fresh);
    }
    __store_i64(at, table_entry(fresh));
    return fresh;
  }
}

// Maps the one page at va to pa.
function (m: Tables) map_page(va: usize, pa: usize, attr: i64): boolean {
  let l1 = m.next_table(m.root, va, 0);
  if (l1 == ZERO) {
    return false;
  }
  let l2 = m.next_table(l1, va, 1);
  if (l2 == ZERO) {
    return false;
  }
  let l3 = m.next_table(l2, va, 2);
  if (l3 == ZERO) {
    return false;
  }
  unsafe {
    __store_i64(l3 + index(va, 3) * ENTRY_BYTES, page_entry(pa, attr));
  }
  return true;
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
      if (at % BLOCK_BYTES == ZERO && end - at >= BLOCK_BYTES && !is_table(__load_i64(l2 + index(at, 2) * ENTRY_BYTES))) {
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

### `split`

A level 2 entry is a table or a block. On arm64 a table has bit 1 set and a block has it clear. On x86-64 a block has the page-size bit, and a table does not. `split` fills a fresh table with the 512 pages of a block, with the attributes of the block. One page can then change on its own. A range that is already mapped can thus change its memory type page by page, which `std/dma` does.

```fern
<<split>>=
function is_table(entry: i64): boolean {
  if ((entry & 1i64) == 0i64) {
    return false;
  }
  if (target_arch() == "arm64") {
    return (entry & 2i64) != 0i64;
  }
  return (entry & 0x80i64) == 0i64;
}

// The attributes of a block, without its address and without the bit that
// makes it a block.
function block_attr(entry: i64): i64 {
  let attr = entry & 0xffff000000000fffu64 as i64;
  if (target_arch() == "arm64") {
    return attr;
  }
  return attr & (0x7fi64 | 0xffff000000000f00u64 as i64);
}

function split(table: usize, block: i64): void {
  unsafe {
    let base = block as usize & 0xffffffe00000 as usize;
    let attr = block_attr(block);
    let i = ZERO;
    while (i < ENTRIES) {
      __store_i64(table + i * ENTRY_BYTES, page_entry(base + i * PAGE_BYTES, attr));
      i += ONE;
    }
  }
}

// Drops every translation that the core cached, after a change to the active
// mapping.
pub function (m: Tables) flush(): void {
  unsafe {
    __fence();
    __tlb_flush_all();
  }
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
      return __sysreg_read("ttbr0_el1") as usize & 0xfffffffff000 as usize;
    }
    return __sysreg_read("cr3") as usize & 0xfffffffff000 as usize;
  }
}
```
