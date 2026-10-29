# Crash-only native serving — design

Status: [reference] implemented in `std/serve`: the supervisor and its prefork
worker pool.

Plan item **D2** of `docs/NICHE-BORROWS-PLAN.md` (Erlang's
crash-only philosophy from `NICHE-LANGUAGE-RESEARCH.md`): isolate
handler failures at the request/worker boundary so *the service*
survives what *one invocation* does. The supervisor and its prefork worker
pool are implemented in `std/serve`.

## Blast radius today

A Fern runtime trap aborts the whole process. The trap taxonomy:

| exit | source | meaning |
|---|---|---|
| 134 | bounds checks (`emitArrBoundsCheck` / slice / str) | matches wasm `unreachable` under wasmtime — deliberate parity |
| 137 | `__fern_alloc` arena-exhaustion guard | bump arena wall (NOT a kernel OOM) |
| 101 | `todo` stub reached | unimplemented path |
| 1 | `assert(...)` failure | precondition violation |

Per target:

- **wasi-http (`wasmtime serve`)** — already crash-only: a trap
  kills that request's component instance; the host answers 500
  and keeps serving. Nothing to build; this is the primary edge
  target and it is already correct.
- **native `serve.run`** (`std/serve`) — the accept loop and
  the handler share one process; any trap in handler code (or in
  parsing hostile input) kills the listener with it. One bad
  request = total outage until an operator restarts.

So the gap is native long-running serving only — today a
dev/self-host convenience, which bounds how much machinery this
deserves.

## Options weighed

