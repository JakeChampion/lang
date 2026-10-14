# Net P7: io_uring — the plan

How #9861 is built, in the order it lands. One PR per slice. Each slice
names its gate. The backend is opt-in and Linux-only, and it is kept only
if it wins on the measurements of slice 5. A backend that does not win is
deleted, not left in as a second path to maintain.

## Where it sits

The serve loop runs on `async.Driver`, a readiness interface: `watch(fd,
interest)`, `unwatch(fd)`, and `wait_into(events, timeout)` answering
(fd, readiness) pairs. Behind it, `RealDriver` keeps one reactor per loop
through three builtins, `reactor_new`, `reactor_ctl` and `reactor_wait`:
epoll on Linux, kqueue on Darwin, a table of wasi:io pollables on wasm.
The loop then calls `tcp_accept`, `tcp_recv_into` and `tcp_send_buf`
itself on a ready descriptor.

io_uring is a completion interface: the program queues operations on a
submission ring and reads their results off a completion ring, and one
`io_uring_enter` both submits and waits. Its advantage over epoll for a
server is fewer system calls per request, not a faster single call:
multishot accept and multishot receive leave an operation armed across
many completions, and the received bytes land in buffers the program
handed the kernel beforehand (provided buffer rings), so a readable
connection costs no `recv` call. Research report 1 puts a naive swap at
1.06 to 1.10 times epoll and engineered use (batching, provided buffers,
zero-copy send) at up to 2.3 times. That is why the slices below are
ordered by the system calls each removes, and why the first slice alone
is expected to measure level with epoll.

The backend stays behind the Driver. `serve.Config.io_uring` (false, set
by `with_io_uring()` or by `FERN_IO_URING=1` in the environment, as libuv
takes `UV_USE_IO_URING`) asks for it; the loop's driver tries the ring when
it is created and falls back to epoll for the whole loop when the ring
cannot be had
(`io_uring_setup` refused by seccomp with EPERM, absent with ENOSYS, or a
kernel without the 6.1 setup flags with EINVAL). It is decided once, at
the driver's creation, never per call. Darwin keeps kqueue and wasm keeps
wasi:sockets; there the ring builtins answer ENOSYS and the fallback is
the only path.

## What the kernel was measured to do

Measured on this repository's container (kernel 6.18) before any code,
since each fact decides a design point below:

- **A multishot poll is edge-triggered.** `IORING_OP_POLL_ADD` with
  `IORING_POLL_ADD_MULTI` on a socket with ten unread bytes posts one
  completion and then none while the bytes stay unread, none after a
  partial read, and one at the peer's EOF that is not repeated. The loop
  is written against level-triggered epoll: it accepts one connection
  per listener event and leaves a connection whose response is pending
  readable for the next pass. The ring therefore uses single-shot polls,
  each re-armed as its completion is read, which reports a descriptor
  that is still ready at once, as epoll does.
- **Every poll reports a peer's half-close.** io_uring adds `POLLRDHUP`
  to the events of every `POLL_ADD`, asked for or not, so a peer that has
  shut down its writing side completes a poll with `POLLIN | POLLRDHUP`.
  epoll reports `EPOLLRDHUP` only when asked, and the loop never asks: a
  half-closed connection whose handler is parked still owes its response.
  The ring reports hang-up for `POLLHUP` and `POLLERR` alone, as the
  reactor does for `EPOLLHUP` and `EPOLLERR`.
- **A pending poll keeps its file open past the process.** A poll the
  ring holds references the descriptor's file, and when the process dies
  the ring is torn down asynchronously, a few milliseconds later. A
  worker's own listener (`reuse_port`) polled by the ring therefore stays
  in the port's group after its worker has exited, and the connections the
  kernel steers to it in that window are reset when it finally closes:
  `TestSelfHostSupervisedServeReusePortWorkers` lost its post-crash request
  in 4 of 10 runs under load, against none under epoll, the packet capture
  showing the handshake answered and reset 5 to 8 ms after the crash.
  epoll's registration holds no reference. The ring therefore keeps an
  exclusive watch (interest 4) in an epoll set of its own, polled by the
  ring, and the serve loop watches its listener exclusively, shared or not,
  which also gives the ring epoll's one wake-up per connection where
  workers share a listener.
- **The ring cannot be mapped at an address the program chooses.**
  `mmap` of the ring descriptor with `MAP_FIXED`, `MAP_FIXED_NOREPLACE`
  or a bare hint address fails with EINVAL. The ring's state (the
  descriptor, the three mapped regions and their offsets) lives in a
  block the runtime allocates when the ring is made, and the driver
  holds its address as an opaque `i64` handle the stdlib never reads
  through. Nothing is kept in a runtime global.
