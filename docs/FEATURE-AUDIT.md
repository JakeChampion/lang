# Feature audit — built-ins & standard library

This document is the **living record** of an ongoing audit of every
built-in language feature and every standard-library function in Fern.
The goal: confirm each feature works **correctly on every backend, in
both the native and the self-hosted compiler** — and fix any bugs found
along the way.

Two compilers, each with its own backends, are in scope:

- **Native** (the Go implementation): four backends — the AST
  **interp**reter, **x86-64**, **arm64**, **wasm**. Audited with the
  data-driven fixture harness (`TestFernFixtures`), which runs a program
  across all four and checks stdout + exit code.
- **Self-hosted** (the Fern-in-Fern compiler under `examples/self_host/`):
  driven by the `self_host_*_test.go` harnesses, which build a driver
  binary (`asm_run.fern` / `asm_ir_run.fern` / `asm_arm64_run.fern` /
  `wasm_ir_run.fern` / `interp_run.fern`), feed it Fern source, then
  assemble + run the result and check the exit code. The self-hosted
  compiler has a narrower **IR subset** than the native one (goal 1 in
  CLAUDE.md is to widen it until the legacy AST fallback is never taken),
  so it is the more likely place to surface gaps.

It is meant to stay up to date — when a feature is audited, its row is
updated; when a bug is found, an issue is opened and the finding is
logged in the [Audit log](#audit-log) at the bottom.

## Testing strategy — property-based + differential

Wherever a feature has an **invariant** (round-trip, idempotence,
involution, ordering, permutation, algebraic law), we prefer a
**property-based** fixture over a single hand-picked input:

- A **deterministic** in-language generator (a small LCG) feeds the
  *same* swarm of inputs to every backend, so a failure is reproducible
  and points straight at the diverging backend rather than at RNG noise.
- Self-checking invariants (`decode(encode(x)) == x`, `sort` is a
  non-decreasing permutation, `reverse∘reverse == id`) need no oracle and
  run as ordinary fixtures across all four backends — verifying both the
  property *and* cross-backend agreement in one shot.
- This complements the existing harnesses already in the tree:
  `internal/e2e/numeric_property_test.go` (differential property testing
  of the numeric surface, interp = oracle), `diff_oracle_test.go`
  (fernsmith-generated whole programs), and the in-language `std/fuzz`
  harness. Property fixtures live under
  `internal/e2e/testdata/cases/prop_*`.

This approach immediately paid off: the very first batch surfaced a real
arm64-only heap-corruption bug (see audit log, 2026-06-09).

## How features are verified

The data-driven fixture harness (`internal/e2e/fixture_test.go`,
`TestFernFixtures`) compiles and runs a program across **all four
backends** and checks stdout + exit code. Most audit work lands as new
fixtures under `internal/e2e/testdata/cases/<name>/`. A fixture exercising
a feature on all four backends is the strongest evidence a feature is
sound; a unit/checker test covers front-end-only behaviour.

Toolchain (must be on `PATH` / env for the wasm + arm64 legs to actually
run rather than SKIP):

- `qemu-aarch64` — arm64 leg
- host is x86-64, so x86-64 runs natively (no qemu)
- `wasmtime` + `wasm-tools` + `FERN_WASI_ADAPTER` — wasm leg

## Status legend

| Mark | Meaning |
|------|---------|
| ⬜ | Not yet audited |
| 🔄 | Audit in progress |
| ✅ | Verified working on all audited backends |
| ⚠️ | Works, but with a documented caveat / partial-backend support |
| 🐛 | Bug found — see audit log (issue linked) |
| 🔧 | Bug found **and fixed** — see audit log |

The per-feature backend columns (I / X / A / W = native interp / x86-64 /
arm64 / wasm) record where a fixture or test confirms the feature on the
**native** compiler. The **S** column records confirmation on the
**self-hosted** compiler (any of its backends; a caveat notes which).
Blank = not yet confirmed on that backend.

---

## A. Built-in language features

Self-host (**S**) verification for §A landed via
`internal/e2e/self_host_audit_builtins_test.go` (per-feature isolated
programs through the self-hosted x86-64 driver + CI-gated arm64); native
(**I/X/A/W**) via the `audit_core_builtins` fixture (all four backends).

| Feature | I | X | A | W | S | Status | Notes |
|---------|---|---|---|---|---|--------|-------|
| Integer arithmetic `+ - * / %` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | incl. trunc-toward-zero for negatives |
| Integer comparison `== != < > <= >=` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| Boolean logic `&& \|\| !` (short-circuit) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | non-eval proven via trap-skip RHS (÷0) |
| Bitwise `& \| ^ << >>` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | prop generators (LCG) + audit fixture |
| Unary minus `-x` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| Operator precedence / parenthesisation | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `2+3*4`, left-assoc, parens |
| Operator overloading on composites (`== != < <= > >=`, `+ - * / % & \| ^ << >>`, unary `-`) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `==`→`eq`, `<`…→`cmp`, `+ - * / %`→`add`/`sub`/`mul`/`div`/`rem`, `& \| ^ << >>`→`bitand`/`bitor`/`bitxor`/`shl`/`shr`, unary `-`→`neg` (#2706); checker desugars to the method, structural by name |
| Sized int types `i8 i16 i32 i64 u8 u16 u32 u64` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | i64 arith, u8/u16 cast; out-of-range literal is a static error |
| Integer overflow / wrapping semantics | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | runtime narrowing cast wraps mod 2ⁿ |
| Float types `f32 f64` arithmetic | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `+ * /`, f32 + f64 |
| Float comparison + NaN semantics | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `< > <= >= == !=` + IEEE NaN: every ordered compare with a NaN is false, only `!=` (incl. `NaN != NaN`) true. Self-host IR pin `TestSelfHostFloatNanIR` (x86-64 + wasm) — x86-64 `ucomisd`+`setcc` folds the unordered/parity flag correctly; wasm `f64.*` is IEEE-direct |
| `boolean` type + literals | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | exercised throughout audit fixture |
| `string` type: `+`, `==`/`!=`, indexing, slice | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | concat, eq/neq, byte index, `s[i:j]`, `.len()` |
| String literals + escape sequences | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `\t \n \r \0 \\ \"` + `\xNN` hex bytes; each decodes to one byte (embedded NUL counts — not C strings). Byte-exact `.len()` / index / concat — native `string_escapes` fixture (4 backends) + self-host IR pin (x86-64 + wasm) |
| f-strings / interpolation | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `f"...{e}..."` desugars (parser) to literal parts + `(e).to_string()` folded with `+`. Native: `TestWASMFStringInterpolation` + closure-capture f-string mirrors. Self-host IR pin (x86-64 + wasm): `TestSelfHostFStringIR` — i32 + string interpolants (the two `to_string` receivers the importless IR path lowers), literal/empty parts, byte offsets, equality |
| Owned arrays `T[]` + indexing + `.with` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | index, `.len()`, `.with` (reassign idiom); **read-after-`.with` aliases on compiled backends, [#2832](https://github.com/JakeChampion/lang/issues/2832)** |
| Slice views `[T]` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | borrowed window `a[i:j]`; `.len()`, indexing, `for x in s`, slice-of-slice, empty windows, `[string]` element slices — native `slice_views` fixture (4 backends) + self-host IR pin (x86-64 + wasm) |
| Tuples `(T, U)` + destructuring | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `.0`/`.1` + `var (a,b) = …` |
| `Map[K, V]` literal + ops | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `insert`/`get_or`/`has`/`len`/`keys`/`values`/`for (k,v)`, i32 + string keys; `without` (functional delete) now on the x86-64/arm64 IR path ([#2926](https://github.com/JakeChampion/lang/issues/2926)) — wasm `without` stays on the AST path (box-return ABI) |
| Array literals | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `var x: T = expr;` + type inference | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | inference (no `: T`) covers wider scalars (i64/u32/u8-wrap/f64/f32/bool/string), composites (tuple/struct/array/enum), and call-return inference — native `var_inference` fixture (4 backends) + self-host IR pin (x86-64 + wasm) |
| Compound assignment `+= -= *= …` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `+= -= *= /= %=`; read-modify-write is width-correct beyond i32 — i64/u32/u8-wrap/f64 + loop accumulation pinned via the self-host IR `compound_assign_wider` pin (x86-64 + wasm) + native fixture (array-element compound assign is E056: arrays are immutable, use `.with`) |
| `if`/`else` statement | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `if` as expression | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `while` loop | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `for(init; cond; step)` loop | ✅ | ✅ | ✅ | ✅ | ✅ | 🔧 | self-host: fixed ([#2820](https://github.com/JakeChampion/lang/issues/2820) / #2841 — parser desugars to a while-loop with a first-iteration flag so `continue` re-runs the step); a parse-time desugar, so both the AST and IR paths get it. Guarded by the executed `c-style-for` audit case + `break`/`continue`-in-for coverage |
| `for x in arr` / `for x in "str"` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | array ✅; `for x in <string>` self-host IR path iterates bytes — literal / local / slice / string-returning call+method ([#2822](https://github.com/JakeChampion/lang/issues/2822), #2834 + the eligibility-probe `str_ret_fns` fix) |
| inclusive / half-open ranges `for i in a..=b` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `0..4` half-open, `0..=5` inclusive |
| `switch` statement (comma cases, default) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | multi-value case + default |
| `break` / `continue` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | S ok in while / foreach / C-style-for — the C-for fix ([#2820](https://github.com/JakeChampion/lang/issues/2820) / #2841) desugars so `continue` re-runs the step correctly |
| `return` (value + void) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| Blocks + expression statements | ✅ | ✅ | ✅ | ✅ | 🔧 | 🔧 | bare nested block `{}` — self-host gap fixed ([#2821](https://github.com/JakeChampion/lang/issues/2821) / #2831), re-enabled as guard |
| `struct` decl + literal + field access | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | + functional update `T { ...old, f: v }` |
| Struct field immutability + functional update | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | fields immutable (E048); self-host `fern` CLI now gates the compile path too ([#2825](https://github.com/JakeChampion/lang/issues/2825) fixed) |
| Methods (receiver clause) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | struct + enum receivers; enum-receiver method calls (`c.method()`, [#2947](https://github.com/JakeChampion/lang/issues/2947)) and enum-array element method calls (`a[i].method()`, [#2954](https://github.com/JakeChampion/lang/issues/2954)) now lower through the self-host IR path |
| `enum` sum types + payloads | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | incl. unit variants; wasm owned-model RC caveat [#2828](https://github.com/JakeChampion/lang/issues/2828) |
| `match` (exhaustiveness checked) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | payload binding, comma-separated arms |
| `match` as expression | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| Generic structs/enums (monomorphised) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `Box[T]` + generic method |
| Generic functions + inference | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `id[T](x: T): T`, inferred |
| Traits (`Display`/`Eq`/`Ord`, bounds) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | trait + impl method dispatch |
| Nested functions + closures (capture) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `function(x: T): R { … cap … }`; incl. returning a capturing closure and calling it inline off the call result (`mk(..)(args)` / curried `(x)=>(y)=>…`) — self-host IR `return_closure` pin (#3551) |
| Function values / indirect calls | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | named fn as value; higher-order |
| Lambdas (anonymous `function(…)` + arrow `(x: T): R => e`) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | arrow form desugars to `function(…){ return e; }` — typed params required, return type optional (#2701) |
| Tail-call optimisation | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | depth 5000 self-recursion, no overflow |
| Modules / imports (`import "./path";`) | | | | | | ⬜ | |
| Visibility (`pub`) | | | | | | ⬜ | front-end only |
| Top-level `const` (folded) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | desugars to a zero-arg fn; a bare ref is a call. Self-host: native + AST path, and now the **IR path** too (a bare const ident lowers to `call_direct(name, 0)`, [#2954](https://github.com/JakeChampion/lang/issues/2954); `TestSelfHostAsmIRPath/const-*`) |
| `len(x)` / `.len()` builtin | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | string / array / map |

## B. Built-in functions (checker-registered)

| Function | I | X | A | W | S | Status | Notes |
|----------|---|---|---|---|---|--------|-------|
| `print(s)` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | stdout + newline |
| `write(s)` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | stdout raw, no newline |
| `eprint(s)` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | stderr (not on stdout) |
| `putchar(b)` | ✅ | ✅ | ✅ | ✅ | ✅ | 🔧 | self-host: fixed on the **IR path** ([#2839](https://github.com/JakeChampion/lang/issues/2839)) — `__fern_putchar` (`write(1, &byte, 1)`) emitted by the x86-64 / arm64 / wasm IR backends, guarded by `self_host_putchar_{,arm64_,wasm_}ir_test.go`. Legacy AST `asm.fern` still doesn't lower it (IR-path-only, per goal 1) |
| `len(x)` / `.len()` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | native uses `.len()` method; self-host also has free `len(x)` |
| `args(): string[]` | | | | | ✅ | ⚠️ | self-host ✓; native arg-passing via CLI e2e tests |
| `env(name): Option[string]` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | unset → `None` |
| `exit(code)` | ✅ | ✅ | ✅ | ⚠️ | ✅ | ✅ | native interp/x86/arm + self-host; wasm proc_exit vs result-line harness |
| `stdin()/stdout()/stderr()` | | | | | | ⬜ | Reader/Writer |
| `read_file` / `write_file` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | native: BACKEND-PARITY ReadFile/WriteFile tests; self-host: fs tests + probe |
| `open_reader/open_writer/open_appender` | | | | | | ⬜ | |
| Reader `.read_line()/.read_chunk(n)/.close()` | | | | | | ⬜ | |
| Writer `.write(s)/.close()` | | | | | | ⬜ | |
| `read_line()` (free) | | | | | | ⬜ | |
| `read_dir` / `stat` | | | | | | ⬜ | |
| `remove_file` / `remove_dir_all` | | | | | | ⬜ | |
| `temp_dir(prefix)` | | | | | | ⬜ | |
| `subprocess(...)` | | | | | | ⬜ | |
| `sleep_ms` | ✅ | ✅ | ✅ | 🐛 | ✅ | ⚠️ | interp + native x86-64/arm64 + self-host ✅ ([#2843](https://github.com/JakeChampion/lang/issues/2843)); self-host **IR path** lowers it on x86-64/arm64 (wasm routes via the AST path); **native wasm pending (WASI poll-based sleep)** |
| `now_unix_ms` / `monotonic_ns` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | both ✅ all backends; `monotonic_ns` native x86-64/arm64 runtimes added ([#2843](https://github.com/JakeChampion/lang/issues/2843)); self-host **IR path** lowers both on x86-64/arm64 (wasm routes via the AST path) |
| `now_ns` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | wall-clock nanoseconds (i64); native interp + x86-64 + arm64 runtimes added (previously wasm-only); self-host x86-64/arm64 now emit it on both the AST and IR paths (wasm routes via the AST path) |
| `random_bytes` / `random_i32` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | length + usable value |
| `f32_bits/f32_from_bits/f64_bits/f64_from_bits` | | | | | | ⬜ | |
| float math builtins `__sqrt_f64` etc. | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | via std/float; self-host IR path (`op_funary`; `TestSelfHostFloatMathIR`): `__sqrt_f64`/`__floor_f64`/`__ceil_f64`/`__trunc_f64`/`__abs_f64` lower to a single hardware instruction on all three backends, and `__round_f64` (round-half-away) lowers too — one instruction on arm64 (`frinta`), emulated as `trunc(x+copysign(0.5,x))` on x86/wasm (`roundsd`/`f64.nearest` have no ties-away mode). Only the libm transcendentals (`__log_f64`/`__exp_f64`/`__sin_f64`/`__cos_f64`/`__pow_f64`) still route AST |
| `strbuf_reset/append/take` | ✅ | ✅ | ✅ | | ✅ | ✅ | global string-builder (reset zeroes / append adds bytes / take returns + resets). interp impl added [#3579](https://github.com/JakeChampion/lang/pull/3579); native x86-64/arm64 + self-host IR lower it. **wasm (native) does not implement it** (`unknown callee "strbuf_reset"`) — W left blank. Tests: `interp_strbuf_test.go`, `self_host_strbuf_ir_test.go`, `arm64_strbuf_test.go` |
| `__heap_bump_bytes` | ⚠️ | ✅ | ✅ | ✅ | ✅ | 🔧 | bump high-water mark (cursor − region base; 0 before the first alloc). self-host **IR path** ([#3534](https://github.com/JakeChampion/lang/issues/3534)) lowers it inline — x86-64 `__fern_heap_ptr − &__fern_heap`, arm64 `__fern_heap_ptr − (__fern_heap_end − heap_size)`, wasm `$heap − heap_base` — with `ir.op_allocates` admitting it so an introspection-only module still emits the heap runtime. Guarded by `TestSelfHostHeapBumpBytesIR{X86_64,Wasm}` (+ native x86-64 cross-check). interp has no bump allocator so it reports 0 (the pre-alloc zero baseline holds; the growth contract does not). Legacy AST self-host path unchanged (IR-path-only, per goal 1) |
| `__rc_*` (inc/dec/get/underflow_count) | | | | | | ⬜ | RC introspection |
| TCP: `tcp_listen/accept/recv/send/close` | | | | | | ⬜ | |
| `udp_send` | | | | | | ⬜ | |
| `map_new` + Map methods | | | | | | ⬜ | |

## C. Built-in types (checker-synthesised)

| Type | I | X | A | W | S | Status | Notes |
|------|---|---|---|---|---|--------|-------|
| `Option[T]` (`Some`/`None`) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | construct + match both arms |
| `Result[T, E]` (`Ok`/`Err`) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | construct + match both arms |
| `IoError` variants | | | | | | ⬜ | |
| `JsonValue` variants | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | via `std/json` parse/encode roundtrip (`audit_std_json`) |
| `Reader` / `Writer` | | | | | | ⬜ | |
| `HttpRequest` / `HttpResponse` | | | | | | ⬜ | |
| `Url` | | | | | | ⬜ | |
| `Map[K, V]` / `MapIter[K, V]` | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | Map ops audited (i32+string keys); MapIter cursor pending |
| Time types (`Instant`/`Date`/…) | | | | | | ⬜ | via std/time |

## D. Standard library — `std/`

Function lists are mirrored from `docs/STDLIB.md`. Each module gets a row;
the audit drills into individual functions as needed and records
per-function bugs in the audit log.

| Module | I | X | A | W | S | Status | Notes |
|--------|---|---|---|---|---|--------|-------|
| `std/i32` (~80 methods) | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | representative set (abs/min/max/clamp/pow/gcd/lcm/is_prime/is_even/signum) — `audit_std_numeric`; self-host via array bundle |
| `std/i64` | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | abs/min/max — `audit_std_path_numeric`; self-host abs/min/max/clamp via the x86-64 IR path (`TestSelfHostNumericMethodsIRX86_64`) |
| `std/u32` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | max/min — `audit_std_path_numeric`; self-host unsigned min/max on the x86-64 IR path (`TestSelfHostNumericMethodsIRX86_64`) and the wasm IR path — the `#2917` wasm unsigned-compare gap is **closed** (`irlower` flags an ordering compare `unsigned` when an operand is u32, `wasm_ir` emits `i32.*_u`; `TestSelfHostUnsignedCompareWasmIR` incl. the `u32_max(big,one)` repro with `big > 2^31`) |
| `std/u64` | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | clamp — `audit_std_path_numeric`; self-host via the IR path: u64 unsigned compare / `>>` / `/` / `%` ([#2904](https://github.com/JakeChampion/lang/issues/2904); `TestSelfHostU64UnsignedIR`) + the `min`/`max`/`clamp` methods incl. high-bit-set bounds (`TestSelfHostU64IR`, oracle-checked) — the i64-domain analog of the u32 wrapping fix; `to_string` routes via the AST path (core/int `__int_to_string_u64`'s `u8[]`/`usize`/`__memcpy`) |
| `std/float` | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | sqrt/floor/ceil/abs/is_finite — `audit_std_path_numeric`; self-host IR path: the `sqrt`/`floor`/`ceil`/`trunc`/`abs`/`round` intrinsics lower via `op_funary` (routing-pinned `TestSelfHostFloatMathIR`; `round` is `frinta` on arm64, `trunc(x+copysign(0.5,x))` on x86/wasm); `min`/`max`/`clamp`/`is_nan`/`is_finite`/`is_inf` are ordinary f64 compares that already lower. Only the transcendentals (`log`/`exp`/`sin`/`cos`/`pow`) still route AST |
| `std/string` (~120 methods) | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | core set (upper/lower/trim/contains/starts_with/ends_with/index_of/replace/repeat/pad/split) — `audit_std_string` + `self_host_string_test`; `prop_string_involution` laws; full ~120 set pending |
| `std/array` | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | reductions sum/max/min/product/sorted_asc — `audit_std_numeric` + `self_host_audit_stdarray_test` |
| `std/math` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | range/i32_max/i32_min — `audit_std_numeric` + `self_host_math_test` |
| `std/sort` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_sort_i32` — ordering + permutation (histogram) + idempotence laws (native, all four backends); self-host via the IR path (x86-64 + wasm): `TestSelfHostSortIR` — i32 ascending/descending insertion sorts, the byte-lexicographic `string_cmp` three-way comparator, and the `string[]` sorts built on it (`.append` build, `.with` element rewrite, indexed scalar + string-byte reads, nested insertion-sort `while`), oracle-checked against the interpreter |
| `std/format` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | self-host via the IR path (x86-64 + wasm): `format_bytes` (`TestSelfHostFormatBytesIR`), `format(fmt, args)` `{}`-substitution (`TestSelfHostFormatStringIR`), `format_duration_ms` (`TestSelfHostFormatDurationIR`), and the `{:fill|align|width.precision}` specs (`TestSelfHostFormatSpecIR`) — all oracle-checked against the interpreter; native via `audit_std_textfmt` + the `format_specs` fixture (with std/float `to_string_prec`) |
| `std/csv` | ✅ | ✅ | ✅ | ✅ | | ✅ | parse_line/join/escape — `audit_std_textfmt`; self-host via the IR path (x86-64 + wasm): `csv_parse_line` (`TestSelfHostCsvParseLineIR`) + `csv_escape`/`csv_join` (`TestSelfHostCsvEscapeIR`, oracle-checked — `index_of`/`replace` lower as `op_str_index_of`/`op_str_replace`) |
| `std/log` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | leveled `Logger`/`LogEntry` (#2683) plain-text + JSON-lines `render` — native via `log_leveled` fixture (all four backends); self-host via the IR path (x86-64 + wasm): `TestSelfHostLogLeveledIR` — structs with i32/boolean/string fields, chained struct-returning receiver methods, the threshold-filter branch, byte-indexed JSON escaping (hardcoded expectations: `.to_string()` is a self-host builtin the importless interp can't resolve, cf. format_bytes) |
| `std/io` | | | | | | ⬜ | |
| `std/io_buffered` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | in-memory `BytesWriter` (`data: u8[]`) — `write_string` / `write_bytes` / `write_byte` / `len` / `is_empty` / `into_string` / `reset` — native via the `bytes_writer` fixture (interp / x86-64 / arm64 / wasm); self-host via the IR path (x86-64 + wasm): `TestSelfHostBytesWriterIR` — struct with a `u8[]` field, functional struct-spread append, `u8[].append` with `as u8` casts, indexed string-byte reads, and `string_from_bytes` via `into_string` (inlined as `BW`, since `BytesWriter` is a reserved builtin type name; `write_string` uses `s[i] as u8` in place of the module's `s.bytes()`, a std/string method the importless driver can't import). The fd-backed buffered Reader/Writer is Phase 2 (effectful, separate) |
| `std/path` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | join/file_name/extension — `audit_std_path_numeric` + `self_host_audit_stdpath_test` |
| `std/base64` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_codec_roundtrip` — 300 random inputs, full byte range; self-host IR path: `base64_encode`/`base64_decode` lower end-to-end (real std/base64 source, routing-pinned `TestSelfHostBase64IR`, x86-64 + wasm + arm64 oracle-checked) |
| `std/hex` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_codec_roundtrip`; self-host IR path: `hex_encode`/`hex_decode` lower end-to-end (real std/hex source, routing-pinned `TestSelfHostHexIR`, x86-64 + wasm + arm64 oracle-checked) — unblocked by the wasm `string_from_bytes` helper-gate fix |
| `std/crypto` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | SHA-256 vectors ✅ native (`audit_std_crypto`); self-host now correct via the IR path — u32 wrapping + array builders + byte builtins ([#2861](https://github.com/JakeChampion/lang/issues/2861) fixed, #2891; `TestSelfHostU32WrapIR`) |
| `std/uuid` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | v4 length/dashes/version/uniqueness — `audit_std_uuid`; self-host v4 + v7 via the IR path (`TestSelfHostUuidIR`) |
| `std/url` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_url_roundtrip` — 300 inputs, all four backends; the arm64 heap-corruption ([#2817](https://github.com/JakeChampion/lang/issues/2817)) is fixed (two-word `string_from_bytes` now uses `__fern_alloc_rc1`); self-host via the IR path (x86-64 + wasm): `url_encode`/`url_decode` percent-coding (`TestSelfHostUrlCodecIR`) + `url_parse` URL decomposition (`TestSelfHostUrlParseIR` — 6-field struct w/ mixed string+i32 fields, repeated struct-spread updates, `Option[Url]` + payload `match`) + `query_parse` (`TestSelfHostUrlQueryIR` — `Map[string, string[]]` w/ string-ARRAY values via `Map {}`/`.get`/`.insert`, incl. the duplicate-key append-to-existing case) — byte classification, bit ops, `u8[]` literals + `as u8` casts, and the `string_from_bytes` builtin all lower; native via the `url_codec` fixture (encode/decode + `url_parse` + `query_parse` incl. dup-key accumulation). (The dup-key wasm-IR map miscompile [#3495](https://github.com/JakeChampion/lang/issues/3495) is now fixed — `op_map_set` threads value-pointerness into the wasm `vis` flag — and the dup-key case is the regression guard.) |
| `std/json` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | parse → get_i32/get_string → encode → re-parse — `audit_std_json` + `self_host_json_test`; `@derive(Json)` incl. **array fields** (`T[]`) — native all backends (`derive_json` fixture), self-host i32/string/struct arrays via the IR path ([#2766](https://github.com/JakeChampion/lang/issues/2766); `TestSelfHostJsonArrayIR`) |
| `std/error` | ✅ | ✅ | ✅ | ✅ | | ✅ | canonical `Error` supertype (`message()`) for heterogeneous errors: `Result[_, dyn error.Error]` + `?` boxes any concrete error that `impl error.Error for …` (`std_error_test`, all four backends) — caps the dyn-error story (#3216 dispatch fix + #3242 `?`-conversion; #2707) |
| `std/convert` | ✅ | ✅ | ✅ | ✅ | | ✅ | canonical `From[T]` / `Into[T]` conversion traits (on generic traits, #3254): `impl convert.From[i32] for Celsius` + `Celsius.from(20)`, `impl convert.Into[F] for Celsius` + `c.into()` (`std_convert_test`, all four backends; #2691) — generic use over a bound awaits bounded-generics-over-generic-traits |
| `std/http` | | | | | | ⬜ | |
| `std/tcp` | | | | | | ⬜ | |
| `std/headers` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `HeaderMap` case-insensitive get/get_all/append/set over two parallel string[] fields — native via `headers_map` fixture (all four backends); self-host via the IR path (x86-64 + wasm): `TestSelfHostHeadersIR` — struct with string[] fields, functional struct-spread update, `string[].append`, indexed string-field compares, `Option[string]` `Some`/`None` + payload-binding `match`, chained struct-returning receiver methods, and the `(h) len()` receiver method (the `append-len` case — pins the [#3478](https://github.com/JakeChampion/lang/issues/3478) fix) (inlined as `Headers` + a lookup-slice `lower`, since `HeaderMap` is a reserved builtin name + the importless driver has no `.to_lower()`) |
| `std/stream` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | in-memory byte `Stream` (`data: u8[]` + `pos` cursor) — the value-threaded CURSOR IDIOM: `len`/`remaining`/`read_byte`/`read_n`/`read_all_string`/`read_line` (CRLF/LF + unterminated tail) — native via the `stream_reader` fixture (interp / x86-64 / arm64 / wasm); self-host via the IR path (x86-64 + wasm): `TestSelfHostStreamIR` — struct with a `u8[]` field + i32 cursor, struct-spread update, tuple-returning methods with pointer + `Option` elements, tuple destructuring in `let`, `u8[].append` with `as u8` casts, `string_from_bytes`, `Option` `Some`/`None` + payload-binding `match` (inlined as `Buf`, since `Stream` is a reserved builtin type + the importless driver has no imports) |
| `std/time` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | is_leap_year/days_in_month/date_make/format_iso — `audit_std_time`; self-host via the IR path: pure-i32 helpers (`TestSelfHostTimeIR`) + the **Date civil-date methods** (Hinnant days_from_civil/civil_from_days, is_valid/add_days/days_since/weekday/day_of_year/format_iso — `TestSelfHostTimeDateIR`, oracle-checked, struct ctor + field access + struct-returning fn + receiver methods) + `date_parse_iso` `Option[Date]` parse (`TestSelfHostTimeParseIR`, `Some`/`None` ctor + payload-binding `match`) + `format_rfc3339` / `instant_parse_rfc3339` (`TestSelfHostTimeRfc3339IR`, **i64 `sec` struct field** — i64 arithmetic/casts + `Some(Instant{ sec: <i64> })`) + `add_span` / `add_duration` / `duration_since` / `days_until` (`TestSelfHostTimeSpanIR`, **8-field Span by-value param** + i64+nsec carry/borrow) + the Zoned / TimeZone surface (`in_zone` / `to_datetime` / `timezone_iana` — `TestSelfHostTimeZonedIR`, **nested structs** `Zoned{instant,zone}` / `DateTime{date,time}` + `Option[TimeZone]`) |
| `std/task` | | | | | | ⬜ | |
| `std/mock_platform` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | call-recording log (`MockPlatform` holds a `MockCall[]`) — `record` / `call_count` / `reset` / `has_call` / `find_call` — native via the `mock_platform_log` fixture (interp / x86-64 / arm64 / wasm); self-host via the IR path (x86-64 + wasm): `TestSelfHostMockPlatformIR` — struct with an array-of-struct field, functional struct-spread append, indexed array-of-struct field reads (`m.calls[i].name`), membership scan, and `find_call`'s `Option[MockCall]` (Option of a struct) + payload-binding `match` (inlined as `MPlat`/`MCall`, since both are reserved builtin type names) |
| `std/test` (~150 assertions) | | | | | | ⬜ | |
| `std/fuzz` | | | | | | ⬜ | |

## E. Core library — `core/`

| Module | I | X | A | W | S | Status | Notes |
|--------|---|---|---|---|---|--------|-------|
| `core/int` | ✅ | ✅ | ✅ | ✅ | 🔧 | 🔧 | radix **parse** direction (`parse_int_radix` / `__radix_digit`, bases 2–36, sign handling) — native via the `core_int_parse` fixture (interp / x86-64 / arm64 / wasm); self-host via the IR path (x86-64 + wasm): `TestSelfHostCoreIntParseIR` — `Option[i32]` `Some`/`None` + payload-binding `match`, string indexing with char-class compares, multiply-accumulate loop, sign + negation. The **to-string radix** direction (`int_to_string_radix`) ALSO lowers on the IR path — it builds via `__alloc_u8` + `.with` + `string_from_bytes` (no `__memcpy`/`usize`), the same builder std/hex / std/base64 use — native via the `core_int_radix` fixture, self-host via `TestSelfHostCoreIntRadixIR` (x86-64 + wasm, oracle-checked). Only `int_to_string` / `__int_to_string_u64` (decimal) stay AST — those poke raw memory via `__memcpy` over a `usize` pointer (same caveat as std/u64 `to_string`) |
| `core/cmp` (traits) | | | | | | ⬜ | |
| `core/iter` (Iterator trait) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **Generic** `Iterator[T]` protocol + integer `Range` (`impl Iterator[i32]`) + eager drivers ([#2686](https://github.com/JakeChampion/lang/issues/2686) / tail of [#2699](https://github.com/JakeChampion/lang/issues/2699)). Value-semantic `next(self): Option[(T, Self)]`. `count[T, I: Iterator[T]]`, `to_array[T, I: Iterator[T]]: T[]`, and `fold[T, A, I: Iterator[T]](it, init: A, f: (A, T) => A): A` (the fundamental left reduction, generic over both element and accumulator type, taking a closure combiner) are generic over the element type. Closure-free adapters `nth`/`last[T, I: Iterator[T]]: Option[T]`, `min`/`max`/`product[I: Iterator[i32]]`, `position`/`count_value[I: Iterator[i32]](it, target)`, and `contains[I: Iterator[i32]](it, target): boolean` round out the set (the i32-bound ones need `+`/`*`/`<`/`==`). Works on native (interp / x86-64 / arm64 / wasm) AND the self-host **IR path** (x86-64 + wasm): parametrised-trait bounds parse on the self-host (#3558) and the native checker recovers the bound-only `T` by bound-driven inference (#3596). Coverage: `TestNativeIteratorTrait{,Module,ModuleGeneric,Arm64}`, `TestSelfHostIteratorTraitIR{X86_64,Wasm}`, `TestNativeGenericIteratorCollector{,Arm64}` + `TestSelfHostGenericCollectorIR{X86_64,Wasm}` (incl. a `boolean`-element impl + `to_array` returning a generic `T[]`), all routing-pinned to `ir` on the self-host |
| `core/map` | | | | | | ⬜ | |
| `core/no_prelude` | | | | | | ⬜ | no-op sentinel |

---

## Audit log

Reverse-chronological. Each entry: what was checked, what was found, what
changed (fixture / fix / commit).

<!-- newest first -->

### 2026-06-21 — self-host IR: a closure that calls another local closure now lifts

A local closure whose body CALLS another local closure — `var add = fn(a){…};
var twice = fn(a){ add(add(a)) }; twice(1)` — bailed the whole module to the AST
emitter; the native compiler runs it (a goal-1 IR-subset gap in the closure /
fn-value family). Root cause: when `closure_lift_one` hoists `add` to a
top-level `__lam_N`, `subst_fcall_expr` rewrote `add`'s call sites in the rest of
the body but didn't recurse into nested `ExprLambda` bodies, so `add(add(a))`
inside `twice` kept an `add` reference → its lift was declined → the module fell
to the first-class-closure path → AST. Fix (two localized spots in
`irlower.fern`): `subst_fcall_expr` recurses into a nested lambda body (declining
only when `fname` or an injected capture name is shadowed by a binding of that
lambda), and each lift round extends the global-fn set with the `__lam_N` names
hoisted so far so a sibling lambda calling an already-hoisted one sees a global,
not an un-typeable capture. Both the capture-free and the **capturing** inner
closure work (the inner closure's captures live in the shared enclosing scope, so
the injected capture args flow through as the calling lambda's own captures —
covering `add` capturing `x`, multi-capture, and a shared capture called by a
third closure). Landed in two PRs (capture-free first, then the capturing-inner
relaxation). The harder remaining case — a first-class closure VALUE captured by
another closure and dispatched dynamically (#3445) — is unchanged. Coverage:
`TestSelfHostClosureCallsClosure{X86IR,WasmIR}` (nested / binary / chain / if /
loop bodies + capturing-inner / two-capture / shared-capture / direct-and-call),
each oracle-checked against native and asserting `__lam_` lifting; the shadow-
decline path falls cleanly to AST with the correct result. Fixpoint stays
byte-identical (the transform is deterministic + self-consistent).

### 2026-06-21 — self-host IR: generic-enum match over call / array / index scrutinees ([#3572](https://github.com/JakeChampion/lang/issues/3572) follow-up)

Closes two follow-on miscompiles in the generic-enum monomorphiser (the same-day
#3572 work below): the pass only recovered a `match` scrutinee's instantiation
from a directly-annotated **ident** (`var o: Opt[i32]; match (o)`), so other
scrutinee shapes left their arm patterns un-mangled — and since the pass *drops*
the generic variant structs, an un-mangled `Sm`/`Nn` pattern (or a unit-variant
construction) dangled → wrong result or a segfault. Now `monomorphize_enums`
recovers the instantiation for a **call** scrutinee (`match (wrap(4))` — via the
callee's declared return type), an **index** scrutinee (`match (xs[0])`), and a
`for`-loop element (`for o in xs` over `Opt[i32][]` binds `o: Opt[i32]`), and an
**array literal** flowing into `Opt[i32][]` propagates the element expected type
to each element so a unit-variant element (`Nn`) still pins its instantiation. An
unannotated `var o = wrap(4)` also records the init's recovered type for a later
`match (o)`. Coverage: four new `TestSelfHostGenericEnum{IRX86_64,WasmIR}` cases
— call scrutinee, array-iter (unit element), index scrutinee, string-payload
array method dispatch — all routing `ir` and oracle-checked against native.
Fixpoint stays byte-identical (compiler source has no generic enums).

### 2026-06-21 — self-host IR: user-defined generic enums (`enum E[T]`) monomorphise + lower ([#3572](https://github.com/JakeChampion/lang/issues/3572))

A **user-defined generic enum** — `enum Opt[T] { Sm(T), Nn }` — previously
bailed the whole module to the legacy AST emitter (construction *and* match):
generic functions and generic structs already monomorphised on the self-host
path, but the enum table passed straight through `monomorphize_module` /
`monomorphize_structs`, so a generic enum's variant payload type stayed the bare
type variable `T` and the IR path couldn't resolve a payload's primitive methods
(`s.len()` on a `Box[string]` payload mis-dispatched → it bailed). Native
monomorphises per instantiation, so this was a goal-1 IR-subset gap. Fix: a new
`parser.monomorphize_enums` pass (mirroring the generic-struct pass) clones each
generic enum per concrete instantiation (`Opt[i32]` → `Opt__i32` with
`Sm__i32(i32)`), mangles the variant **construction** call sites, the `match` arm
**patterns**, and the `Opt[i32]` **annotations** to the clone, and wires the
cloned enums + variant structs into the returned `Module` (the generic originals
are dropped). The instantiation key is inferred from a `var`/param/return
annotation or, for a payloaded variant, the argument types unified against the
variant's field types — exactly the way the struct pass infers a literal's key.
Scope: a generic enum with **no** associated methods and a simple-nominal key
(primitive / string / bare struct); a method-bearing generic enum, or a composite
key (`Opt[Box[i32]]`), is left untouched (it keeps its pre-existing behaviour
rather than dangling). Coverage: `TestSelfHostGenericEnum{IRX86_64,WasmIR}` —
i32 payload, string-payload method dispatch, unit variant, construction-only, and
**two distinct instantiations coexisting** (`Opt[i32]` + `Opt[string]`) — all
routing `ir` and oracle-checked against the native interpreter. Self-host
fixpoint stays byte-identical (modload + stage2): the compiler source + stdlib
use no generic enums, so the pass is a no-op on the fixpoint corpus, exactly like
the generic-struct pass.

### 2026-06-20 — self-host IR: calling a RETURNED capturing closure inline (#3551)

Closed the last common closure shape that bailed the self-host IR path to the
AST emitter: **returning a capturing closure and calling it directly off the
call result** — `mk(10)(5)` where `mk` returns `(y) => k + y`, and the curried
`(x) => (y) => x + y`. Found while sweeping the IR path for remaining AST
fallbacks (it was the only common one left).

The env/RC machinery already worked for the via-`var` form (`var f = mk(10);
f(5)` — `closure_ret_fns` marks `f` a closure local, the box is dispatched
env-first). Only the **inline call-on-call** bailed: the `mk(..)(args)` lowering
handled a callee returning a bare fn pointer (no-capture lambda) but explicitly
`fail()`ed when the callee returned a CLOSURE box. The fix evaluates the inner
call into a fresh temp (marked an array so the exit dec-sweep releases it,
exactly as the bound closure local is) and dispatches env-first off the box —
the same shape the `is_closure_local` call arm already uses.

`irlower.fern` only; the byte-identical self-host fixpoints
(`TestSelfHostModloadFixpointX86_64` / `TestSelfHostStage2FixedPoint`) stay
green. Guarded by `TestSelfHostReturnClosureIR{X86_64,Wasm}` — 7 value-pinned
cases (curry, param/local capture, two-arg, two-captures, plus no-capture and
via-`var` regression guards), routing-pinned to `"ir"` and oracle-checked.

### 2026-06-20 — `std/u32` self-host row → ✅ (the #2917 wasm unsigned-compare gap is closed)

Re-audited the `std/u32` row, which carried a self-host `⚠️` citing the wasm IR
unsigned-compare gap as still-open ([#2917](https://github.com/JakeChampion/lang/issues/2917)).
That gap is **closed**: a u32 ≥ 2³¹ is a signed-negative i32, so a signed wasm
compare (`i32.lt_s`/`gt_s`/…) answers it wrong; `irlower` now flags an ordering
compare `unsigned` when an operand is u32 and `wasm_ir` emits the `i32.*_u`
opcode (the same `to_unsigned_kind` mechanism u64 uses). The fix and its gate
(`TestSelfHostUnsignedCompareWasmIR` — incl. the exact `u32_max(big, one)` repro
with `big = 4e9`, plus `>`/`<`/`>=`/`<=` directly) are in tree and green, so the
self-host column now matches the x86-64/arm64 reality (those keep u32 positive in
their 64-bit slots, so a signed compare already matched). Row flipped `⚠️ → ✅`;
the unsigned-compare gap was the only documented self-host caveat on this row.

### 2026-06-20 — compound assignment (wider types) on the self-host IR path + native audit

Extended the `Compound assignment += -= *= …` row beyond the i32 path. A
compound assignment is a read-modify-write — the lowering loads the local,
applies the op, and stores back, so width-correct load/op/store matters for the
wider types.

- **Native** — `compound_assign_wider` fixture (interp / x86-64 / arm64 / wasm):
  `+=` / `*=` on i64, `+=` on u32 / u8 (wrap), `+=` / `*=` on f64, the full
  `-= /= %=` set on i32, and `+=` loop accumulation into an i64.
- **Self-host IR** — `TestSelfHostCompoundAssignWiderIR{X86_64,Wasm}` run ten
  cases through the x86-64 + wasm IR drivers, pinned to the `"ir"` path and
  oracle-checked against the interpreter (every result ≤ 120). All already
  lower, so **no compiler change**.

(Array-element compound assignment `a[i] += v` is statically rejected with
`E056` — owned arrays are immutable; use `.with` — so it is correctly excluded.)
### 2026-06-20 — float comparison NaN semantics on the self-host IR path

Closed the `⚠️` in the self-host column of the `Float comparison + NaN
semantics` row ("`< > >=` audited; NaN semantics pending"). IEEE-754 says every
ordered comparison with a NaN is **false** — `< > <= >= ==` — and only `!=`
is **true**, including `NaN != NaN`. A NaN is produced importlessly with
`0.0 / 0.0`.

- **Self-host IR** — new `TestSelfHostFloatNanIR{X86_64,Wasm}` run eight cases
  through the x86-64 + wasm IR drivers, pinned to the `"ir"` path and verified
  against the native interpreter: `NaN != NaN` → true, `NaN == NaN` → false,
  `NaN {< > <= >=} 1.0` → all false, the negated `!(NaN < 1.0)` else-branch, and
  two ordered (non-NaN) sanity cases. All already lower correctly, so **no
  compiler change** — the x86-64 float compare's `ucomisd` sets the parity flag
  on an unordered operand and the emitted `setcc` sequence folds it correctly
  (`==` stays false / `!=` true when PF=1); wasm's `f64.eq`/`f64.ne`/`f64.lt`/…
  are IEEE-direct.

Row flipped to ✅ (self-host column; the native I/X/A/W cells were already ✅
for the ordered comparisons).

### 2026-06-20 — `var` type inference (wider types) on the self-host IR path + native audit

Extended the `var x: T = expr + type inference` row from "i32 path; wider types
pending" to full coverage. With no explicit `: T` annotation, the binding's
type is inferred from its initializer; the inferred type then drives the
arithmetic / field access / dispatch that follows, so a wrong inference would
mis-lower or bail.

- **Native** — `var_inference` fixture (interp / x86-64 / arm64 / wasm): infers
  i64 / u32 / u8-wrap / f64 / bool / string / tuple / struct / `i32[]` / enum
  bindings and a call-return-typed binding, then asserts each.
- **Self-host IR** — `TestSelfHostVarInferenceIR{X86_64,Wasm}` run twelve cases
  through the x86-64 + wasm IR drivers, pinned to the `"ir"` path and
  oracle-checked against the interpreter (i64/u32/u8-wrap/f64/f32/bool/string +
  tuple/struct/array/enum + call-return inference; every result ≤ 120). All
  already lower, so **no compiler change**.

Row note updated to reflect the wider-type coverage.
### 2026-06-20 — f-strings / interpolation on the self-host IR path + native audit

Audited the foundational `f-strings / interpolation` row (was ⬜ across the
board). An f-string `f"...{e}..."` is desugared in `parser.fern` to its literal
parts (string literals) and each interpolant as `(e).to_string()`, folded
left-to-right with `+` (an empty f-string → `""`). The self-host IR path already
lowers `.to_string()` on the two primitive receivers an importless program can
name — a string (identity: `"x".to_string() == "x"`) and an i32 (routed to the
`__fern_i32_to_string` runtime helper) — so f-strings interpolating i32 +
string values lower entirely through the IR path with **no compiler change**.

- **Native** — f-strings already exercised end-to-end by
  `TestWASMFStringInterpolation` (literal braces, nested expr interpolants,
  plain text) and the closure-capture f-string mirrors across backends.
- **Self-host IR** — new `TestSelfHostFStringIR{X86_64,Wasm}` run seven cases
  through the x86-64 + wasm IR drivers, pinned to the `"ir"` path and verified
  against the native interpreter (with `import "std/i32";` so `to_string`
  resolves there): single / multi / string interpolants, a literal+interp mix,
  multi-digit values, byte-offset content, and the empty f-string (every result
  ≤ 126).

Row flipped to ✅. f64 / i64 / bool interpolants are deliberately out of scope:
their `to_string` needs an imported stdlib method, so those f-strings fall off
the importless IR surface — a natural follow-up once the IR path grows native
`to_string` lowering for the wider scalar types (mirrors the noted
f-string-over-i32 self-host-vs-native divergence: the self-host IR path is more
permissive than the native checker, which requires the explicit import).

### 2026-06-20 — slice views `[T]` on the self-host IR path + native audit

Audited the foundational `Slice views [T]` row (was ⬜ across the board). A
slice `a[i:j]` is a borrowed (fat-pointer) window over an owned array — it is
leak-only (never reclaims the backing storage), so `.len()`, element indexing
(`s[k]`), `for x in s` iteration, slice-of-slice (`s[a:b]`), empty windows, and
`[string]` element slices all stay on the IR path.

- **Native** — `slice_views` fixture (interp / x86-64 / arm64 / wasm): builds
  windows over `i32[]` / `string[]`, prints `.len()` / element values / a
  `for`-loop sum, and asserts a slice-of-slice, an empty window, and
  length-relative indexing.
- **Self-host IR** — `TestSelfHostSliceViewsIR{X86_64,Wasm}` run eight cases
  through the x86-64 + wasm IR drivers, pinned to the `"ir"` path and
  oracle-checked against the interpreter (`.len()`, index, iter-sum,
  slice-of-slice, slice-as-param, last-element, empty, `[string]` element;
  every result ≤ 126). All already lower, so **no compiler change**.

Row flipped to ✅.
### 2026-06-20 — self-host IR: `__heap_bump_bytes()` introspection builtin lowers on the IR path ([#3534](https://github.com/JakeChampion/lang/issues/3534))

The `__heap_bump_bytes()` builtin — the bump allocator's high-water mark (cursor
− region base; 0 before the first allocation) — had no self-host IR lowering and
bailed the whole module to the legacy AST emitter. Native handles it on every
backend (`internal/ir` → `__fern_heap_bump_bytes`), so this was a goal-1
IR-subset gap. New `ir.op_heap_bump_bytes`, recognised in `irlower.lower_expr`,
emitted inline by each backend from its own heap cursor: x86-64 `__fern_heap_ptr
− &__fern_heap` (the static heap symbol; `cmovne`-guarded so a still-zero cursor
reports 0), arm64 `__fern_heap_ptr − (__fern_heap_end − heap_size)` (the arena is
mmap'd, so the base is recovered from the recorded end; `csel`-guarded), and wasm
`$heap − heap_base` (the `$heap` global is initialised to `heap_base`, so it is 0
pre-alloc). The op is folded into `ir.op_allocates` — the shared heap-runtime
gate the x86 + wasm backends run over the lowered op stream — so a module that
*only* introspects the heap still emits the heap runtime; arm64 marks the heap
need in its op handler. Guarded by `TestSelfHostHeapBumpBytesIR{X86_64,Wasm}`
(routing-pinned to `ir`, exit codes cross-checked against the native x86-64
backend — the interpreter has no bump-allocator model, so it cannot be the
oracle). Self-hosting fixpoint unaffected (the self-host source does not call the
builtin, so no emission changes on the fixpoint corpus). Row flipped to 🔧.

### 2026-06-20 — 🔧 self-host wasm IR: `op_map_set` dropped value-pointerness → `Map[K, ptr]` use-after-free ([#3495](https://github.com/JakeChampion/lang/issues/3495))

The wasm IR backend hardcoded the `__fern_map_set` `vis` (value-is-RC-pointer)
flag to `0` (`wasm_ir.fern`), so a map with **pointer values** (string / array /
struct / …) never retained its values. After `m.insert(k, v)` the caller's `dec`
of the value then freed it under the map; the next allocation reused the freed
buffer, so a sibling key's value array aliased the appended one — e.g.
`query_parse("a=1&b=2&a=3")` gave `b` two values (exit 22 vs 21). The AST wasm
backend already set `vis` correctly from the value expr's type; the IR op
`op_map_set` only carried the KEY kind, so the IR path lost the value flag.
x86-64 was unaffected (its `__fern_map_set` never RC-manages values and its
`arr_dec` is leak-only). Fix: `op_map_set(keykind, valptr)` now carries
value-pointerness in the op's `unsigned` field — `irlower` computes it from the
map's value type (`map_value_is_ptr`: only known scalars are non-pointers;
everything else, incl. unknown/generic `V`, retains), `wasm_ir` emits `vis` from
it, x86-64 is untouched (still reads the key kind from `i32_imm`). Regression
guard: the `dup-keys` case in `TestSelfHostUrlQueryIR{X86_64,Wasm}` plus the
minimal `Map[string, string[]]` append-via-helper repro. Self-hosting fixpoint
unaffected (x86-64 emission byte-identical — it ignores the new flag).

### 2026-06-20 — string literals + escape sequences on the self-host IR path + native audit

Audited the foundational `String literals + escape sequences` row (was ⬜
across the board). The lexer's `scan_string` decodes the C-style escapes
`\t \n \r \0 \\ \"` plus `\xNN` hex bytes (`examples/self_host/lexer.fern` —
`apply_escape`), so a literal carrying any of them is an ordinary string box
and lowers exactly like a plain literal: `.len()`, byte indexing
(`s[i] as i32`), and `+` concat all stay on the IR path.

- **Native** — `string_escapes` fixture (interp / x86-64 / arm64 / wasm):
  prints the observable escape bytes (TAB / backslash / quote / `\x41\x7a`
  → `Az`) and byte-exact asserts every escape is one byte (incl. an embedded
  NUL via `\0` and `\x00` — length-prefixed strings count it, unlike C).
- **Self-host IR** — `TestSelfHostStringEscapesIR{X86_64,Wasm}` run nine
  cases through the x86-64 + wasm IR drivers, pinned to the `"ir"` path and
  oracle-checked against the interpreter (`.len()`, byte index, concat, mixed
  escapes; every result ≤ 126). All already lower, so **no compiler change**.

Row flipped to ✅. (Found while auditing the adjacent ⬜ `f-strings /
interpolation` row: f-string interpolation desugars to `(expr).to_string()`,
which on the self-host IR path is special-cased for `i32` and string-identity
so importless `f"{i32val}"` compiles + routes `"ir"` — but the **native**
checker rejects the same importless program with `E043` because `to_string`
isn't in scope without `import "std/i32"` / `"std/float"`. That self-host
over-permissiveness vs. native is a separate divergence, filed for follow-up;
this entry scopes only the import-free escape-sequence surface, which both
compilers agree on.)

### 2026-06-21 — core/iter: closure-free queries `contains` / `count_value` ([#2686](https://github.com/JakeChampion/lang/issues/2686))

Two i32 equality queries, no closure: `contains[I: Iterator[i32]](it, target):
boolean` and `count_value[I: Iterator[i32]](it, target): i32`. Notably
`contains` returns `boolean` from a *direct* bounded-generic function — which
lowers fine on the self-host IR path (routing `ir`), confirming the
boolean-return self-host gap is specific to *indirect* closure-through-fn-param
calls (#3628), not boolean returns in general. Coverage: `contains-count-value`
in `TestNativeGenericIteratorCollector` + `TestSelfHostGenericCollectorIR{X86_64,
Wasm}` (routing-pinned to `ir`), and `iter.contains`/`count_value` via the
shipped module in `TestNativeIteratorTraitModuleAdapters`. Fixpoint unaffected
(`core/iter` is standalone).

### 2026-06-21 — core/iter: more closure-free adapters `last` / `product` / `position` ([#2686](https://github.com/JakeChampion/lang/issues/2686))

Three more closure-free terminal ops, same proven shape (scalar / `Option`
returns consumed directly, no closure, no erased-generic-struct return — so
they avoid both characterised self-host codegen gaps and lower on every
backend incl. the self-host IR path): `last[T, I: Iterator[T]](it): Option[T]`
(generic), `product[I: Iterator[i32]](it): i32`, and `position[I:
Iterator[i32]](it, target): Option[i32]` (first index equal to `target`; the
closure-taking `find` / `position_by` still await the boolean-indirect-return
fix). Coverage: `product-position` / `last-opt` in
`TestNativeGenericIteratorCollector` + `TestSelfHostGenericCollectorIR{X86_64,
Wasm}` (routing-pinned to `ir`), and `iter.product`/`last`/`position` via the
shipped module in `TestNativeIteratorTraitModuleAdapters`. Fixpoint unaffected
(`core/iter` is standalone).

### 2026-06-21 — core/iter: closure-free adapters `nth` / `min` / `max` ([#2686](https://github.com/JakeChampion/lang/issues/2686))

Added three adapters that take NO closure, so they sidestep the boolean-indirect-
return self-host IR gap (below) and lower fully on every backend incl. the
self-host IR path: `nth[T, I: Iterator[T]](it, n): Option[T]` (generic), and
`min`/`max[I: Iterator[i32]](it): Option[i32]` (i32 — they need `<`/`>`).
Coverage: `nth-i32` / `min-max-i32` in `TestNativeGenericIteratorCollector` +
`TestSelfHostGenericCollectorIR{X86_64,Wasm}` (routing-pinned to `ir`), and
`iter.nth`/`min`/`max` via the shipped module in
`TestNativeIteratorTraitModuleAdapters`. `core/iter` imports nothing and nothing
imports it, so the self-host fixpoint is unaffected. **Deferred:** `skip[T, I:
Iterator[T]](it, n): I` (return the SAME iterator type advanced `n`) works on
native (interp/x86-64/wasm) and the self-host **wasm** IR path but crashes on
the self-host **x86-64** IR backend — returning an erased generic struct value
`I` hits an x86-specific gap; held back pending that fix.

### 2026-06-21 — self-host IR gap characterised: `boolean`-returning closures called through a function-typed param ([#2686](https://github.com/JakeChampion/lang/issues/2686) tail)

Root-caused the self-host IR crash first seen as the `fold` "A≠T" case (#3618).
The real trigger is narrower and type-specific: **a closure whose RETURN type
is `boolean`, invoked INDIRECTLY through a function-typed parameter,
miscompiles on the self-host IR path** — it routes `ir` and emits, then
crashes at runtime (exit -1). A return-type sweep over the minimal repro
`function apply(x: i32, f: (i32) => R): R { return f(x); }` pins it: `R = i32`,
`R = f64`, and `R = string` all lower and run correctly on the self-host IR
path; `R = i64` routes to the AST emitter (correct via fallback); only
`R = boolean` stays on the IR path and crashes. So the #3618 "A≠T" framing was
the symptom — that closure was `(boolean, i32) => boolean`, i.e. a
boolean-return indirect call. This blocks the predicate iterator adapters
(`any` / `all` / `find`, all `(T) => boolean`) from the self-host IR path; they
work on every native backend and are pinned by `TestNativeGenericPredicateAdapters{,Arm64}`
as the behavioural spec, kept OUT of the shipped `core/iter` until the codegen
gap is fixed (a focused follow-up — a conservative AST-fallback guard is
fixpoint-risky because the compiler itself may use such closures, so the IR
result path needs the real fix). A directly-called local closure with a
boolean return is unaffected (works); only the indirect (fn-typed-param) call
is wrong.

### 2026-06-21 — core/iter: generic `fold` reducer ([#2686](https://github.com/JakeChampion/lang/issues/2686))

Added `fold[T, A, I: Iterator[T]](it: I, init: A, f: (A, T) => A): A` — the
fundamental left reduction the other drivers are special cases of (`sum` is
`fold(it, 0, +)`). It carries THREE type parameters (element `T`, accumulator
`A`, iterator `I`) and a closure combiner, exercising bound-driven inference
(#3596) alongside a function-type parameter. `A` may differ from `T` (e.g.
fold an i32 iterator into a boolean). The common `A = T` case (sum / product /
min / max) works on every native backend AND the self-host IR path (`fold-sum`
in `TestNativeGenericIteratorCollector` / `TestSelfHostGenericCollectorIR{X86_64,
Wasm}`, routing-pinned to `ir`), plus `iter.fold` via the shipped module in
`TestNativeIteratorTraitModuleGeneric`. **Known gap:** when the accumulator
type differs from the element type (`A ≠ T`), the self-host IR path
miscompiles a *closure-accumulator* fold — it routes `ir` and emits, but the
closure call ABI passes the differently-typed accumulator wrong and the
program crashes; native handles it. Pinned native-only by
`TestNativeGenericFoldCrossType`; the A≠T self-host fix is a follow-up.

### 2026-06-21 — core/iter generalised to a generic `Iterator[T]` ([#2686](https://github.com/JakeChampion/lang/issues/2686))

With parametrised-trait bounds parsing on the self-host (#3558) and bound-
driven inference on native (#3596), the shipped `core/iter` trait is no
longer i32-locked: `pub trait Iterator[T] { next(self): Option[(T, Self)]; }`,
`Range` provides `impl Iterator[i32]`, and the drivers `count[T, I:
Iterator[T]]` / `to_array[T, I: Iterator[T]]: T[]` are generic over the
element type (`sum[I: Iterator[i32]]` stays i32 — it needs `+`). Backward
compatible: `iter.sum/count/to_array(iter.range(…))` still infer `T = i32`
and return the same types (the module test still returns 27). The generic
face is exercised by `TestNativeIteratorTraitModuleGeneric` — a user
`impl iter.Iterator[boolean]` driven through the module's generic
`count`/`to_array` — plus a `to_array` (generic `T[]` return) case added to
`TestNativeGenericIteratorCollector` / `TestSelfHostGenericCollectorIR`
(routing-pinned to `ir`, runs on the self-host). `core/iter` imports nothing
and nothing imports it, so the self-host fixpoint is unaffected.

### 2026-06-21 — bound-driven inference: fully-generic iterator collectors (`f[T, I: Iterator[T]]`) ([#2691](https://github.com/JakeChampion/lang/issues/2691) step 2)

A function generic over **both** the iterator and its element type —
`count[T, I: Iterator[T]](it: I): i32`, `last[T, I: Iterator[T]](it: I,
dflt: T): T` — where `T` appears only inside another parameter's
parametrised-trait bound. Previously the native checker reported `E040:
could not infer type parameter T`. Now **bound-driven inference** recovers
`T` from the impl the bound resolves to: once `I` is pinned to a concrete
type, the bound's trait args (`Iterator[T]`) unify against that type's
impl's trait args (`Iterator[i32]` / `Iterator[boolean]`) to bind `T`, with
a fixpoint loop so one bound param can feed another. The lever is
normalising bound type-arg leaves to `ParamType` (`normalizeParamRefs`) so
`bindBoundParam` / `substBoundArg` treat `T` uniformly; E021 bound-
satisfaction resolves `T` through the substitution before comparing against
the impl. The native backend monomorphises fully so it needs the inferred
`T`; the **self-host IR path** erases the unbounded `T` (uniform 8-byte
slot) and monomorphises on `I`, so #3558's parametrised-bound parsing
already suffices there — no self-host change. The SAME generic `last` runs
at `T=i32` and `T=boolean`, proving genuine genericity. Coverage:
`TestCheckBoundDrivenInference` (checker), `TestNativeGenericIteratorCollector{,Arm64}`
(interp/x86-64/wasm/arm64), `TestSelfHostGenericCollectorIR{X86_64,Wasm}`
(routing-pinned to `ir`). Self-host fixpoint unaffected (checker change is
native-only). See docs/TRAITS.md §4a.

### 2026-06-20 — self-host: parametrised-trait bounds (`[I: Iterator[i32]]`) lower on the IR path ([#2691](https://github.com/JakeChampion/lang/issues/2691) step 1)

Generic-trait support on the self-hosted compiler: a bounded generic whose bound
is a **parametrised-trait instantiation** — `function f[I: Iterator[i32]](…)` —
now monomorphises and lowers through the IR path. Before this the self-host
type-parameter **bound parser** (`parser.fern`) consumed `Trait (+ Trait)*` with
an optional `mod.` qualifier but **not** the trait's generic args, so it stopped
at the `[` in `Iterator[i32]`, mis-parsed the rest of the param list, and bailed
the whole module to the legacy AST emitter (isolated via an `asm_pathprobe_run`
sweep: the non-parametrised bound `[T: Area]` already routed `ir`; the
parametrised `[I: Iterator[i32]]` routed `ast` even for a scalar-returning
method). Fix: after the bound trait name (and `mod.` qualifier), consume the
balanced `[ … ]` type-arg list. This is the keystone (epic #2691 step 1) for a
generic — not i32-locked — `Iterator[T]`: a generic trait declaration
`Iterator[T]`, a parametrised impl `impl Iterator[i32] for R`, a method returning
`Option[(T, Self)]`, and a bounded-generic driver `sum[I: Iterator[i32]]` now all
lower on the self-host IR path. Coverage: `TestSelfHostGenericTraitBoundIR{X86_64,
Wasm}` (routing-pinned to `ir`, oracle-checked against native) + the native
`TestNativeGenericTraitBound{,Arm64}` cross-check, incl. a two-impls / one-driver
case (two monomorphic clones). Self-host fixpoint stays byte-identical (modload +
stage2): the compiler's own source uses no parametrised-trait bounds, so the new
parse branch never fires on the fixpoint corpus. The `core/iter` row's
generic-`Iterator[T]` caveat is updated accordingly.

### 2026-06-20 — core/iter: numeric Iterator trait + Range on native and the self-host IR path ([#2686](https://github.com/JakeChampion/lang/issues/2686))

First slice of the iterator protocol (the trait-spine epic #2691 step 6, and the
open tail of the Range issue #2699). New `internal/stdlib/core/iter.fern`: a
numeric (i32) `Iterator` trait — `next(self): Option[(i32, Self)]`, value-
semantic (advancing yields a fresh iterator, no interior mutation) — an integer
`Range` (`[lo, hi)`) implementing it, a `range(lo, hi)` constructor, and eager
bounded-generic drivers `sum` / `count` / `to_array`. So a range is now a first-
class iterable value: `iter.sum(iter.range(0, n))`.

The trait is **fixed to i32** ("numbers") on purpose: a *generic* `Iterator[T]`
needs a bound on a parametrised-trait instantiation (`[I: Iterator[T]]`), which
the self-host IR path does not yet monomorphise (it routes such programs AST —
isolated via the `asm_pathprobe_run` sweep; the non-generic i32 form routes
`ir`). The i32 form rides the already-mature trait machinery on BOTH compilers
(concrete struct impl + bounded generics over a plain trait bound + tuple-in-
Option returns), so it lowers natively (interp / x86-64 / arm64 / wasm) and on
the self-host IR path (x86-64 + wasm) with no compiler change — pure stdlib +
tests. Coverage: `TestNativeIteratorTrait{,Module,Arm64}` (the module test
exercises the real `import "core/iter"`) and `TestSelfHostIteratorTraitIR{X86_64,
Wasm}` (routing-pinned to `ir`, oracle-checked, results ≤ 120 for the wasmtime
exit-code clamp). A generic `Iterator[T]` (and a Cell-backed mutable cursor) are
later slices.

### 2026-06-20 — core/int int_to_string_radix on the self-host IR path + native audit

Follow-up to the radix-**parse** audit (#3515): that entry put the whole
to-string direction on the AST path, but only the **decimal** `int_to_string` /
`__int_to_string_u64` actually stay AST (they `__memcpy` over a `usize` pointer).
`int_to_string_radix` builds its result with `__alloc_u8` + `.with` +
`string_from_bytes` — no `__memcpy`, no `usize` — the same IR-eligible builder
std/hex / std/base64 use, so it lowers through the IR path too.

- **Native** — `core_int_radix` fixture (interp / x86-64 / arm64 / wasm): hex,
  binary, base-36, zero, negative, and a multi-digit value.
- **Self-host IR** — `TestSelfHostCoreIntRadixIR{X86_64,Wasm}` run nine cases
  through the x86-64 + wasm IR drivers (i64-magnitude `mag % b64` / `mag / b64`,
  `__alloc_u8` + `.with` build, `string_from_bytes`, plus a round-trip back
  through the IR-audited `parse_int_radix`), pinned to the `"ir"` path and
  oracle-checked against the interpreter (results ≤ 120 for the wasm clamp,
  #2908). All already lower, so **no compiler change**.

The core/int row's to-string note is corrected accordingly; `int_to_string`
(decimal) remains the only AST holdout.

### 2026-06-20 — core/int radix parse on the self-host IR path + native audit

Audited the `core/int` row (was ⬜ across the board) — the **parse** direction.
`parse_int_radix` (bases 2–36, optional `+`/`-` sign) + its `__radix_digit`
char classifier now have native coverage via the `core_int_parse` fixture
(interp / x86-64 / arm64 / wasm) and self-host coverage via the IR path:
`TestSelfHostCoreIntParseIR{X86_64,Wasm}` run the parser through the x86-64 +
wasm IR drivers, pinned to the `"ir"` path (return value = a small deterministic
int, oracle-checked against the interpreter). It exercises `Option[i32]`
`Some`/`None` returns with a payload-binding `match`, string indexing (`s[i]`)
with char-class comparisons, a multiply-accumulate `while` loop, sign handling,
and negation — all already lower, so no compiler change. The two functions are
inlined verbatim (core/int has no reserved type names; the single-program
driver resolves no imports). The **`to_string`** direction (`int_to_string` /
`int_to_string_radix`) stays on the AST path — it pokes raw memory via
`__alloc_u8` / `__memcpy` / `usize`, the same low-level concern that keeps
std/u64 `to_string` off the IR path; row marked 🔧 (parse on IR, to_string AST).

### 2026-06-20 — std/io_buffered BytesWriter on the self-host IR path + native audit

Audited the `std/io_buffered` row (was ⬜ across the board). The module's Phase-1
surface is entirely the in-memory `BytesWriter` (`data: u8[]`; `write_string` /
`write_bytes` / `write_byte` accumulate, `len` / `is_empty` / `into_bytes` /
`into_string` / `reset` inspect — the fd-backed buffered Reader/Writer is
deferred to Phase 2). It now has native coverage via the `bytes_writer` fixture
(interp / x86-64 / arm64 / wasm) and self-host coverage via the IR path:
`TestSelfHostBytesWriterIR{X86_64,Wasm}` run the surface through the x86-64 +
wasm IR drivers, pinned to the `"ir"` path (return value = a small deterministic
int, oracle-checked against the interpreter). It exercises a struct with a
`u8[]` field, functional struct-spread update appending to that array
(`BW { ...w, data: … }`), `u8[].append` with `as u8` element casts, indexed
byte reads, and the `string_from_bytes` builtin via `into_string` — all already
lower, so no compiler change. The type is inlined as `BW` (`BytesWriter` is a
reserved builtin type name + the single-program driver resolves no imports);
`write_string` extracts bytes via `s[i] as u8` in place of the module's
`s.bytes()` (a std/string method the importless driver has no import for).
`std/io_buffered` row flipped to ✅.

### 2026-06-20 — std/mock_platform call-recording log on the self-host IR path + native audit

Audited the `std/mock_platform` row (was ⬜ across the board). The Phase-1
call-recording infrastructure — `MockPlatform` holds a `MockCall[]` effect log;
`record` appends, `call_count` / `reset` / `has_call` / `find_call` inspect it —
now has native coverage via the `mock_platform_log` fixture (interp / x86-64 /
arm64 / wasm) and self-host coverage via the IR path:
`TestSelfHostMockPlatformIR{X86_64,Wasm}` run the surface through the x86-64 +
wasm IR drivers, pinned to the `"ir"` path (return value = a small deterministic
int, oracle-checked against the interpreter). It exercises a struct holding an
array-of-struct field (`MCall[]`), functional struct-spread update appending to
that array (`MPlat { ...m, calls: m.calls.append(MCall { … }) }`), indexed
array-of-struct field reads (`m.calls[i].name`), a membership scan with string
equality, and `find_call`'s `Option[MCall]` (Option of a struct) with a
payload-binding `match` — all already lower, so no compiler change. The types
are inlined as `MPlat`/`MCall` (`MockPlatform`/`MockCall` are reserved builtin
type names + the single-program driver resolves no imports). `std/mock_platform`
row flipped to ✅.

### 2026-06-20 — std/stream byte Stream on the self-host IR path + native audit

Audited the `std/stream` row (was ⬜ across the board). The in-memory byte
`Stream` (`data: u8[]` + `pos` read cursor) follows the value-threaded CURSOR
IDIOM — a read returns `(value, advancedStream)` and the caller rebinds — and
now has native coverage via the `stream_reader` fixture (interp / x86-64 /
arm64 / wasm) and self-host coverage via the IR path:
`TestSelfHostStreamIR{X86_64,Wasm}` run `len` / `remaining` / `read_byte` /
`read_n` / `read_all_string` / `read_line` (CRLF/LF handling + unterminated
tail) through the x86-64 + wasm IR drivers, pinned to the `"ir"` path (return
value = a small deterministic int, oracle-checked against the interpreter). It
exercises a struct with a `u8[]` field + an i32 cursor, functional struct-spread
update (`Buf { ...s, pos: … }`), tuple-returning methods with pointer + `Option`
elements (`(u8[], Buf)`, `(Option[i32], Buf)`, `(Option[string], Buf)`), tuple
destructuring in `let`, `u8[].append` with `as u8` element casts, indexed byte
reads with `as i32`, the `string_from_bytes` builtin, and `Option` `Some`/`None`
with a payload-binding `match` — all already lower, so no compiler change. The
type is inlined as `Buf` (`Stream` is a reserved builtin type name + the
single-program driver resolves no imports). `std/stream` row flipped to ✅.

### 2026-06-21 — `strbuf_reset/append/take` audited; interp gap found + filled (#3579)

Audited the `strbuf_reset/append/take` row (was ⬜ across the board) — the global
string-builder primitive (the AOT backends back it with a 64 MiB BSS scratch
buffer; reset zeroes the length, append memcpys a string's bytes past the tail,
take allocates a fresh string of the accumulated bytes and resets). The audit
found the **interpreter had no implementation at all** — a program using it
errored with `undefined function "strbuf_reset"` (exit 1) even though the checker
knows the signatures (`FuncSigs`) and the native + self-host IR backends lower
it. Filled in [#3579](https://github.com/JakeChampion/lang/pull/3579): a `strbuf
[]byte` accumulator + the three builtins, matching the documented + self-host
semantics, with `interp_strbuf_test.go` cross-checking the interp exit against
native x86-64 across two/three/loop appends, byte-content, and an empty take.

Coverage: interp (#3579), native x86-64 (the same cross-check), native arm64
(`arm64_strbuf_test.go`), and the self-host IR path (`self_host_strbuf_ir_test.go`
+ `self_host_strbuf_buffer_test.go`). **Native wasm does not implement strbuf**
(`wasmbin` reports `unknown callee "strbuf_reset"`), so the W column is left
blank — the builder is an x86-64 / arm64 / self-host-only primitive (it backs the
self-host emitters' O(N) `EmitState.write`, which the wasm target doesn't need).

Two adjacent observations, **not** filled here (filed/left for follow-up): a
native optimizer drops the side effect of a *second* `strbuf_reset` / `strbuf_take`
in one function (the x86-64 codegen emits the reset, but it's elided at runtime —
a native-codegen issue), and the interp/IR-vs-native `i32` signed-overflow split
([#3581](https://github.com/JakeChampion/lang/issues/3581)) surfaced in the same
differential sweep. Row flipped to ✅ (W blank).

### 2026-06-20 — std/url `query_parse` on the self-host IR path (+ found wasm-IR map bug #3495)

Audited `query_parse` (query string → `Map[string, string[]]`, duplicate keys
accumulate). `TestSelfHostUrlQueryIR{X86_64,Wasm}` run the inlined parser through
the x86-64 + wasm IR drivers, pinned to the `"ir"` path. It exercises a `Map`
with string keys and **string-ARRAY values** built via `Map {}` / `.get` /
`.insert`, the append-or-create idiom over the map's `string[]` value,
`Option[string[]]` `Some`/`None` `match`, and `url_decode`'s `u8[]` +
`string_from_bytes` — all already lower, so no compiler change. The native
`url_codec` fixture also exercises `query_parse` incl. duplicate-key
accumulation.

The audit surfaced a **self-host wasm-IR bug**: query_parse's duplicate-key path
(append to an existing `string[]` map value, where the strings come from
slices / `url_decode`) yields the wrong count on a sibling key on the wasm IR
backend (`a=1&b=2&a=3` → b gets 2 values, exit 22 vs 21) — correct on x86-64 IR
and every native backend. Filed as [#3495](https://github.com/JakeChampion/lang/issues/3495)
(looks like the `Map[K, string[]]` analog of the known "map array values"
RC gap in #2857); the dup-key case is omitted from the self-host pinned set and
kept on the native side. The other `std/url` self-host pieces
(`url_encode`/`url_decode`/`url_parse` + single-value `query_parse`) are all ✅.

### 2026-06-20 — std/url `url_parse` on the self-host IR path

Extended the `std/url` self-host audit to `url_parse` (URL → 6-field `Url`
struct). `TestSelfHostUrlParseIR{X86_64,Wasm}` run the inlined parser through the
x86-64 + wasm IR drivers, pinned to the `"ir"` path (return value = a chosen
component's length / port). It exercises a 6-field struct with mixed string + i32
fields, repeated functional struct-spread updates (`Url { ...u, host: …, port:
… }`), string slicing, byte scanning, and `Option[Url]` `Some`/`None` returned
and read via a payload-binding `match` — all already lower, so no compiler
change. The native `url_codec` fixture now also exercises `url_parse`
(round-trip + the empty→`None` case). `query_parse` (builds a `Map`) is still a
later slice.

### 2026-06-20 — std/url percent-encoding on the self-host IR path + native audit

Audited the `std/url` self-host column (was blank). `url_encode` / `url_decode`
— RFC 3986 percent-coding — now route through the IR path on x86-64 + wasm
(`TestSelfHostUrlCodecIR{X86_64,Wasm}`, pinned to `"ir"`, hardcoded expectations
verified against native interp + x86-64) and have a native `url_codec` fixture
(interp / x86-64 / arm64 / wasm). It exercises byte classification, bit ops
(`>>` / `&` / `<<` / `|`), `u8[]` array literals with `as u8` element casts, and
the `string_from_bytes(u8[])` builtin — all already lower, so no compiler change.
(`url_parse` / `query_parse`, which build a `Map`, are left for a later slice.)

### 2026-06-20 — std/sort on the self-host IR path + native audit

Audited the `std/sort` row's self-host `S` column (was blank — native was
already covered by the `prop_sort_i32` fixture across all four backends, but
the self-hosted compiler had never been pinned to lower the sorts through its
IR path). `TestSelfHostSortIR{X86_64,Wasm}` run the sort surface — the i32
ascending / descending insertion sorts, the byte-lexicographic `string_cmp`
three-way comparator, and the `string[]` ascending / descending sorts built on
it — through the x86-64 + wasm IR drivers, pinned to the `"ir"` path (return
value = a small deterministic int, oracle-checked against the interpreter). It
exercises scalar (`i32[]`) and pointer (`string[]`) array build via `.append`,
in-place element rewrite via `.with`, indexed scalar + string-byte reads,
`.len()`, numeric `<`/`>` comparisons, and the nested-`while` insertion-sort
shift — all already lower, so no compiler change. The sort surface is inlined
verbatim from `internal/stdlib/std/sort.fern` (the single-program driver
resolves no imports); the case-insensitive `string_cmp_ci` / `*_ci` sorts and
the `own` in-place sorts are out of scope (they depend on a char `.to_lower()`
and the affine `own`-parameter path respectively). `std/sort` S column flipped
to ✅.

### 2026-06-20 — 🔧 self-host IR: user receiver method named `len` mis-dispatched to the builtin `.len()` ([#3478](https://github.com/JakeChampion/lang/issues/3478))

`irlower.fern` intercepted **every** zero-arg `.len()` call as the builtin
string/array length — without checking the receiver's type. For a struct
receiver with a user-defined `(b: T) len()` method, the receiver isn't a string,
so the intercept fell through to `op_arr_len`, reading the struct box as if it
were an array header (garbage: x86-64 → 26, wasm → 0) instead of dispatching the
user method. The native compiler resolves the user method over the builtin, so
this was a self-host-IR-only divergence (every native backend returned the right
value). Fix: guard the intercept with `expr_struct_type(fa.obj, s) == ""` — a
struct has no builtin `.len()`, so `b.len()` on a struct must be the user method,
which now falls through to normal receiver-method dispatch. (The sibling
`append`/map intercepts were already receiver-type-guarded; `len` was the only
unguarded one.) Discovered while auditing `std/headers`; pinned by the
`append-len` case in `TestSelfHostHeadersIR{X86_64,Wasm}` and a minimal
`(b: Box) len()` vs `count()` differential. No change to string/array `.len()`.

### 2026-06-20 — std/headers `HeaderMap` on the self-host IR path + native audit

Audited the `std/headers` row (was ⬜ across the board). The `HeaderMap` surface
— case-insensitive `get` / `get_all` / `append` / `set` over two parallel
`string[]` fields with case-folded keys — now has native coverage via the
`headers_map` fixture (interp / x86-64 / arm64 / wasm) and self-host coverage
via the IR path: `TestSelfHostHeadersIR{X86_64,Wasm}` run the inlined map
through the x86-64 + wasm IR drivers, pinned to the `"ir"` path (return value =
a small deterministic int). It exercises a struct with two `string[]` fields,
functional struct-spread update (`Headers { ...h, names: …, values: … }`),
`string[].append`, indexed string-field compares, `Option[string]`
`Some`/`None` with a payload-binding `match`, and chained struct-returning
receiver methods — all already lower, so no compiler change. The type is
inlined as `Headers` (`HeaderMap` is a reserved builtin name) and `.to_lower()`
as a local lookup-slice `lower` (the single-program driver resolves no imports);
expectations are hardcoded, verified against the native interp + x86-64 backends.
The audit also surfaced a self-host IR miscompile: the `(h) len()` receiver
method (`return h.names.len();`) returns a callee local's value (x86-64 → 26, the
`lower` lookup-string length; wasm → 0) instead of 2 — filed as
[#3478](https://github.com/JakeChampion/lang/issues/3478) and omitted from the
pinned set (reading `.len()` directly on a returned value is correct).

### 2026-06-20 — std/log leveled `Logger`/`LogEntry` on the self-host IR path + native audit (#2683)

Audited the `std/log` row (was ⬜ across the board). The leveled-logger surface
(#2683) — `Logger` / `LogEntry` structs, the chained builder
(`lg.info_().str(...).int(...).bool(...)`), the min-level threshold filter, and
the pure `render` producing plain-text (`[LEVEL] msg key=value`) or JSON-lines
(`{"level":..,"msg":..,<fields>}`) — now has native coverage via the
`log_leveled` fixture (interp / x86-64 / arm64 / wasm, byte-for-byte) and
self-host coverage via the IR path: `TestSelfHostLogLeveledIR{X86_64,Wasm}` run
the inlined logger through the x86-64 + wasm IR drivers, pinned to the `"ir"`
path (return value = rendered length). It exercises structs with
i32/boolean/string fields, struct-returning receiver methods chained, struct
field reads, the filter branch, byte-indexed JSON escaping, and
`i32.to_string()` + concat — all already lower, so no compiler change.
Expectations are hardcoded (verified against the interpreter with
`import "std/i32"`): the single-program driver treats `.to_string()` as a
self-host builtin while the importless interpreter can't resolve it, so the
interp isn't a drop-in oracle here (same caveat as `TestSelfHostFormatBytesIR`).

### 2026-06-20 — std/format width/precision/fill specs + std/float `to_string_prec` ([#2684](https://github.com/JakeChampion/lang/issues/2684))

`std/format.format` now parses Rust-style `{:[[fill]align][width][.precision]}`
specs on top of the existing `{}` substitution: `<`/`>`/`^` alignment, a custom
fill char (`{:*>8}`), minimum width padding, and `.N` string-precision
truncation — applied to the already-stringified arg, so no trait dispatch is
needed (generic `Display` remains the deferred half of #2684). A `{…}` run whose
body isn't empty and doesn't start with `:` (e.g. JSON `{"k":1}`) now renders
literally and consumes no arg. Paired with `std/float`'s new
`to_string_prec(prec)` (f32/f64) — fixed `prec` fractional digits, rounded half
away from zero, computed entirely in the float domain (`__float_to_string_prec`
→ `__float_int_part`, no `f64→i64` trap boundary). Native across all four
backends via the `format_specs` fixture; self-host via the IR path (x86-64 +
wasm) with `TestSelfHostFormatSpecIR{X86_64,Wasm}` — the inlined spec machinery
(forward `}`-scan, `s[a:b]` slices, byte compares, `boolean`-returning helpers
with `||`, int-coded align, fill-repeat concat loop) oracle-checked against the
interpreter, pinned to the `"ir"` path. No compiler change.

### 2026-06-16 — u64[] arrays on the self-host IR path (widen IR subset)

`u64[]` locals / params / aliases — bind from a literal, index, iterate, and
8-byte round-trip — now route the self-host IR path on **both x86-64 and wasm**.
i64[] / f64[] already rode the 8-byte-element path (`op_arr_make_i64` + the
`is_i64arr` element-width mark); u64[] was deferred.

- **irlower.** The `var xs: u64[] = [...]` literal binding takes the same dedicated
  8-byte branch as i64[] (`op_arr_make_i64`), and the binding annotation marks the
  slot `is_i64arr` (8-byte element reads). The slot is additionally marked `is_u64`
  so a read element gets UNSIGNED arithmetic (`is_arr && is_u64` — the only
  u64-vs-i64 difference).
- **The cross-backend fix.** `is_i64_slot` previously returned true for *any*
  u64-marked slot — including a u64[] array slot — so the wasm backend declared the
  local `(local i64)` though it holds an i32 array pointer, and the wasm verifier
  rejected the array-make ("expected i64, found i32"). `is_i64_slot` now excludes
  ARRAY (pointer) slots: a u64[] slot is an i32 pointer; its u64-ness is about its
  ELEMENTS, handled at the element read. (i64[] was unaffected — it uses the
  separate `is_i64arr` mark, never `is_i64`, so its slot was already i32.)
- **Soundness / scope.** Element storage + reads are 8-byte; values match the
  interpreter oracle (incl. a >2³² round-trip). u64[] as a STRUCT FIELD and a
  u64[]-RETURNING function stay on the AST path for now (field-tag width dispatch
  and the return-type registry — separate increments). The change only affects
  slots that are `is_arr && is_u64` (i.e. u64[]), which never reached IR before, so
  no self-host source changes classification and the Stage-2 fixpoint holds.
- **Tests.** `internal/e2e/self_host_u64_array_ir_test.go` —
  `TestSelfHostU64ArrayIR{X86_64,Wasm}`, 7 cases each routing-pinned `"ir"` and
  oracle-checked (len, index, iterate, alias, 8-byte wide-value, u64[] param, plus
  an i64[] regression guard).

### 2026-06-16 — self-referential / mutually-recursive structs on the self-host IR path (widen IR subset)

`struct Node { v: i32, next: Node[] }` (and mutually-recursive `A { bs: B[] }` /
`B { peers: A[] }`) — the shape behind linked lists, trees, and ASTs — now route
the self-host IR path. Previously a self-referential struct fell to the AST emitter.

- **Root cause.** The leak-safety gate `irlower.decl_is_leaksafe_d` walks a
  struct's field type graph to admit it to the IR path in *leak mode* (no RC; the
  boxes leak with the struct, matching the AST path's exit codes). It used a
  depth cap (`depth > 16`) purely to avoid looping on cyclic type graphs — but
  that same cap rejected legitimate self-reference: a `next: Node[]` field
  recurses into `Node` forever until the cap trips, bailing the whole module.
- **Fix.** Thread a `visiting` set of struct names on the current proof path. A
  field whose (element) type is already being proven is a leak-only *back-edge* —
  a self- or mutually-recursive pointer slot that leaks with the struct, so it
  introduces no unsafe field. Short-circuit such an edge as leak-safe; the outer
  struct's own fields are each still validated. The depth cap stays as a backstop.
- **Soundness.** Leak mode only (no deep drop), exactly as the existing
  non-recursive array-of-struct field (`is_struct_array_field_type`) already
  rides; values match the interpreter oracle. The self-host's *own* recursive
  types go through the `Ty` **union** (`TyOption { inner: Ty }`), handled as a
  nominal-enum field — not a struct back-edge — so no self-host source struct
  changes classification and the Stage-2 byte-identical fixpoint holds.
- **Tests.** `internal/e2e/self_host_selfref_struct_ir_test.go` —
  `TestSelfHostSelfrefStructIR{X86_64,Wasm}`, 7 cases (bind + scalar read, empty /
  filled `Node[]` length, element field read, children-sum loop, struct as
  param/return, mutual recursion), each routing-pinned `"ir"` and oracle-checked.

### 2026-06-15 — labeled break / continue on the self-host IR path (widen IR subset)

`outer: while/for { … break outer; … continue outer; … }` now parses, resolves,
and lowers through the self-host IR path (previously the `outer:` label prefix
failed to parse → module fell to AST). Design (deliberately low-risk):
- **Parser** records a loop label on `StmtWhile`/`StmtFor` (lookahead on a leading
  `IDENT :` before a loop keyword) and a target label on `StmtBreak`/`StmtContinue`
  (`break outer` / `continue outer`); new `label` field on all four structs.
- A **`resolve_labels` pass** runs at the single shared parse entry (`parse_module`,
  before any desugar — and idempotent, so re-running in the prepare chain is
  harmless) and bakes each labeled break/continue's RELATIVE loop depth into its
  existing `tag` field (0 = innermost). This collapses the risk surface: downstream
  passes never need to preserve loop labels, only the already-resolved `tag` (which
  break/continue carry by identity).
- **irlower** break/continue lower to `loop_blk_at_depth(tag)` (tag 0 reproduces
  the old innermost-targeting exactly, so unlabeled behaviour is unchanged).
  All backends emit the same `op_br`, so x86/arm worked immediately; the universal
  `parse_module` placement was needed so the wasm driver (which assembles its own
  prep pipeline) also sees resolved tags — its native multi-level `br` then lands
  correctly.
`ssa.fern` is off the active asm_run/irlower path, so its `tag != 0` guard is moot
here. Verified on x86-64, wasm, and arm64 (qemu) against the interpreter:
`break outer`, `continue outer`, labeled `for`, triple-nested `continue mid`,
`break` out of a `match` arm; plain (unlabeled) break/continue regress. Fixpoint
still converges byte-identically (the compiler's own sources use no labeled loops,
so resolution is a no-op there). Guarded by `TestSelfHostLabeledBreakIR{X86_64,Wasm}`.

### 2026-06-15 — oracle-checked modload coverage: std/u32, std/u64, std/i64, std/sort (coverage)

Extends the self-host **modload** coverage (the `asm_load_run` driver that resolves
`import "std/…"` / `core/…` transitively and lowers the whole program). Unlike the
existing `TestSelfHostStdlibImport` (hardcoded exits), each case is **oracle-checked
against the interpreter** — the interpreter resolves the same imports, so it's an
apples-to-apples oracle for the multi-module program. New coverage: the int-method
modules `std/u32` / `std/u64` / `std/i64` (→ `core/int`) — `.min` / `.max` /
`.clamp` / `.abs` / `.gcd` / `.pow` — plus more `std/sort` variants
(`sort_i32_desc`, `sort_u32_asc`). x86-64 only (the loader driver takes argv file
paths, so it can't run under the qemu runner — mirrors the existing import test's
gate). Coverage-only, no compiler change. Guarded by
`TestSelfHostStdlibModloadIRX86_64`.

Surfaced while writing the cases (not a loader/IR bug): an **unqualified** call to
an imported free function (`range(…)` after `import "std/math"`, rather than
`math.range`) is correctly rejected by the native checker (`error[E001]: undefined
identifier`), and the self-host loader correctly fails to link it — but the *native
interpreter* over-leniently accepts and runs it. So the qualified form is the only
valid one (matching the checker), and the discrepancy is an interpreter-leniency
quirk, not a loader gap. The shipped cases all use methods or qualified `mod.fn`
calls accordingly.

### 2026-06-15 — std/crypto, std/path, std/math via the self-host IR path (coverage)

Same vehicle as the std/hex / std/base64 coverage: each REAL no-import std module
is concatenated with a `main` (single-module, no modload), routing-pinned to `"ir"`
and oracle-checked on x86-64 + wasm (verified on arm64 via qemu). **std/crypto** is
the standout — `sha256_hex` / `sha256_bytes` / `hmac_sha256_hex` drive the entire
u32-heavy SHA-256 message schedule + compression (rotr / `shr_u` / wrapping add)
through the IR path end-to-end. **std/path** (`path_join` / `path_extension` /
`path_file_name` / `path_parent`) and **std/math** (`range` / `range_step` /
`pack_rgb` / `i32_max`) round out the batch. Coverage-only, no compiler change.
Guarded by `TestSelfHost{Crypto,Path,Math}IR{X86_64,Wasm}` in
`self_host_stdlib_concat_ir_test.go` (a shared concat-driver harness).

### 2026-06-15 — fully-matched `Option[Result[T, E]]` on the self-host IR path (bug fix)

A fully-matched `Option[Result[T, E]]` (outer `Some` bound, then the inner Result
matched and its payload read) bailed the module to AST. Root cause in
`some_opt_type`: for `var o: Option[Result[..]] = Some(Ok(x))` it inferred o's type
from the construction, and `elem_type_tag(Ok(x))` **defaults** an Ok/Err payload to
`"i32"` (it doesn't recognise Ok/Err as Result constructions). So o was mis-recorded
as `Option[i32]`, and that wrong inference preempted the authoritative annotation
(the annotation fallback only fires when the inferred type is empty). The inner
`match (r)` then found no Result type on the Some-bound slot and bailed. Fix:
`some_opt_type` returns `""` when the Some payload is itself an Ok/Err (Result)
construction — a bare `Ok(x)` can't name `E` anyway — so the binding's annotation
(`Option[Result[T, E]]`) wins. `Option[Option[T]]` was already fine (a Some payload
types cleanly), and unannotated `Some(<scalar>)` inference is unchanged. Found via
temporary `eprint` instrumentation of the self-host compiler (irlower is itself a
Fern program). Verified on x86-64, wasm, and arm64 (qemu) against the interpreter —
`Some(Ok)`, `Some(Err)`, plus `Option[Option]` and unannotated-Some regressions.
Fixpoint still converges byte-identically. Guarded by
`TestSelfHostNestedOptResultIR{X86_64,Wasm}`.

### 2026-06-15 — Result with a tuple payload on the self-host IR path (bug fix)

A `Result[T, E]` whose `T` or `E` is itself a tuple — e.g. `Result[(i32, i32),
string]`, matched with `t.0`/`t.1` reads — bailed the module to AST. Root cause in
`opt_payload_type`: its Result `T`-vs-`E` comma split counted only `[`/`]`, not
`(`/`)`, so a tuple payload's inner comma was mistaken for the `T`-`E` separator
(`T` parsed as `(i32` instead of `(i32, i32)`), failing payload-type recovery so
the match arm bailed. (Option payloads were unaffected — `opt_payload_type`
returns the whole inner type for Option without splitting; the match-side tuple
binding via `tuple_elems_lowerable` already worked.) One-line fix: the Result
split now counts `(`/`)` as well as `[`/`]`. Verified on x86-64, wasm, and arm64
(qemu) against the interpreter — Ok-tuple payload, Err-string of the same type,
and the mirror `Result[i32, (i32, i32)]` (Err tuple); scalar-Result and
Option-tuple regressions hold. Fixpoint still converges byte-identically. Guarded
by `TestSelfHostResultTuplePayloadIR{X86_64,Wasm}`.

### 2026-06-15 — nested-tuple / Option / Result struct fields on the self-host IR path (widen IR subset)

A struct field whose type is a nested tuple (`(i32, (i32, i32))`), or a tuple
carrying an Option/Result element, bailed the whole struct (and module) to AST.
Flat-tuple struct fields already lowered; the leak-safety gate
`is_leaksafe_tuple_field` accepted only bare scalar/string elements, so any
nested-tuple / Option / Result element made the field — and thus the struct —
non-leaf-safe. Fix (one function in `irlower.fern`): the gate now also accepts an
Option/Result element (`is_leaksafe_opt_field`) and **recurses** on a nested-tuple
element (an inner array still bails, matching the construction guard). The field
access `p.t.N.M` already typed correctly via the depth-aware `expr_tuple_elem_tag`,
so no other change was needed. Verified on x86-64, wasm, and arm64 (qemu) against
the interpreter — deep nested-tuple field read, sum across the nesting, and tuple
fields carrying an Option and a Result; flat-tuple-field regression holds. Fixpoint
still converges byte-identically. Guarded by `TestSelfHostStructTupleFieldIR{X86_64,Wasm}`.

### 2026-06-15 — `Result[T, E]` tuple elements on the self-host IR path (widen IR subset)

Closes the comma-containing-tuple-element story (after nested tuples): a
`Result[T, E]` element of a tuple — `(1, Ok(5))`, accessed via `match (t.1)` —
now lowers on the IR path. `Result[T, E]` has an internal comma but is bracketed,
so the now-depth-aware tag decoders keep it whole; the only remaining blockers
were the explicit "Option-only, Result deferred" exclusions (#3018, left when the
split was comma-naive). Fix, all in `irlower.fern`: (1) the tuple-construction
guard admits a Result element (`expr_is_result` — a bare `Ok(x)`/`Err(x)` or a
Result-typed local — a leak-only one-pointer box like Option); (2)
`tuple_elems_lowerable` accepts a `Result[` element tag; (3) the var-decl tuple
marking prefers the binding's tuple TYPE annotation (`tuple_type_elem_tag`) — the
full `Result[T, E]` is only knowable from the annotation since a bare `Ok(x)`
cannot name E, and the checker rejects an un-annotated Result, so the annotation
is always present where it matters. `opt_payload_type` already recovers both arms
(depth-aware split + `trim_spaces`), so no match-side change was needed. Verified
on x86-64, wasm, and arm64 (qemu) against the interpreter — Ok/Err payloads,
Result as first or second element, and a function returning a Result-bearing
tuple; Option-in-tuple and flat-result regressions hold. Fixpoint still converges
byte-identically. Guarded by `TestSelfHostResultTupleIR{X86_64,Wasm}`.

### 2026-06-15 — nested tuples in return / param position on the self-host IR path (widen IR subset)

Follow-on to nested-tuple construction/access: a function whose **return type** or
a **parameter type** is a nested tuple (`(i32, (i32, i32))`) still bailed the
module to AST. The gate `tuple_elems_lowerable` (which decides whether a
tuple-returning function can lower) split the return type's element tags with a
*naive* comma scan and accepted only scalar/string/leaf-struct/Option elements —
a nested-tuple element tag `(…)` was both mis-split and unlisted. Fix (one
function in `irlower.fern`): the element split is now depth-aware, and a
nested-tuple element is admitted by **recursing** `tuple_elems_lowerable` on it
(so an inner array / `Result` still bails, matching the construction guard). The
construction, call-result binding, and `t.N.M` access already lowered (prior
change) and the return-type elem-tag encoder (`tuple_elem_tags`) was already
depth-aware, so no other change was needed. Verified on x86-64, wasm, and arm64
(qemu) against the interpreter — right/left nesting in return position, a string
sibling, and a nested-tuple parameter; flat-tuple return regression holds.
Fixpoint still converges byte-identically. Guarded by
`TestSelfHostNestedTupleRetIR{X86_64,Wasm}`.

### 2026-06-15 — nested tuples on the self-host IR path (widen IR subset)

A tuple element that is itself a tuple — `(1, (2, 3))`, accessed `t.1.1` —
bailed the whole module to AST. The tuple-element tag encoding
(`local_tuple_elems`, comma-joined) was decoded by *naive* comma splitters, so
any element whose own tag contained a comma (a nested tuple, also `Result[T,E]`)
was rejected at construction (`return s.fail()`), and the legacy AST emitter even
mis-compiled some of them (`(1, ("ab", 9))` gave the wrong answer). Fix, all in
`irlower.fern`: (1) the two slot/kind decoders `tuple_elem_tag` and `csv_nth` are
now **depth-aware** (count `(`/`[` … `)`/`]`, so inner commas don't split the
outer tag — flat tuples never exceed depth 0, unchanged); (2) `elem_type_tag`
gained an `ExprTuple` arm that returns the element's own `(t0,t1,…)` spelling
recursively; (3) a tuple element is admitted at construction (it is a leak-only
heap-tuple pointer — one slot, like a struct/string/array/Option element, needing
no new op); (4) `expr_tuple_elem_tag` recovers `t.N.M` by reading element N's
tuple tag then element M out of it; (5) a new `expr_is_tuple` classifier. Verified
on x86-64, wasm, and arm64 (qemu) against the interpreter — right/left/triple
nesting, a string inside a nested tuple, and an i64 sibling after a nested element
(the depth-aware-kind-decode case). Fixpoint still converges byte-identically.
Guarded by `TestSelfHostNestedTupleIR{X86_64,Wasm}`.

### 2026-06-15 — qualified Option/Result construction on the self-host IR path (widen IR subset)

A routing-gap sweep (pathprobe) found that the **qualified** built-in
Option/Result construction spellings bailed the whole module to AST:
`Option.Some(x)`, `Option.None`, `Result.Ok(x)`, `Result.Err(x)` routed `"ast"`
while the bare forms (`Some(x)` / `Ok(x)` / `None`) already lowered through IR.
The qualified `Enum.Variant` path only consulted `variant_enum_owner` for *user*
enums (`s.structs`); the built-in Option/Result aren't in `s.structs`, so the
qualified form fell through and the AST emitter mis-lowered it as
`# unresolved ident: Option`. Fix: a shared `lower_opt_make_payload(tag, arg)`
helper (extracted from the bare path, widening i64/f64 payloads) is now also
called from the qualified `ExprCall` path for `Option.Some`/`Result.Ok` (tag 0)
and `Result.Err` (tag 1); `Option.None` (a field-access, no call) lowers to
`op_opt_none`. Both produce the identical box as the bare forms. Verified on
x86-64, wasm, and arm64 (qemu) against the interpreter, including composition
with the `?` operator; fixpoint still converges byte-identically. Guarded by
`TestSelfHostQualifiedOptResultIR{X86_64,Wasm}`.

### 2026-06-14 — u8/i8/u16/i16 arithmetic not width-wrapped on the self-host IR path (bug fix)

Follow-on from the u32 div/rem sweep: sub-32-bit integer arithmetic (`+` `-` `*`
`<<`) was **not masked back to its width** on the self-host IR path. An
overflowing result kept its full value on every IR backend (`255u8 + 1` → 256)
instead of wrapping (→ 0). The interpreter and the native Go backends both wrap
per the declared width (`signExtend` by `IntWidth`), so this was a silent
miscompile — the program routed through `"ir"` yet computed the wrong answer (the
struct comment even encoded the wrong assumption: "u8/u16 … need no
post-arithmetic wrap"). Fix: a new `local_subword` slot array (the sub-32-bit
sibling of `local_is_u32`) records each slot's kind (`u8`/`i8`/`u16`/`i16`, set
from the declared type at var-decl, param, and receiver binding; an array slot
holds its element kind). `expr_subword_kind` classifies an expression, and the
binary lowering emits an `int_cast` after `+`/`-`/`*`/`<<` whose result is
sub-word — masking, and sign-extending `i8`/`i16`, exactly as `as u8`/`as i8`
already do. u32/u64 are disjoint, so no double-wrap. Verified on x86-64, wasm,
and arm64 (qemu) against the interpreter; fixpoint still converges byte-identically.
Guarded by `TestSelfHostSubwordWrapIR{X86_64,Wasm}` (u8/i8/u16/i16 add/sub/mul/shl
overflow + a no-overflow exact case).

### 2026-06-14 — u32 `/` and `%` lowered signed on the self-host IR path (bug fix)

A differential sweep (x86 / wasm / arm64 / interp) turned up a real correctness
bug: u32 division and remainder were lowered as **signed** `div_s` / `rem_s` on
the IR path. A u32 numerator >= 2^31 reads as signed-negative in a 32-bit slot,
so x86-64 and wasm computed the wrong quotient (e.g. `3000000000u32 / 3` ≠ 1e9);
arm64 happened to be right only because its 64-bit register held the value
zero-extended. The interpreter computes unsigned. Fix: `irlower.lower_expr`
remaps `div_s`/`rem_s` → `div_u`/`rem_u` when either operand is u32 (mirroring
the existing u32 ordering-compare and u64 div remaps), and `wasm_ir.wasm_binop`
gained the 32-bit `i32.div_u` / `i32.rem_u` selections it was missing. Guarded
by `TestSelfHostU32DivRemIR{X86_64,Wasm}` (high-bit-set div/rem, 4e9 div, and
low-value div/rem), oracle-checked against the interpreter. Fixpoint intact.

### 2026-06-14 — std/base64 via the self-host IR path (coverage)

Same vehicle as the std/hex coverage: `TestSelfHostBase64IR` compiles the real
`internal/stdlib/std/base64.fern` source concatenated with a main (single-module,
no imports), routing-pinned to `"ir"` and oracle-checked on x86-64 + wasm (verified
on arm64 via qemu): `base64_encode` (padded + exact-multiple inputs, output
digit), `base64_decode`, and encode→decode round-trips. Coverage-only, no compiler
change. std/base64 S column flipped to ✅.

### 2026-06-14 — std/hex via the self-host IR path (coverage)

Confirmed `std/hex` (`hex_encode` / `hex_decode`) lowers through the self-host IR
path end-to-end, now that the wasm `string_from_bytes` / `arr_push` helper-gate
fixes unblocked the byte→string primitives it builds on. `TestSelfHostHexIR`
compiles the REAL `internal/stdlib/std/hex.fern` source concatenated with a main
(the single-module trick the std/json self-host test uses — std/hex has no
imports), routing-pinned to `"ir"` and oracle-checked on x86-64 + wasm (verified
on arm64 via qemu too): encode length / digit, decode length / char, and
encode→decode round-trips. Coverage-only, no compiler change. std/hex S column
flipped to ✅.
### 2026-06-14 — fix: `.append` (arr_push) missing helper on the wasm IR path

Same class of bug as the `string_from_bytes` fix: the wasm IR backend lowered
`op_arr_push` to `call $__fern_arr_push`, but `wasm_ir_run` had no gate to emit
that helper — so any IR-path program using `arr.append(v)` produced a wasm module
with a dangling call that failed to link. x86-64 / arm64 already emitted it. Fix:
gate the standalone `wasm.arr_push_helper()` (push-only, so it doesn't
double-define the separately gated `arr_slice` / `arr_slice8` helpers that the
full AST `arr_helpers` bundle also contains) on `module_emits_op(mod,
"arr_push")`. Depends on `$__fern_arr_box`, which `module_allocates` pulls in.

New routing-pinned `TestSelfHostArrPushIR` (x86-64 + wasm, oracle-checked: append
+ length, append + index, a 10-iteration loop that exercises geometric growth /
realloc, and a loop-built array summed). Verified end-to-end on x86-64, wasm, and
arm64; x86-64 + stage2 fixpoint hold. (Found by cross-checking every `call
$__fern_*` the wasm IR backend emits against the helper gates in `wasm_ir_run` —
`arr_push` and `string_from_bytes` were the two missing.)
### 2026-06-14 — fix: `string_from_bytes` missing helper on the wasm IR path

The wasm IR backend lowered `op_str_from_bytes` to `call
$__fern_string_from_bytes`, but `wasm_ir_run` had no gate to emit that helper
(its sibling `str_bytes` did) — so any IR-path program packing a `u8[]` into a
string (`string_from_bytes(buf)`) produced a wasm module with a dangling call that
failed to link (`unknown func $__fern_string_from_bytes`). x86-64 / arm64 already
emitted the helper. Fix: export `wasm.string_from_bytes_helper` and gate it on
`module_emits_op(mod, "str_from_bytes")` in `wasm_ir_run`, mirroring the
`str_bytes` gate. (It depends on `$__fern_alloc` / `$__fern_str_box`, which
`module_allocates` already pulls in.)

New routing-pinned `TestSelfHostStringFromBytesIR` (x86-64 + wasm, oracle-checked:
direct pack + length / byte round-trip, plus a `hex_encode` built on
`__alloc_u8` + `.with` + `string_from_bytes`). Verified end-to-end on x86-64,
wasm, and arm64; x86-64 + stage2 fixpoint hold. (Found while probing pure-Fern
stdlib modules — `std/hex` etc. — for self-host IR coverage.)
### 2026-06-14 — `dyn Trait[]` LOCAL element dispatch via the self-host IR path

A `dyn Trait[]` PARAM recorded the coarse `"dyn Trait"` element type on its slot,
so `for x in param` / `param[i].m()` dispatched dynamically — but the local-`var`
path (`var xs: dyn Trait[] = [...]`) marked the slot `is_arr` with NO element type,
so every method call on a dyn-array LOCAL element (`xs[i].m()`, `var e = xs[i];
e.m()`, `for x in xs { x.m() }`) bailed to the AST path. The local-decl now records
the same coarse `"dyn Trait"` element type (strip the `[]`), mirroring the param
path, so element receivers recover as `dyn Trait` and dispatch through
`op_dyn_dispatch`.

New routing-pinned `TestSelfHostDynArrayLocalIR` (x86-64 + wasm, oracle-checked:
inline index, bound element, loop, two indices, and a three-element heterogeneous
loop). Verified end-to-end on x86-64, wasm, and arm64 (qemu); x86-64 + stage2
fixpoint hold. (Found while probing `dyn Trait` after the #3163 checker-panic fix
unblocked dyn oracle testing.)
### 2026-06-14 — no-capture lambda calls in slice bounds / field-access objects via the IR path

Completes `lift_expr_walk`'s compound-form recursion (after #3148's binary / unary
/ index): a no-capture lambda CALL nested in a SLICE bound (`a[(iife)(0) : 3]`) or
under a FIELD-ACCESS object (`arr[(iife)(0)].v`) is now hoisted, so the module
stays on the IR path. New `ExprSlice` (array/start/end) and `ExprFieldAccess`
(obj) arms recurse via `lift_expr_walk`; an operand with nothing to lift rebuilds
identically, so existing programs are unchanged.

New routing-pinned `TestSelfHostLambdaLiftSliceFieldIR` (x86-64 + wasm,
oracle-checked: IIFE in a slice start bound, a slice end bound, and a
field-access index). Verified end-to-end on x86-64, wasm, and arm64 (qemu);
x86-64 + stage2 fixpoint hold. With this, `lift_expr_walk` descends into every
compound expression form, so a no-capture lambda call anywhere in an expression
lifts. (A CAPTURING lambda still bails everywhere, pending the closure ABI.)

### 2026-06-14 — no-capture lambda calls nested in compound expressions via the IR path

Follow-up to the IIFE/tuple/assignment lift slice (#3125): `lift_expr_walk` now
recurses through the compound expression forms — `ExprBinary`, `ExprUnary`,
`ExprIndex` — so a no-capture lambda CALL nested inside them is still hoisted and
the module stays on the IR path. Previously a lambda call inside `(...) + 1`,
`0 - (...)`, or `a[...]` survived unlifted and bailed to AST. Each operand recurses
via `lift_expr_walk` (reaching a nested IIFE callee / lambda call argument); an
operand with nothing to lift is rebuilt identically (counter untouched), so
existing programs are unchanged.

New routing-pinned `TestSelfHostLambdaLiftNestedIR` (x86-64 + wasm,
oracle-checked: IIFE in either binary operand, under unary minus, as an array
index, a lambda call argument inside a binary, and both operands IIFE calls).
Verified end-to-end on x86-64, wasm, and arm64 (qemu); x86-64 + stage2 fixpoint
hold. (`ExprSlice` / `ExprFieldAccess`-obj recursion is not added here — no probed
gap — and a CAPTURING lambda nested anywhere still bails, pending the closure
ABI.)

### 2026-06-14 — no-capture lambdas in IIFE / tuple / assignment positions via the IR path

Widened the lambda-lift pre-pass to hoist no-capture lambdas in three more
positions, so they lower through the self-host IR path:

- **IIFE callee** — `(function(b){...})(args)` hoists the callee lambda to a
  top-level `__lam_N`, so the call becomes a direct `__lam_N(args)`
  (lift_call_arg now also lifts the callee in `lift_expr_walk`'s ExprCall arm).
- **tuple element** — `(function(x){...}, 10)` hoists to a fn-pointer tuple
  element, so `t.0(t.1)` rides the tuple-element `call_indirect` path (new
  `ExprTuple` arm in `lift_expr_walk`).
- **assignment RHS** — `f = function(x){...}` hoists to `f = __lam_N`, a
  fn-pointer store (new `StmtAssign` arm in `lift_stmt`).

These join the already-lifted call-argument / array-element / struct-field /
return positions. A CAPTURING lambda in any of these is left in place (still
bails to AST — calling it needs the env-passing closure form, an ABI change).
Also still on AST: a lambda nested inside a binary expression (`(iife) + 1`),
since `lift_expr_walk` doesn't recurse into `ExprBinary` — a separate slice.

New routing-pinned `TestSelfHostLambdaLiftPositionIR` (x86-64 + wasm,
oracle-checked: IIFE, 2-arg IIFE, tuple-fn, reassign, plus a call-argument
regression guard). Verified end-to-end on x86-64, wasm, and arm64 (qemu); x86-64
+ stage2 fixpoint hold.

### 2026-06-14 — calling the result of a call (`mk()(args)`) via the self-host IR path

Follow-up to the no-capture-lambda-return slice (#3088): calling the RESULT of a
call — `mk()(args)`, where `mk` returns a function value — now lowers through the
IR path. Binding first (`var g = mk(); g(args)`) and calling a fn-pointer array
element (`fs[i](args)`) already lowered; only the inline call-on-call-result form
bailed, because the `ExprCall`-callee dispatch had no arm for an `ExprCall`
callee. The fix lowers the args, then the callee call (its returned fn pointer on
TOS), then `call_indirect` — the same shape as the array-element / tuple-element
fn-value calls. A callee returning a CAPTURING lambda (a closure-box-returning
fn, tracked in `closure_fns`) needs the env-passing form and still bails to AST.

New routing-pinned `TestSelfHostCallOnCallIR` (x86-64 + wasm, oracle-checked:
inline call, result-in-arithmetic, 2-arg, called-twice, plus bind-then-call and
fn-pointer-array-element regression guards). Verified end-to-end on x86-64, wasm,
and arm64 (qemu); x86-64 + stage2 fixpoint hold. (Implementation note: the new
match-arm binding had to be uniquely named — `lower_expr` is function-scoped and
already binds a `cc: LowerState`, so a `parser.ExprCall(cc)` arm shadowed it.)
### 2026-06-14 — functions returning a no-capture lambda via the self-host IR path

Widened the IR subset: a function returning a NO-CAPTURE lambda
(`function mk(): (i32) => i32 { return function(b) { ... }; }`) now lowers through
the IR path. Returning a *capturing* lambda and returning a *named* function value
already lowered; only the no-capture-lambda return position bailed, because
`lift_lambdas` hoisted no-capture lambdas in call-argument / array-element /
struct-field positions but not in RETURN position. The fix lifts the return value
via `lift_call_arg`, so `return function(b){...}` becomes `return __lam_N` — the
already-working named-function-return path. (The fixpoint driver `asm_run` doesn't
apply `lift_lambdas`, and no compiler function returns a bare lambda, so the
self-bootstrap is unaffected; x86-64 + stage2 fixpoint hold.)

New routing-pinned `TestSelfHostFnRetIR` (x86-64 + wasm, oracle-checked: no-capture
return bound + called, multiply-bodied / 2-arg / called-twice, plus capturing- and
named-return regression guards). Verified end-to-end on x86-64, wasm, and arm64
(qemu). Still on the AST path: immediately calling a returned function value
(`mk()(4)` — a call on a call result), a separate dispatch gap left for later.

### 2026-06-14 — scalar f64 value-receiver methods via the self-host IR path

Resolved the follow-up flagged in the f32-scalar fix: a VALUE-receiver method on a
scalar f64 (`function (x: f64) m(): f64`, called `a.m()`) routed through the IR
path but miscompiled (`(3.5).dbl()` → 0). Root cause: `expr_is_f64` classified a
method call `recv.m()` as f64 only when the receiver was a STRUCT
(`expr_struct_type`), so a scalar f64 receiver fell through as "not f64" and a
following `a.m() as i32` masked the double's low 32 bits (→ 0) instead of
truncating via `f64_to_i32`. The fix adds an `expr_recv_prim_type` fallback so an
f64-returning method `<f64>.m` on a scalar receiver is recognised as an f64 value.
New routing-pinned `TestSelfHostF64RecvIR` (x86-64 + wasm, oracle-checked: arith /
identity / div / chained / intrinsic-bodied); verified end-to-end on x86-64, wasm,
and arm64 (qemu); x86-64 + stage2 fixpoint hold.

(i64 value-receiver methods are unaffected by this class of bug — i32 truncation of
an i64 result equals the low-32-bit mask the integer path already applied. Their
one remaining manifestation is a wasm **legacy-AST** gap: `wasm_eligible` rejects
the module so it falls to `wasm.fern`, which lowers the i64 receiver param as i32
and traps. Per the legacy-AST policy that gap is not fixed here.)

### 2026-06-14 — f32 scalar params / returns / locals via the self-host IR path

Fixed a latent self-host IR miscompile: Fern represents f32 as an f64 internally
(f32<->f64 casts are no-ops; every float op runs at double width), but
`lower_func` and `f64_ret_fns_of` only matched the literal type name `"f64"`, so
an `f32` param / return / local slipped through as a plain **i32** slot — its
8-byte float bit pattern was then passed/returned/cast through the 4-byte integer
path and miscompiled (`id32(5.5 as f32)` returned 0, not 5). A new
`is_f64_scalar_type_name` (`"f64"`/`"f32"`/`"float"`) drives the 8-byte-float slot
marking at the param/receiver/local-binding sites and the f64-returning-fn
registration; wasm's `(result f64)` / `(param f64)` signature emission keys off it
too. The std/float f32 methods (`abs`/`sqrt`/`floor`/`round` as
`__*_f64(x as f64) as f32`), written as free functions, now lower correctly.
New routing-pinned `TestSelfHostF32IR` (x86-64 + wasm, oracle-checked); verified
end-to-end on x86-64, wasm, and arm64 (qemu); x86-64 + stage2 fixpoint hold.
Only f32/"float" *scalar* signatures are covered — f32 arrays / struct fields /
tuple elements are unchanged (still classified via their own paths).

**Discovered, deferred (pre-existing, separate from the above):** a VALUE-receiver
method on a scalar f64 (e.g. `(x: f64) dbl(): f64`) routes through the IR path but
miscompiles (`(3.5).dbl()` → 0). The std/float intrinsic tests call the `__*_f64`
builtins directly, so this dispatch path is uncovered. f32 value-receiver methods
route AST (safe). Tracked as a follow-up — not addressed here to keep this change
to the f32-scalar-signature fix.

### 2026-06-14 — `__round_f64` via the self-host IR path (all 3 backends)

Follow-up to the f64-math-intrinsics work: `__round_f64` (round-half-away-from-
zero, Go's `math.Round`) now lowers through the IR path too, via the same
`op_funary` op (kind `fround`). arm64 gets it in one instruction (`frinta` =
round-to-nearest ties-away); x86 and wasm emulate `trunc(x + copysign(0.5, x))`
because `roundsd` has no ties-away mode and wasm's `f64.nearest` is ties-to-EVEN
(the wasm path reuses the existing `f64tmp` scratch local to duplicate `x`).
`TestSelfHostFloatMathIR` gains half-integer cases (2.5 → 3, 99.5 → 100) that are
exactly where ties-to-even would diverge, plus below/above-half and an f64-local
case; all oracle-checked and routing-pinned. Verified end-to-end on x86-64, wasm
(wasmtime), and arm64 (qemu); x86-64 fixpoint holds. Only the libm
transcendentals (`log`/`exp`/`sin`/`cos`/`pow`) remain on the AST path now.

### 2026-06-14 — std/float f64 math intrinsics via the self-host IR path (all 3 backends)

Closed the self-host gap for std/float's single-instruction f64 math builtins.
A new IR op (`op_funary`) carries `__sqrt_f64` / `__floor_f64` / `__ceil_f64` /
`__trunc_f64` / `__abs_f64`; `irlower` recognises the calls (1 f64 arg → the
intrinsic, and `expr_is_f64` now classifies them as f64-returning so they nest +
feed f64 arithmetic), and each IR backend selects the single hardware
instruction it already uses on the AST path: x86 `sqrtsd` / `roundsd $1/$2/$3` /
sign-bit `andq` (`asm_ir.fern`), arm64 `fsqrt` / `frintm`/`frintp`/`frintz` /
`fabs` (`asm_arm64_ir.fern`), wasm `f64.sqrt`/`floor`/`ceil`/`trunc`/`abs`
(`wasm_ir.fern`). Eligibility flips for free (`ir_eligible` == "lowers"); the new
ops aren't `call_direct` so `calls_only_known` is unaffected.

New routing-pinned `TestSelfHostFloatMathIR` (x86-64 + wasm, 9 cases each,
oracle-checked) replaces the older `TestSelfHostFloatIntrinsicsIR`, which used
`-ir` and so **silently fell back to AST** when the program wasn't all_eligible
— including `__round_f64` (which bails) routed the whole module through the AST
emitter, so it never actually verified the IR path. Verified end-to-end on
x86-64, wasm (wasmtime), and arm64 (qemu) locally; the x86-64 + arm64 fixpoint
holds. `__round_f64` (round-half-away needs a `trunc(x+copysign(0.5,x))`
emulation) and the libm transcendentals (`log`/`exp`/`sin`/`cos`/`pow`) stay on
the AST path — a documented follow-up.

### 2026-06-14 — std/u64 `min`/`max`/`clamp` methods via the self-host IR path (x86-64 + wasm)

Confirmed the std/u64 *method* surface on the self-hosted compiler (its S column
was blank). `TestSelfHostU64IR` runs `min` / `max` / `clamp` (inlined verbatim
from std/u64) through the x86-64 and wasm IR drivers, **oracle-checked against the
interpreter** and pinned to the `"ir"` path. Complements the existing #2904
`TestSelfHostU64UnsignedIR` (raw unsigned `>` `<` `>>` `/` `%` operators) by
covering the methods those don't, with the new wrinkle being **unsigned `max` /
`clamp` against a high-bit-set bound** (>= 2^63) — where the helper's internal
comparison must be unsigned or it picks the wrong branch. Coverage-only, no
compiler change. std/u64's `to_string` stays on the AST path (it wraps core/int's
`__int_to_string_u64`, whose `u8[]` / `usize` / `__memcpy` internals are a
separate low-level concern). std/u64 S column flipped to ✅.

### 2026-06-14 — std/time Zoned / TimeZone surface via the self-host IR path (x86-64 + wasm) — std/time row now fully ✅

Closed the last std/time "self-host pending" piece. `TestSelfHostTimeZonedIR`
runs fixed-offset zone construction, `in_zone`, `to_datetime` (wall-clock
split), and an IANA-style `Option[TimeZone]` lookup through the x86-64 and wasm
IR drivers, **oracle-checked against the interpreter** and pinned to the `"ir"`
path. New ground: **nested structs** — a struct field that is itself a struct
(`Zoned { instant: Instant, zone: TimeZone }`, `DateTime { date: Date, time:
Time }`), built and read through two levels (`z.instant.sec`, `dt.time.hour`,
`dt.date.day`), including a positive/negative offset wall-clock shift and
`timezone_iana` composed into `in_zone` → `to_datetime`. Verified empirically to
route `"ir"` with the emitted code matching the interpreter on both backends —
coverage-only, no compiler change. Structs renamed
`Civil`/`Moment`/`Clock`/`Zd`/`Tz`/`DT` (the built-ins are reserved, E010). With
this the **std/time row is fully ✅ on self-host** (all of pure-i32 helpers,
Date methods, `Option` parse, RFC-3339 i64, Span/Duration, and Zoned/TimeZone).

### 2026-06-13 — std/time calendar/absolute arithmetic (`add_span` / `add_duration` / `duration_since` / `days_until`) via the self-host IR path (x86-64 + wasm)

Brought the std/time self-host coverage up to its near-complete surface.
`TestSelfHostTimeSpanIR` runs `add_span` (Date + Span with month-end clamp),
`add_duration` / `duration_since` (Instant ± Duration, i64 + nsec carry/borrow),
and `days_until` (returns a Span) through the x86-64 and wasm IR drivers,
**oracle-checked against the interpreter** and pinned to the `"ir"` path. New
ground: an **8-field struct passed by value as a parameter** (`add_span(s:
Span)`) and a Duration struct pairing an i64 `sec` with an i32 `nsec`
carry/borrow. Verified empirically to route `"ir"` with the emitted code
matching the interpreter — coverage-only, no compiler change. Structs renamed
`Civil` / `Sp` / `Dur` / `Moment` (`Date` / `Span` / `Duration` / `Instant` are
reserved built-ins, E010). Only the Zoned / TimeZone-IANA operations remain
self-host-unconfirmed.

### 2026-06-13 — std/time RFC-3339 (`format_rfc3339` / `instant_parse_rfc3339`) via the self-host IR path (x86-64 + wasm)

Extended the std/time self-host coverage to the RFC-3339 surface, which adds an
**i64 struct field** (`Instant.sec`). `TestSelfHostTimeRfc3339IR` runs
`format_rfc3339` (Instant → string) and `instant_parse_rfc3339` (string →
`Option[Instant]`) through the x86-64 and wasm IR drivers, **oracle-checked
against the interpreter** and pinned to the `"ir"` path. New ground vs the
earlier std/time coverage: i64 arithmetic (mul/div/mod + `as i64` / `as i32`
casts) over a struct field, an **i64-carrying struct constructor**, and
`Some(Instant{ sec: <i64>, nsec })`. Cases cover parse (hour/seconds/fraction,
too-short, bad-separator), format (whole-second len 20, fractional len 30), and
a parse→format round-trip. Verified empirically to route `"ir"` with the
emitted code matching the interpreter — coverage-only, no compiler change. The
structs are named `Civil` / `Moment` (`Date` / `Instant` are reserved, E010)
and `int.int_to_string` is `.to_string()` (`import "std/i32"` for the oracle).
Remaining std/time surface (Zoned / Span / Duration calendar arithmetic) is the
next self-host target.

### 2026-06-13 — std/time `date_parse_iso` (`Option`-returning) via the self-host IR path (x86-64 + wasm)

Extended the std/time self-host coverage to the `Option`-returning parser.
`TestSelfHostTimeParseIR` runs `date_parse_iso` (verbatim, struct renamed
`Civil`) through the x86-64 and wasm IR drivers, **oracle-checked against the
interpreter** and pinned to the `"ir"` path. A function returning
`Option[Civil]` constructs `Some(Civil{...})` / `None`, and `main`
discriminates the result with a **payload-binding `match`** that reads the
struct's fields — `Option` construction + payload-binding match + struct field
access, all already lower (no compiler change; built on the user-enum
payload-binding match lowering from #2957). Valid, wrong-length,
wrong-separator, and non-digit inputs are all covered. Remaining std/time
self-host gap: the Instant/Zoned RFC-3339 methods (i64 `sec`/`nsec` struct
fields).

### 2026-06-13 — std/time Date civil-date methods via the self-host IR path (x86-64 + wasm)

Widened the std/time self-host coverage from the pure-i32 helpers to the
**Date struct methods**. `TestSelfHostTimeDateIR` runs Howard Hinnant's
`days_from_civil` / `civil_from_days` plus the Date receiver methods
(`is_valid` / `add_days` / `days_since` / `weekday` / `day_of_year` /
`format_iso`) through the x86-64 and wasm IR drivers, **oracle-checked against
the interpreter** and pinned to the `"ir"` path. This is the first self-host IR
coverage to exercise, over a 3-i32 struct: struct construction + field access,
a **struct-returning** function (`civil_from_days`), receiver methods on a
struct, and `.to_string()` + string concat (`format_iso`) — all already lower,
so no compiler change. The struct is named `Civil` (the built-in `Date` name is
reserved, E010) and `int.int_to_string` is written `.to_string()`;
`import "std/i32"` lets the interpreter oracle resolve it while the self-host
driver treats it as a builtin and keeps the IR path. Instant/Zoned RFC-3339
`Option`-returning methods remain pending.

### 2026-06-13 — std/csv `csv_escape` / `csv_join` via the self-host IR path (x86-64 + wasm)

Closed the last `std/csv` "self-host pending" piece (after `csv_parse_line`).
`TestSelfHostCsvEscapeIR` runs the inlined RFC-4180 field escaper + joiner
(`s.index_of(",")`/`"\""`/`"\n"`/`"\r"` guards, `s.replace("\"", "\"\"")`, and a
`string[]` join loop with concat) through the x86-64 and wasm IR drivers,
**oracle-checked against the interpreter**, pinned to the `"ir"` path. The string
`.index_of()` / `.replace()` methods lower as builtins (`op_str_index_of` /
`op_str_replace`), so the program is fully IR-eligible. `import "std/string"` is
included so the native interpreter can resolve those receiver methods; the
self-host single-program driver ignores the import and treats them as builtins,
still taking the IR path. No compiler change; the `std/csv` row is now fully ✅
on self-host.

### 2026-06-13 — std/format `format_duration_ms` via the self-host IR path (x86-64 + wasm)

Closed the last `std/format` "self-host pending" piece (after `format_bytes` +
`format`). `TestSelfHostFormatDurationIR` runs the inlined duration formatter
(h/m/s/ms if-ladder over integer div/sub/mul + `i32.to_string()` + concat +
`.len()`, with `ms.abs()` inlined as a free helper) through the x86-64 and wasm
IR drivers, **oracle-checked against the interpreter**, pinned to the `"ir"`
path. `import "std/i32"` is included so the native interpreter can resolve
`.to_string()` (a self-host builtin; a std/i32 method natively) — the self-host
driver treats it as a builtin and still takes the IR path. No compiler change;
the `std/format` row is now fully ✅ on self-host.

### 2026-06-13 — std/format `format(fmt, args)` via the self-host IR path (x86-64 + wasm)

Closed the remaining `format` "self-host pending" gap (after `format_bytes`):
`TestSelfHostFormatStringIR` runs the inlined `{}`-placeholder substitution
through the self-hosted x86-64 and wasm IR drivers, **oracle-checked against the
interpreter** (return value = rendered length, kept ≤126), pinned to the `"ir"`
path. Exercises string `.len()` / byte index / single-char slice / concat and
`string[]` index+`.len()` in a while loop — all already lower on the IR path, so
no compiler change. (`format_duration_ms` remains pending.)

### 2026-06-13 — 🔧 self-host wasm IR: large i64/u64 literal emitted as i32.const ([#2928](https://github.com/JakeChampion/lang/issues/2928))

`n as i64` / `n as u64` widens its operand by lowering it through the 32-bit
`lower_expr` and appending `int_extend` — but a numeric LITERAL operand is
already a 64-bit value, so this made it an `i32.const`. For a literal above the
i32 range that's invalid WAT (`i32.const 9000000000000000000` is rejected by
wasm); x86/arm64 happened to tolerate the truncation differently. Fix: the
`as_i64` / `as_u64` lowering now emits `const_i64_text` directly for a numeric
literal operand (skipping the i32 lowering + extend). Guarded by
`TestSelfHostLargeIntLiteralIR` (x86-64 + wasm, oracle-checked: a u64 modulo, an
i64 round-trip that a 32-bit truncation would fail, and a large-literal unsigned
compare). Stage-2 fixpoint stays byte-identical.

### 2026-06-13 — std/i64 + std/u32 numeric methods via the self-host x86-64 IR path

Added `TestSelfHostNumericMethodsIRX86_64`: self-contained programs exercising
the i64 (`abs`/`min`/`max`/`clamp`) and u32 (unsigned `min`/`max`) method logic
that std/i64 / std/u32 wrap, run through the self-hosted x86-64 IR driver and
**oracle-checked against the interpreter** (not hardcoded — cf. #2908), with the
routing pinned to the `"ir"` path. The u32 case uses a value above 2^31 so a
signed compare would give the wrong answer, confirming the IR path selects the
unsigned form. Promotes the std/i64 / std/u32 **S** column to ✅ (x86 IR).

Two gaps surfaced (both held out of this test, x86-IR-only for now):
- **wasm IR lowers u32/u64 comparisons as SIGNED** — the same unsigned cases
  return the signed-compare answer on `wasm_ir`, where the x86 path (#2904) is
  correct. A real backend bug ([#2917](https://github.com/JakeChampion/lang/issues/2917)).
- A **>2^63 u64 value built by addition** (`(9e18 as u64)+(9e18 as u64)`) routes
  to the AST emitter on x86 rather than the IR path — an IR-eligibility gap, so
  u64 unsigned coverage stays pending.

### 2026-06-13 — `@derive(Json)` array fields via the self-host IR path ([#2766](https://github.com/JakeChampion/lang/issues/2766))

Added the element-polymorphic array serialiser `pub function (xs: T[])
to_json[T: Json](): string` to `std/json`, so a `T[]` (and a `T[]` struct
field) renders as a JSON array. The native compiler already supported it via
monomorphisation (#2774); this closes the **self-host** side.

The self-host emits generic bodies by **erasure**, so the single emitted
`(xs: T[]) to_json()` body bakes in the i32 element dispatch and can't
serialise a string/struct array. Fixed by special-casing the **call site**
in `irlower` (`lower_array_to_json` / `to_json_loop_stmt`): `arr.to_json()`
on a known array receiver desugars to an inline `[e0,e1,…]` loop whose
per-element `arr[i].to_json()` lowers where the element type is in hand,
dispatching to the right impl (i32 / string / a derived struct's `to_json`).
The split into two functions keeps each small (oversized functions miscompile
on the native backend — #2720).

- **Native (all four backends):** the `derive_json` fixture gains a struct
  with `i32[]` / `string[]` fields plus a bare-array check.
- **Self-host (x86-64 IR + wasm IR):** `TestSelfHostJsonArrayIR` covers
  i32 / string / `@derive(Json)`-struct / empty arrays, pinned to the `"ir"`
  path via `asm_pathprobe_run`. Stage-2 fixpoint stays byte-identical.

Remaining: nested arrays (`i32[][]`) and map objects (the element-array case
isn't detected at `arr[i]`); separate follow-ups.

### 2026-06-12 — 🐛 self-host miscompiles std/crypto SHA-256 ([#2861](https://github.com/JakeChampion/lang/issues/2861))

`std/crypto` `sha256_hex` returns the **correct** digest on the native compiler
(all four backends, validated against the canonical `"abc"` and `""` vectors in
the new `audit_std_crypto` fixture) but a **wrong, deterministic** digest on the
self-hosted compiler:

- `sha256_hex("abc")` → `b0a24a6b…` (want `ba7816bf…`)
- `sha256_hex("")`    → `ca297d15…` (want `e3b0c442…`)

Every u32 primitive was verified correct in isolation on self-host (rotate,
shift, shift-by-u32-amount, 2- and 5-term wrapping add, 3-way XOR, the `__rotr`
helper, u8→u32 word assembly, u32[] build/read, large hex literals like
`0xb5c0fbcf`), so it's an **emergent** miscompile in the 64-round composition,
not a single op. `hmac_sha256_*` is built on the same core and is likely
affected. Self-host crypto is held out pending the fix.

Also landed `audit_std_uuid` (v4 length / dash positions / version nibble /
uniqueness) — ✅ on all four native backends.

(Context: #2828 was fixed upstream by #2854 — the wasm owned-model enum-param
over-release — so `audit_types_match`'s skip-list entry was removed.)

### 2026-06-12 — std/path + std/i64 + std/u32 + std/u64 + std/float audited (native 4-backend; no new bugs)

New native fixture `audit_std_path_numeric` — `path_join` / `path_file_name` /
`path_extension`; i64 `abs`/`min`/`max`; u32 `max`; u64 `clamp`; float
`sqrt`/`floor`/`ceil`/`abs`/`is_finite`. ✅ on interp / x86-64 / arm64 / wasm and
the wasm owned-vs-borrow gate. Self-host: `std/path` is import-free, covered by
new `self_host_audit_stdpath_test.go` (join/file_name/extension); the wider-int
and float modules are ⚠️ pending on self-host. No new bugs.

Author note: `path_join(parts: string[])` takes an array, not varargs.

### 2026-06-12 — std/json + std/time + std/format + std/csv audited (native 4-backend; no new bugs)

New native fixtures, all ✅ on interp / x86-64 / arm64 / wasm and clearing the
wasm owned-vs-borrow differential gate:
- `audit_std_json` — `json_parse` → `json_get_i32` / `json_get_string` →
  `json_encode` → re-parse (also covers the §C `JsonValue` variants). Self-host
  covered by the existing `self_host_json_test.go`.
- `audit_std_time` — `is_leap_year` / `days_in_month` / `date_make` /
  `format_iso`.
- `audit_std_textfmt` — `csv_parse_line` / `csv_join` / `csv_escape` +
  `format.format_bytes`.

No new bugs. `std/time` / `std/format` / `std/csv` self-host coverage is pending
(their bundles pull `std/string`/`std/array`); marked ⚠️ until added.

Author note: module-level free functions are called qualified
(`json.json_parse`, `csv.csv_parse_line`); `json_parse` returns `Option`
(not `Result`); the integer getter is `json_get_i32`.

### 2026-06-12 — std/string core methods audited (native 4-backend differential)

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_std_string` — `to_upper` / `to_lower` /
`trim` / `starts_with` / `ends_with` / `contains` / `index_of` / `replace` /
`repeat` / `pad_start` / `split`, with result strings compared directly. ✅ on
interp / x86-64 / arm64 / wasm.

**Self-host:** already covered by `internal/e2e/self_host_string_test.go`, which
bundles the full std/string and exercises the same core methods (index_of /
trim / upper / lower / contains / starts_with / replace / repeat / split) — so
no new self-host test was needed. The §D std/string row is promoted from 🔄 to
✅ for the core set (the full ~120-method surface remains a ⚠️ follow-up).

Author note: `pad_start(n, ch)` takes the fill string as a second argument.

### 2026-06-12 — std library: std/i32 + std/math + std/array reductions audited (no new bugs)

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_std_numeric` — std/i32 scalar methods
(abs/min/max/clamp/pow/gcd/lcm/is_prime/is_even/signum), std/math (range), and
std/array reductions (sum/max/min/product/sorted_asc). ✅ on interp / x86-64 /
arm64 / wasm — a 4-backend differential check that these heavily-used pure
functions agree everywhere.

**Self-host arm (x86-64 + CI-gated arm64):** new test
`internal/e2e/self_host_audit_stdarray_test.go` broadens the std/array bundle
coverage (sum / product / sorted_asc / max) beyond the existing single gcd_all
case; std/math is already covered by `self_host_math_test.go`. All green.

No new bugs. Author notes: native array `.max()` / `.min()` return `Option[i32]`
(empty-array-safe); `std/math.i32_max`/`i32_min` are 2-arg scalar helpers while
the array-reduction `max`/`min` are receiver methods.

### 2026-06-12 — env / clock / randomness builtins audited; native `monotonic_ns` + `sleep_ms` gap found

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_env_time_random` — `env` (unset → `None`),
`now_unix_ms` (epoch lower-bound), `random_bytes` (length), `random_i32`
(usable). ✅ on interp / x86-64 / arm64 / wasm.

**Self-host arm (x86-64):** new test
`internal/e2e/self_host_audit_platform_test.go` — the above plus `monotonic_ns`
and `sleep_ms`. All 6 pass.

**Finding — `monotonic_ns` + `sleep_ms` unimplemented on native code-gen
backends ([#2843](https://github.com/JakeChampion/lang/issues/2843)):** both
type-check and run on the **interpreter** and the **self-hosted** compiler (which
emits `__fern_monotonic_ns` / `__fern_sleep_ms`), but the native backends emit a
call to an undefined symbol:
- `monotonic_ns` — fails native x86-64 + arm64 (`undefined label`); wasm ✓.
- `sleep_ms` — fails native x86-64 + arm64 + wasm (`unknown callee`).
`now_unix_ms` is implemented on all native backends (control). The native fixture
is restricted to the universally-supported builtins; the two gapped ones are
covered on interp + self-host pending the fix.

### 2026-06-12 — I/O built-in functions audited; self-host `putchar` gap found

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_io_builtins` — exact-stdout pinning of
`write` (raw), `putchar` (byte), `print` (line), `eprint` (stderr, must not
reach stdout), and `.len()` (string + array). ✅ on interp / x86-64 / arm64 /
wasm.

**Self-host arm (x86-64):** new test
`internal/e2e/self_host_audit_io_test.go` — checks the compiled program's
stdout + exit code for `print` / `write` / `eprint` / `len` / `exit`. All pass.

**Finding — `putchar` unsupported on self-host
([#2839](https://github.com/JakeChampion/lang/issues/2839)):** the self-hosted
compiler lowers `putchar(b)` to `call __fn_putchar` but never emits that runtime,
so the program fails to link (both IR and legacy paths). Native inlines it as a
`write(1, …)` syscall. Held out of the self-host I/O table, referencing #2839.

**Notes:** native exposes `len` only as the `.len()` method (free `len(x)` is an
undefined identifier); the self-host front-end also accepts free `len(x)` — a
minor permissiveness difference. `exit(code)` with code > 1 doesn't round-trip
through the wasm result-line harness (proc_exit terminates before the result
line), so the §B `exit` wasm cell is ⚠️.

### 2026-06-12 — sized ints / floats / generics / traits / closures audited (no new bugs)

**Native arm (all four backends):** two new fixtures —
`internal/e2e/testdata/cases/audit_numeric_types` (i64 arithmetic, u8/u16
cast-wrapping, narrowing cast, f32/f64 arithmetic + comparison) and
`audit_generics_traits_closures` (generic fn + struct + method, trait + impl
dispatch, anonymous-function lambda, closure capture, function values,
higher-order, tail-call at depth 5000). ✅ on interp / x86-64 / arm64 / wasm,
and both clear the wasm owned-vs-borrow differential gate.

**Self-host arm (x86-64 + CI-gated arm64):** new test
`internal/e2e/self_host_audit_numgen_test.go` — 17 isolated programs covering
all of the above. All pass on the self-hosted compiler.

**Notes (no bugs filed):**
- **Lambda syntax** is the anonymous `function(x: T): R { … }` form *or* the
  concise arrow `(x: T): R => e` (#2701), which desugars to
  `function(x: T): R { return e; }`. Parameter types are required (as in the
  verbose form); the return type is optional and defaults to void. The arrow
  form is native-only so far; self-host parser support is a follow-up. (Outside
  a lambda, `=>` is the `match`-arm separator and the function-*type* arrow
  `(T) => R`.)
- **Out-of-range integer literal in a cast** (`300 as u8`) is a **static**
  checker error on native (`literal 300 does not fit in u8`); wrapping is for
  **runtime** values (`v as u8`). Fixtures use runtime values for wrap tests.

### 2026-06-12 — strings / arrays / maps audited; Array.with reuse soundness bug found; #2821 fix re-guarded

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_strings_arrays_maps` — string `.len()` /
concat / `==`/`!=` / byte index / slice `s[i:j]`; array literal / index / `.len()`
/ `.with` / iteration; `Map` `insert` / `get_or` / `has` / `len` with i32 and
string keys. ✅ on interp / x86-64 / arm64 / wasm.

**Self-host arm (x86-64 + CI-gated arm64):** new test
`internal/e2e/self_host_audit_data_test.go` — the same as 13 isolated programs.
All pass on the self-hosted compiler.

**Finding — `Array.with` in-place reuse is unsound when the receiver stays live
([#2832](https://github.com/JakeChampion/lang/issues/2832)):** for
`var c = a.with(i, v)` followed by a read of the original `a`, the interpreter
gives value semantics (`a` unchanged) while **all four compiled backends**
(native x86-64 / arm64 / wasm + self-host) reuse `a`'s buffer in place, so `c`
and `a` alias and the original is mutated. `fern -check` accepts the program. The
fix is in reuse analysis (only reuse a dead/uniquely-owned receiver) or the
checker (reject use-after-consume). Fixtures use the canonical reassignment
idiom (`w = w.with(i, v)`), well-defined everywhere.

**Re-guarded:** #2821 (self-host bare block `{ }`) was fixed upstream (#2831);
the `nested-block` case is re-enabled in `self_host_audit_builtins_test.go` and
the §A Blocks row flipped to 🔧.

### 2026-06-12 — composite types + pattern matching audited; struct-immutability self-host gap found

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_types_match` — struct decl / literal / field
access / **functional update**, methods (receiver clause), enum sum types +
payloads (incl. unit variants), `match` statement + expression, tuples + `.0`/`.1`
+ destructuring, and the built-in `Option[T]` / `Result[T, E]`. ✅ on
interp / x86-64 / arm64 / wasm.

**Self-host arm (x86-64 + CI-gated arm64):** new test
`internal/e2e/self_host_audit_types_test.go` — the same as 12 isolated programs.
All pass on the self-hosted compiler.

**Finding — struct-field immutability not enforced on self-host
([#2825](https://github.com/JakeChampion/lang/issues/2825)):** Fern struct
fields are immutable after construction (native `-check` rejects `p.x = v` with
`E048`, directing to `T { ...old, x: v }`). The self-host checker does **not**
enforce this — it accepts `p.x = v` (and `p.x += v`) and mutates. This is the
self-hosted compiler being *more permissive* than native (compiling forbidden
programs), not a runtime miscompile; functional update — the sanctioned form —
works on both. The fixtures use only functional update so they stay valid on
both compilers. **Fixed:** the self-host checker (`checker.fern`) already
detected `E048`/`E056` (the parser desugars `p.x = v` → `__set_field(...)` and
`a[i] = v` → `__set_index(...)`), and `fern -check` reported them — but the
`fern` CLI's **compile path** ran codegen (SSA / IR / AST) without the
immutability gate, so it compiled forbidden mutation straight to a binary.
`fern.fern` now runs `filter_immutability(check_module(...).diags)` before
codegen (the same gate `asm_load_run` already had), matching the native
compiler, which always type-checks ahead of codegen. Guarded by
`TestSelfHostCLIX86_64/compile-rejects-immutable-mutation`.

**Finding — wasm owned-model RC over-release
([#2828](https://github.com/JakeChampion/lang/issues/2828)):** the differential
gate `TestWASMBorrowInferMatchesOwned` (owned-everywhere vs production
borrow-inference) flagged a divergence on `audit_types_match`. Bisected to **an
enum value carrying a payload, passed as an owned function parameter and consumed
by `match`** — the owned (borrow-off) wasm lowering over-releases (traps), while
the production default (borrow-on) is correct. Masked in production; the fixture
is skip-listed out of that one differential gate (referencing #2828) but still
runs on all four backends under the production model via `TestFernFixtures`.

**Note (language direction):** the older "Struct field mutation / compound field
assign" §A row is obsolete — that operation is now a compile error. The row is
replaced with "Struct field immutability + functional update".

### 2026-06-12 — self-host dimension added; §A foundational built-ins audited; 3 self-host gaps found

**Scope change:** the audit now covers the **self-hosted** compiler as a
first-class dimension alongside native (new **S** column on every table).
Self-host verification is driven by `self_host_*_test.go` harnesses (build a
driver binary from `examples/self_host/`, feed it Fern source, assemble + run,
check exit code).

**Native arm (all four backends):** new fixture
`internal/e2e/testdata/cases/audit_core_builtins` — a single program exercising
integer arithmetic / comparison / bitwise / unary minus, operator precedence,
boolean logic **with short-circuit non-evaluation proven via a divide-by-zero
RHS that must never run**, compound assignment, `if`/`else`, `if`-expression,
`while`, C-style `for`, `for`-in array, `for`-in **string**, inclusive +
half-open ranges, `switch` (comma cases + default), `break` / `continue`, and
nested blocks. ✅ on interp / x86-64 / arm64 / wasm.

**Self-host arm (x86-64 + CI-gated arm64):** new test
`internal/e2e/self_host_audit_builtins_test.go` — the same built-ins as isolated
per-feature programs. 17 features pass on the self-hosted compiler. **Three
genuine self-host gaps surfaced** (native handles all three on every backend),
each filed as an issue and held out of the executed table:

- 🐛 **C-style `for (init; cond; step)`** — `examples/self_host/parser.fern`
  has no such `Stmt` node (only the foreach `for VAR in EXPR`); a `for (` is
  misparsed → `StmtUnknown` → the loop var is dereferenced as a pointer →
  **segfault**. Also disables `break` / `continue` *inside* a C-for (they work
  in `while` / foreach). [#2820](https://github.com/JakeChampion/lang/issues/2820).
- 🐛 **Bare nested block `{ … }`** — no `StmtBlock` in the self-host `Stmt`
  union; the block becomes `StmtUnknown` and its inner statements are dropped
  (returns 0 instead of 41). [#2821](https://github.com/JakeChampion/lang/issues/2821).
- 🐛 **`for x in <string>`** — the self-host foreach lowering assumes an array
  memory layout (len@0, elem at `base+idx*8+8`) for the iterable; a string is
  `{ data_ptr@0, len@8 }` with byte elements, so it reads the data pointer as
  the length and 8-byte-strides into the header (returns 2 instead of 131).
  String `.len()` / byte-index work in isolation on self-host.
  [#2822](https://github.com/JakeChampion/lang/issues/2822).

All three are goal-1 self-host widenings (extend `parser.fern`'s `Stmt` union +
the foreach lowering across `irlower.fern` / the AST backends). Re-add each held
-out case to `self_host_audit_builtins_test.go` once its issue is fixed.

**Also:** opened [#2817](https://github.com/JakeChampion/lang/issues/2817) for
the arm64 `std/url` heap-corruption bug below (reconfirmed reproducing today).

### 2026-06-09 — 🐛 arm64 heap-corruption in the RcFree freelist drop/reuse path (FIXED 2026-06-13 — [#2817](https://github.com/JakeChampion/lang/issues/2817))

**Found by:** `prop_url_roundtrip` property fixture — `url_decode(url_encode(s)) == s`
over 300 deterministic random inputs (full 0..255 byte range, lengths 0..47).

**Symptom:** On **arm64 only** (interp / x86-64 / wasm all pass), the round-trip
fails. The corruption is a *single byte* of a **still-live** string being
overwritten: e.g. `url_encode` output `…%DB%90%C0d` becomes `…%DB%80%C0d`
(0x90→0x80) — the byte read by `enc` is 16 lower (bit 4 cleared), yet a later
read of the same string sees the correct value. Different byte values are hit on
different iterations (state-dependent), so it is **not** a logic error in
`std/url` — it is a backend miscompile.

**Root-cause narrowing (done):**
- Toggling `ast.RcFreeEnabled = false` (disables the freelist) makes arm64
  **pass** → the bug is in the **RcFree freelist drop/reuse** path.
- The arm64 freelist helpers (`__fern_alloc`, `__fern_free`,
  `__fern_alloc_reuse`, `__fern_box_free`, `__fern_arr_dec`, `__fern_str_dec`)
  were compared against their x86-64 mirrors: the **size-class arithmetic
  matches** (small tier `(size>>4)-1`; large tier `mant + 4·e2 + 80`) and the
  per-type free-size math matches. So the helpers themselves are not the
  divergence.
- Conclusion: the defect is in the **arm64 drop/reuse call-site emission** — a
  still-live string cell is being recycled (an erroneous/early drop, or a wrong
  pointer/size passed into a drop). x86-64 emits the same IR-level drops
  correctly, so this is arm64 instruction-selection / liveness, not the IR.

**Minimal reproducer** (self-contained single module; fails arm64, passes interp/x86-64):
the mix of a string-slice branch (`out = out + s[i:i+1]`) and a
`string_from_bytes` branch inside an encode loop, followed by a decode loop that
reads the result — driven cumulatively over several LCG-generated inputs.
`internal/e2e/testdata/cases/prop_url_roundtrip/main.fern` reproduces it directly
on arm64 (drop the `backends` sidecar to see the arm64 leg fail).

**Status: FIXED (2026-06-13).** `prop_url_roundtrip` now runs on **all four
backends** (arm64 re-added to its `backends` sidecar).

**Actual root cause** (the earlier "instruction-selection / liveness" narrowing
was on the wrong layer — the *helpers* were the problem after all): on the
two-word string ABI (arm64-`TwoWordOverride`), `string_from_bytes` allocated its
heap buffer with **plain `__fern_alloc`** instead of `__fern_alloc_rc1`
(`internal/codegen/arm64/arm64.go`, the `UseTwoWordStrings` branch). A plain
buffer carries **no rc header** (no live rc at `data-8`, no payload size at
`data-4`). When the resulting string was later dropped by `__fern_str_dec`
— which reads the rc at `data-8` and, at rc==1, `box_free`s using the size at
`data-4` — it read **garbage**: either `rc_dec`'d a neighbouring cell's bytes
(the single-bit `0x90→0x80` corruption) or `box_free`'d a wrong-sized block that
overlapped a still-live cell, recycling it through the freelist. It only
surfaced under the *mixed* slice + `string_from_bytes` churn because the
interleaved `__str_slice` (rc-headered) allocations left a `1` in the word just
below a `string_from_bytes` buffer, steering `__fern_str_dec` down the
`box_free` path. `__str_slice` and `__fern_strcat` already used
`__fern_alloc_rc1` on this path, and the wasm two-word backend's
`string_from_bytes` always did — arm64's was the lone outlier.

**Fix:** one line — `string_from_bytes` (two-word path) now allocates via
`__fern_alloc_rc1`, matching `__str_slice` / `__fern_strcat` and the wasm
mirror. Guarded by `prop_url_roundtrip` running on arm64 again.

### 2026-06-09 — first property-test batch (base64, hex, url, sort, string)

Added five property fixtures under `internal/e2e/testdata/cases/prop_*`:

- `prop_codec_roundtrip` — `base64` + `hex` decode∘encode round-trip. ✅ all 4 backends.
- `prop_url_roundtrip` — `url` decode∘encode round-trip. ✅ all 4 backends (the arm64 `string_from_bytes` rc-header bug above is fixed).
- `prop_sort_i32` — `sort_i32_asc` ordering + permutation (per-value histogram) +
  idempotence. ✅ all 4 backends.
- `prop_string_involution` — `reverse_bytes` involution, `to_lower`/`to_upper`
  idempotence, length preservation. ✅ all 4 backends.

Each runs 300 deterministic LCG-generated inputs across the full byte range
(including embedded NULs / high bytes) so every backend tests identical inputs.

### 2026-06-09 — audit kicked off

- Established the audit harness: `TestFernFixtures` runs each fixture
  across interp / x86-64 / arm64 / wasm. Confirmed the full toolchain is
  available in this environment (qemu-aarch64, wasmtime, wasm-tools,
  `FERN_WASI_ADAPTER`), so no backend leg SKIPs.
- Built this inventory from `docs/STDLIB.md`, the checker's
  `FuncSigs` registrations (`internal/checker/checker.go`), and the
  README "Language at a glance" surface.
</content>
</invoke>
