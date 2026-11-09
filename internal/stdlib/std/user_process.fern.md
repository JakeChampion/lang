# User processes

A user process of a kernel is a static Linux program that runs at EL0 in its own page tables. This module loads the program from its ELF image, runs it with `__user_run`, and answers its system calls with the Linux ABI (milestone 4 of `docs/BARE-METAL-PLAN.md`, #14263). A program that the compiler builds with `-target arm64-linux` thus runs on the kernel with no change. `docs/FERNOS-LINUX-ABI.md` records the calls of the compiler, the program that milestone 6 runs.

Only arm64 runs a process now. x86-64 enters ring 3 in a later slice of #14263.

## The address space

The caller gives `load` a fresh `std/pagetable` mapping that holds the kernel mapping, and the frames for the process. Each process thus has its own tables, and two processes can use the same addresses. The kernel pages stay kernel-only, so a process that touches one takes a fault.

The process has three kinds of user memory:

- **The segments.** `load` copies each page that holds bytes of the file into a frame. The rest of a segment, which is zero, gets a frame when the program first touches it.
- **The start stack.** It takes the `STACK_BYTES` below `STACK_TOP`. `load` writes the argument and environment strings, and the vectors of the Linux start convention, at its top.
- **The `mmap` reservations.** An anonymous `mmap` reserves the next free range from `MAP_BASE`, with one free page after it. The kernel ignores the address hint, because the call does not give `MAP_FIXED`. A page of a reservation gets a zeroed frame when the program first touches it. Thus the 16 GiB heap and the 1 GiB stack of the Fern runtime cost only the pages that the program uses.

A region records the access of a range. `mprotect` adds a region that covers the older ones, and changes the pages that are already present with `protect_user`. A touch of an address in no region, or an access that the region does not allow, ends the process.

## The system calls

`run` returns to the caller at each event that the kernel must see: output, the end of the process, or an IRQ. The other calls are answered in the loop. The kernel reads each pointer argument through the page tables of the process, so a bad pointer gives `EFAULT` and does not fault the kernel.

| Call | Number | What the kernel does |
|---|---|---|
| `write` | 64 | Gives the bytes to the caller as `Output`, for fd 1 and 2, and `EBADF` for any other fd |
| `writev` | 66 | The same, for the bytes of all the vectors |
| `exit`, `exit_group` | 93, 94 | Gives `Exited` with the low 8 bits of the status |
| `mmap` | 222 | Reserves an anonymous range. `EBADF` for a file mapping, `EINVAL` for `MAP_FIXED` or a length of 0 |
| `mprotect` | 226 | Changes the access of a range of the regions, and `ENOMEM` for a range outside them |
| `sigaltstack`, `rt_sigaction`, `rt_sigprocmask` | 132, 134, 135 | Gives 0. A fault ends the process, and no signal is delivered |
| `ioctl` | 29 | `ENOTTY`, because no fd is a terminal |
| any other | | `ENOSYS` |

`write` gives at most `WRITE_LIMIT` bytes for each call, which the Linux ABI allows. A program then writes the rest in a second call.

## Scheduling

An IRQ during EL0 ends `run` with `Interrupted`. The IRQ stays pending at EL0, because the exception entry masks it. `__user_run` then gives back the DAIF of the kernel, and the top half takes the IRQ at EL1 before `run` returns. A scheduler thus calls `run` of the next process at each `Interrupted`. The top half touches no RC value, so the kernel heap is never preempted.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<pub struct Process>>

<<memory>>

<<load>>

<<release>>

<<run>>

<<system calls>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a static Linux program as a user process of a kernel.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/array";
import "std/elf";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/raw";
```

### `constants`

The trap frame of `__user_run` holds x0 to x30 from offset 0, then `SP_EL0`, the PC and PSTATE.

```fern
<<constants>>=
const SP_AT: usize = 248;

const PC_AT: usize = 256;

const PAGE: usize = 4096;

const ZERO: usize = 0;

// The start stack takes the STACK_BYTES below STACK_TOP.
pub const STACK_TOP: usize = 0x1000000000;

pub const STACK_BYTES: usize = 0x800000;

// mmap reserves ranges from MAP_BASE to MAP_END.
pub const MAP_BASE: usize = 0x1000200000;

pub const MAP_END: usize = 0x8000000000;

pub const WRITE_LIMIT: usize = 65536;

const EBADF: i64 = 9;

const ENOMEM: i64 = 12;

const EFAULT: i64 = 14;

const EINVAL: i64 = 22;

const ENOTTY: i64 = 25;

const ENOSYS: i64 = 38;

// The ELF machine of arm64.
const EM_AARCH64: u32 = 183;
```

### `pub struct Process`

`Stop` is the event that ends a `run`. `Output` is the bytes of a `write` to fd 1 or 2. `Exited` gives the exit status. `Killed` gives the reason that the kernel ended the process. `Interrupted` is an IRQ that arrived while the process ran. The caller handles the IRQ and calls `run` again.

```fern
<<pub struct Process>>=
pub struct Region { start: usize, end: usize, read: boolean, write: boolean, exec: boolean }

pub struct Process { tables: pagetable.Tables, frames: frames.Frames, frame: usize, regions: Region[], next_map: usize }

pub enum Stop { Output(i32, u8[]), Exited(i32), Killed(string), Interrupted }

function reg(frame: usize, n: i32): u64 {
  return raw.load_u64(frame + (8 * n) as usize);
}

function set_reg(frame: usize, n: i32, v: i64): void {
  raw.store_u64(frame + (8 * n) as usize, v as u64);
}

function page_down(a: usize): usize {
  return a & ~(PAGE - 1 as usize);
}

function page_up(a: usize): usize {
  return page_down(a + PAGE - 1 as usize);
}

function zero(frame: usize): void {
  let i = ZERO;
  while (i < PAGE) {
    raw.store_u64(frame + i, 0 as u64);
    i += 8 as usize;
  }
}
```

### `memory`

`region` gives the newest region that holds an address. `page_in` gives the page of an address a zeroed frame with the access of its region. `resolve` gives the physical address of a user byte for an access, and pages it in when the region allows. `copy_in` and `copy_out` move the bytes of a user range through `resolve`. Thus they check each page of the range.

```fern
<<memory>>=
function (p: Process) region(va: usize): Option[Region] {
  let i = p.regions.len() - 1;
  while (i >= 0) {
    let r = p.regions[i];
    if (va >= r.start && va < r.end) {
      return Some(r);
    }
    i -= 1;
  }
  return None;
}

function (p: Process) page_in(va: usize): boolean {
  let page = page_down(va);
  let Some(r) = p.region(page) else {
    return false;
  };
  if (!r.read && !r.write && !r.exec) {
    return false;
  }
  if (p.tables.leaf(page).is_some()) {
    return false;
  }
  let Some(frame) = p.frames.alloc() else {
    return false;
  };
  zero(frame);
  if (!p.tables.map_user(page, frame, PAGE, r.write, r.exec)) {
    return false;
  }
  p.tables.flush();
  return true;
}

function (p: Process) resolve(va: usize, write: boolean): Option[usize] {
  if (p.tables.leaf(va).is_none() && !p.page_in(va)) {
    return None;
  }
  let Some(l) = p.tables.leaf(va) else {
    return None;
  };
  if (!l.user || !l.valid || write && !l.write) {
    return None;
  }
  return Some(l.physical);
}

function (p: Process) copy_in(va: usize, len: usize): Option[u8[]] {
  let out: u8[] = [];
  let at = ZERO;
  while (at < len) {
    let Some(pa) = p.resolve(va + at, false) else {
      return None;
    };
    let n = PAGE - (va + at) % PAGE;
    if (n > len - at) {
      n = len - at;
    }
    let i = ZERO;
    while (i < n) {
      out = out.append(raw.load_u8(pa + i));
      i += 1 as usize;
    }
    at += n;
  }
  return Some(out);
}

function (p: Process) copy_out(va: usize, data: u8[]): boolean {
  let len = data.len() as usize;
  let at = ZERO;
  while (at < len) {
    let Some(pa) = p.resolve(va + at, true) else {
      return false;
    };
    let n = PAGE - (va + at) % PAGE;
    if (n > len - at) {
      n = len - at;
    }
    let i = ZERO;
    while (i < n) {
      raw.store_u8(pa + i, data[(at + i) as i32]);
      i += 1 as usize;
    }
    at += n;
  }
  return true;
}

function u64_bytes(v: u64): u8[] {
  let out: u8[] = [];
  for i in 0..8 {
    out = out.append((v >> (8 * i) as u64 & 0xff as u64) as u8);
  }
  return out;
}

function zeros(n: i32): u8[] {
  let out: u8[] = [];
  for i in 0..n {
    out = out.append(0 as u8);
  }
  return out;
}
```

### `load`

`load` checks that the image is a static arm64 executable, and that each segment lies below the start stack and outside the kernel mapping. Two segments can share a page: the page then gets the access of both.

The code of an executable page must reach instruction fetch. `sync_code` thus calls `__cache_sync_code` for each line of the page, at the smallest line size that `CTR_EL0` reports.

From the stack pointer up, the start stack has `argc`, the `argv` pointers, a null, the `envp` pointers and a null. The auxiliary vector follows. The vector gives `AT_PAGESZ` and `AT_ENTRY`. The strings are above it, at the top of the stack.

```fern
<<load>>=
// Loads the static program `image` into `tables`, with frames from `f`, and
// sets it to start with the arguments `argv` and the environment `envp`.
pub function load(f: frames.Frames, tables: pagetable.Tables, image: u8[], argv: string[], envp: string[]): Result[Process, string] {
  let Ok(img) = elf.parse(image) else {
    return Err("the image is not an ELF64 file");
  };
  if (img.kind != 2 as u32 || img.machine != EM_AARCH64) {
    return Err("the image is not a static arm64 executable");
  }
  let Some(frame) = f.alloc() else {
    return Err("no frame for the trap frame");
  };
  zero(frame);
  let p = Process { tables: tables, frames: f, frame: frame, regions: [], next_map: MAP_BASE };
  for s in img.loads() {
    let Ok(q) = p.load_segment(s, image) else {
      return Err("a segment does not load");
    };
    p = q;
  }
  p = p.add_region(STACK_TOP - STACK_BYTES, STACK_TOP, true, true, false);
  let Some(sp) = p.start_stack(argv, envp, img.entry) else {
    return Err("the start stack does not fit");
  };
  raw.store_u64(frame + SP_AT, sp as u64);
  raw.store_u64(frame + PC_AT, img.entry);
  return Ok(p);
}

function (p: Process) add_region(start: usize, end: usize, read: boolean, write: boolean, exec: boolean): Process {
  return Process { ...p, regions: p.regions.append(Region { start: start, end: end, read: read, write: write, exec: exec }) };
}

function (p: Process) load_segment(s: elf.Segment, image: u8[]): Result[Process, string] {
  let start = s.address as usize;
  let end = start + s.memory_size as usize;
  if (start < PAGE || end < start || end > STACK_TOP - STACK_BYTES) {
    return Err("the segment lies outside the user range");
  }
  let Ok(data) = s.bytes(image) else {
    return Err("the segment lies outside the image");
  };
  let file_end = start + data.len() as usize;
  let page = page_down(start);
  while (page < file_end) {
    let Some(frame) = p.segment_page(page, s) else {
      return Err("the segment overlaps the kernel, or the frames ran out");
    };
    copy_page(frame, page, start, data);
    if (s.executable()) {
      sync_code(frame);
    }
    page += PAGE;
  }
  p.tables.flush();
  return Ok(p.add_region(page_down(start), page_up(end), s.readable(), s.writable(), s.executable()));
}

// Copies the bytes of data, which start at the address start, that lie in the
// page at page.
function copy_page(frame: usize, page: usize, start: usize, data: u8[]): void {
  let end = start + data.len() as usize;
  let at = page;
  if (at < start) {
    at = start;
  }
  while (at < page + PAGE && at < end) {
    raw.store_u8(frame + (at - page), data[(at - start) as i32]);
    at += 1 as usize;
  }
}

// The frame of a page of segment s, which an earlier segment can already hold.
function (p: Process) segment_page(page: usize, s: elf.Segment): Option[usize] {
  match (p.tables.leaf(page)) {
    Some(l) => {
      if (!l.user || !p.tables.protect_user(page, PAGE, true, l.write || s.writable(), l.exec || s.executable())) {
        return None;
      }
      return Some(l.physical);
    },
    None => {}
  }
  let Some(frame) = p.frames.alloc() else {
    return None;
  };
  zero(frame);
  if (!p.tables.map_user(page, frame, PAGE, s.writable(), s.executable())) {
    return None;
  }
  return Some(frame);
}

function sync_code(frame: usize): void {
  unsafe {
    let ctr = __sysreg_read("s3_3_c0_c0_1");
    let line = 4i64 << (ctr >> 16i64 & 15i64);
    let iline = 4i64 << (ctr & 15i64);
    if (iline < line) {
      line = iline;
    }
    let at = ZERO;
    while (at < PAGE) {
      __cache_sync_code(frame + at);
      at += line as usize;
    }
  }
}

// Writes the start stack and gives its stack pointer.
function (p: Process) start_stack(argv: string[], envp: string[], entry: u64): Option[usize] {
  let top = STACK_TOP;
  let pointers: u64[] = [];
  for s in argv.concat(envp) {
    let bytes = s.bytes().append(0 as u8);
    top -= bytes.len() as usize;
    if (!p.copy_out(top, bytes)) {
      return None;
    }
    pointers = pointers.append(top as u64);
  }
  let vector: u8[] = u64_bytes(argv.len() as u64);
  for i in 0..pointers.len() {
    if (i == argv.len()) {
      vector = vector.concat(u64_bytes(0 as u64));
    }
    vector = vector.concat(u64_bytes(pointers[i]));
  }
  if (envp.len() == 0) {
    vector = vector.concat(u64_bytes(0 as u64));
  }
  vector = vector.concat(u64_bytes(0 as u64));
  for v in [6 as u64, PAGE as u64, 9 as u64, entry, 0 as u64, 0 as u64] {
    vector = vector.concat(u64_bytes(v));
  }
  let sp = top - vector.len() as usize & ~(15 as usize);
  if (sp < STACK_TOP - STACK_BYTES || !p.copy_out(sp, vector)) {
    return None;
  }
  return Some(sp);
}
```

### `release`

`release` gives back every frame of an ended process: its tables, its pages and its trap frame. The caller first makes another mapping active, because the core still translates through the tables of the process after `run`.

```fern
<<release>>=
pub function (p: Process) release(): void {
  p.tables.free_user();
  let freed = p.frames.free(p.frame);
}
```

### `run`

`run` makes the tables of the process the active translation and runs it until an event. A synchronous exception is a system call, a fault, or another class. A translation fault in a region that allows the access pages the address in, and the process continues. Any other fault ends the process. The fault status code is bits 0 to 5 of `ESR_EL1`. The codes 4 to 7 are a translation fault at levels 0 to 3.

```fern
<<run>>=
pub function (p: Process) run(): (Process, Stop) {
  p.tables.activate();
  while (true) {
    let kind = 0i64;
    let esr = 0i64;
    let far = 0i64;
    unsafe {
      kind = __user_run(p.frame);
      esr = __sysreg_read("esr_el1");
      far = __sysreg_read("far_el1");
    }
    if (kind == 1i64) {
      return (p, Interrupted);
    }
    if (kind != 0i64) {
      return (p, Killed("an exception of kind " + kind.to_string()));
    }
    let class = esr >> 26i64 & 63i64;
    let stop: Option[Stop] = None;
    if (class == 0x15i64) {
      let (q, done) = p.syscall();
      p = q;
      stop = done;
    } else {
      stop = p.fault(class, esr, far as usize);
    }
    match (stop) {
      Some(s) => {
        return (p, s);
      },
      None => {}
    }
  }
  return (p, Killed("unreachable"));
}

// Pages in the address of a translation fault in a region, or ends the process.
function (p: Process) fault(class: i64, esr: i64, far: usize): Option[Stop] {
  let translation = (esr & 0x3ci64) == 4i64;
  if ((class == 0x20i64 || class == 0x24i64) && translation && p.page_in(far)) {
    return None;
  }
  return Some(Killed("a fault of class " + class.to_string() + " at " + far.to_string() + " from " + reg(p.frame, 32).to_string()));
}
```

### `system calls`

```fern
<<system calls>>=
function (p: Process) syscall(): (Process, Option[Stop]) {
  let f = p.frame;
  let n = reg(f, 8);
  let a0 = reg(f, 0);
  let a1 = reg(f, 1);
  let a2 = reg(f, 2);
  if (n == 64 as u64) {
    return (p, p.write(a0, p.copy_in(a1 as usize, limit(a2 as usize))));
  }
  if (n == 66 as u64) {
    return (p, p.write(a0, p.gather(a1 as usize, a2 as usize)));
  }
  if (n == 93 as u64 || n == 94 as u64) {
    return (p, Some(Exited((a0 & 255 as u64) as i32)));
  }
  if (n == 222 as u64) {
    return p.mmap(a1 as usize, a2, reg(f, 3));
  }
  if (n == 226 as u64) {
    return p.mprotect(a0 as usize, a1 as usize, a2);
  }
  return (p, p.signal_call(n, a0, a1, a2));
}

// The signal calls, ioctl, and ENOSYS for each other call.
function (p: Process) signal_call(n: u64, a0: u64, a1: u64, a2: u64): Option[Stop] {
  if (n == 132 as u64) {
    // An old stack_t: no stack, with ss_flags SS_DISABLE.
    return p.answer(a1 == 0 as u64 || p.copy_out(a1 as usize, zeros(8).concat(u64_bytes(2 as u64)).concat(zeros(8))), 0i64);
  }
  if (n == 134 as u64) {
    return p.sigaction(a0, a2, reg(p.frame, 3));
  }
  if (n == 135 as u64) {
    return p.answer(a2 == 0 as u64 || p.copy_out(a2 as usize, zeros(8)), 0i64);
  }
  if (n == 29 as u64) {
    return p.fail(ENOTTY);
  }
  return p.fail(ENOSYS);
}

// SIGKILL and SIGSTOP take no action, and the kernel sigset is 8 bytes.
function (p: Process) sigaction(signal: u64, old: u64, set_bytes: u64): Option[Stop] {
  if (signal < 1 as u64 || signal > 64 as u64 || signal == 9 as u64 || signal == 19 as u64 || set_bytes != 8 as u64) {
    return p.fail(EINVAL);
  }
  return p.answer(old == 0 as u64 || p.copy_out(old as usize, zeros(32)), 0i64);
}

function limit(n: usize): usize {
  if (n > WRITE_LIMIT) {
    return WRITE_LIMIT;
  }
  return n;
}

// Sets the result of the call: v when ok, EFAULT when not.
function (p: Process) answer(ok: boolean, v: i64): Option[Stop] {
  if (!ok) {
    return p.fail(EFAULT);
  }
  set_reg(p.frame, 0, v);
  return None;
}

function (p: Process) fail(errno: i64): Option[Stop] {
  set_reg(p.frame, 0, -errno);
  return None;
}

function (p: Process) write(fd: u64, bytes: Option[u8[]]): Option[Stop] {
  if (fd != 1 as u64 && fd != 2 as u64) {
    return p.fail(EBADF);
  }
  let Some(b) = bytes else {
    return p.fail(EFAULT);
  };
  set_reg(p.frame, 0, b.len() as i64);
  return Some(Output(fd as i32, b));
}

// The bytes of the iovec array at iov, up to WRITE_LIMIT.
function (p: Process) gather(iov: usize, count: usize): Option[u8[]] {
  if (count > 1024 as usize) {
    return None;
  }
  let out: u8[] = [];
  let i = ZERO;
  while (i < count && out.len() as usize < WRITE_LIMIT) {
    let Some(entry) = p.copy_in(iov + 16 as usize * i, 16 as usize) else {
      return None;
    };
    let base = raw_u64(entry, 0);
    let len = limit(raw_u64(entry, 8) as usize);
    let Some(b) = p.copy_in(base as usize, len) else {
      return None;
    };
    out = out.concat(b);
    i += 1 as usize;
  }
  if (out.len() as usize > WRITE_LIMIT) {
    return Some(array.slice(out, 0, WRITE_LIMIT as i32));
  }
  return Some(out);
}

function raw_u64(b: u8[], at: i32): u64 {
  let v = 0 as u64;
  for i in 0..8 {
    v |= b[at + i] as u64 << (8 * i) as u64;
  }
  return v;
}

// MAP_ANONYMOUS is 0x20. MAP_FIXED is 0x10, and MAP_FIXED_NOREPLACE 0x100000.
function (p: Process) mmap(len: usize, prot: u64, flags: u64): (Process, Option[Stop]) {
  if ((flags & 0x20 as u64) == 0 as u64) {
    return (p, p.fail(EBADF));
  }
  if ((flags & 0x100010 as u64) != 0 as u64 || len == ZERO) {
    return (p, p.fail(EINVAL));
  }
  let size = page_up(len);
  if (size < len || size > MAP_END - p.next_map) {
    return (p, p.fail(ENOMEM));
  }
  let at = p.next_map;
  let q = Process { ...p.add_region(at, at + size, (prot & 1 as u64) != 0 as u64, (prot & 2 as u64) != 0 as u64, (prot & 4 as u64) != 0 as u64), next_map: at + size + PAGE };
  set_reg(q.frame, 0, at as i64);
  return (q, None);
}

function (p: Process) mprotect(va: usize, len: usize, prot: u64): (Process, Option[Stop]) {
  if (va % PAGE != ZERO) {
    return (p, p.fail(EINVAL));
  }
  let size = page_up(len);
  let at = va;
  while (at < va + size) {
    if (p.region(at).is_none()) {
      return (p, p.fail(ENOMEM));
    }
    at += PAGE;
  }
  let read = (prot & 1 as u64) != 0 as u64;
  let write = (prot & 2 as u64) != 0 as u64;
  let exec = (prot & 4 as u64) != 0 as u64;
  if (!p.tables.protect_user(va, size, read || write || exec, write, exec)) {
    return (p, p.fail(EINVAL));
  }
  p.tables.flush();
  let q = p.add_region(va, va + size, read, write, exec);
  set_reg(q.frame, 0, 0i64);
  return (q, None);
}
```