**(i) In-process trap recovery (signal handler + longjmp back to
the accept loop) — REJECTED, unsound.** A trap can fire mid-way
through an RC mutation (inc/dec pair half-applied, a freelist
link half-written, a box's fields partially initialised). Resuming
the same heap after longjmp means serving subsequent requests on
top of corrupted allocator / refcount state — converting a loud
crash into silent corruption. This option is permanently off the
table; it would also require per-backend signal trampolines
(SIGSEGV-safe stacks) for less-than-nothing in return.

**(ii) fork() per request — rejected as the default.** True
isolation and per-request leak reclamation, but a fork+exit per
request costs more than most handlers, breaks keep-alive
(connection state dies with the worker), and multiplies RSS under
load. Reasonable future OPT-IN for untrusted-input workloads;
not the default shape.

**(iii) Supervisor: accept-loop worker under a supervising
parent — CHOSEN.** The parent owns nothing but supervision: fork
a worker child that runs today's `tcp_serve` loop; `waitpid` on
it; on abnormal exit, log the death (exit code taxonomy above,
straight to stderr) and refork with bounded backoff. The kernel
listener backlog belongs to the LISTENING socket — created in the
parent before the first fork and inherited by every worker — so
pending connections survive a worker death; only the in-flight
connections of the dead worker are lost (coarse but honest
isolation, same trade Erlang's one_for_one restart makes).
Restart backoff: 100 ms doubling to a 5 s cap (TigerStyle:
bounded everything); a worker that dies within 100 ms of starting
N consecutive times (N=8) is a crash loop — give up and exit with
the child's code (an operator problem, not a retry problem).
Bonus: a refork RESETS the worker's heap, so the cycle-leak class
RC cannot collect (`CYCLE-COLLECTION-ANALYSIS.md`) is reclaimed
on every crash — crash-only in the original Recovery-Oriented
Computing sense.

**(iv) Do nothing + document — the honest baseline.** wasi-http
already isolates; native serving is not yet a production target.
This doc exists so that when that changes, the shape is already
decided. D2' should land when native serving grows a real
consumer, not before.

## Chosen shape (D2' scope) — IMPLEMENTED

- New native-only runtime builtins, capability-gated (E066)
  under a new `proc` capability granted by the four native
  targets only: `proc_fork(): i32` (0 in child, pid in parent,
  negative errno on failure) and `proc_waitpid(pid: i32): i32`
  (child's exit code 0..255; 128+signal for a signal death —
  shell convention; negative errno on failure). Both are thin
  syscall wrappers: x86-64 Linux `fork`(57)/`wait4`(61); arm64
  Linux `clone(SIGCHLD,0,0,0,0)`(220 — no bare fork syscall)/
  `wait4`(260); arm64-darwin BSD `fork`(2, XNU's x1 child-flag
  normalised to 0-in-child) / `wait4`(7) with the carry-set
  +errno convention negated to Linux-shaped -errno. NOT wired
  on wasm targets (no processes in wasi) — the gate makes that
  a check-time error, per the D1 machinery.
- **Interp parity is via fallback, not emulated fork.** The
  interpreter cannot bare-fork (Go's runtime is threaded; a raw
  fork(2) in a multithreaded process is UB in the child), so its
  `proc_fork` returns **-38 (ENOSYS)** permanently (and
  `proc_waitpid` -10/ECHILD — no child can exist).
  `serve.supervise` detects the -38, logs one
  "supervision unavailable; serving single-process" line to
  stderr, and runs the plain accept loop — graceful degradation,
  so the function is runnable under `-interp` (and on any future
  fork-less target) with the same observable serving behaviour,
  minus isolation. The interp builtins are deliberately NOT
  capability-gated (Enforce only runs for compiled targets).
- `std/serve` has `supervise(port, cfg, handler): i32` —
  parent creates the listener (so the backlog survives worker
  deaths), then the fork/waitpid/backoff loop above; the child
  runs the existing accept loop (factored as `__serve_loop`,
  which takes the listener fd — `run` calls it too)
  against the inherited listener fd. `run` itself stays
  exactly as-is behaviourally (single-process, dev-friendly,
  debuggable).
- The synthesised handler `main` serves under the supervisor
  (`serve.supervise` and its `_with` / `_shutdown` twins,
  #9854); `serve.run` is for a `main` you write yourself.

## Test bar (D2' exit criteria)

An e2e test (native x86-64, mirroring the tcp e2e harness) that:
1. serves with `serve.supervise` and a handler that traps
   (array out-of-bounds) when the path is `/boom`;
2. sends `/boom` — observes connection reset / no response and
   the worker-death log line on stderr;
3. sends `/ok` — observes 200, proving the service survived;
4. asserts the process is still alive after N crash/restart
   rounds and that a crash-looping handler (traps on every
   request incl. the first) exits with the bounded-backoff
   give-up rather than spinning forever.

Interp parity: `proc_fork`/`proc_waitpid` get interp builtins
returning -38 (ENOSYS) / -10 (ECHILD) — see "Chosen shape" — so
under `-interp` the supervised path degrades to single-process
serving rather than being untestable; the e2e suite covers both
the native supervised path and the interp fallback.

## Worker model and boundaries

- fork-per-request — needs a real workload to justify. Prefork
  workers exist (#9854). `serve.supervise` forks `serve.Config.workers`
  workers, one for each CPU by default. The supervisor reaps a worker that
  dies (`proc_waitpid(-1)`) and forks its replacement under a backoff and
  a count of fast deaths.

- On Linux and Android, with more than one worker, the supervisor binds
  one `SO_REUSEPORT` listener for each worker before the first fork, in
  order. A filter on the group sends a connection to the listener whose
  index is the CPU the connection arrives on. A NIC spreads arrivals over
  the CPUs. On loopback, the CPU is that of the thread that dials, so a
  client on one CPU reaches one listener.

- The supervisor keeps every listener open. It gives a dead worker's
  listener to the replacement, so the connections in its queue wait and
  are not lost.
  A stalled or dead worker delays the new connections the kernel sends to
  its listener. A shared listener gives them to the other workers.

- `TestSelfHostServeAcceptDistributionOwnListeners` dials a burst of
  4,096 connections from four CPUs, and four workers take 1,024 each.
  Under oha at 4,096 connections, the hello server goes from 127k–129k to
  143k–158k requests per second, and at 64 connections from 170k–191k to
  325k–329k
  (`docs/benchmarks/net-hello-2026-10-29.md`). Without the filter the
  split is as even, but the rate stays at 122k–136k.

- With one worker, on other targets, and for a listener from
  `LISTEN_FDS`, the workers share one listener. Each worker watches it
  with epoll's `EPOLLEXCLUSIVE` (the driver's interest bit 4), so a
  connection wakes one worker. The listener does not block, so a worker
  that wakes after another took the connection waits again.
  `serve.Config.reuse_port` asks for a listener per worker on every
  target. On a shared listener, the worker that wakes takes connections
  from a queue that is never empty, while the others sleep.
  `TestSelfHostServeAcceptDistribution` measures a burst from one client
  landing 3,556, 444, 80 and 16.

- With `with_h3`, the supervisor also binds one HTTP/3 UDP socket for
  each worker before the first fork. It keeps every socket open and
  gives a dead worker's socket to the replacement. The kernel steers each
  datagram over the `SO_REUSEPORT` group by the hash of its flow. A socket
  that joins or leaves the group moves live QUIC connections to a worker
  that has no state for them.

- Graceful drain exists (#9854): SIGTERM is a readiness event on the
  reactor, the supervisor forwards it to its workers and waits for them,
  and each worker keeps accepting for a grace, fails its readiness path,
  closes the listener, ends keep-alive and drains what is in flight
  under a deadline, exiting 1 when it cut a request off.
- Windows — no native Windows target exists.

## Coverage

The self-host compiler supports native supervision. Its worker pool,
per-worker `SO_REUSEPORT` listeners, handler-trap recovery and bounded
crash-loop backoff are exercised by `TestSelfHostSupervisedServe*` in
`internal/testing/e2ecompiler/self_host_serve_test.go`. In particular,
`TestSelfHostSupervisedServeReusePortWorkers` verifies that workers bind
their own listeners and keep serving after a handler traps.
