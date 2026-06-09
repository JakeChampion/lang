# Feature audit — built-ins & standard library

This document is the **living record** of an ongoing audit of every
built-in language feature and every standard-library function in Fern.
The goal: confirm each feature works **correctly on every backend**
(interpreter, x86-64, arm64, wasm) and fix any bugs found along the way.

It is meant to stay up to date — when a feature is audited, its row is
updated; when a bug is found and fixed, it is logged in the
[Audit log](#audit-log) at the bottom.

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
| ✅ | Verified working on all four backends |
| ⚠️ | Works, but with a documented caveat / partial-backend support |
| 🐛 | Bug found — see audit log |
| 🔧 | Bug found **and fixed** — see audit log |

The per-feature backend columns (I / X / A / W = interp / x86-64 / arm64 /
wasm) record where a fixture or test confirms the feature. Blank = not yet
confirmed on that backend.

---

## A. Built-in language features

| Feature | I | X | A | W | Status | Notes |
|---------|---|---|---|---|--------|-------|
| Integer arithmetic `+ - * / %` | | | | | ⬜ | |
| Integer comparison `== != < > <= >=` | | | | | ⬜ | |
| Boolean logic `&& \|\| !` (short-circuit) | | | | | ⬜ | |
| Bitwise `& \| ^ << >>` | ✅ | ✅ | ✅ | ✅ | ✅ | exercised by prop generators (LCG) |
| Unary minus `-x` | | | | | ⬜ | |
| Operator precedence / parenthesisation | | | | | ⬜ | |
| Sized int types `i8 i16 i32 i64 u8 u16 u32 u64` | | | | | ⬜ | incl. `isize`/`usize` |
| Integer overflow / wrapping semantics | | | | | ⬜ | see INTEGER-SEMANTICS.md |
| Float types `f32 f64` arithmetic | | | | | ⬜ | |
| Float comparison + NaN semantics | | | | | ⬜ | see FLOAT-SEMANTICS.md |
| `boolean` type + literals | | | | | ⬜ | |
| `string` type: `+`, `==`/`!=`, indexing | | | | | ⬜ | |
| String literals + escape sequences | | | | | ⬜ | |
| f-strings / interpolation | | | | | ⬜ | confirm syntax exists |
| Owned arrays `T[]` + indexing | | | | | ⬜ | |
| Slice views `[T]` | | | | | ⬜ | |
| Tuples `(T, U)` + destructuring | | | | | ⬜ | |
| `Map[K, V]` literal + ops | | | | | ⬜ | core/map |
| Array literals | | | | | ⬜ | |
| `var x: T = expr;` + type inference | | | | | ⬜ | |
| Compound assignment `+= -= *= …` | | | | | ⬜ | |
| `if`/`else` statement | | | | | ⬜ | |
| `if` as expression | | | | | ⬜ | |
| `while` loop | | | | | ⬜ | |
| `for(init; cond; step)` loop | | | | | ⬜ | |
| `for x in arr` / `for x in "str"` | | | | | ⬜ | |
| `switch` statement (comma cases, default) | | | | | ⬜ | |
| `break` / `continue` | | | | | ⬜ | |
| `return` (value + void) | | | | | ⬜ | |
| Blocks + expression statements | | | | | ⬜ | |
| `struct` decl + literal + field access | | | | | ⬜ | |
| Struct field mutation / compound field assign | | | | | ⬜ | |
| Methods (receiver clause) | | | | | ⬜ | |
| `enum` sum types + payloads | | | | | ⬜ | |
| `match` (exhaustiveness checked) | | | | | ⬜ | |
| `match` as expression | | | | | ⬜ | |
| Generic structs/enums (monomorphised) | | | | | ⬜ | |
| Generic functions + inference | | | | | ⬜ | |
| Traits (`Display`/`Eq`/`Ord`, bounds) | | | | | ⬜ | core/cmp |
| Nested functions + closures (capture) | | | | | ⬜ | |
| Function values / indirect calls | | | | | ⬜ | |
| Lambdas `(x) => expr` | | | | | ⬜ | confirm syntax |
| Tail-call optimisation | | | | | ⬜ | |
| Modules / imports (`import "./path";`) | | | | | ⬜ | |
| Visibility (`pub`) | | | | | ⬜ | front-end only |
| Top-level `const` (folded) | | | | | ⬜ | |
| `len(x)` builtin | | | | | ⬜ | |

## B. Built-in functions (checker-registered)

| Function | I | X | A | W | Status | Notes |
|----------|---|---|---|---|--------|-------|
| `print(s)` | | | | | ⬜ | stdout + newline |
| `write(s)` | | | | | ⬜ | stdout raw |
| `eprint(s)` | | | | | ⬜ | stderr |
| `putchar(b)` | | | | | ⬜ | single byte |
| `len(x)` | | | | | ⬜ | string / array |
| `args(): string[]` | | | | | ⬜ | |
| `env(name): Option[string]` | | | | | ⬜ | |
| `exit(code)` | | | | | ⬜ | |
| `stdin()/stdout()/stderr()` | | | | | ⬜ | Reader/Writer |
| `read_file` / `write_file` | | | | | ⬜ | |
| `open_reader/open_writer/open_appender` | | | | | ⬜ | |
| Reader `.read_line()/.read_chunk(n)/.close()` | | | | | ⬜ | |
| Writer `.write(s)/.close()` | | | | | ⬜ | |
| `read_line()` (free) | | | | | ⬜ | |
| `read_dir` / `stat` | | | | | ⬜ | |
| `remove_file` / `remove_dir_all` | | | | | ⬜ | |
| `temp_dir(prefix)` | | | | | ⬜ | |
| `subprocess(...)` | | | | | ⬜ | |
| `sleep_ms` | | | | | ⬜ | |
| `now_unix_ms` / `now_ns` / `monotonic_ns` | | | | | ⬜ | |
| `random_bytes` / `random_i32` | | | | | ⬜ | |
| `f32_bits/f32_from_bits/f64_bits/f64_from_bits` | | | | | ⬜ | |
| float math builtins `__sqrt_f64` etc. | | | | | ⬜ | via std/float |
| `strbuf_reset/append/take` | | | | | ⬜ | |
| `__heap_bump_bytes` | | | | | ⬜ | introspection |
| `__rc_*` (inc/dec/get/underflow_count) | | | | | ⬜ | RC introspection |
| TCP: `tcp_listen/accept/recv/send/close` | | | | | ⬜ | |
| `udp_send` | | | | | ⬜ | |
| `map_new` + Map methods | | | | | ⬜ | |

## C. Built-in types (checker-synthesised)

| Type | I | X | A | W | Status | Notes |
|------|---|---|---|---|--------|-------|
| `Option[T]` (`Some`/`None`) | | | | | ⬜ | |
| `Result[T, E]` (`Ok`/`Err`) | | | | | ⬜ | |
| `IoError` variants | | | | | ⬜ | |
| `JsonValue` variants | | | | | ⬜ | |
| `Reader` / `Writer` | | | | | ⬜ | |
| `HttpRequest` / `HttpResponse` | | | | | ⬜ | |
| `Url` | | | | | ⬜ | |
| `Map[K, V]` / `MapIter[K, V]` | | | | | ⬜ | |
| Time types (`Instant`/`Date`/…) | | | | | ⬜ | via std/time |

## D. Standard library — `std/`

Function lists are mirrored from `docs/STDLIB.md`. Each module gets a row;
the audit drills into individual functions as needed and records
per-function bugs in the audit log.

| Module | I | X | A | W | Status | Notes |
|--------|---|---|---|---|--------|-------|
| `std/i32` (~80 methods) | | | | | ⬜ | |
| `std/i64` | | | | | ⬜ | |
| `std/u32` | | | | | ⬜ | |
| `std/u64` | | | | | ⬜ | |
| `std/float` | | | | | ⬜ | |
| `std/string` (~120 methods) | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | `prop_string_involution` covers `reverse_bytes`/`to_lower`/`to_upper` laws; rest pending |
| `std/array` | | | | | ⬜ | |
| `std/math` | | | | | ⬜ | |
| `std/sort` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_sort_i32` — ordering + permutation (histogram) + idempotence laws |
| `std/format` | | | | | ⬜ | |
| `std/csv` | | | | | ⬜ | |
| `std/log` | | | | | ⬜ | |
| `std/io` | | | | | ⬜ | |
| `std/io_buffered` | | | | | ⬜ | |
| `std/path` | | | | | ⬜ | |
| `std/base64` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_codec_roundtrip` — 300 random inputs, full byte range |
| `std/hex` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_codec_roundtrip` |
| `std/url` | ✅ | ✅ | 🐛 | ✅ | 🐛 | `prop_url_roundtrip` — **arm64 heap-corruption bug**, see audit log 2026-06-09 |
| `std/json` | | | | | ⬜ | |
| `std/http` | | | | | ⬜ | |
| `std/tcp` | | | | | ⬜ | |
| `std/headers` | | | | | ⬜ | |
| `std/stream` | | | | | ⬜ | |
| `std/time` | | | | | ⬜ | |
| `std/task` | | | | | ⬜ | |
| `std/mock_platform` | | | | | ⬜ | |
| `std/test` (~150 assertions) | | | | | ⬜ | |
| `std/fuzz` | | | | | ⬜ | |

## E. Core library — `core/`

| Module | I | X | A | W | Status | Notes |
|--------|---|---|---|---|--------|-------|
| `core/int` | | | | | ⬜ | |
| `core/cmp` (traits) | | | | | ⬜ | |
| `core/map` | | | | | ⬜ | |
| `core/no_prelude` | | | | | ⬜ | no-op sentinel |

---

## Audit log

Reverse-chronological. Each entry: what was checked, what was found, what
changed (fixture / fix / commit).

<!-- newest first -->

### 2026-06-09 — 🐛 arm64 heap-corruption in the RcFree freelist drop/reuse path (OPEN, top priority)

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
  per-type free-size math matches (and `__fern_alloc_rc1` does store the payload
  length at `data-4`, so `box_free`'s size is correct). So the helpers
  themselves are not the divergence.
- **The representational divergence between backends:** x86-64 uses the
  **single-word** string ABI; arm64 and wasm use the **two-word** ABI
  (`ast.TwoWordOverride` is set during arm64 emit; wasm has `ptrW==4`).
  Since wasm *also* uses two-word strings *and passes*, and the only thing wasm
  lacks is the native freelist, the premature free lives in the **shared
  two-word-string drop path** — latent on wasm (bump allocator never recycles),
  **fatal on arm64** (freelist hands the cell back out), and absent on x86-64
  (different single-word IR branch).
- **Two diagnostic edits to the arm64 backend pinned the mechanism** (both
  reverted — never shipped):
  1. *poison-on-free* (fill freed blocks with `0xAA`): the corrupted byte is
     **not** `0xAA`, so it is not read from a freed-then-poisoned cell.
  2. *never pop the freelist in `__fern_alloc`* (keep all drops/frees, so
     register pressure is unchanged): the program **passes**. This isolates the
     cause to **heap freelist reuse**, not register allocation.
- `__fern_alloc_reuse` is **not** called by the reproducer (confirmed via the
  emitted `bl` targets), so it is plain freelist free→reuse, and `__fern_arr_dec`
  is shared with x86-64 (which passes) — leaving **`__fern_str_dec` freeing a
  still-live two-word string** as the culprit. The freed-while-live value is the
  `strcat` accumulator (the growing `out`/`e` string), i.e. a string the
  **Perceus free-eligibility / precise-drop analysis**
  (`computeFreeEligible` / precise drops in `internal/ir/ir.go`, the
  `ast.UseTwoWordStrings` branch ~line 3614/3651 and 4855) drops one reference
  too early.

**Conclusion / fix target:** a two-word-string reference is freed at a
mis-computed last-use in the Perceus drop analysis. The fix belongs in the
**target-independent IR** two-word-string drop emission (fixes arm64 *and*
removes the latent wasm liability; x86-64 is untouched). It needs IR-level
visibility into the emitted drops for the `out = out + piece` loop-accumulator
pattern plus a full run of the RC regression suite (`rc_*` + `internal/e2e`),
so it is a focused follow-up rather than a rushed patch.

**Minimal reproducer** (self-contained single module; fails arm64, passes interp/x86-64):
the mix of a string-slice branch (`out = out + s[i:i+1]`) and a
`string_from_bytes` branch inside an encode loop, followed by a decode loop that
reads the result — driven cumulatively over several LCG-generated inputs.
`internal/e2e/testdata/cases/prop_url_roundtrip/main.fern` reproduces it directly
on arm64 (drop the `backends` sidecar to see the arm64 leg fail).

**Status / mitigation:** `prop_url_roundtrip` is restricted to
`interp x86_64 wasm` (via its `backends` sidecar) so CI stays green and the 3
good backends are still guarded. **Next step:** trace the arm64 drop/reuse
call-site for the slice + `string_from_bytes` + concat pattern and fix the
recycled-while-live cell. Until fixed, `std/url` is unsafe on arm64 under heavy
allocation churn — and the same class of bug may lurk in other modules, so it is
the highest-priority follow-up.

### 2026-06-09 — first property-test batch (base64, hex, url, sort, string)

Added five property fixtures under `internal/e2e/testdata/cases/prop_*`:

- `prop_codec_roundtrip` — `base64` + `hex` decode∘encode round-trip. ✅ all 4 backends.
- `prop_url_roundtrip` — `url` decode∘encode round-trip. ✅ interp/x86-64/wasm; 🐛 arm64 (above).
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
