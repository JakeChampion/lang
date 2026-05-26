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
  heap to 1 GiB** (zero-page .bss, both backends). A latent fragility
  for any future growth of the compiler; a hard bounds-check/trap is a
  follow-up.
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
- ⬜ stdlib **`std/test` / `std/fuzz` / `std/tcp`** (no user generics
  of their own; gated on remaining stdlib-link work).

Aside (Go backend, separate subsystem): the Go *native* backend
mishandles compound assignment to a struct field (`a.v += n`) — fixed in
the parser (PR allowing FieldAccess lvalues in the compound path).
