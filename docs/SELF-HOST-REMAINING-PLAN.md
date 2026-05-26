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

## Item 2 — `std/array` ✅ / `std/string` ⬜ (packed-u8[] mismatch)

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

## Item 3 — `std/url` + `std/json` → Map runtime 🔧 (core landed)

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

## Item 4 — `std/float` → libm intrinsics ⬜

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
