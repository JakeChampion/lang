# Async on the self-hosted compiler's IR path (plan)

> Status: **plan, not started.** Async is complete and shipped on the
> **native (x86-64/arm64) and wasm** backends *via the Go compiler*
> (`std/async`: `Future[T]` + `gather`/`race`/`with_deadline` +
> `fetch_future`, real overlapping I/O — see `docs/ASYNC-REDESIGN.md` /
> `ASYNC-FUTURE-UNIFICATION.md`). This doc is about the remaining gap:
> the **self-hosted** compiler (`examples/self_host/`) can't yet compile
> `std/async` through its IR path (`irlower.fern` → `asm_ir.fern` /
> `asm_arm64_ir.fern` / `wasm.fern`) — it falls back to, and in fact
> can't even AST-compile it. This is **goal-1 work** (widen the
> self-host IR subset until the AST fallback retires).

## How the self-host routes IR vs AST

Per module: `asm.fern` (~line 8128) takes the IR path only when
`asmcore.new_state().use_ir && asm_ir.all_eligible(module)` — i.e. the
**whole** module must be IR-eligible — else it falls back to the legacy
AST emitter. `all_eligible` → `eligible_core_known_main_view`
(`asm_ir.fern` ~3280) → `func_eligible` per function (~3339):
`irlower.lower_func` must return `ok`, and every `call_direct` op must
target an `is_fern_helper` (`__fern_*` IR runtime helper), a
module-local function, or a program-wide known symbol — otherwise the
module is ineligible.

## What blocks `std/async`

### Blocker 1 — the I/O builtins aren't implemented in the self-host at all

`std/async` calls `poll`, `wasm_timer_pollable`, `wasm_pollable_drop`;
`std/fetch`'s `fetch_future` adds `tcp_connect` / `tcp_send` /
`tcp_recv` / `tcp_close` / `tcp_pollable`. The self-hosted compiler
implements **none** of these:

- `asm.fern` (AST emitter) handles `print` / `exit` / `args` /
  `read_file` / `env` by name, then falls through to a generic
  `call __fn_<name>` for everything else. `poll` has no handler → it
  emits a call to a nonexistent `__fn_poll` → **link failure**.
- `asm_ir.fern` / `irlower.fern` have no op for them either.

So this is **not** "add a name to a whitelist" — it's *implementing the
networking/timer/pollable runtime in the self-hosted compiler*, the way
`monotonic_ns` already is: `irlower` intercepts the builtin name and
emits a dedicated IR op (`monotonic_ns()` → `ir.op_monotonic_ns()`,
`irlower.fern` ~5035), and each IR backend lowers that op to the syscall
/ runtime body. A dedicated op also clears Blocker-1's eligibility check
for free (it's an op, not a `call_direct`).

**The source to port** (native, already correct): `internal/codegen/x86_64/x86_64.go`
`emitPollRuntime`, `emitTcpConnectRuntime` / `…Recv` / `…Send` / `…Close`,
`emitTcpPollableRuntime`, `emitWasmTimerPollableRuntime`,
`emitWasmPollableDropRuntime`; the arm64 mirrors in
`internal/codegen/arm64/arm64.go`; the wasm reactor helpers +
`wasi:io/poll` composition in `internal/codegen/wasmbin` +
`internal/wasm/component`. Each must be re-expressed in the self-host
emitters' `s.write("…asm…")` style (the target-independent frontend is
shared via `asmcore.fern`, but the `emit_*` instruction selection is
hand-maintained per backend).

Per-builtin work unit (×8 builtins × 3 backends):
1. `ir.fern`: add `op_<builtin>` (kind + constructor).
2. `irlower.fern`: intercept the builtin name in the call-lowering
   dispatch (~4940+), lower args, emit `op_<builtin>`; register its
   result width (i32, or i64 where relevant) in the width/return tables
   (cf. the `monotonic_ns` i64 entries at ~3454 / ~3630).
3. `asm_ir.fern` (x86-64), `asm_arm64_ir.fern` (arm64), `wasm.fern`
   (wasm): lower `op_<builtin>` to the syscall / runtime call, and emit
   the `__fern_*` runtime body (port from the native backends). wasm
   additionally needs the `wasi:io/poll` / `wasi:sockets` component
   wiring its composer already does on the Go side.

### Blocker 2 — generic enum with a function-typed payload on the IR path — RESOLVED on x86-64; one wasm shape remains (#4364)

