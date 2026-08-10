# Roadmap: tech debt + self-hosting readiness

> **Status: historical snapshot (2026-05-15).** Self-hosting has since
> shipped; several Part-1 items are struck through as RESOLVED inline. The
> live roadmap is `CLAUDE.md` (goal 1: full IR in the self-host; goal 2: the
> Perceus port) plus the GitHub issue tracker — roadmap-shaped residue from
> this doc is consolidated in
> [#4368](https://github.com/JakeChampion/lang/issues/4368).

Captures the state of the codebase as of 2026-05-15. Combines
two audits — current tech debt (what's wrong / behind / risky)
and self-hosting readiness (what's missing to rewrite the
compiler in Fern itself). Companion to `BACKEND-PARITY.md`,
`SSO-PLAN.md`, `MAP-SPECIALIZATION.md`.

## Part 1 — Tech debt

Ranked roughly by severity. Each item includes file refs, why
it's debt, and the shape of a fix. The first three are on the
critical path for production parity; items 4 and 5 unblock
follow-on perf work.

### 1. ~~arm64-darwin Map heap-address truncation~~ — RESOLVED

Originally the audit's top critical item. `usize` landed as
a target-aware NumberType width earlier; this PR migrates the
Map runtime's pointer-typed params + locals
(`m`, `k`, `v`, `buf`, `entriesBase`, `entryK`, etc.) from
`i32` to `usize`, preserving the full 8-byte address through
every helper on arm64-darwin's high heap. `__map_hash` keeps
its `k: i32` param because wide-scalar K boxing already
ensures the value fits in 32 bits via the cell-pointer route
— a future PR can flip it once a dedicated wide-scalar K
hash entrypoint exists. The `map_heap_value_probe` test's
Darwin skip has been removed; macOS CI now exercises it
alongside Linux.

### 2. ~~Closures-with-captures only lower on wasm~~ — RESOLVED

Originally tracked as a feature gap. On a follow-up audit
the implementation was already in place on every backend:

- `internal/codegen/arm64/arm64.go:emitMakeClosureOrEnv`
- `internal/codegen/x86_64/x86_64.go:emitMakeClosureOrEnv`
- `internal/codegen/wasm/wasm_ir.go:emitMakeClosureFromIR`

Test coverage: 8 `TestArm64Closure*` cases plus matching
counts on x86-64 + wasm, all green. The original audit was
based on a stale `CLAUDE.md` line that has since been
updated. Item is closed — leaving the entry in the doc so
the audit history is preserved.

### 3. ~~Wide-scalar Map K/V (i64 / u64 / f64) silently
   diverges across backends~~ — RESOLVED

- **Where**: `internal/ir/ir.go` `mapKeyKindTag` /
  `mapValKindTag`. The doc previously claimed both only
  knew "0=i32-scalar / 1=string / pointer" — that's stale.
- **Resolution**: the runtime-tag scheme was extended.
  `mapKeyKindTag` now includes **kind 2 = wide-scalar-boxed**
  for i64 / u64 / f64 keys when `ptrW < 8` (wasm32); the
  IR boxes the key into a heap cell and `__map_hash` /
  `__map_lookup` dereference to hash/compare the underlying
  8-byte value. `mapValKindTag` was widened (kinds 0..3 for
  i32-scalar / non-array-pointer / non-rc array / rc-tracked
  array), and wide-scalar V types use the `emitWideMapSet` /
  `emitWideMapGet` boxing codepath.
- **Coverage** (wasm e2e): `TestWASMWideKeyMapBasic`,
  `…HasDelete`, `…Overwrite`, `…Grow`, `…HighBitsDistinct`,
  `…U64`, `…StringV`, `…KeysSnapshot`, plus wide-V
  `TestWASMMapValuesWideI64` / `…F64` and the boxing-dance
  series at `wasm_e2e_test.go:2446`+.

### 4. Map runtime-tag dispatch in hot paths — perf + code size

- **Where**: `internal/prelude/prelude.fern` —
  `__map_hash`, `__map_lookup`, `__map_set_impl`,
  `__map_delete_impl`, `__map_keys_impl`,
  `__map_values_impl`. `docs/MAP-SPECIALIZATION.md` plan
  exists; 0 of 5 steps started.
- **Why**: every probe step pays a runtime `if (keyKind ==
  0)` branch, the hash function takes a tag arg, the
  keys()/values() snapshot pays a `if (valKind == 1)` check.
  Every Map-using binary carries both the scalar and string
  hash bodies even when only one is used (the tree-shaker
  can't eliminate dead branches inside a function).
- **Fix**: 5-step monomorphization via existing
  `internal/monomorph` (the machinery, mangling, and
  re-check pass already work for user-defined generic
  functions). Plan documented in `MAP-SPECIALIZATION.md`.

### 5. 16-byte operand-stack slots on natives — memory + perf

- **Where**: `internal/codegen/arm64/arm64.go:4434`
  (`slotBytes = 16`), `internal/codegen/x86_64/x86_64.go:1678`
  (same).
- **Why**: every push uses 16 bytes regardless of type — a
  16-byte alignment hedge for arm64 `stp` / `ldp` and System
  V pre-call alignment on x86-64. Halving slots to 8 bytes
  is *not* a simple constant flip: an experiment patching
  both `slotBytes` values to 8 (no other changes) **builds
  cleanly but SIGSEGVs 55 of the e2e fixtures**, because
  the existing codegen relies on every push being an even
  multiple of the call-boundary alignment requirement
  (16 bytes for arm64 AAPCS64 and x86-64 SysV) — every
  push currently maintains the invariant "for free".
  Switching to 8-byte pushes makes parity toggle each push,
  so calls land at an unaligned `sp` and the callee's stp /
  push of a save register faults. The alignment requirement
  matters at call boundaries; what's missing is the
  bookkeeping to re-align there.
- **Fix**: switch arm64 to unaligned `str x0, [sp, #-8]!`,
  x86-64 to `push rax`. Re-align sp to 16 only at the
  `bl` / `call`. Touches every push/pop, callee-save spill,
  multi-arg call layout. Medium scope, independent of items
  1-4.

### Latent bugs (not user-visible yet)

- ~~**`internal/codegen/arm64/arm64.go:4088`** — Linux-only
  syscalls crash at *codegen time* on arm64-darwin~~ — fixed
  in PR #391. The pre-scan now detects `read_file` calls on
  Darwin and surfaces a clean error from `EmitWithOptions`
  before any asm is written. The codegen panic stays as a
  defence-in-depth assertion for future helpers that grow
  Linux-only syscalls without a corresponding pre-scan entry.
- **`internal/ir/ir.go:2531,2660`** — i64 → string cast
  returns `"not yet supported"`. Dead today (checker rejects
  it pre-IR), but a latent blocker for the wide-scalar +
  usize work.
- **`internal/ir/ir.go:4717`** — `assignment target %T not
  yet lowered`. Dead code path (checker would catch it
  first); signals an unfinished lowering case.

### Sprawl signals

- **Runtime-tag injection in 3 places** (`ir.go`,
  `checker.go`, `ast.go`) — item 4 will consolidate.
- **`ptrW == 4` checks scattered across ~12 sites** —
  partially unified via `ast.UseTwoWordStrings`; SSO work
  is finishing the consolidation.
- **SSO native-mirror flip in flight** — wasm32 shipped in
  PR #382, arm64 in PR #383 + follow-ups. x86-64 mirror is
  the next chunk; estimated 10-15 PRs.

---

## Part 2 — Self-hosting readiness

The compiler is written in Go (`cmd/fern/main.go` +
`internal/{lexer,parser,ast,checker,monomorph,closureconv,ir,codegen,interp,treeshake,diag,modload,prelude}`).
Self-hosting means rewriting it in Fern itself and
bootstrapping from a previous version. Rough estimate:
**~60% portable today** at the time of this audit. The hard
blocker (union types) has since landed — see the resolved
section below. Updated estimate: ~75% portable.

### ✅ UPDATE: a convergent self-hosting fixpoint is achieved

The `examples/self_host/` Fern port — lexer + recursive-descent
parser + x86-64 emitter (`asm.fern`) + module-flattening
(`flatten.fern`) + a stdin driver — now **compiles its own source
to a byte-identical compiler**. The bootstrap chain
(`internal/e2e/self_host_fixpoint_test.go`):

```
stage 0  Go compiler builds bundle_run (the multi-module driver)
stage 1  bundle_run bundles lexer+parser+asm+flatten+io+driver -> mmc
stage 2  mmc compiles its own source                           -> gen2
stage 3  gen2 compiles its own source                          -> gen3
```

`mmc`, `gen2`, and `gen3` are all **byte-identical** (~3.67 MB of
asm) — the Go-bootstrapped and self-hosted compilers produce exactly
the same output, and the self-hosted compiler reproduces itself.
`gen2` also compiles independent programs (e.g. a 2-module
`a.add(19,23)`) to working binaries. The earlier one-trailing-newline
gap between `mmc` and `gen2` (Go `print` = puts vs the self-host
emitter) was closed by adding a no-newline `write` builtin and
switching the driver to it, so the convergence is now total from
stage 1.

The same fixpoint holds on **ARM64** (`TestSelfHostFixpointArm64`,
CI-gated under qemu-aarch64): the Fern-authored `asm_arm64.fern`
emitter reproduces itself byte-for-byte too.

The driver no longer relies on any compiler-injected I/O shortcut:
the bundle carries the **unmodified** `internal/stdlib/std/io.fern`,
and the self-hosted compiler reads its own stdin through the real
`std/io.read_all_stdin` (`stdin` + `Reader.read_chunk` + `Some`/`None`
+ `match`). Both emitters lower that Reader / Option machinery
(`__fern_reader_read_chunk` / `__fern_reader_close`, Option boxes
`[tag@0, payload@8]`, tag-discriminated `match` with payload binding).

The walls cleared to get here (all in `examples/self_host/`, gated by
`internal/e2e/self_host_*_test.go`): O(N²) output build → `strbuf`;
parser non-advance runaways on qualified names
(`parse_type_name` / `parse_pattern`) and qualified struct literals;
`strbuf` + `read_all_stdin` builtins + a 256 MiB heap in the emitter;
the no-newline `write` builtin (stage-1 byte-identity);
struct-field-read and method-call-result type inference; module
flattening (qualified-ref rewrite + own-decl mangling + bundle);
amortised-O(1) array `push` (geometric growth with a hidden capacity
word); the ARM64 emitter brought to self-hosting parity; and the
Reader / Option lowering on both backends (so the fixpoint compiles
the real `std/io`). The original "minimal fix … ~1-2 weeks" /
"6-9 weeks" estimates below are superseded by this result.

### ✅ UPDATE: real CLI toolchain + import-driven file loading

The self-host emitters have since grown the surface a real compiler
driver needs, all gated by `internal/e2e/self_host_*_test.go` and
cross-checked against the Go backend (both x86-64 and arm64):

- **argv + file I/O**: `args(): string[]`, `arg_at` / `args_count`,
  `read_file(path): Result[string, IoError]` (`openat` + `lseek`-sized
  read + `Ok`/`Err` match), `write` (no-newline output).
- **integer ops**: `expr as Type` casts (mask / sign-extend per width),
  bitwise + shift operators `& | ^ << >>` (added to the parser
  precedence table — they were silently dropped before).
- **byte arrays**: `__alloc_u8(n)`, `string_from_bytes(arr)`, and array
  element assignment `arr[idx] = val`. Together these compile the real
  `std/hex` and `std/base64` (`TestSelfHostBytesX86_64`).

On top of those, `examples/self_host/asm_load_run.fern` is an
**import-driven, file-loading driver**: given an entry `.fern` path it
follows `import "./x"` declarations to sibling files on disk
(`read_file` + the parser's `Import` list), loads them transitively,
and compiles the merged set — no stdin marker-bundle. It reaches its
own **file-driven self-hosting fixpoint** (`TestSelfHostLoadFixpointX86_64`):
the Go-built loader compiles its own source resolving
lexer + parser + flatten + asm from disk → gen1, and gen1 → gen2
byte-identical. (`asm_file_run.fern`, the simpler single-file CLI shape,
was retired in #4398 — it was a strict subset of `asm_load_run`.)

### ✅ UPDATE (2026-06): unified `fern` CLI + a self-hosted wasm backend

Two things landed on top of the native (x86-64 / arm64-linux) self-host
since the notes above:

**Unified `fern` CLI** (`examples/self_host/fern.fern`). Where the
codebase previously had a dozen single-mode `*_run.fern` shims, there is
now one self-hosted binary that parses argv flags and dispatches —
`fern [-check | -interp | -fmt] [-target x86-64-linux|arm64|arm64-darwin|wasm]
[-o OUT] <entry.fern> [stdlib-root]` — reusing the import-driven file
loader.
It hosts both native emitters (`asm.fern` + `asm_arm64.fern`) plus the
checker, interpreter, printer, and the wasm emitter, selected at runtime.
Gated by `internal/e2e/self_host_cli_test.go`.

**A third self-host backend: wasm** (`examples/self_host/wasm.fern`,
driven by `wasm_run.fern` and `fern -target wasm32-wasi`). It emits a WASI core
module in the text format (WAT) — `_start` calls `proc_exit(main())`, so
`wasmtime run prog.wat` exits with the program's result. Built up across
~20 incremental, differential-tested slices (each its own PR, gated by
`internal/e2e/self_host_wasm_emit_test.go`, run end-to-end under
`wasmtime`), it now compiles the **full non-generic-monomorphised core
language**:

- integers with **non-trapping** div/rem (matching the native backends:
  `x/0=0`, `x%0=x`, `INT_MIN/-1=INT_MIN`), comparisons, logical ops;
- locals, `if`/`else`, `while`, `break`/`continue`, early `return`;
- free functions, recursion, mutual recursion, and **receiver methods**
  (`$RecvType__name`, receiver passed first; return types flow through
  the type tracker);
- the full **string** library (heap `[len][bytes]` blocks: concat,
  `==`/ordering, `len`, `starts_with`/`ends_with`/`contains`/`index_of`,
  `to_upper`/`to_lower`/`repeat`, `join`/`split`), `print`/`write`/
  `print_int` via `fd_write`, a bump allocator;
- **arrays** (`[len][cap][elems]`): literals, index, `.len()`, `.push()`
  with geometric growth, index-assignment, `for…in`; i32 **and** string
  element typing;
- **structs** (`[type_id][fields]`): literals, field read/assign, `{
  ...base }` update, nesting, struct-typed params/returns;
- **Option/Result** tag boxes + `match` + the `?` operator, with typed
  Some/Ok payloads (string/array/struct);
- **struct-union `match`** (`type E = A | B`) dispatching on the type id;
- **generics by erasure** (the parser drops the `[T]` lists; the backend
  compiles one body per decl);
- the **wasi runtime builtins**: `env` (environ_get), `random_bytes`
  (random_get), `args` (args_get), `read_file` / `write_file`
  (path_open + fd_read/fd_write), and the **clocks** `now_unix_ms` /
  `monotonic_ns` / `now_ns` (clock_time_get);
- a real **i64 value path** — i64-typed locals / params / returns, i64
  arithmetic (`+ - * /` `%` via guarded 64-bit div/rem) and comparison,
  literal coercion into i64 sinks, and an i64 decimal formatter — so the
  64-bit clock timestamps (which exceed the i32 range) round-trip and
  print correctly;
- an **f64 floating-point path** mirroring the i64 one — f64 locals /
  params / returns, arithmetic + comparison, the `as` casts (f64↔i32/i64,
  with `i32.trunc_f64_s` / `f64.convert_*` and integer narrowing masks),
  and the primitive math builtins (`__sqrt_f64` / `__floor_f64` /
  `__ceil_f64` / `__trunc_f64` / `__round_f64` / `__abs_f64`);
- **maps** — a `Map { … }` literal (desugared to `map_new[_i32](n).set(…)…`)
  with `.set` / `.get` (→ `Option`) / `.get_or` / `.has` / `.len`, backed by
  an open-addressing hash runtime (linear probing, load-factor-0.75 growth
  + rehash). Both **i32 keys** and **string keys** (content hash via FNV-1a
  + `__fern_streq` compare, selected by a key-kind flag in the map box), and
  both **i32 values** and **string values** (value-type tracking types
  `.get` / `.get_or` results so they print / concat correctly), plus
  `.delete` (tombstone deletion — the probe skips tombstones, `set`
  reclaims them, and growth/rehash drops them), `.keys` / `.values`
  (snapshot arrays in probe order — `string[]` for string keys / values, so
  `for k in m.keys()` / `for v in m.values()` type correctly), and
  `for (k, v) in m` direct pair iteration (walks the live slots, binding
  k / v with the right key / value type, honouring break / continue). The
  **map surface is now complete**;
- **slices** `x[a:b]` — a string slice into a fresh `[len][bytes]` block
  (reusing `substr`), an array slice copying the element range into a fresh
  `[len][cap][elems]` block; the result keeps the source's element type
  (`string` / `string[]` / i32[]) so methods, concat, indexing and
  `for v in xs[a:b]` all resolve;
- **tuples** `(a, b, …)` — N consecutive slots accessed by the numeric
  `.N` field; per-element kind tracking types a `t.N` read (so a string
  element prints / concats / supports methods), incl. nested tuples;
- **lambdas + capturing closures** — a lambda value is a closure box
  `[table_idx, cap0, cap1, …]`; the body is emitted as a top-level
  `$__lambda<i>` (leading `$__env` param) and registered in a function
  table, so calls — of a lambda-bound local or an `fn`-typed param — lower
  to `call_indirect` through a per-arity `$clos<N>` type. Free locals of
  the enclosing function are detected by capture analysis, evaluated at
  creation (by value), stored into the box, and read inside the body via
  `$__env` loads (string captures keep their type). Module-wide lambda
  indexing rides on a shared 1-element-array counter threaded through the
  (otherwise pure) `emit_expr`. **The core language is now complete on the
  wasm backend.**
- **`.to_string()` + f-strings** — `(n).to_string()` (the f-string
  desugaring) formats an i32 / i64 into a fresh `[len][bytes]` block via the
  integer→string runtime; a string receiver is the identity. f-strings work
  end-to-end (the parser lowers them to `"…" + (expr).to_string() + …`);
- **arrays of structs** — a `var pts = [Struct{…}, …]` (or a `T[]`
  annotation) is tracked with its element struct type, so `for p in pts`
  binds `p` as that struct and `pts[i].field` resolves (found + fixed via an
  integration capstone — word count, a reduce taking an `fn`, and a
  struct-method loop, all compiled and run together).

A second integration-hardening pass (nested structs, struct array / string
fields, fn-returns-struct, recursion + mutual recursion, the `?`-operator
chain, `Result` match, and a string-builder loop) all compiled + ran on the
first try — and surfaced a pre-existing **parser** robustness bug:
`parse_module` could spin forever on a reserved keyword used where a
declaration / statement is expected (e.g. a function named `use`). The
top-level loop now guarantees forward progress (skips the offending token)
so the compiler terminates instead of hanging; valid programs (incl. the
self-host compiler's own source — the fixpoint stays byte-identical) are
unaffected.

A third pass (struct spread-update, struct-union `match` + a method on the
bound variant, a closure capturing an array, 2-D arrays, `Option[Option]`,
string recursion, split/join round-trip, nested loops with `break`) mostly
ran first-try and turned up one real gap: **`var (a, b) = …` tuple
destructuring** wasn't lowered (the comma-encoded binding fell through to a
single bogus local). The StmtVar path now splits the two names and binds
them from the tuple's slots (`a = t.0`, `b = t.1`).

A fourth pass (continue / push-loops, nested-struct methods, wildcard
`match`, string ordering, f-string method interpolants, arrays of tuples,
struct method-chaining, early return from a loop) ran first-try and turned
up one more gap: **`const` declarations**. The parser desugars a `const` to
a zero-arg function and a bare reference is meant to lower to a call, but
the backend emitted a (non-existent) `local.get`. A bare ident naming a
zero-arg free function (not shadowed by a local) now lowers to `(call $C)`,
and the const's declared return type drives string / i64 / f64 typing so it
concats / formats / does wide arithmetic correctly.

A fifth pass (else-if chains, 3-variant unions, struct mutation through a
function, `pts[i].field = …` assignment, string-method combos) ran
first-try and found one gap: **string char-access `s[i]`** used the array
indexing formula (4-byte element at offset 8) instead of a byte load (the
string block is `[len][bytes@4]`, 1 byte each). `ExprIndex` now byte-loads
when the receiver is a string.

A sixth pass (a char-processing vowel counter, string reverse via slices)
ran first-try but turned up **three** real gaps at once: (1) **bitwise
operators** `& | ^ << >>` silently lowered to `i32.add` (missing from
`wasm_binop`) — now mapped to `i32.and` / `or` / `xor` / `shl` / `shr_s`
(and the i64 variants), with `is_int_binop` propagating i64-ness through
them; (2) **generic type arguments at call / construction sites**
(`f[i32](x)`, `Box[i32] { … }`) **hung the parser** — `parse_postfix` now
erases a `[type-args]` bracket (detected by a non-advancing `parse_expr`)
and, for a trailing `{`, parses the struct literal, and `parse_block` got
the same forward-progress guard `parse_module` has; (3) **methods with a
generic receiver** (`(b: Box[T]) m()`) mangled to `$Box[T]__m` (brackets in
the name) while call sites dispatched to `$Box__m` — the receiver type is
now `base_type_name`-normalised at mangling, registry, and the receiver
local.

A seventh pass (deep nesting, complex `while` conditions, negative-float
compares, hex / escape literals) confirmed those plus compound assignment
all work — and found that **compound assignment to an array element**
(`arr[i] += y`) silently dropped the old value (the parser desugar built
`__set_index(arr, i, y)` instead of `… arr[i] <op> y`); fixed in the
parser, so it matches the struct-field form.

**C-style enums** then closed the last known language gap. The parser
already flattens an `enum` into one zero-field struct per variant, and the
struct-union `match` machinery (with bindingless arms) already worked — the
only missing piece was the enum-constant value. `Color.Green` (a field
access whose receiver isn't a struct value but whose field names a variant
struct) now emits a variant box `[type_id]` exactly like `Green {}`, so
`match (c) { Red => … }` dispatches correctly. The heap allocator is gated
on any struct/enum declaration so the variant box can be built.

An eighth pass (a recursive linked list, negative literals) confirmed
those work and caught **three** more bugs — including two correctness ones:
(1) a statement-position call to a **void** function/method wrapped the
result in `(drop …)` of nothing (validation failure) — now emitted bare
when the callee is a registered user function/method with no return type;
(2) **`&&` / `||` weren't short-circuiting** (they lowered to eager
`i32.and` / `i32.or`, so a guard like `i < len && xs[i] > …` still
evaluated the OOB index) — now lowered to an `(if (result i32) …)` so the
right operand runs only when needed; (3) **nested lambdas** lost the inner
body because the shared lamdefs cell was read before the recursive
`emit_lambda` appended to it — fixed by emitting into a temp first.

A ninth pass caught a **boolean `match`** correctness bug: `match (b) {
true => …, false => … }` always took the first arm. Two layers were
wrong — the parser parsed the `true` / `false` *keywords* as
`PatVariant{type_name:""}` (`peek_ident` returns `""` for keywords), and
no backend emitted a comparison for those patterns. Fixed in the parser
(a `peek_keyword` fallback so the arms become `PatVariant{type_name:
"true"/"false"}`) and in every backend: wasm (`br_if` on `i32.eqz` /
the raw value), x86-64 + arm64 asm (`test` / `cmp #0` + branch to the
next arm), and the interpreter (compare against the `VBool` payload).
Verified fixpoint-clean — the compiler's own source doesn't use
bool-`match`, so the parser / native changes stay byte-identical.

A tenth pass caught a **returned-closure** bug: a function returning a
closure (`function make_adder(n: i32): fn { return function(x) {…}; }`)
worked, but binding its result — `var add5 = make_adder(5)` — then
calling `add5(37)` emitted a *direct* `(call $add5 …)` to a function
that doesn't exist. The wasm backend only treated a local as
closure-valued (`fn_names`) when it was bound to a lambda *literal*; a
local bound to a call of an `fn`-returning function was missed, so the
call site took the direct-call path instead of `call_indirect`. Fixed by
threading the set of `fn`-returning free functions
(`fn_returning_func_names`) into `collect_fn_var_names`, which now also
marks `var f = g(…)` when `g`'s return type is the coarse closure
spelling `fn`. wasm-only change (no parser / native edits), and no
self-host source function returns bare `fn`, so the fixpoint stays
byte-identical.

An eleventh pass caught a **div/rem-in-compound-literal** codegen bug: a
`/` or `%` nested inside a tuple literal (`(a / b, a % b)`), array
literal (`[100 / 4, …]`), struct-literal field value (`R { v: 84 / 2 }`),
or an index / slice position emitted a `(call $__fern_idiv …)` /
`$__fern_irem` but the guarded helper functions weren't emitted —
`module_uses_divrem` / `expr_uses_divrem` only looked through
binary / unary / call / lambda nodes, so a divide buried in any
compound-literal node went undetected and the module failed to resolve
the helper name. Fixed by recursing `expr_uses_divrem` into every
compound node (array, tuple, index, slice, struct-lit, field-access).
wasm-only change; fixpoint byte-identical.

A twelfth pass extended the returned-closure fix to **methods**: `var f =
obj.m()` where the receiver method `m` returns a closure was still lowered
as a direct `(call $f …)`. The tenth-pass fix only recognised
*free-function* calls (`fn_returning_func_names` + an `ExprIdent` callee in
`collect_fn_var_names`). Added `fn_returning_method_names` and an
`ExprFieldAccess`-callee case so `var f = obj.m()` is marked closure-valued
and flows through `call_indirect`. (Surfaced while probing with the
Go-compiler-accurate `(i32) => i32` function-type spelling — the Go
compiler rejects the bare `fn` type as non-callable, so probes must use the
precise arrow form, which the self-host coarsens to `fn`.) wasm-only;
fixpoint byte-identical. Capturing a struct *receiver* field directly
inside such a lambda (`return function(x) { return x + a.base; }`) remains a
separate, deeper capture-typing gap, tracked for a later pass.

A thirteenth pass found a **parser** gap (not a backend one): a
nested-array *type annotation* — `var grid: i32[][] = …` — dropped the
`var` binding. `parse_type_name` consumed only the first `[…]` group, so
the second `[]` was left on the cursor and the surrounding declaration
misaligned. (The nested-array literal + iteration already worked when
written unannotated, e.g. `var grid = [[1,2],[3,4]]`.) Fixed by consuming
trailing `[]` array suffixes after the first bracket group, so `i32[][]`,
`T[][][]`, `Map[K, V][]`, etc. parse to a complete type name. This is a
parser edit (shared by every backend), so the fixpoint gate is the guard;
the self-host source uses no nested-array types, so it stays
byte-identical.

A fourteenth pass closed the **struct-capture-in-lambda** gap flagged in
the twelfth: a lambda that captures a struct value and reads one of its
fields — `var f = function() { return p.x; }` over a struct local `p`, or
the method-receiver form `return function(x) { return x + a.base; }` —
emitted a bogus `(i32.const 0)` for the field read. The capture pointer
loaded fine from the env box, but the captured name wasn't struct-typed
inside the lambda, so `struct_type_of` returned "" and the field offset
couldn't be resolved. Two fixes: (1) `build_ctx` now adds the method
receiver to `all_locals` so it can be captured, and (2) `emit_lambda`
computes each capture's struct type from the enclosing scope and threads
it (`cap_sv_names` / `cap_sv_types`) into the lambda's `build_ctx`, which
registers them in `sv_names` / `sv_types`. Covers struct locals,
struct params, returned closures, and method receivers. wasm-only;
fixpoint byte-identical.

A fifteenth pass closed the **tuple-destructure-struct-typing** gap: a
`var (p, n) = t` whose tuple element is a struct left the binding untyped,
so `p.field` read a bogus `(i32.const 0)`. The destructure-emit (loading
`t.0` / `t.1` as i32 pointers) was already correct; only the *type
tracking* of the new bindings was missing. Fixed by typing each
destructured struct element via a new `tuple_elem_struct_type` helper that
resolves the element type from an inline tuple literal, a tuple-returning
free function or method (parsed from its return-type spelling), or a
tracked tuple local (a new `tup_svtypes` Ctx field, populated in statement
order during `collect_str_locals_stmts` so an intermediate
`var t = (P{…}, 1); var (p, n) = t;` works too). wasm-only; fixpoint
byte-identical.

Gated by 482 differential cases as of this writing. What remained for the
wasm backend was packaging, not language — and that packaging is now wired
into the unified `fern` CLI:

- **`fern -target wasm32-wasi -emit core-module`** emits runnable **binary** `.wasm` via the
  self-hosted WAT→binary assembler (`watbin.fern`), not WAT text.
- **`fern -target wasm32-wasi-component`** emits a **Component-Model `wasi:cli/run`**
  component, auto-selecting the framing from the program's WASI usage and
  covering every wasi:cli/run shape the self-host emit supports (no-I/O,
  stdout, filesystem read/write/rw, random, env, args, clock wall/mono,
  stderr, exit, and the fs-paired two-category combos). Unsupported WASI
  combinations are rejected with a clear error, never a broken component.

The remaining wasm shape is **`wasi:http/incoming-handler`** (native
`-target wasm32-wasi32-wasi-http`), which needs a self-host core emitter that lowers the
request/response **resource handles** — deferred until the in-progress
own/borrow resource-handle work lands.
The core wasi builtins (clock / file / env / random) are now covered.

**Binary-encoder track (started).** Slice 1 landed: `examples/self_host/
leb128.fern` — pure, import-free unsigned/signed LEB128 byte encoders
(`leb_u32` / `leb_i32` / `leb_i64`) over an `i32[]` byte buffer, the
variable-length integer encoding every wasm count / size / index /
`*.const` operand uses. Unit-tested end-to-end through the self-host wasm
pipeline against the canonical vectors (`TestSelfHostLEB128`, which
concatenates the module with a self-test driver — the module is kept
import-free precisely so it can be both imported by the future encoder
and concatenated for the test).

Slice 2 landed: `examples/self_host/wat_lex.fern` — a tokenizer for the
folded-S-expr WAT that `emit_module` produces (`(` / `)` / atoms /
`"…"` strings, whitespace discarded). This fixes the encoder's
architecture: rather than a second AST→binary backend duplicating
`wasm.fern`'s ~5000 lines of lowering, the binary emitter **assembles the
already-tested WAT** — tokenize → parse the S-exprs into module structure
→ encode with `leb128.fern` — so it rides on the proven text emitter and
only does the mechanical text→bytes mapping. Unit-tested end-to-end
(`TestSelfHostWatLex`, same concatenate-with-driver shape as the LEB128
test).

Slice 3 landed: `examples/self_host/wat_parse.fern` — a recursive-descent
S-expr parser turning the token stream into a tree of `SExpr` nodes
(`kind` list/atom/string + `items` children). Parsing one node returns it
plus the index just past it (`ParseOne`) so siblings scan cleanly. Building
it surfaced — and the prior two PRs fixed — three struct-array
element-typing gaps the recursive `SExpr { items: SExpr[] }` tree leans on:
indexed struct-array *fields*, then struct-array *params*. Unit-tested
end-to-end (`TestSelfHostWatParse`, parsing `(module (func $f))` and
asserting the nested tree).

Slice 4a landed: `examples/self_host/wat_encode.fern` — the byte-emission
primitives the module-walker builds on, over an `i32[]` byte buffer atop
`leb128.fern`: `wmagic` (the `"\0asm"` + version preamble), `wname` (a
length-prefixed name), `wsection` (id + LEB byte-length + body), `wvec`
(LEB count + elements), `valtype_byte`, and `wcat`. Unit-tested
(`TestSelfHostWatEncode`).

Slice 4b landed — **the self-host now emits runnable binary wasm**, not
just WAT text. `examples/self_host/wat_emit_bin.fern` walks the parsed
`(module …)` tree and emits a binary module: it classifies children into
the type / import / func / memory / global / export / code / data sections
(binary order), builds func / local / global symbol tables to resolve
`$name` references, and encodes folded instructions post-order (operands
then operator) with per-instruction immediate handling. This first slice
covers the shape `emit_module` produces for integer / arithmetic programs
(the two WASI imports, memory, an active data segment, the `$heap` global,
`$__fern_alloc` / `$main` / `$_start`, and the i32 const / arithmetic /
bitwise / compare / local / global / call / return opcodes). End-to-end
differential test (`TestSelfHostWasmBinary`): a program → WAT (existing
emitter) → tokenize → parse → `emit_binary` → `.wasm`, asserting `wasmtime
run prog.wasm` matches the WAT path's exit (and `wasm-tools validate`
passes). Verified on `return 42`, arithmetic, locals, subtraction, and
bitwise.

Slice 4c grew the opcode coverage to **control flow + i32 memory**:
`block` / `loop` / `if` (with `(result …)` blocktypes) / `br` / `br_if`,
and `i32.load` / `store` / `load8_u` / `store8` (natural-alignment
memargs). The interesting part is `br`/`br_if`: WAT uses named labels
(`$brk`/`$cont`) but binary uses *relative depths*, so the encoder
threads a control-frame stack (block/loop/if, including anonymous `if`
frames) and resolves each label to its depth. This surfaced a sharp
self-host bug — `arr.push` mutates the backing array in place when it has
spare capacity, so pushing an `if` frame onto the enclosing loop's stack
corrupted that loop's later `br` depths; fixed with a copy-on-push
(`labels_push`). `TestSelfHostWasmBinary` now also covers while-sum,
if-then, return-in-if-in-loop, break/continue, short-circuit `&&`, and
nested loops — each binary module matching the WAT path.

Slice 4d filled in the rest of the **i32 arithmetic / comparison set** plus
`select`: `div_s` / `div_u` / `rem_s` / `rem_u`, the unsigned comparisons
(`lt_u` / `gt_u` / `le_u` / `ge_u`), `shr_u` / `rotl` / `rotr`, and a
`select` (three stack operands). It also fixed a latent opcode bug —
`i32.shr_s` was emitted as `0x76` (which is `shr_u`) instead of `0x75`.
With i32.load/store already in, this brings **structs** into reach:
`TestSelfHostWasmBinary` adds div/rem, left/right shift, and struct field
read / mutate / nested-struct cases, each binary module matching the WAT
path.

Slice 4e added the **i64 op set + numeric conversions**, and locked in
**strings** (which `TestSelfHostWasmBinary` shows already encode once the
slice-4d unsigned-compares / `select` landed — the string runtime needs
no further opcodes). i64: `add` … `rotr`, the `i64` comparisons, plus the
conversions `i32.wrap_i64` / `i64.extend_i32_s`-`u` / `*.trunc_f64_s`-`u`
/ `f64.convert_*` (all single-byte, unary). New tests: i64 div / sub /
mul-compare (values above 2³¹ round-tripping through the 8-byte ops and
`i32.wrap_i64` on the `as i32`), and string length / index / concat /
comparison — each binary module matching the WAT path.

Slice 4f switched the differential test harness from **embedding** the
target WAT in a string literal to **`read_file`**-ing it: the assembler
(the five encoder modules + a driver) is now built once and reads the WAT
from the preopened dir, so module size no longer caps the test. This
unlocked **maps** — which need no further opcodes, but whose ~33 KB WAT
overran the embed approach — and let the test diff **stdout** too (so
`write(...)` programs are checked). `TestSelfHostWasmBinary` now also
covers `write` output, a string-builder loop, i32-keyed and string-keyed
map get, all matching the WAT path. One scaling limit surfaced: the
assembler's bump heap is 16 pages (1 MB), so a WAT past ~40 KB (e.g. a
program pulling in both the `string[]` and map runtimes at once) OOMs the
assembler — relevant when the goal becomes round-tripping the compiler's
own (large) modules, which will want the self-host to emit a bigger
`(memory …)` for such programs.

Slice 4g added **closures** — the last big structural piece. Closures
introduce named `(type $clos*)` function-type declarations, a `(table N
funcref)`, a `(elem (i32.const 0) $f …)` segment, and `call_indirect
(type $c) <args> <funcptr>`. The type section now emits the named types
first (so `call_indirect` can reference them by index) and offsets every
function's own type index past them; the table (id 4) and elem (id 9)
sections slot into binary order; and `call_indirect` pushes its args then
the table-index operand before the `0x11 typeidx 0`. (Signature parsing
was also generalised to read the unnamed `(func …)` inside a type decl.)
`TestSelfHostWasmBinary` adds a capturing closure, a closure capturing an
array, and a lambda passed as an argument — each binary module matching
the WAT path. With this the binary backend covers **everything
`emit_module` produces except f64 constants**.

Slice 4h added the **`f64_bits` / `f64_from_bits`** builtins to the wasm
backend (`wasm.fern`) — `i64.reinterpret_f64` / `f64.reinterpret_i64`.
These were a real wasm-vs-native parity gap (the native backends already
reinterpret), and they're the unblocker for `f64.const` in the binary
encoder: the encoder will parse the decimal literal to an f64 value, then
`f64_bits` it to the 8 IEEE-754 bytes (the `f64.const` immediate is raw
bytes, not a LEB). Guarded by `f64-bits-hi` / `f64-bits-roundtrip` in the
wasm differential suite, cross-checked against the Go interpreter.

Slice 4i added **f64.const + the f64 op set**, completing the core-module
encoder. `f64.const` emits `0x44` then the 8-byte little-endian IEEE-754
immediate, whose bits come from `f64_bits(parse_f64(literal))` — a small
decimal float parser (sign / int / fraction / e±exp; nearest f64 for
exactly-representable literals, ~1 ULP otherwise, which is fine for the
values `emit_module` emits and the truncate-to-int results the tests
assert). The f64 arithmetic / comparison ops and `f64.{abs,neg,ceil,floor,
trunc,nearest,sqrt,min,max,copysign}` were added to the opcode table.
`TestSelfHostWasmBinary` adds f64 mul / sub / compare / sqrt / int→float
cases. **The self-hosted binary encoder now covers the full core-module
shape `emit_module` produces** (36 differential cases, exit + stdout
matching the WAT path).

Slice 4j made `$__fern_alloc` **grow linear memory** instead of trapping
once the bump pointer passes the initial 16 pages — `memory.grow` by
`ceil((heap − capacity) / 64KiB)` pages — and taught the encoder
`memory.size` (`0x3f 0x00`) / `memory.grow` (`0x40 0x00`). This was a real
wasm-backend robustness gap (any program allocating > 1 MB trapped), and
it also lifts the assembler's own ceiling: rebuilt with the growing
allocator, the assembler no longer OOMs on large WATs, so the dropped
string[]+map word-count integration (~34 KB WAT) now assembles, and a
program allocating > 1 MB round-trips. `TestSelfHostWasmBinary` adds a
memory-grow case and the word-count capstone (38 differential cases).

Remaining is packaging, not coverage: wrap the core module in the
`wasi:cli/run` / `wasi:http` component shapes (the Component-Model binary
format — the self-host emits a WASI-preview1 command today, so this also
involves the preview1→preview2 adapter composition the Go backend uses).
*(Update: the `wasi:cli/run` packaging is now complete and wired into
`fern -target wasm32-wasi-component` — see the summary near the top of this
section. `wasi:http/incoming-handler` remains, pending resource-handle
lowering.)*

The **component-wrapper track** has started. Investigation of the Go
backend's `-target wasm32-wasi` output shows it is preview2-native: the core
module has *no* WASI imports and exports `_lang_run`, wrapped with `canon
lift` into a `wasi:cli/run@0.2.0` instance export. A component's binary
preamble is the wasm magic + `0d 00 01 00` (version 13, layer 1, vs a core
module's `01 00 00 00`), and an embedded core module is a component
section (id 1) holding that module's bytes. Slice C1 landed
`examples/self_host/wat_component.fern` — `component_preamble` +
`component_wrap(core)`, which embeds the core-module bytes in the
component envelope; `TestSelfHostWasmComponent` assembles a core module,
wraps it, and asserts `wasm-tools` validates a `(component (core module
…))`.

Slice C2 landed the **full `wasi:cli/run` component framing**
(`component_full`). Like the native compiler — which hand-rolls the
component in Go (`internal/wasm/component/component.go`) rather than
shelling out to `wasm-tools component new` — the self-host emits the
component sections directly: beyond the embedded core (section 1), the
six trailing sections (core-instance 2, alias 6, component-type 7, canon
lift 8, instance 5, `wasi:cli/run@0.2.0` export 11) are *constant* for
this fixed shape (they only reference the core's `_lang_run` export and
the `() -> result` signature), so they're emitted verbatim. Given a core
that exports `_lang_run`, `component_full` produces a component
**byte-identical to the Go backend's `-target wasm32-wasi` output**, validated
both ways: `TestSelfHostWasmComponentFull` feeds the Go backend's own core
to `component_full` and asserts byte-equality + a matching `wasmtime` run
(ok path `main()==0` → exit 0; err path → exit 1).

Slice C3 closed the loop for **no-I/O programs**: `wasm.fern` gained
`emit_module_run` (alongside `emit_module`), which emits the Component
*run* core — no preview1 scaffold, exporting `main` and `_lang_run`
(`(i32.eqz (i32.eqz (call $main)))`, so `main()==0` → ok). For programs
that use no WASI I/O the run core is import-free, so a core-instance can
`instantiate` it with no args and `component_full` wraps it directly.
This makes the whole pipeline self-hosted end to end: **source →
`emit_module_run` (preview2 core WAT) → `emit_binary` (core) →
`component_full` (component)**, producing a `wasi:cli/run@0.2.0` component
`wasmtime` runs to the right result (`TestSelfHostWasmComponentEndToEnd`:
ok path `main()==0` → exit 0; err path → exit 1; over `return 0` /
`return 42` / `x-y` / `n*7`). The `emit_module` refactor (a shared
`emit_module_mode`) is additive — the preview1 command path is unchanged
and all the core-module suites stay green.

There are two routes to **I/O in components** (a printing / file program,
which `component_full`'s import-free path can't take):

1. **Adapter route — works today.** The self-host already emits a correct
   WASI-preview1 *command* core (with `fd_write`); composing it with the
   preview1→preview2 adapter via `wasm-tools component new --adapt
   wasi_snapshot_preview1=$FERN_WASI_ADAPTER` yields a `wasi:cli/run`
   component with real I/O — no codegen change. This is exactly the
   native compiler's `-wasi-adapter` option.
   `TestSelfHostWasmComponentAdapter` compiles printing / f-string /
   loop-print programs through the self-host to a preview1 core, composes
   with the adapter, and asserts the component's stdout under `wasmtime`.
2. **Preview2-native route — now works for stdout.** The self-host emits
   the I/O directly against the preview2 interfaces, no adapter:
   - **Framing** (`component_full_io`, `wat_component.fern`): the wasi
     `io/error` / `io/streams` / `cli/stdout` type imports + the two
     canon-lower shim cores + the canon-lift / instance / export are
     constant for the stdout shape, so they're embedded verbatim as `\xNN`
     blobs (`io_prefix` / `io_suffix`) around the core — byte-identical to
     the native compiler's I/O component when given the same core.
   - **Core codegen** (`emit_module_run_io`, `wasm.fern`): a run core that
     imports `wasi:cli/stdout/get-stdout` + `wasi:io/streams/[method]
     output-stream.blocking-write-and-flush` and defines a `$fd_write`
     *shim* over the stream (cached stdout handle; an 8-aligned return
     area for the result), so every existing `fd_write` call site works
     unchanged.
   End to end — source → `emit_module_run_io` → `emit_binary` →
   `component_full_io` → a `wasi:cli/run` component that prints under
   `wasmtime`, with the right run() result (`TestSelfHostWasmComponentStdout`:
   write / `\n` / f-string / print-int / multi-write, and a non-zero-main
   err path). (Not byte-identical end-to-end — the self-host's core differs
   from native's codegen — but the same preview2 shape, adapter-free.)
3. **Preview2-native file read — `read_file` works.** A third `fs` mode
   (`emit_module_run_io_fs`, `wasm.fern`) extends the stdout core with the
   `wasi:filesystem` import set: `preopens/get-directories`,
   `types/[method]descriptor.open-at` + `read-via-stream`, and
   `io/streams/[method]input-stream.blocking-read`, plus a `cabi_realloc`
   export so the canonical ABI can grow runtime lists. `$__fern_read_file`
   is rewritten against those interfaces (get the preopened dir; `open-at`
   the path; `read-via-stream`; loop `blocking-read` 4 KiB chunks into a
   grown buffer until EOF), returning the same `[tag][payload]` Result box
   the preview1 path produced, so every `read_file` call site is unchanged.
   The framing (`component_full_io_fs`, `wat_component.fern`) embeds the
   read_file+stdout import-set's prefix/suffix as `\xNN` blobs, byte-identical
   to native's component when given the same core. End to end — source →
   `emit_module_run_io_fs` → `emit_binary` → `component_full_io_fs` → a
   `wasi:cli/run` component that reads a preopened file under `wasmtime`
   (`TestSelfHostWasmComponentReadFile`: read contents / missing-file err
   path / a 10 000-byte multi-chunk read exercising the grow loop).
4. **Preview2-native file write — `write_file` works.** The same `fs` mode
   adapts its imports to the file ops a program uses: `get-directories` +
   `open-at` are shared; read pulls in `read-via-stream` +
   `input-stream.blocking-read`; write pulls in
   `types/[method]descriptor.write-via-stream` (reusing the stdout shim's
   already-imported `output-stream.blocking-write-and-flush`).
   `$__fern_write_file` opens the path with `open-flags` create|truncate (9)
   and `descriptor-flags` write (2), `write-via-stream`s it, then loops
   `blocking-write-and-flush` over <=4 KiB chunks until the content block is
   drained, returning the same `[tag][payload]` Option box (None=success /
   Some(errcode)) the preview1 path produced. The framing
   (`component_full_io_fs_write`, `wat_component.fern`) carries the
   write import-set's prefix/suffix `\xNN` blobs (write-via-stream replaces
   read-via-stream + blocking-read), byte-identical to native's component for
   that core. End to end — `TestSelfHostWasmComponentWriteFile` (write +
   read-back / truncate-existing / a 10 000-byte multi-chunk write loop) plus
   the byte-identical `TestSelfHostWasmComponentFullIOFSWrite`. (A subtlety
   the multi-chunk case surfaced: the canonical-ABI result discriminant is a
   single byte, so the success check reads it with `i32.load8_u`, not a
   4-byte `i32.load` that would trip over stale padding in the reused return
   area — the read path's discriminant checks were tightened to match.)
5. **Preview2-native read + write together.** A program that calls *both*
   `read_file` and `write_file` (the realistic edge shape — read a config,
   write a log) needs the combined import set: `get-stdout` +
   `blocking-write-and-flush`, then `get-directories`, `open-at`,
   `read-via-stream`, `blocking-read`, `write-via-stream`. The `fs` mode
   already emits exactly that order (the imports adapt to the ops used and the
   read/write helpers are both emitted), so this slice is framing-only:
   `component_full_io_fs_rw` (`wat_component.fern`) carries the combined
   import-set's prefix/suffix `\xNN` blobs, byte-identical to native's
   component for that core (`TestSelfHostWasmComponentFullIOFSRW`). End to end
   — `TestSelfHostWasmComponentReadWriteFile` copies one preopened file to
   another (`read_file` → `write_file` → stdout marker) and exercises the
   missing-source error arm. (Native's *no-stdout* read+write shape orders the
   imports differently — `bwf` last — but the self-host always includes the
   stdout shim, so the read+write+stdout shape is the one it targets.)
   **File I/O is done** for read + write (single and combined). The remaining
   filesystem builtins (`stat` / `read_dir` / `remove_*` / `temp_dir`) have no
   preview2 path in the native `wasmbin` backend either, so there is no
   framing to mirror — they would need their component types hand-rolled from
   scratch, a separate effort from the I/O-stream parity tracked here.
6. **Preview2-native randomness — `random_bytes` works.** Beyond file I/O,
   the preview2-native (`io`) path now also draws randomness adapter-free.
   `random_bytes` is gated on `io`: the adapter route (`emit_module_run`,
   `io=false`) keeps the preview1 `random_get` import for the adapter to
   wrap, while the adapter-free route (`emit_module_run_io`) imports
   `wasi:random/random@0.2.0`'s `get-random-u64` and fills the same `i32[]`
   byte-array result one u64 (8 bytes) at a time, slicing each byte out with
   a shift+mask — so every `random_bytes` call site is unchanged. Import
   order is `get-stdout`, `blocking-write-and-flush`, `get-random-u64`
   (no `cabi_realloc` — the source is a scalar, not a list). The framing
   (`component_full_io_random`, `wat_component.fern`) is byte-identical to
   native's component for that core. Tests:
   `TestSelfHostWasmComponentFullIORandom` (byte-identical) +
   `TestSelfHostWasmComponentRandom` (length / every byte in 0..255 / a
   non-multiple-of-8 length).
7. **Preview2-native environment — `env` works.** `env(name)` is gated on
   `io` the same way: the adapter route keeps the preview1
   `environ_sizes_get` / `environ_get` pair, while the adapter-free route
   imports `wasi:cli/environment@0.2.0`'s `get-environment` (a
   `list<tuple<string,string>>` — header `(ptr, count)` into the return area,
   each tuple 16 bytes: key `{ptr@0,len@4}`, value `{ptr@8,len@12}`). The
   rewritten `$__fern_env` scans the tuples for a key byte-equal to `name` and
   returns the same `Option[string]` box (Some=tag 0 + a fresh `[len][bytes]`
   value block; None=tag 1). Because `get-environment` returns a
   host-allocated list, the core now exports `cabi_realloc` whenever the
   adapter-free path uses env (not just `fs`). Import order is `get-stdout`,
   `blocking-write-and-flush`, `get-environment`; framing
   `component_full_io_env`, byte-identical to native. Tests:
   `TestSelfHostWasmComponentFullIOEnv` (byte-identical) +
   `TestSelfHostWasmComponentEnv` (present / absent→None / a prefix var that
   must not false-match — exact key compare). (A subtlety: the
   `get-environment` return area must be aligned — its `(ptr, count)` header
   is two i32s — so `$ra` is over-allocated and rounded up to 8, the same
   trick the stdout shim and the file-I/O return areas use; an unaligned bump
   cursor traps `pointer not aligned`.)
   `args` (→ `wasi:cli/environment` `get-arguments`) and `now_unix_ms`
   (→ `wasi:clocks/wall-clock`) are the same shape and the natural
   follow-ups; the static-blob-per-import-set approach will want a generative
   component builder once the combinations multiply.
8. **Preview2-native arguments — `args` works.** `args()` follows env exactly:
   `io`-gated, the adapter-free route imports `wasi:cli/environment@0.2.0`'s
   `get-arguments` (a `list<string>` — header `(ptr, count)` into the return
   area, each string 8 bytes `{ptr@0,len@4}`) and copies each entry into a
   fresh `[len][bytes]` block stored in the same `string[]` array block
   (`[len][cap][elem-ptrs]`) the preview1 helper built — so every `args()`
   call site is unchanged. Same aligned return area (`get-arguments` writes a
   two-i32 header) and the same `cabi_realloc` export (now gated on
   `io && (has_env || has_args)`). Framing `component_full_io_args`,
   byte-identical to native. Tests: `TestSelfHostWasmComponentFullIOArgs`
   (byte-identical) + `TestSelfHostWasmComponentArgs` (count incl. argv[0] /
   the passed values in order). `now_unix_ms` (→ `wasi:clocks/wall-clock`)
   is the remaining same-shape builtin.
9. **Preview2-native clock — `now_unix_ms` / `now_ns` work.** The wall-clock
   builtins go adapter-free over `wasi:clocks/wall-clock@0.2.0`'s `now`, which
   returns a `datetime` record (`seconds: u64` @ 0, `nanoseconds: u32` @ 8)
   into an 8-aligned return area (no list → no `cabi_realloc`): `now_ns =
   seconds*1e9 + nanos`, `now_unix_ms = seconds*1000 + nanos/1e6`. The clock
   path is now emitted **precisely per builtin** (matching native): `io`-gated,
   wall-clock is imported only when `now_unix_ms`/`now_ns` is called and
   monotonic-clock (`monotonic_ns`, a u64 instant returned directly) only when
   `monotonic_ns` is — so the core's import set matches its framing. Framing
   `component_full_io_clock` (wall-clock + stdout) is byte-identical to native.
   Tests: `TestSelfHostWasmComponentFullIOClock` (byte-identical) +
   `TestSelfHostWasmComponentClock` (`now_unix_ms` is a recent epoch-ms value;
   `now_ns` > `now_unix_ms`, proving the two readings use distinct math). The
   monotonic clock shape is also wired: `component_full_io_clock_mono`
   (`monotonic-clock` + stdout; `now` returns a u64 instant directly, no
   record / no `cabi_realloc`), byte-identical to native, tested by
   `TestSelfHostWasmComponentFullIOClockMono` +
   `TestSelfHostWasmComponentClockMono` (a second read after a busy loop is
   `>=` the first — monotonic never goes backwards).

   **The full preview1-builtin surface now has an adapter-free preview2 path**
   in the self-host: stdout, `read_file`, `write_file` (+ combined),
   `random_bytes`, `env`, `args`, and both clocks. With these single-import
   framings embedded as `\xNN` blobs, the next step is **combinations** (a real
   edge handler reads a config file *and* env secrets, etc.).
10. **Canonical import order → combinations unlocked.** The self-host now emits
   its preview2 wasi imports in the **native compiler's canonical interface
   order** — `get-stdout`, `blocking-write-and-flush`, then random, wall-clock,
   monotonic-clock, args (`get-arguments`), env (`get-environment`), and the
   filesystem chain (`get-directories`, `open-at`, `read-via-stream`,
   `blocking-read`, `write-via-stream`). Single-import shapes are unaffected
   (a lone import still lands right after the stdout pair, so every existing
   byte-identical framing test still passes), but a *combination* core now
   wires up byte-identically to native — which means a combination's framing
   is just native's captured prefix/suffix, exactly like the singles. First
   combination landed: `read_file` + `env` + stdout
   (`component_full_io_fs_read_env`) — the canonical edge-handler shape (read a
   config file, read env secrets, respond). Tests:
   `TestSelfHostWasmComponentFullIOFSReadEnv` (byte-identical to native) +
   `TestSelfHostWasmComponentReadEnv` (config + secret; missing-env fallback).
   The richest realistic edge shape also landed: `read_file` + `write_file` +
   `env` + stdout (`component_full_io_fs_rw_env`) — read a config file, read
   env secrets, write an output/log file, respond — tested by
   `TestSelfHostWasmComponentFullIOFSRWEnv` (byte-identical) +
   `TestSelfHostWasmComponentReadWriteEnv` (read→transform-with-env→write).
   Remaining combinations (random+write, args+fs, clock+fs, …) are now each a
   capture-the-blob-and-test slice; the **generative component builder**
   (mirroring `internal/wasm/component/component.go`, computing the
   lift/lower/instance/canon wiring from the import set) remains the eventual
   collapse of the blob set, but is no longer blocking real edge programs.
   It now has a concrete design + phased, byte-identical-validated
   implementation plan: see `docs/WASM-COMPONENT-GENERATOR.md`.

The core encoder was also **validated at scale**: beyond the per-feature
cases, `TestSelfHostWasmBinary` round-trips substantial multi-feature
programs through the binary path (deep recursion — `fib`; a struct-array
"linked list" walked by index; a string `split` + iteration; a
string-keyed count map), each binary module matching the WAT path. 41
differential cases in total.

The at-scale probe also turned up a self-host **parser** gap (and fixed
it): a parenthesized type followed by `[]` — a tuple array `(i32, i32)[]`
or a closure array `(() => i32)[]` — left the trailing `[]` on the cursor
after `parse_type_name`'s paren branch, so the `var`'s binding local was
never declared ("unknown local $ps"). `parse_type_name` now consumes the
trailing `[]` (`consume_array_suffix`), which fixes **array-of-tuples**
end-to-end (Go already handled it; now the self-host does too — index,
`.0`/`.1`, and `for p in ps`). A follow-up then made **closure arrays**
work fully too: the paren reader treats a `=>` *inside* the parens as a
function type (coarsed to `fn`, so `(() => i32)[]` → `fn[]`), and
`wasm.fern` tracks `fn[]` locals/params (`collect_fn_arr_names`) so a
closure read from an element — `var c = fns[i]` or `for f in fns` — is
itself callable through `call_indirect`. `var c = fns[0]; c()`,
`for f in fns { f() }`, and `((i32) => i32)[]` with args all run and match
the Go compiler. (The bare `fn` type stays intentionally opaque — not
callable; doesn't accept a concrete lambda on return.) Both fixes are in
the fixpoint bundle and the self-compile still converges byte-identically.

A sixteenth pass found one remaining **language** gap (so the "what
remains is packaging, not language" claim above is not yet absolute):
**`i64[]` arrays whose elements exceed 32 bits** on the *compiled*
backends. The wasm / native array representation uses a fixed **4-byte
element slot** (`[len:i32][cap:i32][elems: 4 bytes each]`), so an `i64[]`
element is stored / loaded as an i32. Small i64 values (< 2³¹) happen to
round-trip — `var xs: i64[] = [10, 20, 30]` sums correctly — but a
literal that doesn't fit in i32 is emitted as an out-of-range
`(i32.const 5000000000)` and the wasm module fails to load; a large value
read back is truncated. The Go compiler stores `i64[]` elements in 8-byte
slots (`i64.store` / `i64.load`), so this is a genuine parity gap.

(The self-host **interpreter** is *not* affected by element width — it
uses a `Value` model, not raw byte slots — but it has its own i64 ceiling:
`VInt` is i32-backed, so a literal above 2³¹ truncates there regardless.
A separate bug, since fixed, made it *look* like an array problem: the
interpreter's unary evaluator errored on every `as` cast, so any test that
narrowed an i64 with `… as i32` returned garbage. See the seventeenth
pass.)

Element width is baked into the literal emit, index read, `__set_index`,
the `for` loop, `__fern_arr_push` / `__fern_arr_slice`, and the alloc
size. Only the **wasm** backend was on the broken 4-byte path; the
eighteenth pass moved it to 8-byte slots. **The x86-64 and arm64 native
backends were already correct** — they store 64-bit elements in 8-byte
slots and large values round-trip (verified directly; see the nineteenth
pass, which added the missing native regression tests). So with the wasm
fix this language-parity gap is **closed across all compiled backends**.
(`f64[]` shares the same 8-byte slot. The self-host *interpreter* still
has its separate i32-backed `VInt` ceiling, noted above — that is a
value-model limitation, not an array one.)

A seventeenth pass fixed the **interpreter `as` cast** bug surfaced while
investigating the above: `interp.fern`'s unary evaluator only handled `-`
and `!`, so every `expr as <Type>` (desugared to the unary op
`as_<Type>`) fell through to `v_err("unknown unary op")`. Any program that
narrowed/widened a number — e.g. returning an i64 computation from `main`
via `… as i32` — errored out. Added integer- and float-target cast cases
to the evaluator (integer target: identity on a `VInt`, truncate a
`VFloat`; float target: widen a `VInt`, identity on a `VFloat`), matching
the interp's i32 / f64 value model. Guarded by four new cases in
`TestSelfHostInterpDriverX86_64` (`cast-i64-to-i32`, `cast-f64-to-i32`,
`cast-i32-to-f64`, `cast-in-i64-array-sum`), cross-checked against the Go
interpreter. Interpreter-only change; the native compiler fixpoint does
not compile `interp.fern`, so it is unaffected.

An eighteenth pass landed the first leg of the 8-byte-slot series: the
**wasm backend now stores `i64[]` / `f64[]` elements in 8-byte slots**
with `i64`/`f64` load/store. A new `array_elem_kind` classifier (backed by
`i64_arr_names` / `f64_arr_names` Ctx sets, seeded from `i64[]`/`f64[]`
params + var decls + wide-array-returning calls/slices) drives the stride
(`elem_slot_size`) and op (`elem_load_op` / `elem_store_op`) at every
element site: the literal emit (`emit_array_literal_kind`, taking the
declared kind so a bare-integer `i64[]` literal stores wide), the index
read (added `ExprIndex` cases to `emit_i64` / `emit_f64`, with
`is_i64_expr` / `is_f64_expr` routing wide reads there), `__set_index`,
the `for` loop (with `collect_wide_loop_vars` typing the loop var so the
8-byte load lands in an i64/f64 local), `.push()` (new `$__fern_arr_push_i64`
/ `$__fern_arr_push_f64`), and slice (new `$__fern_arr_slice8`, a raw
8-byte move). Values above 2³¹ now round-trip. Guarded by 8 new
differential cases (`i64arr-*`, `f64arr-for-sum`, plus an `i32arr` mix to
pin no-regression), all cross-checked against the Go compiler; the full
self-host suite (incl. fixpoint) stays green.

A nineteenth pass confirmed the other two compiled backends needed **no
change**: the x86-64 and arm64 emitters already store `i64[]` / `f64[]`
elements in 8-byte slots, so values above 2³¹ round-trip through literal,
index, `for`, `__set_index`, push, and slice (verified end-to-end — x86-64
natively, arm64 under qemu). That behaviour was previously **untested**,
so the pass added 6 native cases each to `TestSelfHostAsmRunX86_64` and the
arm64 emit suite (`arr-i64-literal-index-large`, `-for-sum`,
`-set-index-large`, `-push-grow`, `-slice`, `arr-f64-for-sum`),
cross-checked against the Go compiler. With wasm fixed (pass 18) and the
natives guarded, the `i64[]` / `f64[]` language-parity gap is closed on
every compiled backend.

### ✅ UPDATE (2026-06): self-hosted arm64-darwin (Mach-O) target

`fern -target arm64-darwin` now emits arm64-apple-darwin assembly that
clang + ld64 / lld link into a Mach-O executable on Apple Silicon —
closing the one native backend the self-host had that the Go compiler
already supported. It reuses `asm_arm64.fern`'s instruction selection
verbatim; a post-pass, `asm_arm64.darwinize`, reskins the GAS output
into the Mach-O dialect:

- PC-relative addressing `adrp X, sym` / `add X, X, :lo12:sym` →
  `adrp X, sym@PAGE` / `add X, X, sym@PAGEOFF` (the identical ADRP/ADD
  relocation pair, Apple syntax);
- sections `.section .rodata` → `__TEXT,__const`, `.bss` →
  `__DATA,__bss`; ELF-only `.type` / `.size` / `.note.GNU-stack` dropped;
- entry `_start` → `_main` (ld64 / lld's default, invoked via LC_MAIN);
- the number-compatible syscalls (read / write / close / openat / lseek /
  exit / mmap) remapped to the Darwin BSD vector with `svc #0x80`.

Mirrors the Go backend's `EmitWithOptions{Darwin}` branch one-for-one.
Supported surface is the **core language** (arithmetic, control flow,
functions, structs, enums, arrays, strings incl. heap concat, closures,
maps, stdout/stdin I/O).

Two kinds of Darwin divergence, handled in two places:

1. **Error convention** (all fallible syscalls): Darwin returns `+errno`
   with the carry flag set; Linux returns `-errno`. `darwinize` injects
   `b.cc <lbl>; neg x0, x0; <lbl>:` after each remapped `svc #0x80` so the
   self-host's `x0 < 0` checks see Linux-shaped `-errno`. (`exit` skipped.)
2. **Structural / constant differences** (struct layout, chunking, flag
   constants) — emitted directly by the code generator via a `darwin`
   flag threaded into `asm_arm64.emit_module`, since the lexical post-pass
   can't restructure them.

Ported so far via the flag:

- `now_unix_ms` → `gettimeofday` (BSD 116; Darwin has no `clock_gettime`
  syscall), reading `struct timeval`'s `tv_usec` and dividing by 1000;
- `random_bytes` → chunked `getentropy` (BSD 500, 256-byte cap per call,
  no flags arg) in place of Linux `getrandom`;
- `read_file` + `write_file` — number-compatible syscalls
  (openat / lseek / read / write / close; read_file sizes via `lseek`,
  not `fstat`, so no struct-stat dependency) plus the right open-flag /
  `AT_FDCWD` constants (`O_WRONLY|O_CREAT|O_TRUNC` is 1537 on Darwin vs
  577 on Linux; `AT_FDCWD` is -2 vs -100) and errno normalisation, so
  both succeed *and* report errors correctly.
- `stat` / `FileStat` — `newfstatat` (#79) -> Darwin `fstatat` (#470,
  same args) + the `struct stat` field offsets (`st_mode` is a u16 at
  offset 4 / `st_size` an i64 at offset 96 on Darwin's 64-bit-inode
  stat, vs u32@16 / i64@48 on Linux arm64). `S_IFMT`/`S_IFREG`/`S_IFDIR`
  are the same POSIX constants. is_file / is_dir / size all read back
  correctly.
- `remove_file` — `unlinkat` (#35) -> Darwin #472 (same args, flags 0)
  + `AT_FDCWD` -2 + errno normalisation.
- `monotonic_ns` — no syscall: reads the architectural counter
  `CNTVCT_EL0` scaled by `CNTFRQ_EL0` to ns (this is what
  `mach_absolute_time` does on Apple Silicon). EL0-readable; the Go
  backend never ported this, so the self-host is ahead here.
- `temp_dir` — `mkdirat` (#34 -> Darwin #475) + `AT_FDCWD` -2; its unique
  suffix comes from the now-ported `monotonic_ns`.

Still on their Linux encoding (out of scope, documented gaps): the
directory-enumeration builtins (`read_dir` / `remove_dir_all` use
`getdents64`, whose Darwin equivalent `getdirentries` has a different
`dirent` layout — the genuinely structural one) and the subprocess
family. These are emitted
but only misbehave if a program actually calls them on Darwin.

Gated by `internal/e2e/self_host_macho_test.go` — cross-links each
program with clang + lld and asserts a valid arm64 Mach-O executable off
Apple Silicon, and on the macOS arm64 CI runner builds the self-host CLI
natively, runs it, and **executes** the emitted Mach-O (incl. the
`now_unix_ms` / `random_bytes` / `read_file` / `write_file` cases)
asserting exit codes — plus the `darwinize` + Darwin-builtin self-test
cases in `asm_arm64.fern`'s own `main()`.

### ~~Hard blocker — no interface / union-of-struct polymorphism~~ — RESOLVED

Originally the audit's main blocker. Landed in PR #390
(core) + #392 (implicit struct → union wrap):

```
struct Add { l: i32, r: i32 }
struct Lit { v: i32 }
type Expr = Add | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var e: Expr = Add { l: 1, r: 2 };  // implicit wrap
    return eval(e);
}
```

Implementation is sugar over the existing enum machinery —
the checker desugars `type X = A | B | C` to a synthesised
`enum X { A(A), B(B), C(C) }` before the rest of the
pipeline runs. The implicit wrap lets bare struct literals
flow into union positions without `Member(...)` ceremony.

Punted to follow-ups (don't block self-hosting in practice):
- Generic union members (`type Tree[T] = Leaf[T] | Node[T]`)
- Qualified variant references (`Expr.Add(...)`)

**Minimal fix**: union-of-structs sugar over the existing
enum machinery. Each `type Expr = A | B | C` lowers to an
auto-generated enum with one variant per struct type, and
`match expr { A(a) => ..., B(b) => ... }` works as today.
Type-system extension; ~1-2 weeks design + implementation.

### Major gaps (workarounds heavy)

1. **Process spawning** — the *self-hosted* native path still
   invokes `clang` / `lld` / `as` / `ld` to assemble + link its
   emitted `.s` text (see Part 3). The **goal is zero external
   tools**: the fern-port should emit ELF / Mach-O / wasm bytes
   directly, the way it already does for wasm. This is no longer a
   research question — the Go bootstrap already does it in-process
   (`internal/native/{x86_64,arm64,elf,macho}`), so the work is a
   port, not an invention. Tracked as the **native-binary track**
   in `SELF-HOST-REMAINING-PLAN.md` (the ELF-64 writer landed first
   as `examples/self_host/elf.fern`). Only if that proves
   impractical do we fall back to a thin Go link bootstrap.

2. **No sort / no custom comparators**. Compiler sorts
   small lists (field offsets, map sizes). Inline the sort
   bodies or add `(arr: T[]).sort_by(cmp)` to the prelude
   once first-class function args to generic functions
   work cleanly.

### Minor gaps (workarounds cheap, ~50-200 lines each)

- **No `u64` Map keys** — only `i32` and `string` today.
  Re-key to strings or add a u64-keyed variant.
- **No char classifiers** (`is_digit`, `is_letter`,
  `is_hex`) — add to prelude.
- **No `os.Stat` equivalent** — use `read_file()`'s error
  arm to probe existence.
- **No sprintf-style formatter** — ~~concat with `+`, or
  add a minimal `format(fmt, args[])` to prelude.~~ Resolved:
  `format(fmt, args: string[])` landed in the prelude.
  Walks the template byte-by-byte and substitutes each `{}`
  placeholder with the next `args[i]`. Callers pre-stringify
  via `.to_string()`. Underflow (more `{}`s than args) emits
  literal `{}` so the bug surfaces at runtime; overflow
  (more args) silently drops extras. Mirrors Python
  `str.format()` / Rust `format!()` minimal subset.
- **No regex** — hand-coded byte scans (the prelude
  already does this in `trim` / `split` / etc.).

### Realistic first-port milestone

The lexer. ~400 Go LOC → ~600 Fern LOC. Pure procedural,
produces simple Token structs, validates string indexing /
slicing / struct-lit creation under real workload.
**Estimated 1 week.**

Then parser (~2 weeks; validates recursive descent +
recursive struct types + pattern matching), then a stub
checker (~1 week; symbol-table over `Map[string, Symbol]`),
then IR emission (~3 weeks).

**Full lexer → IR on wasm**: 4-6 weeks. Union types (PRs
#390 + #392) landed the AST visitor pattern; closures on
every backend (already in place) cover the small visitor-
callback usage. Full compiler self-host on wasm: 6-9 weeks.
Native self-host (arm64-linux / x86-64) is now equally
viable since closures-with-captures lower on every target.

---

## Part 3 — External tools: where they're still used, and the plan to remove them

**Goal: a fully self-hosted toolchain that needs no external tools at
all** — the compiler should emit runnable ELF / Mach-O / wasm bytes
directly. Status by half:

- **wasm: done.** Neither the Go backend nor the self-host shells out
  anymore. `wasm-tools` is no longer a build dependency (see Part 2): the
  binary encoder + Component-Model framing live in-tree in Go
  (`internal/wasm/*`) **and** in Fern (`leb128.fern` → `wat_lex.fern` →
  `wat_parse.fern` → `wat_encode.fern` → `wat_emit_bin.fern` →
  `wat_component.fern`). `fern -target wasm32-wasi` produces a runnable binary
  with no external process.
- **native (x86-64 / arm64 ELF, arm64-darwin Mach-O): split.** The **Go
  bootstrap already emits these in-process** —
  `internal/native/x86_64` + `internal/native/arm64` (text-asm → machine
  code), `internal/native/elf` (ELF-64 writer), `internal/native/macho`
  (Mach-O writer + ad-hoc code signing). So the default `fern` build needs
  no `as`/`ld` for native targets either. The **self-hosted** compiler,
  however, still only emits `.s` **text** (`asm.fern` / `asm_arm64.fern`)
  and relies on `clang` / `gcc` / `as` / `ld` / `lld` to assemble + link.

So the remaining external-tool dependency is **only on the self-hosted
native path**, and closing it is a *port* of the four Go `internal/native`
packages into Fern — mirroring exactly how the wasm binary track was
ported. This is the **native-binary track**, tracked in
`SELF-HOST-REMAINING-PLAN.md`; the ELF-64 writer landed first as
`examples/self_host/elf.fern` (`TestSelfHostELF`). The subsections below
document the external tools the self-host *currently* shells out to (and
the `-cc`/`-fuse-ld` paths the Go backend still offers as an option) until
the port completes.

### clang (or `aarch64-linux-gnu-gcc` / `x86_64-linux-gnu-gcc`)

**Used at**: `cmd/fern/main.go:311,347` via `exec.Command`.

The Fern compiler does **not** include an assembler. After
emitting `.s` it shells out to a C toolchain driver, passing
flags like `-static -nostdlib -s prog.s -o binary`. Clang /
gcc:

1. Run the assembler (`as`) to turn `.s` into a relocatable
   object file. Reading text asm and emitting machine code
   for the target ISA is non-trivial — even a minimal
   assembler is a few thousand lines per architecture.
2. Run the linker to resolve symbols, lay out sections,
   write the ELF / Mach-O headers, and patch in start /
   exit code.
3. Drop libc, crt0, and libgcc with `-nostdlib` because the
   compiler provides its own `_start`, syscall wrappers,
   allocator, and string runtime in the prelude.

The choice between `clang` and the cross `*-linux-gnu-gcc`
toolchains is just driver preference — the actual work
(assembling + linking) is identical. clang is used for
arm64-darwin specifically because of point 2 below.

### lld (LLVM linker, Mach-O backend)

**Used at**: `cmd/fern/main.go:304` via `-fuse-ld=lld`.

Only relevant for **cross-compiling to arm64-darwin from
Linux**. On a native macOS host, clang's default linker is
ld64 (Apple's) and you don't need lld. On a Linux CI box
producing Mach-O binaries for the macOS CI runner, you
need a linker that understands Mach-O object files, dylib
linkage, and the dyld stub layout. ld64 isn't packaged for
Linux; lld is. The flag combination is:

```
clang --target=arm64-apple-darwin -fuse-ld=lld -nostdlib \
      -Wl,-arch,arm64 prog.s -o binary
```

On the native macOS path (`cmd/fern/main.go:300`) the
arguments are different — `-nostdlib -lSystem` — because
Apple's ld64 requires the libSystem.dylib stub for dynamic
linkage, even when we link nothing FROM libSystem.

### wasm-tools *(no longer a toolchain dependency)*

**Historical.** The wasm backend used to shell out to
`wasm-tools` in three subprocess calls (`parse` →
`component embed` → `component new --adapt
wasi_snapshot_preview1=ADAPTER`) to turn the emitted core
module into a **Component Model** artifact — the shape
edge-function runtimes (`wasmtime serve`, Fastly Compute,
Cloudflare workerd, etc.) consume.

That pipeline is gone. The "10K+ lines of work" this section
once argued against — a native binary wasm encoder, the
Component Type encoder, and the preview-1 → preview-2 adapter
— now lives in-tree under `internal/wasm/*` (`encode`,
`componenttype`, `component`, plus the `leb128` / `sections`
/ `inst` / `imports` / `memory` / … building blocks).
`fern -target wasm32-wasi` / `-target wasm32-wasi32-wasi-http` compose components
natively in Go (`component.ClassifyCore` → `component.Compose`,
see `cmd/fern/main.go`), with no `wasm-tools` shell-out and no
preview-1 adapter. The only external process the toolchain
still spawns is the linker (and qemu under `--run`) — building
and running wasm output needs neither `wasm-tools` nor the
adapter. A handful of e2e tests still call `wasm-tools print`
to inspect a composed component, but that's a test-only
convenience, not a build dependency.

### The native-binary track: emitting ELF / Mach-O directly

The earlier framing of this section — "to produce a binary the
compiler MUST invoke at least the linker; that's too much code to
bring into the language" — is **superseded**. It is not too much
code, and the proof is in-tree: the Go bootstrap already assembles
and links natively, with no external process, via
`internal/native/{x86_64,arm64,elf,macho}`. Symbol resolution +
section layout + ELF/Mach-O header writing for a single
`-static -nostdlib` blob (no archives, no shared objects, no
external relocations, no PLT/GOT) is a few hundred lines per piece,
not a research project. The wasm half already made the same jump
(`internal/wasm/*`, mirrored in the self-host).

So the plan is to **port those four Go packages into Fern** so the
self-hosted compiler emits native binaries directly, exactly as it
already emits binary wasm. Slices (tracked in
`SELF-HOST-REMAINING-PLAN.md`), each independently testable through
the wasm self-test harness like the LEB128 / wat_encode slices:

```
[fern source] → [compiler in fern]
              → [emit machine-code bytes  (asm.fern bytes / asm_arm64.fern bytes)]
              → [wrap in ELF / Mach-O     (elf.fern / macho.fern)]
              → [write 0o755 file]        → [runnable binary, no external tool]
```

1. **ELF-64 writer** — `examples/self_host/elf.fern` (**landed**,
   `TestSelfHostELF`): the container half, mirroring
   `internal/native/elf/elf.go`. x86-64 + arm64 Linux, `R+X` and
   `R+W+X` single-PT_LOAD images.
2. **x86-64 assembler** — Intel-syntax text → machine code (mirror
   `internal/native/x86_64/`, incl. the SSE / x87 float paths).
3. **arm64 assembler** — GAS text → AArch64 bytes (mirror
   `internal/native/arm64/asm.go` / `gas.go`).
4. **Mach-O writer + ad-hoc signature** — `examples/self_host/macho.fern`
   (**landed**, `TestSelfHostMachO`): the arm64-darwin container half,
   mirroring `internal/native/macho/` incl. `sign.go`. Static, non-PIE
   `__PAGEZERO`/`__TEXT`/`__DATA`/`__LINKEDIT` image with an
   `LC_UNIXTHREAD` entry and a mandatory ad-hoc `LC_CODE_SIGNATURE` (Apple
   Silicon refuses unsigned binaries). Carries a self-contained SHA-256
   (no stdlib here) for the CodeDirectory page hashes.

With 1–4 in Fern, the self-host wires `asm.fern` → assembler →
`elf.fern` → file (and the arm64 / Darwin equivalents), and the
`clang` / `lld` / `as` / `ld` shell-outs go away. (`disasm.fern`,
which used to double as a cross-check for an emitted-bytes
assembler, was retired in #4392 along with the bytecode VM it
disassembled; a future in-Fern assembler cross-check would need
its own disassembler over the new emitted-bytes format.)

(`wasi:system/process` is no longer on the critical path: with the
in-Fern assembler + writer there is nothing left to spawn. A WASI
self-host running under wasmtime would write the binary through its
normal file I/O.)
