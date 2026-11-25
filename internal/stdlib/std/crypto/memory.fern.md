# Keeping secrets out of swap and core dumps

This module keeps key material out of the two places where memory leaves a process: the swap device and a core dump.

`lock_pages(bytes)` keeps the pages that hold `bytes` in RAM (`mlock`), so the kernel does not write a key to swap. Android swaps to zram, so a key in an unlocked page can go to compressed swap. `no_core_dumps()` stops a crash dump of the process memory to a file: `prctl(PR_SET_DUMPABLE, 0)` on Linux and Android, and a zero `RLIMIT_CORE` on Darwin.

## Model

The pages stay locked until the process exits. Another value can share a page, so the module does not unlock a page. `RLIMIT_MEMLOCK` limits the locked memory. An Android app can lock 64 KiB by default, so lock only the arrays that hold keys.

A copy of the bytes is in a different block, and that block is not locked. For example, `with` on a shared array makes a copy. Lock the array that holds the key after you make it, and `crypto.erase` it before you release it.

## Contracts

`lock_pages` gives `Refused` with the errno when the kernel refuses: `EPERM`, `ENOMEM` above `RLIMIT_MEMLOCK`, or `EAGAIN`. It gives `Unsupported` in an interpreter, which has no native pages. An empty array needs no lock and gives `Ok`. `no_core_dumps` gives `Refused` when neither call succeeds.

The two functions use the `rlimit` capability of the target. Linux, Android and Darwin have it. On WASI, a call of either function is the compile error E066.

## Executable example

```fern test name=std-crypto-memory-example
import "std/crypto/memory";

function main(): i32 {
  let key = __alloc_u8(32);
  match (memory.lock_pages(key)) {
    Ok(_) => {},
    Err(memory.Refused(_)) => {},
    Err(memory.Unsupported) => {}
  }
  let Ok(_) = memory.lock_pages([]) else { return 1; };
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<errors>>

<<locked pages>>

<<core dumps>>
```

### Errors

```fern
<<errors>>=
// Keep key material out of swap and core dumps.
import "core/cmp";
import "core/error";

pub enum MemoryError {
  Refused(i32),
  Unsupported,
}

impl error.Error for MemoryError {}

impl cmp.Display for MemoryError {}

pub function (e: MemoryError) message(): string {
  return match (e) {
    Refused(errno) => "crypto memory: the kernel refused, errno " + errno.to_string(),
    Unsupported => "crypto memory: this target cannot lock pages in memory",
  };
}

pub function (e: MemoryError) to_string(): string {
  return e.message();
}
```

### Locked pages

```fern
<<locked pages>>=
const ENOSYS: i64 = 38i64;

pub function lock_pages(bytes: [u8]): Result[void, MemoryError] {
  if (bytes.len() == 0) {
    return Ok(());
  }
  let r = __mlock_bytes(bytes);
  if (r == 0i64) {
    return Ok(());
  }
  if (r == 0i64 - ENOSYS) {
    return Err(Unsupported);
  }
  return Err(Refused((0i64 - r) as i32));
}
```

### Core dumps

```fern
<<core dumps>>=
pub function no_core_dumps(): Result[void, MemoryError] {
  if (disable_core_dumps()) {
    return Ok(());
  }
  return Err(Refused(0));
}
```
