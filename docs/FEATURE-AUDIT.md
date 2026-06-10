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
| `std/i32` (~80 methods) | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | `prop_int_roundtrip` covers `to_string`/`parse_int`/`to_hex`/`parse_hex_int` round-trips; rest pending |
| `std/i64` | | | | | ⬜ | |
| `std/u32` | | | | | ⬜ | |
| `std/u64` | | | | | ⬜ | |
| `std/float` | | | | | ⬜ | |
| `std/string` (~120 methods) | 🔄 | ✅ | 🔧 | 🔧 | 🔧 | `prop_string_involution` + `prop_split_join` (✅ all 4); the two-word `split`/`append` bug was found here and **fixed** — see audit log |
| `std/array` | | | | | ⬜ | |
| `std/math` | | | | | ⬜ | |
| `std/sort` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_sort_i32` — ordering + permutation (histogram) + idempotence laws |
| `std/format` | | | | | ⬜ | |
| `std/csv` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_csv_roundtrip` — escape/join/parse round-trip, quoting alphabet |
| `std/log` | | | | | ⬜ | |
| `std/io` | | | | | ⬜ | |
| `std/io_buffered` | | | | | ⬜ | |
| `std/path` | | | | | ⬜ | |
| `std/base64` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_codec_roundtrip` — 300 random inputs, full byte range |
| `std/hex` | ✅ | ✅ | ✅ | ✅ | ✅ | `prop_codec_roundtrip` |
| `std/url` | ✅ | ✅ | ✅ | ✅ | 🔧 | `prop_url_roundtrip`; arm64 heap-corruption bug **fixed** — see audit log 2026-06-09 |
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

### 2026-06-09 — 🔧 FIXED: consuming `.append`/`.with` in return/expression position double-dropped the receiver's elements

**The bug** (investigation below): `return out.append(x)` (and
`var b = a.append(x)` / `b = a.append(x)` with `b != a`) reclaimed the
old receiver array with the **element-walking** drop
(`__fern_drop_arr_str` two-word / `__fern_drop_arr_ptr` single-word) at
the exit sweep. But the push/grow helper **shallow-copies** the element
pointers into the result *without* an inc, so walking the old buffer and
dec'ing each element **double-freed** the strings now owned by the result.
On the two-word string backends (arm64 + wasm) this surfaced as
single-byte heap corruption of `split`'s output (earlier parts clobbered,
e.g. `"ac"`→`"0p"`); single-word x86-64 was latent. The self-reassign
form `out = out.append(x)` was already correct — its reinit-drop uses the
buffer-only `__fern_arr_dec`.

**The fix** (`internal/ir/ir.go`): new `computeShallowArrDrops` analysis
marks an owned array local consumed by a `.append`/`.with` at its **last
use** in a non-self-reassigning statement; the exit-sweep `emitDec` then
reclaims that buffer with `__fern_arr_dec` (buffer only) instead of the
element-walking drop — mirroring the reassign path. The shared
`__fern_arr_push_grow` rc dance (rc 2→1 in place, free old on copy) is
preserved, so no leak and no double-free.

**Investigation:** bisected the `prop_split_join` failure to a function
returning a `string[]` built by appending sliced strings in a loop;
`__str_slice`, the two-word element store/load, and `__fern_arr_push_grow`
all checked out. Dumping the lowered IR (throwaway `ir.LowerWith` harness)
showed the in-loop `out = out.append(...)` emitting shallow `arr_dec`
while the final `return out.append(...)` emitted the element-walking
`drop_arr_str` — the smoking gun. Confirmed single-word x86-64 emits the
analogous `drop_arr_ptr` (same double-drop, latent because of timing).

**Verification:** `prop_split_join` now passes on **all four backends**
(arm64+wasm re-enabled). Regression: IR/codegen/checker unit tests; the
RC / drop / move / consuming / reuse / string-concat / string-array e2e
suites (all backends); the full `TestFernFixtures` + array/map-builder +
combinator suites — all green.

### 2026-06-09 — 🐛→🔧 two-word `string[]` built by appending sliced strings in a loop corrupts earlier elements (investigation)

**Found by:** `prop_split_join` property fixture
(`join(split(s, ",")) == s`). Fails on **arm64 AND wasm** (both two-word
string backends); **interp + x86-64 pass** (single-word). Because it
fails on wasm — which has no freelist — it is **independent of the
`string_from_bytes` freelist fix above**: a separate, pre-existing
two-word-string bug.

**Symptom:** `s.split(",")` returns parts with the **correct lengths**
but **corrupted bytes** — e.g. for `",ac,abc,bcacbbaabac,b"` arm64
returns `["", "0p", "5bc", "       bac", "b"]` (later bytes of each part
survive; the leading bytes are clobbered — `out[3]`'s 11-byte heap buffer
has its first 8 bytes overwritten).

**Narrowing (done) — minimal reproducer:** a function that builds and
returns a `string[]` by appending `s[start:i]` **slices inside a loop**:
```
function mk(s: string): string[] {
    var out: string[] = [];
    var start: i32 = 0; var i: i32 = 0; var n: i32 = s.len();
    while (i + 1 <= n) {
        if (s[i] == 44) { out = out.append(s[start:i]); i = i + 1; start = i; }
        else { i = i + 1; }
    }
    return out.append(s[start:n]);
}
```
Bisection isolates the trigger to **loop + sliced (freshly-allocated)
two-word string + array append**:
- the same appends **unrolled** (no loop) → correct;
- a loop appending a **string literal** (not a slice) → correct;
- direct slices outside any array, and appends in `main` (not a returned
  function result) → correct.
So it is the combination of a loop body that slices the parameter and
appends the result to a `string[]`. `__str_slice` itself is fine
(direct slicing round-trips); the corruption is in the loop/append/array
interaction for two-word strings.

**Status:** ✅ FIXED (see the FIXED entry above). `prop_split_join` runs
on all four backends again; the `backends` sidecar was removed.
`std/string.split` (and the `splitn` / `fields` / `lines` / CSV/HTTP
parsers built on it) are correct on every backend.

### 2026-06-09 — 🔧 FIXED: arm64 two-word `string_from_bytes` allocated a headerless string

**The bug** (full investigation below): the arm64 two-word-string
`string_from_bytes` helper allocated its heap buffer with **raw
`__fern_alloc`** instead of `__fern_alloc_rc1`. Every sibling helper
(`__fern_strcat` / `__str_slice` two-word, and the single-word
`string_from_bytes`) uses `__fern_alloc_rc1`, which writes `rc = 1` at
`data-8` and the payload length at `data-4`. A raw buffer has neither —
so when such a string was later dropped via `__fern_str_dec`, the helper
read a **garbage rc** at `data-8`; whenever that garbage happened to be
`1`, it called `__fern_box_free` with a **garbage size** from `data-4`,
pushing a bogus block onto the freelist. A subsequent allocation popped
that block and its write **overwrote a still-live string** (the URL
round-trip's encoded buffer), flipping one byte. arm64-only because:
wasm is two-word but has no freelist (latent, harmless) and its
`string_from_bytes` already used `alloc_rc1` + an inline path; x86-64 is
single-word (different helper).

**The fix** (`internal/codegen/arm64/arm64.go`,
`emitStringFromBytesRuntime`, two-word path): `bl __fern_alloc` →
`bl __fern_alloc_rc1`, so the result is a properly rc-headered owned
heap string. One instruction. The `strcat2W` helper already carried the
exact same fix with a comment about a past SIGSEGV from raw buffers being
str_dec'd — `string_from_bytes` was simply missed.

**Verification:** `prop_url_roundtrip` now passes on **all four
backends** (arm64 re-enabled, `backends` sidecar removed). Regression:
the RC + string e2e suites (`Rc*` / `*HeapBump*` / `*StringConcat*` /
`*StringArray*` / `*StringReinit*`, all three backends) and the
ir/codegen/checker unit tests all pass. (Follow-up perf nicety, not
done: the arm64 two-word path still lacks the `len ≤ 7` inline-SSO
fast path that the single-word and wasm versions have — short
`string_from_bytes` results go to the heap instead of staying inline.
Correct, just a missed allocation-elision opportunity.)

### 2026-06-09 — 🐛→🔧 arm64 heap-corruption found via prop_url_roundtrip (investigation)

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

**Conclusion (this entry's hypothesis was partly wrong — see the FIXED
entry above for the real cause).** The reasoning correctly reached
"`__fern_str_dec` frees a still-live two-word string under freelist
reuse," but guessed the *premature drop* came from a mis-computed last-use
in the Perceus precise-drop analysis. The actual cause was simpler and
lower-level: the dropped string was a `string_from_bytes` result that had
been allocated **without an rc header** (raw `__fern_alloc`), so
`__fern_str_dec` read a garbage rc and freed a garbage size. The drop was
emitted in the right place; the *value* it dropped was malformed. Dumping
the lowered IR for `dec` (a throwaway `ir.LowerWith` harness under
`TwoWordOverride=true`) plus a before/after `hex_encode(e)` probe — showing
`e` intact after `enc` and corrupted after `dec` — localised it to the
`string_from_bytes` decode path, and reading `emitStringFromBytesRuntime`
showed the raw-alloc.

**Minimal reproducer** (self-contained single module; fails arm64, passes interp/x86-64):
the mix of a string-slice branch (`out = out + s[i:i+1]`) and a
`string_from_bytes` branch inside an encode loop, followed by a decode loop that
reads the result — driven cumulatively over several LCG-generated inputs.
`internal/e2e/testdata/cases/prop_url_roundtrip/main.fern` reproduces it directly
on arm64 (drop the `backends` sidecar to see the arm64 leg fail).

**Status:** ✅ FIXED (see the FIXED entry above). `prop_url_roundtrip` runs
on all four backends again; the `backends` sidecar was removed.

### 2026-06-09 — second property-test batch (int, csv, split/join)

Added three more `prop_*` fixtures:

- `prop_int_roundtrip` — `parse_int∘to_string` (incl. negatives) and
  `parse_hex_int∘to_hex`. ✅ all 4 backends.
- `prop_csv_roundtrip` — `csv_parse_line∘csv_join` over field arrays
  drawn from a quoting/escaping alphabet (`,` `"` letters spaces).
  ✅ all 4 backends.
- `prop_split_join` — `join∘split` for a single-byte separator. Found
  the two-word `split`/`append` double-drop bug (logged above, now ✅
  fixed); passes on all four backends.

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
