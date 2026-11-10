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

`run` returns to the caller at each event that the kernel must see: output, the end of the process, an IRQ, or a call that waits. The other calls are answered in the loop. The kernel reads each pointer argument through the page tables of the process, so a bad pointer gives `EFAULT` and does not fault the kernel.

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

## Files

`with_root` gives a process a root filesystem: a `std/cpio` archive, such as the initrd, with a layer in memory above it. The process then opens, reads, seeks, stats and lists its entries with the Linux calls, and the kernel copies the bytes from the archive. A process has no current directory but the root, so a relative path starts at `/`. A path resolves `.` and `..`, and a symbolic link resolves to its target, for at most 8 links.

The descriptors 0, 1 and 2 are the console. The first file gets descriptor 3, and a closed descriptor is used again.

The process can make directories and files in memory, write them, rename them, change their modes and remove them. The archive stays read-only: a write, a rename or a removal of an entry of the archive gives `EROFS`. A new file in memory can go in a directory of the archive. A file in memory keeps its bytes as a list of chunks, so a long run of writes at the end copies each byte once. `written` gives the kernel the bytes of a file in memory, for example the output of a compile, after the process ends. `image` gives the whole root as one `newc` archive: the entries of the archive, then the files and directories in memory. A kernel can write it to a disk, and read it back as the root of a later boot.

| Call | Number | What the kernel does |
|---|---|---|
| `openat` | 56 | Opens an entry. `O_CREAT` makes a file in memory, and `O_EXCL` gives `EEXIST` for one that exists. `ENOENT` for a missing path, `ENOTDIR` for `O_DIRECTORY` on a file, and `EROFS` for a write access to the archive |
| `close` | 57 | Closes a descriptor |
| `read` | 63 | Copies at most `READ_LIMIT` bytes from the offset, and 0 at the end. `EISDIR` for a directory |
| `lseek` | 62 | Sets the offset from the start, the offset or the end |
| `getdents64` | 61 | Lists `.`, `..` and the children of a directory, as many as the buffer holds |
| `fstat`, `newfstatat` | 80, 79 | Gives the mode, the size, the inode and the link count of an entry |
| `statx` | 291 | Gives the basic stats: the type, the mode, the link count, the owner, the times, the inode, the size and the blocks |
| `readlinkat` | 78 | Gives the target of a symbolic link, with no NUL |
| `getcwd` | 17 | Gives `/` |
| `mkdirat` | 34 | Makes a directory in memory. `EEXIST` for a path that exists, and `ENOENT` or `ENOTDIR` for a parent that is not a directory |
| `write`, `writev` | 64, 66 | For a descriptor of 3 or more, writes at the offset of a file in memory that was opened for writing |
| `renameat`, `renameat2` | 38, 276 | Moves a file or a directory in memory, and replaces a file in memory at the target |
| `unlinkat` | 35 | Removes a file, or with `AT_REMOVEDIR` an empty directory, in memory |
| `fchmod`, `fchmodat` | 52, 53 | Sets the mode of a file or directory in memory |
| `fsync`, `fdatasync` | 82, 83 | Gives 0, because the files are in memory |

`with_exe` names the path of the program in the root. `/proc/self/exe` is then a symbolic link to it. Thus a program can read its own image, as the compiler does for the key of its cache.

## Sockets

`with_net` gives a process a network: a `std/user_net` state over an interface of `std/ip`. The process can then listen on one TCP port and serve connections with the Linux calls, as a `std/serve` program does. A socket, an epoll instance and a signalfd are descriptors, beside the files.

A call that waits ends `run` with `Waiting`, and the next `run` starts the call again. `signal` gives the process a signal, which a signalfd reads. A program that stops on SIGTERM thus stops when the kernel calls `signal(15)`.

| Call | Number | What the kernel does |
|---|---|---|
| `socket` | 198 | Makes a TCP socket for `AF_INET` and `SOCK_STREAM`. `EAFNOSUPPORT` for another family, or with no network |
| `bind`, `listen` | 200, 201 | Takes the port, and listens on it. `EADDRINUSE` for a second listener |
| `accept`, `accept4` | 202, 242 | Takes the oldest connection that completed its handshake, and writes the address of the peer |
| `getsockname`, `getpeername` | 204, 205 | Writes the local or the peer `sockaddr_in` |
| `read`, `recvfrom` | 63, 207 | Takes the bytes that the connection received, and 0 at its end |
| `write`, `writev`, `sendto` | 64, 66, 206 | Queues the bytes and sends them. `EPIPE` for a connection that is gone |
| `shutdown`, `close` | 210, 57 | Ends the stream of the process. `close` of the listener stops it |
| `setsockopt`, `getsockopt` | 208, 209 | Change nothing, and every option reads 0 |
| `fcntl` | 25 | Reads and sets `O_NONBLOCK`. The other commands change nothing |
| `epoll_create1`, `epoll_ctl`, `epoll_pwait` | 20, 21, 22 | Keeps the descriptors and the events of each instance, and gives the ready ones. A timeout is a deadline that the restarted call keeps |
| `signalfd4` | 74 | Makes a signalfd for a mask, or changes the mask. A read gives the lowest pending signal of the mask |

A descriptor with `O_NONBLOCK` gives `EAGAIN` where a blocking one waits.

## Randomness

`with_random` gives the process a source of random bytes, for example a `std/virtio_rng` device. `getrandom` (278) then copies at most `WRITE_LIMIT` bytes from it for each call. A process with no source gets `ENOSYS`, because a kernel must not give bytes that are not random.

## Time

`clock_gettime` (113) reads the generic timer's counter. The monotonic clocks give the counter time since the counter started. `with_clock` sets the wall clock, for example from a real-time clock, and `CLOCK_REALTIME` adds it to the counter time. Without `with_clock` the wall clock starts at 0, in 1970. The kernel gives no vDSO, so the runtime makes the system call.

## Scheduling

An IRQ during EL0 ends `run` with `Interrupted`. The IRQ stays pending at EL0, because the exception entry masks it. `__user_run` then gives back the DAIF of the kernel, and the top half takes the IRQ at EL1 before `run` returns. A scheduler thus calls `run` of the next process at each `Interrupted`. The top half touches no RC value, so the kernel heap is never preempted.

A second core can run processes too, through `serve_core` and a mailbox (see `other cores`). It runs only the EL0 code. Core 0 answers each system call and fault, so only core 0 touches an RC value.

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

<<other cores>>

<<system calls>>

<<sockets>>

<<files>>
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
import "std/cpio";
import "std/elf";
import "std/frames";
import "std/option";
import "std/pagetable";
import "std/raw";
import "std/string";
import "std/user_net";
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

`Stop` is the event that ends a `run`. `Output` is the bytes of a `write` to fd 1 or 2. `Exited` gives the exit status. `Killed` gives the reason that the kernel ended the process. `Interrupted` is an IRQ that arrived while the process ran. The caller handles the IRQ and calls `run` again. `Waiting` is a call that waits for a socket, an epoll instance or a signal. The caller can run other work, and then calls `run` again, which starts the call again.