> **RESOLVED on x86-64 IR; one wasm shape remains (2026-07-07).** A user
> enum with a function-typed payload now constructs, `match`-binds, and
> indirect-calls on the IR path — the closure-conv work (#4354 + the
> CLOSURE-CONV slices) lowers a function value in payload position as an
> ordinary pointer-sized payload, and the user-enum `match` lowering
> dispatches the bound payload through the existing closure-call
> machinery.
>
> **x86-64 IR:** verified for the exact `Future[T] = Ready(T) |
> Pending(i32, (i32) => Future[T])` shape (generic + recursive +
> payload-fn-returns-the-enum) plus named-fn / capturing-closure /
> recursive-`Step` / generic-`Box[T]` variants — all route `module: IR`
> and match the interp oracle. The x86 IR path does not monomorphize the
> generic enum, so the generic/recursive shapes ride the same lowering as
> the non-generic ones.
>
> **wasm IR:** the four non-fully-generic-recursive shapes lower; the
> exact `Future[T]` shape (generic + recursive + payload-fn-returns-the-
> generic-enum) is a **remaining wasm-only gap**. The wasm driver
> monomorphizes the generic enum (`Fut[T]` → `Fut__i32`, #3893), renaming
> the variant structs, but the nested `match (cont(tok))` on the
> closure-call scrutinee can't have its arm patterns rewritten to the
> mangled names because the fn payload's return type is discarded at parse
> (`StructFieldDecl` has no `fn_ret` slot; the variant field is the coarse
> `type_name: "fn"`). `lower_func` then bails and the wasm module's `main`
> is truncated. Closing it needs fn-payload-return-type preservation at
> the variant-desugar layer — an invasive parse-layer change, tracked as
> #4722. See `docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md` for the
> full root cause.
>
> Pinned by `internal/e2eselfhost/self_host_fn_payload_variant_ir_test.go`
> (x86-64 all five shapes; wasm the four that lower, `Future[T]` skipped
> with a pointer to the gap). Only the async *runtime* builtins (Blocker 1
> / the #4315–#4320 poll / timer / socket set) plus this wasm shape remain
> for the end-to-end `std/async`-on-IR payoff. The historical analysis
> below is retained.

`Future[T] = Ready(T) | Pending(i32, (i32) => Future[T])` is a generic,
recursive, **user** enum whose `Pending` payload is a function type. On
the IR path, function-typed `match` payloads are recognized only for the
built-in `Option`/`Result` patterns (`irlower.is_builtin_pattern`,
~1804; the function-payload match handling ~10472/10756+). A user enum
with a function-typed field falls through to the user-enum path, which
recovers the payload type but has no path to mark it a closure local /
dispatch the indirect call — so `lower_func` bails → ineligible.

**Already supported** (do not redo): escaping capturing lambdas
(`irlower` "slice 3a", ~172-196 / ~6337 — the `<fn>$clo` + env-tuple
hoist; the `asm_ir.fern` ~3365 "lambdas bail" comment is **stale**),
tuple returns (so `race`'s `(i32, T)` is fine), and `monotonic_ns`.
Generic monomorphization happens in the **native** compiler before the
self-host sees the program, so plain generics don't themselves block.

The fix: extend the user-enum `match` lowering so a function-typed
payload field is marked a closure local and dispatched via the existing
`call_indirect` / closure-call machinery (the same mechanism slice-3a
lambdas already use), not just for `Option`/`Result`.

## Sliced plan (ordered; each slice independently testable)

Because the self-host build/test cycle is slow and **no `std/async`
program goes IR until both blockers are fully done**, each slice ships
with an *intermediate* self-host test that exercises the new capability
in isolation (a non-async program), so progress is verifiable before the
end-to-end payoff.

1. **`poll` on self-host x86-64 IR.** `op_poll` + `irlower` intercept +
   `asm_ir.fern` lowering + `__fern_poll` body. Test: a self-host-compiled
   program polling two `timer_fd` / file fds routes IR (`all_eligible`
   true) and returns the ready index — the self-host twin of the native
   `poll` tests.
2. **`poll` on arm64 + wasm** (the other two backends). Same op, mirror
   the lowering; wasm routes `op_poll` → `wasm_poll`/`wasi:io/poll`.
3. **`timer_fd` / `wasm_timer_pollable` / `wasm_pollable_drop`** across
   the three backends (timers + pollable lifecycle). Test: self-host
   `with_deadline`-shape program over timers.
4. **`tcp_*` family** (`connect`/`send`/`recv`/`close`/`pollable`) across
   the three backends. Test: a self-host-compiled outbound fetch.
5. **Generic enum with function-typed payload** (Blocker 2). Test: a
   minimal self-host program with a hand-written `Future`-shaped enum +
   indirect call, routed IR.
6. **End-to-end:** a self-host-compiled `std/async` program (`gather` /
   `race` / `with_deadline` / `fetch_future`) routes IR and runs —
   enroll it on the self-host differential gate; flip the expectation
   from AST-fallback to IR.

## Risks / notes

- **Backend parity:** the self-host x86-64 and arm64 emitters share
  their frontend (`asmcore.fern`) but not instruction selection, so each
  op needs hand-written lowering on both; CI's arm64/qemu lanes are the
  backstop. wasm is a third, structurally different emitter
  (component-model composition), the most involved per builtin.
- **No incremental async payoff** until ~slice 5-6; slices 1-4 are pure
  infrastructure justified by their intermediate tests + the goal-1
  IR-widening objective, not by async features.
- **Build cost:** self-host e2e tests rebuild the self-hosted compiler
  (~minutes each); budget for slow iteration. Clear
  `/tmp/selfhost-bincache-*` between regressions.
- **Scope discipline:** this is the I/O-runtime slice of goal-1. It
  belongs to the standing "widen the IR subset" objective; do it only
  when goal-1 work is in scope.
