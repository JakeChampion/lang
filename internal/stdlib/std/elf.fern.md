# ELF64 images

A kernel that starts a process needs to read the image of the program (#14264, milestone 5 of `docs/BARE-METAL-PLAN.md`). The in-process linkers of the compiler write ELF, and this module reads it: the header and the program headers of an ELF64 image in little-endian byte order, which is what every Fern target emits. It is pure code over a byte array, so a hosted test drives it. A kernel reads an image from its filesystem, or from a buffer the VMM loaded.

`parse` checks the magic, the class, the byte order and the version, and it reads the program headers. The segments of kind `PT_LOAD` are what a loader maps: each one names a range of the file, an address and a size in memory, and the permissions. The bytes of the file beyond `file_size` up to `memory_size` are zero in memory, which is where `.bss` lives. The loader of the kernel maps frames, copies the file bytes and clears the rest, with `std/raw`. This module gives it the plan.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct ElfError>>

<<constants>>

<<pub struct Segment>>

<<pub struct Image>>

<<pub function parse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the header and the program headers of an ELF64 image.
```

### `dependencies`

```fern
<<dependencies>>=
import "core/cmp";
import "core/error";
import "std/binary";
import "std/option";
```

### `pub struct ElfError`

```fern
<<pub struct ElfError>>=
pub struct ElfError { offset: i32, reason: string }

impl error.Error for ElfError {}

impl cmp.Display for ElfError {}

pub function (e: ElfError) message(): string {
  return "elf: " + e.reason + " at offset " + e.offset.to_string();
}

pub function (e: ElfError) to_string(): string {
  return e.message();
}
```

### `constants`

The sizes of the ELF64 header and of one program header, and the machines the compiler emits. Then the kinds of an image and of a segment, and the permission bits of a segment.

```fern
<<constants>>=
pub const HEADER_BYTES: i32 = 64;

pub const PROGRAM_HEADER_BYTES: i32 = 56;

pub const EM_X86_64: u32 = 62;

pub const EM_AARCH64: u32 = 183;

// The kinds of an image: an executable at fixed addresses, or a
// position-independent one.
pub const ET_EXEC: u32 = 2;

pub const ET_DYN: u32 = 3;

pub const PT_LOAD: u32 = 1;

pub const PT_NOTE: u32 = 4;

pub const PF_X: u32 = 1;

pub const PF_W: u32 = 2;

pub const PF_R: u32 = 4;
```

### `pub struct Segment`

A segment is one program header: `offset` and `file_size` name its bytes in the file, `address` and `memory_size` its place in memory, and `physical` the load address a VMM uses.

```fern
<<pub struct Segment>>=
pub struct Segment { kind: u32, flags: u32, offset: u64, address: u64, physical: u64, file_size: u64, memory_size: u64, align: u64 }

pub function (s: Segment) is_load(): boolean {
  return s.kind == PT_LOAD;
}

pub function (s: Segment) readable(): boolean {
  return (s.flags & PF_R) != 0 as u32;
}

pub function (s: Segment) writable(): boolean {
  return (s.flags & PF_W) != 0 as u32;
}

pub function (s: Segment) executable(): boolean {
  return (s.flags & PF_X) != 0 as u32;
}

// The bytes of the segment in the file. The loader clears the rest of the
// memory of the segment, up to memory_size.
pub function (s: Segment) bytes(image: [u8]): Result[u8[], ElfError] {
  if (s.offset > image.len() as u64 || s.file_size > image.len() as u64 - s.offset) {
    return Err(ElfError { offset: s.offset as i32, reason: "a segment ends beyond the image" });
  }
  let out: u8[] = [];
  let i = s.offset as i32;
  let end = (s.offset + s.file_size) as i32;
  while (i < end) {
    out = out.append(image[i]);
    i += 1;
  }
  return Ok(out);
}
```

### `pub struct Image`

```fern
<<pub struct Image>>=
pub struct Image { kind: u32, machine: u32, entry: u64, segments: Segment[] }

// The segments a loader maps, in the order of the program headers.
pub function (i: Image) loads(): Segment[] {
  let out: Segment[] = [];
  for s in i.segments {
    if (s.is_load()) {
      out = out.append(s);
    }
  }
  return out;
}
```

### `pub function parse`

`parse` refuses an image that is not ELF, not 64-bit, not little-endian, or whose program headers lie beyond its bytes.

```fern
<<pub function parse>>=
pub function parse(image: [u8]): Result[Image, ElfError] {
  match (identification(image)) {
    Some(e) => {
      return Err(e);
    },
    None => {}
  }
  let kind = word16(image, 16);
  let machine = word16(image, 18);
  let Ok(entry) = binary.read_u64_le(image, 24) else {
    return Err(ElfError { offset: 24, reason: "the entry point is cut" });
  };
  let Ok(offset) = binary.read_u64_le(image, 32) else {
    return Err(ElfError { offset: 32, reason: "the program header offset is cut" });
  };
  let size = word16(image, 54);
  let count = word16(image, 56);
  if (size != PROGRAM_HEADER_BYTES as u32 && count != 0 as u32) {
    return Err(ElfError { offset: 54, reason: "a program header is not " + PROGRAM_HEADER_BYTES.to_string() + " bytes" });
  }
  let table = count as u64 * (PROGRAM_HEADER_BYTES as u64);
  if (offset > image.len() as u64 || table > image.len() as u64 - offset) {
    return Err(ElfError { offset: 32, reason: "the program headers lie beyond the image" });
  }
  let segments: Segment[] = [];
  let n = 0;
  while (n < count as i32) {
    segments = segments.append(segment(image, offset as i32 + n * PROGRAM_HEADER_BYTES));
    n += 1;
  }
  return Ok(Image { kind: kind, machine: machine, entry: entry, segments: segments });
}

// The fault of the first 16 bytes of the image, the ELF identification, or
// None when they name a little-endian ELF64 image.
function identification(image: [u8]): Option[ElfError] {
  if (image.len() < HEADER_BYTES) {
    return Some(ElfError { offset: 0, reason: "the image is shorter than an ELF64 header" });
  }
  if (image[0] != 0x7fu8 || image[1] != 0x45u8 || image[2] != 0x4cu8 || image[3] != 0x46u8) {
    return Some(ElfError { offset: 0, reason: "the magic is not ELF" });
  }
  if (image[4] != 2u8) {
    return Some(ElfError { offset: 4, reason: "the class is not ELF64" });
  }
  if (image[5] != 1u8) {
    return Some(ElfError { offset: 5, reason: "the byte order is not little-endian" });
  }
  if (image[6] != 1u8) {
    return Some(ElfError { offset: 6, reason: "the ELF version is not 1" });
  }
  return None;
}

// One program header at `at`, which parse has checked to be inside the image.
function segment(image: [u8], at: i32): Segment {
  return Segment {
    kind: word32(image, at),
    flags: word32(image, at + 4),
    offset: word64(image, at + 8),
    address: word64(image, at + 16),
    physical: word64(image, at + 24),
    file_size: word64(image, at + 32),
    memory_size: word64(image, at + 40),
    align: word64(image, at + 48),
  };
}

function word16(image: [u8], at: i32): u32 {
  return image[at] as u32 | image[at + 1] as u32 << 8;
}

function word32(image: [u8], at: i32): u32 {
  let Ok(v) = binary.read_u32_le(image, at) else {
    return 0 as u32;
  };
  return v;
}

function word64(image: [u8], at: i32): u64 {
  let Ok(v) = binary.read_u64_le(image, at) else {
    return 0 as u64;
  };
  return v;
}
```