```fern
<<pub struct Process>>=
pub struct Region { start: usize, end: usize, read: boolean, write: boolean, exec: boolean }

pub struct Process { tables: pagetable.Tables, frames: frames.Frames, frame: usize, regions: Region[], next_map: usize, root: Option[cpio.Archive], files: OpenFile[], exe: string, epoch_ns: i64, random: Option[(i32) => Option[u8[]]], nodes: Node[], next_inode: u32, net: Option[user_net.Net], epolls: Epoll[], signals: u64, wait_until: i64 }

pub enum Stop { Output(i32, u8[]), Exited(i32), Killed(string), Interrupted, Waiting }

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

`region` gives the newest region that holds an address. `page_in` gives the page of an address a zeroed frame with the access of its region. The entry of the page was invalid, so `publish` makes it visible with a barrier and no TLB flush. `resolve` gives the physical address of a user byte for an access, and pages it in when the region allows. `copy_in` and `copy_out` move the bytes of a user range through `resolve`. Thus they check each page of the range.

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
  p.tables.publish();
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
  let p = Process { tables: tables, frames: f, frame: frame, regions: [], next_map: MAP_BASE, root: None, files: [], exe: "", epoch_ns: 0i64, random: None, nodes: [], next_inode: NODE_INODE, net: None, epolls: [], signals: 0 as u64, wait_until: 0i64 };
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
    let (q, stop) = p.event(kind, esr, far);
    p = q;
    match (stop) {
      Some(s) => {
        return (p, s);
      },
      None => {}
    }
  }
  return (p, Killed("unreachable"));
}

// Answers the end of a run at EL0: the kind of __user_run, and ESR_EL1 and
// FAR_EL1 after it. None means that the process continues.
function (p: Process) event(kind: i64, esr: i64, far: i64): (Process, Option[Stop]) {
  if (kind == 1i64) {
    return (p, Some(Interrupted));
  }
  if (kind != 0i64) {
    return (p, Some(Killed("an exception of kind " + kind.to_string())));
  }
  let class = esr >> 26i64 & 63i64;
  if (class == 0x15i64) {
    return p.syscall();
  }
  return (p, p.fault(class, esr, far as usize));
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

### `other cores`

A second core can run the EL0 code of a process, and core 0 answers the events. A `@secondary` function calls `serve_core` with the address of a mailbox of `MAILBOX_SIZE` bytes. `serve_core` is `@norc`, so the second core touches no RC value.

Core 0 posts a process to the mailbox with `post`, and continues with other work. `busy` is true while the core runs the process. `collect` waits for the event and answers it on core 0, with the tables of the process active. The mailbox holds these words:

| Offset | Word |
|---|---|
| 0 | The state: `CORE_IDLE`, `CORE_POSTED`, `CORE_DONE` or `CORE_QUIT` |
| 8 | The address of the trap frame |
| 16 | The root of the tables, for `TTBR0_EL1` |
| 24, 32, 40 | The kind of `__user_run`, `ESR_EL1` and `FAR_EL1` |
| 48 | The number of runs on the core |

The state moves with a release store and an acquire load. Thus each core sees the frame, the tables and the result that the other core wrote before it.

```fern
<<other cores>>=
pub const MAILBOX_SIZE: usize = 56;

pub const CORE_IDLE: i64 = 0;

pub const CORE_POSTED: i64 = 1;

pub const CORE_DONE: i64 = 2;

pub const CORE_QUIT: i64 = 3;

pub struct Core { mailbox: usize }

// A core whose mailbox is at address, with the state CORE_IDLE.
pub function core_at(address: usize): Core {
  for i in 0..7 {
    raw.store_u64(address + (8 * i) as usize, 0 as u64);
  }
  return Core { mailbox: address };
}

// Runs each posted process at EL0 until CORE_QUIT, on the core that calls it.
@norc
pub function serve_core(mailbox: usize): void {
  unsafe {
    let state = __atomic_load_acquire(mailbox);
    while (state != CORE_QUIT) {
      if (state == CORE_POSTED) {
        __sysreg_write("ttbr0_el1", __load_i64(mailbox + 16 as usize));
        __tlb_flush_all();
        let kind = __user_run(__load_i64(mailbox + 8 as usize) as usize);
        __store_i64(mailbox + 24 as usize, kind);
        __store_i64(mailbox + 32 as usize, __sysreg_read("esr_el1"));
        __store_i64(mailbox + 40 as usize, __sysreg_read("far_el1"));
        __store_i64(mailbox + 48 as usize, __load_i64(mailbox + 48 as usize) + 1i64);
        __atomic_store_release(mailbox, CORE_DONE);
      }
      state = __atomic_load_acquire(mailbox);
    }
  }
}

// Gives the process to the core, which runs it at EL0 until an event.
pub function (p: Process) post(c: Core): void {
  raw.store_u64(c.mailbox + 8 as usize, p.frame as u64);
  raw.store_u64(c.mailbox + 16 as usize, p.tables.root as u64);
  unsafe {
    __atomic_store_release(c.mailbox, CORE_POSTED);
  }
}

// True while the core runs a posted process.
pub function (c: Core) busy(): boolean {
  unsafe {
    return __atomic_load_acquire(c.mailbox) == CORE_POSTED;
  }
}

// Waits for the event of the posted process and answers it. None means that
// the process continues, and the caller posts it again.
pub function (p: Process) collect(c: Core): (Process, Option[Stop]) {
  unsafe {
    while (__atomic_load_acquire(c.mailbox) == CORE_POSTED) {}
  }
  let kind = raw.load_u64(c.mailbox + 24 as usize) as i64;
  let esr = raw.load_u64(c.mailbox + 32 as usize) as i64;
  let far = raw.load_u64(c.mailbox + 40 as usize) as i64;
  unsafe {
    __atomic_store_release(c.mailbox, CORE_IDLE);
  }
  p.tables.activate();
  return p.event(kind, esr, far);
}

// The number of runs that the core completed.
pub function (c: Core) runs(): i64 {
  return raw.load_u64(c.mailbox + 48 as usize) as i64;
}

