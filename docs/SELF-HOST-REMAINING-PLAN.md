# Self-hosting: remaining work plan

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
- ✅ **`switch` / `case`** — desugars in the parser to a nested
  if/else-if chain (multi-value cases OR their `==` comparisons; no
  fall-through) (`self_host_switch_test.go`).
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
  basenames). `core/no_prelude` is treated as a directive, not a file.
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
    `remove_dir_all` — emitted on both backends (these were
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
    (`self_host_fs_test.go`); arm64 mirror is CI-gated.
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
