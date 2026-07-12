# Crash-only native serving — design

Plan item **D2** of `docs/NICHE-BORROWS-PLAN.md` (Erlang's
crash-only philosophy from `NICHE-LANGUAGE-RESEARCH.md`): isolate
handler failures at the request/worker boundary so *the service*
survives what *one invocation* does. This doc fixes the design;
implementation is a separate slice (D2').

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
- **native `tcp_serve`** (`std/tcp.fern`) — the accept loop and
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

## Chosen shape (D2' scope)

- New native-only runtime builtins, capability-gated (E066)
  under a new `proc` capability granted by the four native
  targets only: `proc_fork(): i32` (0 in child, pid in parent,
  negative errno on failure) and `proc_waitpid(pid: i32): i32`
  (child's exit code, or negative errno). Both are thin syscall
  wrappers (fork/wait4) on arm64 + x86-64 Linux and arm64-darwin;
  NOT wired on wasm targets (no processes in wasi) — the gate
  makes that a check-time error, per the D1 machinery.
- `std/tcp` grows `tcp_serve_supervised(port, handler): i32` —
  parent creates the listener (so the backlog survives worker
  deaths), then the fork/waitpid/backoff loop above; the child
  runs the existing accept loop against the inherited listener
  fd. `tcp_serve` itself stays exactly as-is (single-process,
  dev-friendly, debuggable).
- The synthesised handler `main` keeps calling plain `tcp_serve`
  — supervision is opt-in by writing your own `main`. Flipping
  the synthesised default is a separate decision once D2' has
  soaked.

## Test bar (D2' exit criteria)

An e2e test (native x86-64, mirroring the tcp e2e harness) that:
1. serves with `tcp_serve_supervised` and a handler that traps
   (array out-of-bounds) when the path is `/boom`;
2. sends `/boom` — observes connection reset / no response and
   the worker-death log line on stderr;
3. sends `/ok` — observes 200, proving the service survived;
4. asserts the process is still alive after N crash/restart
   rounds and that a crash-looping handler (traps on every
   request incl. the first) exits with the bounded-backoff
   give-up rather than spinning forever.

Interp parity: `proc_fork`/`proc_waitpid` get interp builtins
(interp already has host-process access — `subprocess` precedent)
so the supervised path is testable under `-interp` too.

## Deliberately deferred

- fork-per-request / prefork worker pools (SO_REUSEPORT) — needs
  a real workload to justify.
- Graceful drain (SIGTERM → stop accepting, finish in-flight) —
  orthogonal; belongs to a signals design.
- Windows — no native Windows target exists.