// Stops serve_core. The core must not be busy, or its run overwrites the state.
pub function (c: Core) quit(): void {
  unsafe {
    __atomic_store_release(c.mailbox, CORE_QUIT);
  }
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
  if (p.takes_net_call(n, a0)) {
    return p.net_call(n, a0, a1, a2, reg(f, 3));
  }
  if (n == 64 as u64) {
    return p.write_to(a0, p.copy_in(a1 as usize, limit(a2 as usize)));
  }
  if (n == 66 as u64) {
    return p.write_to(a0, p.gather(a1 as usize, a2 as usize));
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
  if (is_file_call(n)) {
    return p.file_call(n, a0, a1, a2, reg(f, 3));
  }
  if (is_write_call(n)) {
    return p.write_call(n, a0, a1, a2, reg(f, 3));
  }
  return (p, p.signal_call(n, a0, a1, a2));
}

// The signal calls, ioctl, clock_gettime, getrandom, and ENOSYS for each
// other call.
function (p: Process) signal_call(n: u64, a0: u64, a1: u64, a2: u64): Option[Stop] {
  if (n == 113 as u64) {
    return p.clock_gettime(a0, a1 as usize);
  }
  if (n == 278 as u64) {
    return p.getrandom(a0 as usize, a1 as usize, a2);
  }
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

// fd 1 and 2 are the console, and a higher fd a file the process made.
function (p: Process) write_to(fd: u64, bytes: Option[u8[]]): (Process, Option[Stop]) {
  if (fd >= FIRST_FD) {
    let Some(b) = bytes else {
      return (p, p.fail(EFAULT));
    };
    return p.file_write(fd, b);
  }
  return (p, p.write(fd, bytes));
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

### `sockets`

A socket, an epoll instance and a signalfd are descriptors, beside the files. The kind of a descriptor says which it is.

The handle of a socket is its port before `listen`, and the slot of its connection after `accept4`. The generation of a connection socket names the accept of its slot. The handle of an epoll instance is its index in the instances of the process. The mask of a signalfd is the set of its signals.

Each socket call first runs `pump`, so the call sees the frames that arrived. A call that must wait moves the PC back to the `svc`, and `run` ends with `Waiting`. The next `run` starts the call again. A timeout of `epoll_pwait` becomes a deadline, which the call that starts again keeps. A struct `epoll_event` on arm64 has 16 bytes: the events, 4 bytes of padding, and the data.

```fern
<<sockets>>=
const FD_FILE: i32 = 0;

const FD_SOCKET: i32 = 1;

const FD_LISTENER: i32 = 2;

const FD_CONNECTION: i32 = 3;

const FD_EPOLL: i32 = 4;

const FD_SIGNALFD: i32 = 5;

const EAGAIN: i64 = 11;

const EPIPE: i64 = 32;

const ENOTSOCK: i64 = 88;

const EOPNOTSUPP: i64 = 95;

const EAFNOSUPPORT: i64 = 97;

const EADDRINUSE: i64 = 98;

const ENOTCONN: i64 = 107;

const O_NONBLOCK: u64 = 0x800;

const EPOLLIN: u32 = 1;

const EPOLLOUT: u32 = 4;

const EPOLLHUP: u32 = 16;

const SIGINFO_BYTES: usize = 128;

pub struct Watch { fd: u64, events: u32, data: u64 }

pub struct Epoll { watches: Watch[] }

// Gives the process a network.
pub function (p: Process) with_net(n: user_net.Net): Process {
  return Process { ...p, net: Some(n) };
}

// Marks a signal as pending. A signalfd whose mask has it becomes readable.
pub function (p: Process) signal(signo: i32): Process {
  return Process { ...p, signals: p.signals | 1 as u64 << (signo - 1) as u64 };
}

// The connections of the process that closed.
pub function (p: Process) closed_connections(): i32 {
  let Some(n) = p.net else {
    return 0;
  };
  return n.closed_count();
}

const NET_CALLS: i32[] = [198, 200, 201, 202, 242, 204, 205, 206, 207, 208, 209, 210, 25, 20, 21, 22, 74];

const DESCRIPTOR_CALLS: i32[] = [198, 25, 20, 74, 21, 22];

const RECEIVE_CALLS: i32[] = [63, 207];

const SEND_CALLS: i32[] = [64, 206, 66];

function one_of(n: u64, calls: i32[]): boolean {
  for c in calls {
    if (c as u64 == n) {
      return true;
    }
  }
  return false;
}

// The socket, epoll, signalfd and fcntl calls, and read, write, writev and
// close on a descriptor that is not a file.
function (p: Process) takes_net_call(n: u64, fd: u64): boolean {
  return one_of(n, NET_CALLS) || p.special_fd_call(n, fd);
}

// read, write, writev and close on a descriptor that is not a file.
function (p: Process) special_fd_call(n: u64, fd: u64): boolean {
  if (n != 63 as u64 && n != 64 as u64 && n != 66 as u64 && n != 57 as u64) {
    return false;
  }
  let Some(f) = p.open_file(fd) else {
    return false;
  };
  return f.kind != FD_FILE;
}

function (p: Process) net_call(n: u64, a0: u64, a1: u64, a2: u64, a3: u64): (Process, Option[Stop]) {
  let q = p.pump();
  if (one_of(n, DESCRIPTOR_CALLS)) {
    return q.descriptor_call(n, a0, a1, a2, a3);
  }
  let Some(f) = q.open_file(a0) else {
    return q.failed(EBADF);
  };
  if (n == 57 as u64) {
    return q.close_special(a0, f);
  }
  if (one_of(n, RECEIVE_CALLS)) {
    return q.receive(f, a1 as usize, a2 as usize);
  }
  if (one_of(n, SEND_CALLS)) {
    return q.send_call(n, f, a1 as usize, a2 as usize);
  }
  return q.socket_call(n, a0, f, a1, a2, a3);
}

// socket, fcntl, the epoll calls and signalfd4.
function (p: Process) descriptor_call(n: u64, a0: u64, a1: u64, a2: u64, a3: u64): (Process, Option[Stop]) {
  if (n == 198 as u64) {
    return p.socket(a0, a1);
  }
  if (n == 25 as u64) {
    return p.fcntl(a0, a1, a2);
  }
  if (n == 20 as u64) {
    return Process { ...p, epolls: p.epolls.append(Epoll { watches: [] }) }.add_file(special_file(FD_EPOLL, p.epolls.len(), false));
  }
  if (n == 74 as u64) {
    return p.signalfd(a0, a1 as usize, a2, a3);
  }
  return p.epoll_call(n, a0, a1, a2, a3);
}

// write and sendto take one buffer, and writev a vector.
function (p: Process) send_call(n: u64, f: OpenFile, va: usize, len: usize): (Process, Option[Stop]) {
  let bytes = p.copy_in(va, limit(len));
  if (n == 66 as u64) {
    bytes = p.gather(va, len);
  }
  let Some(b) = bytes else {
    return p.failed(EFAULT);
  };
  return p.send(f, b);
}

// bind, listen, accept, the names, the options and shutdown of a socket.
function (p: Process) socket_call(n: u64, fd: u64, f: OpenFile, a1: u64, a2: u64, a3: u64): (Process, Option[Stop]) {
  if (f.kind != FD_SOCKET && f.kind != FD_LISTENER && f.kind != FD_CONNECTION) {
    return p.failed(ENOTSOCK);
  }
  if (n == 200 as u64) {
    let Some(addr) = p.copy_in(a1 as usize, 4 as usize) else {
      return p.failed(EFAULT);
    };
    return p.set_file(fd, OpenFile { ...f, handle: addr[2] as i32 * 256 + addr[3] as i32 }).done(0i64);
  }
  if (n == 201 as u64) {
    return p.listen(fd, f);
  }
  if (n == 202 as u64 || n == 242 as u64) {
    return p.accept(f, a1 as usize, a2 as usize, a3);
  }
  return p.socket_option_call(n, f, a1, a2);
}

// The names, the options and shutdown of a socket.
function (p: Process) socket_option_call(n: u64, f: OpenFile, a1: u64, a2: u64): (Process, Option[Stop]) {
  if (n == 204 as u64 || n == 205 as u64) {
    return p.socket_name(n == 205 as u64, f, a1 as usize, a2 as usize);
  }
  if (n == 209 as u64) {
    // getsockopt: every option reads 0, so SO_ERROR has no error.
    return (p, p.answer(p.copy_out(reg(p.frame, 3) as usize, zeros(4)) && p.copy_out(reg(p.frame, 4) as usize, u32_bytes(4 as u32)), 0i64));
  }
  if (n == 210 as u64) {
    return p.end_stream(f, a1 != 0 as u64);
  }
  // setsockopt: the options change nothing that the kernel does.
  return p.done(0i64);
}

function special_file(kind: i32, handle: i32, nonblock: boolean): OpenFile {
  let mode = 0o140777 as u32;
  if (kind == FD_EPOLL || kind == FD_SIGNALFD) {
    mode = 0o600 as u32;
  }
  let e = cpio.Entry { name: "", inode: 0 as u32, mode: mode, uid: 0 as u32, gid: 0 as u32, links: 1 as u32, mtime: 0 as u32, start: 0, size: 0 };
  return OpenFile { path: "", entry: e, offset: ZERO, open: true, memory: false, writable: true, kind: kind, handle: handle, gen: 0, nonblock: nonblock, mask: 0 as u64 };
}

function (p: Process) pump(): Process {
  let Some(n) = p.net else {
    return p;
  };
  return Process { ...p, net: Some(n.pump(counter_ns() / 1000000i64)) };
}

function (p: Process) socket(domain: u64, kind: u64): (Process, Option[Stop]) {
  if (p.net.is_none() || domain != 2 as u64) {
    return p.failed(EAFNOSUPPORT);
  }
  if ((kind & 15 as u64) != 1 as u64) {
    return p.failed(EOPNOTSUPP);
  }
  return p.add_file(special_file(FD_SOCKET, 0, (kind & O_NONBLOCK) != 0 as u64));
}

function (p: Process) listen(fd: u64, f: OpenFile): (Process, Option[Stop]) {
  let Some(n) = p.net else {
    return p.failed(EAFNOSUPPORT);
  };
  if (f.kind != FD_SOCKET) {
    return p.failed(EINVAL);
  }
  let Some(listening) = n.listen(f.handle) else {
    return p.failed(EADDRINUSE);
  };
  return Process { ...p, net: Some(listening) }.set_file(fd, OpenFile { ...f, kind: FD_LISTENER }).done(0i64);
}

function (p: Process) accept(f: OpenFile, addr: usize, len: usize, flags: u64): (Process, Option[Stop]) {
  let Some(n) = p.net else {
    return p.failed(EINVAL);
  };
  if (f.kind != FD_LISTENER) {
    return p.failed(EINVAL);
  }
  let (taken, found) = n.accept();
  let Some(c) = found else {
    return p.would_block(f);
  };
  let (id, gen) = c;
  let q = Process { ...p, net: Some(taken) };
  if (addr != ZERO && !q.copy_out(addr, sockaddr(taken.peer(id))) || len != ZERO && !q.copy_out(len, u32_bytes(16 as u32))) {
    return q.failed(EFAULT);
  }
  return q.add_file(OpenFile { ...special_file(FD_CONNECTION, id, (flags & O_NONBLOCK) != 0 as u64), gen: gen });
}

// A sockaddr_in: the family, the port and the address in network order.
function sockaddr(peer: (u8[], i32)): u8[] {
  let (address, port) = peer;
  let out = [2u8, 0u8, (port >> 8) as u8, (port & 255) as u8];
  return out.concat(address).concat(zeros(8));
}

function (p: Process) socket_name(peer: boolean, f: OpenFile, addr: usize, len: usize): (Process, Option[Stop]) {
  let Some(n) = p.net else {
    return p.failed(EINVAL);
  };
  let name = sockaddr(([0u8, 0u8, 0u8, 0u8], f.handle));
  if (f.kind == FD_LISTENER) {
    name = sockaddr(([0u8, 0u8, 0u8, 0u8], n.port()));
  }
  if (f.kind == FD_CONNECTION) {
    name = sockaddr((n.iface.address, n.port()));
    if (peer) {
      name = sockaddr(n.peer(f.handle));
    }
  } else if (peer) {
    return p.failed(ENOTCONN);
  }
  return (p, p.answer(p.copy_out(addr, name) && p.copy_out(len, u32_bytes(16 as u32)), 0i64));
}

function (p: Process) receive(f: OpenFile, va: usize, len: usize): (Process, Option[Stop]) {
  if (f.kind == FD_SIGNALFD) {
    return p.read_signal(f, va, len);
  }
  let Some(n) = p.net else {
    return p.failed(EINVAL);
  };
  if (f.kind != FD_CONNECTION) {
    return p.failed(ENOTCONN);
  }
  let (read, got) = n.read(f.handle, f.gen, limit(len) as i32);
  let Some(bytes) = got else {
    return p.would_block(f);
  };
  let q = Process { ...p, net: Some(read) };
  return (q, q.answer(q.copy_out(va, bytes), bytes.len() as i64));
}

function (p: Process) send(f: OpenFile, b: u8[]): (Process, Option[Stop]) {
  let Some(n) = p.net else {
    return p.failed(EINVAL);
  };
  if (f.kind != FD_CONNECTION) {
    return p.failed(ENOTCONN);
  }
  let (written, ok) = n.write(f.handle, f.gen, b);
  if (!ok) {
    return p.failed(EPIPE);
  }
  return Process { ...p, net: Some(written) }.pump().done(b.len() as i64);
}

// shutdown with SHUT_WR or SHUT_RDWR ends the stream of the process.
function (p: Process) end_stream(f: OpenFile, write: boolean): (Process, Option[Stop]) {
  let Some(n) = p.net else {
    return p.done(0i64);
  };
  if (f.kind == FD_CONNECTION && write) {
    return Process { ...p, net: Some(n.close(f.handle, f.gen)) }.pump().done(0i64);
  }
  return p.done(0i64);
}

function (p: Process) close_special(fd: u64, f: OpenFile): (Process, Option[Stop]) {
  let q = p.set_file(fd, OpenFile { ...f, open: false });
  let Some(n) = q.net else {
    return q.done(0i64);
  };
  if (f.kind == FD_CONNECTION) {
    return Process { ...q, net: Some(n.close(f.handle, f.gen)) }.pump().done(0i64);
  }
  if (f.kind == FD_LISTENER) {
    return Process { ...q, net: Some(n.unlisten()) }.done(0i64);
  }
  return q.done(0i64);
}

// A blocking descriptor waits, and a nonblocking one gives EAGAIN.
function (p: Process) would_block(f: OpenFile): (Process, Option[Stop]) {
  if (f.nonblock) {
    return p.failed(EAGAIN);
  }
  return p.wait();
}

// Moves the PC back to the svc, so the next run starts the call again.
function (p: Process) wait(): (Process, Option[Stop]) {
  set_reg(p.frame, 32, reg(p.frame, 32) as i64 - 4i64);
  return (p, Some(Waiting));
}

function (p: Process) fcntl(fd: u64, cmd: u64, arg: u64): (Process, Option[Stop]) {
  let Some(f) = p.open_file(fd) else {
    if (fd < FIRST_FD) {
      return p.done(2i64);
    }
    return p.failed(EBADF);
  };
  if (cmd == 3 as u64) {
    let flags = 2i64;
    if (f.nonblock) {
      flags |= O_NONBLOCK as i64;
    }
    return p.done(flags);
  }
  if (cmd == 4 as u64) {
    return p.set_file(fd, OpenFile { ...f, nonblock: (arg & O_NONBLOCK) != 0 as u64 }).done(0i64);
  }
  // F_GETFD gives no flags, and F_SETFD and the other commands change nothing.
  return p.done(0i64);
}

function (p: Process) signalfd(fd: u64, mask_va: usize, size: u64, flags: u64): (Process, Option[Stop]) {
  if (size != 8 as u64) {
    return p.failed(EINVAL);
  }
  let Some(m) = p.copy_in(mask_va, 8 as usize) else {
    return p.failed(EFAULT);
  };
  let mask = raw_u64(m, 0);
  if (fd == 0xffffffffffffffff as u64) {
    return p.add_file(OpenFile { ...special_file(FD_SIGNALFD, 0, (flags & O_NONBLOCK) != 0 as u64), mask: mask });
  }
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  if (f.kind != FD_SIGNALFD) {
    return p.failed(EINVAL);
  }
  return p.set_file(fd, OpenFile { ...f, mask: mask }).done(fd as i64);
}

// Gives the lowest pending signal of the mask as a signalfd_siginfo.
function (p: Process) read_signal(f: OpenFile, va: usize, len: usize): (Process, Option[Stop]) {
  if (len < SIGINFO_BYTES) {
    return p.failed(EINVAL);
  }
  let ready = p.signals & f.mask;
  if (ready == 0 as u64) {
    return p.would_block(f);
  }
  let signo = 1;
  while ((ready & 1 as u64 << (signo - 1) as u64) == 0 as u64) {
    signo += 1;
  }
  let q = Process { ...p, signals: p.signals & ~(1 as u64 << (signo - 1) as u64) };
  let info = u32_bytes(signo as u32).concat(zeros(124));
  return (q, q.answer(q.copy_out(va, info), SIGINFO_BYTES as i64));
}

function (p: Process) epoll_call(n: u64, epfd: u64, a1: u64, a2: u64, a3: u64): (Process, Option[Stop]) {
  let Some(f) = p.open_file(epfd) else {
    return p.failed(EBADF);
  };
  if (f.kind != FD_EPOLL) {
    return p.failed(EINVAL);
  }
  if (n == 21 as u64) {
    return p.epoll_ctl(f.handle, a1, a2, a3 as usize);
  }
  return p.epoll_wait(f.handle, a1 as usize, a2 as i32, (a3 as u32) as i32);
}

function (p: Process) epoll_ctl(ep: i32, op: u64, fd: u64, event: usize): (Process, Option[Stop]) {
  if (p.open_file(fd).is_none()) {
    return p.failed(EBADF);
  }
  let watches = p.epoll(ep).watches;
  let kept: Watch[] = [];
  let found = false;
  for w in watches {
    if (w.fd == fd) {
      found = true;
    } else {
      kept = kept.append(w);
    }
  }
  if (op == 1 as u64 && found) {
    return p.failed(EEXIST);
  }
  if (op != 1 as u64 && !found) {
    return p.failed(ENOENT);
  }
  if (op != 2 as u64) {
    let Some(e) = p.copy_in(event, 16 as usize) else {
      return p.failed(EFAULT);
    };
    kept = kept.append(Watch { fd: fd, events: raw_u64(e, 0) as u32, data: raw_u64(e, 8) });
  }
  return p.set_epoll(ep, Epoll { watches: kept }).done(0i64);
}

function (p: Process) epoll(ep: i32): Epoll {
  return p.epolls[ep];
}

function (p: Process) set_epoll(ep: i32, e: Epoll): Process {
  return Process { ...p, epolls: p.epolls.with(ep, e) };
}

function (p: Process) epoll_wait(ep: i32, va: usize, max: i32, timeout: i32): (Process, Option[Stop]) {
  if (max <= 0) {
    return p.failed(EINVAL);
  }
  let (out, count) = p.ready_events(ep, max);
  let now = counter_ns();
  if (count > 0 || timeout == 0 || p.wait_until != 0i64 && now >= p.wait_until) {
    let q = Process { ...p, wait_until: 0i64 };
    return (q, q.answer(q.copy_out(va, out), count as i64));
  }
  if (timeout > 0 && p.wait_until == 0i64) {
    return Process { ...p, wait_until: now + timeout as i64 * 1000000i64 }.wait();
  }
  return p.wait();
}

// The struct epoll_event of each ready descriptor of instance ep, at most max.
function (p: Process) ready_events(ep: i32, max: i32): (u8[], i32) {
  let out: u8[] = [];
  let count = 0;
  for w in p.epoll(ep).watches {
    let ready = p.readiness(w.fd) & (w.events | EPOLLHUP);
    if (ready != 0 as u32 && count < max) {
      out = out.concat(u32_bytes(ready)).concat(zeros(4)).concat(u64_bytes(w.data));
      count += 1;
    }
  }
  return (out, count);
}

// The epoll events that the descriptor has now.
function (p: Process) readiness(fd: u64): u32 {
  let Some(f) = p.open_file(fd) else {
    return EPOLLHUP;
  };
  if (f.kind == FD_SIGNALFD) {
    return events_if((p.signals & f.mask) != 0 as u64, EPOLLIN);
  }
  let Some(n) = p.net else {
    return EPOLLIN | EPOLLOUT;
  };
  if (f.kind == FD_LISTENER) {
    return events_if(n.pending(), EPOLLIN);
  }
  if (f.kind == FD_CONNECTION) {
    return connection_readiness(n, f);
  }
  if (f.kind == FD_SOCKET || f.kind == FD_EPOLL) {
    return 0 as u32;
  }
  return EPOLLIN | EPOLLOUT;
}

function connection_readiness(n: user_net.Net, f: OpenFile): u32 {
  let events = events_if(n.readable(f.handle, f.gen), EPOLLIN);
  if (n.live(f.handle, f.gen)) {
    return events | EPOLLOUT;
  }
  return events | EPOLLHUP;
}

function events_if(ok: boolean, events: u32): u32 {
  if (ok) {
    return events;
  }
  return 0 as u32;
}
```

### `files`

An open file holds its entry and its offset. For a directory the offset is the index of the next name in the listing. The root directory has no entry in an archive, so `root_entry` stands for it.

```fern
<<files>>=
pub struct OpenFile { path: string, entry: cpio.Entry, offset: usize, open: boolean, memory: boolean, writable: boolean, kind: i32, handle: i32, gen: i32, nonblock: boolean, mask: u64 }

// A file or a directory that the process made, in memory. The entry holds its
// kind, mode, inode and size, as for an entry of the archive.
pub struct Node { path: string, entry: cpio.Entry, chunks: u8[][] }

// openat and newfstatat take this as the directory of a relative path.
const AT_FDCWD: u64 = 0xffffffffffffff9c;

const AT_SYMLINK_NOFOLLOW: u64 = 0x100;

const AT_EMPTY_PATH: u64 = 0x1000;

const O_CREAT: u64 = 0x40;

const O_TRUNC: u64 = 0x200;

const O_DIRECTORY: u64 = 0x4000;

const FIRST_FD: u64 = 3;

const MAX_FILES: i32 = 256;

const PATH_MAX: usize = 4096;

pub const READ_LIMIT: usize = 1048576;

const ENOENT: i64 = 2;

const ENOTDIR: i64 = 20;

const EISDIR: i64 = 21;

const EMFILE: i64 = 24;

const ESPIPE: i64 = 29;

const EROFS: i64 = 30;

const ERANGE: i64 = 34;

const ENAMETOOLONG: i64 = 36;

const ELOOP: i64 = 40;

// Gives the process the archive as its root filesystem.
pub function (p: Process) with_root(a: cpio.Archive): Process {
  return Process { ...p, root: Some(a) };
}

// The time of the generic timer's counter, in nanoseconds since it started.
pub function counter_ns(): i64 {
  unsafe {
    let c = __sysreg_read("cntvct_el0");
    let f = __sysreg_read("cntfrq_el0");
    return c / f * 1000000000i64 + c % f * 1000000000i64 / f;
  }
}

// Sets the wall clock of the process: unix_ns is the Unix time now, in
// nanoseconds, for example from a real-time clock.
pub function (p: Process) with_clock(unix_ns: i64): Process {
  return Process { ...p, epoch_ns: unix_ns - counter_ns() };
}

// CLOCK_REALTIME and its coarse form, 0 and 5, add the wall clock to the
// counter. CLOCK_MONOTONIC, its raw and coarse forms and CLOCK_BOOTTIME, 1, 4,
// 6 and 7, are the counter.
function (p: Process) clock_gettime(clock: u64, va: usize): Option[Stop] {
  let now = counter_ns();
  if (clock == 0 as u64 || clock == 5 as u64) {
    now += p.epoch_ns;
  } else if (clock != 1 as u64 && clock != 4 as u64 && clock != 6 as u64 && clock != 7 as u64) {
    return p.fail(EINVAL);
  }
  let ts = u64_bytes((now / 1000000000i64) as u64).concat(u64_bytes((now % 1000000000i64) as u64));
  return p.answer(p.copy_out(va, ts), 0i64);
}

// Gives the process a source of random bytes for getrandom: source(n) gives n
// bytes, or None when it fails.
pub function (p: Process) with_random(source: (i32) => Option[u8[]]): Process {
  return Process { ...p, random: Some(source) };
}

const EIO: i64 = 5;

// GRND_NONBLOCK, GRND_RANDOM and GRND_INSECURE are the flags. A source never
// blocks, so each flag gives the same bytes.
function (p: Process) getrandom(va: usize, len: usize, flags: u64): Option[Stop] {
  if ((flags & ~(7 as u64)) != 0 as u64) {
    return p.fail(EINVAL);
  }
  let Some(source) = p.random else {
    return p.fail(ENOSYS);
  };
  let n = limit(len);
  let Some(bytes) = source(n as i32) else {
    return p.fail(EIO);
  };
  return p.answer(p.copy_out(va, bytes), bytes.len() as i64);
}

// Names the path of the program in the root, which /proc/self/exe links to.
pub function (p: Process) with_exe(path: string): Process {
  return Process { ...p, exe: path };
}

const SELF_EXE: string = "proc/self/exe";

// The symbolic link at /proc/self/exe.
function (p: Process) exe_link(): cpio.Entry {
  return cpio.Entry { ...root_entry(), name: SELF_EXE, mode: 0xa1ff as u32, links: 1 as u32, size: p.exe.len() };
}

// The path that the link at f points to.
function (p: Process) link_target(f: OpenFile): u8[] {
  if (f.path == SELF_EXE) {
    return p.exe.bytes();
  }
  let Some(a) = p.root else {
    return [];
  };
  return a.read(f.entry);
}

function (p: Process) readlinkat(dirfd: u64, va: usize, buf: usize, len: usize): Option[Stop] {
  match (p.find_at(dirfd, va, false)) {
    Ok(f) => {
      if (!f.entry.is_symlink()) {
        return p.fail(EINVAL);
      }
      let target = p.link_target(f);
      if (target.len() as usize > len) {
        target = array.slice(target, 0, len as i32);
      }
      return p.answer(p.copy_out(buf, target), target.len() as i64);
    },
    Err(errno) => {
      return p.fail(errno);
    }
  }
}

// STATX_BASIC_STATS: the type, the mode, the link count, the owner, the
// three times, the inode, the size and the blocks.
const STATX_BASIC_STATS: u32 = 0x7ff;

function (p: Process) statx(dirfd: u64, va: usize, flags: u64, buf: usize): Option[Stop] {
  let path = match (p.path_in(va)) {
    Ok(s) => s,
    Err(errno) => {
      return p.fail(errno);
    },
  };
  let found: Option[OpenFile] = None;
  if (path == "" && (flags & AT_EMPTY_PATH) != 0 as u64) {
    found = p.open_file(dirfd);
  } else {
    match (p.find_at(dirfd, va, (flags & AT_SYMLINK_NOFOLLOW) == 0 as u64)) {
      Ok(f) => {
        found = Some(f);
      },
      Err(errno) => {
        return p.fail(errno);
      }
    }
  }
  let Some(f) = found else {
    return p.fail(EBADF);
  };
  return p.answer(p.copy_out(buf, statx_bytes(f.entry)), 0i64);
}

// A struct statx of 256 bytes.
function statx_bytes(e: cpio.Entry): u8[] {
  let out = u32_bytes(STATX_BASIC_STATS).concat(u32_bytes(PAGE as u32)).concat(zeros(8));
  out = out.concat(u32_bytes(e.links)).concat(u32_bytes(e.uid)).concat(u32_bytes(e.gid));
  out = out.concat(array.slice(u32_bytes(e.mode), 0, 2)).concat(zeros(2));
  out = out.concat(u64_bytes(e.inode as u64)).concat(u64_bytes(e.size as u64)).concat(u64_bytes(((e.size + 511) / 512) as u64));
  out = out.concat(zeros(8));
  for i in 0..4 {
    out = out.concat(u64_bytes(e.mtime as u64)).concat(zeros(8));
  }
  return out.concat(zeros(256 - out.len()));
}

function is_file_call(n: u64): boolean {
  return n == 17 as u64 || n == 56 as u64 || n == 57 as u64 || n == 61 as u64 || n == 62 as u64 || n == 63 as u64 || n == 78 as u64 || n == 79 as u64 || n == 80 as u64 || n == 291 as u64;
}

function (p: Process) file_call(n: u64, a0: u64, a1: u64, a2: u64, a3: u64): (Process, Option[Stop]) {
  if (n == 56 as u64) {
    return p.openat(a0, a1 as usize, a2, a3);
  }
  if (n == 57 as u64) {
    return p.close(a0);
  }
  if (n == 63 as u64) {
    return p.read(a0, a1 as usize, a2 as usize);
  }
  if (n == 62 as u64) {
    return p.lseek(a0, a1 as i64, a2);
  }
  if (n == 61 as u64) {
    return p.getdents(a0, a1 as usize, a2 as usize);
  }
  if (n == 80 as u64) {
    return (p, p.stat_to(p.open_file(a0), a1 as usize));
  }
  if (n == 79 as u64) {
    return (p, p.fstatat(a0, a1 as usize, a2 as usize, a3));
  }
  if (n == 291 as u64) {
    return (p, p.statx(a0, a1 as usize, a2, reg(p.frame, 4) as usize));
  }
  if (n == 78 as u64) {
    return (p, p.readlinkat(a0, a1 as usize, a2 as usize, a3 as usize));
  }
  return p.getcwd(a0 as usize, a1 as usize);
}

function (p: Process) getcwd(va: usize, len: usize): (Process, Option[Stop]) {
  if (len < 2 as usize) {
    return p.failed(ERANGE);
  }
  return (p, p.answer(p.copy_out(va, "/".bytes().append(0 as u8)), 2i64));
}

function (p: Process) done(v: i64): (Process, Option[Stop]) {
  set_reg(p.frame, 0, v);
  return (p, None);
}

function (p: Process) failed(errno: i64): (Process, Option[Stop]) {
  return p.done(-errno);
}

// The NUL-terminated path at va.
function (p: Process) path_in(va: usize): Result[string, i64] {
  let out: u8[] = [];
  while (out.len() as usize < PATH_MAX) {
    let at = va + out.len() as usize;
    let Some(chunk) = p.copy_in(at, PAGE - at % PAGE) else {
      return Err(EFAULT);
    };
    for b in chunk {
      if (b == 0 as u8) {
        return utf8_path(out);
      }
      out = out.append(b);
    }
  }
  return Err(ENAMETOOLONG);
}

function utf8_path(b: u8[]): Result[string, i64] {
  unsafe {
    return Ok(string_from_bytes_unchecked(b));
  }
}

// The path of `path` from the directory `base`, with no leading "/", and with
// "." and ".." resolved.
pub function resolve(base: string, path: string): string {
  let full = path;
  if (!path.starts_with("/")) {
    full = base + "/" + path;
  }
  let parts: string[] = [];
  for part in full.split("/") {
    if (part == "..") {
      if (parts.len() > 0) {
        parts = array.slice(parts, 0, parts.len() - 1);
      }
    } else if (part != "" && part != ".") {
      parts = parts.append(part);
    }
  }
  return parts.join("/");
}

function root_entry(): cpio.Entry {
  return cpio.Entry { name: "", inode: ROOT_INODE, mode: 0x41ed as u32, uid: 0 as u32, gid: 0 as u32, links: 2 as u32, mtime: 0 as u32, start: 0, size: 0 };
}

// The entry at path, through at most 8 symbolic links, and the path of the
// entry. follow false keeps a link at the end of the path.
function (p: Process) lookup(path: string, follow: boolean): Result[OpenFile, i64] {
  let at = path;
  for hops in 0..9 {
    match (p.hop(at, follow)) {
      Found(f) => {
        return Ok(f);
      },
      Next(next) => {
        at = next;
      },
      Missing => {
        return Err(ENOENT);
      }
    }
  }
  return Err(ELOOP);
}

// One step of a lookup: the entry at path, or the path that a link at path
// points to.
enum Hop { Found(OpenFile), Next(string), Missing }

function (p: Process) hop(at: string, follow: boolean): Hop {
  let Some(a) = p.root else {
    return Missing;
  };
  if (at == "") {
    return Found(archive_file("", root_entry()));
  }
  if (at == SELF_EXE && p.exe != "") {
    if (follow) {
      return Next(resolve("", p.exe));
    }
    return Found(archive_file(at, p.exe_link()));
  }
  if let Some(f) = p.node_file(at) {
    return Found(f);
  }
  let Some(e) = a.find(at) else {
    return Missing;
  };
  if (!e.is_symlink() || !follow) {
    return Found(archive_file(at, e));
  }
  unsafe {
    return Next(resolve(parent_of(at), string_from_bytes_unchecked(a.read(e))));
  }
}

function (p: Process) node_file(path: string): Option[OpenFile] {
  let Some(i) = p.node_index(path) else {
    return None;
  };
  return Some(OpenFile { path: path, entry: p.nodes[i].entry, offset: ZERO, open: true, memory: true, writable: false, kind: FD_FILE, handle: 0, gen: 0, nonblock: false, mask: 0 as u64 });
}

function archive_file(path: string, e: cpio.Entry): OpenFile {
  return OpenFile { path: path, entry: e, offset: ZERO, open: true, memory: false, writable: false, kind: FD_FILE, handle: 0, gen: 0, nonblock: false, mask: 0 as u64 };
}

function parent_of(path: string): string {
  let parts = path.split("/");
  return array.slice(parts, 0, parts.len() - 1).join("/");
}

// The open file of descriptor fd.
function (p: Process) open_file(fd: u64): Option[OpenFile] {
  if (fd < FIRST_FD || fd - FIRST_FD >= p.files.len() as u64) {
    return None;
  }
  let f = p.files[(fd - FIRST_FD) as i32];
  if (!f.open) {
    return None;
  }
  return Some(f);
}

// The directory that a relative path starts from.
function (p: Process) base_of(dirfd: u64, path: string): Result[string, i64] {
  if (path.starts_with("/") || dirfd == AT_FDCWD) {
    return Ok("");
  }
  let Some(f) = p.open_file(dirfd) else {
    return Err(EBADF);
  };
  if (!f.entry.is_dir()) {
    return Err(ENOTDIR);
  }
  return Ok(f.path);
}

function (p: Process) find_at(dirfd: u64, va: usize, follow: boolean): Result[OpenFile, i64] {
  match (p.path_in(va)) {
    Ok(path) => {
      match (p.base_of(dirfd, path)) {
        Ok(base) => {
          return p.lookup(resolve(base, path), follow);
        },
        Err(errno) => {
          return Err(errno);
        }
      }
    },
    Err(errno) => {
      return Err(errno);
    }
  }
}

function (p: Process) openat(dirfd: u64, va: usize, flags: u64, mode: u64): (Process, Option[Stop]) {
  let write = (flags & 3 as u64) != 0 as u64;
  match (p.find_at(dirfd, va, true)) {
    Ok(f) => {
      return p.open_found(f, flags, write);
    },
    Err(errno) => {
      if (errno != ENOENT || (flags & O_CREAT) == 0 as u64) {
        return p.failed(errno);
      }
      return p.create_at(dirfd, va, mode);
    }
  }
}

// Opens an entry that exists. Only a file in memory can be written.
function (p: Process) open_found(f: OpenFile, flags: u64, write: boolean): (Process, Option[Stop]) {
  let trunc = (flags & O_TRUNC) != 0 as u64;
  let errno = open_errno(f, flags, write || trunc);
  if (errno != 0i64) {
    return p.failed(errno);
  }
  let q = p;
  if (trunc) {
    q = p.set_node(f.path, [], 0);
  }
  return q.add_file(OpenFile { ...f, entry: q.entry_of(f), writable: write });
}

// Gives f the lowest free descriptor.
function (p: Process) add_file(f: OpenFile): (Process, Option[Stop]) {
  for i in 0..p.files.len() {
    if (!p.files[i].open) {
      let q = Process { ...p, files: p.files.with(i, f) };
      return q.done(FIRST_FD as i64 + i as i64);
    }
  }
  if (p.files.len() >= MAX_FILES) {
    return p.failed(EMFILE);
  }
  let q = Process { ...p, files: p.files.append(f) };
  return q.done(FIRST_FD as i64 + p.files.len() as i64);
}

function (p: Process) set_file(fd: u64, f: OpenFile): Process {
  return Process { ...p, files: p.files.with((fd - FIRST_FD) as i32, f) };
}

function (p: Process) close(fd: u64): (Process, Option[Stop]) {
  if (fd < FIRST_FD) {
    return p.done(0i64);
  }
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  let q = p.set_file(fd, OpenFile { ...f, open: false });
  return q.done(0i64);
}

function (p: Process) read(fd: u64, va: usize, len: usize): (Process, Option[Stop]) {
  if (fd == 0 as u64) {
    return p.done(0i64);
  }
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  if (f.entry.is_dir()) {
    return p.failed(EISDIR);
  }
  let size = p.size_of(f);
  let n = ZERO;
  if (f.offset < size) {
    n = size - f.offset;
  }
  if (n > len) {
    n = len;
  }
  if (n > READ_LIMIT) {
    n = READ_LIMIT;
  }
  if (!p.copy_out(va, p.bytes_of(f, f.offset, n))) {
    return p.failed(EFAULT);
  }
  let q = p.set_file(fd, OpenFile { ...f, offset: f.offset + n });
  return q.done(n as i64);
}

// SEEK_SET is 0, SEEK_CUR 1 and SEEK_END 2.
function (p: Process) lseek(fd: u64, offset: i64, whence: u64): (Process, Option[Stop]) {
  if (fd < FIRST_FD) {
    return p.failed(ESPIPE);
  }
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  let from = 0i64;
  if (whence == 1 as u64) {
    from = f.offset as i64;
  } else if (whence == 2 as u64) {
    from = p.size_of(f) as i64;
  } else if (whence != 0 as u64) {
    return p.failed(EINVAL);
  }
  if (from + offset < 0i64) {
    return p.failed(EINVAL);
  }
  let q = p.set_file(fd, OpenFile { ...f, offset: (from + offset) as usize });
  return q.done(from + offset);
}

// The names of a directory listing: ".", ".." and the children.
function (p: Process) listing(f: OpenFile): cpio.Entry[] {
  let Some(a) = p.root else {
    return [];
  };
  let dot = cpio.Entry { ...f.entry, name: "." };
  let up = cpio.Entry { ...root_entry(), name: ".." };
  let out = [dot, up].concat(a.children(f.path));
  for n in p.nodes {
    if (parent_of(n.path) == f.path) {
      out = out.append(n.entry);
    }
  }
  return out;
}

// A linux_dirent64 record: the inode, the offset of the next record, the
// length of this one, the type, and the name with a NUL, padded to 8 bytes.
function dirent(e: cpio.Entry, next: usize): u8[] {
  let name = e.name.split("/");
  let bytes = name[name.len() - 1].bytes().append(0 as u8);
  let len = (19 + bytes.len() + 7) / 8 * 8;
  let kind = 8;
  if (e.is_dir()) {
    kind = 4;
  } else if (e.is_symlink()) {
    kind = 10;
  }
  let out = u64_bytes(e.inode as u64).concat(u64_bytes(next as u64));
  out = out.append((len & 255) as u8).append((len >> 8) as u8).append(kind as u8).concat(bytes);
  return out.concat(zeros(len - out.len()));
}

function (p: Process) getdents(fd: u64, va: usize, len: usize): (Process, Option[Stop]) {
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  if (!f.entry.is_dir()) {
    return p.failed(ENOTDIR);
  }
  let names = p.listing(f);
  let out: u8[] = [];
  let i = f.offset;
  while (i < names.len() as usize) {
    let record = dirent(names[i as i32], i + 1 as usize);
    if (out.len() as usize + record.len() as usize > len) {
      break;
    }
    out = out.concat(record);
    i += 1 as usize;
  }
  if (out.len() == 0 && i < names.len() as usize) {
    return p.failed(EINVAL);
  }
  if (!p.copy_out(va, out)) {
    return p.failed(EFAULT);
  }
  let q = p.set_file(fd, OpenFile { ...f, offset: i });
  return q.done(out.len() as i64);
}

function (p: Process) fstatat(dirfd: u64, va: usize, buf: usize, flags: u64): Option[Stop] {
  let path = match (p.path_in(va)) {
    Ok(s) => s,
    Err(errno) => {
      return p.fail(errno);
    },
  };
  if (path == "" && (flags & AT_EMPTY_PATH) != 0 as u64) {
    return p.stat_to(p.open_file(dirfd), buf);
  }
  match (p.find_at(dirfd, va, (flags & AT_SYMLINK_NOFOLLOW) == 0 as u64)) {
    Ok(f) => {
      return p.stat_to(Some(f), buf);
    },
    Err(errno) => {
      return p.fail(errno);
    }
  }
}

// Writes the stat of f to buf: an arm64 struct stat of 128 bytes.
function (p: Process) stat_to(f: Option[OpenFile], buf: usize): Option[Stop] {
  let Some(file) = f else {
    return p.fail(EBADF);
  };
  let e = file.entry;
  let out = u64_bytes(1 as u64).concat(u64_bytes(e.inode as u64));
  out = out.concat(u32_bytes(e.mode)).concat(u32_bytes(e.links)).concat(u32_bytes(e.uid)).concat(u32_bytes(e.gid));
  out = out.concat(zeros(16)).concat(u64_bytes(e.size as u64)).concat(u32_bytes(PAGE as u32)).concat(zeros(4));
  out = out.concat(u64_bytes(((e.size + 511) / 512) as u64));
  for i in 0..3 {
    out = out.concat(u64_bytes(e.mtime as u64)).concat(zeros(8));
  }
  return p.answer(p.copy_out(buf, out.concat(zeros(8))), 0i64);
}

function u32_bytes(v: u32): u8[] {
  return array.slice(u64_bytes(v as u64), 0, 4);
}

// The writable files and directories of a process, in memory.

const O_EXCL: u64 = 0x80;

const AT_REMOVEDIR: u64 = 0x200;

const EEXIST: i64 = 17;

const ENOTEMPTY: i64 = 39;

const S_IFDIR: u32 = 0x4000;

const S_IFREG: u32 = 0x8000;

// The inodes of the nodes start here, above those of an archive, and the root
// has the last inode.
const NODE_INODE: u32 = 0x40000000;

const ROOT_INODE: u32 = 0xffffffff;

// The bytes of the file in memory at path, for the kernel to read after the
// process ends.
pub function (p: Process) written(path: string): Option[u8[]] {
  let Some(i) = p.node_index(resolve("", path)) else {
    return None;
  };
  return Some(join_chunks(p.nodes[i].chunks));
}

// The root of the process as one cpio archive: the entries of the archive,
// then the files and directories in memory, in the order of their creation.
pub function (p: Process) image(): u8[] {
  let items: cpio.Item[] = [];
  if let Some(a) = p.root {
    for e in a.entries {
      items = items.append(cpio.Item { name: cpio.clean(e.name), mode: e.mode, data: a.read(e) });
    }
  }
  for n in p.nodes {
    items = items.append(cpio.Item { name: cpio.clean(n.path), mode: n.entry.mode, data: join_chunks(n.chunks) });
  }
  return cpio.write(items);
}

function join_chunks(chunks: u8[][]): u8[] {
  let out: u8[] = [];
  for c in chunks {
    out = out.concat(c);
  }
  return out;
}

function (p: Process) node_index(path: string): Option[i32] {
  for i in 0..p.nodes.len() {
    if (p.nodes[i].path == path) {
      return Some(i);
    }
  }
  return None;
}

// The entry of f as it is now: a node can change after the open.
function (p: Process) entry_of(f: OpenFile): cpio.Entry {
  if (!f.memory) {
    return f.entry;
  }
  let Some(i) = p.node_index(f.path) else {
    return f.entry;
  };
  return p.nodes[i].entry;
}

function (p: Process) size_of(f: OpenFile): usize {
  return p.entry_of(f).size as usize;
}

// n bytes of f from offset.
function (p: Process) bytes_of(f: OpenFile, offset: usize, n: usize): u8[] {
  if (!f.memory) {
    let Some(a) = p.root else {
      return [];
    };
    let at = f.entry.start + offset as i32;
    return array.slice(a.bytes, at, at + n as i32);
  }
  let Some(i) = p.node_index(f.path) else {
    return [];
  };
  let all = join_chunks(p.nodes[i].chunks);
  return array.slice(all, offset as i32, (offset + n) as i32);
}

// Gives the node at path the chunks and the size.
function (p: Process) set_node(path: string, chunks: u8[][], size: i32): Process {
  let Some(i) = p.node_index(path) else {
    return p;
  };
  let n = p.nodes[i];
  let entry = cpio.Entry { ...n.entry, size: size };
  return Process { ...p, nodes: p.nodes.with(i, Node { ...n, entry: entry, chunks: chunks }) };
}

// Each node gets an inode that no other node had, because a program can tell
// two directories apart by their inodes.
function (p: Process) add_node(path: string, mode: u32, links: u32): Process {
  let entry = cpio.Entry { name: path, inode: p.next_inode, mode: mode, uid: 0 as u32, gid: 0 as u32, links: links, mtime: 0 as u32, start: 0, size: 0 };
  return Process { ...p, nodes: p.nodes.append(Node { path: path, entry: entry, chunks: [] }), next_inode: p.next_inode + 1 as u32 };
}

// The path at va from dirfd, and whether its parent is a directory.
function (p: Process) new_path(dirfd: u64, va: usize): Result[string, i64] {
  match (p.path_in(va)) {
    Ok(path) => {
      match (p.base_of(dirfd, path)) {
        Ok(base) => {
          let full = resolve(base, path);
          if (full == "") {
            return Err(EEXIST);
          }
          return p.parent_dir(full);
        },
        Err(errno) => {
          return Err(errno);
        }
      }
    },
    Err(errno) => {
      return Err(errno);
    }
  }
}

function (p: Process) parent_dir(full: string): Result[string, i64] {
  match (p.lookup(parent_of(full), true)) {
    Ok(dir) => {
      if (!dir.entry.is_dir()) {
        return Err(ENOTDIR);
      }
      return Ok(full);
    },
    Err(errno) => {
      return Err(errno);
    }
  }
}

// Why f cannot be opened with flags, or 0. change is a write or a truncation.
function open_errno(f: OpenFile, flags: u64, change: boolean): i64 {
  if ((flags & O_CREAT) != 0 as u64 && (flags & O_EXCL) != 0 as u64) {
    return EEXIST;
  }
  if ((flags & O_DIRECTORY) != 0 as u64 && !f.entry.is_dir()) {
    return ENOTDIR;
  }
  if (change && f.entry.is_dir()) {
    return EISDIR;
  }
  if (change && !f.memory) {
    return EROFS;
  }
  return 0i64;
}

function (p: Process) create_at(dirfd: u64, va: usize, mode: u64): (Process, Option[Stop]) {
  match (p.new_path(dirfd, va)) {
    Ok(path) => {
      let q = p.add_node(path, S_IFREG | (mode & 0xfff as u64) as u32, 1 as u32);
      let Some(i) = q.node_index(path) else {
        return q.failed(EIO);
      };
      return q.add_file(OpenFile { path: path, entry: q.nodes[i].entry, offset: ZERO, open: true, memory: true, writable: true, kind: FD_FILE, handle: 0, gen: 0, nonblock: false, mask: 0 as u64 });
    },
    Err(errno) => {
      return p.failed(errno);
    }
  }
}

function (p: Process) mkdirat(dirfd: u64, va: usize, mode: u64): (Process, Option[Stop]) {
  if let Ok(found) = p.find_at(dirfd, va, false) {
    return p.failed(EEXIST);
  }
  match (p.new_path(dirfd, va)) {
    Ok(path) => {
      let q = p.add_node(path, S_IFDIR | (mode & 0xfff as u64) as u32, 2 as u32);
      return q.done(0i64);
    },
    Err(errno) => {
      return p.failed(errno);
    }
  }
}

// Writes b at the offset of fd. A write at the end adds a chunk, so a long
// run of writes copies each byte once.
function (p: Process) file_write(fd: u64, b: u8[]): (Process, Option[Stop]) {
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  if (!f.writable) {
    return p.failed(EBADF);
  }
  let Some(i) = p.node_index(f.path) else {
    return p.failed(EBADF);
  };
  let n = p.nodes[i];
  let size = n.entry.size as usize;
  let chunks = n.chunks;
  if (f.offset == size) {
    chunks = chunks.append(b);
  } else {
    chunks = [overwrite(join_chunks(chunks), f.offset, b)];
  }
  let end = f.offset + b.len() as usize;
  if (end < size) {
    end = size;
  }
  let q = p.set_node(f.path, chunks, end as i32).set_file(fd, OpenFile { ...f, offset: f.offset + b.len() as usize });
  return q.done(b.len() as i64);
}

// all with b written at offset, and zeros before offset past the end.
function overwrite(all: u8[], offset: usize, b: u8[]): u8[] {
  let out = all;
  while (out.len() as usize < offset) {
    out = out.append(0 as u8);
  }
  for i in 0..b.len() {
    let at = offset as i32 + i;
    if (at < out.len()) {
      out = out.with(at, b[i]);
    } else {
      out = out.append(b[i]);
    }
  }
  return out;
}

function (p: Process) set_mode(path: string, mode: u64): (Process, Option[Stop]) {
  let Some(i) = p.node_index(path) else {
    return p.failed(EROFS);
  };
  let n = p.nodes[i];
  let kind = n.entry.mode & 0xf000 as u32;
  let entry = cpio.Entry { ...n.entry, mode: kind | (mode & 0xfff as u64) as u32 };
  let q = Process { ...p, nodes: p.nodes.with(i, Node { ...n, entry: entry }) };
  return q.done(0i64);
}

function (p: Process) fchmod(fd: u64, mode: u64): (Process, Option[Stop]) {
  let Some(f) = p.open_file(fd) else {
    return p.failed(EBADF);
  };
  return p.set_mode(f.path, mode);
}

function (p: Process) fchmodat(dirfd: u64, va: usize, mode: u64): (Process, Option[Stop]) {
  match (p.find_at(dirfd, va, true)) {
    Ok(f) => {
      return p.set_mode(f.path, mode);
    },
    Err(errno) => {
      return p.failed(errno);
    }
  }
}

// Moves the node at from, and each node below it, to to. An existing file at
// to goes away, as with rename.
function (p: Process) renameat(olddir: u64, oldva: usize, newdir: u64, newva: usize): (Process, Option[Stop]) {
  let Ok(from) = p.find_at(olddir, oldva, false) else {
    return p.failed(ENOENT);
  };
  if (!from.memory) {
    return p.failed(EROFS);
  }
  match (p.new_path(newdir, newva)) {
    Ok(to) => {
      if let Ok(old) = p.lookup(to, false) {
        if (!old.memory) {
          return p.failed(EROFS);
        }
      }
      let kept = p.without(to);
      return kept.moved(from.path, to).done(0i64);
    },
    Err(errno) => {
      return p.failed(errno);
    }
  }
}

function (p: Process) without(path: string): Process {
  let kept: Node[] = [];
  for n in p.nodes {
    if (n.path != path) {
      kept = kept.append(n);
    }
  }
  return Process { ...p, nodes: kept };
}

function (p: Process) moved(from: string, to: string): Process {
  let out: Node[] = [];
  for n in p.nodes {
    let path = n.path;
    if (path == from) {
      path = to;
    } else if (path.starts_with(from + "/")) {
      path = to + slice_unchecked(path, from.len(), path.len());
    }
    out = out.append(Node { ...n, path: path, entry: cpio.Entry { ...n.entry, name: path } });
  }
  return Process { ...p, nodes: out };
}

function (p: Process) unlinkat(dirfd: u64, va: usize, flags: u64): (Process, Option[Stop]) {
  let Ok(f) = p.find_at(dirfd, va, false) else {
    return p.failed(ENOENT);
  };
  if (!f.memory) {
    return p.failed(EROFS);
  }
  let dir = (flags & AT_REMOVEDIR) != 0 as u64;
  if (dir != f.entry.is_dir()) {
    if (dir) {
      return p.failed(ENOTDIR);
    }
    return p.failed(EISDIR);
  }
  for n in p.nodes {
    if (parent_of(n.path) == f.path) {
      return p.failed(ENOTEMPTY);
    }
  }
  return p.without(f.path).done(0i64);
}

// The calls that change the filesystem in memory.
function is_write_call(n: u64): boolean {
  return n == 34 as u64 || n == 35 as u64 || n == 38 as u64 || n == 52 as u64 || n == 53 as u64 || n == 82 as u64 || n == 83 as u64 || n == 276 as u64;
}

function (p: Process) write_call(n: u64, a0: u64, a1: u64, a2: u64, a3: u64): (Process, Option[Stop]) {
  if (n == 34 as u64) {
    return p.mkdirat(a0, a1 as usize, a2);
  }
  if (n == 35 as u64) {
    return p.unlinkat(a0, a1 as usize, a2);
  }
  if (n == 38 as u64 || n == 276 as u64) {
    return p.renameat(a0, a1 as usize, a2, a3 as usize);
  }
  if (n == 52 as u64) {
    return p.fchmod(a0, a1);
  }
  if (n == 53 as u64) {
    return p.fchmodat(a0, a1 as usize, a2);
  }
  // fsync and fdatasync: the files are in memory, so there is nothing to write.
  if (p.open_file(a0).is_none()) {
    return p.failed(EBADF);
  }
  return p.done(0i64);
}
```
