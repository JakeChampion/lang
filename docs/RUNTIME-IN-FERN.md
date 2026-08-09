# Runtime helpers in Fern — migration design (issue #2649)

Status (2026-07): **the Tier-0–2 helper migration is complete** — the
byte-building Tier-2 set (`chr`, `str_concat`, `i32_to_string`,
`str_to_upper`/`_lower`, `str_repeat`, `str_reverse`, `str_replace`,
`string_from_bytes`, `str_split`) now lowers as Fern functions via the
raw-memory intrinsics (`RUNTIME-INTRINSICS.md`), on top of the earlier
Tier-0/1 slices (`__fern_i32_pow`, the five `__fern_arr_i32_*` reducers,
`__fern_str_to_i32`, and the str predicates/utilities). The **syscall
leaves** followed on the x86-64 IR path (`random_bytes`, `random_i32`, the
three clocks, and the whole fs family) over the `__syscall3` / `__syscall4` /
`__raw_scratch` / `__raw_environ` sub-floor, and as of 2026-08 they are
reaching **arm64** as well: `random_bytes` first, then `read_file` /
`write_file` / `remove_file` / `temp_dir` / `stat` with their shared
`__fern_io_error`, then `env`, and finally `open_fd` — the Reader/Writer
opener, and the last leaf that had kept a register-ABI hand-asm
`__fern_io_error` alive on arm64 (that copy is deleted; the bundle is the only
one now). Each is ONE source across all three native
targets, with the syscall numbers, `AT_FDCWD`, open flag-sets and struct
offsets coming from `asmcore.sysno` / `at_fdcwd` / `oflag` / `statoff` keyed
by the target.

`open_fd` is the one place a target key was not enough. Its openat flags are a
PARAMETER, not a constant: irlower picks them per builtin (`open_reader` /
`open_writer` / `open_appender`) and has no target, so the op site passes the
Linux value everywhere and Darwin has to translate at run time — the same two
comparisons the hand-asm did, now in Fern, emitted only for `arm64-darwin`.
Getting this wrong is invisible until it corrupts data: untranslated,
`open_writer` asks XNU for O_CREAT-without-O_TRUNC and an overwrite leaves the
old file's trailing bytes (#6042). The **array producers** followed — the first non-syscall helpers
to move, all three over the `__raw_arr_box` + `__raw_array` pair:
`xs.reverse()` / `xs.concat(ys)` arch-independent (one source, no target
parameter), and `a[start:end]` with one, because its bounds check traps.

`random_i32` is worth a line of its own, because migrating it **fixed a bug
neither backend's tests could see**. Both hand-asm bodies loaded the four
random bytes with a zero-extending load (`movl` / `ldr w0`), so the i32 never
had its top bit set in its 64-bit slot and `random_i32() < 0` was dead code —
a disagreement with the native backend on every draw above 2^31. The Fern
helper returns `i32`, so the load sign-extends and the divergence goes away.
The existing coverage asked only that two draws differ, which both the correct
and the broken version satisfy; `random-i32-signed` on both IR paths now pins
the sign.

That trap is worth recording, because it read as a blocker and was not.
`__fern_oob_abort` is only `exit(134)`, which the existing `__syscall3`
sub-floor already expresses — so "there is no Fern expression for abort" was
true and beside the point. All it cost was an `exit` row in `asmcore.sysno`
(60 / 93 / 1); `darwinize`'s `darwin_sysno` already mapped `93 -> 1`. No new
intrinsic, no new IR op, no new backend code. Before reaching for a primitive,
check whether the floor already says the thing.

