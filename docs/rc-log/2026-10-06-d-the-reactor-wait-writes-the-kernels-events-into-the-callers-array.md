# The reactor wait writes the kernel's events into the caller's array

2026-10-06: `asmcore.epoll_wait_src`, `asmcore.kqueue_wait_src`. Slice 6 of
`docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2 (#9853), the reactor's row.

## The shape

```fern
let buf: usize = __raw_alloc(cap * 12);
let n: i32 = __syscall6(epoll_pwait, r_fd, buf, cap, timeout_ms, 0, 8);
// ... each event read from buf and its pair stored into events ...
let buf_own: string = __raw_string(buf, cap * 12);
```

`__fern_reactor_wait` built a block for the kernel's `epoll_event`s (or
`kevent`s and a timespec on Darwin) on every wait and dropped it on the way
out: 792 bytes for the serve loop's room of 64, one allocation and one free
per wait, so per request on a keep-alive connection. `async.wait_into` was
documented as the wait that allocates nothing, and the array the caller
keeps is exactly what the runtime then copied out of a fresh block.

## The rule

The kernel writes into the caller's array, behind its length word, and
each event is turned into its `(fd, readiness)` pair where it lies. A pair
is two words. An `epoll_event` is 12 bytes on x86-64 and 16 on arm64, so
event `i` sits at or below pair `i`: the events are walked from the last
down, each read before the pair that overlaps it is written, and the pairs
written so far never reach an event not yet read. A `kevent` is 32 bytes,
so the array holds half as many as it has pairs of room, and those are
walked upwards: pair `i` ends before `kevent i + 1` starts. The timespec
goes in the scratch buffer, and so does the one `kevent` of an array with
room for a single pair, which holds none itself. epoll and kqueue are both
level-triggered here (no `EPOLLET`, no `EV_CLEAR`), so a wait on Darwin
that reports half the room's worth leaves the rest for the next wait.

The wasm wait is its own body and still builds the pollable list, the slot
table and the poll result per wait (`wasm_ir.reactor_wait_func`, three
`__fern_alloc`s freed on the way out). The gate runs on x86-64; it is the
same shape to take.

## Measured

x86-64, `TestSelfHostServeAllocsPerRequest`, a hello handler on one
keep-alive connection:

| Step | Per request |
| --- | ---: |
| Before | 10 |
| The kernel's events in the caller's array | 9 |

`TestSelfHostReactorFloor` and `TestSelfHostReactorSignal` cover the pairs
on x86-64, arm64 and wasm, the signal path through the drain included, and
`TestSelfHostArm64DarwinReactorCompiles` the kqueue body.

## What is left on the path

The read's copy, `__Wire`, the parse's three, `__serve_produce`'s and
`__serve_ready`'s tuples (reaching an indirect call, so counted as able to
park), and the handler's response and header map: the §6.1 rows less the
one this slice took.