- **`IORING_SETUP_SINGLE_ISSUER | IORING_SETUP_DEFER_TASKRUN` is
  accepted.** Under `DEFER_TASKRUN` the kernel posts completions only
  inside the issuing task's own `io_uring_enter`, so the ring's head and
  tail words are never written while the program reads them, and plain
  loads and stores around the system call are ordered by it. The runtime
  helpers need no atomic operations.

The feature floor is the one #9861 names: kernel 6.1 for the setup
flags, with multishot accept and buffer rings from 5.19 and multishot
receive from 6.0. GitHub's hosted runners run 6.8 or later on both
x86-64 and arm64. qemu-user implements no io_uring (`ring_new` under
qemu-aarch64 answers ENOSYS), so the arm64 legs under qemu exercise the
fallback; the native arm64 runner exercises the ring.

## Slices

1. **The ring as the readiness set (landed).** Three builtins mirroring the
   reactor floor: `ring_new(entries): i64` (the handle, or -errno),
   `ring_ctl(ring, op, fd, arg): i32` and `ring_wait(ring, events,
   timeout_ms): i32`, with the reactor's ops and contract: op 1 watches
   `fd` for `arg`'s interest bits (1 readable, 2 writable, 4 exclusive,
   which the ring keeps in an epoll set of its own; see below),
   op 2 unwatches it, op 3 closes the ring, ops 4 to 6 the signal and
   parent watches, and a wait writes (fd, readiness) pairs into the
   caller's array and answers how many. Runtime helpers in
   `compiler/asmcore.fern` for x86-64 and arm64 Linux over `__syscall6`
   and `mmap`; the ENOSYS bodies for Darwin and wasm; the interpreter
   answers ENOSYS. A watch queues a single-shot `POLL_ADD` keyed by the
   descriptor; an interest change queues a `POLL_REMOVE` and a fresh
   `POLL_ADD`; a wait submits everything queued and the re-arms of the
   completions it read in one `io_uring_enter` with a timeout
   (`IORING_ENTER_EXT_ARG`). `RealDriver` gains the ring beside the
   reactor, `async.real_driver_io_uring()` makes one that asks for it and
   `.io_uring()` says whether it got it; the serve loop asks under
   `Config.io_uring`. Each new builtin is four classifications
   (`docs/FREESTANDING-CORE.md`'s target half and `internal/pkg/caps`'
   package half, each mirrored in the compiler). Gates:
   `TestRingDriverX86_64` drives one probe through the ring (the listener
   on a connect, a socket reported while its bytes stay unread and not
   after, writable on request, nothing after an unwatch and readable again
   when watched anew, forty connections at once), and its interpreter and
   qemu-aarch64 twins read the same through the fallback; the
   zero-allocation gate and the accepted and kept held-connection shapes
   run again under the ring (`TestSelfHostServeAllocsPerRequestRing`, the
   `-io_uring` shapes); and the whole x86-64 serve suite was run with
   `FERN_IO_URING=1` before landing, as the nightly lane runs it.
2. **Multishot accept (dropped).** A multishot `IORING_OP_ACCEPT` on the
   listener is a pending request that holds the listener's file, as a
   pending poll does (above), and a ring torn down after its worker dies
   may still complete it. A worker's `reuse_port` listener would then stay
   in the port's group past the crash, the window
   `TestSelfHostSupervisedServeReusePortWorkers` measured resetting
   connections, and a shared listener could hand a connection to a ring
   with no process left to serve it. What it saves is one `accept4` per
   connection. The listener stays in the ring's epoll set and is accepted
   with `tcp_accept`.