What remains hand-written — and what keeps
[#2649](https://github.com/JakeChampion/lang/issues/2649) open — is the
core allocator / map runtime and the array MUTATORS (`__fern_alloc`,
`__fern_map_*`, `__fern_arr_push`), Darwin's `monotonic_ns` alone, and the
per-backend wasm helper bundles. **The "shape-diverging leaves" category is
now empty** — see below; every entry that was in it turned out to be table
rows over the existing floor.

## The hot core needs better codegen first, not better expressiveness

`__fern_arr_push` was attempted and **backed out** (#6446). It is worth reading
before anyone tries it, the map runtime, or `arr_push_owned`, because the
blocker is not the one the rest of this document would lead you to expect.

Expressiveness was fine. The box header needs no new primitive — cap sits at
`data-16` and rc at `data-8`, and `__raw_addr` takes a BYTE offset with
full-width pointer arithmetic, so `__load_i32(__raw_addr(p, 0 - 8))` reads the
rc word's low half, where the immortal bit (31) lives. The ownership contract
needs no special handling either: the helper writes through its parameter and
returns its own receiver, and the emitted body carried ZERO refcount traffic,
because a raw-pointer body never handles the array as a typed value except
through the type-only bridges. It was correct, and passed every behavioural
probe on both native backends.

What killed it was emitted size, which is a proxy for speed here:

| | hand-asm | Fern-compiled |
| --- | --- | --- |
| total instructions | 27 | 353 |
| in-place fast path | ~15 | ~169 |

The IR's stack-machine lowering spends four instructions and two stack
round-trips materialising the constant `-8`, and `pushq %rax` / `popq %rax`
pairs run throughout. Every leaf migrated before this one was dominated by
something other than its own instruction count — the syscall leaves by syscall
latency, the array producers by allocation plus an O(n) copy — so codegen
quality did not matter to any of them. `arr_push` runs on every `.append()`
and its fast path is fifteen instructions of pointer arithmetic. A 10x
expansion there is a straight loss.

**So the precondition for the hot core is a better lowering, not a bigger
floor.** The `len(asm) > N` assertions scattered through `internal/e2eselfhost`
are what caught it; they read as "did the module bail to the AST path" but they
double as the only guard against codegen bloat, and raising them to accommodate
a migration would discard exactly the signal they exist to give.

### First instalment on that precondition: the push/pop peephole

`asm_ir.peephole_push_pop` folds an adjacent `pushq`/`popq` into the move it
actually is, removing **20% of the emitted lines** on the compiler's own source
(1,860,341 → 1,484,006) for 1.3% of compile time. It is a post-pass over the
emitted text rather than a change to instruction selection, so it needs no
clobber analysis: `popq Y` reads what the line above it just pushed, which makes
the pair `Y := X` whatever either line was *meant* for.

That is an instalment, not the fix — but the rest of the peephole avenue is
**not** where the remaining cost is, and the numbers say so clearly enough to
save the next person the detour.

The obvious follow-up is the non-adjacent pair: a binary op's outer operands,
left one instruction apart once the inner pair folds.

```asm
pushq -8(%rbp)          movq -16(%rbp), %rcx
movq -16(%rbp), %rcx    movq -8(%rbp), %rax     # not done — and not worth it
popq %rax
```

**Measure it before building it.** On the post-peephole self-compile
(1,485,135 lines) every push/pop pair still matched inside a basic block comes
to ~43,600 — 20,815 at distance 2, 15,210 at distance 3, ~7,600 beyond. Folding
*all* of them, with an unbounded window, is **2.9%**. The adjacent case was 85%
of every in-block pair there ever was. Paying for a clobber analysis to get the
remaining 15% is a bad trade, and the analysis is not cheap: implicit writes
make a mnemonic blocklist unsafe, since `idivq %rcx` clobbers `%rax` while
naming neither.

### Where the cost actually is: every argument goes via the stack

Sorting the 268,596 surviving pushes by what consumes them:

| consumed by | count |
| --- | --- |
| a `call` (stack argument) | 171,881 |
| a `popq` (peephole-able) | 42,331 |
| something else | ~54,000 |

with 130,118 call sites and 132,310 `addq $N, %rsp` cleanups beside them. Two
thirds of all remaining pushes are argument setup. Every call pays push-per-arg
plus a stack adjustment, and the callee then re-loads each one from `N(%rbp)`
into its local slot.

So the lever for the hot core is **register-passing for direct calls**, not more
peepholing. Note what that would touch before starting: the hand-written runtime
blobs call migrated helpers as `__fn___fern_*` under the current stack ABI, so
caller, callee prologue, and every hand-asm call site move together — a roadmap
decision, not a drive-by.

And none of this on its own clears the `arr_push` bar: 353 instructions less 20%
is still ~280 against 27.

One rule the attempt did establish, which applies to any future raw op: **it
must push a result.** Every raw op is typed `i32` and the statement lowering
emits a drop, so an op that consumes operands without pushing one unbalances
the operand stack at every call — `raw_store_ptr` pushes its value back for
precisely this reason. Getting that wrong hung six fixtures.

The **clocks** used to be on that shape-diverging list and mostly are not.
`now_unix_ms` / `now_ns` do diverge — Linux takes
`clock_gettime(clk, &timespec)` filling `{i64 sec; i64 nsec}`, XNU takes
`gettimeofday(&timeval, NULL)` (buffer FIRST) filling `{i64 sec; i32 usec}`,
so the number, the argument order and the subsecond field's WIDTH all move
at once — but every one of those is expressible with the existing floor, and
`clock_read(t, clk)` is where all three live. Only `monotonic_ns` on Darwin
is genuinely out of reach: XNU's monotonic clock on Apple Silicon is not a
syscall at all but `mrs cntvct_el0` scaled by `cntfrq_el0`, and Fern has no
system-register-read intrinsic. That one helper stays hand-written.

`read_dir` / `remove_dir_all` were the last two on that list, and went the
same way. Both grounds recorded against them were table rows: Darwin's
`getdirentries64` takes a 4th out-param `getdents64` has no equivalent of —
but `__syscall4` was already in the floor, so the shared body issues four
arguments on every target and hands Linux a register it ignores — and the
dirent layouts differ only in where the name starts (19 vs 21, XNU carrying a
`d_namlen` the Linux record does not), which is `direntoff`.

**Three consecutive slices found the same thing**, which is worth stating as
a rule rather than three anecdotes: a leaf recorded as "needs a per-target
body" usually needs a per-target CONSTANT plus a floor primitive that already
exists. Check the floor before designing a fork. The genuine exceptions so
far share one property — they are not syscalls at all (`mrs cntvct_el0`).

The counter-lesson from the same slice: `O_DIRECTORY` differs between the two
**Linux** arches too (65536 on x86-64, 16384 on arm64 — an arch-specific
override), so a Darwin-vs-Linux fork is not always the right shape for a
table. Every wrong value there is a silent wrong FLAG, not a rejected
constant: 65536 on arm64 Linux is `O_RDONLY|O_DIRECT`, EINVAL on `/tmp`. This is the architecture document the end goal of
[#2649](https://github.com/JakeChampion/lang/issues/2649) needs as more helpers
move; see the "Slice 1 / Slice 2 (landed)" sections at the end for what the
first migrations actually took, which was simpler than first proposed. The near-term stepping stone it references
(declarative `runtime_need_deps` table + `close_needs` transitive closure +
the symbol-closure link check) has already landed (PRs #2650, #3697); this
doc picks up where that left off.

## The end goal, restated

The backend runtime helpers — `__fern_alloc`, `__fern_str_eq`,
`__fern_map_set`, `__fern_arr_push`, `__fern_i32_pow`, … — are today
**hand-written assembly strings**, emitted per-backend:

| Backend | Emitter | Gating |
| --- | --- | --- |
| self-host x86-64 | `asm_ir.fern` `emit_ir_runtime` | `need`/`has_need` + `runtime_need_deps`/`close_needs`, shared via `asmcore.fern` |
| self-host arm64 | `asm_arm64_ir.fern` | same |
| self-host wasm | `wasm_ir.fern` helper bundles | `module_uses_*` + ad-hoc `if` coupling |
| native (Go) x86-64 | `internal/codegen/x86_64/x86_64.go` | `recordUse` use-flags |
| native (Go) arm64 | `internal/codegen/arm64/arm64.go` | `recordUse` use-flags |
| native (Go) wasm | `internal/codegen/wasmbin/runtime.go` | use-flags |

Their inter-helper dependencies are tracked **out of band**: a helper body
that `call`s another helper is a link-time edge nothing in the compiler
statically owns. The declarative table made that edge *declared* and
*transitively closed*, but it is still a parallel artifact that can drift
from the asm, and the wasm side still couples helpers with ad-hoc `if`s.

The principled end-state: **write the helpers as ordinary Fern functions in a
`runtime` module compiled through the same pipeline.** Then

- **dependencies are the call graph** — `map_set` calling `str_eq` is an
  ordinary edge, no `mark_*`, no `runtime_need_deps`;
- **gating is `treeshake`** (`examples/self_host/treeshake.fern`), the
  reachability DCE that already prunes `mod.funcs` to what `main` reaches —
  an unused helper is dropped for free, per program, per backend;
- **a missing dependency is a compile error, not a link error**;
- the hand-maintained per-backend asm and the manual dependency machinery
  (`runtime_need_deps`, `close_needs`, `module_uses_*`, the OR-gates)
  disappear.

## The hard part: circularity and the primitive floor

A runtime helper exists because the backend needs a routine for an operation.
The trap: **you cannot write a helper in terms of the operation it
implements.** `__fern_str_eq` implements `a == b` for strings; if you write

```fern
function __fern_str_eq(a: string, b: string): boolean { return a == b; }
```

the compiler lowers `a == b` to `call __fern_str_eq` — infinite regress.

So a Fern-written helper must bottom out only in operations that lower
**inline, without a helper call**. Call that set the *primitive floor*. It
partitions the helpers into three tiers by how far they sit above the floor:

### Tier 0 — pure scalar, already above the floor

Helpers whose body is only integer/boolean arithmetic, comparison, and
control flow. These lower entirely inline today and have **no helper
dependency and no circularity**. `__fern_i32_pow` is the canonical example
(`asm.fern:7857`):

```fern
// emits as the symbol __fern_i32_pow; ABI: (base, exp) -> base^exp
fn __fern_i32_pow(base: i32, exp: i32): i32 {
    var r: i32 = 1;
    var e: i32 = exp;
    while (e > 0) { r = r * base; e = e - 1; }
    return r;
}
```

Its body uses `*`, `-`, `>`, `while` — none of which is the `**`/pow operator
that would call back into it. It compiles to standalone scalar code whose ABI
(`base`, `exp` → result) already matches the asm version. **This is the
slice-1 candidate.** Companions: `arr_i32_sum`/`product`/`min_max` (once array
indexing is confirmed inline — see Tier 1), `str_cmp`/`str_eq` *given* a
raw-byte-read primitive (Tier 2).

### Tier 1 — composite access (arrays, struct/box fields)

Helpers that read a `string`/array box's `{ptr, len}` layout or index
elements. Array indexing (`xs[i]`) and `.len()` lower inline, so an
`i32[]`-only helper like `__fern_arr_i32_sum` is expressible — *but* it
receives an RC-managed array and the compiler's normal Perceus inc/dec would
fire on the parameter. The helper ABI is "borrow, no refcount traffic," so we
need either (a) a `borrow`/`@raw` parameter mode that suppresses RC on these
functions, or (b) to accept and then strip the RC ops in a runtime-module
pass. **Decision: (a)** — a function attribute (`@runtime` / `@raw`, see
below) that marks the whole function as operating on borrowed, unmanaged
values, suppressing inc/dec insertion and the implicit drop epilogue. This is
also what keeps the emitted code byte-for-byte comparable to the asm version.

### Tier 2 — raw memory (strings byte-by-byte, alloc, maps)

`__fern_str_eq`, `__fern_str_concat`, `__fern_map_set`, and ultimately
`__fern_alloc` itself read/write raw bytes at computed addresses and call the
allocator. These are *below* the Fern surface language: there is no safe Fern
expression for "load the byte at `ptr + i`." They require a small set of
**intrinsics** that lower directly to a load/store/syscall with no helper:

- `__raw_load8(ptr: i64, off: i64): i32` / `__raw_store8(ptr, off, v)`
- `__raw_load64` / `__raw_store64`
- `__raw_alloc(bytes: i64): i64` (the bump/freelist primitive — `__fern_alloc`
  becomes a *thin Fern wrapper* over this, or the intrinsic *is*
  `__fern_alloc` and stays asm the longest)
- `__syscall3(nr, a, b, c): i64` for the I/O leaves (`read_file`,
  `random_bytes`, the clock helpers)

These intrinsics are the irreducible floor — the asm that *cannot* move. The
goal is to shrink the hand-written runtime to exactly this floor plus
`__fern_alloc`, with everything else expressed in Fern above it.

**Migration ladder:** Tier 0 (pow, then the other pure-scalar/`arr_i32_*`
reductions) → introduce `@runtime`/borrow attribute → Tier 1 (composite,
borrow-safe) → introduce raw-memory intrinsics → Tier 2 (strings, maps) →
`__fern_alloc`/syscalls stay as the floor.

## How the runtime module reaches every program

The auto-prelude injector was removed (CLAUDE.md, Phase 5): a program now sees
only what it `import`s, and `modload` mangles + dedupes loaded modules. A Fern
runtime must therefore be **auto-loaded by the driver**, not by the user. Two
options:

1. **Implicit import of `runtime`** — the driver prepends the runtime module
   to every compilation's load set (a single, controlled, runtime-only
   re-introduction of injection, *not* a general prelude). Helpers are normal
   decls; `treeshake` drops the unreached ones before IR lowering, so a
   trivial `main` still links tiny. **Recommended** — it is the most direct
   route to "gating is the existing DCE pass."
2. **Emit-time compile of `runtime.fern`** — keep the runtime out of the
   user's module graph; the backend compiles `runtime.fern` separately and
   appends only the reached helpers' emitted code where `emit_runtime` sits
   today. More plumbing, but zero risk of the runtime's decls colliding with
   user names or perturbing modload mangling.

Either way the helpers must emit under their **exact existing symbol names**
(`__fern_str_eq`, …) and ABIs so the transition is a drop-in: a backend can
move one helper to Fern while the rest stay asm, and call sites are unchanged.
That needs a way for a Fern function to fix its emitted symbol — a
`@symbol("__fern_str_eq")` / `@runtime` attribute (also the natural carrier
for the borrow/no-RC semantics of Tier 1/2). Defining the attribute is a
prerequisite sub-task.

## Gating: `treeshake` replaces the dependency machinery

Once helpers are reachable Fern functions, `treeshake` (already a CI-gated
pass, `examples/self_host/treeshake.fern`) prunes `mod.funcs` to those
reachable from `main`. A helper calling another helper is an ordinary
identifier reference the collector already follows — so the transitive
closure that `close_needs` computes by hand becomes the reachability walk for
free, **per backend**, with no table. As each helper moves to Fern, its entry
is deleted from `runtime_need_deps` and its `has_need`/OR-gate from the
emitter; when the table is empty, `runtime_need_deps`/`close_needs`/`need`/
`has_need` and the wasm `module_uses_*` couplings are deleted outright.

## Constraints the migration must hold

- **Byte-identical self-host fixpoint.** `TestSelfHostLoadFixpointX86_64` /
  `…ModloadFixpoint…` require `compiler(compiler)` to be stable. Moving a
  helper changes the emitted bytes of *every* program (including the compiler
  binary), so each slice must re-establish the fixpoint, and the differential
  oracles must show the new emission is correct, not merely stable.
- **Exact ABI parity during transition.** Each moved helper keeps its register
  ABI (e.g. `__fern_i32_pow`: `%rdi=base, %rsi=exp → %rax`; string box =
  `{ptr@0, len@8}`) so mixed asm/Fern runtimes interoperate.
- **No RC traffic in helpers.** Tier 1/2 helpers borrow; the `@runtime`
  attribute must suppress Perceus inc/dec and the drop epilogue, or the helper
  changes the heap accounting the rest of the runtime assumes.
- **IR 512-function budget.** `treeshake` already exists to keep
  stdlib-importing programs under budget; the runtime module must shake down
  to only the reached helpers so it does not push programs onto the AST
  fallback.
- **arm64 is CI-validated, not local.** Per CLAUDE.md, gate locally on the
  x86-64 + wasm equivalents; the arm64 matrix runs in CI. Because the two asm
  backends share `asmcore.fern`, an x86-64-green frontend change is almost
  always arm64-green — but the per-helper instruction selection is the part
  that is *not* shared, so a moved helper must still be differential-tested on
  both.
- **Goal ordering (CLAUDE.md).** The self-host **IR path** is the priority
  (goal 1); the native Go backends are slated for retirement, so the migration
  targets `asm_ir.fern`/`asm_arm64_ir.fern`/`wasm_ir.fern` first. A helper
  that works on the IR + self-host path but not the legacy AST→asm backend is
  acceptable per the project's stated gap policy.

## Validation strategy per slice

1. **Differential** — emit the program both ways (helper-as-asm vs
   helper-as-Fern) and assert identical observable behaviour across the
   x86-64 absolute-exit oracle, the wasm emit oracle, and the three
   differential suites (`internal/e2e/feature_differential_test.go` et al.).
2. **Symbol closure** — `internal/e2e/runtime_helper_closure_test.go` must
   still pass: the emitted runtime stays symbol-closed whether a helper comes
   from asm or from compiled Fern.
3. **Fixpoint** — re-establish `TestSelfHostLoadFixpointX86_64` +
   `…Modload…`.
4. **The helper's own behaviour** — an e2e test exercising the operation the
   helper backs (e.g. `2 ** 10`, `xs.sum()`), proving the Fern implementation
   is correct, not just present.

## Slice 1 (landed) — `__fern_i32_pow`

`__fern_i32_pow` (the integer-power helper backing `n.pow(e)`) is the first
helper moved off hand-written asm. It is **self-host-only** (the native Go
backends lower `**`/`pow` their own way and never emit it) and lives only in
the **AST** emitters (`asm.fern` x86-64, `asm_arm64.fern`) — the IR path
doesn't lower `pow` at all — so the migration touched exactly those two
backends plus their shared `asmcore`.

The implementation turned out **simpler than the attribute + module-injection
plan above**, because `i32_pow` is gated by an explicit `need`, not by
reachability, and is called only from backend-generated code (no Fern call
site to rewrite through modload):

1. **The helper is Fern source, compiled at emit time.**
   `asmcore.rt_src_i32_pow()` returns the function text
   (`function __fern_i32_pow(base, exp) { … multiply loop … }`) — shared in
   `asmcore` so x86-64 and arm64 have one source of truth. Each backend has a
   small `emit_runtime_fern_fn(src, s)` that does
   `parser.parse_module(lexer.tokenize(src))` and runs each function through
   the **normal `emit_function`**. No `@runtime`/`@symbol` attribute was
   needed: a top-level Fern function already emits under `__fn_<name>`, and the
   body is fully type-annotated pure scalar, so it needs no checker pass and
   calls no other helper (no circularity).
2. **The call site uses the ordinary stack convention.** `n.pow(e)` now pushes
   args right-to-left and `call __fn___fern_i32_pow` (x86-64) / `bl` (arm64),
   cleans the stack, and pushes the result — exactly how any user-function call
   is lowered — instead of the old register-ABI `call __fern_i32_pow`.
3. **Gating is unchanged.** The existing `i32_pow` `need` (`mark_i32_pow` /
   `has_need("i32_pow")`) still gates emission, so a program that never calls
   `.pow()` emits nothing new. The hand-written asm block and the bare
   `__fern_i32_pow` symbol are gone.
4. **Tests.** The behavioural pow cases in `TestSelfHostAsmRunX86_64`
   (`i32-pow-2-10`, `-exp-zero`, `-base-zero`, `-3-5`) prove correctness;
   `TestSelfHostRuntimeHelperI32PowIsFern` locks in the migration itself
   (asserts the emitted symbol is `__fn___fern_i32_pow` and the hand-asm form
   is gone). The x86-64 self-host fixpoint (`TestSelfHostLoadFixpointX86_64` /
   `…Modload…`) re-establishes; arm64 runs in CI.

### What this proved, and what the bigger migration still needs

Slice 1 establishes the reusable primitive — **a runtime helper written in
Fern, compiled through the real `emit_function`, linking under the right
symbol** — for the cheap case: a Tier 0 leaf gated by an explicit `need` and
called only from backend codegen. The `@runtime`/`@symbol` attribute, the
driver-loaded `runtime.fern`, and `treeshake`-based gating from the sections
above become necessary precisely when those conditions break — i.e. once a
helper must be **reached from Fern call sites** (so modload/treeshake must keep
and not-mangle it) or carry **borrowed, non-RC params** (Tier 1+). The next
leaf candidates that fit slice 1's cheap shape (need-gated, scalar/borrowed,
backend-called) can reuse `emit_runtime_fern_fn` directly.

## Slice 2 (landed) — the i32-array reducers

`__fern_arr_i32_sum` / `_product` / `_index_of` (backing `xs.sum()` /
`.product()` / `.index_of(x)` / `.contains(x)`) followed, reusing
`emit_runtime_fern_fn` unchanged (`asmcore.rt_src_arr_i32_*`). They are the
first **Tier 1** helpers — they take an array param — and the slice confirmed
the borrowed-param assumption holds in practice with **no new machinery**:
`emit_function` already records the receiver+param boundary so the Perceus exit
sweep releases only locals, not params, and at the call site the array is
passed by the same plain pointer-push the old register-ABI path used (no inc).
The bodies bottom out in inline-lowered `xs.len()` / `xs[i]` indexing and i32
arithmetic — still no helper call and no allocation — so a Tier 1 borrowed
reducer needs nothing beyond what slice 1 built.

`__fern_arr_i32_min` / `_max` (backing `xs.min()` / `.max()`) followed in the
same slice. They look like they'd need the heap-returning `Option[i32]` shape,
but the call site already special-cases the empty array (→ `None`) and builds
the `Option[i32]` box itself — so each helper only ever runs on a **non-empty**
array and just returns the extremum `i32`. That makes them the identical clean
Tier 1 reduce-with-comparison shape as sum/product: the Option boxing stays in
the call site (unchanged), only the two reduce loops move to Fern.

### After Slice 2 — the clean leaves are exhausted

`i32_pow` and the five `arr_i32_*` reducers were the helpers that are
**AST-only** (the IR path doesn't lower them), **call no other helper**, and
**don't allocate**. Every remaining helper breaks at least one of those, which
is what the next phase has to confront:

- **IR-path-integrated** — `str_to_i32`, `i32_to_string`, `chr`, `str_cmp`,
  `str_search`/`str_starts_with`/`str_index_of` are emitted *and* called on the
  self-host IR path too (via register-ABI calls or `__fn___fern_*` stack
  wrappers in `emit_ir_runtime` / `asm_arm64`'s runtime). Migrating one means
  reconciling the IR call convention, not just the AST call site.
- **Helper-to-helper** — `str_eq` is called by `__fern_map_set` and
  `__fern_arr_str_index_of` via the register ABI; moving it to the `__fn_`
  stack convention breaks those hand-written callers unless they move too
  (a cluster migration).
- **Tier 2 (heap / syscalls)** — `str_concat`, `str_split`, `str_repeat`,
  `str_reverse`, the `arr_str_*` joins, the clock/random/`alloc` leaves all
  allocate or syscall, and need the raw-memory intrinsics / borrow attribute
  the upper sections describe.

So the next slice is a genuine step up: pick one IR-path helper and extend
`emit_runtime_fern_fn` (or its IR equivalent) to emit the Fern function on the
IR path too, **or** migrate the `str_eq` cluster together. Both want the IR
side validated, not just the AST backends.

## Slice 3 (landed) — the IR path hosts a Fern runtime helper (`str_to_i32`)

The structural unblock: `asm_ir.fern` gained `emit_ir_runtime_fern_fn`, the
**IR-path analog of `emit_runtime_fern_fn`** — it parses a helper's Fern source
and lowers it through the *IR pipeline* (`irlower.lower_func` → `emit_function_via_ir`)
so it emits as the ordinary user-function symbol `__fn_<name>`, which the IR
call site (`op_call_direct("__fern_<name>")` → `ir_helper_symbol`) already
targets. `__fern_str_to_i32` is the first helper moved this way: its hand-written
stack-arg wrapper in `emit_ir_runtime` is gone, replaced by lowering
`asmcore.rt_src_str_to_i32` (now written with `for b in s` + `break`, both
IR-eligible, instead of `s[i]` indexing).

Key mechanics that made it safe:

- **Self-contained ⇒ singleton side-tables.** `emit_function_via_ir` needs ~18
  return-type / signature side-tables. Because the helper calls no other user
  function, computing them over the helper *alone* is complete — in particular
  `borrowable_params_interproc([fd])` correctly marks the read-only `string`
  param **borrowed**, so the lowering inserts no RC dec that would free the
  caller's string. The `str2i32-roundtrip` test (which feeds a freshly-allocated
  `i32_to_string(99)` straight into `str_to_i32`) is the use-after-free probe
  that confirms this.
- **Entry-only, in `.text`, after need-aggregation.** The shared runtime is
  emitted only by the entry unit, after sibling `extra_needs` are folded and
  `close_needs` runs, so the call is placed there gated on the `str_to_i32`
  need (writing `.text` first, since the literal pools left us in `.rodata`).
  `emit_runtime_globls` already `.globl`s `__fn___fern_str_to_i32`, so the
  per-module link resolves it across units.

Scope: this migrates `str_to_i32` on the **x86-64 IR path** only. The x86-64 AST
register body (`asm.fern`) and the arm64 paths (whose AST+IR share
`asm_arm64.emit_runtime`, so they need a coordinated change) keep their
hand-written bodies for now — separate compiles, no symbol conflict. The point
of this slice is the *primitive*: `emit_ir_runtime_fern_fn` now exists, so the
remaining IR-path helpers (`i32_to_string`, `chr`, …) and the AST/arm64 copies
can follow without inventing new machinery.

Validated on x86-64: `TestSelfHostAsmIRPath/str2i32-*` (behaviour incl.
roundtrip), `TestSelfHostIRRuntimeHelperClosure` (per-module link of all 46
need-roots), both fixpoint suites (self-hosting preserved), and
`TestSelfHostRuntimeHelperStrToI32IsFernIR` (locks in the Fern symbol + the
absence of the hand-asm wrapper). arm64 unchanged.
