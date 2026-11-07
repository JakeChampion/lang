# PVH start info

This module reads the start info that a VMM gives an x86-64 kernel at the Xen PVH entry. qemu microvm and Firecracker use this entry.

## Model

The VMM puts an `hvm_start_info` structure in guest memory and passes its address in `%ebx`. The reset entry of `-target x86-64-kernel` saves that address, and `__boot_info()` gives it to the program. `read` checks the magic word and copies the fields and the memory map into a `StartInfo`. `ram_bytes` adds the size of each RAM region.

## Contracts

`read` is unsafe, because it reads memory at the address that the caller gives. The caller must pass the address of a start info, normally `__boot_info()`. `read` refuses a structure with the wrong magic word. A version 0 structure has no memory map, so `memory` is empty for it.

## Executable example

```fern test name=std-pvh-example
import "std/pvh";

function main(): i32 {
  unsafe {
    let p: usize = __alloc(64);
    __memset(p, 0, 64);
    __store_i32(p, 0x336ec578);
    let r = pvh.read(p);
    __free(p, 64);
    let Ok(info) = r else {
      return 1;
    };
    if (info.version != 0 || info.memory.len() != 0) {
      return 2;
    }
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<model>>

<<reading>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the PVH start info that a VMM gives an x86-64 kernel.
import "core/cmp";
import "core/error";
```

### `model`

```fern
<<model>>=
pub struct PvhError { reason: string }

impl error.Error for PvhError {}

impl cmp.Display for PvhError {}

pub function (e: PvhError) message(): string {
  return "pvh start info: " + e.reason;
}

pub function (e: PvhError) to_string(): string {
  return e.message();
}

// One entry of the memory map. A kind of 1 is RAM. The other kinds are
// reserved, ACPI and unusable memory.
pub struct MemoryRegion { address: u64, size: u64, kind: i32 }

// `cmdline` and `rsdp` are the addresses of the kernel command line and of
// the ACPI root table, or 0.
pub struct StartInfo { version: i32, flags: i32, modules: i32, cmdline: usize, rsdp: usize, memory: MemoryRegion[] }

// The sum of the RAM regions of the memory map.
pub function (s: StartInfo) ram_bytes(): u64 {
  let total: u64 = 0 as u64;
  for r in s.memory {
    if (r.kind == 1) {
      total = total + r.size;
    }
  }
  return total;
}
```

### `reading`

The structure is `hvm_start_info` from Xen's `arch-x86/hvm/start_info.h`. The fields are little-endian. Version 1 adds the memory map, whose entries are 24 bytes each.

```fern
<<reading>>=
const MAGIC: i32 = 0x336ec578;

// Reads the start info at `address`.
pub unsafe function read(address: usize): Result[StartInfo, PvhError] {
  unsafe {
    if (__load_i32(address) != MAGIC) {
      return Err(PvhError { reason: "no start-info magic" });
    }
    let version = __load_i32(address + 4);
    let memory: MemoryRegion[] = [];
    if (version >= 1) {
      let table = __load_i64(address + 40) as usize;
      for i in 0..__load_i32(address + 48) {
        let e = table + (i * 24) as usize;
        memory = memory.append(MemoryRegion { address: __load_i64(e) as u64, size: __load_i64(e + 8) as u64, kind: __load_i32(e + 16) });
      }
    }
    return Ok(StartInfo {
      version: version,
      flags: __load_i32(address + 8),
      modules: __load_i32(address + 12),
      cmdline: __load_i64(address + 24) as usize,
      rsdp: __load_i64(address + 32) as usize,
      memory: memory,
    });
  }
}
```
