# Secret pages

A key in the heap can go to swap, and a core dump holds each key that is live.
This module keeps secret bytes in pages of their own. The kernel does not write
locked pages to swap, and a core dump leaves the pages out.

`new(len)` maps `len` bytes of fresh pages, from 1 to `MAX_BYTES`. On Linux
and Android the kernel marks the pages `MADV_DONTDUMP` before `new` returns.
When the kernel refuses that mark, `new` returns `Refused` with the errno and
keeps no pages.

Then `new` locks the pages with `mlock`. When the kernel refuses the lock, the
pages stay usable but unlocked: `locked()` is `false`, and `lock_errno()` gives
the errno. A process past its `RLIMIT_MEMLOCK` without `CAP_IPC_LOCK` gets
`ENOMEM` or `EPERM` there.

On macOS the pages are locked, but no per-page dump mark exists. Call
`disable_dumps()` to keep the whole process out of a core dump. A target with
no kernel beneath it, such as WASI, gets `Unsupported`, and `supported()` is
`false` there.

A `Pages` value is a handle. A copy of it names the same pages, and `write`
through one copy shows in each copy. After the last use of the last copy, the
drop writes zeroes over the bytes and unmaps the pages. `wipe()` writes zeroes at
once and keeps the pages.

`read` copies bytes into an ordinary heap array, which is not locked. Erase
that copy with `crypto.erase` when it is no longer necessary.

The two interpreters serve the pages from their own heap. They cannot lock the
pages, so `locked()` is `false` and `lock_errno()` is 38, `ENOSYS`.

`disable_dumps()` calls `prctl(PR_SET_DUMPABLE, 0)` on Linux and Android, and
sets the core size limit to zero on macOS. It returns `true` when the kernel
accepts the change.

```fern test name=crypto-secret-round-trip
import "core/cmp";
import "std/crypto/secret";
import "std/result";

function main(): i32 {
  if (!secret.supported()) { return 0; }
  let Ok(p) = secret.new(32) else { return 1; };
  if (p.len() != 32) { return 2; }
  if (p.write(4, [1u8, 2u8, 3u8]).is_err()) { return 3; }
  let Ok(got) = p.read(3, 5) else { return 4; };
  if (got != [0u8, 1u8, 2u8, 3u8, 0u8]) { return 5; }
  if (p.write(30, [1u8, 2u8, 3u8]).is_ok()) { return 6; }
  if (p.read(0, 33).is_ok()) { return 7; }
  if (p.locked() != (p.lock_errno() == 0)) { return 8; }
  p.wipe();
  let Ok(zero) = p.read(4, 3) else { return 9; };
  if (zero != [0u8, 0u8, 0u8]) { return 10; }
  if (secret.new(0).is_ok()) { return 11; }
  if (secret.new(secret.MAX_BYTES + 1).is_ok()) { return 12; }
  return 0;
}
```

```fern
<<*>>=
// Secret bytes in pages that the kernel keeps out of swap and core dumps.

import "core/cmp";
import "core/error";
import "core/mem";

// The largest size that one call to `new` maps.
pub const MAX_BYTES: i32 = 1048576;

pub enum SecretError {
  // The size is below 1 or above MAX_BYTES.
  Size,
  // The range is outside the pages.
  Range,
  // The target has no kernel that maps pages.
  Unsupported,
  // The kernel refused the mapping or the dump mark, with this errno.
  Refused(i32),
}

pub function (e: SecretError) to_string(): string {
  return e.message();
}

pub function (e: SecretError) message(): string {
  match (e) {
    Size => {
      return "secret: the size is below 1 or above MAX_BYTES";
    },
    Range => {
      return "secret: the range is outside the pages";
    },
    Unsupported => {
      return "secret: the target has no kernel that maps pages";
    },
    Refused(errno) => {
      return "secret: the kernel refused the pages, errno " + errno.to_string();
    }
  }
}

impl error.Error for SecretError {}

impl cmp.Display for SecretError {}

struct Block { addr: i64, size: i64 }

impl mem.Drop for Block {
  function drop(self: Block): void {
    if (target_os() == "linux" || target_os() == "android" || target_os() == "darwin") {
      unsafe {
        secret_unmap(self.addr, self.size);
      }
    }
  }
}

pub opaque struct Pages { block: Block, lock_errno: i32 }

// True when the target maps secret pages. `new` returns `Unsupported` when it is false.
pub function supported(): boolean {
  return target_os() == "linux" || target_os() == "android" || target_os() == "darwin";
}

pub function new(len: i32): Result[Pages, SecretError] {
  if (len < 1 || len > MAX_BYTES) {
    return Err(Size);
  }
  let out: Result[Pages, SecretError] = Err(Unsupported);
  if (target_os() == "linux" || target_os() == "android" || target_os() == "darwin") {
    out = __map(len as i64);
  }
  return out;
}

function __map(size: i64): Result[Pages, SecretError] {
  unsafe {
    let addr = secret_map(size);
    if (addr < 0i64) {
      return Err(Refused((0i64 - addr) as i32));
    }
    let locked = secret_lock(addr, size);
    let errno = 0;
    if (locked < 0i64) {
      errno = (0i64 - locked) as i32;
    }
    return Ok(Pages { block: Block { addr: addr, size: size }, lock_errno: errno });
  }
}

pub function (p: Pages) len(): i32 {
  return p.block.size as i32;
}

// True when the kernel locked the pages in memory.
pub function (p: Pages) locked(): boolean {
  return p.lock_errno == 0;
}

// The errno of the refused lock, or 0 when the pages are locked.
pub function (p: Pages) lock_errno(): i32 {
  return p.lock_errno;
}

function __in_range(p: Pages, off: i32, n: i32): boolean {
  return off >= 0 && n >= 0 && off as i64 + n as i64 <= p.block.size;
}

// Copies `bytes` into the pages at `off`.
pub function (p: Pages) write(off: i32, bytes: [u8]): Result[void, SecretError] {
  if (!__in_range(p, off, bytes.len())) {
    return Err(Range);
  }
  let base = (p.block.addr + off as i64) as usize;
  unsafe {
    for i in 0..bytes.len() {
      __store_u8(base + i as usize, bytes[i] as i32);
    }
  }
  return Ok(());
}

// A heap copy of `n` bytes of the pages at `off`.
pub function (p: Pages) read(off: i32, n: i32): Result[u8[], SecretError] {
  if (!__in_range(p, off, n)) {
    return Err(Range);
  }
  let base = (p.block.addr + off as i64) as usize;
  let out: u8[] = [];
  unsafe {
    for i in 0..n {
      out = out.append(__load_u8(base + i as usize) as u8);
    }
  }
  return Ok(out);
}

// Writes zeroes over each byte of the pages.
pub function (p: Pages) wipe(): void {
  let base = p.block.addr as usize;
  unsafe {
    for i in 0..p.len() {
      __store_u8(base + i as usize, 0);
    }
  }
}

// Keeps this process out of a core dump. True when the kernel accepts it.
pub function disable_dumps(): boolean {
  return disable_core_dumps();
}
```
