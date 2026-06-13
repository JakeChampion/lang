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
| Sized int types `i8 i16 i32 i64 u8 u16 u32 u64` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | i64 arith, u8/u16 cast; out-of-range literal is a static error |
| Integer overflow / wrapping semantics | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | runtime narrowing cast wraps mod 2ⁿ |
| Float types `f32 f64` arithmetic | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `+ * /`, f32 + f64 |
| Float comparison + NaN semantics | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | `< > >=` audited; NaN semantics pending |
| `boolean` type + literals | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | exercised throughout audit fixture |
| `string` type: `+`, `==`/`!=`, indexing, slice | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | concat, eq/neq, byte index, `s[i:j]`, `.len()` |
| String literals + escape sequences | | | | | | ⬜ | |
| f-strings / interpolation | | | | | | ⬜ | confirm syntax exists |
| Owned arrays `T[]` + indexing + `.with` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | index, `.len()`, `.with` (reassign idiom); **read-after-`.with` aliases on compiled backends, [#2832](https://github.com/JakeChampion/lang/issues/2832)** |
| Slice views `[T]` | | | | | | ⬜ | |
| Tuples `(T, U)` + destructuring | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `.0`/`.1` + `var (a,b) = …` |
| `Map[K, V]` literal + ops | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `insert`/`get_or`/`has`/`len`, i32 + string keys |
| Array literals | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `var x: T = expr;` + type inference | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | i32 path; wider types pending |
| Compound assignment `+= -= *= …` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `+= -= *= /= %=` |
| `if`/`else` statement | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `if` as expression | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `while` loop | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `for(init; cond; step)` loop | ✅ | ✅ | ✅ | ✅ | 🐛 | 🐛 | **self-host: unsupported → segfault, [#2820](https://github.com/JakeChampion/lang/issues/2820)** |
| `for x in arr` / `for x in "str"` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | array ✅; `for x in <string>` self-host IR path iterates bytes — literal / local / slice / string-returning call+method ([#2822](https://github.com/JakeChampion/lang/issues/2822), #2834 + the eligibility-probe `str_ret_fns` fix) |
| inclusive / half-open ranges `for i in a..=b` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `0..4` half-open, `0..=5` inclusive |
| `switch` statement (comma cases, default) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | multi-value case + default |
| `break` / `continue` | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | S ok in while/foreach; broken inside C-for ([#2820](https://github.com/JakeChampion/lang/issues/2820)) |
| `return` (value + void) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| Blocks + expression statements | ✅ | ✅ | ✅ | ✅ | 🔧 | 🔧 | bare nested block `{}` — self-host gap fixed ([#2821](https://github.com/JakeChampion/lang/issues/2821) / #2831), re-enabled as guard |
| `struct` decl + literal + field access | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | + functional update `T { ...old, f: v }` |
| Struct field immutability + functional update | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | fields immutable (E048); self-host `fern` CLI now gates the compile path too ([#2825](https://github.com/JakeChampion/lang/issues/2825) fixed) |
| Methods (receiver clause) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `enum` sum types + payloads | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | incl. unit variants; wasm owned-model RC caveat [#2828](https://github.com/JakeChampion/lang/issues/2828) |
| `match` (exhaustiveness checked) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | payload binding, comma-separated arms |
| `match` as expression | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| Generic structs/enums (monomorphised) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `Box[T]` + generic method |
| Generic functions + inference | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `id[T](x: T): T`, inferred |
| Traits (`Display`/`Eq`/`Ord`, bounds) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | trait + impl method dispatch |
| Nested functions + closures (capture) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | `function(x: T): R { … cap … }` |
| Function values / indirect calls | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | named fn as value; higher-order |
| Lambdas (anonymous `function(…)`) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | **arrow `(x) => e` is match-arm-only, NOT a lambda** |
| Tail-call optimisation | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | depth 5000 self-recursion, no overflow |
| Modules / imports (`import "./path";`) | | | | | | ⬜ | |
| Visibility (`pub`) | | | | | | ⬜ | front-end only |
| Top-level `const` (folded) | | | | | | ⬜ | |
| `len(x)` / `.len()` builtin | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | string / array / map |

## B. Built-in functions (checker-registered)

| Function | I | X | A | W | S | Status | Notes |
|----------|---|---|---|---|---|--------|-------|
| `print(s)` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | stdout + newline |
| `write(s)` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | stdout raw, no newline |
| `eprint(s)` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | stderr (not on stdout) |
| `putchar(b)` | ✅ | ✅ | ✅ | ✅ | 🐛 | 🐛 | **self-host: undefined `__fn_putchar`, [#2839](https://github.com/JakeChampion/lang/issues/2839)** |
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
| float math builtins `__sqrt_f64` etc. | | | | | | ⬜ | via std/float |
| `strbuf_reset/append/take` | | | | | | ⬜ | |
| `__heap_bump_bytes` | | | | | | ⬜ | introspection |
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
| `std/u32` | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | max — `audit_std_path_numeric`; self-host unsigned min/max via the x86-64 IR path (`TestSelfHostNumericMethodsIRX86_64`); **wasm IR lowers u32/u64 compares as SIGNED** ([#2917](https://github.com/JakeChampion/lang/issues/2917)) |
| `std/u64` | ✅ | ✅ | ✅ | ✅ | | ⚠️ | clamp — `audit_std_path_numeric`; self-host pending (>2^63 value built by addition isn't IR-eligible on x86 yet; wasm IR signed-compare) |
| `std/float` | ✅ | ✅ | ✅ | ✅ | | ⚠️ | sqrt/floor/ceil/abs/is_finite — `audit_std_path_numeric`; self-host: f64 intrinsics (floor/ceil/sqrt/abs/round/trunc) ✅ via the IR path (`TestSelfHostFloatIntrinsicsIR`) |
| `std/string` (~120 methods) | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | core set (upper/lower/trim/contains/starts_with/ends_with/index_of/replace/repeat/pad/split) — `audit_std_string` + `self_host_string_test`; `prop_string_involution` laws; full ~120 set pending |
| `std/array` | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | reductions sum/max/min/product/sorted_asc — `audit_std_numeric` + `self_host_audit_stdarray_test` |
| `std/math` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | range/i32_max/i32_min — `audit_std_numeric` + `self_host_math_test` |
| `std/sort` | ✅ | ✅ | ✅ | ✅ | | ✅ | `prop_sort_i32` — ordering + permutation (histogram) + idempotence laws |
| `std/format` | ✅ | ✅ | ✅ | ✅ | | ⚠️ | `format_bytes` — `audit_std_textfmt`; self-host: `format_bytes` logic ✅ via the IR path (`TestSelfHostFormatBytesIR`, `i32.to_string` builtin); `format`/`format_duration_ms` pending |
| `std/csv` | ✅ | ✅ | ✅ | ✅ | | ⚠️ | parse_line/join/escape — `audit_std_textfmt`; self-host: `csv_parse_line` ✅ via the IR path (`TestSelfHostCsvParseLineIR`); escape/join pending (need `index_of`/`replace` inlining) |
| `std/log` | | | | | | ⬜ | |
| `std/io` | | | | | | ⬜ | |
| `std/io_buffered` | | | | | | ⬜ | |
| `std/path` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | join/file_name/extension — `audit_std_path_numeric` + `self_host_audit_stdpath_test` |
| `std/base64` | ✅ | ✅ | ✅ | ✅ | | ✅ | `prop_codec_roundtrip` — 300 random inputs, full byte range |
| `std/hex` | ✅ | ✅ | ✅ | ✅ | | ✅ | `prop_codec_roundtrip` |
| `std/crypto` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | SHA-256 vectors ✅ native (`audit_std_crypto`); self-host now correct via the IR path — u32 wrapping + array builders + byte builtins ([#2861](https://github.com/JakeChampion/lang/issues/2861) fixed, #2891; `TestSelfHostU32WrapIR`) |
| `std/uuid` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | v4 length/dashes/version/uniqueness — `audit_std_uuid`; self-host v4 + v7 via the IR path (`TestSelfHostUuidIR`) |
| `std/url` | ✅ | ✅ | ✅ | ✅ | | ✅ | `prop_url_roundtrip` — 300 inputs, all four backends; the arm64 heap-corruption ([#2817](https://github.com/JakeChampion/lang/issues/2817)) is fixed (two-word `string_from_bytes` now uses `__fern_alloc_rc1`) |
| `std/json` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | parse → get_i32/get_string → encode → re-parse — `audit_std_json` + `self_host_json_test`; `@derive(Json)` incl. **array fields** (`T[]`) — native all backends (`derive_json` fixture), self-host i32/string/struct arrays via the IR path ([#2766](https://github.com/JakeChampion/lang/issues/2766); `TestSelfHostJsonArrayIR`) |
| `std/http` | | | | | | ⬜ | |
| `std/tcp` | | | | | | ⬜ | |
| `std/headers` | | | | | | ⬜ | |
| `std/stream` | | | | | | ⬜ | |
| `std/time` | ✅ | ✅ | ✅ | ✅ | | ⚠️ | is_leap_year/days_in_month/date_make/format_iso — `audit_std_time`; self-host: pure-i32 helpers (is_leap_year/days_in_month) ✅ via the IR path (`TestSelfHostTimeIR`); Date/Instant methods (structs/Option) pending |
| `std/task` | | | | | | ⬜ | |
| `std/mock_platform` | | | | | | ⬜ | |
| `std/test` (~150 assertions) | | | | | | ⬜ | |
| `std/fuzz` | | | | | | ⬜ | |

## E. Core library — `core/`

| Module | I | X | A | W | S | Status | Notes |
|--------|---|---|---|---|---|--------|-------|
| `core/int` | | | | | | ⬜ | |
| `core/cmp` (traits) | | | | | | ⬜ | |
| `core/map` | | | | | | ⬜ | |
| `core/no_prelude` | | | | | | ⬜ | no-op sentinel |

---

## Audit log

Reverse-chronological. Each entry: what was checked, what was found, what
changed (fixture / fix / commit).

<!-- newest first -->

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
- **Lambda syntax** is the anonymous `function(x: T): R { … }` form. The arrow
  `(x) => e` is **match-arm-only** — native rejects it as a lambda value with
  `P001`. The §A "Lambdas" row is corrected accordingly. (The self-host parser
  accepts the invalid arrow form and then miscompiles it rather than erroring —
  an error-reporting parity nuance, not a valid-program miscompile.)
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
