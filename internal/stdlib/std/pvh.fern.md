# PVH start info

This module reads the PVH start info. A VMM such as qemu `-M microvm` or Firecracker gives this structure to an x86-64 kernel that it enters through the Xen PVH note. The kernel finds its command line, its boot modules, the ACPI root pointer and the memory map in it.

## Model

The VMM puts the address of the start info in `%ebx` at entry, and `__boot_info()` gives that address on `x86-64-kernel`. A board copies `START_INFO_BYTES` bytes from it, and `parse` reads them into a `StartInfo`. Each field is a little-endian integer at a fixed offset.

The start info gives the physical address and the count of the entries of the memory map. A board copies `MEMMAP_ENTRY_BYTES` bytes for each entry from that address, and `parse_memmap` reads them into a list of `MemoryRegion` values. A region has an E820 type, and `is_ram` is true for usable RAM.

The start info also gives the physical address and the count of the boot modules. A VMM loads a module for the kernel: qemu and Firecracker load the `-initrd` image as module 0. A board copies `MODULE_ENTRY_BYTES` bytes for each entry from `modlist_address`, and `parse_modules` reads them into a list of `Module` values. A module gives the address and the size of its bytes, and the address of its command line, which is 0 when it has none.

The command line is a NUL-terminated string at `cmdline_address`. A board copies its bytes, and `command_line` reads the text before the first NUL.

## Contracts

`parse` refuses bytes that do not start with the magic `0x336ec578`, and bytes that end before the fields of their version. Version 0 has no memory map, so `memmap_address` and `memmap_entries` are zero. A version above 1 can add fields after the version 1 fields, and `parse` reads only the version 1 fields. `parse_memmap` and `parse_modules` refuse bytes that end before the last entry. `command_line` refuses bytes with no NUL and text that is not UTF-8. Each error gives the byte offset of the problem.

## Executable example

```fern test name=std-pvh-example
import "std/binary";
import "std/pvh";

function main(): i32 {
  let b = binary.append_u32_le([], 0x336ec578 as u32);
  b = binary.append_u32_le(b, 1 as u32);
  for i in 0..12 {
    b = binary.append_u32_le(b, 0 as u32);
  }
  let Ok(info) = pvh.parse(b) else {
    return 1;
  };
  if (info.version != 1 as u32 || info.memmap_entries != 0 as u32) {
    return 2;
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<model>>

<<the start info>>

<<the memory map>>

<<the modules>>

<<the command line>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the PVH start info: the description of the boot that a
// VMM gives an x86-64 kernel that it enters through the PVH note.
import "core/cmp";
import "core/error";
import "std/binary";
import "std/utf8";
```

### `model`

The offsets come from the `hvm_start_info`, `hvm_memmap_table_entry` and `hvm_modlist_entry` structures of the Xen public headers.

```fern
<<model>>=
pub struct PvhError { offset: i32, reason: string }

impl error.Error for PvhError {}

impl cmp.Display for PvhError {}

pub function (e: PvhError) message(): string {
  return "PVH start info: " + e.reason + " at byte " + e.offset.to_string();
}

pub function (e: PvhError) to_string(): string {
  return e.message();
}

// The bytes to copy from the start info address: the size of version 1.
pub const START_INFO_BYTES: i32 = 56;

// The bytes of one memory map entry.
pub const MEMMAP_ENTRY_BYTES: i32 = 24;

// The bytes of one module list entry.
pub const MODULE_ENTRY_BYTES: i32 = 32;

// The E820 type of usable RAM.
pub const RAM: u32 = 1;

pub struct StartInfo {
  version: u32,
  flags: u32,
  modules: u32,
  modlist_address: u64,
  cmdline_address: u64,
  rsdp_address: u64,
  memmap_address: u64,
  memmap_entries: u32,
}

// One entry of the memory map. `kind` is its E820 type.
pub struct MemoryRegion { address: u64, size: u64, kind: u32 }

pub function (r: MemoryRegion) is_ram(): boolean {
  return r.kind == RAM;
}

// One boot module, such as an initrd.
pub struct Module { address: u64, size: u64, cmdline_address: u64 }
```

