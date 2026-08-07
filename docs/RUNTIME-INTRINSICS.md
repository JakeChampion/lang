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
> `string_from_bytes_unchecked` and `str_split` all lower as Fern functions via these
> raw-memory intrinsics.
>
> **Status update (2026-08, the syscall floor reaches arm64.)** `random_bytes`
> is the first syscall leaf to be Fern on **both** register backends: the
> `__syscall3` op now has an arm64 emitter, and the shared
> `asmcore.rt_src_random_bytes` source takes the target's entropy syscall number
> as a parameter (x86-64 getrandom 318 / arm64-linux getrandom 278 /
> arm64-darwin getentropy BSD 500). That parameterisation is the answer to the
> "the syscall number is arch-specific, so a shared source can't hardcode one"
> problem this doc used to close the syscall-leaf section with.
>
> Two things made it work. First, **darwinize**: it remaps a syscall by matching
> a literal `mov x8, #N`, which a generic `__syscall3` — whose number is an
> operand — never emits, so it instead matches the op's number load
> (`ldr x8, [sp], #16`), rewrites it to `x16`, flips the trap to `svc #0x80` and
> normalises the carry-flag errno; the number itself is already the Darwin one
> because the source was generated for that target. Second, **`__raw_addr`**
> (added to the table below): the helper fills the buffer in <= 256-byte chunks
> — getentropy's hard per-call limit, and on Linux the fix for getrandom
> short-filling an n > 256 — and the chunk address cannot be written `p + off`,
> which arm64 truncates to 32 bits. Migrating also fixed a live arm64 bug: the
> hand-asm built a bare 16-byte `{data,len}` box, so every dec of a
> `random_bytes` string read its refcount out of the preceding allocation;
> `__raw_string` goes through `__fern_str_box`, which writes the rc header.
>
> **The fs leaves followed** — `read_file`, `write_file`, `remove_file`,
> `temp_dir` and `env` are Fern on arm64 too, with `__syscall4` gaining its arm64
> emitter (on arm64 the 4th syscall arg is just `x3`; there is no x86-64-style
> `%r10` shuffle) and the per-target constants factored into three tables in
> `asmcore`: **`sysno(t, name)`**, **`at_fdcwd(t)`** (-100 Linux / -2 XNU) and
> **`oflag(t, name)`** (O_WRONLY|O_CREAT|O_TRUNC is 577 on Linux, 1537 on
> Darwin). `t` is `"x86_64"` / `"arm64"` / `"arm64-darwin"`. Those four leaves
> and the shared `__fern_io_error` classifier emit as one bundle module, so each
> helper's call to the classifier threads as an ordinary call-graph edge — the
> #2649 end-state in miniature, now on both register backends. `env` sits
> outside the bundle: it issues no syscall (the `__raw_environ` op, which the
> arm64 emitter now has, reads the `__fern_envp` slot `_start` stashes) and
> reports a missing variable as `None` rather than an error, so it calls no
> classifier and needs no per-target constant at all. Its `.bss` slot is emitted
> unconditionally — `_start`'s save is gated on `heap`, not on `env`.
>
> `__raw_addr` also let `read_file` grow the read LOOP its hand-asm always had:
> the single-read shape was a workaround for the missing primitive, and it
> silently truncated on a short read. `write_file` gained the matching
> short-write loop.
>
> Landing that surfaced a **darwinize** bug worth knowing about: its pending
> syscall number was reset by every line, so it only rewrote an `svc` that came
> IMMEDIATELY after the number load. The two abort paths (`.Lalloc_oom`,
> `__fern_oob_abort`) load their exit status in between, so `exit(125)` /
> `exit(134)` kept a Linux `svc #0` in Mach-O output and died by trap instead of
> exiting with the status. The pending number is now sticky until an `svc`
> consumes it; an unmappable `mov x8, #N` still clears it, so a syscall with no
> Darwin form can never inherit the previous one's mapping.
>
> **`stat` followed, and it corrects this doc.** The blocker recorded here — and
> in #6352 / #6356 — was that `stat` needs a per-target source BODY because
> Darwin's `struct stat` puts a **u16** `st_mode` at 4 where Linux arm64 has a
> u32 at 16, and `st_size` at 96 rather than 48. That is a real layout
> difference, but it is still only CONSTANTS, for two reasons the original
> reading missed. First, the u16 needs no separate load: the only consumer masks
> with `S_IFMT` (0xF000), so every bit above 15 — Darwin's `st_nlink`, sharing
> the word — is discarded anyway, and a plain 32-bit load at the right offset is
> correct on all three. Second, the 2-arg-vs-4-arg asymmetry (x86-64's `stat` 4,
> which arm64-linux does not have at all) disappears by unifying on the **4-arg
> `fstatat` family** — newfstatat 262 / newfstatat 79 / fstatat64 470, one
> signature. So the offsets became a third table, **`statoff(t, name)`**, and
> `stat` is one body like the rest. arm64 also gained the `__raw_scratch` op and
> its `__fern_scratch` .bss slot, which the helper hands the kernel to write into.
>
> **The array producers left the asm too.** `xs.reverse()` and `xs.concat(ys)`
> (`asmcore.rt_src_arr_reverse` / `_arr_concat`) are Fern now, over the
> `__raw_arr_box` + `__raw_array` pair added to the table below. These are the
> first helpers to move that have nothing to do with syscalls, and they are
> **arch-independent** — one source, no target parameter, replacing the two
> hand-asm copies x86-64 and arm64 each carried. That is the shape the rest of
> the array/map core should follow.
>
> Two properties make one `i32[]`-typed body serve every element type. The copy
> is a raw 8-byte SLOT copy, so a `string[]`'s box pointers ride through
> untouched; and it is deliberately SHALLOW — no element refcount traffic —
> which is what the hand-asm did and what typing the parameters `i32[]` keeps
> the RC insertion agreeing with. `arr_slice` did NOT come along: its
> construction-time bounds check (#5419) traps to `__fern_oob_abort`, and there
> is no Fern expression for "abort" — that needs its own primitive.
>
> Still **x86-64 IR only**: the three clocks (`monotonic_ns` / `now_unix_ms` /
> `now_ns`) and `read_dir` / `remove_dir_all`. These are the genuinely
> shape-diverging ones. Darwin has no
> `clock_gettime` at all (gettimeofday against a different struct, or CNTVCT_EL0,
> not a syscall), and `getdirentries64` takes a 4th out-param that Linux's
> `getdents64` has no equivalent of — an argument the caller must supply and
> thread, which no constant can stand in for. Each needs a per-target source
> BODY, which is why `sysno` deliberately has no entry for them. wasm has no
> generic syscall at all. The clocks
> slice added one
> more floor primitive — `__raw_scratch` (a fixed static buffer for the kernel
> to write `timespec`/`stat` into — no per-call heap leak) — and reuses the
> existing `__load_i64` for the i64 `tv_sec`/`tv_nsec` read-back. What remains
> hand-written (the residue tracked by
> [#2649](https://github.com/JakeChampion/lang/issues/2649)) is the shape-diverging
> leaves on arm64 listed just above, the wasm bundles, `__fern_alloc` itself, and
> the map/array mutator core.
>
> **Status update (2026-07, user-typed returns): `stat` migrated.** `stat` is the
> first leaf returning a **user-typed** value — `Result[FileStat, IoError]` — and
> it lowers as a Fern function (`asmcore.rt_src_stat`): a stat syscall into
> `__raw_scratch` (2-arg `stat` when this shipped; the 4-arg `fstatat` family
> since it went cross-target — see the 2026-08 block above), read
> `st_mode`/`st_size` with `__load_i32`/`__load_i64`, then
> build `Ok(FileStat{...})` or, on failure, the errno-mapped `IoError` variant
> (`ENOENT→NotFound`, `EACCES→PermissionDenied`, `EEXIST→AlreadyExists`,
> `EINTR→Interrupted`, else `Other`) — the same mapping native's `__fern_io_error`
> and the arm64 hand-asm use. This proves a runtime helper compiled via
> `emit_ir_runtime_fern_fn` can construct user structs **and multi-variant enums**
> (it receives `all_structs`), which is the capability `read_file` and the rest of
> the fs family need. **`read_file` followed** (`asmcore.rt_src_read_file` →
> `Result[string, IoError]`): openat/lseek/read/close via `__syscall3`, the sized
> read buffer becomes the `Ok(string)`. (It shipped reading the whole file in ONE
> `read` because the i32 raw-pointer floor could not offset a high heap buffer
> 64-bit-safely; `__raw_addr` closed that, and it is a read LOOP now — see the
> 2026-08 block above.) And
> **`remove_file`** (`rt_src_remove_file` → `Option[IoError]`): `unlinkat` via
> `__syscall3`, `None` on success / the errno-mapped `Some(IoError)` on failure.
> `write_file` (`rt_src_write_file` → `Option[IoError]`, the first `__syscall4`
> user) followed.
>
> **`read_dir`** (`rt_src_read_dir` → `Result[string[], IoError]`) followed too —
> the first fs leaf that returns an *array*. It `openat`s the dir
> (`O_RDONLY|O_DIRECTORY`) and **drains** it with a `getdents64` loop over a FRESH
> 64 KiB buffer per read, parsing each `linux_dirent64` record (`d_reclen@16`,
> NUL-terminated `d_name@19`, skipping `.` / `..`) and appending each base name to
> the result `string[]` with `.append()` (→ `__fern_arr_push` / the sole-owner
> `arr_push_owned`, like `str_split`). The fresh-buffer-per-read shape sidesteps
> the `buf + offset`-as-syscall-arg arithmetic the i32 raw-pointer floor can't do
> 64-bit-safely (`getdents64` never splits a record across calls, so each buffer
> parses independently); the record fields are read via `__raw_load8(buf, pos+k)`,
> which offsets in the emitter.
>
> **`remove_dir_all`** (`rt_src_remove_dir_all` → `Option[IoError]`) closed out the
> fs leaves — a best-effort recursive `rm -rf`. It classifies the target with
> `openat(O_DIRECTORY)` (ENOENT → `None`, ENOTDIR → `unlinkat` the file, else
> `Some(io_error)`), then for a directory **drains it inline** with the same
> `getdents64`-per-fresh-buffer loop as `read_dir`, builds each child path
> (`path + "/" + name`) directly in a raw buffer, and recurses, then rmdirs.
> Notably it does **not** reuse the `read_dir` builtin: `read_dir` returns an
> RC-managed `string[]`, and **holding that array live across the recursive call —
> which allocates heavily — corrupts it** (the held array's backing memory gets
> reused by the recursion's allocations; SIGSEGV on any tree with a subdirectory).
> A raw `getdents` buffer + raw child-path buffers are `__raw_alloc`'d (bump-only,
> never freed → never reused), so they survive the recursion untouched; the only
> RC value crossing the recursion is the returned `Option[IoError]`, which is safe.
> This RC/arena interaction (a heap array held live across a recursive allocating
> call) is a latent self-host bug worth a standalone fix — noted on #2649.
>
> **Status update (2026-07, dependencies-as-the-call-graph): the fs leaves share a
> Fern `__fern_io_error`.** `stat` / `read_file` / `remove_file` / `write_file` /
> `temp_dir` / `read_dir` / `remove_dir_all` no longer *inline* the five-way errno→variant classification
> (`ENOENT→NotFound`, `EACCES→PermissionDenied`, `EEXIST→AlreadyExists`,
> `EINTR→Interrupted`, else `Other`) — they **call** a single Fern classifier,
> `rt_src_io_error`. The needed helpers + `io_error` are emitted as one bundle
> module (only the needed ones, gated on the union of their needs), so the standard
> inter-procedural RC analysis threads each helper→`io_error` call as an ordinary
> call-graph edge (`io_error` consumes its fresh path arg; the helpers hand it a
> fresh copy, never their borrowed `path`). This is the #2649 end-state in
> miniature — a helper's dependency is a real call, not a hand-tracked inline.
> (`temp_dir` additionally calls the `monotonic_ns` builtin, op-mediated, so it
> composes in the bundle unchanged.) The arm64 / AST backends keep the register-ABI
> hand-asm `__fern_io_error`; this is x86-64 IR only. Verified RC-neutral against
> the pre-consolidation inlined bodies (identical arena growth) and byte-identical
> on the self-compile fixpoint.

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
| `__syscall4(nr, a1, a2, a3, a4): i32` | like `__syscall3` plus `a4→%r10; syscall` | the 4-arg sub-floor sibling, for syscalls whose 4th arg is meaningful (`openat`'s `mode` with `O_CREAT`, `newfstatat`'s `flags`) |
| `__raw_environ(): i32` | `movq __fern_envp(%rip), %rax` | the process `envp` pointer (saved by `_start`); the `env` leaf walks the array from it |
| `__raw_arr_box(n: i32): i32` | `mov n→%rdi; call __fern_arr_box` → data ptr | a fresh n-element array box, rc header + length + capacity already written — the array sibling of `__raw_alloc`, and the one array primitive that stays a call. Element i is at `[ptr + (i+1)*W]`, exactly what `__raw_store_ptr` addresses, so a helper fills it without naming the layout |
| `__raw_array(ptr: i32): i32[]` | nothing | the type-only bridge back to a typed array, the array sibling of `__raw_string`. The data pointer already IS the array value, so this emits no op — only the checker needed convincing |
| `__raw_addr(ptr: i32, off: i32): i32` | `addq %rcx, %rax` / `add x0, x0, x1` | `ptr + off` at the machine's FULL pointer width, for an address that has to be **passed** somewhere (a syscall buffer arg). Writing `p + off` in the helper source does not work: a raw pointer's surface type is `i32`, so arm64 narrows the sum back (`sxtw x0, w0`) and a high heap address arrives truncated. The load/store intrinsics dodge this by folding the offset into the addressing mode; this is the form for everything else |

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
5. **The syscall leaves** via the `__syscall3` / `__syscall4` sub-floor.
   **Done on x86-64 IR:** `random_bytes`, the three clocks (which also needed
   `__raw_scratch` + `__load_i64`), and the whole fs family — `stat`,
   `read_file`, `write_file`, `remove_file`, `read_dir`, `remove_dir_all`,
   `temp_dir`, `env` — over a shared Fern `__fern_io_error` classifier.
   **On arm64: `random_bytes` only, so far.** The blocker this doc used to name
   here — "the syscall number is arch-specific and the `rt_src_*` sources are
   shared, so parity needs an arch-parameterised source" — is now solved:
   `rt_src_random_bytes(sysno)` takes the number as a parameter, and darwinize
   rewrites the generic-syscall sequence for Mach-O (see the status block up
   top). The remaining arm64 leaves are the ones where the *number* is not the
   only difference: Darwin has no `clock_gettime` (gettimeofday with another
   struct, or CNTVCT_EL0) and its `stat` layout differs from Linux's, so each
   needs a per-platform source body rather than a substituted constant.
   wasm keeps its WASI bundles — it has no generic syscall.

Each slice ships with IR lock-in tests (the helper compiles from Fern, the
hand-asm label is gone) and reuses the existing behavioural coverage, and
must keep the self-compile fixpoint byte-identical.
