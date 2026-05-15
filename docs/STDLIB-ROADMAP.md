# Stdlib roadmap

Captures a survey of seven reference languages — Roc, Rust,
MoonBit, Gleam, Elixir, Go, Zig — and proposes a prioritised
list of stdlib additions for lang. Companion to
`ROADMAP-AND-SELF-HOSTING.md` and `LANGUAGE-DIRECTION.md`.

Lang's target use cases are small fast-startup CLI tools and
short-lived edge-function-style HTTP servers. The picks below
favour those over general-purpose breadth.

## Current state

What lang's prelude / built-ins cover today, as a baseline:

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
  `read_line`, `read_chunk`, `exit`, `arena_save/restore`,
  `env`, `args`, `random_bytes`.
- **net**: `tcp_listen / accept / recv / send / close`,
  `http_parse_request`, `http_serialize_response`, `tcp_serve`.
- **misc**: `format(fmt, args)`, f-string literals.

## Per-language highlights (gaps lang doesn't have)

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
`Set` type, `Str.replaceEach` (vs lang's single replace).

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

### 1. Generic array combinators · medium · ☐

**Surface**: `map[T, U](xs: T[], f: fn(T) U) U[]`, `filter`,
`fold[T, A](xs, init: A, f: fn(A, T) A)`, `any`, `all`,
`find` (returning `Option[T]`), `enumerate` (returning
`(i32, T)[]`).

**Why**: Single biggest expressivity win; everything else
builds on these. Today `for` + `push` is the only path.

**Inspiration**: Rust Iter, Elixir Enum, Gleam list.

**Notes**: Eager (returns arrays) is fine for v1; arena
allocation makes copying cheap. Needs the generic-fn-over-`T[]`
machinery (push already exercises it).

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

### 7. Map convenience methods · small · ☐

**Surface**: `Map.update(k, default, fn(V) V)`,
`Map.get_or_insert(k, default)`, `Map.merge(other)`,
`Map.entries()` → `(K, V)[]`, `Map.from_entries(...)`.

**Why**: The single insert-or-modify pattern (counters,
group_by) currently takes 4 lines.

**Inspiration**: Elixir Map, Gleam dict, Rust entry API.

### 8. Path manipulation (string-level) · small · ☑ (partial)

**Surface**: `path_join(parts: string[])`, `path_parent(p)`,
`path_file_name(p)`, `path_extension(p)`, `path_clean(p)`.

**Why**: Every CLI tool needs these; pure string ops, no
FS interaction required.

**Inspiration**: Go `path/filepath`, Zig `fs.path`.

**Status**: `path_join` / `path_parent` / `path_file_name` /
`path_extension` shipped. `path_clean` (resolving `..`, `.`,
duplicate `/`) deferred — more complex semantics; punt to a
follow-up if real demand surfaces.

### 9. stdin + println + io.Copy · small · ☑ (partial)

**Surface**: `read_all_stdin() string`, `print(s)` /
`println(s)`, `eprintln(s)`, `copy(reader, writer) i64`.

**Why**: `read_file` + `write_file` exist but no stdin
equivalent; `println` is more discoverable than `write` to
fd 1.

**Inspiration**: Go `io.ReadAll`, Elixir `IO.puts`.

**Status**: `read_all_stdin()` shipped. `print` / `eprint`
already exist (lang's `print` is the println variant — appends
a newline). `copy(reader, writer)` deferred — needs a real
Reader/Writer plumbing decision.

### 10. Generic `sort_by(cmp)` + `sort_key(fn)` · medium · ☐

**Surface**: `sort_by[T](xs: T[], cmp: fn(T, T) i32)`,
`sort_key[T, K](xs, fn(T) K)` where `K` is `Ord`.

**Why**: Current sort is `i32`-only; struct-array sort is
the common case.

**Inspiration**: Go `sort.Slice`, Rust `sort_by`.

**Notes**: Needs generic comparator dispatch; later
deprecates `sort_i32_asc/desc` once `Ord` traits land.

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
- **Byte case helpers**: `(b).is_lower()` / `is_upper()` /
  `to_lower()` / `to_upper()` — ASCII-only. Non-letters pass
  through unchanged from the to_* helpers.
- **String predicates**: `s.is_ascii_only()` /
  `s.is_numeric()` / `s.is_alpha_only()` / `s.is_alnum_only()`.
  Suffix `_only` to avoid shadowing the byte-receiver
  predicates already in the prelude.

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