### `the start info`

Version 0 ends after the ACPI root pointer, at byte 40. Version 1 adds the memory map fields and ends at byte 56.

```fern
<<the start info>>=
function u32_at(b: [u8], at: i32): Result[u32, PvhError] {
  let Ok(v) = binary.read_u32_le(b, at) else {
    return Err(PvhError { offset: at, reason: "the bytes end inside a field" });
  };
  return Ok(v);
}

function u64_at(b: [u8], at: i32): Result[u64, PvhError] {
  let Ok(v) = binary.read_u64_le(b, at) else {
    return Err(PvhError { offset: at, reason: "the bytes end inside a field" });
  };
  return Ok(v);
}

// Reads the start info that starts at byte 0 of `b`.
pub function parse(b: [u8]): Result[StartInfo, PvhError] {
  if (u32_at(b, 0)? != 0x336ec578 as u32) {
    return Err(PvhError { offset: 0, reason: "the magic is not 0x336ec578" });
  }
  let version = u32_at(b, 4)?;
  let info = StartInfo {
    version: version,
    flags: u32_at(b, 8)?,
    modules: u32_at(b, 12)?,
    modlist_address: u64_at(b, 16)?,
    cmdline_address: u64_at(b, 24)?,
    rsdp_address: u64_at(b, 32)?,
    memmap_address: 0 as u64,
    memmap_entries: 0 as u32,
  };
  if (version == 0 as u32) {
    return Ok(info);
  }
  return Ok(StartInfo { ...info, memmap_address: u64_at(b, 40)?, memmap_entries: u32_at(b, 48)? });
}
```

### `the memory map`

```fern
<<the memory map>>=
// Reads `entries` memory map entries that start at byte 0 of `b`.
pub function parse_memmap(b: [u8], entries: u32): Result[MemoryRegion[], PvhError] {
  if (entries as i64 * (MEMMAP_ENTRY_BYTES as i64) > b.len() as i64) {
    return Err(PvhError { offset: b.len(), reason: "the bytes end before the last entry" });
  }
  let out: MemoryRegion[] = [];
  for i in 0..entries as i32 {
    let at = i * MEMMAP_ENTRY_BYTES;
    out = out.append(MemoryRegion { address: u64_at(b, at)?, size: u64_at(b, at + 8)?, kind: u32_at(b, at + 16)? });
  }
  return Ok(out);
}
```

### `the modules`

An entry has a fourth field of 8 bytes, which is reserved.

```fern
<<the modules>>=
// Reads `entries` module list entries that start at byte 0 of `b`.
pub function parse_modules(b: u8[], entries: u32): Result[Module[], PvhError] {
  if (entries as i64 * (MODULE_ENTRY_BYTES as i64) > b.len() as i64) {
    return Err(PvhError { offset: b.len(), reason: "the bytes end before the last module" });
  }
  let out: Module[] = [];
  for i in 0..entries as i32 {
    let at = i * MODULE_ENTRY_BYTES;
    out = out.append(Module { address: u64_at(b, at)?, size: u64_at(b, at + 8)?, cmdline_address: u64_at(b, at + 16)? });
  }
  return Ok(out);
}
```

### `the command line`

```fern
<<the command line>>=
// The text before the first NUL of `b`.
pub function command_line(b: [u8]): Result[string, PvhError] {
  let text: u8[] = [];
  for x in b {
    if (x == 0 as u8) {
      let Ok(s) = utf8.from_bytes(text) else {
        return Err(PvhError { offset: 0, reason: "the command line is not UTF-8" });
      };
      return Ok(s);
    }
    text = text.append(x);
  }
  return Err(PvhError { offset: b.len(), reason: "the command line has no NUL" });
}
```
