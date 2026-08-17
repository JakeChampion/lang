# Standard library

The Fern stdlib lives in two namespaces:

- **`std/…`** — high-level helpers user code reaches for directly.
  Receiver methods (`(5).abs()`, `"hello".split(",")`, `arr.sum()`)
  resolve here.
- **`core/…`** — low-level primitives the `std/…` modules build on
  top of. Raw-memory routines (allocator probes, scratch buffers
  written backwards, `__memcpy` plumbing) live here. User code
  normally shouldn't reach for these.

The magic auto-injected prelude is gone (Phase 5 of
[`docs/PRELUDE-TO-MODULES.md`](./PRELUDE-TO-MODULES.md)) — a
program sees only the modules it declares via `import "std/…";` /
`import "core/…";` lines.

## `std/`

### `std/i32`

Receiver methods on i32 / byte values.

- **Byte classifiers (`b: i32` receiver):**
  `is_ascii_digit`, `is_ascii_alpha`, `is_ascii_alnum`, `is_ascii`,
  `is_ascii_white_space`, `is_ascii_newline`, `is_ascii_vowel`,
  `is_ascii_printable`, `is_ascii_control`, `is_ascii_letter`,
  `is_ascii_hex_digit`, `is_ascii_punct`, `is_ascii_lower`,
  `is_ascii_upper`, `matches_any`,
  `hex_digit`, `digit_value`, `hex_value`, `to_ascii_lower`,
  `to_ascii_upper`, `to_ascii_string`
- **Sign / classification:** `signum`, `is_positive`, `is_negative`,
  `is_zero`, `is_in_range`, `is_between`, `is_multiple_of`,
  `is_perfect_square`, `is_palindrome`, `is_even`, `is_odd`,
  `is_power_of_2`, `is_prime`
- **Scalar:** `abs`, `abs_diff` (`|n - other|`),
  `midpoint(other)` (overflow-safe average via `(a&b)+((a^b)>>1)`,
  rounds toward −∞), `min`, `max`,
  `clamp`, `min_zero`, `sign_str`,
  `percent_of`, `reverse_digits`, `sum_of_digits`, `has_digit`,
  `saturating_add`, `saturating_sub`, `checked_add`,
  `checked_sub`, `checked_div`, `pow`, `gcd`, `lcm`, `factorial`,
  `next_power_of_2`, `log2_floor`, `sqrt_floor`, `ceil_div`,
  `round_up_to`, `round_down_to`, `divmod`
- **Bit ops:** `count_ones`, `count_zeros` (`32 - count_ones`),
  `leading_zeros`, `trailing_zeros`, `bit_length` (bits to
  represent `|n|`, highest set bit + 1),
  `bit`, `set_bit`, `clear_bit`, `toggle_bit`, `byte_swap`,
  `rotate_left`, `rotate_right`
- **String formatting:** `to_string`, `to_string_padded`,
  `to_string_with_sep`, `to_hex`, `to_binary`, `to_oct`,
  `to_string_radix(base)` (arbitrary base 2–36, the general form
  behind the others; write-side inverse of `string.parse_int_radix`),
  `to_rgb_hex`, `digits`, `pluralize`, `ordinal` (English ordinal,
  `1`→`"1st"`/`2`→`"2nd"`/`11`→`"11th"`, with the 11/12/13 exception)

### `std/i64`

- **Scalar:** `abs`, `abs_diff` (`|n - other|`),
  `midpoint(other)` (overflow-safe average, i64 sibling of the
  i32 one), `min`, `max`, `clamp`, `pow`, `gcd`, `lcm`
- **Roots / powers:** `sqrt_floor` (floor of √n via Newton, exact
  into the i64 range), `is_power_of_2` (`n & (n-1)` bit trick),
  `is_perfect_square` (`sqrt_floor(n)² == n`), `next_power_of_2`
  (smallest power of two `>= n`; caps at 2^62, returns 0 above),
  `log2_floor` (`floor(log2 n)`, `-1` for `n <= 0`)
- **Integer division:** `is_multiple_of(d)` (`d == 0` → false),
  `ceil_div(d)` (round toward +∞; `d <= 0` → 0)
- **Parity:** `is_even`, `is_odd`
- **Sign:** `signum` (-1/0/1), `is_positive`, `is_negative`, `is_zero`
- **Range:** `is_in_range` (half-open `[lo, hi)`), `is_between`
  (inclusive `[lo, hi]`)
- **Bit ops:** `count_ones` (set bits in the 64-bit two's-complement
  rep), `bit_length` (bits to represent `|n|`, i64::MIN → 64)
- **Overflow-aware:** `saturating_add`/`saturating_sub` (clamp to
  i64::MAX/MIN), `checked_add`/`checked_sub` (`Option[i64]`, `None` on
  overflow)
- **String:** `to_string`, `to_string_radix(base)` (arbitrary base
  2–36; renders i64::MIN cleanly via a u64 magnitude)

### `std/u32`

- **Scalar:** `min`, `max`, `clamp`, `pow`, `abs_diff` (`|n - other|`,
  always fits — unsigned), `midpoint(other)` (overflow-safe average
  via `(a&b)+((a^b)>>1)`)
- **Roots / powers:** `sqrt_floor` (floor of √n via Newton, exact to
  `(2^16-1)²`), `is_power_of_2` (`n & (n-1)` bit trick), `next_power_of_2`
  (smallest power `>= n`; caps at 2^31, returns 0 above), `log2_floor`
  (`floor(log2 n)`, `-1` for `n == 0`)
- **Predicates:** `is_zero`, `is_even`, `is_odd`
- **Range (unsigned):** `is_in_range` (half-open `[lo, hi)`),
  `is_between` (inclusive `[lo, hi]`)
- **Overflow-aware (unsigned):** `saturating_add`/`saturating_sub`
  (clamp to u32::MAX / 0), `checked_add`/`checked_sub` (`Option[u32]`,
  `None` on overflow/underflow)
- **String:** `to_string`

### `std/u64`

- **Scalar:** `min`, `max`, `clamp`, `pow`, `abs_diff` (`|n - other|`,
  always fits — unsigned), `midpoint(other)` (overflow-safe average,
  u64 sibling of the u32 one)
- **Roots / powers:** `sqrt_floor` (floor of √n via Newton, exact to
  `(2^32-1)²`), `is_power_of_2` (`n & (n-1)` bit trick), `next_power_of_2`
  (smallest power `>= n`; caps at 2^63, returns 0 above), `log2_floor`
  (`floor(log2 n)`, `-1` for `n == 0`)
- **Predicates:** `is_zero`, `is_even`, `is_odd`
- **Range (unsigned):** `is_in_range` (half-open `[lo, hi)`),
  `is_between` (inclusive `[lo, hi]`)
- **Overflow-aware (unsigned):** `saturating_add`/`saturating_sub`
  (clamp to u64::MAX / 0), `checked_add`/`checked_sub` (`Option[u64]`,
  `None` on overflow/underflow)
- **String:** `to_string`

### `std/float`

- **String:** `(n: f32) to_string()`, `(n: f64) to_string()` —
  shortest round-trip decimal (Dragonbox): the fewest digits
  that parse back to exactly the same float, correctly rounded,
  matching Go's `strconv` shortest digit for digit. NaN / ±Inf
  handled. `(n) to_string_prec(prec)` — fixed `prec`
  fractional digits (no trimming), rounded half away from zero.
- **Math primitives** (on both f32 and f64; f32 wrappers
  promote to f64, apply, demote): `abs`, `floor`, `ceil`,
  `round`, `round_to(digits)` (round to N decimal places, half
  away from zero; negative `digits` round to tens/hundreds),
  `trunc`, `fract` (signed fractional part
  `x - trunc(x)`), `sqrt`, `cbrt` (real cube root, defined for
  negatives), `pow(y)`, `hypot(y)` (2-D Euclidean length,
  overflow-safe), `hypot3(y, z)` (3-D Euclidean length,
  overflow-safe), `log` (natural), `log2` / `log10`
  (base-2 / base-10, via change-of-base ÷ ln2 / ÷ ln10),
  `exp`, `exp2` / `exp10` (base-2 / base-10 exponentials,
  inverses of `log2` / `log10`, via `e^(x·ln2)` / `e^(x·ln10)`),
  `sin`, `cos`, `tan`, `sinh` / `cosh` / `tanh` (hyperbolic,
  built on `exp`; `tanh` saturates to `±1` past `|x| = 20`).
  Routed through the checker-injected
  `__<op>_f64` builtins so every backend can use its
  hardware-precise op.
- **IEEE-754 classification:** `is_nan`, `is_finite`, `is_inf`
- **Combinators:** `min(y)`, `max(y)`, `clamp(lo, hi)` — NaN
  propagates (any NaN input → NaN output), matching Go's
  `math.Min` / `math.Max` semantics; `clamp01` (restrict to
  `[0, 1]`), `abs_diff(b)` (`|a - b|`), `mul_add(b, c)`
  (`a*b + c`; not a hardware-fused FMA — the multiply rounds first)
- **Convenience:** `signum` (`±1.0`, `0.0` at zero, NaN-preserving),
  `lerp(b, t)` (precise `a + (b - a) * t` linear interpolation;
  `t` outside `[0, 1]` extrapolates), `recip` (`1 / x`),
  `copysign(sign)` (magnitude of the receiver, sign of the argument;
  `sign < 0` test, so `-0.0` reads positive), `midpoint(b)`
  (overflow-safe halfway point `a*0.5 + b*0.5`), `to_radians` /
  `to_degrees` (degree↔radian conversion via a high-precision π)

### `std/string`

Receiver methods on strings — the biggest module (~120 helpers).
Includes the byte-level free function `__is_ascii_ws` used by
`trim` / `fields` / `is_blank` and by `std/i32`'s
`is_ascii_white_space`.

