# Roadmap: tech debt + self-hosting readiness

Captures the state of the codebase as of 2026-05-15. Combines
two audits — current tech debt (what's wrong / behind / risky)
and self-hosting readiness (what's missing to rewrite the
compiler in lang itself). Companion to `BACKEND-PARITY.md`,
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
Self-hosting means rewriting it in lang itself and
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
byte-identical. `examples/self_host/asm_file_run.fern` is the simpler
single-file CLI shape (compile one file by path).

### ✅ UPDATE (2026-06): unified `fern` CLI + a self-hosted wasm backend

Two things landed on top of the native (x86-64 / arm64-linux) self-host
since the notes above:

**Unified `fern` CLI** (`examples/self_host/fern.fern`). Where the
codebase previously had a dozen single-mode `*_run.fern` shims, there is
now one self-hosted binary that parses argv flags and dispatches —
`fern [-check | -interp | -fmt] [-target x86-64|arm64|arm64-darwin|wasm]
[-o OUT] <entry.fern> [stdlib-root]` — reusing the import-driven file
loader.
It hosts both native emitters (`asm.fern` + `asm_arm64.fern`) plus the
checker, interpreter, printer, and the wasm emitter, selected at runtime.
Gated by `internal/e2e/self_host_cli_test.go`.

**A third self-host backend: wasm** (`examples/self_host/wasm.fern`,
driven by `wasm_run.fern` and `fern -target wasm`). It emits a WASI core
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

Gated by 482 differential cases as of this writing. What remains for the
wasm backend to retire the Go wasm path is packaging, not language: the
**`wasi:cli/run` / `wasi:http` component shapes** (the Component-Model
packaging in `internal/wasm/component`, ported to Fern, on top of this
core module), and binary wasm encoding
(today it emits WAT text, runnable directly by
`wasmtime`).
The core wasi builtins (clock / file / env / random) are now covered.

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

1. **Process spawning** — the compiler invokes `clang` /
   `lld` / `wasm-tools` (see Part 3). Lang has no `exec`
   and WASI's `wasi:system/process` proposal isn't
   finalised. **Path forward**: keep a thin Go bootstrap
   that handles linking only, and let the fern-port emit
   asm / WAT. Standard self-hosting pattern (TinyCC and
   others do this).

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

The lexer. ~400 Go LOC → ~600 lang LOC. Pure procedural,
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

## Part 3 — Why we need clang, lld, and wasm-tools

The lang compiler emits **text** at every backend — `.s`
assembly for arm64-linux / arm64-darwin / x86-64-linux, and
`.wat` (WebAssembly Text Format) for wasm. Producing a
runnable binary from that text requires external tools.

### clang (or `aarch64-linux-gnu-gcc` / `x86_64-linux-gnu-gcc`)

**Used at**: `cmd/fern/main.go:311,347` via `exec.Command`.

The lang compiler does **not** include an assembler. After
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
`fern -target wasm` / `-target wasi-http` compose components
natively in Go (`component.ClassifyCore` → `component.Compose`,
see `cmd/fern/main.go`), with no `wasm-tools` shell-out and no
preview-1 adapter. The only external process the toolchain
still spawns is the linker (and qemu under `--run`) — building
and running wasm output needs neither `wasm-tools` nor the
adapter. A handful of e2e tests still call `wasm-tools print`
to inspect a composed component, but that's a test-only
convenience, not a build dependency.

### Why a self-hosted compiler needs process spawning

To produce a binary, the compiler MUST invoke at least the
linker — symbol resolution + section layout + executable
header writing is too much code to bring into the language
runtime today. For wasm targets you additionally need the
component-model pipeline.

The standard self-hosting workaround is to keep a thin
external orchestrator that handles ONLY the link step:

```
[lang source] → [lang compiler in lang]
              → [.s text + temp file write]
              → [thin Go bootstrap calls clang / lld / wasm-tools]
              → [runnable binary]
```

The "compiler in lang" part covers lex / parse / check /
monomorph / closureconv / IR / codegen — ~95% of the
codebase by line count. The bootstrap stays in Go (or a
shell script) and is ~50 lines: read asm from a temp file,
exec clang, exit-code propagation. Many self-hosting
compilers settle here permanently (TinyCC, Roc, Zig's
self-host bootstrap before it got `std.process.spawn`).

If WASI ever lands a stable process-spawn API
(`wasi:system/process`, currently proposed but unstable)
the bootstrap could move into the lang runtime too. Until
then, the bootstrap is the pragmatic answer.
