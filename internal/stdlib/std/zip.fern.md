# ZIP archives

This module reads a ZIP archive from a byte array. A JAR file, such as `android.jar`, is a ZIP archive. The tool that writes JNI bindings reads class files from it (#14737). It is pure code over a byte array.

## Model

A ZIP archive ends with a central directory. The directory has one record for each file: its name, its sizes, its checksum, and the offset of the file in the archive. A short record after the directory gives the place of the directory.

`parse` reads the central directory into an `Archive`. It reads no file. An `Entry` is one record of the directory. The name of a directory ends with `/`, and its size is 0.

`read` gives the bytes of one entry. A file is stored as it is, or it is compressed with DEFLATE. `read` decompresses the file and checks its CRC-32. `find` gives the entry with a name.

## Contracts

`parse` refuses bytes with no central directory. It refuses a record with a bad signature, and a record that ends past the bytes. It refuses a name that is not UTF-8.

`parse` refuses an archive in the ZIP64 format and an archive with more than one disk. Thus the limits are 65534 entries and 2 GiB for each size and each offset.

`read` refuses an encrypted entry and a compression method that is not `STORED` or `DEFLATED`. It refuses a file whose size or checksum is not the value of the directory. `read` allocates no more than the size that the directory gives.

The error gives the byte offset of the problem.

## Executable example

```fern test name=std-zip-example
import "std/zip";
import "std/option";

function main(): i32 {
  // An archive with one stored file: "a.txt" with the bytes "hi\n".
  let bytes: u8[] = [
    80, 75, 3, 4, 20, 0, 0, 0, 0, 0, 0, 0, 33, 0, 122, 122, 111, 237, 3, 0, 0, 0, 3, 0, 0, 0, 5, 0, 0, 0, 97, 46,
    116, 120, 116, 104, 105, 10, 80, 75, 1, 2, 20, 3, 20, 0, 0, 0, 0, 0, 0, 0, 33, 0, 122, 122, 111, 237, 3, 0, 0, 0, 3, 0,
    0, 0, 5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 128, 1, 0, 0, 0, 0, 97, 46, 116, 120, 116, 80, 75, 5, 6, 0, 0, 0,
    0, 1, 0, 1, 0, 51, 0, 0, 0, 38, 0, 0, 0, 0, 0,
  ];
  let Ok(archive) = zip.parse(bytes) else {
    return 1;
  };
  let Some(entry) = archive.find("a.txt") else {
    return 2;
  };
  let Ok(data) = zip.read(bytes, entry) else {
    return 3;
  };
  if (data.len() != 3 || data[0] != 104 as u8) {
    return 4;
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<pub struct ZipError>>

<<pub struct Archive>>

<<bytes>>

<<pub function parse>>

<<pub function read>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads a ZIP archive from a byte array.
import "core/cmp";
import "core/error";
import "std/deflate";
import "std/hash/crc32";
import "std/option";
import "std/utf8";
```

### `pub struct ZipError`

```fern
<<pub struct ZipError>>=
pub struct ZipError { offset: i32, reason: string }

impl error.Error for ZipError {}

impl cmp.Display for ZipError {}

pub function (e: ZipError) message(): string {
  return "zip: " + e.reason + " at byte " + e.offset.to_string();
}

pub function (e: ZipError) to_string(): string {
  return e.message();
}
```

### `pub struct Archive`

```fern
<<pub struct Archive>>=
// The compression method of a file that is stored as it is.
pub const STORED: i32 = 0;

// The compression method of a file that is compressed with DEFLATE.
pub const DEFLATED: i32 = 8;

// One record of the central directory. `packed` is the size of the file in
// the archive and `size` is its size after decompression. `header` is the
// offset of the local header of the file.
pub struct Entry { name: string, method: i32, flags: i32, crc: u32, packed: i32, size: i32, header: i32 }

pub struct Archive { entries: Entry[] }

// True for an entry that is a directory.
pub function (e: Entry) is_dir(): boolean {
  return e.name.ends_with("/");
}

// The first entry with the name `name`.
pub function (a: Archive) find(name: string): Option[Entry] {
  for e in a.entries {
    if (e.name == name) {
      return Some(e);
    }
  }
  return None;
}
```

### `bytes`

A ZIP archive holds each number with its low byte first.

```fern
<<bytes>>=
function le16(b: [u8], i: i32): i32 {
  return b[i] as i32 | b[i + 1] as i32 << 8;
}

function le32(b: [u8], i: i32): u32 {
  return le16(b, i) as u32 | le16(b, i + 2) as u32 << 16u32;
}

// The size or the offset at `i`. A value of 2 GiB or more is not a value
// that this module reads. The ZIP64 format gives each such value as
// 0xffffffff.
function size_at(b: [u8], i: i32): Result[i32, ZipError] {
  let v = le32(b, i);
  if (v > 0x7fffffffu32) {
    return Err(ZipError { offset: i, reason: "the value is 2 GiB or more, or the archive is ZIP64" });
  }
  return Ok(v as i32);
}

// True when the four bytes at `i` are "PK" and the bytes `c` and `d`.
function signed(b: [u8], i: i32, c: i32, d: i32): boolean {
  return b[i] == 80 as u8 && b[i + 1] == 75 as u8 && b[i + 2] as i32 == c && b[i + 3] as i32 == d;
}

// Checks that `b` has `n` bytes at `at`.
function need(b: [u8], at: i32, n: i32): Result[i32, ZipError] {
  if (at < 0 || n < 0 || at > b.len() - n) {
    return Err(ZipError { offset: at, reason: "the record ends past the bytes" });
  }
  return Ok(at);
}
```

### `pub function parse`

The record at the end of the archive has 22 bytes. A comment of not more than 65535 bytes follows it. `end_record` looks for it from the end. The length of its comment must agree with the end of the bytes. Thus the signature in a comment is not a record.

```fern
<<pub function parse>>=
const END_BYTES: i32 = 22;

const RECORD_BYTES: i32 = 46;

// The offset of the record at the end of the archive.
function end_record(b: [u8]): Result[i32, ZipError] {
  let i = b.len() - END_BYTES;
  let last = i - 65535;
  while (i >= 0 && i >= last) {
    if (signed(b, i, 5, 6) && le16(b, i + 20) == b.len() - i - END_BYTES) {
      return Ok(i);
    }
    i -= 1;
  }
  return Err(ZipError { offset: 0, reason: "the archive has no central directory" });
}

// The entry of the record at `p`, and the offset of the next record.
function record(b: [u8], p: i32): Result[(Entry, i32), ZipError] {
  let head = need(b, p, RECORD_BYTES)?;
  if (!signed(b, p, 1, 2)) {
    return Err(ZipError { offset: p, reason: "the record of the central directory has a bad signature" });
  }
  let name_bytes = le16(b, p + 28);
  let next = p + RECORD_BYTES + name_bytes + le16(b, p + 30) + le16(b, p + 32);
  let all = need(b, p, next - p)?;
  let Ok(name) = utf8.from_bytes(b[p + RECORD_BYTES:p + RECORD_BYTES + name_bytes]) else {
    return Err(ZipError { offset: p + RECORD_BYTES, reason: "the name is not UTF-8" });
  };
  let packed = size_at(b, p + 20)?;
  let size = size_at(b, p + 24)?;
  let header = size_at(b, p + 42)?;
  return Ok((Entry { name: name, method: le16(b, p + 10), flags: le16(b, p + 8), crc: le32(b, p + 16), packed: packed, size: size, header: header }, next));
}

// The entries of the archive `bytes`.
pub function parse(bytes: [u8]): Result[Archive, ZipError] {
  let end = end_record(bytes)?;
  let count = le16(bytes, end + 10);
  if (count == 0xffff || le16(bytes, end + 4) != 0 || le16(bytes, end + 8) != count) {
    return Err(ZipError { offset: end, reason: "the archive is ZIP64 or has more than one disk" });
  }
  let p = size_at(bytes, end + 16)?;
  let entries: Entry[] = [];
  for k in 0..count {
    let (e, next) = record(bytes, p)?;
    entries = entries.append(e);
    p = next;
  }
  return Ok(Archive { entries: entries });
}
```

### `pub function read`

A local header comes before the bytes of each file. It has 30 bytes, then the name and an extra field. Its two lengths can differ from the lengths of the central directory, so `read` takes them from the local header.

```fern
<<pub function read>>=
const LOCAL_BYTES: i32 = 30;

// The offset of the bytes of the file of `e`.
function data_at(b: [u8], e: Entry): Result[i32, ZipError] {
  let h = need(b, e.header, LOCAL_BYTES)?;
  if (!signed(b, h, 3, 4)) {
    return Err(ZipError { offset: h, reason: "the local header has a bad signature" });
  }
  return need(b, h + LOCAL_BYTES + le16(b, h + 26) + le16(b, h + 28), e.packed);
}

// The bytes of the file of `e` before the checksum is checked.
function unpacked(b: [u8], e: Entry, at: i32): Result[u8[], ZipError] {
  if (e.method == STORED) {
    let out: u8[] = [];
    return Ok(out.concat(b[at:at + e.packed]));
  }
  if (e.method != DEFLATED) {
    return Err(ZipError { offset: e.header, reason: "the compression method is not stored or DEFLATE" });
  }
  return match (deflate.inflate(b[at:at + e.packed], e.size)) {
    Ok(got) => Ok(got.out),
    Err(err) => Err(ZipError { offset: at, reason: err.message() }),
  };
}

// The bytes of the file of the entry `e` of the archive `bytes`.
pub function read(bytes: [u8], e: Entry): Result[u8[], ZipError] {
  if ((e.flags & 1) != 0) {
    return Err(ZipError { offset: e.header, reason: "the file is encrypted" });
  }
  let at = data_at(bytes, e)?;
  let out = unpacked(bytes, e, at)?;
  if (out.len() != e.size) {
    return Err(ZipError { offset: at, reason: "the size is not the size of the central directory" });
  }
  if (crc32.of(out) != e.crc) {
    return Err(ZipError { offset: at, reason: "the checksum does not match the file" });
  }
  return Ok(out);
}
```
