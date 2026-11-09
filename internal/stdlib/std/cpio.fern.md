# cpio archives

This module reads a cpio archive in the `newc` format. The `newc` format is the format of a Linux initramfs: a VMM or a boot loader loads the archive into memory, and the kernel takes its first files from it. It is the first filesystem of `docs/BARE-METAL-PLAN.md` (milestone 5, #14264). It is pure code over a byte array, so a hosted test drives it. A kernel reads the archive from a buffer that the VMM loaded, or from a block device.

## Model

An archive is a sequence of entries. Each entry has a header of 110 ASCII bytes, a name and the bytes of the file. The header starts with the magic `070701`, or `070702` for the format with a checksum. Then 13 fields follow, each of 8 hexadecimal digits. They are the inode, the mode, the user, the group, the link count and the modification time. Then come the file size, four device numbers, the name size and the checksum.

The name follows the header and ends with a NUL, and the name size counts that NUL. The header and the name together take a multiple of 4 bytes, and so do the bytes of the file. The entry named `TRAILER!!!` ends the archive.

`parse` reads every entry into an `Archive`. An entry gives the range of the archive that holds its bytes, so `parse` copies no file. `read` copies the bytes of one entry, and `file` finds a regular file by its path and copies its bytes. The mode gives the kind of the entry: `is_file`, `is_dir` and `is_symlink` read it. The bytes of a symbolic link are the path that it points to.

A path in an archive has no `/` at its start, and a tool can write it with `./` at its start. `find` and `file` compare the paths without these prefixes, so `/init`, `./init` and `init` name the same entry.

## Contracts

`parse` refuses an archive with a bad magic, or with a field that is not 8 hexadecimal digits. It refuses a name that is empty, that does not end with a NUL, or that is not UTF-8. It also refuses an entry that ends past the bytes, and an archive with no trailer.

For the `070702` format, `parse` refuses a regular file whose checksum is not the 32-bit sum of its bytes. The checksum of any other kind of entry is not checked, because a writer such as GNU cpio gives it the value 0. The error gives the byte offset of the problem. `parse` stops at the trailer and reads nothing after it.

A hard link in a `newc` archive has its bytes only with its last name: the other names of the link have the size 0. This module does not join them.

## Executable example

```fern test name=std-cpio-example
import "std/cpio";
import "std/option";

function hex8(v: i32): string {
  let s = v.to_string_radix(16);
  while (s.len() < 8) {
    s = "0" + s;
  }
  return s;
}

// One entry: the 110-byte header, the name with its NUL, and the data,
// each padded to 4 bytes.
function entry(name: string, mode: string, data: string): u8[] {
  let fields = "00000001" + mode + "000000000000000000000001" + "00000000";
  fields += hex8(data.len());
  fields += "00000000000000000000000000000000";
  fields += hex8(name.len() + 1) + "00000000";
  let out: u8[] = [];
  for b in ("070701" + fields + name).as_bytes() {
    out = out.append(b);
  }
  out = out.append(0 as u8);
  while (out.len() % 4 != 0) {
    out = out.append(0 as u8);
  }
  for b in data.as_bytes() {
    out = out.append(b);
  }
  while (out.len() % 4 != 0) {
    out = out.append(0 as u8);
  }
  return out;
}

function main(): i32 {
  let bytes = entry("hello.txt", "000081a4", "fern\n");
  for b in entry("TRAILER!!!", "00000000", "") {
    bytes = bytes.append(b);
  }
  let Ok(archive) = cpio.parse(bytes) else {
    return 1;
  };
  let Some(data) = archive.file("/hello.txt") else {
    return 2;
  };
  if (data.len() != 5 || data[0] != 102 as u8) {
    return 3;
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct CpioError>>

<<constants>>

<<pub struct Entry>>

<<pub struct Archive>>

<<pub function parse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads a cpio archive in the newc format: the format of a Linux
// initramfs.
```

### `dependencies`

```fern
<<dependencies>>=
import "core/cmp";
import "core/error";
import "std/array";
import "std/option";
import "std/utf8";
```

### `pub struct CpioError`

```fern
<<pub struct CpioError>>=
pub struct CpioError { offset: i32, reason: string }

impl error.Error for CpioError {}

impl cmp.Display for CpioError {}

pub function (e: CpioError) message(): string {
  return "cpio: " + e.reason + " at byte " + e.offset.to_string();
}

pub function (e: CpioError) to_string(): string {
  return e.message();
}
```

### `constants`

The kind of an entry is the top 4 bits of the 16-bit mode, as in `stat`.

```fern
<<constants>>=
pub const HEADER_BYTES: i32 = 110;

pub const TRAILER: string = "TRAILER!!!";

pub const KIND_MASK: u32 = 0xf000;

pub const KIND_FILE: u32 = 0x8000;

pub const KIND_DIRECTORY: u32 = 0x4000;

pub const KIND_SYMLINK: u32 = 0xa000;
```

### `pub struct Entry`

`start` and `size` give the bytes of the entry in the archive.

```fern
<<pub struct Entry>>=
pub struct Entry { name: string, inode: u32, mode: u32, uid: u32, gid: u32, links: u32, mtime: u32, start: i32, size: i32 }

pub function (e: Entry) kind(): u32 {
  return e.mode & KIND_MASK;
}

pub function (e: Entry) is_file(): boolean {
  return e.kind() == KIND_FILE;
}

pub function (e: Entry) is_dir(): boolean {
  return e.kind() == KIND_DIRECTORY;
}

pub function (e: Entry) is_symlink(): boolean {
  return e.kind() == KIND_SYMLINK;
}

// The permission bits of the mode: the set-id and sticky bits and rwx for
// the user, the group and others.
pub function (e: Entry) permissions(): u32 {
  return e.mode & 0xfff as u32;
}
```

### `pub struct Archive`

```fern
<<pub struct Archive>>=
pub struct Archive { bytes: u8[], entries: Entry[] }

// The path without its leading "/" and "./" prefixes.
pub function clean(path: string): string {
  let b = path.as_bytes();
  let i = 0;
  let more = true;
  while (more) {
    if (i + 1 < b.len() && b[i] == 46 as u8 && b[i + 1] == 47 as u8) {
      i += 2;
    } else if (i < b.len() && b[i] == 47 as u8) {
      i += 1;
    } else {
      more = false;
    }
  }
  if (i == 0) {
    return path;
  }
  // The prefixes are ASCII, so the rest is UTF-8 too.
  let Ok(rest) = utf8.from_bytes(b[i:b.len()]) else {
    return path;
  };
  return rest;
}

// The first entry with the path `path`.
pub function (a: Archive) find(path: string): Option[Entry] {
  let want = clean(path);
  for e in a.entries {
    if (clean(e.name) == want) {
      return Some(e);
    }
  }
  return None;
}

// A copy of the bytes of `e`.
pub function (a: Archive) read(e: Entry): u8[] {
  return array.slice(a.bytes, e.start, e.start + e.size);
}

// A copy of the bytes of the regular file at `path`, or None when the archive
// has no regular file there.
pub function (a: Archive) file(path: string): Option[u8[]] {
  let Some(e) = a.find(path) else {
    return None;
  };
  if (!e.is_file()) {
    return None;
  }
  return Some(a.read(e));
}
```

### `pub function parse`

```fern
<<pub function parse>>=
pub function parse(bytes: u8[]): Result[Archive, CpioError] {
  let entries: Entry[] = [];
  let at = 0;
  while (true) {
    let (e, next) = entry(bytes, at)?;
    if (e.name == TRAILER) {
      return Ok(Archive { bytes: bytes, entries: entries });
    }
    entries = entries.append(e);
    at = next;
  }
  return Err(CpioError { offset: at, reason: "the archive has no trailer" });
}

// The entry whose header starts at `at`, and the offset of the next header.
function entry(b: u8[], at: i32): Result[(Entry, i32), CpioError] {
  let crc = header(b, at)?;
  let size = field(b, at, 6)?;
  let name = entry_name(b, at)?;
  let start = align4(at + HEADER_BYTES + name.len() + 1);
  if (start > b.len() || size > (b.len() - start) as u32) {
    return Err(CpioError { offset: at + 6 + 6 * 8, reason: "the file ends past the bytes" });
  }
  let e = fields(b, at, name, start, size as i32)?;
  if (crc) {
    let checked = check_sum(b, at, e)?;
  }
  return Ok((e, align4(start + e.size)));
}

// Checks the checksum of a regular file of the 070702 format.
function check_sum(b: u8[], at: i32, e: Entry): Result[Entry, CpioError] {
  if (e.is_file() && sum(b, e.start, e.start + e.size) != field(b, at, 12)?) {
    return Err(CpioError { offset: at + 6 + 12 * 8, reason: "the checksum does not match the file" });
  }
  return Ok(e);
}

// Checks that a whole header starts at `at`. True for the 070702 format.
function header(b: u8[], at: i32): Result[boolean, CpioError] {
  if (at >= b.len()) {
    return Err(CpioError { offset: at, reason: "the archive has no trailer" });
  }
  if (at > b.len() - HEADER_BYTES) {
    return Err(CpioError { offset: at, reason: "the bytes end inside a header" });
  }
  if (magic(b, at, "070701")) {
    return Ok(false);
  }
  if (magic(b, at, "070702")) {
    return Ok(true);
  }
  return Err(CpioError { offset: at, reason: "the magic is not 070701 or 070702" });
}

// The name that follows the header at `at`, without its NUL.
function entry_name(b: u8[], at: i32): Result[string, CpioError] {
  let size = field(b, at, 11)?;
  let name_at = at + HEADER_BYTES;
  if (size == 0 as u32 || size > (b.len() - name_at) as u32) {
    return Err(CpioError { offset: at + 6 + 11 * 8, reason: "the name ends past the bytes" });
  }
  let end = name_at + size as i32 - 1;
  if (b[end] != 0 as u8) {
    return Err(CpioError { offset: end, reason: "the name does not end with a NUL" });
  }
  let Ok(name) = utf8.from_bytes(b[name_at:end]) else {
    return Err(CpioError { offset: name_at, reason: "the name is not UTF-8" });
  };
  if (name.len() == 0) {
    return Err(CpioError { offset: name_at, reason: "the name is empty" });
  }
  return Ok(name);
}

function fields(b: u8[], at: i32, name: string, start: i32, size: i32): Result[Entry, CpioError] {
  return Ok(Entry {
    name: name,
    inode: field(b, at, 0)?,
    mode: field(b, at, 1)?,
    uid: field(b, at, 2)?,
    gid: field(b, at, 3)?,
    links: field(b, at, 4)?,
    mtime: field(b, at, 5)?,
    start: start,
    size: size,
  });
}

function magic(b: u8[], at: i32, want: string): boolean {
  let w = want.as_bytes();
  for i in 0..w.len() {
    if (b[at + i] != w[i]) {
      return false;
    }
  }
  return true;
}

// Field `index` of the header at `at`: 8 hexadecimal digits after the magic.
function field(b: u8[], at: i32, index: i32): Result[u32, CpioError] {
  let start = at + 6 + index * 8;
  let v = 0u32;
  for i in 0..8 {
    let Some(d) = hex_digit(b[start + i]) else {
      return Err(CpioError { offset: start + i, reason: "a header field is not hexadecimal" });
    };
    v = v << 4 as u32 | d;
  }
  return Ok(v);
}

function hex_digit(c: u8): Option[u32] {
  let x = c as u32;
  if (x >= 48 as u32 && x <= 57 as u32) {
    return Some(x - 48 as u32);
  }
  if (x >= 97 as u32 && x <= 102 as u32) {
    return Some(x - 87 as u32);
  }
  if (x >= 65 as u32 && x <= 70 as u32) {
    return Some(x - 55 as u32);
  }
  return None;
}

function align4(n: i32): i32 {
  return n + 3 & -4;
}

// The 32-bit sum of the bytes from start to end: the checksum of 070702.
function sum(b: u8[], start: i32, end: i32): u32 {
  let s = 0u64;
  for i in start..end {
    s = s + b[i] as u64 & 0xffffffff as u64;
  }
  return s as u32;
}
```
