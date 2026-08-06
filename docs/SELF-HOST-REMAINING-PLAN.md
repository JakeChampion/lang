# Self-hosting: remaining work plan

> **Status: largely complete — kept as the goal-1 historical record.** The
> per-function IR subset this plan set out to widen is now mature (closures
> incl. nested, matches incl. guards, generics, `dyn` traits, `try`/`?`,
> tuples, iterators all lower). The remaining AST fallbacks are the
> merged-bundle budget ([#3425](https://github.com/JakeChampion/lang/issues/3425))
> and the wasm-IR component-model exclusions
> ([#4316](https://github.com/JakeChampion/lang/issues/4316)–[#4320](https://github.com/JakeChampion/lang/issues/4320)).
> The old coarse tracker #2857 is closed — open threads live in those issues,
> the checker residue ([#4363](https://github.com/JakeChampion/lang/issues/4363)),
> and the roadmap umbrella ([#4368](https://github.com/JakeChampion/lang/issues/4368)).
> Verify against the issues, not the inline ⬜ markers below.

Tracks the modules / features the self-hosted compiler
(`examples/self_host/`) cannot yet compile, with a concrete plan per
item. Ordered roughly easiest → hardest. Each item ships as its own PR
with x86-64 + CI-gated arm64 tests, cross-checked against the Go backend,
and must keep the self-hosting fixpoint byte-identical.

Status legend: ✅ done · 🔧 in progress · ⬜ not started.

Already landed this effort (for context): the convergent fixpoint on both
backends; `read_file`/`args`/`write`; `as` casts; bitwise/shift; byte-array
builtins + index-assignment; `random_bytes`; `f64` floats (both backends);
tuple type annotations; the import-driven file-loading driver; the
self-hosted type checker; and real `std/io`, `std/hex`, `std/base64`,
`std/math`.

**Update (2026-06):** beyond the native (x86-64 / arm64-linux) work
tracked below, two larger pieces have since landed — see the matching
section in `ROADMAP-AND-SELF-HOSTING.md` for detail:

- a **unified self-hosted `fern` CLI** (`examples/self_host/fern.fern`)
  with `-target` / `-check` / `-interp` / `-fmt` / `-o`, replacing the
  one-mode `*_run.fern` shims (gated by `self_host_cli_test.go`); and
- a **self-hosted wasm backend** (`examples/self_host/wasm.fern`,
  `fern -target wasm`) that emits a runnable WASI core module (WAT) and
  compiles the full non-generic core language — integers (non-trapping
  div/rem), control flow, recursion, the string library, i32/string
  arrays, structs + methods, Option/Result + `match`/`?`, struct-union
  match, generics-by-erasure, the wasi runtime builtins (`env`,
  `random_bytes`, `args`, `read_file` / `write_file`, and the clocks
  `now_unix_ms` / `monotonic_ns` / `now_ns` via `clock_time_get`), a
  real i64 value path (i64 locals / params / returns, i64 arithmetic +
  comparison with guarded 64-bit div/rem, and an i64 formatter for the
  64-bit clock timestamps), an f64 floating-point path (f64 locals /
  params / returns, arithmetic + comparison, `as` casts f64↔i32/i64, and
  the primitive math builtins), and maps (`Map { … }` literals with
  `.set` / `.get` / `.get_or` / `.has` / `.len` over an open-addressing
  hash runtime, both i32 and string keys, i32 and string values, plus
  `.delete` via tombstones, `.keys` / `.values` snapshot arrays, and
  `for (k, v) in m` pair iteration — the map surface is complete), and
  slices `x[a:b]` (string + array, preserving the source element type),
  tuples `(a, b, …)` with `.N` access + per-element kind tracking, and
  lambdas + capturing closures (a `[table_idx, caps…]` box + `call_indirect`
  through a function table; free locals are captured by value into the box
  and read via `$__env`; `fn`-typed params dispatch the same way) — the
  core language is now complete on the wasm backend — plus `.to_string()`
  + f-strings (integer→string runtime) and arrays of structs (`for p in
  pts` / `pts[i].field`, struct spread-update, struct-union match + method,
  2-D arrays, `var (a, b) = …` tuple destructuring, `const` declarations,
  string char-access `s[i]`, bitwise operators `& | ^ << >>`, and generics
  with explicit type args (`f[i32](x)`, `Box[i32] { … }`, `(b: Box[T]) m()`),
  compound assignment incl. `arr[i] += y`, and C-style `enum` values +
  `match`). Gated by 455 differential cases under `wasmtime`
  (`self_host_wasm_emit_test.go`), including integration capstones (word
  count; reduce over an `fn` param; struct-method loop; nested structs;
  `?`-chains; `Result` match; string-builder). Hardening passes also fixed
  a parser hang on a reserved keyword misused as an identifier
  (`parse_module` / `parse_block` now guarantee forward progress), `var (a,
  b)` destructure lowering, `const` references (bare ident → call, typed by
  the const's return type), string char-access `s[i]` (byte load), bitwise
  operators, generic type-argument erasure + generic-receiver method
  mangling, `arr[i] += y` compound assignment, C-style `enum`
  constants (`Color.Green` → a variant box, reusing struct-union `match`),
  void-function statement calls, short-circuit `&&` / `||`, and nested
  closures. The full core language is supported.

  **Update (wasm packaging — now wired into the unified `fern` CLI).** What
  this section called "remaining for wasm" is largely done:
  - `fern -target wasm-bin` emits a runnable **binary** `.wasm` (the
    self-hosted WAT→binary assembler, `watbin.fern`), not WAT text.
  - `fern -target wasm-component` emits a **Component-Model** `wasi:cli/run`
    component, auto-selecting the framing from the program's WASI usage:
    no-I/O, stdout, filesystem (read / write / read+write), random, env,
    args, clock (wall / monotonic), stderr, exit, and the fs-paired
    two-category combos (fs+env, fs+args, random+write). Programs whose WASI
    combination has no wrap yet are rejected with a clear error rather than
    emitting a broken component. This covers **every wasi:cli/run shape the
    self-host emit supports**.

    The one wasm shape still missing is **`wasi:http/incoming-handler`**
    (the native `-target wasi-http`): it needs a new self-host core emitter
    that lowers the request/response **resource handles** — which builds on
    the in-progress own/borrow resource-handle work — so it's deferred until
    that lands.

---

## Item 1 — ASCII i32 char methods → `std/sort` ✅

**Blocker.** `std/sort` links against `__fn_i32__to_lower` (case-insensitive
string compare). `std/string` additionally uses `is_digit` / `is_alpha` /
`is_alnum` / `is_hex_digit` / `is_upper` / `to_lower` / `to_upper`.

**Plan.** Emit these inline as i32 methods, exactly like the existing
`abs` / `min` / `is_even` family. ASCII semantics mirror `std/i32`:
range checks lower to `subq lo; cmpq hi-lo; setbe` (unsigned); `to_lower`
/ `to_upper` use `cmov`. Register their result tags in `infer_expr_type`
(`is_*` → bool, `to_*` → i32). Arm64 mirror: `sub; cmp; cset ls` and
`csel`.

**Verification.** Bundle `std/sort` (+ a main) and assert a sorted
result; direct i32-method cases cross-checked vs Go.

**Status.** x86-64 emit + infer done (uncommitted); arm64 mirror + tests
pending. ~½ PR remaining.

**Risk.** Low — same shape as existing inline i32 methods.

---

## Item 2 — `std/array` ✅ / `std/string` ✅

**Status (std/string).** Links and works on the self-host. The feared
packed-u8[] mismatch turned out narrow: `std/string` only references
the packed representation from its `bytes()` body (`__memcpy(out,
s.as_bytes(), n)`), but the self-host dispatches `s.bytes()` to its own
slot-array `__fern_str_bytes` builtin, leaving that body uncalled. So
the fix was just two linkable shims: `s.as_bytes()` → `__fern_str_bytes`
(same fresh u8[]; no zero-copy slice in the slot model) and `__memcpy`
→ `rep movsb` (x86) / byte loop (arm64). Verified by
`self_host_string_test.go` (index_of, trim, to_upper/lower, contains,
starts_with, replace, repeat, split) cross-checked vs the Go backend.



**Blockers (after Item 1).** `std/string` still needs `__memcpy` (a
runtime byte-copy), `int.parse_int_radix`, and `i32.to_ascii_string`.
`std/array` needs the `std/sort` functions (resolved by bundling sort)
and `i32.gcd` / `i32.lcm`. Plus the **coarse tuple-element-type**
limitation: `split_at(): (string, string)` then `p.0.len()` mis-infers
`p.0` as non-string (tuple element types are erased to `"tuple"`).

**Plan.**
- `__memcpy(dst, src, n)` runtime builtin (rep movsb / a byte loop).
- `i32.parse_int_radix` / `to_ascii_string` / `gcd` / `lcm` as inline i32
  methods or small runtimes.
- Tuple-element types: extend the parser to keep per-element type tags
  (e.g. store `"tuple:string,string"` instead of `"tuple"`) and teach
  `infer_expr_type` of `ExprIndex`/`.0`/`.1` on a tuple to read the
  element tag. This is the only non-mechanical part.

**Verification.** Bundle `std/string` / `std/array` (+ their std deps) and
exercise representative functions (e.g. `split_at`, `index_of`, sort).

**Risk.** Medium — the tuple-element-type inference touches the
parser's type representation and `infer_expr_type`.

---

## Item 3 — `std/url` ✅ + `std/json` ✅ → Map runtime

**Status.** `std/url` full-links on both backends. The Map runtime is
now on x86-64 **and** arm64 (`__fern_map_new`/`set`/`get`/`has`), and the
self-host emitter tracks the Map value type in the type tag
(`"map:<value-tag>"`), so `m.get(k): Option[string[]]` binds the payload
as `string[]` — `existing.push(val)` / `.len()` dispatch to the array
runtime instead of mis-typing as a string. Verified by
`self_host_url_test.go` (x86 + CI-gated arm64): `url_parse`,
`query_parse` over `Map[string, string[]]` with duplicate keys.
`std/json` now round-trips on both backends (parse / encode / typed
get, incl. nested objects + arrays) — see `self_host_json_test.go`. Four
capabilities landed to get there: (1) Rust-style `enum` declarations +
call-style constructors + payload-binding matches (`self_host_enum_test.go`);
(2) Map iteration — `m.iter()` → `MapIter[K,V]` with
`.has_next()`/`.key()`/`.value()`/`.advance()` (`self_host_map_iter_test.go`);
(3) `std/string` linking (Item 2); and (4) **struct field assignment**
(`obj.field = v`) — the recursive descent parser mutates `p.pos` /
`p.error`, which was previously a silent no-op (the lvalue fell to a
StmtUnknown). Field assignment desugars to `__set_field(obj, "field",
v)`, shape-dispatched to the field slot like a field read
(`self_host_field_assign_test.go`). The emitter now also injects the
builtin enums the Go checker auto-injects — `JsonValue` and `IoError`
(`parser.inject_builtin_enums`, seeded into `emit_module`'s struct table,
not `parse_module` which is a tested API) — so a program uses `std/json`
or `IoError` without a local `enum` declaration
(`self_host_json_test.go` now bundles no enum;
`self_host_builtin_enum_test.go` covers `IoError`).

Aside: found a Go *native* backend bug while testing — compound
assignment to a struct field (`a.v += 35`) yields a wrong result
(plain field-assign and compound-on-locals are fine). The self-host
handles it correctly. Separate subsystem; flagged, not fixed here.

**Blocker.** Both need a hash-map: `map_new`, `map_get`/`get_or`,
`map_set`, `map_has`, `map_delete`, `map_len`, `map_keys`, `map_values`.
`std/json` also needs call-style enum constructors (`JString(...)`,
`JNumber(...)`, `JArray(...)`, `JObject(...)`) — these are the same
shape as `Some(x)` / `Ok(x)`, already supported, but currently only the
Option/Result names are special-cased.

**Note (refined).** `std/url` is *doubly* blocked: beyond the Map
runtime it relies on `m.get(key): Option[string[]]` and then
`existing.push(val)` on the payload — but the coarse "Some payload is a
string" heuristic (item 5) mis-types `existing` as a string, so `.push`
mis-dispatches (`string__push`). So `std/url`/`std/json` full-linking
also depends on item 5. The **Map runtime is the self-contained core**
and is landed first on its own (testable directly with `Map[string,i32]`,
where the payload-type coarseness doesn't bite).

**Plan.**
- A string-keyed map runtime in the emitter: parallel `keys[]` /
  `values[]` arrays with linear probe (simplest correct form; the
  self-host compiler is not perf-critical), or an open-addressed table.
  Box layout: `{keys: string[], values: T[]}`. Emit `__fern_map_*`
  runtimes on both backends.
- Generalise the `Some`/`Ok` call-style constructor + match handling to
  arbitrary user union variants if `std/json`'s `JString(x)` etc. aren't
  already covered by the existing struct-shape path (verify first).

**Verification.** Bundle `std/url` (parse a URL) / `std/json` (round-trip
a small document).

**Risk.** Medium-high — the map runtime is a sizeable new subsystem,
and K/V type combinations (string→string vs string→i32) need handling.

---

## Item 4 — `std/float` → libm intrinsics ✅

**Blocker.** `std/float` calls `__abs_f64`, `__sqrt_f64`, `__floor_f64`,
`__ceil_f64`, `__round_f64`, `__trunc_f64`, `__sin_f64`, `__cos_f64`,
`__exp_f64`, `__log_f64`, `__pow_f64`.

**Plan.** Split by difficulty:
- **Cheap (single SSE / arm64 instr):** `abs` (andpd sign mask), `sqrt`
  (`sqrtsd` / `fsqrt`), `floor`/`ceil`/`round`/`trunc` (`roundsd` imm /
  `frintm`/`frintp`/`frintn`/`frintz`). Land these first.
- **Hard (numeric approximations):** `sin`/`cos` (range-reduce +
  minimax/Taylor), `exp`/`log` (range-reduce + polynomial), `pow`
  (= `exp(y·log(x))`). These need accurate implementations and careful
  testing against the Go backend across a value grid.

**Verification.** Per-intrinsic cases vs the Go backend; the transcendentals
checked to a tolerance (compare truncated/scaled results).

**Risk.** High for the transcendentals — specialised numerics, the most
error-prone item. The cheap subset is low-risk but `std/float` only fully
links once all 11 exist.

**Status.** Done. Cheap subset (abs/sqrt/floor/ceil/round/trunc) is
inlined as single SSE / FP instructions on both backends (`round` is
ties-away-from-zero: `frinta` on arm64, `trunc(x + copysign(0.5, x))`
on x86). Transcendentals (sin/cos/exp/log/pow): x86 uses the x87 FPU
directly (`fsin`/`fcos`/`fyl2x`/`f2xm1` — hardware-accurate, no libm);
arm64 has no transcendental instructions, so they're polynomial-
approximation runtime functions (range-reduced Taylor/series, validated
<1e-5 vs libm). Tested by `self_host_float_intrinsics_test.go`
(x86 + CI-gated arm64); fixpoint stays byte-identical.

---

## Item 5 — `interp.fern` → inference overhaul ✅

**Blocker.** The self-host `infer_expr_type` is name-based: a struct
field read `x.f` resolves via the *first* struct declaring field `f`.
`interp.fern`'s `Value` union has `VInt{v:i32}`, `VBool{v:boolean}`,
`VString{v:string}`, `VFloat{v:f64}` — all field `v` — so `s.v.len()`
(where `s` is a `VString`) infers `s.v` as `i32` (the first `v`) and
emits a bogus `i32.len()` call.

**Plan.** Track the *specific* struct/union type of locals (not just the
coarse `"struct"` tag), so `match (val) { VString(s) => s.v … }` records
`s : VString` and `s.v` resolves to `VString.v` = string. This means:
- Extend `bind_local_typed` / `local_types` to carry struct names.
- In match arms, bind the variant's payload local with its struct type.
- `field_type_tag` takes the receiver's struct name to disambiguate.

**Verification.** Compile `interp_run.fern` (lexer+parser+interp) and
evaluate programs end-to-end (`return 7` → exit 7, arithmetic, etc.);
cross-check vs the Go interpreter.

**Risk.** High — touches the core of the self-host inference; broad blast
radius. Do last, with the full suite as a regression net.

---

## Item 6 — `std/time` ✅

**Status.** Uninvestigated: currently exit-137s (a parser runaway) like
`std/string` did before the tuple-type fix.

**Plan.** Bisect to the offending construct (likely another type-syntax
or expression form the self-host parser doesn't consume), then fix it the
way the tuple-type / `as` gaps were fixed. Re-probe afterwards for
remaining runtime deps.

**Risk.** Unknown until diagnosed; the prior 137s were single clean
parser gaps.

---

## Parity gaps (post-fixpoint audit)

After items 1–6 + the module follow-ups landed, a full audit (all 32
stdlib modules + 96 e2e feature cases probed through the self-host
compiler) surfaced the remaining gaps between the self-host compiler and
the Go front-end. Excluding probe artifacts (cases that `import` stdlib
won't link when only `main.fern` is bundled; `err_*` checker
negative-tests that don't apply to the emit-only path), they are, in
planned order:

- ✅ **Array-literal inference + `min`/`max` semantics.** `var a =
  [1,2,3]` now infers `array_i32` (was generic `array`, so `.sum()`
  mis-dispatched); `arr.min()`/`max()` now return `Option[i32]`
  (Some/None) instead of a raw i32 — matching the reference
  (`self_host_array_methods_test.go`).
- ✅ **`if let`** pattern sugar — `if let PAT = E { … } else { … }`
  desugars in the parser to `match (E) { PAT => …, _ => … }`
  (`self_host_if_let_test.go`).
- ✅ **`let else`** — `let PAT = E else { divergent };` desugars in the
  parser by folding the *rest of the enclosing block* into the success
  arm of a statement-match: `match (E) { PAT => { <rest> }, _ => {
  divergent } }`. Reuses the existing match binding + codegen — no new
  AST node, all backends inherit it (`self_host_let_else_test.go` on
  x86-64 + arm64, plus wasm cases in `self_host_wasm_emit_test.go`). The
  success bindings live for the rest of the block; the else branch is
  expected to diverge. Diagnostic gap: the reference checker's dedicated
  E022 ("let-else source must be an enum value") surfaces here as E035
  (match-on-non-enum) via the desugar — a follow-up, matching the
  if-let / match-guard precedent.
- ✅ **Recursive local functions** — `function f(...) { … f(…) … }` inside
  another function. A non-recursive local already desugars to `var f =
  function(…){…}` (a closure); a self-recursive one can't see its own name
  through the closure value. `hoist_local_funcs_module` (a post-parse pass
  in `module_with_builtins`) lifts a self-recursive local to a top-level
  function — recursion resolves once it's top-level — reusing all the
  existing top-level-function machinery (no new AST node, no backend
  changes). A **capturing** recursive local is lambda-lifted: the captured
  enclosing names become trailing parameters (untyped — the self-host's
  uniform 8-byte slots don't need a parse-time type, which the pre-checker
  pass couldn't supply), and every call site — the recursive self-calls
  inside the lifted body and the external calls in the enclosing body — is
  rewritten to pass them (`rw_call_stmts`/`rw_call_expr`, with the lift
  list threaded through `HoistResult.lifts`). The pass only rebuilds a body
  that
  actually contains a recursive local (the `hl_has_rec_local` precheck), so
  the self-host's own sources — which use none — pass through untouched and
  the byte-identical stage-2 fixpoint holds. Surfaced (and required) the
  arm64/x86 rc-helper below-heap guard fix (#2292): a no-capture closure is
  a bare code pointer the exit-dec sweep must not rc-dec.
  `self_host_recursive_local_test.go` (x86-64 + arm64) + wasm cases.
- ✅ **`switch` / `case`** — desugars in the parser to a nested
  if/else-if chain (multi-value cases OR their `==` comparisons; no
  fall-through) (`self_host_switch_test.go`).
- ✅ **`match` on a non-enum scrutinee** — `match (n) { 1 => …,
  "yes" => …, _ => … }` on an i32 / string value. The self-host's
  `Pattern` grammar is variant-only, so the match-arm parsers
  (`parse_match_stmt` / `parse_match_expr`) now recognise a literal at
  the pattern position (`peek_is_literal`) and, when any arm is a
  literal, desugar the whole match to an if/else-if chain
  (`build_literal_match`) — the same shape `switch` and the native
  `emitLiteralMatch` produce. A guard folds in as `scrut == lit &&
  guard`; the `_` arm becomes the final else. Lives entirely in the
  parser (no new AST node, no `Pattern`-union ripple), so every backend
  — including the IR path — inherits it; this also widened the SSA
  emit subset (an int-literal match now lowers through SSA instead of
  falling back to the AST emitter). The native **interpreter** had the
  matching gap (it rejected non-enum scrutinees as "expected enum
  value" while the compiled backends already lowered them) — fixed in
  `internal/interp` so the reference oracle agrees with codegen
  (`self_host_match_literal_ir_test.go`,
  `TestInterpMatchLiteralNonEnum`). Diagnostic note: a variant pattern
  on a non-enum scrutinee still parses as a variant and draws E035, as
  before.
- ✅ **i32-keyed maps** — `Map[i32, V]` tags as `mapI:<V>`; the
  dispatch passes a key-kind flag and the runtime takes an integer
  (`==`) key-compare path instead of `__fern_str_eq`
  (`self_host_map_i32_test.go`).
- ✅ **Map literals** `Map { k: v, … }` — parsed (in a `parse_map_lit`
  helper) into a chained `map_new[_i32](n).set(k,v)…` desugar; integer
  vs string keys picked from the first key (`self_host_map_literal_test.go`).
- ✅ **`m.keys()` / `m.values()`** — return the map box's parallel
  arrays (offset 0/8) as array_i32/array_string so array methods chain
  off them (`self_host_map_keys_test.go`).
- ✅ **`for (k,v) in m`** destructuring iteration — parser encodes the
  names as "k,v"; the emitter (`emit_map_kv_for`) walks the parallel
  keys[]/values[] arrays (`self_host_for_kv_test.go`). Adding this code
  exhausted the self-host's 256 MiB bump heap when compiling the larger
  bundle, overflowing into the adjacent output buffer and corrupting
  emitted strings (no GC, no bounds check) — **fixed by enlarging the
  heap to 1 GiB** (zero-page .bss, both backends). The follow-up hard
  **bounds-check/trap is now in `__fern_alloc`** (both backends): an
  allocation that would run past the heap end traps with a clean,
  recognisable exit code (137) instead of silently corrupting adjacent
  .bss. The check preserves `__fern_alloc`'s original register-clobber
  contract (only the return + size registers), so callers that hold
  live values across the call (e.g. `to_ascii_string` keeps the byte in
  `%rcx`) are unaffected (`self_host_alloc_trap_test.go`).
- ✅ **Function types `(T) => R`** → higher-order functions. The
  parser consumes the `(T1, …) => R` type spelling (returning a coarse
  "fn" tag); a function referenced by name as a value lowers to a
  1-slot closure box `[&__fn_name]`. The closure-call convention now
  passes the box in a register (x86 `%r10` / arm64 `x9`) rather than as
  a hidden stack arg, so a plain function and a real closure share one
  call convention with no param shifting and no per-function
  trampolines — the plain function simply ignores the box
  (`self_host_higher_order_test.go`).
- ✅ **Closures** (capturing nested functions returned as values).
  The always-boxed `function(…)` lambda form captures locals
  (single + multiple, i32 + string), is callable, can be returned
  across a `(T) => R` return type, and curries
  (`self_host_closures_test.go`). A 0-arg function passed by name
  (`run(work)` where `run`'s param is `() => R`) is indistinguishable
  from a const at the bare-ident site — both desugar to a 0-param
  function — so the function-value-vs-call decision is made at the
  **call site**: if the callee's param at that index is fn-typed and
  the argument is a bare ident naming a function, it lowers to a
  function-value box rather than a call (`callee_param_is_fn` /
  `arg_fn_value_name`). NB: Fern has no arrow-lambda *value* syntax
  `(x) => …` — `=>` only spells function types and match arms; lambda
  values are always the `function(…)` form.
- ✅ **Tuple destructuring** (`var (a, b) = …`) — parser encodes the
  names as "a,b"; the emitter binds a = tuple.0, b = tuple.1
  (`self_host_tuple_destructure_test.go`). Also fixed `count_locals` to
  account for the two bindings (and the four that `for (k,v)` binds).
- ✅ **`?` try operator** — `expr?` desugars (parser) to the unary op
  "try_"; the emitter unwraps a Some/Ok payload or early-returns the
  None/Err box from the enclosing function
  (`self_host_try_op_test.go`).
- ✅ **Generics** — by type **erasure**, not monomorphisation. The
  self-host ABI is a uniform 8-byte stack slot for every value (i32,
  ptr, …), so one emitted body / field layout is correct for every
  instantiation; the parser consumes + discards the `[…]`
  type-parameter list on `function name[T](…)` and `struct Name[T]
  { … }` declarations. Covers generic functions (`id[T]`,
  `fst[A, B]`), generic structs (`Box[T]`, `Pair[A, B]`), and a single
  generic body used at multiple concrete types in one program — the
  two upstream parity cases (`generic_id`, `generic_box`) plus
  multi-param + mixed-instantiation (`self_host_generics_test.go`).
  Lives entirely in the shared parser, so both backends benefit with
  no emitter change. **Known erasure boundary:** calling a
  type-specific method on a value whose concrete type is known only
  via an instantiation (e.g. `Box { val: "hi" }` then `b.val.len()`)
  mis-dispatches, because the erased field type tag is `"unknown"`.
  Truly parametric code can't call type-specific methods on `T`
  anyway, and the parity cases don't hit it; tracking per-
  instantiation field types is a follow-up if a real program needs it.
- 🔧 stdlib **`std/test` / `std/fuzz` / `std/tcp`** (no user generics
  of their own). **Survey done:** all three now **parse + emit cleanly**
  through the self-host (0 `ExprUnknown`). The sole parse gap was a
  **trailing comma before the closing `}` of a struct literal**
  (`TestRunner { …, quiet: false, }`) — the self-host had two struct-lit
  parsers (`parse_struct_lit_body` + the inline one in `parse_primary`),
  both of which looped back to expect another field after the comma and
  cascaded into a run of `ExprUnknown`. Both now allow the trailing
  comma (`self_host_trailing_comma_test.go`, shared parser → both
  backends). **Update (post-prelude-removal):** the old "needs prelude
  injection for `std/test`'s bare-name `TestRunner` / `assert_*`"
  blocker is **obsolete** — the auto-prelude is gone, so `std/test` is
  now used the same way as any module (`import "std/test"; t :=
  test.test_new(...); test.assert_eq_i32(...)`), and the self-host's
  stdlib-import resolution (see the ✅ item below) already handles that.
  What remains is purely the **runtime-builtin surface** (next item).
- ✅ **Stdlib import resolution in the file-loading driver.**
  `asm_load_run.fern` now takes an optional stdlib root as its second
  argument and resolves `std/…` / `core/…` imports under it
  (`<root>/std/foo.fern`), loading them transitively through the same
  worklist + `flatten.bundle` machinery it already used for local
  `./…` imports (the worklist tracks full import paths, not just
  basenames).
  With no root given, std/core imports are skipped — identical to the
  prior behaviour, so the file-driven fixpoint is untouched. Proven
  end-to-end by `self_host_stdlib_import_test.go`: a program
  `import "std/math"` (leaf) and `import "std/sort"` (transitive →
  `std/string`) compile + run correctly through the self-host.
- ✅ **Low-level memory + RC intrinsics** (`__alloc` / `__free` /
  `__load_i32` / `__store_i32` / `__load_i64` / `__load_ptr` /
  `__store_ptr` / `__ptr_width` / `__memset` + `__fern_rc_inc` /
  `__fern_arr_dec` / `__fern_drop_arr_ptr`). `std/json` imports
  `core/map`, whose open-addressing source pokes raw memory through
  these names; before, a `std/json`-importing program failed to link
  with undefined `__fn___alloc` &c. They're now emitted as plain
  runtime functions on both backends under the self-host's stack-arg
  ABI (param[0] at `16(%rbp)` / `[x29,#16]`, result in the return
  register). `__alloc` forwards to `__fern_alloc`; the memory pokes
  are real; `__free` and the three RC intrinsics are no-ops, consistent
  with the leak-everything bump heap (nothing is ever reclaimed). Note
  the self-host's own `Map[K,V]` lowers to the *native* `__fern_map_*`
  runtime, so `core/map`'s bodies are compiled-but-dead — the symbols
  only need to resolve. Proven by the `std-json-intrinsics` case in
  `self_host_stdlib_import_test.go` (a `std/json`-importing program
  links + runs: 10+32=42).
- ✅ **`std/test` end-to-end runs through the self-host.** Compiled by
  the file-loading driver (`asm_load_run.fern` + the real repo stdlib as
  its import root), assembled, linked, and run, several example suites
  now produce **byte-identical TAP-13 output and exit codes** to the
  reference interpreter (`self_host_stdtest_test.go`, native x86 — the
  driver resolves stdlib by host path so it can't run under qemu):
  `arithmetic`, `strings`, `fail_fast`, `quiet_mode`,
  `skip_and_subsuites`, plus a synthetic failing suite pinning the
  `not ok` / exit-1 path.
  - **Required fixing `print` to append a newline** (Fern's `print` ==
    `println`, matching the interp + Go backend; `write` stays verbatim).
    The self-host had `print` and `write` both emit bytes with no
    newline, so TAP lines ran together. Fixed on both backends (literal
    fast path folds the `\n` into the interned string; the non-literal
    path emits a second 1-byte write). The self-host's own AsmRun /
    arm64-emit string-output cases were rewritten to use `write` for
    no-newline/token output and bare `print` for whole lines.
  - ✅ **Tuple-destructure element typing.** `var (a, b) = init` bound
    both names to `i32`, so a struct-typed element's receiver method
    mis-mangled as `__fn_i32__<m>` (e.g. `__fn_i32__it` on the
    `TestRunner` from `must_temp_dir`) and failed to link. The parser
    now preserves the precise tuple spelling (`(A, B)` instead of the
    coarse `"tuple"`), and both emitters split it (tuple literal →
    per-item inference; ident call → declared return type) to bind each
    element's real tag — so struct elements route through the runtime
    shape-pointer dispatch. Guarded by the
    `tuple-destructure-struct-method` AsmRun case on both backends.
  - ✅ **Match-scrutinee payload typing for `Option[T]` / `Result[T,…]`
    locals + call results.** `match (o) { Some(msg) … }` bound `msg` as
    `"unknown"` whenever the scrutinee was either an `ExprIdent` (a
    local like `var o: Option[string] = …`) *or* a call to a user /
    module function whose return type wraps a payload. That mis-routed
    string-typed payloads through struct shape dispatch — e.g.
    `msg.contains(...)` claimed `"expected 5"` wasn't in
    `"assert_eq_i32: expected 5, got 4"`. `match_payload_type` now
    walks the local type table for `ExprIdent` scrutinees and parses
    `Option[T]` / `Result[T, …]` out of the called function's
    `ret_type` for `ExprCall`. With this, `runner_self_test` and
    `option_and_set_ops_test` join the std/test e2e gate
    (`self_host_stdtest_test.go`).
  - ✅ **Result Ok-payload typing.** `ret_tag_of("Result[T, E]")`
    collapsed to `"result:unknown"`, so `match (r) { Ok(v) … }` bound
    `v` as `"unknown"` and `v.len()` (e.g. on a Result[string[], …]
    payload) fell through every primitive dispatch arm to a
    `-1` sentinel, segfaulting downstream. Both backends now wrap the
    generic args, reuse `split_tuple_ret`'s bracket-depth-aware split,
    and prefix `"result:"` so existing `is_result()` checks still match.
    `result_assertions_test` now matches the interpreter byte-for-byte
    and joins the gate — closing out the std/test follow-up list.
  - ✅ **`Ok(x)` / `Err(x)` constructors.** Only `Some(x)` was lowered
    as a wrapper heap box; `Ok` / `Err` fell through to the generic
    function-call path and emitted `call __fn_Ok` / `call __fn_Err` —
    undefined at link time inside any Fern body that constructs a
    Result (`std/fuzz`'s `fuzz_corpus_from_dir`, user helpers, the
    obvious `return Ok(...)` pattern). Both backends now emit Result
    heap boxes (tag 0 / 1 @ 0, payload @ 8 — matching the StmtMatch
    discriminant and the existing runtime-helper-constructed Results).
    Guarded by the `result-ok-err-constructors` AsmRun case on both
    backends; all three `std/fuzz` example suites (`fuzz_example`,
    `fuzz_corpus`, `fuzz_shrink`) now match the interpreter
    byte-for-byte and join the differential gate.
  - ✅ **Small builtins follow-up batch.** Six builtins the interp
    exposes that the self-host hadn't surfaced yet — each tiny but
    individually blocking one or more example suites:
    - `sleep_ms(n)` — nanosleep wrapper (no heap; lives next to the
      clock helpers, emitted unconditionally).
    - `remove_file(path)` — single `unlinkat`, mirroring
      `remove_dir_all`'s file branch; returns `Option[IoError]`.
    - `f64_bits(x)` / `f64_from_bits(b)` — no-op reinterprets (the
      rt-stack already holds the IEEE bit pattern).
    - `f32_bits(x)` / `f32_from_bits(b)` — `cvtsd2ss`+`movd`+`movsxd`
      narrow / `movd`+`cvtss2sd` widen, since the self-host has no
      distinct f32 value.
    Guarded by the `small-builtins-roundtrip` AsmRun case on both
    backends; `filesystem_ops_test` (the `remove_file` driver) joins
    the differential gate. The link-error unblock on `batch7_test`,
    `float_test`, and `timing_test` exposes other downstream bugs in
    those suites (NaN-handling, segfaults in i64 / sleep paths) that
    are separate follow-ups.
  - ✅ **IEEE NaN semantics in f64 compares (x86).** `ucomisd` sets
    CF = ZF = PF = 1 on unordered, so naked `setb` / `setbe` / `sete`
    treated `NaN < x`, `NaN <= x`, and `NaN == NaN` as true. `<` / `<=`
    / `==` now AND with `setnp` (ordered-only); `!=` ORs with `setp`
    (true when unordered) so `is_nan(x) = x != x` finally works. arm64
    was already correct (its `cset eq` / `lt` / `le` family requires
    ordered flags). Guarded by the `f64-nan-compares` AsmRun case on
    both backends; `float_math_test` and `float_array_strict_sort_test`
    now match the interpreter byte-for-byte and join the gate.
  - ✅ **`subprocess(cmd, args, stdin)` runtime helper (x86).**
    Forks a child, plumbs the child's stdin / stdout / stderr through 3
    pipes (pipe2 × 3), execve's the cmd with `argv = [cmd, args…, NULL]`
    and the parent's envp, then in the parent: writes the stdin
    payload, reads stdout + stderr to EOF (64 KiB scratch buffers each),
    waitpid's the child, decodes the exit status (WIFEXITED → status>>8;
    signal → 128+sig), and packs the result into a `ProcessResult
    {stdout, stderr, exit_code}` struct (decl injected via
    `parser.inject_builtin_enums`, shape pointer pre-interned in the
    .rodata pool). The child falls back to `/bin/<cmd>` and
    `/usr/bin/<cmd>` so bare names like `"echo"` / `"sh"` / `"cat"`
    resolve without a full PATH search; spawn failure lands as
    `exit_code = 127` via the child's `exit(127)` after the final
    execve returns. Unblocks `process_assertions_test`,
    `process_output_shortcuts_test`, and `lang_binary_e2e_test` —
    all three match the interpreter byte-for-byte and join the
    differential gate.
  - ✅ **`subprocess(cmd, args, stdin)` runtime helper (arm64).**
    Arm64 mirror of the x86 implementation above. Same shape but
    using arm64 syscalls — `pipe2`=59, `clone(SIGCHLD, 0, …)`=220
    as the fork equivalent (arm64 Linux has no `fork`), `dup3`=24
    in place of `dup2`, `close`=57 / `read`=63 / `write`=64 /
    `execve`=221 / `wait4`=260 / `exit`=93 — with `__fern_envp`
    loaded PC-relatively via `adrp` / `add` / `ldr`. Locals + 5
    callee-saved pairs share a 224-byte frame (6 pipe fds + wait
    status packed at +96..+123 from `sp`; scratch above).
    `infer_expr_type` now recognises `subprocess` as returning
    `"unknown"` so the `ProcessResult` field access routes through
    shape-pointer dispatch. The 3 previously-skipped suites
    (`process_assertions`, `process_output_shortcuts`,
    `lang_binary_e2e`) now ride the arm64 gate too — the gate
    sits at **49/49** on both backends.
  - ✅ **Stage-2 fixed-point gate.** `TestSelfHostStage2FixedPoint`
    proves the self-host is a fixed point of its own emit:
    mmc-stage1 (Go-compiled `asm_load_run.fern`) compiles
    `asm_load_run.fern` to a 5.8 MB asm program — that program
    links into mmc-stage2 (a fully self-hosted compiler). For
    `asm_load_run.fern` itself plus 10 representative gate cases
    (basic arith, struct shape dispatch, Option / Result payload
    typing, std/fuzz, f64, wider-int / unsigned compare, json
    typed-get, bench, subprocess, http response), the asm emitted
    by mmc-stage1 and mmc-stage2 is **byte-identical**. So the
    Fern emitter is deterministic AND idempotent under self-
    recompilation — the first non-trivial proof that the self-host
    has reached its eigenvector. Runs in ~8 s; cheap to gate.
  - ✅ **Arm64 stage-2 fixed-point gate.**
    `TestSelfHostStage2FixedPointArm64` mirrors the x86 version
    but for the arm64 emit path. mmc-arm64-stage1 (x86 host
    binary, the cross-compiler-on-host pattern the differential
    gate already uses) compiles `asm_arm64_load_run.fern` to ~6.8
    MB of aarch64 asm; that links into mmc-arm64-stage2, a real
    aarch64 binary running under qemu-aarch64 (or native on arm64
    hardware). For 4 representative cases (`self`, `sort_wider`
    — unsigned `cset lo/ls/hi/hs`, `float_math` — IEEE NaN
    compares, `process_assertions` — `clone(SIGCHLD)` /
    `dup3` / `execve` syscall fork) the arm64 asm emitted by
    stage-1 (running on x86) and stage-2 (running under qemu) is
    **byte-identical**. So the arm64 emit path is both
    deterministic (host arch doesn't influence output) and
    idempotent under self-recompilation. Runs in ~15 s.
  - ✅ **Arm64 backend `strbuf_take` return-shape fix.** The Go
    arm64 backend's `returnIsString` table — which decides
    whether `OpCallDirect`'s post-call push moves both `x0` (data
    ptr) and `x1` (byte length) onto the operand stack — listed
    `random_bytes` / `tcp_recv` / `string_from_bytes` /
    `__str_slice` but NOT `strbuf_take`. So strbuf-take's
    two-word return went through the single-word path: only `x0`
    was pushed, the second-word `OpStoreLocal` read garbage from
    the stack as the byte length, and any program using strbuf
    silently mis-rendered its output (e.g. a 0x10000000-byte
    write to fd 1 that EFAULTs, then the trailing newline from
    `print`). The most visible symptom was the arm64 self-host
    (`asm_arm64_load_run.fern`) compiling cleanly through the Go
    arm64 backend but emitting **0 bytes of asm** at runtime —
    the strbuf-take-then-print chain at the end of `emit_module`
    dropped its payload. Also: the strbuf runtime references
    `__fern_memcpy` (for the append + take memcpys) and
    `__fern_alloc` (for take's fresh string box), so the
    `strbuf_*` cases in arm64's `OpCallDirect` dispatch now set
    `g.usesMemcpy` / `g.usesAlloc` to pull those runtimes in —
    matching the x86 mirror. Without that, a program that used
    only strbuf (and nothing else triggering memcpy / alloc)
    failed to link with undefined `__fern_memcpy` references.
    Pinned by `TestArm64StrBufTake` (3-char reproducer) +
    `TestArm64StrBufLargeAppend` (1 000 × 5 bytes for the bump-
    loop / multi-page path).
  - ✅ **bench / rel_tol_and_ms_bench joined the differential gate.**
    With the `fn`-arg boxing fix below, both `bench_test` and
    `rel_tol_and_ms_bench_test` run cleanly end-to-end through the
    self-host. Their `# bench …` comment lines carry per-iteration
    timings that differ run-to-run, so the gate strips lines starting
    with `# bench ` on both sides before comparing — the rest of the
    TAP output is byte-identical to the interpreter.
  - ✅ **`fn`-typed args boxed (not called) on method calls.** The
    free-function call path already detected when a bare ident
    argument named a function whose callee param was `() => void` and
    wrapped it as a closure value (`{&fn_ptr}`) — so
    `call_n(3, my_workload)` correctly passed the function. The
    **method-call** arg loop didn't, so `f.bench("…", n, my_workload)`
    silently invoked `my_workload` 0-arg and passed the return value;
    inside `bench`, `fn()` then dereferenced the garbage box and
    segfaulted. Both backends' method-call arg loops now use
    `arg_fn_value_name` like the free-fn path, and `callee_param_is_fn`
    looks up both free and receiver methods. Both `bench_test` and
    `rel_tol_and_ms_bench_test` now exit 0 through the self-host;
    they aren't byte-identical to the interp (timing in `# bench …`
    comment lines differs run-to-run) so they don't join the
    differential gate, but the `method-fn-arg-boxed-not-called` AsmRun
    case pins the codegen on both backends.
  - ✅ **Prefix-bracket type `[T]` no longer OOMs the parser.**
    `var ab: [u8] = …` left the `[` unconsumed (`parse_type_name`'s
    fall-through with no base ident / keyword / paren), so the
    surrounding decl loop spun until the kernel killed the
    compiler. Now `[T]` desugars to `T[]` in the parser — reusing
    the existing postfix-array type path. `string_prelude_migrated_test`
    now compiles, runs, and joins the differential gate.
  - ✅ **`int.int_to_string(n)` routes to `__fern_i32_to_string`.**
    The qualified call `int.int_to_string(n)` modload-mangles to
    `int__int_to_string`; the self-host had no dispatch for that
    name, so core/int's pure-Fern body ran (raw __alloc_u8 + __memcpy
    + string_from_bytes path) and produced empty strings on the
    self-host's u8[] layout. std/http's response serializer chains
    `... + int.int_to_string(resp.status) + ...`, producing
    `HTTP/1.1  OK` (status dropped) and `Content-Length: ` (length
    dropped). Both backends now special-case the mangled name and
    route to `__fern_i32_to_string`. `http_response_headers_migrated_test`
    matches the interpreter byte-for-byte and joins the gate.
  - ✅ **`HeaderMap` / `HttpRequest` / `HttpResponse` struct decls
    injected.** std/headers and std/http construct these types via
    record literals (e.g. `HeaderMap { names: …, values: … }`) but
    never declare a `struct`. The Go-side checker infers the shape
    from usage; the self-host's shape-pointer dispatch needs the
    declaration to resolve field offsets. Tried adding the decls to
    the stdlib files first — that broke the Go checker by tipping
    its resolution toward "field, not method" for `h.get_all(…)` etc.
    Instead inject them in `parser.inject_builtin_enums` (same hook
    as `FileStat` / `IoError`) so the self-host gets the entries
    without changing the stdlib source. `header_map_migrated_test` and
    `http_request_headers_migrated_test` now match the interpreter
    byte-for-byte and join the differential gate.
    `http_response_headers_migrated_test` still differs on test 1
    (a separate behavioral bug in the response-builder serialization).
  - ✅ **Match-payload typing for receiver method calls.**
    `match_payload_type` already walked `s.funcs` for ExprCall scrutinees
    with an ExprIdent callee (free functions), but its
    ExprFieldAccess branch only special-cased a tiny whitelist
    (`read_chunk`, `get`, `min`, `max`). So `match s.parse_int() {
    Some(got) … }` — where `parse_int` is a `(s: string) parse_int():
    Option[i32]` receiver method — bound `got` as `"unknown"`. The
    downstream `got.to_string()` then fell through the primitive
    dispatch to struct shape-dispatch and segfaulted in any deeply-
    nested string-building flow (e.g. `assert_json_eq_field_i32`'s
    failure-message branch). Both backends now walk `s.funcs` for a
    receiver-method whose `receiver_type` matches the obj's type tag
    and parse `Option[T]` / `Result[T, …]` out of its declared
    `ret_type`. Guarded by `match-receiver-method-option-payload`
    AsmRun case (uses an inline `Wrap.try_get(): Option[i32]` so the
    fixture is self-contained); `json_field_eq_test` joins the
    differential gate.
  - ✅ **`Map.get_or(k, default)`.** core/map's pure-Fern
    `__map_get_or_impl` body uses an open-addressed layout that doesn't
    match the self-host's native `__fern_map_*` runtime, so falling
    through to it (via the generic method-call path) read garbage and
    segfaulted on first use. Both backends now special-case
    `m.get_or(k, default)`: inline as `__fern_map_get(m, k)` → branch on
    the Option tag → return the `Some` payload or the supplied default.
    Guarded by the `map-get-or-string` AsmRun case;
    `map_eq_and_predicates_test` joins the differential gate.
    `json_field_eq_test` and the migrated header / http tests link
    and run further, but hit other separate runtime bugs (tracked).
  - ✅ **Wider / unsigned integer tags.** `u32`, `u64`, `i64`,
    `usize`, `isize` weren't recognised by `ret_tag_of`, so a variable
    of one of those types had its tag default to `"unknown"`. Method
    calls like `(n as u32).to_string()` then fell through the primitive
    `.to_string()` dispatch (which keys on `prim_recv == "i32"`) to
    struct shape-dispatch, segfaulting. The first cut mapped these
    name strings onto the `i32` codegen path; the follow-up below
    promotes them to first-class tags so the unsigned compare path
    gets exercised. Guarded by `wider-int-as-cast-to-string` AsmRun
    case; `assert_at_wider_test` and `array_at_and_f32_range_test`
    join the differential gate.
  - ✅ **Unsigned compare semantics + wider-int array dispatch (x86).**
    `ret_tag_of` now returns distinct `u32` / `u64` / `i64` / `usize`
    / `isize` scalar tags and `array_u32` / `array_u64` / `array_i64`
    array tags. `ExprBinary`'s integer compare emit detects unsigned
    operand tags and picks `setb` / `setbe` / `seta` / `setae` instead
    of `setl` / `setle` / `setg` / `setge`, so values near `u64::MAX`
    sort by unsigned order. Method dispatch on the wider-int arrays —
    `.len()` / `.is_empty()` / `.push()` / `.reverse()` / `.concat()`
    / `.first()` / `.last()` / `arr[a:b]` slice and `for v in arr` —
    now accepts every `is_int_array_tag`, not only the literal
    `"array_i32"` string, so `for v in i64_arr { … }` iterates
    correctly. Nineteen previously-broken `examples/tests/*_test.fern`
    suites (`sort_wider`, `array_reductions`, `wide_numerics`,
    `wider_array_contains_count`, `sorted_unique_range`,
    `all_substring_array`, `array_prefix_suffix_subseq`, `batch7`,
    `ci_string_and_log_kv`, `env_unreachable`,
    `file_lines_and_timestamp`, `float`, `helpers`, `json_detail`,
    `one_of_none_of`, `set_eq`, `string_count_and_dir_listing`,
    `timing`, `unions_migrated`) join the differential gate.
    arm64 mirror is a follow-up — the x86 self-host gate is the
    one that diff-compares this on every PR.
  - ✅ **arm64 mirror of the unsigned-cmp / wider-int dispatch.**
    `asm_arm64.fern` now matches `asm.fern`'s tag system: distinct
    `u32` / `u64` / `i64` / `usize` / `isize` scalar tags + the
    matching array tags, the four `is_int_tag` / `is_int_array_tag`
    / `is_unsigned_tag` / `is_unsigned_int_array_tag` helpers, and
    `ExprBinary`'s integer compare picks `cset lo / ls / hi / hs`
    for unsigned operands instead of `lt / le / gt / ge`. Method
    dispatch (`.len()` / `.is_empty()` / `.push()` / `.reverse()` /
    `.concat()` / `.first()` / `.last()` / array slice + `for v in
    arr`), `is_concrete` / `friendly_type`, and `StmtFor`'s checker
    arm all accept every `is_int_array_tag`. Both backends now emit
    the same shape for wider-int sort / reduction code, so the CI-
    only arm64 self-host gate stays in step with x86 as the
    differential gate grows.
  - ✅ **arm64 differential gate alongside x86.** A new
    `asm_arm64_load_run.fern` driver (file-based, same import
    machinery as `asm_load_run` but routes through `asm_arm64`)
    plus `TestSelfHostStdTestE2EArm64` mirror the x86 gate's case
    list: the driver builds as a native x86 host binary
    (cross-compiler-on-host, the pattern the existing arm64
    reader / alloc-trap tests use), the aarch64 cross-gcc
    assembles + links each compiled program, and `qemu-aarch64`
    runs the result — stdout + exit code must match the reference
    interpreter byte-for-byte. Three suites are excluded
    (`process_assertions`, `process_output_shortcuts`,
    `lang_binary_e2e`) because the arm64 emitter doesn't yet
    have the `__fern_subprocess` runtime helper; track + retest
    once that mirror lands. Catches arm64 emitter regressions on
    every PR — without it, parity gaps only surface when someone
    runs the arm64 emit suite manually.
  - ✅ **`.lines()` trailing-newline.** The self-host's `s.lines()`
    was sugar for `s.split("\n")` — so `"a\nb\nc\n".lines()` returned
    4 lines (including a phantom trailing empty), and `"".lines()`
    returned `[""]` instead of `[]`. Both backends now compute a
    trim_flag (`s.len == 0 || s[s.len-1] == '\n'`) before calling
    `__fern_str_split`, then decrement the resulting array's `len`
    word by the flag — matching the interp + Rust/Python semantics.
    The existing `str-lines-trailing-newline` AsmRun case was
    rewritten (`"x\n".lines().len()` is 1, not 2); `lines_log_test`
    now matches the interpreter byte-for-byte and joins the gate.
- 🔧 **`std/test` runtime-builtin surface** (now complete). Used via
  explicit `import "std/test"; test.test_new(...)`
  (the prelude question is gone — see the update above). The batch,
  with progress:
  - ✅ `write_file(path, content): Option[IoError]` — emitted on both
    backends (`__fern_write_file`: NUL-terminate path, openat
    O_WRONLY|O_CREAT|O_TRUNC, write loop, close; None on success,
    Some(_) on error, mirroring `__fern_read_file`'s Err shape).
    Verified x86 by a write→read round-trip through the self-host
    (`self_host_write_file_test.go`); arm64 mirror is CI-gated.
  - ✅ `env(name): Option[string]` — emitted on both backends.
    `_start` saves `envp` (the vector past argv's NULL terminator) into
    `__fern_envp`; `__fern_env` walks it for a `NAME=` key and returns
    `Some(value)` (a string box pointing straight at the immortal envp
    bytes — no copy) or `None`. Gated on `needs_heap` (the helper
    allocates, and any `env()` caller marks the heap). Verified by a
    set/unset round-trip (`self_host_env_test.go`); arm64 mirror is
    CI-gated.
  - ✅ filesystem syscalls: `temp_dir` / `read_dir` / `stat` /
    `remove_dir_all` — dispatch + bodies fully working on both
    backends. arm64 ports landed in three rounds: (a) dispatch
    wired (#1626) — the helper bodies were always emitted
    unconditionally; emit_call just lacked the recognition; (b)
    the `O_DIRECTORY` constant — arm64 uses `040000=16384`, not
    the x86 `65536` (which is `O_DIRECT` on arm64). strace caught
    it in seconds: `openat(...,O_RDONLY|O_DIRECT)=EINVAL`. Same
    bug in both `__fern_read_dir` and `__fern_remove_dir_all`;
    (c) `.ltorg` after every emitted function body — the bundled
    self-host compiler is >1 MiB of code, so without periodic
    literal-pool flushes some `ldr X, =N` references slipped
    outside the PC-relative ±1 MiB window and `as` rejected
    them. The reader test that surfaced alongside this work was
    a test-program typo (`print` vs `write`), already fixed in
    #1630. The x86-64 helpers (these were
    interpreter-only before; no Go *native* backend implements them, so
    this was new ground, not a mirror). `temp_dir` mkdirs
    `/tmp/<prefix>-<monotonic_ns>`; `read_dir` walks `getdents64` into a
    1 MiB buffer and builds a `[cap, len, …]` array of base-name boxes
    (skipping `.`/`..`); `stat` `newfstatat`s into a stack buffer and
    builds a `FileStat` struct (`{is_file, is_dir, size}`);
    `remove_dir_all` recurses (open as dir → recurse children → rmdir;
    ENOTDIR → unlink; ENOENT → None). `stat` needs a synthetic
    `FileStat` struct decl (injected in `parser.inject_builtin_enums`)
    so field access knows the layout, and "FileStat" is pre-interned
    before the `.rodata` pool so the helper's shape pointer resolves
    even in programs that never mention `stat`. The arm64 `struct stat`
    `st_mode` sits at offset 16 (vs 24 on x86-64). Verified end-to-end
    by a make/write/stat/list/remove round-trip
    (`self_host_fs_test.go`); arm64 mirror is missing — see above.
  - ✅ time: `now_unix_ms` / `monotonic_ns` — emitted on both backends
    via `clock_gettime` (x86 syscall 228, arm64 113;
    `__fern_now_unix_ms` REALTIME→ms, `__fern_monotonic_ns` MONOTONIC
    →ns). Both return an i64 in the accumulator; the self-host has no
    distinct i64 tag, but all integers live in 64-bit slots and compare
    via `cmpq`/`cmp`, so the values round-trip without truncation (the
    `> 1e12` ms assertion in `self_host_clock_test.go` exceeds 32-bit
    range, proving it). Helpers are emitted unconditionally (they don't
    ride the `needs_heap` gate). arm64 mirror is CI-gated.
  - ✅ `f64__to_string` (float→decimal formatting) — emitted on both
    backends as `__fern_f64_to_string`, a hand-asm transcription of
    `std/float`'s `__float_to_string` with k=15: NaN → "NaN"; sign
    split; Inf (`x != 0 && x*2 == x`) → "Inf"; integer part via
    truncating float→int convert (`cvttsd2si` / `fcvtzs`); fraction
    scaled by 1e15, truncated, zero-padded to 15 digits with trailing
    zeros trimmed and the decimal point dropped when the fraction is 0.
    `(x: f64).to_string()` / `(x: f32).to_string()` dispatch to it
    (f32 collapses to the f64 tag in the self-host, so it formats with
    k=15 rather than std/float's k=7 — the self-host has no distinct f32
    value). Verified by a differential test whose expected output is
    std/float's exact rendering — including the IEEE noise digits
    (`123456.789000000004307`, `9999999.990000000223517`) —
    (`self_host_f64_test.go`); arm64 mirror is CI-gated. With this the
    runtime-builtin surface for `std/test` is complete.

Aside (Go backend, separate subsystem): the Go *native* backend
mishandles compound assignment to a struct field (`a.v += n`) — fixed in
the parser (PR allowing FieldAccess lvalues in the compound path).

---

## Native binary emission — no external assembler / linker

The end goal is a **fully self-hosted toolchain with zero external
tools**. The wasm backend already reaches this: the self-host emits
runnable binary wasm + Component-Model components in Fern
(`leb128.fern` → `wat_lex.fern` → `wat_parse.fern` → `wat_encode.fern`
→ `wat_emit_bin.fern` → `wat_component.fern`), no `wasm-tools`. The
**native** path is the remaining gap: the self-host emits `.s` *text*
(`asm.fern` / `asm_arm64.fern`) and shells out to `clang` / `gcc` /
`as` / `ld` / `lld` to assemble + link.

This is a **port, not an invention** — the Go bootstrap already emits
native binaries in-process, so each slice mirrors a Go reference. Like
the wasm binary track, every slice is independently testable through the
wasm self-test harness (concatenate the import-free module + a self-test
`main()`, run under `wasmtime`, assert via exit code). Ordered
smallest → largest:

- ✅ **ELF-64 writer** — `examples/self_host/elf.fern`, mirroring
  `internal/native/elf/elf.go`. Static, non-PIE, single `PT_LOAD`
  images for x86-64 + arm64 Linux: `elf_static_executable` /
  `_x86` (R+X) and `elf_static_executable_data` / `_x86` (R+W+X, .text
  8-byte-padded then data). Byte buffer is the same `i32[]`-of-0..255
  convention as `leb128.fern`; 8-byte fields via `elf_le64` (i64).
  Gated by `internal/e2e/self_host_elf_test.go` (`TestSelfHostELF`),
  asserting the fixed header + program-header layout (magic, class,
  `e_type`/`e_machine`, `e_entry` = 0x400078, the single PT_LOAD,
  `p_flags`, sizes, body placement + data alignment) for both the arm64
  R+X and x86-64 R+W+X shapes.
- ✅ **Mach-O writer + ad-hoc signature** — `examples/self_host/macho.fern`,
  mirroring `internal/native/macho/` (`macho.go` + `image.go` + `sign.go`).
  PIE, dyld-loaded arm64-darwin executable: `__PAGEZERO`, an r-x `__TEXT`
  (header + load commands + `__text`), an optional r/w `__DATA` whose vmsize
  covers the zero-init bss tail, and an r `__LINKEDIT` carrying the code
  signature; execution starts at `LC_MAIN`'s file offset. It emitted a
  static, dyld-free `LC_UNIXTHREAD` image until #6042 — every such binary is
  SIGKILLed at exec on Apple Silicon, the same platform rule #6000 hit on the
  native writer. The public entry is
  `macho_executable(text, data, ident, entry_off, bss)`; `macho_text_vaddr` /
  `macho_data_vaddr` expose the same fixed addresses an `@PAGE`/`@PAGEOFF`
  assembler must resolve against (the parity of the Go `SegmentAddrs`).
  Apple Silicon refuses unsigned binaries, so the ad-hoc
  `CSMAGIC_EMBEDDED_SIGNATURE` SuperBlob + `CSMAGIC_CODEDIRECTORY` is
  mandatory — its per-4 KiB-page hashes need **SHA-256**, which has no
  stdlib home yet, so a self-contained FIPS 180-4 implementation
  (`sha256_bytes`, words carried in `[0, 2^32)` i64s masked with
  `& 0xffffffff`) lives in the module. Big-endian for the signing blobs,
  little-endian for the Mach-O header/load-commands; the same
  `i32[]`-of-0..255 byte-buffer convention as `elf.fern`. Gated by
  `internal/e2e/self_host_macho_emit_test.go` (`TestSelfHostMachO`),
  asserting the fixed header + load-command layout (magic, cputype,
  filetype, `ncmds`/`sizeofcmds`, the segment names, the `LC_UNIXTHREAD`
  entry pc, the SuperBlob + CodeDirectory magics) for both the no-data and
  `__DATA` shapes, plus the SHA-256 "abc"/"" test vectors. **Not yet wired
  into the emitter** — like `elf.fern`, the next slice connects the arm64
  assembler's bytes to it and writes the file (replacing the `clang`/`ld64`
  shell-out).
- 🔧 **arm64 assembler** — AArch64 instruction text/forms → machine-code
  bytes, mirroring `internal/native/arm64/arm64.go`. The arm64 counterpart
  of the x86-64 assembler below; built up in slices.
  - ✅ **slice 3a — encoding primitives + `exit(N)` subset**:
    `examples/self_host/arm64_encode.fern` (`i32[]` byte-buffer convention;
    fixed-width 32-bit little-endian words). Encoders: the move-wide family
    (`movz`/`movk`/`movn`), `add`/`sub` immediate + register, `mov` reg
    (`orr Xd, XZR, Xm`), `svc`, `ret` — each byte-checked against the Go
    reference (pinned to llvm-mc) via `TestSelfHostArm64Encode`. Gated
    end-to-end by **`TestSelfHostArm64DarwinMachOExitRuns`**, the first
    arm64-darwin proof with **no external tool**: a Fern program assembles
    `exit(42)` (`movz x0,#42` / `movz x16,#1` / `svc #0x80`) to machine
    code and wraps it with `macho.fern` into an ad-hoc-signed static
    Mach-O; the test asserts `debug/macho` parses it as an arm64
    `MH_EXECUTE` (structural on the Linux box) and, on Apple Silicon,
    executes it and checks exit 42 — the whole chain (Fern encoder →
    Mach-O writer + signature → kernel → `svc`) with no `clang`/`ld64`/
    `codesign`. Forward references, named labels, and the wider
    instruction surface (loads/stores, the `@PAGE`/`@PAGEOFF` literal
    addressing the full backend needs) are later slices.
  - ✅ **slice 3b — compare + control flow (backward branches)**:
    `cmp` (reg / imm, as `subs XZR, …`) and the branch family `b` /
    `b.cond` / `cbz` / `cbnz`, plus the signed condition codes
    (`eq`/`ne`/`lt`/`ge`/`gt`/`le`). Branch targets are PC-relative byte
    deltas (÷4 inside the encoder); a *backward* branch knows its target
    when emitted (`target_off - buf.len()`), so loops assemble without a
    label table. Byte-checked by `TestSelfHostArm64Branches` and gated
    end-to-end by **`TestSelfHostArm64DarwinMachOLoopRuns`**: a Fern
    program assembles a `6 × 7` loop (`add`/`sub` + a backward `cbnz`),
    wraps it with `macho.fern`, and the signed Mach-O exits 42 — still no
    external tool. Forward references / named labels are the next slice.
  - ✅ **slice 3c — forward references (placeholder + patch)**: a forward
    branch is emitted with a zero displacement, its byte offset recorded,
    then the immediate is spliced in once the target is known —
    `arm64_rel` (byte delta) + `arm64_patch_b` (imm26) / `arm64_patch_b19`
    (imm19, shared by `b.cond`/`cbz`/`cbnz`), the splicers preserving the
    opcode/cond/Rt bits via a read-modify-write of the 4-byte word.
    Byte-checked by `TestSelfHostArm64ForwardRefs` and gated end-to-end by
    **`TestSelfHostArm64DarwinMachOMaxRuns`**: a Fern program assembles
    `max(42, 17)` (`cmp; b.ge done; mov; done:`) where the *taken* forward
    `b.ge` skips the `mov`, wraps it with `macho.fern`, and the signed
    Mach-O exits 42 — no external tool. A named-label table over these
    primitives (so multiple forward labels + calls resolve by name) is the
    next slice.
  - ✅ **slice 3d — named-label assembler**: an `Arm64Asm` struct
    (`code` + a label table + a fixup queue with per-fixup kind: imm26 for
    `b`/`bl`, imm19 for `b.cond`/`cbz`/`cbnz`), the arm64 counterpart of
    `X86Asm`. `arm64_asm_label` records a name at the current offset;
    `arm64_asm_b`/`bl`/`bcond`/`cbz`/`cbnz` branch to a (possibly forward)
    name — patched immediately if already placed, else queued;
    `arm64_asm_resolve` patches the rest once everything is placed. Adds
    `bl` (branch-with-link). Byte-checked by `TestSelfHostArm64Labels`
    (forward `b`/`b.cond`/`bl` via resolve + backward `cbnz` patched
    inline) and gated end-to-end by **`TestSelfHostArm64DarwinMachOCallRuns`**:
    a Fern program assembles `_main { bl compute; exit(x0) }` /
    `compute { loop 6 × 7; ret }` — a forward call + a backward loop by
    name — wraps it with `macho.fern`, and the signed Mach-O exits 42 with
    no external tool.
  - ✅ **slice 3e — loads / stores (stack frame)**: `ldr` / `str` Xt,
    [Xn, #off] (64-bit, unsigned scaled immediate; register 31 names SP),
    plus an `arm64_sp` helper. Byte-checked by `TestSelfHostArm64LoadStore`
    and gated end-to-end by **`TestSelfHostArm64DarwinMachOFrameRuns`**: a
    Fern program assembles a stack-frame round-trip (`sub sp`; `movz`;
    `str x0,[sp,#8]`; clobber; `ldr x0,[sp,#8]`; `add sp`), wraps it with
    `macho.fern`, and the signed Mach-O exits 42 — no external tool.
  - ✅ **slice 3f — @PAGE / @PAGEOFF data addressing**: `adrp Xd,
    sym@PAGE` + `ldr Xt, [Xn, sym@PAGEOFF]` to reach a `__DATA` constant,
    the immediates computed from macho.fern's fixed segment addresses
    (`arm64_page_delta` / `arm64_page_off` over `macho_text_vaddr` /
    `macho_data_vaddr` — the first use of the `SegmentAddrs` parity), plus
    `arm64_patch_adrp` / `arm64_patch_ldr_off` splicers. Byte-checked by
    `TestSelfHostArm64Adrp` and gated end-to-end by
    **`TestSelfHostArm64DarwinMachODataRuns`** (the arm64 rodata test): a
    Fern program lays a `.quad 42` in `__DATA`, loads it via `adrp`+`ldr`,
    and the signed Mach-O exits 42 — no external tool. The encoder now has
    the addressing the full backend needs.
  - ✅ **slice 3g — GAS-text assembler**: `examples/self_host/arm64_gas.fern`,
    the arm64 counterpart of `x86_gas.fern` — it parses an AArch64
    assembly-text subset (the canonical GAS spellings: `mov`/`movz`/`movk`,
    `add`/`sub` imm+reg, `cmp`, `ldr`/`str [Xn,#off]`, `b`/`bl`/`b.<cond>`/
    `cbz`/`cbnz` by label, `svc`, `ret`; `name:` labels; `//` comments;
    directives ignored) into machine code via the `arm64_encode` encoders +
    `Arm64Asm` label machinery (bracket-aware operand split, `#imm`/`0x`
    parsing, register aliases `sp`/`lr`/`xzr`). Byte-checked by
    `TestSelfHostArm64Gas` and gated end-to-end by
    **`TestSelfHostArm64DarwinMachOGasRuns`**: a Fern program feeds an
    assembly-text program (subroutine call + backward loop by label) to
    `arm64_gas_assemble`, wraps the bytes with `macho.fern`, and the signed
    Mach-O exits 42 — the first time the arm64-darwin path goes from
    **assembly text → runnable signed binary** with no external
    `as`/`clang`/`ld64`.
  - ✅ **slice 3h — frame prologue/epilogue ops**: the instruction surface
    a *real function* emits — `stp`/`ldp` with writeback (`[Xn, #off]!`
    pre / `[Xn], #off` post), single `ldr`/`str` writeback (signed imm9),
    and the `mov Xd, sp` / `mov sp, Xn` add-immediate alias (ORR can't name
    SP) — added to `arm64_encode.fern` (encoders pinned vs llvm-mc) and
    parsed by `arm64_gas.fern` (a `!`-tolerant memory parser + the pre/post
    operand-count dispatch). Byte-checked by `TestSelfHostArm64FrameGas`
    and gated end-to-end by **`TestSelfHostArm64DarwinMachOFnGasRuns`**: a
    Fern program assembles the actual prologue/epilogue + frame idiom the
    backend emits (`stp` … `mov x29, sp` … push/pop via `str`/`ldr`
    writeback … `ldp` … `ret`, called by `bl`), wraps it with `macho.fern`,
    and the signed Mach-O exits 42 — no external tool.
  - ✅ **slice 3i — data section + named-symbol relocation**: `arm64_gas`
    gains an `Arm64GasProg` (the text assembler + a `__DATA`/`__const`
    blob + a data-symbol table + a page-fixup queue). The data directives
    `.quad` / `.4byte` / `.word` / `.byte` / `.asciz` / `.string` /
    `.align` build the blob; `.section __TEXT,__const` / `.data` switch
    section; a label in the data section records a data symbol. `adrp
    sym@PAGE`, `ldr [Xn, sym@PAGEOFF]`, and `add Xn, sym@PAGEOFF` queue
    fixups that `arm64_gas_link(p, text_vaddr, data_vaddr)` resolves once
    macho.fern's segment addresses are known (`arm64_patch_adrp` /
    `_ldr_off` / `_addimm_off`). Byte-checked by `TestSelfHostArm64DataGas`
    and gated end-to-end by **`TestSelfHostArm64DarwinMachOSymbolRuns`**: a
    Fern program assembles text that defines a `.quad 42` in `__const` and
    loads it *by name* (`adrp`/`ldr` `@PAGE`/`@PAGEOFF`), links the fixups,
    wraps it with `macho.fern`, and the signed Mach-O exits 42 — a real
    cross-segment symbol reference, no external tool. (Found + fixed a
    self-host gotcha: a local named `as` collides with the cast keyword and
    silently mis-compiles.)
  - ✅ **slice 3j — runtime instruction surface (neg / ubfx / tbz/tbnz /
    conditions)**: dumping the backend's *actual* arm64-darwin asm for
    `print("hi")` showed the gap to assembling real output is not
    `.ltorg`/`ldr =` (unused — addresses go via `adrp`/`add`) but the
    `__fern_puts` runtime's ops: `neg` (the `sub Xd, XZR, Xm` alias),
    `ubfx` (UBFM alias), `tbz`/`tbnz` (test-bit branch, a new imm14 label
    fixup kind), and the full condition-code set (`cc`/`cs`/`hs`/`lo`/`hi`/
    `ls`/`mi`/`pl`/`vs`/`vc`/`al`). Encoders pinned vs llvm-mc; parsed by
    `arm64_gas`. Byte-checked by `TestSelfHostArm64BitOpsGas` and gated
    end-to-end by **`TestSelfHostArm64DarwinMachOBitOpsRuns`**: a program
    computes 42 via `ubfx` + `neg` + a `tbz` branch and the signed Mach-O
    exits 42 — no external tool.
  - ✅ **slice 3k — assemble the compiler's *real* emitted asm**: the
    capstone. **`TestSelfHostArm64DarwinMachORealAsm`** takes the backend's
    actual arm64-darwin assembly (from `internal/codegen/arm64`, which
    `asm_arm64.fern` mirrors) for real Fern programs — `return 42`,
    `6 * 7`, recursive `fib(10)` — feeds it to `arm64_gas_program` +
    `arm64_gas_link` + `macho.fern`, and the result is a valid arm64
    `MH_EXECUTE` (every host; exit-code-checked on Apple Silicon). To make
    the structural check trustworthy (a dropped instruction yields a
    well-formed-but-wrong binary), `Arm64GasProg` now records any
    `unknown` mnemonic and the driver surfaces it. That flushed out four
    silently-missing ops — `mul` (MADD alias), `ldur`/`stur` (unscaled
    frame load/store), and `cset` (CSINC alias) — now added (pinned vs
    llvm-mc). The arm64-darwin path now assembles real compiler output to a
    runnable signed binary with no `as`/`clang`/`ld64`.
  - ✅ **slice 3l — coverage widening (strings / structs / arrays /
    options)**: drove `TestSelfHostArm64DarwinMachORealAsm` with richer
    real programs — `Option`/`match`, string concat, a struct method, an
    array sum loop — and added every instruction their real asm needs
    (each gap surfaced by the `unknown`-guard, then encoded + pinned vs
    llvm-mc): `lsl`/`lsr` immediate (x + w forms, UBFM aliases), `cmn`
    (ADDS-to-XZR), `csel`, register `and`, the no-dot branch aliases
    (`bhi`/`blt`/`bne`/…), and `and Xd, Xn, #imm` for the 16-byte alloc
    alignment mask (`#-16`, fields verified vs llvm-mc; an unsupported
    bitmask immediate is recorded by the guard, never mis-encoded — a full
    AArch64 bitmask-immediate encoder is the follow-up). The capstone now
    assembles 7 real programs (incl. the heap/alloc prologue) to valid
    arm64 Mach-O.
  - ✅ **slice 3m — full AArch64 bitmask-immediate encoder**:
    `arm64_encode_bitmask` (a faithful port of the Go reference's
    `encodeBitmask`) replaces the single-mask lookup, so any legal
    `and Xd, Xn, #imm` logical immediate encodes — not just `#-16`. Needed
    a few i64 bit primitives Fern lacks natively: a masked logical shift
    right (`>>` is arithmetic), and ctz/clz via bit-test loops. Byte-checked
    vs llvm-mc across a spread of masks (`#-16`, `#-256`, `#7`, `#0xff`,
    `#1`, `#0xf`) plus the legality cases (0 / all-ones / non-bitmask
    rejected) in `TestSelfHostArm64Bitmask`; the `and #imm` guard now keys
    on `arm64_and_imm_ok`.
  - ✅ **slice 3n — merge into one module (`arm64_native.fern`)**: the
    three import-free files (`arm64_encode` / `arm64_gas` / `macho`) were
    designed to be *concatenated* in the self-tests, so they reference each
    other by bare name — which the module loader can't resolve across files
    (it needs `pub` exports + qualified refs). To let the unified `fern`
    CLI `import` the assembler+writer, the three are merged into a single
    `examples/self_host/arm64_native.fern` (same-module bare refs keep
    working; `pub` on the CLI-facing entry points — `arm64_gas_program` /
    `arm64_gas_link` / `macho_executable` / `macho_text_vaddr` /
    `macho_data_vaddr` + the `Arm64Asm` / `Arm64GasProg` structs). The
    e2e self-tests now concatenate the one module via an `arm64NativeSrc`
    helper; behaviour-preserving (all arm64/Mach-O tests stay green).
  - 🔧 **slice 3o — `fern.fern` arm64-darwin wiring (in progress)**: making
    `arm64_native.fern` compile through the **Go front-end** (so the CLI can
    `import` it) surfaced several gaps the self-host wasm pipeline had
    tolerated, now fixed in the module: struct fields are immutable in the
    Go checker (every `x.f = v` rewritten to a `{ ...x, f: v }` rebuild);
    the Go checker has no string `index_of`/`contains`/`starts_with`/`split`
    *methods* (added portable helpers over `.len()` / `s[i]` / `s[a:b]`);
    i64-typed shifts/literals must be explicit (`(1 as i64) << k`); and i32
    literals that exceed the signed range (the Mach-O magics `0xfeedfacf` /
    `0xfade0cc0`, the `__text` flags) need i64-arg emitters (`macho_le32w` /
    `macho_be32w`).
    **Blocker A — struct-update FBIP-reuse miscompile (FIXED, slice 3r).**
    Originally filed as a "Go x86 backend bug": a struct **spread-update of a
    function *parameter*** (`p = T { ...p, field: v }` where the value flows
    through `p`) miscompiled, worked around at the time by binding a local
    copy in every such `arm64_native` function. It turned out NOT to be x86-
    specific — it was a bug in the **shared IR lowering** affecting all three
    compiled backends (x86-64, arm64, wasm; the AST interpreter was correct):
    `tryStructReuseOverwrite` (the FBIP self-overwrite reuse fast path) only
    placed the explicitly-listed `sl.Fields`, so for the spread form the un-
    overridden `sl.Base` fields were left uninitialised on the fresh-alloc
    (rc>1) branch (read back as 0 — nondeterministic, correct only when `p`
    was unique). Fixed in `internal/ir` by deferring the spread form to the
    general StructLit lowering; guarded by `TestStructUpdateParamSpreadReuse`
    (all three backends). The `arm64_native` local-copy workarounds are now
    removed (slice 3s) — the 15 functions take their struct parameter directly
    again. `TestSelfHostArm64NativeViaGoBackend` assembles real
    darwin asm through arm64_native
    compiled by the **Go x86 backend** (the CLI's backend) into valid
    Mach-O — it segfaults without the local-copy fixes, so it guards the
    class. The backend bug itself still wants a dedicated fix. **The
    "second Go-x86 miscompile" turned out to be a misdiagnosis** — see
    slice 3p: the `concat` abort was a real missing-instruction gap in
    `arm64_native` (the signed-offset `ldp` form indexing `ops[3]` out of
    range), not a Go-x86 codegen bug. Fixing it removed the flip blocker.
    **Blocker B — instruction coverage.** The self-host `asm_arm64` emitter
    emits its *full* runtime (incl. float helpers) for **every** program, so
    even `return 42`'s darwin asm uses ~32 mnemonics `arm64_native` must
    cover. Progress (byte-checked vs llvm-mc, `TestSelfHostArm64IntRuntime`):
    the **integer / load-store / system batch** — `orr`, `subs` (reg/imm),
    `udiv`/`sdiv`/`msub`, `rev16`, `ldrb`/`strb`/`ldrh`/`strh`/`ldrsw`, and
    `mrs` of the clock sysregs (`cntvct_el0`/`cntfrq_el0`, unknown sysregs
    guarded); then the **f64 family** (`fadd`/`fsub`/`fmul`/`fdiv`/`fneg`/
    `fcmp`/`fmov` ×3 forms/`fcvtzs`/`scvtf`/`frinta`, with d-register
    parsing) byte-checked by `TestSelfHostArm64Float`. **Coverage is now
    complete** for the base runtime: **`TestSelfHostArm64DarwinAssembles‑
    RealRuntime`** feeds `asm_arm64`'s *actual* darwin output for `return 42`
    / `6*7` / `fib(10)` through `arm64_native` and gets a valid arm64 Mach-O
    with **zero** unknown mnemonics. So the in-Fern path assembles real
    compiler output end-to-end. **Next:** flip `fern.fern`'s
    `-target arm64-darwin` to emit → `arm64_gas` → `macho` (drop the `.s` +
    `clang`/`ld64` path) + rework the flagship `TestSelfHostArm64DarwinBuilds`
    to the no-clang flow; cover any feature-specific instructions the wider
    case list surfaces (the `unknown`-guard makes each a hard failure). (The
    param-spread miscompile is fixed — see Blocker A / slice 3r.)
  - ✅ **slice 3p — large-frame pair forms + register shifts + `sxtw`
    (flip blocker removed)**: the `concat` exit-134 abort previously
    attributed to a "second Go-x86 miscompile" was bisected (gdb
    `catch syscall exit_group` → `arm64_gas_emit`; minimal trigger
    `ldp x27, x28, [sp, #80]`) to a **real gap in `arm64_native`**: the
    `ldp` handler only parsed the 4-operand post-index form
    (`ldp Xt, Xt2, [Xn], #off`) and read `ops[3]` — but `asm_arm64` emits
    the 3-operand **signed-offset** form `ldp Xt, Xt2, [Xn, #off]` for
    large frames (callee-saved regs spilled/reloaded at fixed offsets after
    one `sub/add sp`), so `ops[3]` was an out-of-range index (the bump
    runtime's bounds check exits 134, which reads as SIGABRT). A sweep of
    diverse programs surfaced three more gaps: the `stp Xt, Xt2, [Xn, #off]`
    offset form was silently **mis-encoded as pre-index** (writeback); the
    register (variable) shifts `lsl/lsr/asr Rd, Rn, Rm` (LSLV/LSRV/ASRV,
    64- and 32-bit) were unhandled (`lsl`/`lsr` mis-routed through the
    `#imm` encoder, `asr` wholly unknown); and `sxtw Xd, Wn` (i32→i64
    widening, the SBFM alias) was unknown. All four fixed with byte-pinned
    encoders (`arm64_stp_off`/`arm64_ldp_off`, `arm64_lslv`/`arm64_lsrv`/
    `arm64_asrv`, `arm64_sxtw`) and added to the known-mnemonic guard.
    Byte-checked vs llvm-mc by **`TestSelfHostArm64OffsetPairGas`**;
    guarded end-to-end by **`TestSelfHostArm64NativeViaGoBackend`** (now
    spans `concat` / `i64math` / `bitwise`, which exercise these forms and
    previously aborted). This unblocks the `-target arm64-darwin` flip —
    the in-Fern assembler now produces a valid Mach-O for the real
    `asm_arm64` output of every program in the sweep under the CLI's Go x86
    backend, with zero unknowns and no bounds aborts.
  - ✅ **slice 3q — the flip: `fern.fern -target arm64-darwin` emits the
    Mach-O binary in-process**: `fern.fern` now `import`s `arm64_native` and
    its `arm64-darwin` branch runs `asm_arm64.darwinize(...)` →
    `arm64_gas_program` (unknown-instruction guard → `eprint` + exit 2) →
    `macho_text_vaddr`/`macho_data_vaddr` → `arm64_gas_link` →
    `macho_executable`, packing the signed Mach-O bytes into the
    output string written verbatim by `write_file`. **The `.s` + `clang`/
    `ld64` path is gone** — `-target arm64-darwin` produces a runnable,
    ad-hoc-signed arm64 executable with no external toolchain, matching the
    Go native backend's container exactly (`__PAGEZERO`/`__TEXT`/`__DATA`/
    `__LINKEDIT` + `LC_UNIXTHREAD` + `LC_CODE_SIGNATURE`; verified by
    disassembling fib/concat — every instruction decodes, branch + @PAGE
    relocations resolve to the right addresses). A pre-flip sweep through a
    dedicated `asm_arm64_darwin_run.fern` emitter found and closed the last
    instruction gap, `eor` (register 64/32-bit + the `#imm` boolean-not
    idiom, byte-pinned in `TestSelfHostArm64OffsetPairGas`); the collector
    confirmed **zero** unknown mnemonics across all 24 flagship programs
    (incl. the fs-builtins lifecycle, subprocess, and socket cases). Test
    rework: the flagship **`TestSelfHostArm64DarwinBuilds`** now emits the
    binary directly (no clang link) — structural Mach-O check on Linux,
    chmod + execute + exit-code check on the macOS arm64 runner;
    **`TestSelfHostArm64NativeViaGoBackend`** drives the flipped CLI under
    the Go x86 backend (Linux) over the new-instruction programs; and
    **`TestSelfHostArm64DarwinAssemblesRealRuntime`** sources its darwin text
    from the new emitter (the CLI no longer emits `.s`) to keep the
    wasm-backend coverage of `arm64_native`. The decisive runtime check
    (binaries actually execute on Apple Silicon) runs on the `macos-latest`
    CI runner — the one place arm64-darwin execution can be verified.
  - ✅ **slice 3r — fix the struct-update FBIP-reuse miscompile (Blocker A)**:
    the param-spread bug the arm64_native local-copy workarounds were papering
    over turned out to be a **shared `internal/ir` lowering bug**, not an x86
    backend bug — it hit x86-64, arm64, AND wasm (the AST interp was correct).
    `tryStructReuseOverwrite` (FBIP self-overwrite reuse) only placed the
    explicitly-listed `sl.Fields`; for the spread form `p = T { ...p, f: v }`
    the un-overridden `sl.Base` fields were left uninitialised on the fresh-
    alloc (rc>1) branch — read back as 0, nondeterministically (correct only
    when `p` was uniquely owned and the reuse branch fired). Fixed by bailing
    out of the reuse fast path for spread literals so they take the general
    StructLit lowering (which copies the base's fields correctly). Guarded by
    `TestStructUpdateParamSpreadReuse` across all three compiled backends
    (fails 30/35 + 21/22 without the fix). The arm64_native local-copy
    workarounds are now redundant (removed in slice 3s).
  - ✅ **slice 3s — remove the redundant local-copy workarounds**: with the
    FBIP-reuse miscompile fixed (3r), the 15 `arm64_native` functions that
    bound `var a = a0;` / `var p = p0;` to dodge the param-spread bug now take
    their `Arm64Asm` / `Arm64GasProg` parameter directly again (and spread-
    update it in place). No behaviour change — guarded unchanged by
    `TestSelfHostArm64NativeViaGoBackend` (Go x86 backend),
    `TestSelfHostArm64DarwinAssemblesRealRuntime` (wasm), the byte-pinned
    `*Gas` tests, and the flagship `TestSelfHostArm64DarwinBuilds`.
  - ✅ **slice 3t — literal pool + negative-offset load/store (toward
    arm64-Linux ELF; also fixes the darwin path)**: running asm_arm64's *raw
    Linux* output (`emit_module(false)`) through arm64_native surfaced two
    instruction-selection bugs that had been silently mis-assembled — the
    darwin tests never caught them because they only check exit codes / Mach-O
    parseability, and the flagship `TestSelfHostArm64DarwinBuilds` *skips* on
    macOS when an emitted binary segfaults (so the broken binaries read as
    green skips). (1) `ldr Xd, =N` + `.ltorg`: the PC-relative **literal
    pool** asm_arm64 uses for every integer immediate was entirely
    unimplemented — `ldr x0, =42` was assembled as `ldr x0, [x0]` and `.ltorg`
    dropped. Added LDR-literal encoding (0x58…) + a per-program pending-pool
    (`lit_sites`/`lit_vals`, flushed 8-aligned at each `.ltorg` and at end of
    program), byte-pinned in `TestSelfHostArm64LitPoolGas`. (2)
    `str/ldr Xt, [Xn, #neg]`: a negative (or non-8-aligned) frame offset
    can't use the scaled unsigned form (`[x29, #-8]` mis-encoded as
    `[x29, #0x7ff8]`); now falls back to the unscaled signed-imm9 `stur/ldur`,
    as GAS does. **New end-to-end proof:**
    `TestSelfHostArm64NativeLinuxElfRuns` assembles asm_arm64's real Linux
    output through arm64_native + `elf.fern` and **runs the ELF under
    qemu-aarch64** (exit42 / arith / fib, correct exit codes) — the first time
    the in-Fern assembler's output is *executed* on the Linux CI box (the
    darwin path can only be run on a macOS runner). **Next:** `:lo12:` add
    parsing, `.bss`/`.skip` heap reservation (ELF memsz), and the rodata
    `.ascii` path, then flip `fern.fern -target arm64` to emit ELF directly.
  - ✅ **slice 3u — `:lo12:` / `.ascii` / `.double` / `.bss` + array/string
    instruction forms (arm64-Linux runs the common surface)**: closed the
    remaining gaps so asm_arm64's real Linux output runs end-to-end under
    qemu-aarch64. Directives: `add Xd, Xn, :lo12:sym` (the ELF low-12-bits
    relocation, reusing the same kind-2 fixup as darwin's `@PAGEOFF`),
    `.ascii` (no-NUL string), `.double` (decimal → IEEE-754 via a parse_f64 +
    `f64_bits`, reusing `arm64_gas_data_le`), and a real `.bss` section
    (`bss_size` + bss-symbol table; `.skip`/`.zero`/`.quad`/`.align` reserve
    memsz, not file bytes — the 1 GiB `__fern_heap` is zero-fill; bss symbols
    resolve to `data_vaddr + data.len() + bss_off`). Instruction forms two
    bugs hid (they mis-assembled silently): `ldr/str Xt, [Xn, Xm, lsl #3]`
    (register-offset **array indexing** — was parsed as `[Xn]`, dropping the
    index) and `ldrb/strb Wt, [Xn], #1` (post-index **byte copy** in
    `__fern_str_concat` — ignored the writeback). Both also affected darwin
    (arrays + string concat). Byte-pinned in `TestSelfHostArm64LitPoolGas`;
    `TestSelfHostArm64NativeLinuxElfRuns` now runs print / concat / array /
    string-build under qemu (rodata strings, `:lo12:`, the heap, indexing,
    byte copy) in addition to exit42 / arith / fib. **Next:** flip
    `fern.fern -target arm64` to emit the ELF in-process (sweep the wider
    case list for any remaining forms, then drop the `.s` + gcc path). A
    follow-up sweep (structs/enums/options/i64/floats) found one more silent
    mis-encode — `ldr/str Dt, [Xn{, #off}]` (the SIMD&FP f64 load/store) was
    encoded as a general-register load, so floats never reached d-registers;
    fixed (`arm64_ldr_fp`/`arm64_str_fp`), byte-pinned, and `floats` runs
    under qemu. (Darwin floats were broken by this too.)
  - ✅ **slice 3v — the flip: `fern.fern -target arm64` emits the ELF
    in-process**: `fern.fern` now `import`s `elf` and routes **both** emit
    paths through `arm64_elf_binary(asm)` — the SSA path (`ssa_arm64`, the
    default for `-target arm64`) and the AST fallback (`asm_arm64.emit_module`)
    — which runs `arm64_gas_program` (unknown guard → `eprint` + exit 2) →
    `arm64_gas_link` (text_vaddr = `elf_text_vaddr()`, data 8-aligned after
    `.text`) → `elf_image_entry_bss` (entry = `_start`, memsz tail = the bss
    heap), packing the runnable ELF into the output string. **The `.s` + gcc/
    ld path is gone** for `-target arm64`. A pre-flip sweep confirmed the SSA
    path needed one extra form — `sbfiz Xd, Xn, #lsb, #width` (scaled-index
    addressing of an i32; SBFM alias) — now handled. Decisive proof:
    **`TestSelfHostArm64LinuxBuilds`** builds the flipped CLI (Go x86 backend)
    and **runs** 11 programs (incl. structs / options / enums / arrays /
    strings / floats) as arm64 ELF under **qemu-aarch64**, checking exit codes
    — the arm64-Linux path executes on the Linux CI box (the darwin path can
    only run on a macOS runner). The cli test's `emit-target-arm64` and the
    macho / gobackend stagings add `elf.fern`; `arm64_native.arm64_asm_label_off`
    and the `elf_*` helpers are now `pub`. Both native arm64 targets (Linux
    ELF + Darwin Mach-O) now emit runnable binaries with no external toolchain.
- 🔧 **x86-64 assembler** — Intel-syntax asm text → machine-code bytes,
  mirroring `internal/native/x86_64/` (`asm.go` + `parse.go` + `sse.go`
  + `x87.go` + `rodata.go`). The largest piece; built up in slices.
  - ✅ **slice 2a — encoding primitives + integer/syscall subset**:
    `examples/self_host/x86_encode.fern` (`i32[]` byte-buffer convention;
    REX.W prefix, ModR/M direct form, imm32/disp32 LE) with the
    instruction encoders `mov r32, imm32` / `mov r64, r64` /
    `add`/`sub r64, r64` / `push`/`pop r64` / `syscall` / `ret`, each
    byte-checked against `as`/objdump (`TestSelfHostX86Encode`). Gated
    end-to-end by `TestSelfHostX86ElfExitRuns`, the first true proof of
    the track: a Fern program assembles `exit(42)` to machine code,
    wraps it with `elf.fern`, and the binary **runs natively on x86-64**
    (exit 42) — no external assembler or linker.
  - ✅ **slice 2b — immediate ALU + control flow**: `add`/`sub`/`cmp
    r64, imm32` (group-1 `0x81 /digit`), `cmp r64, r64`, and near
    branches `jcc`/`jmp rel32` (`0F 8x` / `0xE9`) with the rel32 math
    helper `x86_branch_rel`. Byte-checked against `as`/objdump
    (`TestSelfHostX86Encode`) and gated end-to-end by
    `TestSelfHostX86LoopRuns`: a Fern program assembles a real loop
    (acc=0; repeat 7×: acc += 6; exit(acc)) with a backward `jne`, and
    the binary **runs natively** exiting 42. Backward branches resolve
    directly (target known); forward branches still await the label
    table (next slice).
  - ✅ **slice 2c — forward references + `call`**: `call rel32` (`0xE8`)
    and `x86_patch_rel32` — emit a branch with a placeholder rel32, record
    its field offset, and patch it once the target is known (`x86_rel_to`
    does the `target - (patch_off + 4)` math). Built on the self-host's
    array element-assignment (`buf[i] = v`); this is the mechanism the
    text assembler's label table will sit on. Byte-checked
    (`TestSelfHostX86Encode`) plus two end-to-end native runs:
    `TestSelfHostX86MaxRuns` (forward `jge` over an else-arm → max(42,17))
    and `TestSelfHostX86CallRuns` (forward `call` to a later subroutine +
    `ret`).
  - ✅ **slice 2d — named-label assembler**: an `X86Asm` struct (code
    buffer + parallel label name/offset arrays + a forward-fixup list) with
    `x86_label` / `x86_jcc_label` / `x86_jmp_label` / `x86_call_label` /
    `x86_resolve`. Branches name a label; backward targets patch
    immediately, forward ones queue and `x86_resolve` patches them via
    `x86_patch_rel32`. This is the API the GAS-text parser will call
    instead of hand bookkeeping. Byte-checked (`TestSelfHostX86Labels`:
    forward/backward branch + call + lookup) and run end-to-end
    (`TestSelfHostX86LabelProgramRuns`: a two-routine program — `main`
    calls `compute`, which loops to 42 and returns — assembled entirely
    through the label API, runs natively exiting 42).
  - ✅ **slice 2e — memory operands**: `mov r64, [base+disp]` /
    `mov [base+disp], r64` (`0x8B`/`0x89` /r) via `x86_emit_mem` + `x86_sib`,
    handling the mod 00/01/10 disp sizing and the two special cases —
    `rsp` (SIB escape) and `rbp` (no mod=00 form, so `[rbp]` is mod=01
    disp8=0). Byte-checked (`TestSelfHostX86Encode`) and run end-to-end
    (`TestSelfHostX86FrameRuns`: a stack-frame round-trip — store 42 to
    `[rbp-8]`, clobber, reload, exit — runs natively exiting 42).
  - ✅ **slice 2f — `.rodata` + rip-relative addressing**: a `.rodata`
    section on `X86Asm` (cross-section labels: `x86_rodata_label` /
    `x86_rodata_quad`), `lea r64, [rip+label]` (`x86_lea_rip_label`,
    `48 8D` + ModR/M mod=00 rm=101), and `x86_resolve` extended to place
    `.rodata` labels at the padded `.text` length and patch rip-relative
    fixups (same `target - (next+4)` math as branches, since the whole
    image is one segment). This is the addressing mode `asm.fern` uses
    pervasively (`leaq .S<n>(%rip)` for the string pool, function
    addresses, floats, `__fern_argc`). Byte-checked (`TestSelfHostX86Labels`)
    and run end-to-end (`TestSelfHostX86RodataRuns`: `lea rax,[rip+answer]`;
    `rax=[rax]`; exit — a `.quad 42` in `.rodata`, R+W+X data ELF, runs
    natively exiting 42).
  - ✅ **slice 2g — GAS-text front-end**: `examples/self_host/x86_gas.fern`
    parses the AT&T assembly `asm.fern` emits and drives the encoders +
    label/`.rodata` API. Covers the core integer/pointer subset — directives
    `.text` / `.section .rodata` / `.globl` / `.quad`, labels, and `movq`
    (imm/reg/reg-reg/load/store), `leaq sym(%rip)`, `addq`/`subq`/`cmpq`,
    `pushq`/`popq`, `jmp`/`jCC`, `call`, `ret`, `syscall`, `leave` — with
    operand forms `%reg` / `$imm` / `disp(%base)` / `sym(%rip)` / bare
    label, in AT&T `src, dst` order. Operand parsers byte-checked
    (`TestSelfHostX86Gas`); two end-to-end native runs assemble hand-written
    GAS **text** → machine code → ELF → run on x86-64 exiting 42:
    `TestSelfHostX86GasLoopRuns` (a loop) and `TestSelfHostX86GasRodataRuns`
    (a `.section .rodata` `.quad` loaded via `leaq sym(%rip)`). *Found on
    the way:* the self-host string `trim` strips only spaces, not tabs, so
    the front-end carries its own tab-aware `x86_gas_trim` (asm.fern indents
    with tabs).
  - ✅ **slice 2h — integer mnemonic coverage**: added the high-frequency
    64-bit ops `incq`/`decq`/`negq` (`0xFF`/`0xF7` group), `testq`,
    `andq`/`orq`/`xorq` (reg-reg `0x21`/`09`/`31` + the `$imm` group-1
    forms), `imulq` (`0F AF`), `idivq`/`divq` + `cqto`, and `shlq $imm8`.
    Byte-checked vs `as`/objdump (`TestSelfHostX86Encode`) and three native
    runs through the GAS front-end: `TestSelfHostX86GasMulRuns` (`imulq`,
    6·7), `…IncShlRuns` (`incq`+`shlq`), `…DivRuns` (`cqto`+`idivq`, 84/2).
  - ✅ **slice 2i — extended registers r8..r15**: added `x86_rex` (REX
    prefix with dynamic R/X/B bits) and threaded it through every encoder
    (reg-reg movs/ALU/test/imul, unary inc/dec/neg/div/idiv, push/pop, the
    B8 imm32 mov, load/store, lea-rip) so any operand can be r8..r15. The
    existing `base & 7` memory logic already handled r12 (SIB escape, like
    rsp) and r13 (no-mod0, like rbp); only REX.B was missing. The GAS
    front-end parses `%r8`..`%r15`. Surfaced by surveying real `asm.fern`
    output, which uses r12/r13 as frame/string bases pervasively.
    Byte-checked (`TestSelfHostX86Encode`, r0..r7 cases unchanged) and two
    native runs: `…GasExtRegRuns` (`imulq %r13,%r12`) and `…GasExtMemRuns`
    (store `%r8` to `[rsp]`, reload into `%r9`).
  - ✅ **slice 2j — SIB index addressing**: `[base + index*scale + disp]`
    via `x86_emit_mem_idx` + `x86_scale_bits` (scale 1/2/4/8 → 0/1/2/3),
    with `x86_mov_load_r64_idx` / `x86_mov_store_r64_idx` (REX.X for an
    index >= 8). The GAS front-end parses `disp(%base,%index,scale)`, and
    crucially the operand splitter is now **paren-aware**
    (`x86_gas_top_comma` skips commas inside `(%b,%i,s)`). Byte-checked
    (`TestSelfHostX86Encode`, `TestSelfHostX86Gas`) and a native run
    (`TestSelfHostX86GasIndexRuns`: store 42 at `[rsp+rcx*8]`, reload).
  - ✅ **slice 2k — byte / 8-bit ops**: `movb` ($imm/reg8 → mem, mem →
    reg8; `0xC6`/`0x88`/`0x8A`), `movzbq` (mem/reg8 → r64, REX.W `0F B6`),
    `movl $imm` (the `0xB8` 32-bit mov), and `cmpb $imm8, %reg8` (`0x80 /7`),
    plus an 8-bit register parser (`%al`..`%dil`, `%r8b`..`%r15b`). Byte ops
    reuse the ModR/M+SIB encoders with a W=0 REX emitted only when needed
    (extended reg/base/index, or spl..dil). Byte-checked
    (`TestSelfHostX86Encode`, `TestSelfHostX86Gas`) and two native runs:
    `…GasByteImmRuns` (`movb $42`+`movzbq`) and `…GasByteRegRuns`
    (`movb %cl`+`movzbq` into `%r8`). With this the integer surface
    `asm.fern` emits is essentially covered.
  - ✅ **slice 2l — capstone: assemble asm.fern's real output**. Surveying
    `asm.fern`'s emit for a real program surfaced the last integer gaps,
    all now added: `push`/`pop` of a **memory** operand (`0xFF /6` / `0x8F
    /0`), the **3-operand** `imul $imm, %src, %dst` (`0x69 /r id`), and
    ALU with a **memory source** (`add`/`sub`/`cmp %reg, mem` → `0x03`/
    `0x2B`/`0x3B /r`). `elf.fern` gained an **entry-offset** image
    (`elf_image_entry` / `elf_static_executable_data_x86_at`) because
    `asm.fern` emits `__fn_main` before `_start`, so the ELF entry is the
    `_start` label's offset, not 0. `TestSelfHostX86Capstone` is the
    milestone: it compiles a real Fern program with `asm.fern`, feeds the
    emitted AT&T text through `x86_gas_assemble` + `elf.fern`, and runs the
    binary **natively on x86-64 exiting 42 — no external `as` or `ld`**.
  - ✅ **slice 2m — setCC + broadened capstone**. Real programs with
    comparisons / loops surfaced the missing `setCC` (`asm.fern` does
    `cmpq` → `setl %al` → `movzbq` to materialise a bool); without it every
    comparison produced garbage. Added `x86_setcc_reg8` (`0F 90+cc /0`) and
    the `set{e,ne,l,le,g,ge,b,be,a,ae,s,ns}` mnemonics. `TestSelfHostX86Capstone`
    is now table-driven over **arithmetic, while loops, if/else, function
    calls, and recursion** (`fib`) — every case compiles via `asm.fern`,
    assembles through the Fern toolchain, and runs natively exiting 42.
  - ✅ **slice 2n — SSE double (f64) support**. Added the scalar-double
    SSE surface `asm.fern` emits for `f64`: the `.double` directive (a
    decimal-float parser → `f64_bits` → 8 IEEE-754 bytes in `.rodata`),
    `movsd sym(%rip)` (+ mem load/store), `movq` xmm↔gpr (`66 REX.W 0F
    6E/7E`), `addsd`/`subsd`/`mulsd`/`divsd` (`F2 0F 58/5C/59/5E`),
    `cvttsd2si` / `cvtsi2sd`, and `ucomisd`, plus an xmm register parser.
    The GAS front-end also learned **same-line `label: directive`**
    (asm.fern emits `.L0: .double 84.0` on one line). `TestSelfHostX86Capstone`
    gains a `float` case (`84.0 / 2.0` → 42) that runs natively.
  - ✅ **slice 2o — strings (`.ascii` + `push $imm`)**. A `write("…")`
    program surfaced two gaps: the `.ascii "…"` directive (string bytes in
    `.rodata`, with the common C escapes) and `push $imm` (`0x68 id`).
    Added `x86_push_imm32` + `x86_gas_ascii`. `TestSelfHostX86Capstone`
    gains a `string` case (`write("hi!")`) that asserts **stdout** "hi!"
    (not just exit code) from the self-assembled native binary.
  - 🔧 **CLI wiring — blocked by the Go-vs-self-host semantic gap.** The
    natural finish line is `fern -target x86-64-elf -o OUT` emitting an ELF
    directly. Attempted (import `x86_encode`/`x86_gas`/`elf` into
    `fern.fern`, dispatch a new target through `x86_gas_assemble` +
    `elf`), but **`fern.fern` is built with the Go backend** in the CLI
    test/bootstrap, and the Go checker rejects these modules: they use
    **mutable struct fields** (`a.code = …`, which Go requires rebuilt as
    `T { ...old, … }`) and **string-method syntax** (`s.contains(…)`,
    `s.index_of(…)`) the Go checker doesn't dispatch. Both are accepted by
    the self-host **wasm** backend (which is why the capstone, compiled via
    `wasm_run`, works). So the path is one of: (a) make the three modules
    Go-checker-compatible (immutable-field rebuilds + Go-supported string
    ops) so the Go-built CLI can import them; or (b) build `fern.fern`
    itself via the self-host backend. Until then the capstone test is the
    end-to-end proof, and the CLI keeps emitting `.s` text.
  - ✅ **slice 2p — movabsq + extra jCC (runtime-batch part 1)**. Probing a
    `struct` program showed `asm.fern` emits its full heap/alloc/memcpy/map
    runtime inline, needing ~12 more instruction forms. First batch:
    `movabsq $imm64` (`REX.W B8+rd io`, with an i64/hex/`-` literal parser
    `x86_gas_atoi64`) and the conditional jumps `js`/`jns`/`ja`/`jae`/`jb`/
    `jbe`/`jp` (new `x86_cc_*` codes feeding the existing jcc encoder).
    Byte-checked vs `as`/objdump.
  - ✅ **slice 2q — runtime batch part 2 (rest of the integer +
    string-op surface)**. Added 32-bit ALU (`movl` reg-reg / load / store,
    `addl`/`subl`/`andl`/`orl`/`xorl`/`cmpl` imm+reg via `x86_alu_r32_imm32`
    / `x86_binop_r32_r32`, `shrl $imm`), `testb` (imm + reg-reg), `cmov<cc>`
    (`0F 40+cc`), `movslq` / `movzwq` (sign/zero-extend loads), the string
    ops `rep stosb` / `rep movsb` + `cld` (memset/memcpy) and `xorpd` (xmm
    zeroing), plus a 32-bit register parser. With slice 2p this covers the
    **full instruction surface `asm.fern`'s heap/alloc/memcpy/map runtime
    emits**. Byte-checked vs `as`/objdump (13 new cases).
  - ✅ **slice 2r — read_file capstone harness**. The capstone driver now
    `read_file`s the asm from a fixed `in.s` at runtime (run under
    `wasmtime --dir`) instead of embedding it as a string literal, so the
    driver compiles **once** and the (previously OOMing) embedded-asm size
    no longer bloats it. All seven small-program cases run through it.
    (While building this I *suspected* three self-host wasm bugs — a
    match-on-`Result` second-arm codegen bug, an `args()` alignment trap,
    and `__fern_alloc` not growing — but **all three were spurious**, later
    retracted in slice 2x: the first two were my own malformed test
    programs (a `match` written without commas between arms, and invalid
    map/empty-Map syntax), and the heap-grow one never reproduced. No
    `wasm.fern` bug was involved.)
  - ✅ **slice 2s — heap-program assembler bugs (found via bisection)**.
    Bisecting the struct program's asm pinned two real bugs that corrupted
    / mis-encoded heap-program assembly:
      1. **`movsd %xmm,%xmm` (reg-reg)** wasn't handled — it fell into the
         load branch, which called `parse_mem("%xmm0")`; `parse_mem`'s
         `op[0:lp]` with `lp = index_of("(") = -1` built a corrupt slice
         (`op[0:-1]`) → heap corruption → trap. Fixed with `x86_movsd_rr`
         (`F2 0F 10 /r`) + a `parse_mem` guard for the no-`(` case.
      2. **rip-relative `movq`** — `movq sym(%rip), %rax` / `movq %rax,
         sym(%rip)` (the heap-pointer global access in `__fern_alloc`) was
         encoded as `mov rax, [rax]` (the `is_rip` flag was ignored). Fixed
         with `x86_mov_load_rip_label` / `x86_mov_store_rip_label`
         (`48 8B`/`89` + ModR/M rm=101 + deferred fixup). All byte-checked.
    With these, the struct asm now **assembles** (no corruption); the
    remaining gap is purely data-section support (next).
  - ✅ **slice 2t — `.bss` section + ELF memsz → HEAP PROGRAMS RUN.**
    `asm.fern` declares its globals in `.section .bss` (`.align 8`,
    `__fern_heap_ptr: .quad 0`, `__fern_heap: .skip 1073741824` — a **1 GB**
    heap) and rip-addresses them. Added: a third section in the front-end
    (`.section .bss`/`.data`), the `.align` / `.skip` / section-aware
    `.quad` directives, `.bss` labels (`x86_bss_label` / `x86_bss_skip`,
    a running `bss_size`), `x86_final_off` placing `.bss` labels past
    `.rodata`, and `elf_static_executable_bss_x86_at` / `elf_image_entry_bss`
    setting **`p_memsz = p_filesz + bss`** (the 1 GB bss is zero-init
    memory, not file bytes). **`TestSelfHostX86Capstone` now runs `struct`
    and `array` natively (exit 42)** — heap-using programs compile
    `asm.fern` → `x86_gas` → `elf` → a runnable ELF with **no external `as`
    or `ld`**. The capstone now spans arithmetic / control-flow / calls /
    recursion / float / string / **struct / array**.
  - ✅ **slice 2u — `movslq` reg-reg → strings work**. Probing richer
    programs found `s.len()` returned 0: `asm.fern` widens the i32 length
    with `movslq %eax, %rax` (reg-reg), but the dispatch only had the
    *memory* form and sent `%eax` to `parse_mem` (→ a bogus base) → garbage.
    Added `x86_movslq_rr` (`48 63 /r`) + reg-vs-mem dispatch. `strlen` and
    `strchar` capstone cases now run natively (the capstone is up to **11
    program shapes**). Probing also confirmed **multi-function** programs
    (`f3∘f2∘f1`) and `fizzbuzz` (`%`/`if` chains) already work.
  - ✅ **slice 2v — maps work (no bug after all)**. The earlier
    "map returns 254" was a **malformed test program** — `Map[string,i32]{}`
    is a syntax error even in the Go compiler, so `asm.fern` emitted garbage
    (the gcc-assembled reference exited 254 too). With the correct literal
    `Map { k: v }`, both i32-keyed and string-keyed maps assemble + run
    natively to 42 — the full FNV-hash / open-addressing runtime works
    through the assembler. Added `mapi32` / `mapstr` capstone cases: the
    capstone is now **13 program shapes** (arith, while, ifelse, call,
    recur, float, string, struct, array, strlen, strchar, map×2) — the
    whole core-language surface, all assembled by the self-host toolchain
    and run natively with no external `as`/`ld`.
  - ✅ **slice 2w — SSE float-math encoders (`sqrtsd` / `roundsd`)**. Added
    `x86_sqrtsd` (`F2 0F 51 /r`) and `x86_roundsd` (`66 0F 3A 0B /r ib`,
    the 3-operand `$mode, %src, %dst`) — the ops `asm.fern`'s `__sqrt_f64` /
    `__floor_f64` / `__ceil_f64` / `__trunc_f64` builtins emit. Byte-checked
    vs `as`/objdump. *Found:* the user-facing f64 methods `.sqrt()` /
    `.floor()` / `.ceil()` / `.trunc()` are an **`asm.fern` gap** — it emits
    `call __fn_f64__sqrt` etc. without emitting those method bodies (an
    undefined reference even for gcc's linker), so they can't run e2e yet
    (a self-host emitter bug, not the assembler). The inline `__*_f64`
    builtins (used by `std/math`) do emit the encoders, so a `std/math`
    program would exercise them once multi-module assembly is in the
    capstone.
  - ✅ **slice 2x — retract the spurious "wasm bugs"**. Verified the two
    `wasm.fern` "bugs" from slice 2r were **my malformed test programs**,
    not real bugs: a properly-comma'd two-arm `match (read_file(p)) {
    Ok(s) => …, Err(e) => … }` compiles + runs fine (the passing
    differential cases `result-ok`/`result-err` always had the commas;
    mine omitted them), and `args()` works in well-formed code (the earlier
    trap was downstream of the comma-less match in the same driver). The
    map "bug" (slice 2v) was likewise invalid syntax. So **no self-host
    wasm bug was ever involved** — corrected the plan doc + the capstone
    driver comment. The driver keeps its single-arm `Ok` + fixed-path form
    (simplest; the file always exists), now for clarity rather than to
    dodge a bug.
  - ✅ **slice 2y — `read_file` 64 KiB truncation fix (a REAL wasm bug)**.
    Pushing the capstone toward larger programs (toward assembling
    `asm.fern`'s own output) hit a hard ceiling at ~64 KiB of asm. Root
    cause, verified directly: the self-host wasm preview1 `read_file`
    (`wasm.fern` `readfile_func`) used a **fixed 64 KiB buffer** — once the
    read offset reached 64 KiB the iovec length went to 0, `fd_read`
    returned 0, and the loop mistook a full buffer for EOF, silently
    truncating any larger file (a 140 KB / 300 KB file both read back as
    64 KB). Fixed by growing the buffer in 64 KiB chunks (the bump
    allocator hands out contiguous bytes and nothing else allocates during
    the read loop, so each extension lands right after the buffer; room is
    ensured *before* each read so a full buffer is never mistaken for EOF).
    Now large files round-trip — `TestSelfHostWasmReadFileLarge` reads a
    200 KB file and checks bytes past the boundary; the capstone assembler
    reads multi-hundred-KB asm intact (ELF size scales with input). No
    regression across the wasm / CLI / capstone suites. (This is unrelated
    to the spurious "bugs" of 2x — it's an actual, reproduced defect.)
  - ✅ **assembler-at-scale correctness — verified, NO bug** (the "150-fn →
    85" claim was spurious). With large asm now delivered intact, the
    self-host assembler was probed at 40 / 150 / 400 / 600 functions
    (up to ~124 KB asm) and **matches gcc's assembly of the same `.s`
    byte-for-result on every size**. The earlier "nfn=400 returns 144, want
    400" reading was simply the **Unix 8-bit exit-code truncation**
    (400 & 0xFF == 144), not a miscompile — and the "150 → 85" figure came
    from a malformed test program (the `f32`/`f64` reserved-keyword function
    names of 2x), not the assembler. Guarded by `TestSelfHostX86ScaleProbe`
    (`self_host_x86_scale_probe_test.go`), which keeps each result < 256 so
    the exit code is unmasked and cross-checks every case against gcc.
    There is no O(n²)-label or fixup defect at scale. **Lesson (recurring):**
    verify the test program is valid Fern and account for exit-code masking
    before declaring an assembler/wasm bug.
  - ⬜ remaining: the x87 transcendentals (`fldl`/`fstpl` + `fsin`/`fcos`/…)
    for `sin`/`cos`/`exp`; the f64-method `asm.fern` gap above; assembling
    `asm.fern`'s own output → a native self-host fixpoint; and the CLI
    wiring (blocked above).

  *Found on the way (latent, not fixed here):* the self-host **wasm
  checker doesn't flag arg-count mismatches** — calling a 1-param
  function with 2 args silently mis-compiled (it dropped the body in an
  early draft of the exit driver) where the Go checker errors `E004`.
  Separate subsystem (self-host checker), tracked for a follow-up.
- ⬜ **arm64 assembler** — GAS aarch64 text → AArch64 bytes, mirroring
  `internal/native/arm64/asm.go` + `gas.go` + `gasprog.go`
  (adrp / `:lo12:` PC-relative resolution against `elf_text_vaddr`).
- ⬜ **Mach-O writer + ad-hoc code signature** — for arm64-darwin,
  mirroring `internal/native/macho/` (`image.go` / `macho.go` /
  `sign.go`). Apple Silicon refuses unsigned binaries, so the ad-hoc
  `LC_CODE_SIGNATURE` blob is mandatory, not optional.

Wiring (final slice): the self-host driver routes `asm.fern` → x86-64
assembler → `elf.fern` → write `0o755` (and the arm64 / Darwin
equivalents), dropping the external link step. (`examples/self_host/
disasm.fern`, which used to double as a cross-check for the
emitted-bytes assemblers, was retired in #4392 along with the
bytecode VM it disassembled.)
