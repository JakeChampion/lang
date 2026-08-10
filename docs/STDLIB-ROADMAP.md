# Stdlib roadmap

Captures a survey of seven reference languages — Roc, Rust,
MoonBit, Gleam, Elixir, Go, Zig — and proposes a prioritised
list of stdlib additions for Fern. Companion to
`ROADMAP-AND-SELF-HOSTING.md` and `LANGUAGE-DIRECTION.md`.

Fern is a general-purpose language whose two sweet spots are small
fast-startup CLI tools and short-lived edge-function-style HTTP
servers. The picks below are ordered by leverage for those two
workloads first — that's where the stdlib is most complete — but
general-purpose breadth is now in scope rather than something to
trade away, so a broadly useful primitive is no longer disqualified
just because it isn't edge/CLI-specific.

## Current state

What Fern's prelude / built-ins cover today, as a baseline:

- **strings**: `is_empty`, `to_string`, `repeat`, `starts_with`,
  `ends_with`, `contains`, `index_of`, `trim`, `bytes` /
  `as_bytes`, `to_lower` / `to_upper`, `split`, `lines`,
  `replace`, `parse_int`, `parse_float`
- **arrays**: `push` (generic), and on `string[]`: `join`,
  `index_of`, `contains`, `reverse`. `sort_i32_asc / desc`
  for `i32[]`.
- **numbers**: `int_to_string`, method-style `to_string` for
  `i32` / `u32` / `i64` / `u64` / `f32` / `f64`, plus
  `parse_int`, `parse_float`.
- **bytes**: `is_digit`, `is_alpha`, `is_alnum`, `is_ascii`,
  `is_ascii_white_space`, `is_hex_digit`.
- **encoding**: `base64_encode` / `_decode`, `hex_encode` /
  `_decode`, `url_encode` / `_decode` / `url_parse`,
  `query_parse`, `json_encode` / `json_parse`.
- **maps**: `Map[K, V]` with `set` / `get` / `get_or` / `has`
  / `delete` / `iter` / `keys` / `values` / `len` / `clear`.
- **i/o**: `read_file`, `write_file`, `open_reader/writer/appender`,
  `read_line`, `read_chunk`, `exit`, `env`, `args`, `random_bytes`.
- **net**: `tcp_listen / accept / recv / send / close`,
  `http_parse_request`, `http_serialize_response`, `tcp_serve`.
- **misc**: `format(fmt, args)`, f-string literals.

## Per-language highlights (gaps Fern doesn't have)

Survey done 2026-05-15. URLs of doc sites surveyed:
- Roc: https://www.roc-lang.org/builtins
- Rust: https://doc.rust-lang.org/std/
- MoonBit: https://docs.moonbitlang.com/, https://github.com/moonbitlang/core
- Gleam: https://hexdocs.pm/gleam_stdlib/
- Elixir: https://hexdocs.pm/elixir/
- Go: https://pkg.go.dev/std (strings, strconv, fmt, sort, slices, maps)
- Zig: https://ziglang.org/documentation/master/std/

