# The raw-memory intrinsic floor (issue #2649, Tier 2)

This is the **implementation design** for the primitive floor sketched in
`RUNTIME-IN-FERN.md` (§"The hard part: circularity and the primitive floor").
The Tier-0/1 helpers — `i32_pow`, `i32_gcd`/`lcm`, the `arr_i32_*` reducers,
`str_to_i32`, `str_cmp`, the `str_search` predicates, `str_eq`,
`arr_str_index_of`, `arr_str_join`, `str_trim`, `str_lines`, `str_bytes`,
`str_chars` — are now Fern runtime functions.

> **Status update (2026-07): the intrinsics below shipped and the Tier-2
> migration is complete.** `chr`, `str_concat`, `i32_to_string`,
> `str_to_upper`/`_lower`, `str_repeat`, `str_reverse`, `str_replace`,
> `string_from_bytes` and `str_split` all lower as Fern functions via these
> raw-memory intrinsics.
>
> **Status update (2026-07, syscall sub-floor): in progress.** The `__syscall3`
> intrinsic (below) landed; `random_bytes` and the three clocks (`monotonic_ns`
> / `now_unix_ms` / `now_ns`) are the syscall leaves moved to Fern so far — on
> the **x86-64 IR path** (`asmcore.rt_src_random_bytes` / `_monotonic_ns` / …;
> the arm64 / AST backends keep their hand-asm because the syscall numbers are
> arch-specific, and wasm has no generic syscall). The clocks slice added one
> more floor primitive — `__raw_scratch` (a fixed static buffer for the kernel
> to write `timespec`/`stat` into — no per-call heap leak) — and reuses the
> existing `__load_i64` for the i64 `tv_sec`/`tv_nsec` read-back. What remains
> hand-written (the residue tracked by
> [#2649](https://github.com/JakeChampion/lang/issues/2649)) is `read_file` + the
> rest of the fs family, the syscall leaves on arm64/AST/wasm, `__fern_alloc`
> itself, and the map/array mutator core.
>
> **Status update (2026-07, user-typed returns): `stat` migrated.** `stat` is the
> first leaf returning a **user-typed** value — `Result[FileStat, IoError]` — and
> it lowers as a Fern function (`asmcore.rt_src_stat`): `stat(2)` into
> `__raw_scratch`, read `st_mode`/`st_size` with `__load_i32`/`__load_i64`, then
> build `Ok(FileStat{...})` / `Err(NotFound(_))`. This proves a runtime helper
> compiled via `emit_ir_runtime_fern_fn` can construct user structs/enums (it
> receives `all_structs`), which is the capability `read_file` and the rest of
> the fs family need. **`read_file` followed** (`asmcore.rt_src_read_file` →
> `Result[string, IoError]`): openat/lseek/read/close via `__syscall3`, the sized
> read buffer becomes the `Ok(string)`. It reads the whole file in ONE `read`
> (into an lseek-sized buffer, passed whole) rather than a read-loop, because the
> i32 raw-pointer floor can't do 64-bit-safe `buf + offset` arithmetic on a high
> heap buffer — correct for regular files ≤ 2 GiB read without interruption. And
> **`remove_file`** (`rt_src_remove_file` → `Option[IoError]`): `unlinkat` via
> `__syscall3`, `None` on success / `Some(NotFound(_))` on failure.

Tier-2 helpers were stuck for one reason: **Fern had no way to write a byte
to a computed heap address.** `s[i]` *reads* a byte; there was no `s[i] = b`,
and no way to allocate N raw bytes and fill them. That write capability is
the floor. This doc defines the minimal intrinsic set that supplies it, how
each backend lowers it, and the migration order.

## Design goals

1. **Minimal surface.** Add the fewest intrinsics that let every Tier-2 helper
   be written in Fern. Everything above the floor stays ordinary Fern.
2. **No new IR concepts where avoidable.** Each intrinsic lowers to a *single*
   existing instruction (a load, a store, a `call __fern_alloc`, or a syscall)
   — the same shape the hand-written helpers already emit inline.
3. **Unsafe, unchecked, internal.** These are `__`-prefixed, never part of the
   user surface. They carry no bounds checks (the helpers that use them are
   audited), matching the language's no-runtime-check stance.
4. **Same four-backend parity** as the helper migrations: x86-64 AST + IR,
   arm64 AST + IR, recognized once in `try_emit_builtin` (AST) and the IR op
   emitter, so a helper written against them lowers everywhere.

## The intrinsic set

All pointers are machine words carried as `i32` in the source-level signature
(the self-host surface has no `i64`/pointer type; the backends already treat a
heap "pointer" as an 8-byte slot on arm64 / a 4-byte one on wasm via the
`WidthPtr` sentinel — see `CLAUDE.md`). The intrinsic lowering uses the native
pointer width, not the i32 the signature advertises; callers only ever pass
values that came out of `__raw_alloc` / a box field, so the narrowing is
nominal.

| intrinsic | lowers to | notes |
|---|---|---|
| `__raw_alloc(n: i32): i32` | `mov n→%rdi; call __fern_alloc` → ptr in `%rax` | the bump/freelist primitive; the one call that stays asm |
| `__raw_store8(ptr: i32, off: i32, v: i32)` | `movb %v, (%ptr,%off)` | write low byte of `v` |
| `__raw_load8(ptr: i32, off: i32): i32` | `movzbl (%ptr,%off), %eax` | zero-extended byte read (also expressible as `s[i]`, included for symmetry) |
| `__raw_store_ptr(ptr: i32, off: i32, v: i32)` | `mov %v, (%ptr,%off*W)` | store a word-sized slot (W = pointer width); for writing box `{data,len}` fields and array slots |
| `__raw_load_ptr(ptr: i32, off: i32): i32` | `mov (%ptr,%off*W), %rax` | word-sized slot read |
| `__raw_string(data: i32, len: i32): string` | alloc a 16-byte box `{data, len}` | the *one* intrinsic that produces a typed `string`; the bridge from raw bytes back to the surface |
| `__syscall3(nr: i32, a1: i32, a2: i32, a3: i32): i32` | `mov nr→%rax; a1→%rdi; a2→%rsi; a3→%rdx; syscall` → result in `%rax` | the I/O sub-floor for the syscall leaves; a single `syscall`/`svc`, no runtime symbol. Native-syscall backends only (x86-64 / arm64 Linux); wasm has no generic syscall |
| `__raw_scratch(n: i32): i32` | `leaq __fern_scratch(%rip), %rax` | a fixed static (.bss) scratch buffer the syscall leaves hand the kernel to write into (`timespec`, `stat`) — reused, never freed, so no per-call leak. `n` is a size hint; the buffer is fixed. **Non-reentrant** (one leaf reads it fully before another runs) |

Reading the kernel-written 8-byte fields (`tv_sec` / `tv_nsec`) back into i64
math reuses the **existing** `__load_i64(addr): i64` intrinsic (#4375) — no new
op was added for that.

`__raw_string` is the key: it lets a helper allocate a byte buffer with
`__raw_alloc`, fill it with `__raw_store8`, and hand back a real `string` the
checker accepts — without the helper ever naming the box layout. Array
construction reuses the existing `__fern_arr_box` (already a callable runtime
symbol) wrapped the same way, or stays on the `.append()` path that `str_bytes`
/ `str_chars` already use.

## Worked example — `chr`

The hand-asm (see `asm.fern`) allocates 1 data byte, stores the low byte of the
argument, then allocs a 16-byte box `{data, 1}`. In Fern:

```
function __fern_chr(b: i32): string {
    var p: i32 = __raw_alloc(1);
    __raw_store8(p, 0, b);
    return __raw_string(p, 1);
}
```

That is the entire Tier-2 vocabulary exercised in one helper: allocate, store a
byte, box. It is the **first slice** — smallest possible proof that the floor
works end-to-end on all four backends, with a lock-in test
(`__fn___fern_chr` emitted, `__fern_chr:` hand-asm gone) plus the existing
behavioural `chr` cases.

## Worked example — `str_concat` (the payoff)

```
function __fern_str_concat(a: string, b: string): string {
    var la: i32 = a.len();
    var lb: i32 = b.len();
    var p: i32 = __raw_alloc(la + lb);
    var i: i32 = 0;
    while (i < la) { __raw_store8(p, i, a[i]); i = i + 1; }
    var j: i32 = 0;
    while (j < lb) { __raw_store8(p, la + j, b[j]); j = j + 1; }
    return __raw_string(p, la + lb);
}
```

`str_to_upper`/`_lower` (case-flip per byte), `str_repeat` (n copies),
`str_reverse` (reversed copy), `str_replace` (scan + copy with substitution),
and `i32_to_string` (digit buffer) are all the same shape: size, alloc, fill,
box. None needs anything beyond the table above.

## Lowering, per backend

Recognition mirrors the existing bare-name runtime calls (e.g. `str_to_i32`):

- **AST** (`asm.fern` / `asm_arm64.fern`): a new arm in `try_emit_builtin`
  matching the `__raw_*` names, emitting the single instruction inline (no
  `call`, except `__raw_alloc`/`__raw_string` which `call __fern_alloc`). Args
  arrive on the operand stack as usual.
- **IR** (`asm_ir.fern` / `asm_arm64_ir.fern`): a new `ir.Op` per intrinsic
  (`raw_store8`, `raw_load8`, `raw_store_ptr`, `raw_load_ptr`, `raw_alloc`,
  `raw_string`), produced by `irlower` for the matching `ExprCall`, emitted by
  the op handler as the same single instruction. `raw_store_ptr`/`raw_load_ptr`
  use `WidthPtr` so wasm32 (4-byte) and arm64/x86-64 (8-byte) slots both work.

Because each lowers to one instruction, there is **no new runtime symbol** for
the load/store intrinsics — they vanish into the helper's body. Only
`__raw_alloc`/`__raw_string` touch `__fern_alloc`, which is always present when
`heap` is needed (every Tier-2 helper allocates, so `mark_*` pulls `heap`).

## What this retires

Once the Tier-2 helpers are Fern, the hand-written runtime shrinks to exactly:
`__fern_alloc` + the freestanding scaffolding (`_start`, the `.bss` heap, the
syscall leaves). At that point the `mark_*` / `runtime_need_deps` /
`close_needs` / `all_runtime_need_roots` / `emit_runtime_globls` apparatus has
only the floor left to track — and the project can take the final #2649 step:
lift the `rt_src_*` strings into real importable `core/runtime` modules
(see `RUNTIME-IN-FERN.md` §"How the runtime module reaches every program"),
deleting the manual bookkeeping in favour of the real call graph + deadcode.

## Migration order

1. **Intrinsics + `chr`** (this slice) — land the floor with the smallest
   possible consumer as the end-to-end proof.
2. **`str_concat`** — backs `+` on strings; high-traffic, exercises the
   two-source copy loop.
3. **`i32_to_string`** — the digit-buffer build; backs `(n).to_string()`.
4. **`str_to_upper` / `_lower`**, **`str_repeat`**, **`str_reverse`**,
   **`str_replace`** — the remaining per-byte string builders, one slice each
   (or grouped by similarity), following the established four-backend +
   AST/IR-lock-in-test pattern.
5. **The syscall leaves** (`read_file`, clocks, `random_bytes`) via the
   `__syscall3` intrinsic — a separate sub-floor. **In progress:** `__syscall3`
   landed; `random_bytes` (`rt_src_random_bytes`) and the three clocks
   (`rt_src_monotonic_ns` / `_now_unix_ms` / `_now_ns`, which also needed
   `__raw_scratch` + reused `__load_i64`) moved to Fern on the x86-64 IR path. The
   syscall *number* is arch-specific (x86-64 getrandom = 318 / clock_gettime =
   228, arm64 = 278 / 113) and the `rt_src_*` sources are shared between the
   register backends, so arm64/AST parity needs an arch-parameterised source (or
   a `__sysno_*` constant); wasm keeps its WASI bundles. `read_file` + the fs
   family (multi-syscall + `stat` scratch + Result) follow.

Each slice ships with AST + IR lock-in tests (the helper compiles from Fern,
the hand-asm label is gone) and reuses the existing behavioural coverage, and
must keep the self-compile fixpoint byte-identical.