Grouped by family:

- **Bytes:** `s.as_bytes(): [u8]` is a non-owning **view** over the
  string's bytes — no allocation, no copy — and works on a `str`
  receiver too, where it is a reinterpretation rather than a copy.
  Indexing is bounds-checked and traps like any array access. Reach for
  it whenever you only need to *read* bytes. `s.bytes(): u8[]` is the
  **copying** constructor, for when you want owned, mutable bytes.
  Caveat: a view borrows, so it must not outlive the string it aliases —
  keep it in a scope where the owner is live. (The escape rule is the
  same open question as `str`'s, #4814.)
- **Length / shape:** `is_empty`, `to_string`, `repeat`
- **Substring search:** `starts_with`, `ends_with`, `contains`,
  `index_of`, `last_index_of`, `starts_with_ci`,
  `ends_with_ci`, `contains_ci`, `index_of_ci`,
  `starts_with_any`, `ends_with_any`. The `index_of` /
  `last_index_of` / `index_of_ci` family reports "not found"
  with the `-1` sentinel; prefer the `Option`-returning
  companions `find`, `rfind`, `find_ci` (which return
  `None` instead) so a forgotten `< 0` check can't read a
  bogus index — consistent with `split_once` / `strip_prefix`.
- **Casing / transform:** Unicode case mapping (full 1→N) with an ASCII
  fast path inside — `to_lower`, `to_upper`, `capitalize` (first code
  point), `title_case` (first code point of each whitespace-separated
  word), `swap_case` (à la Python `str.swapcase`). Each has a
  byte-wise twin for known-ASCII input, which is also what you want
  for bytes that may not be UTF-8: `to_ascii_lower`,
  `to_ascii_upper`, `to_ascii_capitalize`, `to_ascii_title_case`,
  `to_ascii_swap_case`.
  Plus `snake_case`, `kebab_case`, `to_acronym`,
  `word_count`, `slugify` (free-form text →
  URL slug: lowercased, non-`[a-z0-9]` runs collapsed to `-`, ends
  trimmed — distinct from `kebab_case`, which only folds camelCase)
- **Caseless comparison:** `eq_ignore_case` (full Unicode case
  folding — `"ß"` equals `"ss"`), `case_fold` (the folded form
  itself, for comparison not display), and `eq_ignore_ascii_case`
  (byte fold, allocation-free, the right choice for protocol tokens
  where the fold is ASCII by spec)
- **Escape / encode:** `escape_html`, `escape_c`, `escape_shell`
- **Strip / trim:** `strip_quotes`, `strip_prefix`,
  `strip_suffix`, `remove_prefix`, `remove_suffix`, `trim`,
  `trim_start`, `trim_end`, `trim_chars`, `trim_start_chars`,
  `trim_end_chars`, `trim_start_matches`, `trim_end_matches`,
  `rstrip_newline`
- **Hashing:** `hash_fnv32`, `hash_djb2`
- **Character-class predicates:** `is_numeric`, `is_alpha_only`,
  `is_alnum_only` (Unicode — every code point in the class), with
  `is_ascii_numeric` / `is_ascii_alpha_only` / `is_ascii_alnum_only`
  as the byte-wise twins. `is_ascii_only` asks whether the string is
  confined to ASCII and so has no Unicode counterpart.
- **Predicates:** `is_valid_identifier`, `is_ipv4`,
  `is_email_like`, `is_url_like`, `is_json_like`,
  `is_kebab_case`, `is_snake_case`, `is_quoted`,
  `is_ascii_only`, `is_numeric`, `is_alpha_only`,
  `is_alnum_only`, `is_int`, `is_float`, `is_blank`,
  `is_hex_string`, `is_uuid`, `is_http_safe_method`,
  `is_http_idempotent_method`
- **Comparison:** `common_prefix`, `common_suffix`
- **Words / lines:** `word_at`, `word_count_min`,
  `longest_word`, `lines`, `lines_non_empty`,
  `count_lines`, `fields`, `reverse_words`
- **Replace:** `replace`, `replace_n`, `replace_byte`,
  `replace_first`, `remove_all`, `shift_byte`
- **Char-set ops:** `without_chars`, `contains_only`,
  `count_chars_in`
- **Split / pad / center:** `split`, `splitn`, `split_at`,
  `split_once`, `rsplit_once` (split at the LAST separator — the
  mirror of `split_once`), `partition`/`rpartition` (Python-style
  three-way `(head, sep, tail)` that KEEPS the separator; first /
  last occurrence), `pad_start`, `pad_end`, `pad_start_str`,
  `pad_end_str`, `zfill` (zero-pad a numeric string keeping the sign
  in front, à la Python `str.zfill`), `center`, `wrap`, `indent`,
  `dedent` (strip the
  common leading-whitespace prefix — the inverse of `indent`, à la
  Python `textwrap.dedent`), `repeat_with_sep`
- **Slice / count / reverse:** `take`, `drop`, `chunks`, `at`,
  `chars`, `to_array`, `reverse_bytes`, `count`, `count_byte`,
  `find_all` (start indices of every non-overlapping occurrence,
  `len` == `count`), `bytes`, `first`, `last`, `before`, `after`,
  `between`, `truncate`, `ellipsis`, `first_line`
- **Parse:** `parse_bool`, `parse_int`, `parse_hex_int`,
  `parse_bin_int`, `parse_int_radix(base)` (arbitrary base 2–36,
  the general form behind the others), `parse_float` — correctly
  rounded (ties to even), bit-exact with Go's `strconv.ParseFloat`
  for any number of digits, via Eisel-Lemire with an exact
  big-integer fallback
- **Build:** `repeat_char`

### `std/array`

Receiver methods on arrays. `Array.push` stays a built-in IR primitive
(intercepted by codegen) and is registered by the checker.

Two spellings reach the same `arr.<name>(…)` dispatch, and which one a verb
uses is now a statement about the verb rather than about the compiler.
Anything usable at more than one element type is written with a real
element-polymorphic receiver, `pub function (xs: T[]) name(…)`. The older
`__method_Array_<name>(arr: i32[], …)` form, auto-discovered from the naming
convention, pins a concrete element type and is reserved for verbs that are
genuinely specific to it. The namespace keys on the method NAME, so the two
forms cannot both claim one name.

- **Element-polymorphic reductions** (one bounded generic each, so the same
  call works for i32 / i64 / u32 / u64 / f32 / f64 / string and
  `@derive(Ord)` / `@derive(Eq)` element types alike):
  `sum` (`Add + Zero`) / `product` (`Mul + One`),
  `max` / `min` (`Ord` → `Option[T]`, `None`
  when empty), `sorted_asc` / `sorted_desc` (`Ord`, fresh array, input
  untouched), `count(target)` / `first_index_of(target)` (`Eq`; the latter
  is the `Option[i32]` companion to `index_of`'s `-1` sentinel),
  `distinct` (`Eq`), and the structural `reverse` / `take(n)` / `drop(n)`.

  These used to exist twice — once for `i32[]`, once for `string[]` under a
  name invented to dodge the shared namespace (`count` vs `count_str`,
  `reversed` vs `reverse`, `sorted_asc` vs `sorted_str_asc`). Those dodged
  names are gone (#2663); use the single generic verb.

  `sum` and `product` joined last, and cost the most. Delegating to
  `num.sum` / `num.product` needs `std/array` to import `std/num`, and since
  `std/string` imports `std/array` that puts `impl Add for i32` in the
  transitive closure of nearly every program. Trait methods used to land in
  one flat `i32.<name>` namespace, so num's `Add::add` collided with a USER
  trait that also provided `add` for i32 — `E006: method "add" on i32
  redeclared`, pointing into stdlib source the program never imported.
  Per-trait namespacing plus the call-site ranking fixed that (#6931,
  docs/TRAITS.md §5.1): both impls register, and a user's own trait outranks
  one that only arrived through the closure. Import `std/num` *directly*
  alongside your own `add` for i32 and the two tie, which is `E074` — an
  ambiguity naming both traits, not a redeclaration at your definition.

- **i32[]-specific:** `avg`, `range`, `gcd_all`, `lcm_all`, `abs_each`,
  `pairwise_diffs`, `min_max`, `every_positive`, `cumsum`, `sum_squared`,
  `sum_abs`, `all_zero`, `median`, `mode`. These need integer division or an
  i32 identity, so they stay pinned to the element type.

- **string[]-specific:** `join`, `join_with_last`, `filter_non_empty`,
  `count_non_empty`, `distinct_count`, `max_by_len`, `min_by_len`,
  `sum_lens`, `all_non_empty`, `any_contains`, `all_starts_with`,
  `all_ends_with`, `all_eq_str`.

- **i64 / f64 free functions** (statistical and vector reductions with no
  generic bounded equivalent — `avg` in particular cannot be written
  generically without a numeric conversion trait): `avg_i64`;
  `product_f64` (empty = 1), `cumsum_f64` (running prefix sum),
  `cumprod_f64` (running product), `diff_f64` (successive differences, one
  shorter; inverse of `cumsum`), `avg_f64`, `variance_f64` / `stddev_f64`
  (population variance and its square root, `Option[f64]`, `None` for
  empty), `median_f64` (averages the two middles for even length),
  `range_f64` (`max - min` spread), `dot_f64(a, b)` (dot product, runs to
  the shorter length), `norm_f64` (Euclidean / L2 norm,
  `sqrt(dot(self, self))`), `distance_f64(a, b)` (Euclidean distance
  `norm(a - b)`), `normalize_f64` (unit vector; zero / empty returned
  unchanged), `scale_f64(arr, k)` (scalar multiply), `add_f64(a, b)`
  (element-wise sum, runs to the shorter length).

- **generic `[T]` combinators, free + method form** (so pipelines read
  left-to-right — `xs.map(f).filter(g)`): `is_empty`, `first`/`last`
  (→ `Option[T]`, `None` when empty), `get(i)` (bounds-checked →
  `Option[T]`, negative index → `None`), `map`, `filter`, `fold`, `reduce`,
  `any`, `all`, `none` (complement of `any`), `find`, `find_last`,
  `position` (index of the first element satisfying a PREDICATE — the
  value-driven form is `first_index_of`), `rposition`, `count_where` (tally
  matching a predicate), `sum_by` (sum of an i32 projection over any element
  type), `enumerate`, `concat`, `chunks`, `chunks_exact`, `windows`, `zip`,
  `flat_map`, `partition` (→ `(kept, rejected)`), `scan` (running left fold,
  same length as input), `intersperse`, `step_by`, `rotate_left`/
  `rotate_right` (cyclic shift by n mod len; negative n rotates the other
  way), `max_by`/`min_by` (extremum under a `sort_by`-style comparator;
  first on a tie, `None` when empty), `sort_by`.
  Eq/Ord-bounded: `contains`, `index_of`, `index_of_last`, `dedup`
  (collapse consecutive runs — single-pass complement of `distinct`),
  `binary_search` (O(log n) → `Option[i32]` over an ascending-sorted array),
  `all_equal` (≤ 1 distinct value), `is_sorted`, `equal`,
  `starts_with`/`ends_with`. Every Eq-bounded verb compares through the
  bound's `eq` method, so a `@derive(Eq)` struct or enum element works as
  well as a primitive one.

- **generic `[T]`, FREE FUNCTION ONLY** — no `xs.verb()` form, call as
  `array.verb(xs, …)`: `find_map` (first `Some` of a projection returning
  `Option[U]`), `find_indices`, `take_while`/`drop_while`,
  `take_last`/`drop_last`, `slice`, `max_by_i32_key`/`min_by_i32_key`
  (extremum by an i32 projection), `running_max`/`running_min`, and the
  Eq-bounded set algebra `union`/`intersection`/`difference`. `flatten`
  (`T[][]` → `T[]`) is free-only structurally: an array-receiver method must
  be element-polymorphic `(xs: T[])`, and a nested-array receiver is
  rejected by E021.

### `std/unicode`

Unicode-aware case mapping — what `std/string`'s casing methods
delegate to, callable directly over a whole string, plus the `char`
method surface for a single scalar. Decodes UTF-8, maps each code point
(Latin, Greek, Cyrillic, Armenian, fullwidth, …) via tables generated
from the Go stdlib's `unicode` package, and re-encodes.

- `to_upper(s)` / `to_lower(s)` — whole-string, **full (1→N)** mapping
  (`ß` → `SS`)
- `swap_case(s)` / `capitalize(s)` / `title_case(s)`
- **`char` methods** — `c.to_upper()` / `c.to_lower()` (**simple** 1:1,
  since a 1→N expansion has no single scalar to return), plus
  `c.is_letter()` / `is_digit()` (Nd) / `is_alnum()` / `is_whitespace()`
  / `is_upper()` / `is_lower()`. Methods rather than free functions
  because a free `to_upper(c: char)` would collide with
  `to_upper(s: string)` — and because the receiver type is what says
  which operation you meant.
- `case_fold(s)` — the comparison form (`ß` → `ss`); a third operation,
  not lowercasing
- `eq_ignore_case(a, b)` — caseless equality under **full case folding**

**Canonical normalization.** The same text can be spelled more than one
way — `é` as one code point (NFC) or as `e` plus a combining acute
(NFD) — and `==` on strings is byte equality, so those two compare
unequal. Normalize when the input is text a human typed or another
system sent (search, dedup, usernames); NFD-shaped text comes from
macOS filesystem APIs, some IME and browser input paths, and any client
that normalizes differently from the server.

- `nfc(s)` / `nfd(s)` — the two canonical forms. Separate entry points
  rather than one `normalize(s, form)` so per-function DCE keeps the
  composition table out of programs that only decompose.
- `eq_canonical(a, b)` — canonical-equivalence comparison, with a
  byte-equality fast path so identical strings never normalize
- `is_nfc(s)` / `is_nfd(s)` — quick checks that answer without building
  a normalized copy; ASCII short-circuits before any table lookup.
  `is_nfc` saves the *allocation*, not the binary: an inconclusive
  ("Maybe") code point has to fall back to a full comparison, so it
  links the composition table anyway. `is_nfd` needs no such fallback
  and stays cheap on both counts.

NFKC/NFKD are **not** provided: they are compatibility (lossy) forms
needing a second full-size table, which the payoff does not justify.

**Grapheme segmentation (UAX #29).** A "character" as a *reader* means a
grapheme cluster, not a byte and not a code point: `e`+combining-acute,
a family emoji ZWJ sequence, a flag, and a Hangul syllable are each one
cluster. Opt-in, and DCE'd to nothing unless called.

- `graphemes(s): str[]` — split into extended grapheme clusters. The
  elements are non-copying **views** into `s`, so the split allocates no
  cluster text; they borrow, so keep them in a scope where `s` is live.
- `grapheme_count(s): i32` — a separate scan, so counting does not
  allocate the array
- `reverse_graphemes(s): string` — reverse by cluster. This is the
  correct-by-default sibling of `reverse_bytes`, which keeps its name
  because it is the honest one: it carries the hazard in the name.

Reaching for the *n-th* grapheme is usually a design smell — it is O(n)
to find and rarely what the problem needed. Prefer iterating, or an
operation that need not know about clusters at all. `s.len()` stays
**bytes** and `s[i]` stays a byte index precisely so the cheap
operations remain visibly cheap.

Caveats: the per-scalar `char` methods `c.to_upper()` / `c.to_lower()` stay
**simple** (1:1) — a 1→N expansion has no single code point to return.
Greek Final_Sigma **is** applied when lowercasing (a word-final `Σ`
becomes `ς`); the locale tailorings — Turkish dotless i, Lithuanian —
are not, by design.
The tables are regenerated by `cmd/unicodegen`. Two of them have no
build-time oracle in Go's stdlib: the full case mappings (Go has simple
mappings only) and the normalization data (Go has neither canonical
decompositions nor combining classes). Both come from CPython's
`unicodedata` — see `cmd/unicodegen/gen_normdata.py`, which regenerates
the checked-in `normdata.txt`. Hangul composes and decomposes
arithmetically rather than by table.

### `std/dotenv`

Parse a `.env` file (12-factor KEY=VALUE config) into a
`Map[string, string]`.

- `parse(s): Map[string, string]` — `KEY=value` lines (key/value
  trimmed), `#` comments and blank lines ignored, an optional `export `
  prefix stripped, `"..."` double-quoted values (with `\n \t \r \\ \" \'`
  escapes) and `'...'` single-quoted (literal) values; a repeated key's
  last assignment wins. `\r\n` endings handled.

### `std/glob`

Shell-style glob matching over a path-like string.

- `glob_match(pattern, text): boolean` — `*` (any run except `/`), `?`
  (one non-`/` char), `**` (globstar, crosses `/`, with `**/` matching
  zero directories), and `[abc]` / `[a-z]` / `[!…]` character classes.
  Anchored (whole text vs whole pattern).

### `std/textwrap`

Greedy word wrapping for terminal / help text.

- `word_wrap(text, width): string` — break `text` into lines of at most
  `width` code points, breaking only between words; preserves hard
  newlines (blank lines stay blank), places an over-long word on its own
  line unbroken, and collapses runs of spaces. Non-positive `width`
  returns `text` unchanged.

### `std/ansi`

Raw, composable ANSI SGR terminal styling — the mechanism layer beneath
`std/cli`'s NO_COLOR-gated `cli_*` helpers. Each wrapper always emits the
escape codes; nesting composes because every wrap ends in a full reset.

- `sgr(code, s)` — wrap `s` in `ESC[<code>m … ESC[0m`; exposed for
  256-colour (`"38;5;208"`) / truecolour (`"38;2;r;g;b"`) codes.
- **Foreground:** `black`/`red`/`green`/`yellow`/`blue`/`magenta`/`cyan`/
  `white` (+ `bright_*` variants).
- **Background:** `bg_black` … `bg_white`.
- **256-colour:** `fg_256(n, s)` / `bg_256(n, s)` (xterm palette 0–255).
- **Truecolour (24-bit):** `fg_rgb(r, g, b, s)` / `bg_rgb(r, g, b, s)`.
- **Styles:** `bold`, `dim`, `italic`, `underline`, `reverse`,
  `strikethrough`.
- `strip(s)` — remove every SGR sequence again (for display-width
  measurement or plain-text logs); preserves surrounding + UTF-8 text.

### `std/table`

Render rows of strings as a column-aligned text table (CLI output).

- `render(rows: string[][]): string` — pad each column to its widest
  cell (code-point width), two spaces between columns, last column
  unpadded; short rows get empty trailing cells.
- `render_with_header(headers, rows): string` — the same with a header
  row and a `-` rule under each column.

### `std/strdist`

String similarity — for fuzzy matching / "did you mean" / dedup.

- `levenshtein(a, b): i32` — edit distance over Unicode **code points**
  (so `levenshtein("café", "cafe") == 1`).
- `similarity(a, b): f64` — `1.0 - distance / max_len`, in `[0.0, 1.0]`
  (1.0 for identical or both-empty).

### `std/rand`

Randomised array helpers over the CSPRNG-backed `std/math.random_int`.
Value-semantic (they never mutate the input).

- `shuffle(xs): T[]` — a uniformly random permutation (Fisher-Yates).
- `choice(xs): Option[T]` — a random element (`None` when empty).
- `sample(xs, k): T[]` — `k` elements without replacement, random order.

### `std/semver`

Semantic Versioning 2.0.0 (semver.org) — parse and precedence-compare.

- `parse(s): Option[SemVer]` — `major.minor.patch` (required) with an
  optional `-prerelease` and `+build`; validates numeric fields (no
  leading zeros) and identifier syntax.
- `(a).compare(b): i32` (-1 / 0 / 1) plus `.eq` / `.lt` / `.gt`, and
  `(v).to_string()`. Precedence follows §11: numeric core, a prerelease
  ranks below the release, prerelease identifiers compare numerically /
  lexically (numeric < alphanumeric), and **build metadata is ignored**.

### `std/math`

Free helpers — random, ranges, numeric constants, angle + interpolation
helpers, RGB packing.

- `random_int(lo, hi)`
- `range(start, end)`, `range_step(start, end, step)`
- `i32_max()`, `i32_min()`, `i64_max()`, `i64_min()`
- `pi(): f64`, `tau(): f64` — the closest f64 to π and 2π (`tau() == 2.0 *
  pi()` exactly).
- `to_radians(deg): f64`, `to_degrees(rad): f64` — inverse angle
  conversions; the zero angle is exact both ways.
- `lerp(a, b, t): f64` — linear interpolation (`a·(1−t) + b·t`), exact at
  both endpoints (`t == 0.0` → `a`, `t == 1.0` → `b`) and extrapolating
  outside [0, 1].
- `pack_rgb(r, g, b)` — pack three 0–255 channels into a 24-bit i32.
- `parse_rgb_hex(s): Option[i32]` — inverse: parse `#rrggbb` / `rrggbb`
  / `#rgb` shorthand (case-insensitive) into a packed RGB i32, `None` if
  malformed. Completes the colour pipeline with `(i32).to_rgb_hex()` and
  `std/ansi.fg_rgb`.
- `rgb_luminance(rgb): i32` — perceived brightness 0–255 (ITU-R BT.601
  luma), and `rgb_is_dark(rgb): boolean` (luma < 128) for picking a
  readable foreground over a coloured background.

### `std/sort`

Free sort / compare helpers. The non-consuming sorts are stable
bottom-up merge sorts, O(n log n) — safe on large inputs, not just
the small-list convenience cases.

- `sort_i32_asc(arr)`, `sort_i32_desc(arr)`
- `sort_i64_asc(arr)`, `sort_i64_desc(arr)`
- `sort_u32_asc(arr)`, `sort_u64_asc(arr)`
- `sort_f64_asc(arr)`, `sort_f64_desc(arr)` (NaN ordering
  unspecified — filter NaNs first if it matters)
- `sort_strings_asc(arr)`, `sort_strings_desc(arr)`,
  `sort_strings_asc_ci(arr)`
- `string_cmp(a, b)`, `string_cmp_ci(a, b)`
- `sort_by_i32_key(arr, key)` — sort by an `i32` projection
  (Schwartzian: each `key(x)` computed once)
- `sort_key[T, K: cmp.Ord](arr, key)` — the generic-key
  generalisation: sort by a projection to any `Ord` key
  (`string`, `u64`, a `@derive(Ord)` struct), dispatching the
  order through `key.cmp(...)`

`sort_by[T](xs, cmp)` (comparator-driven) and `sort[T: cmp.Ord]`
(no-comparator, `Ord`-ordered) live in `std/array` / `core/cmp`.

### `std/set`

A generic, value-semantic set of distinct elements,
`Set[T: cmp.Eq]`. Every operation returns a NEW set and leaves
its receiver untouched. Element type only needs `cmp.Eq`
(membership is decided by the bound's `eq`, so a `@derive(Eq)`
struct or enum works); iteration / `to_array()` is in
first-inserted order.

- `set_new()`, `set_of(xs)` — empty set / dedup an array
- `(s).add(x)`, `(s).remove(x)` — insert / delete, returning a
  new set (a no-op returns the receiver)
- `(s).contains(x)`, `(s).len()`, `(s).is_empty()`,
  `(s).to_array()`
- `(s).union(o)`, `(s).intersect(o)`, `(s).difference(o)`,
  `(s).symmetric_difference(o)` (elements in exactly one set)
- `(s).is_subset(o)`, `(s).is_superset(o)`, `(s).is_disjoint(o)`,
  `(s).equals(o)` (order-insensitive)

Backed by a linear-scan array, so `contains` / `add` are O(n)
(an n-element build is O(n²)) — right-sized for CLI-scale working
sets, not for large collections.

### `std/format`

- `format(fmt, args: string[])` — template substitution with `{}`
  placeholders and Rust-style
  `{:[[fill]align][sign]['0'][width][.precision]}` specs (`{:>8}`,
  `{:*^10}`, `{:+06}`, `{:.3}`, `{:>8.2}`). `.N` counts fractional
  digits on a decimal numeral (rounded half-away-from-zero) and bytes on
  anything else.
- `format_values(fmt, args: T[])` for `T: cmp.Display`, and
  `format1(fmt, a)` … `format4(fmt, a, b, c, d)` — the same substitution
  over args that are NOT pre-stringified, each rendered through its own
  `Display` impl. `format_values` takes one element type; the arity
  family binds each arg's type separately, so its args are heterogeneous
  (`format3("{} {} {}", 3, "cats", true)`). Both monomorphise, so there
  is no boxing and no runtime dispatch.
- `format_bytes(n)` — `"1024 → 1 KiB"` shape (binary prefixes).
- `format_duration_ms(ms)` — `"1h 23m 45s"` shape.
- `parse_duration_ms(s)` — inverse of `format_duration_ms`: parse a
  `<int><unit>` sequence (units `ms`/`s`/`m`/`h`/`d`, space-optional,
  e.g. `"1h30m"`, `"1h 30m"`, `"500ms"`) into `Option[i64]` milliseconds;
  `None` on empty input, a missing/unknown unit, or a part with no number.

### `std/csv`

RFC 4180 escape / join / parse (single record and full document).

- `csv_escape(s)`, `csv_join(arr)`, `csv_parse_line(s)` — one record.
- `csv_parse(s)` — a whole document → `string[][]`; quoted fields may
  hold embedded commas AND newlines, records split on `\n` / `\r\n`,
  and a trailing terminator yields no spurious empty record.
- `csv_serialize(rows)` — the inverse of `csv_parse` (CRLF-separated).

### `std/log`

Zero-config stderr wrappers plus a leveled logger (#2683).

- `log_info(msg)`, `log_warn(msg)`, `log_error(msg)` — thin stderr
  wrappers with a level prefix.
- `new_logger(min_level)` / `new_json_logger(min_level)` — a `Logger`
  value carrying a min-level threshold (`level_trace()`..`level_error()`)
  and a plain-text vs JSON-lines output mode.
- `logger.at(level)` / `logger.info_()` … begin a `LogEntry`; chain
  `.str(k, v)` / `.int(k, v)` / `.bool(k, v)` to attach structured
  fields, then `.render(msg)` (pure → string, "" if below threshold)
  or `.emit(msg)` (writes to stderr).

### `std/io`

- `read_all_stdin()` — read until EOF into a single string.

### `std/path`

POSIX path manipulation (string-level only).

**Paths are assumed to be valid UTF-8**, and there is no `OsStr`-style
type — deliberately. On Linux and macOS a path is really an arbitrary
byte sequence, so this is a simplification: Rust needs `OsStr`/WTF-8
because Windows paths are UTF-16 with possible lone surrogates, Python
needs `surrogateescape`, Haskell added `OsPath`. Fern targets Linux,
macOS and WASI, where paths are UTF-8 in practice, and a second string
type would cost more across the language than the cases it buys.

A path that is not valid UTF-8 is a **boundary error, not a value**: it
should surface where the path enters the program (an argument, a
`read_dir` entry, a config file), not deep inside path manipulation. If
you need to handle one, skip this module and work on raw bytes —
`s.as_bytes()` gives a non-copying `[u8]` view, and the byte-level file
APIs carry the name through unmodified.

- `path_join(parts)`, `path_parent(p)`, `path_file_name(p)`,
  `path_extension(p)`, `path_clean(p)`.
- `path_is_absolute(p)` — true iff `p` begins at the root (`/`).
- `path_stem(p)` — last component minus its final extension
  (`"archive.tar.gz"` → `"archive.tar"`, `".bashrc"` → `".bashrc"`).
- `path_with_extension(p, ext)` — replace/append the final extension
  (`ext` without a leading dot; empty `ext` drops it), preserving the
  directory (`"a/b/foo.txt"`, `"md"` → `"a/b/foo.md"`).

### `std/base64`

- `base64_encode(b: u8[])` / `base64_decode(s): u8[]` / `base64_decode_strict(s): Option[u8[]]` — standard RFC 4648 alphabet, `=` padding. Bytes are `u8[]` on both sides (#5730): encoded output is text, the payload is not. Encode a string's bytes with `std/string`'s `s.bytes()`.
- `base64url_encode(b: u8[])` / `base64url_decode(s): u8[]` / `base64url_decode_strict(s): Option[u8[]]` — URL-safe variant (`-`/`_` alphabet, no padding; decode tolerates padded input). The JWT / URL-token encoding; the strict decoder returns `Option` (`None` on malformed input or a non-url-safe `+`/`/`).

### `std/base32`

RFC 4648 base32 (standard `A–Z 2–7` alphabet, `=` padding).

- `base32_encode(b: u8[])` / `base32_decode(s): u8[]` — decode is lenient
  (stops at the first non-base32 / non-`=` byte). Round-trips any
  content; the case-insensitive, digit-safe alphabet suits TOTP secrets,
  filenames, and DNS labels.
- `base32_decode_strict(s): Option[u8[]]` — `None` on malformed input
  (bad char, wrong padding, impossible group) instead of truncating;
  the strict variant to use for a security-sensitive secret / token,
  matching `base64_decode_strict` / `hex_decode_strict`.

### `std/hex`

Hex round-trip.

- `hex_encode(b: u8[])` (lowercase `0-9a-f`), `hex_encode_upper(b: u8[])`
  (uppercase `0-9A-F`).
- `hex_decode(s): u8[]` (lenient, either case),
  `hex_decode_strict(s): Option[u8[]]` (`None` on malformed input).

### `std/crypto`

From-scratch SHA-256 (FIPS 180-4) and HMAC-SHA256 (RFC 2104), verified
against NIST / RFC 4231 known-answer vectors.

Bytes are `u8[]`, not `string` (#5730): digests, derived keys, and every
key / salt / IKM / info input. The `*_hex` variants still return a
`string` — hex output genuinely is text — and the message to hash / the
password to stretch stay `string`. Pass a string's bytes to a byte-typed
parameter with `std/string`'s `s.bytes()`.

- `sha256_bytes(s: string): u8[]` / `sha256_hex(s: string): string`.
- `hmac_sha256_bytes(key: u8[], msg: string): u8[]` /
  `hmac_sha256_hex(key: u8[], msg: string): string`.
- `consteq(a: u8[], b: u8[])` — constant-time byte compare; `hmac_verify` /
  `hmac_verify_hex` — the timing-safe way to check a MAC.
- `pbkdf2_sha256(password: string, salt: u8[], iterations, dk_len): u8[]` /
  `pbkdf2_sha256_hex(...): string` — PBKDF2-HMAC-SHA256 (RFC 8018)
  password-based key derivation. Use a random per-password salt and a
  high iteration count for password storage.
- `pbkdf2_verify(password, salt, iterations, expected: u8[])` /
  `pbkdf2_verify_hex(...)` — re-derive and compare against a stored key
  in constant time (`consteq`). Use these to verify a password; the
  short-circuiting `pbkdf2_sha256(...) == stored` that used to be the
  timing-oracle hazard here no longer even compiles, since `u8[]` has no
  structural `==` (E041).
- `hkdf_extract(salt: u8[], ikm: u8[]): u8[]` /
  `hkdf_expand(prk: u8[], info: u8[], length): u8[]` /
  `hkdf_sha256(salt, ikm, info, length): u8[]` / `hkdf_sha256_hex(...): string` —
  HKDF-SHA256 (RFC 5869) key derivation for high-entropy input keying
  material (a shared secret / random key), for key separation and
  subkey derivation. Distinct from PBKDF2, which stretches a low-entropy
  password.
- `hotp_sha256(key: u8[], counter, digits)` /
  `totp_sha256(key: u8[], unix_time, period, digits)` — one-time
  passwords for 2FA (RFC 4226 / RFC 6238, SHA-256 mode). `key` is the raw
  secret bytes, so `base32.base32_decode(secret)` feeds it directly;
  returns the code as an integer to zero-pad to `digits`.

### `std/uuid`

UUID generation + inspection (RFC 4122 / RFC 9562), canonical
hyphenated lowercase form.

- `uuid_v4()` — random version-4 UUID.
- `uuid_v7()` — time-ordered version-7 UUID (48-bit Unix-ms prefix +
  random tail); sortable identifier.
- `uuid_nil()` — the all-zeros nil UUID; `uuid_is_nil(s)` tests for it.
- `uuid_version(s)` — the version digit (index-14 nibble): `4`/`7`/`0`
  for v4/v7/nil, or -1 if `s` isn't a well-formed 36-char UUID.
- Validate a UUID string with `string.is_uuid()`.

### `std/url`

Percent-encoding, URL parsing, query parsing.

- `url_encode(s)`, `url_decode(s)`, `form_encode(s)`, `form_decode(s)`
- `url_parse(s) Option[Url]`
- `query_parse(s) Map[string, string[]]`, `query_encode(pairs)`
- **Single-key query accessors** (scan the raw query string, no map
  build): `query_get(query, key) Option[string]` (first value),
  `query_get_all(query, key) string[]` (ordered), `query_has(query, key)`

### `std/json`

- `json_encode(v: JsonValue): string` — compact canonical JSON
- `json_encode_pretty(v: JsonValue, indent: i32): string` — indented,
  human-readable JSON (`indent` spaces per level; empty arrays/objects
  stay on one line). Same value tokens as `json_encode` — only
  whitespace differs.
- `json_parse(s: string): Option[JsonValue]`
- `json_escape(s: string): string` — escape a raw string for embedding
  inside a JSON string literal (caller supplies the quotes): `\` `"`
  backslash-escaped, `\n` `\r` `\t` short escapes, other C0 controls as
  lowercase `\u00XX`, everything else byte-for-byte. The one shared
  escaper — the `JsonValue` encoder, `@derive(json.Json)`, and
  `std/log`'s JSON-lines mode all route through it.

### `std/http`

HTTP/1.1 request parsing, response builders, wire-format
serializer.

- **Response builders:** `http_response_ok`,
  `http_response_text`, `http_response_not_found`,
  `http_response_bad_request`, `http_response_internal_error`,
  `http_response_redirect`, `http_response_no_content`; typed-body
  variants that set `Content-Type` up front:
  `http_response_json` / `http_response_json_status` /
  `http_response_html` / `http_response_plain`
- **Header methods:** `(resp).with_header(name, value)` (set) /
  `(resp).with_appended_header(name, value)` (append) /
  `(resp).with_content_type(ct)`
- **Cookies (RFC 6265):** `(req).cookie(name): Option[string]`;
  `SetCookie` built via `cookie_new(name, value)` (hardened
  defaults: `Path=/`, `HttpOnly`, `SameSite=Lax`) or
  `cookie_delete(name)`, serialized with `(c).serialize()` and
  attached with `(resp).with_set_cookie(c)` (append semantics —
  one `Set-Cookie` header per cookie)
- **Status / classifiers:** `http_status_text`,
  `is_valid_http_status`, and the RFC 9110 status-class predicates
  `http_is_informational` / `http_is_success` / `http_is_redirect` /
  `http_is_client_error` / `http_is_server_error` (1xx–5xx), plus
  `http_is_error` (4xx or 5xx)
- **Path / header / UA:** `http_path_segments`,
  `http_url_path_only`, `http_user_agent_is_bot`,
  `http_header_value`
- **Wire format:** `http_parse_request(buf): Option[HttpRequest]`,
  `http_serialize_response(resp): string`

### `std/tcp`

- `tcp_serve(port, handler)` — HTTP/1.1 accept loop. Calls
  `handler(req: HttpRequest): HttpResponse` once per accepted
  connection. Each connection's request read is bounded by a
  10 s deadline (the slow-loris guard).
- `tcp_serve_deadline(port, handler, recv_deadline_ms)` —
  `tcp_serve` with an explicit per-request read deadline; a
  client that hasn't delivered a complete request in time is
  disconnected without a response.
- `tcp_recv_deadline(fd, max, deadline_ms): Option[string]` —
  recv bounded by a readability deadline: `Some(chunk)` in time
  (empty chunk = EOF), `None` at the deadline. On interp (where
  `poll` is a stub) it degrades to a blocking recv.
- `__port_from_env(name, fallback)` — env-var port lookup used
  by the auto-`main`-from-`handle()` synthesis so handler-shaped
  programs can be tuned via `PORT=N ./bin`.

The raw socket primitives `tcp_listen` / `tcp_accept` / `tcp_recv`
/ `tcp_send` / `tcp_close` are runtime-provided, emitted by
codegen from extern stubs at module boundary — not declared in
this module.

### `std/fetch`

Outbound HTTP/1.1 client (the upstream-fetch half of the edge
use case). Hosts are literal IPv4 (no DNS / TLS yet).

- **Addresses:** `ipv4(a,b,c,d)` / `parse_ipv4(s)` — pack a
  dotted-quad into the network-byte-order i32 `tcp_connect` wants.
- **Blocking:** `fetch_raw(host_be, port, request)`,
  `fetch_get(host_be, port, path)`, `get_url("http://…")` — send
  and read the whole response ("" on failure).
- **Deadline-bounded:** `fetch_raw_deadline` /
  `fetch_get_deadline(host_be, port, path, deadline_ms):
  Option[string]` — `Some(response)` in time, `None` when the
  upstream was too slow (connect/send failure is `Some("")`,
  mirroring `fetch_raw`).
- **Awaitable:** `fetch_future(host_be, port, path):
  async.Future[string]` — fan out through `async.gather` /
  `async.race` / `async.with_deadline`.
- **Response helpers:** `http_status(resp)`, `http_body(resp)`.
- **Capability-scoped:** `(plat: Platform).fetch(host, port,
  path): i32` — status-code GET through the handler's `Platform`
  bag.

### `std/headers`

HTTP `HeaderMap` with case-insensitive lookup, multi-valued
entries, and insertion-ordered iteration. Backs the `headers`
field slated for `HttpRequest` / `HttpResponse`.

- `header_map_new()` — empty map.
- `(h).set(name, value)` / `(h).append(name, value)` — replace vs.
  add a value under a case-folded key.
- `(h).get(name): Option[string]` (first value) /
  `(h).get_all(name): string[]` (every value) / `(h).len()`.

### `std/stream`

Byte-stream value backing the eventual `HttpRequest.body: Stream`
migration. Phase 1 is an in-memory buffer-backed `Stream`.

- Constructors: `stream_from_bytes(bs)`, `stream_from_string(s)`,
  `stream_empty()`.
- Readers: `(s).read_byte()`, `(s).read_n(n)`, `(s).read_line()`,
  `(s).read_all()`, `(s).read_all_string()`.
- Introspection: `(s).len()`, `(s).remaining()`, `(s).is_empty()`.

### `std/io_buffered`

In-memory buffered `BytesWriter` — accumulate bytes / strings,
then drain once.

- `bytes_writer_new()`; `(w).write_string(s)`, `(w).write_bytes(bs)`,
  `(w).write_byte(b)`.
- `(w).into_bytes()` / `(w).into_string()` to drain; `(w).len()`,
  `(w).is_empty()`, `(w).reset()`.

### `std/time`

Date/time module shaped after jiff / NodaTime, backing the
built-in `Instant`, `Date`, `Time`, `DateTime`, `Zoned`, `Span`,
`Duration`, and `TimeZone` types.

- **Instants:** `instant_now()`, `instant_from_unix(sec)`,
  `instant_parse_rfc3339(s)`, `instant_zoned_parse_rfc3339(s)`.
- **Calendar:** `date_make(y, m, d)`, `time_make(h, m, s)`,
  `datetime_make(date, time)`, `date_parse_iso(s)`,
  `is_leap_year(y)`, `days_in_month(y, m)`.
- **Zones:** `timezone_utc()`, `timezone_fixed_offset(secs)`.
- **Spans / durations:** `span_seconds`/`_minutes`/`_hours`/`_days`/
  `_weeks`/`_months`/`_years(n)`, `duration_seconds(s)`,
  `duration_millis(ms)`, `(d: Duration).to_string()` — compact
  canonical form (`"1h30m45s"`, `"-500ms"`, `"0s"`; ms resolution),
  distinct from the space-separated i32-ms `format_duration_ms`.
- **Humanised relative time:** `(i: Instant).relative_to(now)` — the
  `fromNow` shape, e.g. `"5 minutes ago"`, `"in 2 days"`, `"just now"`
  (coarse units: month ≈ 30 days, year = 365 days).
- Named constants: `NANOS_PER_SECOND`, `SECONDS_PER_DAY`,
  `DAYS_PER_WEEK`, etc.

### `std/async`

The blessed structured-concurrency surface (see
`docs/ASYNC-REDESIGN.md`) — combinators over a `Future[T]`, a
not-yet-ready value of type `T`. Colorless (no function coloring, no
compiler transform) and portable: every combinator compiles and runs
on all backends, driving the universal `poll` builtin. Replaces the
old `concurrent { … }` / `await` keyword surface.

- `Future[T]` (enum) — `Ready(T)` or `Pending(fd, resume)`, a poll fd
  plus its continuation.
- `gather(fs, on_incomplete)` — await ALL futures, values in input
  order, their I/O overlapping on one thread; an unresolved slot gets
  `on_incomplete`.
- `race(fs, none_val)` — return on the FIRST to finish as `(index,
  value)`; `(-1, none_val)` if none can progress.
- `with_deadline(ms, fs)` — await all with a timeout, yielding
  `Option[T][]`: `Some(v)` for each that resolved in time, `None` for
  one abandoned at the deadline.

### `std/mock_platform`

Test-ergonomics helpers for recording and asserting on platform
capability calls (log / fetch / kv / now) once `Platform` grows
beyond its placeholder shape.

- `mock_platform_new()`; `(m).record(call)`, `(m).reset()`.
- `(m).call_count()`, `(m).has_call(name)`, `(m).find_call(name)`.

### `std/test`

Pure-Fern unit-test runner. Tests are functions returning
`TestOutcome` (`Pass` = pass, `Fail(msg)` = fail). The shape
the project plans to migrate to once the compiler is self-
hosted and the Go-side `*_test.go` harness retires; see
`docs/ROADMAP-AND-SELF-HOSTING.md`. Output is TAP-13 so
existing test runners (`prove`, `tape`, jUnit converters)
can consume it directly.

```
import "std/test";

function test_addition(): test.TestOutcome {
    return test.assert_eq(2 + 2, 4);
}

function main(): i32 {
    var r: test.TestRunner = test.test_new("arithmetic");
    r = r.it("addition", test_addition());
    return r.finish();
}
```

Free functions are reached through the `test.` module prefix
(`test.test_new`, `test.assert_eq`, `test.fail`); the runner type
is `test.TestRunner`; receiver methods (`.it`, `.finish`, `.skip`)
stay bare.

- **Runner:** `TestRunner` (struct), `test_new(suite)`,
  `test_new_verbose(suite)`, `(r).it(name, result)`,
  `(r).finish() -> i32`
- **Skips & subsuites:** `(r).skip(name, reason)`,
  `(r).skip_if(cond, name, reason, result)`,
  `(r).subsuite(name)`, `(r).merge(child)` — toolchain-gated
  cases emit a TAP `# SKIP` directive; subsuites print with
  `parent / child` prefixes while keeping monotonic TAP
  numbering
- **Cleanup hook:** `(r).defer_cleanup(path)` registers a
  filesystem path for `remove_dir_all` at `finish()` time;
  used with the `temp_dir(...)` builtin to scrub fixtures
  regardless of test outcome.  Cleanup errors print as TAP
  comments and bump the exit code to 2 (the "tests passed
  but cleanup leaked" sentinel) so CI can distinguish from
  a real test failure.
- **Outcome constructors:** `pass()`, `fail(msg)`
- **Boolean assertions:** `assert_true(cond)`, `assert_false(cond)`
- **Generic equality / ordering:** `assert_eq(actual, expected)`,
  `assert_neq`, `assert_lt`, `assert_le`, `assert_gt`, `assert_ge`
  — trait-bounded (`cmp.Eq + cmp.Display` for `assert_eq` / `assert_neq`,
  `cmp.Ord + cmp.Display` for the relational four), so one helper each
  covers every integer width, `boolean`, and `string`. Failure
  messages quote both the actual and expected `Display` forms
- **Float assertions:** `assert_eq_f64_near(actual, expected,
  epsilon)`, `assert_eq_f32_near`, `assert_eq_f64_exact`,
  `assert_is_nan_f32`, `assert_is_nan_f64` — `_near` is the
  default; `_exact` is for f32_bits round-trips / NaN-payload
  canonicalisation tests
- **Relative-tolerance float assertions:**
  `assert_eq_f64_rel(actual, expected, rel_tol)`,
  `assert_eq_f32_rel` — passes when
  `|actual - expected| / |expected| <= rel_tol`. Reach for
  this (over `_near`) when the test covers values spanning
  many orders of magnitude — a fixed absolute epsilon is
  either too tight at large scales or too loose at small
  ones. Falls back to absolute compare when `expected == 0.0`
- **Range:** `assert_in_range_i32`, `assert_in_range_i64`,
  `assert_in_range_f64(v, lo, hi)`, `assert_in_range_f32` —
  inclusive bounds; the float variants fail on NaN inputs
  (NaN never satisfies an ordering compare)
- **Order:** `assert_sorted_asc(arr)` — generic
  (`cmp.Ord + cmp.Display`), monotonically non-decreasing;
  empty / single-element arrays vacuously pass; failure
  embeds the inversion index. `assert_sorted_desc` for
  descending order (pair with `sort_*_desc` output).
  `assert_strictly_sorted_asc` for the "sorted AND unique"
  contract — equal adjacent pairs are a violation here,
  unlike the non-strict variant
- **Float array:** `assert_eq_f64_array_near(actual,
  expected, epsilon)` / `assert_eq_f32_array_near` —
  element-wise compare with tolerance; NaN anywhere fails;
  mismatches name the index so long-vector diffs localise
- **Uniqueness:** `assert_unique(arr)` — generic
  (`cmp.Eq + cmp.Display`); every element appears at most
  once; walks the array so input order doesn't matter
- **Multi-substring:** `assert_contains_all(haystack, needles[])`,
  `assert_contains_any`, `assert_contains_in_order` — the
  failure message names which needle(s) didn't match so the
  diagnostic is grep-able
- **String diff:** `assert_eq_string_diff(actual, expected)` —
  reports the first differing line with its 1-based number
  + the two values; friendlier than the base `assert_eq_string`
  on multi-line stdout / generated source
- **Lines:** `assert_lines_eq(actual, expected_lines: string[])`
  — splits `actual` on `\n` and compares to a string array;
  reads better than escaping a long multi-line literal
- **Logging:** `(r).log(msg)` — chainable TAP-comment emitter
  (`# msg`) for debug breadcrumbs between cases.
  `(r).log_kv_string(key, value)` / `_i32` / `_i64` —
  structured `# key=value` form (string values quoted,
  numerics unquoted so `awk -F=` filters work); use when
  the post-run log scraper wants to pick out specific
  breadcrumbs
- **File state:** `assert_file_exists`, `assert_file_not_exists`,
  `assert_file_contains`, `assert_file_contents`,
  `assert_is_file`, `assert_is_dir`, `assert_file_size` —
  the last three are `stat()`-backed and distinguish files
  from directories
- **File lines:** `assert_file_lines(path, expected_lines:
  string[])` — read + split + compare line-by-line
  (delegates to `assert_lines_eq` so the diff messaging is
  identical to the in-memory version).
  `assert_file_line_count(path, n)` — line cardinality
  (trailing newline doesn't overcount)
- **Directory listing:** `assert_eq_dir_listing(dir,
  expected_names: string[])` — list the directory,
  sort both sides, compare element-wise (readdir order
  isn't observable). Pair with `must_temp_dir` + fixture
  creation to pin "the operation produced exactly these
  files"
- **JSON deep equality:** `assert_json_eq(actual, expected)` —
  parses both sides via `std/json` and walks the value
  trees in order-independent fashion (JObject key order
  isn't observable)
- **JSON detail (narrower than `_eq`):**
  `assert_json_has_key(json_text, key)` /
  `assert_json_lacks_key(json_text, key)` — top-level
  JObject key presence.
  `assert_json_array_len(json_text, n)` /
  `assert_json_object_size(json_text, n)` — cardinality.
  Each helper reports a distinct diagnostic for invalid
  JSON, wrong top-level type, and missing/extra entries
- **JSON field extraction:**
  `assert_json_eq_field_string(json_text, key, expected)`,
  `assert_json_eq_field_i32(json_text, key, expected)`,
  `assert_json_eq_field_bool(json_text, key, expected)`
  — pin a single top-level field's value at a specific
  type. The most common HTTP/RPC test shape ("response
  has `user_id` equal to 'abc-123'"). Each variant
  reports distinct diagnostics for the five failure
  modes (invalid JSON / non-object top-level / missing
  key / wrong type at key / value mismatch). The `_i32`
  variant rejects non-i32-parseable JNumbers (decimals,
  out-of-range) rather than silently truncating
- **Timing:** `assert_elapsed_lt_ms(start_ns, max_ms)` /
  `assert_elapsed_lt_us(start_ns, max_us)` — pair with
  `monotonic_ns()` to stamp the start; failure message embeds
  both the observed elapsed and the deadline.
  `assert_close_to_now_ms(actual_ms, max_skew_ms)` —
  wall-clock timestamp recency (bidirectional skew bound;
  failure names the observed signed skew so future-skewed
  vs old timestamps are distinguishable)
- **Benchmarks:** `(r).bench(name, iter, fn)` runs `fn`
  repeatedly and emits a TAP comment with min / median /
  mean / max microseconds; always passes.
  `(r).bench_max_us(name, iter, fn, budget)` fails when the
  MEDIAN per-iteration time exceeds the budget — median (not
  mean) so a single GC pause doesn't tip a regression bound.
  `(r).bench_max_ms(name, iter, fn, budget_ms)` is the
  millisecond-budget companion (1 ms = 1000 us); use it
  when the budget reads naturally in ms ("frame under 16 ms").
- **Set equality (order-independent):** `assert_set_eq`,
  `assert_subset` — generic (`cmp.Eq + cmp.Display`); multiset
  semantics so duplicate counts must match; failure message
  names the first unmatched element
- **Env-var:** `assert_env_set(name)`, `assert_env_unset(name)`,
  `assert_env_eq(name, expected)` — wrap the `env(name)`
  builtin's `Option[string]` return; failure messages
  distinguish "missing" from "wrong value"
- **Unreachable branch:** `unreachable(label)` — sugar for
  `fail("unreachable: " + label)`. Use in match-default arms
  that the test logic claims can't fire
- **Map assertions:** `assert_map_len(m, n)`,
  `assert_map_has(m, key, value)`, `assert_map_lacks(m, key)`,
  `assert_eq_map(actual, expected)` — generic over
  `K, V: cmp.Eq + cmp.Display`, so one helper each covers
  i32 / string keys and values. `assert_eq_map` is full deep
  equality (order-independent; walks `actual.keys()` so
  insertion-order differences don't matter)
- **Array predicates:** `assert_all_i32(arr, pred)` /
  `assert_all_string` — ∀ predicate, vacuous pass on []
  (failure names index + value). `assert_any_i32` /
  `assert_any_string` — ∃ predicate, vacuous FAIL on []
  (mathematical convention). Predicate signature is
  `(T) => boolean`; pass a lambda inline or a named fn
- **Golden files:** `assert_matches_golden(path, actual)`
  (bootstraps the file if missing — developer workflow) and
  `assert_matches_golden_strict(...)` (fails on missing — CI
  workflow)
- **`--filter PATTERN` selection:** `test_new_filtered(suite,
  pattern)` + `parse_filter_from_args(args())` — cases whose
  (prefix + name) don't contain the filter substring
  convert to skips with reason "filtered out". Pair with
  `fern -interp test.fern -- --filter foo` on the CLI.
- **`--fail-fast` short-circuit:** `test_new_fail_fast(suite)`
  / `(r).with_fail_fast()` + `parse_fail_fast_from_args(args())`
  — once any case fails, subsequent `it()` calls auto-skip
  with reason "fail-fast: prior case failed". Each skipped
  case still emits a TAP line so the plan stays faithful.
  Off by default (the full TAP stream is usually more useful
  in CI). Pair with `fern -interp test.fern -- --fail-fast`.
- **`--quiet` output mode:** `test_new_quiet(suite)` /
  `(r).with_quiet()` + `parse_quiet_from_args(args())` —
  suppresses the per-case `ok N - name` line for passes
  and skips; `not ok` lines + diagnostic blocks still
  print, as does the `1..N` plan + summary footer.
  Counters are unaffected (it's a print-suppression
  switch only). Use for the developer loop where seeing
  every passing test is noise; CI logs usually want the
  full TAP stream for triage.
- **Tempdir convenience:** `must_temp_dir(r, prefix) ->
  (string, TestRunner)` — single-shot tempdir + cleanup
  registration with fallback to a recorded skip on failure
- **string assertions:** the generic `assert_eq` / `assert_neq`
  cover `boolean` and `string` directly (both are `cmp.Eq +
  cmp.Display`). String-specific sugar: `assert_empty_string`,
  `assert_non_empty_string`
- **Substring:** `assert_contains`, `assert_not_contains`,
  `assert_starts_with`, `assert_ends_with`
- **Substring (case-insensitive):** `assert_eq_string_ci`,
  `assert_neq_string_ci`, `assert_contains_ci`,
  `assert_starts_with_ci`, `assert_ends_with_ci` — wrap
  the ASCII case-fold methods from `std/string`. Failure
  messages embed both raw values (no display-side case
  folding) so the byte-level difference is visible
- **Substring (multi-option):**
  `assert_starts_with_any(s, prefixes)` /
  `assert_ends_with_any(s, suffixes)` — single string
  matches at least one of the supplied options; empty
  options list always fails
- **Substring count:** `assert_string_count(haystack,
  needle, n)` — `needle` appears exactly `n` times in
  `haystack` (non-overlapping; delegates to
  `std/string`'s `.count(sub)`). Failure embeds both the
  observed and expected counts
- **String-array substring:**
  `assert_all_starts_with(arr, prefix)` /
  `assert_all_ends_with(arr, suffix)` /
  `assert_all_contain(arr, needle)` — substring property
  held across every element; empty array vacuously passes
  (∀ over ∅); failure embeds the first violation's index
  and value
- **Array assertions:** `assert_len_i32`, `assert_len_string`
  (length only); `assert_eq_array(actual, expected)` —
  generic (`cmp.Eq + cmp.Display`) element-wise compare over
  any element type. Single-position spot check:
  `assert_at(arr, idx, expected)` — generic, bounds-checked;
  failure distinguishes out-of-bounds from value mismatch.
  Float variants: `assert_at_f64(arr, idx, expected,
  epsilon)` / `_f32` — mandatory tolerance; NaN inputs
  always fail; failure message embeds the diff and the
  epsilon bound
- **Array membership:** `assert_array_contains(arr, needle)`,
  `assert_array_not_contains(arr, needle)` — generic
  (`cmp.Eq + cmp.Display`) membership; failure embeds the
  needle (positive) / index (negative). Empty arrays fail
  the positive form vacuously
- **Array cardinality:** `assert_count_i32(arr, pred, n)` /
  `_string` — exactly `n` elements satisfy `pred`; sits
  between `assert_all` (every) and `assert_any` (at least
  one). Failure message embeds the observed count
- **Option result:** `assert_is_some_i32(opt)` /
  `_string` — payload value irrelevant.
  `assert_is_none_i32(opt)` / `_string` — failure embeds
  the unexpected payload.
  `assert_is_some_eq_i32(opt, expected)` / `_string` —
  Some AND equal in one call; failure distinguishes None
  from value-mismatch
- **Result (Result[T, IoError]):**
  `assert_is_ok_string(res)` / `_string_array` — Ok
  variant; payload irrelevant.
  `assert_is_err_string(res)` / `_string_array` — Err
  variant; Ok-on-Err diagnostic embeds the unexpected
  payload (string value or array length).
  `assert_is_ok_eq_string(res, expected)` — Ok AND value
  matches; failure distinguishes Err-when-Ok-expected
  from value-mismatch. Stdlib's Result error type is
  uniformly `IoError` so helpers specialise on the Ok
  type only
- **Array set relations:**
  `assert_array_intersects_i32(a, b)` / `_string` — at
  least one shared element (empty either side always
  fails). `assert_array_disjoint_i32(a, b)` / `_string`
  — no shared element (empty either side vacuously
  passes; failure names the first shared element)
- **Array order-sensitive relations:**
  `assert_array_starts_with_i32(arr, prefix)` /
  `_string` — `arr` begins with `prefix` element-wise
  (empty prefix vacuously passes; failure either reports
  too-short or names first mismatching index).
  `assert_array_ends_with_i32(arr, suffix)` / `_string`
  — same anchored at the tail; failure index is in
  array coords so the bad slot is locatable.
  `assert_array_contains_subseq_i32(arr, needle)` /
  `_string` — `needle` appears as a contiguous
  sub-array of `arr` (order-sensitive complement to
  `assert_subset`)
- **Enumerated value:** `assert_one_of_i32(actual,
  allowed)` / `_string` — positive set membership
  (e.g., "exit code is one of [0, 1, 2]"). Empty allowed
  set always fails. `assert_none_of_i32(actual,
  forbidden)` / `_string` — negative membership
  (e.g., "log level is not any of [error, fatal,
  panic]"). Empty forbidden set vacuously passes.
  Failure messages render the rejected actual value with
  appropriate per-type quoting
- **Process assertions** (paired with the `subprocess(...)`
  builtin): `assert_exit`, `assert_stdout_eq`,
  `assert_stderr_eq`, `assert_stdout_contains`,
  `assert_stderr_contains`, `assert_process(result, exit,
  stdout_substr)`. Exit shortcuts:
  `assert_exit_zero(proc)`, `assert_exit_nonzero(proc)`.
  Multi-line and cardinality: `assert_stdout_lines(proc,
  lines[])` / `assert_stderr_lines`,
  `assert_stdout_line_count(proc, n)` /
  `assert_stderr_line_count`

Examples live under `examples/tests/`; the runner's own
meta-test (`runner_self_test.fern`) walks every assertion
helper on both pass and fail paths.

### `std/fuzz`

Byte-stream fuzzing harness layered on `std/test`. A fuzz
target is a `(string) => Option[string]` function — same
shape as a regular test — that gets called with each seed
verbatim and then `iterations` mutated variants (byte flip /
drop / insert / unchanged). The first failing input surfaces
as the runner's failure message with the offending bytes
escaped so the log doubles as a reproducer.

```
function check_to_upper_idempotent(input: string): Option[string] {
    if (input.to_upper().to_upper() == input.to_upper()) { return None; }
    return Some("to_upper is not idempotent");
}

function main(): i32 {
    var r: TestRunner = test_new("fuzz");
    r = r.fuzz("to_upper idempotent",
               ["", "abc", "Hello"], 100,
               check_to_upper_idempotent);
    return r.finish();
}
```

- `fuzz_run(seeds, iterations, target)` — raw entry point;
  returns `Option[string]` with the reproducer on failure
- `(r).fuzz(name, seeds, iterations, target)` — receiver-
  method form that folds the outcome into the runner as one
  TAP case
- `fuzz_run_shrink` / `(r).fuzz_shrink` — same shape, but on
  a failure the harness minimises the offending input via
  halving + single-byte drops before reporting. Failure
  message embeds both the raw input and the shrunk form so
  the log doubles as a clean reproducer.
- `fuzz_corpus_from_dir(path)` /
  `fuzz_corpus_from_dir_or(path, fallback)` — load every
  regular file under `path` as a seed (sorted by name,
  dotfiles + `_`-prefixed metadata skipped). The `_or`
  variant falls back to inline seeds when the directory
  is missing or empty.
- `fuzz_default_iterations()` — `200`; tuned for sub-second
  per-target runs in CI

Limitations: a target that crashes (out-of-bounds index,
division by zero) aborts the whole run (Fern has no panic
recovery); the harness is uniform-random, not coverage-
guided. The API is shaped so both can layer in later
without breaking the surface.

## `core/`

### `core/int`

Low-level integer to-string formatters. Pokes raw memory
(`__alloc_u8`, `__memcpy`, scratch buffers written backwards).
User code should reach for the method-syntax surface
(`(n).to_string()`, `(n).to_hex()`, `(n).to_binary()`) or
`format(…)` rather than calling these directly.

- `int_to_string(n)` — signed i32 → ASCII decimal
- `__int_to_string_u64(mag, neg)` — i64 / u64 helper
- `__radix_digit(c)` / `__radix_char(d)`
- `parse_int_radix(s, base)` — bases 2..36
- `int_to_string_radix(n, base)` — bases 2..36

### `core/bigint`

Arbitrary-precision integers. Sign-magnitude, little-endian base
2^32, one limb per `u64` slot.

Values are **immutable** — every operation returns a fresh `BigInt`,
matching the pure-collection convention. Types are referenced
module-qualified (`bigint.BigInt`); functions likewise
(`bigint.parse`).

- `zero()` / `from_i64(v)` / `parse(s)` → `Option[BigInt]`
- `(a).add(b)` / `.sub(b)` / `.mul(b)` — schoolbook multiply
- `(a).negate()` / `.abs()` / `.cmp(b)` / `.eq(b)`
- `(a).is_zero()` / `.is_negative()` / `.bit_length()`
- `(a).mul_pow10(k)` / `.shl(k)`
- `(a).to_string()` / `.hash()`

It imports **nothing**, deliberately: `std/string` needs a bignum
for `parse_float`'s exact fallback and cannot import anything that
reaches back to it. That is also why its `Display` / `Eq` / `Ord` /
`Hash` / `Default` / `Debug` impls live in `core/cmp` rather than
here — the orphan rule accepts the trait's module or the type's, and
only that side keeps this module import-free. `cmp` returns the
-1/0/1 `std/sort` expects, so `cmp.sort` over a `BigInt[]` works.

Only schoolbook multiplication is implemented. The asymptotic ladder
(Karatsuba → Toom-Cook → NTT) is deliberately not built out until
there is a workload to measure against.

### `core/cmp`

The comparison + display trait foundation. Three small traits
underpin the generic assertion helpers in `std/test` (and any
user code abstracting over "printable" / "comparable" values):

- `trait Display` — a value with a `to_string()` rendering.
- `trait Eq` — equality (`==` / `!=`).
- `trait Ord` — total ordering (`<` / `<=` / `>` / `>=`).

The built-in integer widths, `boolean`, and `string` all satisfy
these, which is why `test.assert_eq[T: cmp.Eq + cmp.Display]` and
friends work across every primitive with one generic helper.

### Module resolution

There is no auto-injected prelude (Phase 5 of
`docs/PRELUDE-TO-MODULES.md` is complete) — a program sees only
what it `import`s. A program that uses nothing but built-ins
(`putchar`, `print`, `len`, array indexing, arithmetic) needs no
imports at all.

Free-function calls into stdlib are qualified —
`int.int_to_string_radix(s, 16)` rather than a bare
`int_to_string_radix(s, 16)`. Bare receiver-method calls (`.abs()`,
`.to_string()`, `.pad_start(...)`) stay unchanged: the
checker dispatches them by receiver type through the
Methods map regardless of import path.

Transitive stdlib loads: importing a stdlib module pulls
in every other stdlib module its body dispatches into.
`import "std/i32"` reaches `std/string` (for the byte-
method ↔ string-method cycle) which reaches `std/array`
(for `.reverse()` / `.join()`) which reaches `std/sort`
(for `sort.sort_*` qualified). Cyclic stdlib imports are
allowed and resolve through modload's stdlib-cycle gate.
End-to-end coverage on arm64 / x86-64 / wasm32 lands as
the `Test*NoPreludeStdlibImports` suites in `internal/e2e`.

### `core/mem`

Value-lifetime hooks. One trait:

- `Drop` — `function drop(self: Self): void`, a finalizer the RC runtime
  runs when the value's last reference goes away. The value-scoped
  counterpart to `defer` (which is function-scoped: it fires when the
  enclosing call returns, no matter who still holds the value).

The call is emitted into the type's generated drop glue
(`__drop_struct_<C>` / `__drop_enum_<C>`), at the top of the rc==1 branch
— before the field releases and the box free, so the body still reads
every field. Three consequences worth knowing:

- **A `Drop` type is excluded from Perceus reuse.** Reuse hands a dying
  value's box straight to the next same-shaped constructor instead of
  freeing it, which would skip the finalizer; the optimisation is declined
  for these types.
- **`drop` fires only on the compiled backends.** The interpreter has no
  refcounts, so no value reaches rc-zero there and the finalizer never
  runs. Its tests are compiled-only for that reason.
- **It is a hook, not a guarantee.** A value the reclamation passes cannot
  prove dead never reaches rc-zero, so its `drop` never runs. Do not hang
  correctness on it firing.

### `core/map`

Generic `Map[K, V]` runtime. Open-addressing core implementing
the `Map.set` / `get_or` / `has` / `delete` / `iter` / `len` /
`keys` / `values` / `clear` methods that the checker registers.
User code calls those methods; the IR rewrites the dispatch to
the `_impl` functions here at codegen time.

**Cost note — `keys()` / `values()` allocate.** Each call builds a
*fresh* array snapshot of the column (retaining/inc-ref'ing every
element), so calling either inside a loop — or re-evaluating
`for k in m.keys()` per iteration — re-snapshots every time. For the
common "visit every entry" case prefer **`for (k, v) in m`**, which
desugars to the `MapIter` cursor (`m.iter()` / `has_next()` / `key()` /
`value()` / `advance()`) and walks entries in insertion order **without
per-iteration allocation**. Reach for `keys()` / `values()` only when
you genuinely need a materialised `K[]` / `V[]` (to sort, index, or
retain past the map's lifetime). A snapshot-free `entries()`-style
protocol for the general case is tracked in #2686.

22 internal functions:

- Layout: `__map_pow2_ceil`, `__map_hash`
- Lifecycle: `map_new_impl`, `__map_len_impl`,
  `__map_lookup`, `__map_has_impl`, `__map_get_impl`,
  `__map_get_or_impl`
- Mutation: `__map_grow`, `__map_set_impl`,
  `__map_delete_impl`, `__map_clear_impl`
- Columns: `__map_column`, `__map_keys_impl`,
  `__map_values_impl`, `__map_string_column`
- Iteration: `__map_iter_impl`, `__mapiter_has_next_impl`,
  `__mapiter_entry_addr`, `__mapiter_key_impl`,
  `__mapiter_value_impl`, `__mapiter_advance_impl`

## Built-in types

The following types are synthesised by the checker (declared in
`internal/checker/checker.go`) and don't need an import:

- `Option[T]` — `Some(T)` / `None`
- `Result[T, E]` — `Ok(T)` / `Err(E)`
- `IoError` — `NotFound`, `PermissionDenied`, `AlreadyExists`,
  `InvalidUtf8`, `Interrupted`, `Unsupported`, `Other`
- `JsonValue` — `JNull`, `JBool`, `JNumber`, `JString`,
  `JArray`, `JObject`
- `Reader`, `Writer` — stdin / stdout / stderr / file
- `HttpRequest`, `HttpResponse` — request / response shape
- `Url` — host / port / path / query / fragment parts
- `Map[K, V]`, `MapIter[K, V]` — generic associative container
  + iterator