**Roc** — `Str.walkUtf8`, `Str.toUtf8`/`fromUtf8`, `List.map`
/`keepIf`/`walk`/`sortWith`/`chunksOf`/`mapWithIndex`,
`Result.map`/`mapErr`/`withDefault`/`try`, `Num.toStr` with
format records, `Dict.update` (insert-or-modify in one pass),
`Set` type, `Str.replaceEach` (vs Fern's single replace).

**Rust** — `str::char_indices` & `chars()`,
`str::splitn/rsplit`, `str::trim_start_matches`/`trim_end_matches`,
`Iterator` trait (`map`/`filter`/`fold`/`take`/`skip`/`enumerate`
/`zip`/`chain`/`flat_map`/`collect`), `format!` width / precision
/ fill specs, `?` operator, `Path` / `PathBuf` (`join` /
`parent` / `file_name` / `extension`), `u32::from_str_radix`
/ `to_string_radix`, `Duration` + `Instant`, `Vec::dedup` /
`windows` / `chunks` / `binary_search`.

**MoonBit** — `@string.View` (zero-copy slicing), `Iter[T]`
lazy iterator with full combinator set, `@buffer.T` for
efficient string building, pipe-friendly method syntax,
`@json.from_string`, `Char.is_digit` / `to_int`.

**Gleam** — Pipe-oriented: `string.pad_start`/`pad_end`,
`string.slice(from, length)`, `string.split_once`, `list.map`
/`filter`/`fold`/`find`/`group`/`intersperse`/`key_find`/`at`,
`result.try`/`then`/`map`/`map_error`/`unwrap`, `option.then`
/`map`/`unwrap`, `int.to_string_in_base(n, base)`,
`int.parse_in_base`, `dict.update`, `bit_array` ops.

**Elixir** — `String.pad_leading`/`trailing`, `String.split`
with limit + trim, `String.graphemes`, `String.duplicate`,
`Enum.map`/`filter`/`reduce`/`group_by`/`chunk_every`/`zip`
/`take`/`drop`/`sort_by`/`uniq`/`frequencies`/`min_by`/`max_by`,
`Map.update!`/`merge`/`put_new`/`get_lazy`,
`Integer.digits(n, base)`, `Integer.parse`,
`Float.round(n, precision)`, `IO.read(:stdio, :all)`,
`IO.puts`, `Path.join`/`extname`/`basename`.

**Go** — `strings.Builder` (cheap concat), `strings.Cut`
(split-once), `strings.Fields` (whitespace split),
`strings.Map` (transform runes), `strings.EqualFold`,
`strconv.FormatInt(n, base)` / `ParseInt(s, base, bits)`,
`strconv.Quote`, `sort.Slice` w/ comparator,
`slices.Sort`/`SortFunc`/`BinarySearch`/`Index`/`Contains`
/`Equal`/`Clone`/`Insert`/`Delete`/`Compact`,
`maps.Keys`/`Values`/`Clone`/`Equal`/`Copy`, `fmt.Sprintf`
width/precision (`%5d`, `%-10s`, `%.2f`, `%x`),
`time.Now`/`Sub`/`Format`/`Parse`,
`path/filepath.Join`/`Ext`/`Base`/`Dir`/`Clean`, `io.ReadAll`,
`io.Copy`.

**Zig** — `std.mem.split`/`tokenize`/`indexOf`/`lastIndexOf`
/`startsWith`/`endsWith`, `std.fmt.parseInt(T, str, base)` &
`formatInt` with width/fill/alignment/radix in one call,
`std.ArrayList` (growable), `std.fmt.bufPrint`,
`std.sort.pdq` w/ comparator,
`std.fs.path.join`/`extension`/`basename`/`dirname`,
`std.time.nanoTimestamp`/`Instant`,
`std.io.getStdIn().reader().readAllAlloc`,
`error{...}` sets + `try` / `catch` / `errdefer`.

## Top picks, ordered by impact-to-effort

Each item gets:
- Proposed shape (function/method form, signatures)
- 1-line motivation
- Closest existing language inspiration
- Difficulty (**small** = ~50-line prelude addition;
  **medium** = needs checker / IR work; **large** = needs
  runtime support)
- Status: ☐ not started · ◐ in flight · ☑ shipped

---

### 1. Generic array combinators · medium · ☑

**Surface**: `map[T, U](xs: T[], f: fn(T) U) U[]`, `filter`,
`fold[T, A](xs, init: A, f: fn(A, T) A)`, `any`, `all`,
`find` (returning `Option[T]`), `enumerate` (returning
`(i32, T)[]`).

**Why**: Single biggest expressivity win; everything else
builds on these. Today `for` + `push` is the only path.

**Inspiration**: Rust Iter, Elixir Enum, Gleam list.

**Notes**: Eager (returns arrays) is fine for v1; cheap
single-cursor heap-bump allocation plus RC reclaim of the
intermediates makes copying acceptable. Needs the
generic-fn-over-`T[]` machinery (push already exercises it).

**Status**: shipped — all seven landed in `std/array` as free
generic functions over `T[]` (the exact surface above; the
function-type syntax is `(T) => U`, not `fn(T) U`). Eager as
planned. Called qualified: `array.map(xs, f)` etc. Coverage:
`examples/tests/array_combinators_test.fern` (20 cases through
the std/test runner — happy path, empty-array semantics,
type-changing map, accumulator-type-differing fold, a
captured-variable closure, both Option arms of find) gated by
`TestRunnerArrayCombinatorsExample`; plus `TestStdArrayCombinators`
in `internal/e2e/generic_array_combinators_test.go` pins the
qualified stdlib calls across interp + x86-64 + wasm-bin. The
enabling monomorph substitution fix (the `*ast.Assign` walker
gap that blocked `out = out.push(f(x))`) is documented at #1758
and pinned by `TestGenericArrayCombinators`.

A future lazy `std/iter` and/or pipe / UFCS sugar (`xs.map(f)`)
can layer on top of these eager bodies without reworking them.

### 2. Result / Option combinators + `?` operator · medium · ☐

**Surface**: `Result.map` / `map_err` / `and_then` /
`unwrap_or` / `ok` / `err`, `Option.map` / `and_then` /
`unwrap_or` / `or_else`. Plus postfix `?` desugaring to
early-return on `Err` / `None`.

**Why**: Removes the dominant boilerplate in HTTP handlers
and parsers. Result is everywhere already.

**Inspiration**: Rust `?`, Gleam `use`, Zig `try`.

**Notes**: Combinators are ~50 lines each; `?` is a parser
desugar + checker rule.

### 3. String slice / pad / split_once / trim_matches · small · ☑

**Surface**: `slice(s, start, end)` (byte indices, half-open),
`pad_start(s, n, ch)`, `pad_end`, `split_once(s, sep)` →
`Option[(string, string)]`, `trim_start_matches`,
`trim_end_matches`, `replace_n(s, old, new, n)`.

**Why**: Closes most day-to-day string gaps. Pad + slice
are required for any formatted output (tables, hex dumps).

**Inspiration**: Go strings, Gleam string.

**Status**: shipped — `pad_start` / `pad_end` / `split_once`
/ `trim_start_matches` / `trim_end_matches` / `replace_n` /
`count`. Slicing is already covered by the built-in
`s[start:end]` syntax — no helper needed.

### 4. Number format specs · medium · ☐

**Surface**: `format_int(n, FormatOpts { base, min_width,
pad_char, sign })`, `format_float(f, FormatOpts {
precision, mode: Fixed / Sci })`. Plus f-string format specs:
`f"{n:04x}"`, `f"{f:.3}"`.

**Why**: Currently impossible to render `01:02:03`, hex
dumps, fixed-decimal money. Highest "felt absence" item.

**Inspiration**: Rust `format!`, Zig `std.fmt`.

**Notes**: Runtime helpers are small; the f-string spec
parsing is the real work.

### 5. StringBuilder · medium · ☐

**Surface**: `StringBuilder` with `push`, `push_str`,
`push_int`, `to_string`, `len`. Arena-backed.

**Why**: Repeated `+` is O(n²); HTTP response building +
JSON encoding paths all want this. Pairs with arena.

**Inspiration**: Go `strings.Builder`, MoonBit Buffer, Rust
`String`.

**Notes**: Needs runtime support for a resizable byte buffer
(arena bump is enough; no realloc needed if we size the
initial capacity from a hint).

### 6. UTF-8 / codepoint iteration · medium · ☐

**Surface**: `chars(s) Char[]` (or a lazy `char_iter`),
`char_at(s, byte_index) Option[(Char, i32)]`, `Char` type
with `is_digit` / `is_alpha` / `to_lower` / `to_upper` /
`to_int`, `string_from_chars(cs)`.

**Why**: Byte-only today; anything non-ASCII (URL paths,
JSON strings with escapes, CLI input) is fragile.

**Inspiration**: Rust chars, Roc walkUtf8.

**Notes**: Needs a `Char` type (or `i32` codepoint alias) +
a UTF-8 decode runtime function.

### 7. Map convenience methods · small · ☑

**Surface**: `Map.update(k, init, fn(V) V)`,
`Map.get_or_insert(k, default)`, `Map.merge(other)` / `extend`,
`Map.entries()` → `(K, V)[]`, `map.from(pairs)`,
`Map.contains_value(v)`.

**Why**: The single insert-or-modify pattern (counters,
group_by) currently takes 4 lines.

**Inspiration**: Elixir Map, Gleam dict, Rust entry API.

**Status**: all shipped in `internal/stdlib/core/map.fern` (#2685).
`entries` / `merge` / `extend` / `from` / `get_or_insert` landed
first; `update` (one-pass insert-or-modify) and `contains_value`
complete the set. Covered by `internal/e2e/map_verbs_test.go`
(interp + wasm) and `examples/tests/map_verbs_test.fern` (the
pure-Fern runner). `from_entries` is spelled `map.from(pairs)`.

### 8. Path manipulation (string-level) · small · ☑

**Surface**: `path_join(parts: string[])`, `path_parent(p)`,
`path_file_name(p)`, `path_extension(p)`, `path_clean(p)`.

**Why**: Every CLI tool needs these; pure string ops, no
FS interaction required.

**Inspiration**: Go `path/filepath`, Zig `fs.path`.

**Status**: shipped — `path_join` / `path_parent` /
`path_file_name` / `path_extension` / `path_clean`. `path_clean`
resolves `..`, `.`, and duplicate `/` lexically (Go `path.Clean`,
Unix mode); a rooted `..` cannot climb above `/`, a relative
leading `..` is kept, and a path that cancels out cleans to `.`.

### 9. stdin + println + io.Copy · small · ☑ (partial)

**Surface**: `read_all_stdin() string`, `print(s)` /
`println(s)`, `eprintln(s)`, `copy(reader, writer) i64`.

**Why**: `read_file` + `write_file` exist but no stdin
equivalent; `println` is more discoverable than `write` to
fd 1.

**Inspiration**: Go `io.ReadAll`, Elixir `IO.puts`.

**Status**: `read_all_stdin()` shipped. `print` / `eprint`
already exist (Fern's `print` is the println variant — appends
a newline). `copy(reader, writer)` deferred — needs a real
Reader/Writer plumbing decision.

### 10. Generic `sort_by(cmp)` + `sort_key(fn)` · medium · ☑

**Surface**: `sort_by[T](xs: T[], cmp: fn(T, T) i32)`,
`sort_key[T, K](xs, fn(T) K)` where `K` is `Ord`.

**Why**: Current sort is `i32`-only; struct-array sort is
the common case.

**Inspiration**: Go `sort.Slice`, Rust `sort_by`.

**Notes**: `sort_by[T](xs, cmp)` (comparator-driven, stable
insertion sort) shipped in `std/array.fern` (#2689), free +
receiver-method forms. `sort_key[T, K: cmp.Ord](xs, key)` has now
landed in `std/sort.fern` — the generic-key Schwartzian sort
(sibling of the i32-pinned `sort_by_i32_key`), dispatching the
order through `key.cmp(...)` so any `Ord` key (`string`, `u64`, a
`@derive(Ord)` type) works. Landing it self-host-first surfaced —
and fixed — two monomorphiser gaps: a type param appearing ONLY in
a fn-param's return (`key: (T) => K`) was never bound (template
silently dropped → link error), and an UNBOUNDED element param
surviving in the return type (`T[]`) left callers unable to recover
the element type for dispatch. Deprecating `sort_i32_asc/desc` in
favor of the `Ord`-bound generic remains open.
**Status**: every non-consuming sort (generic `sort_by` /
`core/cmp.sort[T: Ord]` and the monomorphic `sort_i32_*` /
`sort_i64_*` / `sort_u32/u64_*` / `sort_strings_*` families) is now a
stable bottom-up merge sort, O(n log n) (see
`docs/LANGUAGE-REVIEW-2026-07.md` Part VIII item 7, which flagged the
O(n²) insertion sorts). Insertion sort survives only in the `fip`
`sort_i32_inplace_*` variants, which cannot allocate merge scratch by
definition. The scalar entry points stay monomorphic for the direct
`<`/`>` hot-loop compare; the string sorts are monomorphic only for a
wasm-codegen reason. All three original enablers have landed (modload
rewrites module-local comparator references from arg positions /
lambda bodies, #4802; the `call_indirect` seam accepts two-slot
`string` params, #4804; the `__fern_box_free` funcidx collision is
fixed, #4816), and a `sort_by[string]` instantiation now compiles +
runs standalone — but delegating the three string sorts to `sort_by`
still makes `prop_sort_strings` fail `-target wasm32-wasi` validation (a
separate two-slot-`string` value-flow defect surfaced only through the
mangled-module delegation, #4829). They become `sort_by` delegations
once #4829 closes; the planned deprecation in favor of the `Ord`-bound
generic remains open.

### 11. Time primitives · medium · ☐

**Surface**: `now_unix_ms() i64`, `now_unix_ns() i64`,
`format_http_date(ms) string`, `parse_http_date(s)`,
`Duration` as an i64-ms alias.

**Why**: Edge handlers need request timing, cache headers,
log timestamps. HTTP-date is the killer use case given the
HTTP server focus.

**Inspiration**: Go time (subset), Zig `std.time`.

**Notes**: Needs a syscall (`clock_gettime`) per backend.

### 12. chunks / windows / take / drop / slice on arrays · small · ☐

**Surface**: `chunks[T](xs, size) T[][]`, `windows[T](xs,
size) T[][]`, `take[T](xs, n)`, `drop[T](xs, n)`,
`slice[T](xs, start, end)`.

**Why**: Frequently needed for batch processing (base64
chunking, paged DB writes, etc.). Cheap once #1 exists.

**Inspiration**: Rust slice methods, Elixir Enum.chunk_every.

### 13. Typed error enums + `?` plumbing · small + medium · ☐

**Surface**: Convention: `union Error { Io(string),
Parse(string), Net(string), ... }`. Plus `Result.map_err`
so `?` can convert between domain errors.

**Why**: Without this `?` just bubbles `string` errors; loses
context. Pairs with #2.

**Inspiration**: Rust error enums, Zig error sets.

**Notes**: Auto error-conversion (Rust's `From` trait) is
medium-effort; convention alone is small.

### 14. Integer parsing / formatting in arbitrary base · small · ☑

**Surface**: `parse_int_radix(s, base) Result[i64, string]`,
`int_to_string_radix(n, base) string`.

**Why**: Hex / binary / octal parsing currently needs hand-
rolled loops. Tiny code.

**Inspiration**: Rust `from_str_radix`, Go strconv.

### 15. String fields + ascii-fold + strip_prefix/suffix · small · ☑

**Surface**: `fields(s) string[]` (whitespace split, no
empties), `eq_ignore_ascii_case(a, b) boolean`,
`strip_prefix(s, p) Option[string]`, `strip_suffix(s, p)
Option[string]`.

**Why**: HTTP header parsing wants all four; saves
repetitive `to_lower` allocations.

**Inspiration**: Go `strings.Fields` / `EqualFold`, Rust
`strip_prefix`.

### 16. `Set[T]` · small · ☑

**Surface**: `std/set` — a generic, value-semantic
`Set[T: cmp.Eq]`: `set_new` / `set_of`, `add` / `remove` /
`contains` / `len` / `is_empty` / `to_array`, and the algebra
`union` / `intersect` / `difference` / `is_subset` / `equals`
(order-insensitive).

**Why**: The most-cited missing container (Roc `Set`); dedup /
membership / seen-id working sets are constant in CLI tools.

**Inspiration**: Roc `Set`, Rust `HashSet` API shape.

**Status**: shipped in `internal/stdlib/std/set.fern`, backed by
an insertion-ordered array (membership by `==`). Every mutating
op returns a NEW set and copies the backing array rather than
appending onto the receiver's field in place — that in-place
form passes the interpreter but silently mutates a shared
receiver once compiled (the copy-on-write aliasing hazard), so
the copy is load-bearing, not incidental. Covered by
`examples/tests/set_test.fern` (pure-Fern runner, `add is pure`
being the value-semantics guard) and
`internal/e2e/set_module_test.go` (differential across interp /
x86-64 / wasm / arm64). **Complexity**: linear-scan store, so
`contains` / `add` are O(n) and an n-element build is O(n²) —
right-sized for CLI-scale sets, a trap past ~10⁴ elements. The
O(1)-membership hash-backed version awaits a native codegen fix
for returning a `Map`-wrapping struct from a function (a
value-semantic `Set` re-wraps its map on every `add`); the
public API is designed to carry over unchanged.

### 17. `std/unicode` — Unicode case mapping · small · ☑

**Surface**: `std/unicode` — case mapping (`to_upper(s)` /
`to_lower(s)`, and the `char` methods `c.to_upper()` / `c.to_lower()`,
`eq_ignore_case(a, b)`) and character classes (`is_letter`, `is_digit`,
`is_alnum`, `is_whitespace`, `is_upper`, `is_lower`).

**Why**: closes the July-review "ASCII-only casing" gap —
the byte fold (now named `to_ascii_upper`/`to_ascii_lower`) remaps
only A–Z, so
`café` / `ΑΒΓ` / `привет` were untouched. This maps the full set of
code points with a **full (1→N)** mapping (Latin, Greek, Cyrillic,
Armenian, fullwidth, …), decoding UTF-8 via `std/utf8` and re-encoding.

**Status**: shipped in `internal/stdlib/std/unicode.fern`, the tables
generated by `cmd/unicodegen` from the Go stdlib's `unicode` package —
re-runnable on a toolchain upgrade, deterministic output, and verified
at generation time against `unicode.ToUpper`/`ToLower` and the category
predicates for every code point in 0..MaxRune. **Caveats**: simple
mappings only (multi-code-point expansions like `ß`→`SS` are left
unchanged, matching Go — full mapping is #5630), and not locale-aware.
Covered by `examples/tests/unicode_test.fern`,
`internal/e2e/unicode_case_test.go` (differential across interp /
x86-64 / wasm / arm64), and `cmd/unicodegen/main_test.go`.

**Representation (#5627)**: the tables were once `i32[]` literals —
2900 array-literal stores of *code*, rebuilt on every call. That
measured at **176 KB of binary and 22× the ASCII byte fold** on a
34-byte ASCII string, so "fine at CLI scale" was wrong. They are now
range-coalesced (351 case runs, not 2900 pairs) and emitted as static
**string** literals decoded in place, with an ASCII fast path fronting
every entry point: **27.8 KB and at parity with the byte fold**. When
const/static arrays land as a language feature, the string encoding
should be deleted in favour of them. `docs/STRINGS-SOTA.md` sets the
wider design — Unicode-correct defaults with `ascii`-named fast paths,
a distinct `char` type, and normalization/segmentation — tracked as
epic #5626.

## Extras (not in the original 15)

These landed in the chunks above but weren't on the original
prioritised list — recording them so a future audit doesn't
think they're free additions to make.

- **i32[] math**: `arr.sum()` / `arr.max()` / `arr.min()`.
  Constrained-receiver i32-element dispatch. `max` / `min`
  return `Option[i32]` to handle empty arrays. Inspired by
  Elixir Enum.sum/min/max and Rust slice methods.
- **`s.count(sub)`**: non-overlapping match count. Standalone
  helper that fell out of the `replace_n` work — the inner
  loop is the same as Rust `str::matches().count()`.
- **i32 scalar methods**: `(n).abs()` / `(n).min(other)` /
  `(n).max(other)` / `(n).clamp(lo, hi)`. Standard scalar
  reductions; the abs path widens to i64 internally to handle
  i32::MIN cleanly (matches Rust wrapping_abs).
- **Byte case helpers**: `(b).is_ascii_lower()` / `is_ascii_upper()` /
  `to_ascii_lower()` / `to_ascii_upper()` — ASCII-only. Non-letters pass
  through unchanged from the to_* helpers.
- **String predicates**: `s.is_ascii_only()` /
  `s.is_numeric()` / `s.is_alpha_only()` / `s.is_alnum_only()`.
  Suffix `_only` to avoid shadowing the byte-receiver
  predicates already in the prelude.
- **i64 / u32 / u64 scalar methods**: parallels to the i32
  `abs` / `min` / `max` / `clamp` helpers. i64 abs wraps on
  i64::MIN (no i128 to widen into); u32 / u64 have no abs
  (always non-negative).
- **String byte-level helpers**: `s.at(i)` (bounds-checked
  Option), `s.chars()` (i32[] of byte values), `s.reverse_bytes()`
  (ASCII-only reverse; multibyte UTF-8 will scramble). The
  name `reverse_bytes` carries the warning.
- **Byte classifiers**: `(b).is_ascii_punct()` (Python's string.
  punctuation set), `(b).hex_digit()` (numeric → single-byte
  string).
- **i32 sign helpers**: `(n).signum()` (-1/0/1 trichotomy),
  `is_positive()` / `is_negative()` / `is_zero()`.
- **String predicates v2**: `s.is_blank()` (empty or all
  whitespace), `s.is_hex_string()` (all hex digits, case-
  insensitive).
- **`s.indent(prefix)`**: prepend prefix to every line.
  Bytewise — empty lines get the prefix too. Trailing
  newline doesn't produce an extra prefix at EOF.
- **Parity predicates**: `(n: i32).is_even()` / `is_odd()`
  plus i64 versions. Cheap shortcut; saves writing `n % 2
  == 0` per call site.
- **Integer power / gcd / lcm**: `(n: i32).pow(exp)` (by
  squaring, wraps on overflow; negative exp returns 0),
  `gcd(other)` (Euclidean, sign-agnostic), `lcm(other)`
  (computed as `abs(a*b)/gcd` with the gcd-first ordering
  to keep the intermediate multiply small).
- **Rightmost string search**: `s.last_index_of(needle)`
  (symmetric to index_of). Empty needle returns `len(s)`
  per the Python rfind / Go LastIndex "match every gap"
  convention.
- **`s.capitalize()`**: uppercase the first code point, leave the
  rest unchanged (`to_ascii_capitalize` is the byte-wise twin).
  Different from `to_upper` (which
  folds every letter) and from Python `str.capitalize` (which
  ALSO lowercases the tail — our version preserves the
  tail since the lossy fold is rarely what callers want).
- **i32 bit ops**: `count_ones()`, `leading_zeros()`,
  `trailing_zeros()`, `byte_swap()`. Software implementations
  (no intrinsic surface in Fern yet) — O(width) per call.
- **i64 pow/gcd/lcm**: parity with the i32 versions. `pow`
  takes an i32 exponent.
- **`range(start, end)` / `range_step(start, end, step)`**:
  i32[] generators for half-open ranges. `range(5, 5)` is
  empty; `range_step` with step <= 0 is empty (no reverse
  ranges).
- **`s.repeat_with_sep(n, sep)`**: like `repeat` but with a
  separator between every pair. `n <= 0` returns empty.
- **i32[] product / avg**: `arr.product()` (multiplicative
  identity 1 for empty; wraps on overflow), `arr.avg()`
  (Option[i32] integer mean; truncates).
- **String leading / trailing count**: `s.leading_count(b)` /
  `trailing_count(b)`. Number of leading / trailing bytes
  matching b. Useful for indent detection / column alignment.
- **`s.hash_fnv32()`**: FNV-1a 32-bit hash. Non-cryptographic;
  good for bucket selection / fingerprinting.
- **`s.escape_c()`**: C-style escape — `\\` `\"` `\n` `\t`
  `\r` `\0` get their two-char escape forms. Other bytes
  pass through. Useful for emitting source-ready string
  literals.
- **`repeat_char(ch, n)`**: fresh string of n copies of the
  byte `ch`. Faster than `chr(c).repeat(n)` would be.
- **`http_status_text(code)`**: IANA reason phrase for the
  common HTTP status codes (RFC 9110). `""` for unknown.
- **i32 saturating + checked arithmetic**: `saturating_add` /
  `saturating_sub` (clamp at MAX/MIN), `checked_add` /
  `checked_sub` (Option[i32], None on overflow),
  `checked_div` (None on DBZ or i32::MIN/-1). Overflow
  detection via sign-bit comparison — the natural i64
  widening trick exposed an arm64/x86-64 i64 comparison bug
  across the i32::MAX threshold that this implementation
  sidesteps.
- **`(n: i32) to_hex()`**: sugar for `int_to_string_radix(n,
  16)`. Signed (negatives emit a leading `-`); callers
  wanting the two's-complement bit pattern should widen to
  u32 first.
- **`(s: string) parse_bool()`**: accepts the canonical
  spellings `"true"` / `"false"` / `"1"` / `"0"`. Anything
  else returns None — no implicit case-folding.
- **HTTP method classifiers**: `s.is_http_safe_method()` (GET
  / HEAD / OPTIONS / TRACE per RFC 9110 §9.2.1),
  `s.is_http_idempotent_method()` (safe set + PUT / DELETE).
- **Case-insensitive ASCII search**: `s.contains_ci(needle)` /
  `s.index_of_ci(needle)`. Folds A-Z to a-z per byte;
  multibyte UTF-8 matches only byte-exact.
- **Multi-byte pad**: `s.pad_start_str(w, fill)` /
  `s.pad_end_str(w, fill)`. The earlier `pad_start(n, ch)`
  uses a single byte; these repeat the `fill` string and
  truncate at the boundary. Useful for prefix-dash / line-
  rule decoration.
- **`s.truncate(n, ellipsis)`**: cap to n bytes, suffix with
  ellipsis when the input is longer. If n is shorter than
  the ellipsis itself, hard-truncate without it.
- **`(n: i32).digits()`**: number of decimal digits. Sign
  not counted. `(0).digits() == 1`.
- **`(n: i32).pluralize(singular, plural)`**: choose
  singular when `|n| == 1` else plural. Caller composes
  the count into the result themselves.
- **Range constants**: `i32_max()` / `i32_min()` /
  `i64_max()` / `i64_min()` as function-style accessors
  (Fern has no const declaration syntax yet).
- **One-sided trim**: `s.trim_start()` / `s.trim_end()`.
  Asymmetric whitespace strip.
- **`s.trim_chars(chars)`**: strip any byte in `chars`
  from both ends. Useful for unwrapping `"(x)"`, `"=x="`,
  etc. in one pass.
- **Case-insensitive prefix/suffix**: `s.starts_with_ci(p)`
  / `s.ends_with_ci(s)`. Pairs with the `_ci` search
  variants from bundle 9.
- **String sort**: `sort_strings_asc(arr)` /
  `sort_strings_desc(arr)`. Insertion-sort, like the i32
  variants. Backed by a new `string_cmp(a, b)` three-way
  comparator since Fern's `<` / `>` operators are
  numerics-only.
- **String splitn**: `s.splitn(sep, n)` caps the result at
  n pieces; the last piece carries the unsplit tail. Useful
  for "first token, rest" parsing (HTTP request lines,
  URL scheme separation, header `key: value`).
- **String first / last**: `s.first()` / `s.last()` return
  Option[i32] of the byte. None on empty. Saves the
  off-by-one of `s.at(len(s) - 1)`.
- **String take / drop**: `s.take(n)` / `s.drop(n)` with
  bounds clamping (out-of-range counts saturate to 0 /
  len). Saves the `s[0:min(n, len(s))]` boilerplate.
- **String chunks**: `s.chunks(size)` splits into consecutive
  size-byte pieces; last piece may be short. size <= 0
  returns `[s]`. Useful for base64 line-wrapping, hex-dump
  formatting.
- **Case-insensitive string ops**: `string_cmp_ci(a, b)` /
  `sort_strings_asc_ci(arr)`. Per-byte fold on the
  comparison.
- **i32 radix sugar**: `(n).to_binary()` / `(n).to_oct()`.
- **i32 bit accessors**: `(n).bit(i)` / `set_bit(i)` /
  `clear_bit(i)` / `toggle_bit(i)`. Out-of-range i is a
  no-op for the mutators and false for the reader.
- **`(b: i32).is_ascii_newline()`**: true for LF or CR. Companion
  to the existing is_ascii_white_space.
- **`(s: string).count_lines()`**: count newline-separated
  lines; a trailing newline doesn't add a phantom empty
  line.
- **HTTP response builders**: `http_response_ok(body)`,
  `http_response_text(status, body)`,
  `http_response_not_found()`. Saves the
  `HttpResponse { status: 200, body: ... }` boilerplate.
- **Log helpers**: `log_info(msg)` / `log_warn(msg)` /
  `log_error(msg)`. Thin wrappers around `eprint` with a
  `[LEVEL]` prefix.
- **String array hygiene**: `arr.filter_non_empty()` /
  `arr.count_non_empty()`. Useful after `split(sep)` when
  adjacent separators left empty pieces.
- **`s.word_count()`**: whitespace-separated word count.
  Empty / all-whitespace input returns 0.
- **`s.escape_html()`**: escape the five HTML / XML
  metacharacters (& < > " ').
- **`s.strip_quotes()`**: `Option[string]`, returns inner
  if the string starts AND ends with matching `"` or `'`.
- **`(n: i32) to_string_padded(width)`**: decimal with
  zero-pad. Negatives pad the body and re-prefix `-`
  (`-0042` not `0-042`).
- **`trim_start_chars` / `trim_end_chars`**: one-sided
  trim_chars companions.
- **`random_int(lo, hi)`**: CSPRNG-backed random in [lo,
  hi). Draws a full 32-bit value (4 bytes) and modulo-maps to
  the range width, so the WHOLE requested range is reachable
  (previously a 24-bit draw silently truncated any range >
  2^24 to the low ~16M). The 24-bit cap had been a workaround
  for the natives' signed-u32-modulo codegen bug; that bug is
  fixed (full `u32 % range` is differential-verified across
  interp / x86-64 / wasm), so the draw was widened. Residual
  modulo bias for `range << 2^32` is negligible; rejection
  sampling to remove it entirely is a follow-up.
- **`format_bytes(n)`**: human-readable size — "N B" / "N
  KiB" / "N MiB" / "N GiB". i32 input (caps at ~2 GiB
  representable range).
- **`csv_escape(s)` / `csv_join(arr)`**: RFC 4180 CSV
  field escape + comma-join. Fields with `,` / `"` / `\n`
  / `\r` get wrapped in `"..."` with interior quotes
  doubled.
- **String-array dedup**: `arr.distinct()` /
  `arr.distinct_count()`. First-occurrence-wins, order-
  preserving. O(n²) — fine for small lists.
- **Power-of-2 helpers**: `(n).is_power_of_2()` /
  `(n).next_power_of_2()`. Matches Rust's
  `is_power_of_two` / `next_power_of_two`. Zero returns
  false for is, 1 for next.
- **`(b: i32) to_ascii_string()`**: byte → single-char
  string. Out-of-range returns `""`.
- **`s.hash_djb2()`**: Bernstein's djb2 hash. Alternate
  non-crypto hash to FNV-1a; sometimes a better mix for
  short keys.
- **`http_path_segments(path)`**: split an HTTP path into
  non-empty components. Strips query string and collapses
  duplicate slashes. Useful for simple routing.
- **`s.center(width, ch)`**: equal padding on both sides;
  odd-padding splits leave the extra on the right.
- **`s.reverse_words()`**: split on whitespace, reverse,
  join with single space.
- **i32 bit rotation**: `(n).rotate_left(bits)` /
  `rotate_right(bits)`. Bit count masked mod 32 so OOB
  values still produce a valid rotation.
- **`csv_parse_line(s)`**: RFC 4180 single-line parser.
  Handles quoted fields with embedded commas and doubled-
  quote escapes. Multi-line CSV (newlines inside quoted
  fields) deferred — needs a streaming parser.
- **`http_header_value(headers, key)`**: case-insensitive
  header lookup against a CRLF-separated header block.
  Returns the first matching value, trimmed.
- **String-array length analytics**: `arr.max_by_len()`
  (Option[string]) and `arr.sum_lens()` (i32).
- **`(n: i32).log2_floor()`**: floor(log2(n)) for n >= 1;
  -1 sentinel for n <= 0.
- **`(n: i32).sqrt_floor()`**: integer square root via
  Newton's method. 0 fallback for n <= 0.
- **`(n: i32).to_rgb_hex()`**: render the low 24 bits as
  `"#RRGGBB"`. CSS / SVG / terminal-ANSI emission.
- **`(b: i32).is_ascii_vowel()`**: ASCII a/e/i/o/u in either case.
  Y not counted.
- **`s.rstrip_newline()`**: strip a single trailing `\n` or
  `\r\n` — preserves runs.
- **i32 rounding**: `(n).ceil_div(d)` / `(n).round_up_to(m)`
  / `(n).round_down_to(m)`. Useful for memory alignment,
  page rounding, table-column math. d/m <= 0 are no-ops.
- **String prefix/suffix removal**: `s.remove_prefix(p)` /
  `s.remove_suffix(s)`. Like strip_* but return s unchanged
  on no-match instead of `Option[None]`.
- **`s.is_uuid()`**: shape check for canonical 8-4-4-4-12
  hex UUID. Doesn't validate version / variant nibbles.
- **`format_duration_ms(ms)`**: human-readable durations
  like `"1h 23m 45s"` / `"500ms"`. Components only emitted
  when non-zero; `0` returns `"0ms"`.
- **Byte digit/hex values**: `(b).digit_value()` / `(b).hex_value()`.
  Return -1 on non-digits. Useful for hand-rolled parsers.
- **`s.count_byte(b)`**: single-byte fast path on count.
- **`http_url_path_only(path)`**: strip the `?query`
  suffix.
- **`http_user_agent_is_bot(ua)`**: heuristic check for
  common bot tokens (bot / crawler / spider / slurp,
  case-insensitive). Hint, not a security control.
- **`(n: i32).to_string_with_sep(sep)`**: decimal with
  thousand-separator. `1234567` → `"1,234,567"`.
- **`(n: i32).divmod(d)`**: returns `(quotient, remainder)`
  pair. `d == 0` returns `(0, 0)`.
- **`s.escape_shell()`**: POSIX-shell-safe single-quote
  wrap with `'\''` escape dance for interior quotes.
- **`s.snake_case()` / `s.kebab_case()`**: convert
  camelCase / PascalCase / space-separated to lower-case
  with underscore / hyphen separators.
- **`s.is_valid_identifier()`**: Fern / C / JS identifier
  pattern: `[a-zA-Z_][a-zA-Z0-9_]*`.
- **`is_valid_http_status(code)`**: `[100, 599]` per RFC
  9110.
- **String numeric predicates**: `s.is_int()` /
  `s.is_float()`. Don't validate i32 overflow — use the
  parse variants for that.
- **`s.wrap(prefix, suffix)`**: thin concat helper.
- **String[] take / drop**: bounds-clamped prefix /
  suffix selection on string arrays.
- **`pack_rgb(r, g, b)`**: pack three 0..255 components
  into a 24-bit i32. Pairs with `to_rgb_hex` for round-trip.
- **Byte printability**: `(b).is_ascii_printable()` (32..126),
  `(b).is_ascii_control()` (0..31 or 127).
- **Radix parse sugar**: `s.parse_hex_int()` /
  `s.parse_bin_int()`. Companion to the generic
  `parse_int_radix`.
- **`(n: i32).is_in_range(lo, hi)`**: half-open bucket check.
- **`(b: i32).matches_any(bytes)`**: scan check; saves the
  `b == x || b == y || ...` chain.
- **`(n: i32).reverse_digits()`**: 1234 → 4321 with sign
  preserved.
- **`(n: i32).is_palindrome()`**: decimal-palindrome check.
- **`(s: string).to_array()`**: string[] of single-byte
  strings — companion to `chars()` with string semantics.
- **`s.remove_all(needle)`**: sugar for `replace(needle, "")`.
- **`s.before(sep)` / `s.after(sep)`**: substring around the
  FIRST `sep`. before returns s on no-match; after returns
  empty.
- **`s.between(start, end)`**: Some(content) between matched
  markers; None if either is missing.
- **`(n: i32).is_between(lo, hi)`**: inclusive companion to
  `is_in_range` (which is half-open).
- **`(b: i32).is_ascii_letter()`**: alias for `is_ascii_alpha`. Roc /
  MoonBit naming.
- **`(arr: string[]).all_non_empty()`**: vacuously true on
  empty array; false on any empty entry.

### Additional compiler bug — FIXED

- **arm64 + x86-64: u32 modulo path uses signed arithmetic
  when the dividend has the high bit set.** ~~Reproduces
  with `(255 as u32) << (24 as u32) % (100 as u32)` — the
  natives return 240 (the result of `-16777216 % 100 = -16`,
  cast back to u32 → 0xFFFFFFF0 → low byte 240), interp
  + wasm return 80 (the correct unsigned mod).~~ **Fixed** —
  `((255 as u32) << 24) % 100` now returns 80 on all four
  backends (differential-verified). The `random_int` 24-bit
  workaround has accordingly been removed (it now draws a
  full 32 bits, so ranges > 2^24 are no longer truncated).

## Known compiler bugs surfaced during this work

- **Generic prelude functions break unrelated `i8`-literal
  inference at monomorph re-check time.** (Moot since #4408
  retired `i8`/`i16`/`u16` — kept as historical record; the
  underlying re-check inference bug this surfaced may still
  apply to other sub-i32 polymorphic literals, i.e. `u8`.)
  Adding any `function foo[T](...)` to the prelude trips the
  `TestWASMSubI32Widths` family — the `-7` in
  `var s: i8 = -7;` re-fails as "operator '-' requires
  i32, got i8" during the post-monomorph type re-check.
  Root cause not pinned down yet; the re-check inference
  seems to settle polymorphic numeric literals differently
  when the prelude contains generic decls. Concrete
  workaround: keep prelude generic methods limited to the
  IR-intercepted shape (push) or to per-element-type concrete
  declarations (join / index_of / contains / reverse / sum /
  max / min) until the inference bug is fixed.
- **Method-dispatch path for generic Array methods doesn't
  re-use stamped `n.TypeArgs` for inference**: when
  `arr.is_empty()` rewrites to `__method_Array_is_empty(arr)`
  and the dispatch stamps TypeArgs from the receiver, the
  subsequent generic-inference pass at checker.go:3146 still
  tries to infer T from arg types — but `expected` is now
  already-substituted (concrete), so no ParamType binds and
  inference fails. Fix shape: seed `sub` from `n.TypeArgs`
  when they're already set. Documented here so the next
  attempt at generic Array methods starts from this context.
  - **Related gap — FIXED.** A *generic user function* that
    called an Array method on a `T[]` receiver
    (`function map[T,U](xs: T[], f) { … out = out.push(f(x)); … }`)
    failed the post-monomorph re-check with "expected `T[]`, got
    `i32[]`". Root cause was in `internal/monomorph` rather than
    the checker: `substituteExpr` had no `*ast.Assign` case, so the
    `push` call buried in `out = out.push(x)` (typically inside a
    `for-in` loop body) was never walked, and its stamped
    `TypeArgs` stayed `[T]` instead of being substituted to the
    concrete instantiation. The method-signature substitution then
    ran `T→T` (a no-op) and left the parameter type abstract. Fixed
    by walking `*ast.Assign` / `*ast.FString` / `*ast.MapLit` /
    `*ast.EnumLit` in `substituteExpr`, plus `*ast.Defer` /
    `*ast.Switch` / `For.Step` in `substituteStmt` (every remaining
    type-bearing node the substitution walker had been skipping).
    This **unblocks STDLIB-ROADMAP item #1 (generic array
    combinators)** — `map` / `filter` / `fold` over `T[]` now
    compile + run on interp, x86-64, and wasm. Guarded by
    `TestRunSubstitutesMethodCallTypeArgsInGenericBody`
    (`internal/monomorph`) and `TestGenericArrayCombinators`
    (`internal/e2e`).
- **arm64 / x86-64 i64 comparison across the i32::MAX
  boundary returns wrong result**. The expression
  `(la + lb) > 2147483647 as i64` where `la = 2147483647 as
  i64` and `lb = 1 as i64` should evaluate to `true` (since
  la + lb = 2147483648 > 2147483647) but the natives return
  `false`. Interp + wasm get it right. The native saturating
  / checked arithmetic prelude additions sidestep this by
  using sign-bit overflow detection on plain i32 ops rather
  than the natural i64-widening approach.
- **arm64 / x86-64 u32 modulo uses signed arithmetic when
  the dividend has the high bit set**. ~~`(255 as u32) << (24
  as u32) % (100 as u32)` returns 240 on the natives
  (`-16777216 % 100 = -16`, cast to unsigned exit code →
  240) but 80 on interp + wasm.~~ **FIXED** — now returns 80
  on all four backends; the `random_int` 24-bit workaround
  was removed (it draws a full 32 bits again).
- **arm64 `len(stringTuple.field)` segfaults.** Calling
  `len()` directly on a `(string, string)` tuple-element
  access (e.g. `len(p.0)` where `p: (string, string)`)
  crashes the arm64 backend with SIGSEGV. The string-header
  load appears to fold incorrectly when the receiver is a
  tuple-field expression rather than a plain identifier or
  struct-field access. interp and x86-64 both handle it.
  Workaround: bind the field to a `var s: string = p.0;`
  local first, then call `len(s)`. Applied in prelude's
  `is_email_like`. Likely a missing pointer-deref step in
  the load path for tuple-element string values on arm64;
  worth tracking down — same shape probably affects other
  string methods called on tuple-field accesses.

## Cross-cutting decisions

- **Naming**: drop type-suffixed names (`sort_i32_asc`,
  `int_to_string`) once generics + `Ord` traits are in.
  Migration story: keep old names as aliases for one
  release.
- **Pipe ergonomics**: several of these (string transforms,
  array combinators, Result chains) become unpleasant
  without a pipe operator or UFCS. Worth designing #1, #2,
  #3 with the syntax question in mind — MoonBit and Gleam
  both show how much pipe + method-call sugar carries the
  stdlib's weight.
- **Eager vs lazy iterators**: start eager (return arrays)
  for simplicity; revisit a lazy `Iter[T]` (MoonBit / Rust
  style) only once allocation pressure shows up in profiles.
  Arena allocation makes eager cheaper here than in most
  languages.
- **Don't ship**: process spawn, threads, async, file
  watching. Out of scope per the language's sync edge-
  function focus.

## Sequencing

Recommended execution order. Group small items into single
PRs where they share a surface area (e.g. items 8 + 14 + 15
are all "small string/integer additions" and could ship in
one PR).

1. **Quick wins, no design**: 7 (Map convenience), 8 (paths),
   9 (stdin/println), 12 (slice/chunks/take/drop — needs #1
   first), 14 (radix), 15 (fields/strip/eq_ignore_case).
2. **Foundational expressivity**: 1 (array combinators).
   Everything iterator-shaped builds on this.
3. **Error ergonomics**: 2 (Result/Option combinators + `?`).
   Touches parser, checker, and prelude.
4. **Formatting + concat**: 4 (format specs), 5
   (StringBuilder). Format specs probably want the builder
   under the hood.
5. **Text correctness**: 6 (UTF-8 / codepoints).
6. **Polish**: 3 (string surface), 10 (generic sort), 13
   (typed errors), 11 (time).

The biggest unlocks for the stated CLI + edge-handler use
cases are 1, 2, 4, 5, and 11. Those five alone close most of
the gap to what an idiomatic Go or Rust edge handler looks
like today.