3. **Provided buffers and multishot receive (landed).** One buffer ring per
   ring, 256 buffers of 4 KiB (the loop's scratch), registered with
   `IORING_REGISTER_PBUF_RING`. A watch with interest 8 beside 1 reads by a
   multishot `IORING_OP_RECV` selecting from it in place of a read poll;
   the serve loop sets it on the connections it reads itself and leaves it
   off where something else reads the socket: a body's pull, which reads
   once the loop has unwatched the connection, the kernel's TLS record
   layer (`Config.ktls`), and the HTTP/2 connections fetch drives. A
   completion queues its buffer on the descriptor, the wait reports it
   readable while the queue holds bytes or the stream has ended, and
   `drv.recv_into(fd, scratch)`, the `ring_recv` builtin, copies from the
   queue, as many buffers as the scratch holds, and then reads the socket,
   so the bytes come in order; epoll's driver reads the socket alone. The
   loop reads until a read comes back short, so the queue is empty when it
   lends or closes a connection, and an unwatch drops what is left. A
   connection with 16 buffers queued, or one the pool ran dry for
   (ENOBUFS), reads by poll until its queue has emptied and 64 buffers are
   free, so a connection that stops reading cannot hold the others' reads
   back. Every completion a wait reaps goes through one ready list, which
   the wait reports from as the events array allows; the completion ring
   is sized at 16 entries per submission entry (`IORING_SETUP_CQSIZE`), so
   the kernel never holds completions back. Gates: `TestRingRecvX86_64`
   and its interpreter and qemu-aarch64 twins (a line read whole, unread
   bytes reported again and read in pieces, bytes received before the
   watch dropped bit 8 read ahead of later ones, the end of the stream, and
   forty connections sending 32 KiB each before any is read, more than the
   pool holds, every byte in order); the zero-allocation gate under both
   backends; `TestSelfHostServePipelinedBurst` and its `Ring` twin, 64,000
   pipelined requests over four connections, some 2.3 MB, answered in
   order; and the x86-64 serve suite under `FERN_IO_URING=1`.
4. **Zero-copy send above the crossover (landed).** A bytes body of at
   least `Config.io_uring_zc_bytes` (3,072 by default, about the kernel's
   measured crossover) on a plain HTTP/1.1 connection leaves the loop's
   builder: the head goes out as before and the body becomes a tail
   (`ZcTail`) written by `IORING_OP_SEND_ZC`, through `ring_send_zc(ring,
   fd, data, from)` and `drv.send_zc`. The SQE takes the body's address and
   no unit of it. The loop holds the body (`__Conns.zc_held`) until the
   kernel's notification, so it stays alive and unchanged however the
   response ends, and frees the slot then (`ring_ctl` ops 8 to 10: the
   write's result, whether it was notified, release). The connection is
   watched for nothing but the write's completion, which the wait reports
   as writable; a short write sends the rest from where it stopped. A
   driver without the send (epoll, a kernel that refuses it) answers
   -EOPNOTSUPP and the loop copies from then on. Loopback copies, so on
   every host here the kernel sends a copy behind the same completions,
   and what this slice saves is measured only between two machines with a
   real NIC; the gates are correctness. Gates: `TestRingZcX86_64` and its
   interpreter and qemu-aarch64 twins (a write from the start and one from
   an offset, each reported, read back whole, notified and released, and
   -EOPNOTSUPP on the fallback); `TestSelfHostServeZeroCopy` and its
   `Ring` twin (bodies from 1,000 bytes to 8 MiB, kept, closing, HTTP/1.0,
   HEAD and sixteen clients at once, every body whole); the
   zero-allocation gate; and the x86-64 serve suite under both backends.
5. **Measured, and kept only if it wins.** The HttpArena profiles #9861
   names (baseline at 4,096 connections, pipelined at 4,096, and the
   async profile at 32,000) on the fixed host, epoll against the ring
   after each of slices 1, 3 and 4: throughput, p99 and bump bytes per
   connection, recorded in `docs/benchmarks/`. The `net-nightly` lane
   runs the serve suites and `scripts/net-bench` under both backends;
   the per-PR lanes run epoll only, plus the slice gates above. If the
   ring does not win, it is deleted with this plan's record of why.
   Recorded so far: `scripts/net-bench` loads the same hello binary
   under both backends (`net/fern-ring/*` beside `net/fern/*`), so the
   nightly trend holds the comparison. `docs/benchmarks/net-ring-2026-10-14.md`
   is the first comparison, from the development container. The
   backends serve the same at 64 connections. At 4,096 the ring serves 5
   to 7% fewer requests at a 13% lower 99th percentile, and slice 3
   moves it 1 to 2%. In the pipelined profile (`pipeload`, 16 requests
   per batch, also recorded nightly as `net/<server>/p<N>.rps`) the three
   are within 5% of each other, the runs' own spread. In the async
   profile (`twofetch`, two upstream fetches per request) the ring serves
   8% more at 4,096 connections, about the runs' spread. On this box the
   ring has not won by a margin the measurements can tell from noise.
   Whether to keep it is to be decided from the nightly trend on the
   runner.

## What is not here

- `SQPOLL`. A kernel thread polling the submission ring spends a core to
  save the submit call; on a share-nothing worker per core it competes
  with the workers, and libuv turned it off by default for that reason.
- Zero-copy receive (`IORING_OP_RECV_ZC`, 6.15). It needs NIC header and
  data split and a flow steered to a dedicated queue, which no target
  host here has.
- Files. `std/fs` stays on blocking calls; the loop's only file I/O is
  `sendfile`, which slice 4 leaves alone.
- A completion-shaped Driver. Slice 3 adds `recv_into` to the Driver
  rather than recasting it around completions:
  the loop's structure, its parser's lent buffer and its zero-allocation
  path stay as they are, and epoll implements it by the call the
  loop makes today.
