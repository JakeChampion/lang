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
  (`to_ascii_string` accepts a `u8`: 0..127 produces one ASCII byte,
  including NUL; 128..255 produces the empty string. Keep arbitrary bytes
  in `u8[]`, or validate a complete sequence with `utf8.from_bytes`.)
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
  `next_power_of_2` (smallest power of two `>= n`; caps at 2^30,
  returns 0 above), `log2_floor`, `sqrt_floor`, `ceil_div`,
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
  `(n) shortest_digits()` — the same shortest decimal as
  `(significand, exponent)`, for a caller doing its own layout;
  `pow10_hi(k)` / `pow10_lo(k)` / `pow10_log2(k)` — the 128-bit
  power-of-ten table that formatter runs on, for scaled arithmetic
  of the same kind (`lib/ld.fern`'s `round_to`).
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
  hardware-precise op. `asin` / `acos` / `atan`, `y.atan2(x)`
  (the angle of `(x, y)` in `[-π, π]`), `expm1` (`eˣ − 1`) and
  `log1p` (`ln(1 + x)`, both exact near zero) are instead fdlibm
  ports written in Fern, within 1 ulp of glibc on every backend.
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

A concrete element type is legal on a receiver as well — `pub function
(arr: i32[]) gcd_all(…)`, the form that would retire the naming convention
entirely — and any program can declare one. std/array cannot use it yet: the
self-hosted compiler resolves such a method but does not lower it, and a single
declaration costs an unrelated generic its type-parameter substitution, so the
convention stays until that is fixed. A receiver whose element is itself an
array or slice (`T[][]`) is rejected by E021 on both forms — dispatch binds the
element in one step and would resolve a level too deep.

- **Element-polymorphic reductions** (one bounded generic each, so the same
  call works for i32 / i64 / u32 / u64 / f32 / f64 / string and
  `@derive(Ord)` / `@derive(Eq)` element types alike):
  `sum` (`Add + Zero`) / `product` (`Mul + One`),
  `max` / `min` (`Ord` → `Option[T]`, `None`
  when empty), `sorted_asc` / `sorted_desc` (`Ord`, fresh array, input
  untouched), `count(target)` / `index_of(target)` (`Eq`; the latter
  → `Option[i32]`, `None` when absent),
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
  `cumsum_f64` (running prefix sum),
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
  value-driven form is `index_of`), `rposition`, `count_where` (tally
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
  (`T[][]` → `T[]`) is free-only structurally: a nested-array receiver is
  rejected by E021, so it cannot be spelled as a method.

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

**Word segmentation (UAX #29).** The same standard's other boundary,
and it is not `s.split(" ")` with extra steps: it keeps `can't`, `3.14`,
`1,000` and `snake_case` whole, finds the boundary in `hello,world`
with no space to help it, and works on text with no ASCII spaces in it
at all. Opt-in and DCE'd to nothing unless called, like the clusters.

- `word_segments(s): str[]` — split at every word boundary,
  **losslessly**: the pieces concatenate back to `s`, so a run of
  spaces and a punctuation mark come back as segments of their own.
  Reach for this when the gaps matter — rejoining after a transform,
  or highlighting.
- `words(s): str[]` — the word-like segments only, meaning the ones
  holding at least one letter or digit. Whitespace and punctuation runs
  are dropped, and so are emoji: a family ZWJ sequence is one *segment*
  but not a *word*.
- `word_count(s): i32` — a separate scan, so counting does not allocate
  the array.

Elements are non-copying **views**, as with `graphemes`.

The segmentation is the language-independent default UAX #29 defines,
which is right for the space-separated scripts. It is **not** a
substitute for a dictionary in Thai, Lao, Khmer or Japanese, which do
not mark word boundaries with spaces — UAX #29 says so itself. Han and
Hiragana therefore segment per code point; Katakana runs stay together.

Caveats: the per-scalar `char` methods `c.to_upper()` / `c.to_lower()` stay
**simple** (1:1) — a 1→N expansion has no single code point to return.
Greek Final_Sigma **is** applied when lowercasing (a word-final `Σ`
becomes `ς`); the locale tailorings — Turkish dotless i, Lithuanian —
are not, by design.
The tables are regenerated by `cmd/unicodegen`. The case mappings and
character classes come from Go's `unicode` package at build time. The rest
has no oracle in Go's stdlib — the normalization data (canonical
decompositions and combining classes) and `Grapheme_Cluster_Break`,
`Extended_Pictographic` and `Word_Break` — so it is read from the Unicode
Character Database files of the release Go's `unicode` tracks and checked
in: `gen_normdata.py VERSION` → `normdata.txt`, `gen_gcbdata.py VERSION` →
`gcbdata.txt` and `gen_wbdata.py VERSION` → `wbdata.txt`. The UCD is a
**regeneration-time** input only; the module itself has no dependencies.
The segmentation data is not frozen by a stability policy, so each
generated table records the version it came from, and the file header
names the version of each source. Hangul composes and decomposes
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
`std/cli`'s tty- and NO_COLOR-gated `cli_*` helpers. Each wrapper always emits the
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

Plus a seeded PCG32 generator for throughput or reproducibility, whose
state is threaded by the caller (an `i64`). It is **not** cryptographic —
two consecutive outputs pin the state — so never draw a token, a nonce or
a key from it.

- `rng_seed(seed): i64` — the initial state; equal seeds give equal
  sequences on every backend. `rng_seed_from_os()` seeds from the platform
  CSPRNG instead, for throughput without a reproducible run.
- `rng_next(state): (i64, u32)`, `rng_below(state, n)` and
  `rng_between(state, lo, hi)` — one draw and the advanced state; the
  bounded forms are unbiased (Lemire).
- `rng_fill(state, h, n): i64` — push `n` pseudorandom bytes onto the
  capacity-carrying builder `h` and return the advanced state.
  Eight bytes leave per `buf_push_u64`; the bulk builder measurements
  are recorded in #9221.
- `rng_bytes(state, n): (i64, u8[])` returns the same stream as owned
  bytes. Each eight-byte word consumes two draws, low byte first; a
  partial final word discards unused high bytes. For `n <= 0`, the array
  is empty and the state is unchanged. Use a validating text constructor
  if the bytes must become a string.
- `shuffle_seeded` / `choice_seeded` / `sample_seeded` are the array
  helpers over the same generator.

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

### `std/ordmap`

A persistent, ordered map with structural sharing: `OrdMap[K: cmp.Ord, V]`,
a weight-balanced tree (Adams / Hirai–Yamamoto, delta 3, ratio 2 — the shape
behind Haskell's `Data.Map`). Every operation returns a new map; a snapshot
(`let old = m;`) costs one pointer and shares every node, and an update
rebuilds only the O(log n) path to the key. When the input is not shared
(`m = m.insert(k, v)`), the compiler's reuse pass writes the new path into the
old nodes in place, so the same source line allocates nothing. Keys are
ordered by the bound's `cmp` (a `@derive(cmp.Ord)` struct works), iteration
is always in key order, and every node caches its subtree size, so rank
queries are O(log n).

- `ordmap_new()`, `from_arrays(keys, values)` (a repeated key keeps its
  last value)
- `(m).insert(k, v)`, `(m).remove(k)`, `(m).update(k, f)` — O(log n)
- `(m).get(k): Option[V]`, `(m).get_or(k, fallback)`, `(m).contains(k)`,
  `(m).len()`, `(m).is_empty()`
- `(m).min_key()`, `(m).max_key()`, `(m).remove_min()`, `(m).remove_max()`
- `(m).key_at(i)`, `(m).value_at(i)`, `(m).index_of(k)` — rank access
- `(m).keys()`, `(m).values()`, `(m).fold(init, f)`, `(m).for_each(f)`,
  `(m).map_values(f)`, `(m).filter(pred)` — key order
- `(m).union(o)` (left-biased), `(m).intersection(o)`, `(m).difference(o)`
  — split-and-join, O(m log(n/m + 1)); `(m).split_lt(k)`, `(m).split_gt(k)`
- `(m).is_valid()` — the invariant checker, for tests

### `std/ordset`

`OrdSet[T: cmp.Ord]`: `std/ordmap`'s tree with unit values — sorted
`to_array()`, `min` / `max` / `at(i)` / `index_of`, `add` / `remove` /
`contains`, the join-based `union` / `intersection` / `difference`,
`is_subset` / `equals`, `filter` / `fold` / `for_each`. `ordset_new()`,
`ordset_of(xs)`.

### `std/pmap`

A persistent hash map with structural sharing: `PMap[K: cmp.Hash + cmp.Eq, V]`,
a 32-way hash array mapped trie (Bagwell's HAMT, the shape behind Clojure's
and Scala's immutable hash maps). The same two paths as `std/ordmap`: a
shared input is path-copied (at most seven levels), a uniquely-held one is
updated in place. Keys are hashed by the bound's `hash` (mixed once more
here, so a weak derived hash still spreads over the trie) and compared by its
`eq`; equal-hash keys share a collision node. Iteration is in hash order —
stable for a given key set, neither insertion order nor sorted. Every branch
caches its size, so `len()` is O(1).

- `pmap_new()`, `from_arrays(keys, values)`
- `(m).insert(k, v)`, `(m).remove(k)`, `(m).update(k, f)` — O(log32 n)
- `(m).get(k): Option[V]`, `(m).get_or(k, fallback)`, `(m).contains(k)`,
  `(m).len()`, `(m).is_empty()`
- `(m).keys()`, `(m).values()`, `(m).fold(init, f)`, `(m).for_each(f)`,
  `(m).map_values(f)`, `(m).filter(pred)`
- `(m).union(o)` (left-biased; the smaller side is folded into the larger),
  `(m).intersection(o)`, `(m).difference(o)`
- `(m).is_valid()` — the invariant checker, for tests

### `std/pset`

`PSet[T: cmp.Hash + cmp.Eq]`: `std/pmap`'s trie with unit values — `add` /
`remove` / `contains`, `to_array()` (hash order), `union` / `intersection` /
`difference`, `is_subset` / `equals`, `filter` / `fold` / `for_each`.
`pset_new()`, `pset_of(xs)`.

### `std/ndarray`

A multidimensional array as a counted handle over flat storage:
`NdArray[T]` is `{ data: T[], shape, strides, offset }`, strides in
elements, and every structural operation is metadata over the same
storage. `docs/ARRAY-SHAPES.md` has the decisions and the materialization
rule; `docs/ARRAY-ALGEBRA.md` §4 is why a wrong shape aborts.

- `from_flat(data, shape)` — shares `data`; the shape must account for
  every element (`[]` is a rank-0 scalar)
- `(a).shape()`, `(a).strides()`, `(a).rank()`, `(a).len()` (elements),
  `(a).get(idx)` (a full index; out of range aborts)
- metadata only: `(a).transpose()`, `(a).permute(axes)`,
  `(a).reverse(axis)`, `(a).slice(axis, lo, hi)`, `(a).select(axis, i)`
  (the partial index, one rank less)
- `(a).reshape(shape)` — metadata when `(a).is_row_major()`, a copy
  otherwise
- `(a).packed()`, `(a).to_flat()` — a copy exactly when not
  `(a).is_packed()`
- elementwise, in reading order, as a packed handle: `(a).map(f)` (same
  shape), `(a).zip_with(b, f)` (the shape the two broadcast to, else
  abort)
- `broadcast_shape(x, y)` — the shape two shapes broadcast to, aligned at
  their last axes (an extent of 1 stretches); `(a).broadcast_to(shape)` —
  metadata only, a stretched axis at stride 0
- `(a).outer(b, f)` — `f` over every pair, shape `a.shape() ++ b.shape()`;
  `(a).inner(b, init, mul, add)` — the last axis of `a` contracted against
  the first of `b` (dot product, matrix product, matrix times vector), in
  increasing index order; extents that differ abort. Both are recognized
  by `fern -array-report`.
- `(a).fold_all(init, f)` — every element in reading order
- `(a).reduce_axis(axis, init, f)` — the fold along `axis` in increasing
  index order, one rank less; `(a).scan_axis(axis, init, f)` — the
  running fold, same shape
- `(a).map_rank(k, f)` — `f` over every rank-`k` CELL, the shape splitting
  at `rank - k` into a leading frame and the cell, as a handle of
  `frame ++ f's result shape`. Cells are views, so peeling them copies no
  element, and they arrive in increasing index order. Every cell result
  must have the same shape; one that differs aborts. With no cells — some
  frame extent is 0 — `f` never runs and the result is the empty handle of
  shape `frame`, which is the one place the result's rank depends on the
  input's extents rather than on its shape alone.

### `std/pvec`

A persistent vector with structural sharing: `PVec[T]`, a 32-way
bit-partitioned trie plus a tail buffer (the shape behind Clojure's and
Scala's persistent vectors). `get` / `with` are O(log32 n) (at most seven
levels), `append` / `pop` amortised O(1) through the tail, snapshots O(1).
Where a built-in `T[]` copies its whole buffer when a shared array is
written, `PVec` copies a path of small nodes.

- `pvec_new()`, `from_array(xs)`
- `(v).append(x)`, `(v).pop()`, `(v).with(i, x)` (out of range: unchanged)
- `(v).get(i): Option[T]`, `(v).get_or(i, fallback)`, `(v).first()`,
  `(v).last()`, `(v).len()`, `(v).is_empty()`
- `(v).to_array()`, `(v).fold(init, f)`, `(v).for_each(f)`, `(v).map(f)`,
  `(v).filter(pred)`
- `(v).concat(o)` — O(len(o)); `(v).slice(lo, hi)` — O(hi - lo), clamped
- `(v).is_valid()` — the invariant checker, for tests

Design, measurements, and the compiler work these rely on:
[`docs/PERSISTENT-COLLECTIONS.md`](./PERSISTENT-COLLECTIONS.md).

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

- `read_all_stdin(): Result[string, IoError]`: consume and close stdin,
  validating UTF-8 after collecting the complete input. Empty input is
  `Ok("")`; malformed input is `Err(InvalidUtf8("stdin"))`. I/O errors are
  propagated instead of returning partial text.
- `read_input(path): Result[string, IoError]`: validated text from stdin for
  `"-"` or `""`, otherwise from a file.
- `read_all_bytes(reader): Result[u8[], IoError]`: collect raw bytes to EOF
  without closing the caller's reader. A read failure returns an error.
- `read_all_stdin_bytes(): Result[u8[], IoError]`: collect raw stdin and
  close it, preserving a read error when closure also fails.
- `read_input_bytes(path): Result[u8[], IoError]`: raw stdin for `"-"` or
  `""`, otherwise the contents of a file. These byte APIs preserve malformed
  UTF-8 and encodings split across read boundaries.

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

### `std/deflate`

DEFLATE decoding (RFC 1951) with the zlib (RFC 1950) and gzip (RFC 1952)
framings, pure Fern. Every decoder takes `max_out`, the most bytes it
will produce, and answers `OutputLimit` past it: a compressed body is a
caller-controlled expansion, so the bound is part of the call.

- `inflate(input, max_out): Result[Inflated, InflateError]` and
  `inflate_from(input, from, max_out)` decode a raw stream;
  `Inflated { out, consumed }` says how many input bytes it took, so a
  framing can read what follows.
- `gunzip(input, max_out): Result[u8[], InflateError]` decodes every
  member in the input and checks each CRC-32 and length;
  `zlib_decode(input, max_out)` checks the Adler-32 (`adler32(bs)` is
  public). A preset dictionary is not supported.
- `InflateError`: `Truncated`, `Malformed(what)`, `OutputLimit`,
  `BadChecksum`, `BadHeader(what)`; `(e).message()`.
- No encoder yet.

### `std/hex`

Hex round-trip.

- `hex_encode(b: u8[])` (lowercase `0-9a-f`), `hex_encode_upper(b: u8[])`
  (uppercase `0-9A-F`).
- `hex_decode(s): u8[]` (lenient, either case),
  `hex_decode_strict(s): Option[u8[]]` (`None` on malformed input).

### `std/crypto`

Message digests — MD5 (RFC 1321), SHA-1 (RFC 3174), SHA-224 / SHA-256 /
SHA-384 / SHA-512 (FIPS 180-4), BLAKE2b (RFC 7693) — plus HMAC-SHA256
(RFC 2104), PBKDF2, HKDF and HOTP/TOTP. Pure Fern, verified against the RFC /
NIST known-answer vectors (`examples/tests/digest_*_test.fern`). MD5 and
SHA-1 are for interoperability (checksums, legacy protocols), not for
anything new.

Every digest is a **streaming hasher**: a value with `update` / `final`,
rebound in the cursor style (docs/CURSOR-IDIOM.md) so the pending block and
the state box are reused in place rather than copied per call:

```fern
let h: crypto.Sha256 = crypto.sha256_new();
h = h.update(chunk);              // a string of any length (a read_chunk piece)
h = h.update_bytes(arr[a:b]);     // a [u8] view: a u8[] lends itself, or a slice
let hex: string = h.final_hex();  // or h.final_bytes(): u8[]
```

A state can be forked (`let h2 = h.update("x")` while `h` stays live); each
side then owns its own copy. `final_*` reads the state without consuming it.

- Constructors: `md5_new(): Md5`, `sha1_new(): Sha1`, `sha256_new(): Sha256`,
  `sha224_new(): Sha256` (same block function, SHA-224 IV, 28-byte output),
  `sha512_new(): Sha512`, `sha384_new(): Sha512` (48-byte output),
  `blake2b_new(out_len: i32): Blake2b` (`out_len` 1..64 bytes; b2sum's `-l N`
  is bits, so pass `N / 8`; unkeyed), `sm3_new(): Sm3` (GB/T 32905-2016,
  32-byte output; `cksum -a sm3` is the one utility that offers it).
- Methods on each state type: `update(chunk: string)`,
  `update_bytes(chunk: [u8])` — both return the new state;
  `final_bytes(): u8[]`, `final_hex(): string`.
- One-shots: `md5_bytes(s: string): u8[]` / `md5_hex(s): string`, and the
  same pair for `sha1`, `sha224`, `sha256`, `sha384`, `sha512`, `sm3`;
  `blake2b_bytes(s, out_len)` / `blake2b_hex(s, out_len)`.
- The block functions are generated by `tools/gen_digests.py` (rounds
  unrolled over locals, constants as immediates) into the marked region of
  `crypto.fern`; edit the generator, not the region. Throughput on
  x86-64 (`-O`, 200 MB in 128 KiB chunks): MD5 128 MB/s, SHA-1 110,
  SHA-256 55, SHA-512 59, BLAKE2b 75 — GNU coreutils' libcrypto builds do
  350–790 MB/s on the same box; the gap is the stack-machine emitter's
  per-op spills and the rotate idiom (`(x >> n) | (x << (w - n))` lowers to
  eight instructions, not one).

Bytes are `u8[]`, not `string` (#5730): digests, derived keys, and every
key / salt / IKM / info input. The `*_hex` variants still return a
`string` — hex output genuinely is text — and the message to hash / the
password to stretch stay `string`. Pass a string's bytes to a byte-typed
parameter with `std/string`'s `s.bytes()`.

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

### `std/hash`

Non-cryptographic checksums, in std/crypto's streaming shape (`update` /
`update_bytes` rebound, `finish()` reads the value) but kept apart from it:
a CRC detects line noise, not an adversary, and this is where a future FNV /
xxHash / map hasher belongs. Bit-for-bit what GNU coreutils prints
(`examples/tests/hash_checksums_test.fern`).

- `cksum_new(): Cksum` — the CRC-32 of cksum(1): polynomial `0x04C11DB7`,
  MSB first, the byte length folded in little-endian, complemented.
  `update(chunk: string)` / `update_bytes(chunk: [u8])`, `finish(): u32`,
  `len(): u64` (the second field cksum prints), `cksum_of(s: string): u32`.
- `bsd_sum_new(): BsdSum` — `sum -r`: 16-bit rotate-and-add.
  `finish(): u32`, `len(): u64`, `blocks(): u64` (1 KiB blocks, rounded up),
  `bsd_sum_of(s)`.
- `sysv_sum_new(): SysvSum` — `sum -s`: the 32-bit byte sum folded to 16
  bits. Same methods; `blocks()` counts 512-byte blocks. `sysv_sum_of(s)`.

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

- `url_encode(s)`, `url_decode(s)`, `form_encode(s)`, `form_decode(s)`;
  an escape that does not spell UTF-8 decodes to U+FFFD, and
  `url_decode_bytes(s)` / `form_decode_bytes(s)` hand back the bytes
- `url_parse(s) Option[Url]`
- `url_resolve(base, reference) Option[string]` — RFC 3986 §5.2
  reference resolution: an absolute reference stands, a scheme-relative
  one takes the base's scheme, a path is replaced or merged onto the
  base's directory with dot segments removed, an empty reference keeps
  the base's path and (without a `?`) query; `None` when the base has no
  scheme. `url_remove_dot_segments(path)` is the §5.2.4 step on its own
  (a `..` above the root is dropped, where the request parser refuses it).
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
- `json_parse(s: string): Option[JsonValue]`;
  `json_parse_result(s): Result[JsonValue, JsonError]` with where the text
  broke
- **Typed decode:** `FromJson` is the decode half of `Json`:
  `T.from_json_value(v: JsonValue): Result[T, string]`, implemented for the
  integers, floats, `boolean` and `string`, and derived for a struct by
  `@derive(json.FromJson)` (fields decode through their own `FromJson`; an
  `E[]` field through `from_json_array[E]`, an `Option[E]` field through
  `from_json_option[E]`, `None` when the field is missing or null; a
  container nested in another is E021). `json.decode[T](text)` parses and
  decodes in one call, and a derived type also has `T.from_json(text)`. A
  shape error says where: `missing field "id"`, `field "id": expected an
  integer, got a string`, `field "tags": [1]: expected a string, got null`;
  a parse failure reads `invalid JSON: <line>:<col>: <why>`. `json_kind(v)`
  names a value's kind for such messages.
- `json_escape(s: string): string` — escape a raw string for embedding
  inside a JSON string literal (caller supplies the quotes): `\` `"`
  backslash-escaped, `\n` `\r` `\t` short escapes, other C0 controls as
  lowercase `\u00XX`, everything else byte-for-byte. The one shared
  escaper — the `JsonValue` encoder, `@derive(json.Json)`, and
  `std/log`'s JSON-lines mode all route through it.

### `std/http`

HTTP/1.1 request parsing, response builders, wire-format
serializer.

- **Response builders:** `http.ok(body)`, `http.created(body)`,
  `http.text(status, body)`, `http.not_found()`, `http.bad_request(body)`,
  `http.internal_error(body)`, `http.redirect(location)`,
  `http.no_content()`; typed-body variants that set `Content-Type` up
  front: `http.json(body)` / `http.json_status(status, body)` /
  `http.html(body)` / `http.plain(body)`; `http.problem(status, title,
  detail)` for an RFC 9457 problem document
- **Bodies:** `HttpResponse.body` is a `Body`: `BodyText(string)`,
  `BodyBytes(u8[])`, `BodyStream(Stream)` (the stream's remainder),
  `BodyFile(string)` (a path) or `BodyChunks((i32) => Option[u8[]])` (a
  producer asked for chunk 0, 1, 2, … until it answers None).
  `http.bytes(status, bytes)`, `http.stream(status, stream)`,
  `http.file(path)` and `http.chunks(status, next)` build
  the last four. `(resp).body_string()` (an ill-formed sequence in a
  byte-domain body reads as U+FFFD: unlike `(req).body_string()` it has no
  error arm, a response being the handler's own), `(resp).body_bytes()` and
  `(resp).body_len()` read whichever a response carries (`(body).bytes()`
  is the read on the `Body` itself), a producer's chunks
  joined; `(resp).body_text()` is the checked decode, `None` for bytes that
  are not UTF-8, for a response that came off the wire (`std/fetch`'s);
  a `BodyFile` reads as empty from a handler, since a handler may not
  reach the file system (E080), and `http_materialize(resp)` reads one whole
  for a test. The serve loop produces a file body and a chunks body as the
  socket takes them, after the handler has answered: a file is opened and
  streamed from a `Reader` under its size as the `Content-Length`, moved
  from the file to the socket by `sendfile(2)` where the target has it
  (Linux and Darwin; `tcp_sendfile(fd, file, max)` is the builtin, -ENOTSUP
  on wasm, where the loop reads and sends each piece), or answered 404 when
  it cannot be opened; chunks go out under chunked transfer
  coding to an HTTP/1.1 client, and close-delimited (the connection ending
  with the body) to an HTTP/1.0 one, so the length need not be known up
  front. A response to HEAD, or with a 1xx, 204 or 304 status, produces no
  body either way. `http_serialize_response_head(resp, keep_alive, framing)`
  is the head alone, the framing line (`Content-Length` or
  `Transfer-Encoding`) the caller's.
- **A chunked body as it arrives:** `chunk_decoder(limits)` is a
  `ChunkDecoder` at the start of a chunked body; `(d).feed(bytes)` answers
  `ChunkData(next, data)` — the data decoded from this feed, a chunk's
  passed on as it arrives, and the decoder to feed next — `ChunkEnd(data,
  trailers, rest)` once the body is complete, `rest` the bytes past it, or
  `ChunkRefused(status)`. Its rules are the parser's (the framing budget,
  the body cap, the extensions, the trailer section), so a body fed in any
  pieces answers what the parser answers for the whole; the serve loop's
  streamed request bodies read through it.
- **Request body:** `HttpRequest.body` is a `Stream` over the bytes as
  they came, or over a source that pulls them as the handler reads
  (`std/stream`); `(req).body_string(): Result[string, BodyError]` is the
  whole body as text, `Err(NotUtf8)` when it is not well-formed UTF-8 and
  `Err(EndedEarly(fault))` when a streamed body ended early (413 past the
  cap, 408 stalled, 400 malformed or cut short), so a handler declared as
  `Result[HttpResponse, http.BodyError]` reads `let text: string =
  req.body_string()?;` and refuses either with the right status.
  `(req).body_bytes()` is the whole body as it came; `(req).body_len()` its
  length, the declared `Content-Length` for a streamed body (-1 when
  chunked).
- **Typed JSON body:** `body_json[T](req): Result[T, BodyError]` decodes the
  body as a `T: json.FromJson` and tells the failures apart:
  `UnsupportedMediaType(ct)` when the `Content-Type` is not
  `application/json` or a `+json` type (or is missing), `NotUtf8` when the
  body is not well-formed UTF-8, `MalformedJson(e)` with std/json's
  `JsonError`, `WrongShape(why)` naming the field. `BodyError` is
  `ToResponse` (415 / 400 / 400 / 422, as RFC 9457 problems), so a handler
  declared as `Result[HttpResponse, http.BodyError]` reads
  `let item: Item = http.body_json[Item](req)?;`.
- **Header methods:** `(resp).with_header(name, value)` (set) /
  `(resp).with_appended_header(name, value)` (append) /
  `(resp).with_content_type(ct)`, and `(resp).with_trailer(name, value)`
  for `HttpResponse.trailers`: fields the serve loop sends after a
  `chunks` body's last chunk, named in the head's `Trailer` field (RFC
  9110 §6.5). A body with a length, an HTTP/1.0 client's close-delimited
  stream and the wasi-http wrapper's outgoing body carry none, so there
  they are dropped. `http_serialize_fields(map)` writes a map as field
  lines, and `http_fields_ok(map)` says whether every field can be: a
  token name and a value with no control byte but HTAB. The serve loop
  answers a response whose headers or trailers fail it with a bare 500
  instead, so a value copied from the request (a decoded path can hold
  CR LF) cannot add a field or end the head.
- **Response parsing:** `http_parse_response_framed(buf, method, eof,
  limits): HttpResponseFraming` is the client side of the wire, the
  request parser's twin: `Complete(HttpResponseFramed { response, len,
  version, keep_alive })` once the whole response is in the buffer,
  `Unfinished` while it is not, `Rejected(code)` for one it will not read
  (400 malformed, 413 a body past `limits.body`, 431 a header block or
  chunk framing past its budget, 501 a coding under `chunked`, 505 another
  major version). `eof` says the peer closed, which delimits a body with
  neither `Content-Length` nor `Transfer-Encoding` (RFC 9112 §6.3); a
  response to HEAD, or with a 1xx, 204 or 304 status, has no body
  whatever its headers say. `http_parse_response_framed_from(buf, from,
  …)` parses behind an interim response. `http_hop_by_hop(name)` and
  `http_end_to_end(map)` name and strip the fields a connection owns
  (`Connection` and the fields it lists, `Keep-Alive`, `Proxy-Connection`,
  `Transfer-Encoding`, `TE`, `Trailer`, `Upgrade`).
- **Request writing:** what a client may put on the wire, the parser's
  refusals turned outward: `http_method_ok(method)` (a token),
  `http_method_idempotent(method)` (GET, HEAD, PUT, DELETE, OPTIONS and
  TRACE, the methods a client may send again after a reset before any
  response byte, RFC 9110 §9.2.2),
  `http_target_ok(target)` (an origin-form request-target: `/`, `pchar`
  and `/`, then a query of `pchar`, `/` and `?`, every `%` followed by
  two hex digits; `%2F` passes, since what a decoded segment may hold is
  the server's rule), `http_field_name_ok(name)` (a token) and
  `http_field_value_ok(value)` (no control byte but HTAB and no DEL).
  `std/fetch` checks each before it connects, so a CRLF in a caller's URL
  or header cannot split the request it is written into.
- **Request builder:** `request(method, path)` is a request to hand a
  handler in a test (no headers, no body), and `(req).with_header(name,
  value)`, `(req).with_body(body)` (with the `Content-Length` a client
  sends) and `(req).with_json(body)` (`Content-Type: application/json`
  too) build it up; it reads the way a served request does. With a
  `MockPlatform`'s bag and `std/test`'s `assert_status` /
  `assert_header` / `assert_no_header` / `assert_body`, a handler is
  tested without a socket (`examples/tests/http_request_builder_test.fern`).
- **Errors a handler answers with:** a handler's helpers fail with `?`
  over `Result[T, E]` and `respond(result)` turns a
  `Result[HttpResponse, E]` into the reply: `Ok(r)` is `r`, `Err(e)` is
  `e.to_response()`, for any `E` implementing `ToResponse`
  (`function to_response(self): HttpResponse`). `problem(status, title,
  detail)` is the RFC 9457 problem-details response
  (`application/problem+json`; a title of `""` reads as the status text,
  a detail of `""` is left out); `HttpError { status, title, detail }`
  carries one as an error value and `fail(status, detail)` builds it
  titled with the status text. `json.JsonError` answers 400 titled
  "Malformed JSON" with where the text broke, and a `string` error
  answers 500 with the status text alone, since an internal message is
  for the log rather than the peer. std/fetch's `FetchError` answers 504
  for a `Timeout`, 500 for an `InvalidRequest` (a method, header or body
  the handler wrote that cannot go on the wire) and 502 for the rest, an
  `InvalidUrl` among them since the URL may have come off the request,
  without its message, which names upstream hosts and addresses
  (`examples/tests/http_respond_test.fern`).
  A `Result[HttpResponse, dyn error.Error]` goes through
  `respond_error(result, plat)` instead: the error's `message()` is
  written to `plat.log` and the reply is the bare 500 problem
  (`respond_error_with(pair, plat)` for the state-threading pair).
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
- **Wire format:** `http_parse_request_bytes(buf: u8[]): Option[HttpRequest]`
  reads a request as it came off the wire and keeps owned copies of what a
  handler reads (the method, the path, each header, the body), so the wire
  buffer is the connection's to reuse; `http_parse_request(buf: string)` is
  the same parse over text, one copy dearer.
  `http_parse_request_framed(buf: u8[]): HttpFraming` is the parse a
  persistent connection needs, answering `Framed(HttpFramed)`,
  `Incomplete` (keep reading), `Continue` (the header block has arrived
  and says `Expect: 100-continue`, the body has not: the loop sends
  `100 Continue` once and keeps reading, RFC 9110 §10.1.1; an HTTP/1.0
  request's expectation is ignored, as that section asks) or
  `Malformed(status)` (the loop answers `status` with an empty body and
  `Connection: close`, then closes; a malformed request behind an
  answered one gets no answer, since that answer already said close,
  RFC 9112 §9.6):
  `HttpFramed { request, len, keep_alive }`
  says how many bytes the request occupied at the head of the buffer (so
  the next pipelined request can be found behind it) and whether the
  connection outlives it (RFC 9112 §9.3: HTTP/1.1 unless `Connection:
  close`, HTTP/1.0 only with `Connection: keep-alive`; `Connection` is
  read as a comma-separated token list and only a whole token counts,
  so `Connection: upgrade` with an `Upgrade` header is ignored, the
  request answered as HTTP/1.1 on a connection that persists, as RFC
  9110 §7.8 allows a server with no protocol to switch to;
  a higher HTTP/1 minor version is read as HTTP/1.1, RFC 9112 §2.3).
  `http_parse_request_framed_from(buf, from)` is the same parse over
  `buf[from, len)`, so a loop answering pipelined requests moves an
  offset instead of copying the buffer forward, with `len` counted from
  `from`. The path a handler sees is the request-target (§3.2) as
  `http_request_target(method, target)` gives it: an origin-form target
  (`/a/b?q`) or an absolute-form one (`http://host/a/b?q`, accepted from
  any client, §3.2.2) becomes the path decoded once and with its dot
  segments removed (RFC 3986 §5.2.4), then the query as it came (its
  decoding depends on what reads it); `*` is kept for an OPTIONS. The
  target is refused with 400 when it is none of those, its authority is
  empty, a byte in the path or the query is outside the URI grammar, a
  `%` is not followed by two hex digits, a decoded byte is NUL or a slash
  (`%2F` hides a segment from the path's grammar), or the path climbs above
  the root (`/../x`: a client resolves that before sending). There is no
  lenient mode: a request line whose method is not a
  token or whose version is not `HTTP/` a digit `.` a digit (§2.3; a
  major version other than 1 is refused with 505 rather than read under
  HTTP/1's framing), a header line without
  a colon or whose name is not a token (so whitespace before the colon,
  §5.1, and obs-fold, §5.2, both refuse), a value holding a control byte
  other than HTAB (RFC 9110 §5.5), a bare CR or LF (§2.2),
  `Transfer-Encoding` beside `Content-Length` or naming any coding but a
  single `chunked` (§6.1, §6.3), a duplicate, non-numeric or overflowing
  `Content-Length` (§6.3), an HTTP/1.1 request without a `Host`, any
  request with two or with one holding anything but visible ASCII (§3.2),
  a request line over 8 KiB, a header block over 32 KiB or 100 fields, or
  a body over 1 MiB is malformed, and a violation is refused as soon as it
  is known: a request past a cap once the cap is passed, a bad request
  line once its CRLF has arrived, before the rest of the request. The
  status names the violation: 400 for the grammar, the `Host` rule and a
  `Transfer-Encoding` list whose last coding is not `chunked` (RFC 9112
  §6.3),
  413 for a body past its cap, 414 for a request line past its cap, 417
  for an `Expect` other than `100-continue`, 431
  for a header block past its byte or field cap or chunk framing past its
  budget, 501 for a coding under the final `chunked` the parser cannot
  decode, 505 for
  another major version. One empty line before the request line is
  ignored, as §2.2 asks, and counted in `len`; a second is refused. A
  chunked body (§7.1, HTTP/1.1 only) is decoded into `request.body`:
  chunk extensions are skipped when well-formed (§7.1.1: a `;`, a name
  token, at most one `=` with a token or quoted-string value, whitespace
  only before the `;` and around the `=`) and refused otherwise, trailers are
  read under the header rules into `request.trailers`, kept apart from
  the headers (nothing knows their semantics, so none may merge,
  RFC 9110 §6.5.1; a Content-Length body's are empty), the decoded bytes are held to
  the body cap, and the framing (chunk-size lines, extensions, CRLFs and
  the trailer section) to another header block's bytes, so a
  chunk-flood cannot hold more buffer than any other request. The caps
  are an `HttpLimits { request_line, header_bytes, header_fields, body }`:
  `http_limits()` is 8 KiB, 32 KiB, 100 fields and 1 MiB, what
  `http_parse_request_framed` uses; `http_parse_request_framed_from(buf,
  from, limits)` takes them, and a serve loop reads them from
  `serve.Config.limits`. `http_request_bytes_cap(limits)` (header block,
  blank line, body and chunk framing, each at its cap) is the most a
  connection buffers without a complete request.
  `http_header_value(block, key)` reads a raw header block by the same
  rules, so a block the parser would refuse names no header.
  `http_serialize_response(resp): string` writes `Connection: close`;
  `http_serialize_response_conn(resp, keep_alive)` writes `keep-alive` or
  `close` as the serve loop decided; `http_serialize_response_to(method,
  resp, keep_alive)` is what the loop sends, with no body on a response
  to HEAD (its `Content-Length` kept) or a 1xx, 204 or 304 (neither),
  whatever the handler put in the body (RFC 9112 §6.3). The loop adds a
  `Date`, formatted once per second, unless the handler set one.

### `std/net`

IP addresses, socket addresses, and the typed error every networking
primitive reports (#9853). `IpAddr` is `V4(bytes)` / `V6(bytes)` in network
order, built with `ipv4(a, b, c, d)`, `ipv6(bytes)` or `ip_parse(text)` and
rendered by `to_string()` in the RFC 5952 canonical form; `SocketAddr` is an
address and a port, parsed by `socket_addr_parse` from `a.b.c.d:port` and
the bracketed `[v6]:port` form. The predicates (`is_loopback`,
`is_private`, `is_link_local`, `is_multicast`, `is_unspecified`) read the
address as given; `to_canonical()` unwraps an IPv4-mapped IPv6 address for
them. `is_global()` is the one an outbound block list wants: false for
every block that is not reachable from anywhere (unspecified, loopback,
private, link-local, shared 100.64/10, the documentation and benchmarking
nets, multicast, reserved, broadcast, unique-local, 2001:db8::/32), and an
IPv6 address carrying an IPv4 one (IPv4-mapped, NAT64's well-known
64:ff9b::/96, 6to4) answers for the address it carries; one in NAT64's
local-use 64:ff9b:1::/48 is global only under every prefix length the
operator may pick. `(a).embedded_v4(bits)` is the address a NAT64 address
carries under a prefix of `bits` in RFC 6052's layout. `packed_v4()`
bridges an `IpAddr` to the packed IPv4 argument `tcp_connect` takes.

`NetError` is a closed enum (`AddrInUse`, `ConnectionRefused`,
`WouldBlock`, … and `Other(errno)`); `error_from_errno(n)` maps the errno a
builtin returns, negated or not, onto it using the Linux, Darwin or WASI
numbering `target_os()` names, and `errno()` is the inverse.
`internal/stdlib/net_errno_test.go` pins the three tables to
`internal/strerror`.

The socket controls are typed faces over the descriptor builtins
`tcp_listen_with` and `tcp_socket_ctl`, on the same `i32` descriptors the
`tcp_*` builtins and `std/tcp` use:

- `listen_at(addr, opts)` — a listener bound to a `SocketAddr` of either
  family with `ListenOptions { backlog, reuse_port }`; `::` takes every
  interface of both families (`IPV6_V6ONLY` is cleared, whatever the
  host's default) except on wasi:sockets, which keeps an IPv6 socket
  IPv6-only.
- `listen_with(port, opts)` — `listen_at` on every IPv4 interface at `port`, with `ListenOptions { backlog,
  reuse_port }` (`listen_options()` is `tcp_listen`'s 128 and one
  listener per port), or the `NetError` the bind or listen reported.
- `set_nodelay(sock, on)`, `set_keepalive(sock, on)`,
  `set_nonblocking(sock, on)` — `TCP_NODELAY`, `SO_KEEPALIVE` and
  `O_NONBLOCK`, each `Result[(), NetError]`. `TCP_NODELAY` is already on
  for every socket `connect`, `connect_start` and `accept` answer and every
  connection the serve loop accepts, as Go and Node have it; `false` turns
  Nagle's coalescing back on. A non-blocking `read` answers `WouldBlock`
  at once when nothing is queued, and a non-blocking `write` what the
  kernel took, or `WouldBlock` when it had no room.
- `read(sock, buf)` — one read from a connected stream into `buf`, up to
  its length: the byte count, `Ok(0)` at the end of the stream, or the
  `NetError`. On wasi:sockets every read is non-blocking, so a reader
  waits for readability first (`async.wait_any`).
- `write(sock, data)` — `data` written to a connected stream: the bytes
  accepted (fewer than `data` holds when a non-blocking socket fills), or
  the `NetError`; a write never raises SIGPIPE.
- `send_queue(sock)` — how many bytes handed to the socket the peer has
  not acknowledged yet, unsent and in flight alike (`SIOCOUTQ` on Linux,
  `SO_NWRITE` on Darwin), as `Result[i32, NetError]`: how far the peer has
  got with what was written, which is what a minimum data rate on a
  response is judged by.
- `local_addr(sock)` — the address and port a socket is bound to, as
  `Result[SocketAddr, NetError]`: the address the host picked for a
  connected socket, `0.0.0.0` or `::` for a listener on every interface.
- `peer_addr(sock)` — the address and port of the peer a socket is
  connected to, an IPv4 peer of a dual-stack listener as the `V4` it is
  rather than the v4-mapped `V6` the socket reports; `Other(ENOTCONN)`
  for a socket with no peer.
- `peer_key(sock)` — the peer's address as one 32-bit key, or `None` for
  a socket with no peer: an IPv4 address packed as `tcp_connect` takes
  it (the first octet in the low byte), a v4-mapped IPv6 address its IPv4
  one, any other IPv6 address the two words of its first eight bytes
  XORed, so the peers of one /64 share a key. What
  `serve.Config.max_connections_per_ip` counts by.
- `shutdown(sock, how)` — `Shutdown.Read`, `Write` or `Both`; a write-side
  shutdown is the end of stream the peer's `tcp_recv` reads as EOF.

On wasm, `set_nodelay`, `set_nonblocking` and `send_queue` answer
`Other(58)` (`ENOTSUP`): wasi:sockets 0.2 has neither control and no
reading of the queue, and `reuse_port` is ignored there.

The datagram sockets are typed faces over `udp_bind`, `udp_connect`,
`udp_sendto_bytes` and `udp_recvfrom`, on the same descriptors:

- `udp_socket(addr)` — a socket bound to a `SocketAddr` of either family
  (port 0 lets the host pick), receiving from any peer until
  `set_peer(sock, peer)` fixes one.
- `send_to(sock, data, to)` and `send(sock, data)`: send one `u8[]` datagram
  to `to` or the fixed peer and return the byte count. Empty arrays send empty
  datagrams. The payload remains available to the caller. Convert text with
  `string.bytes(text)` from `std/string` before sending it.
- `recv_from(sock, buf)` and `recv(sock, buf)` — one datagram into the
  caller's `u8[]`, up to its length: the byte count, with the sender as a
  `SocketAddr` from `recv_from`. A non-blocking socket with nothing queued
  answers `WouldBlock`.
- `local_port(sock)` and `close(sock)` — the bound port, and the release,
  of a socket of either kind.

Unix-domain sockets, native only (no WASI world has a filesystem namespace
for sockets, so a program naming them does not compile for wasm):
`listen_unix(path, backlog)` is a stream listener at the path (`AddrInUse`
while a socket file is there, `Other(ENAMETOOLONG)` past 107 bytes on
Linux and 103 on Darwin), `connect_unix(path)` a connection to it
(`Other(ENOENT)` when nothing is there), and `accept(sock)` the next queued
connection of any listener.

Connecting: `connect(addr)` is a connection to a `SocketAddr` or the error
the dial reported; `connect_start(addr)` is a non-blocking socket whose
connect is under way, and `connect_result(sock)` says how it ended,
`Ok(())` once connected, `Err(InProgress)` while still under way, else the
failure (`ConnectionRefused` for a closed port). The interpreter connects
before `connect_start` answers, so there the result is `Ok(())` at once.

The stream verbs on a TCP connection (`recv` / `send` over owned buffers),
Unix-domain sockets and IPv6 listeners arrive with the primitives that make
them honest on every address family; until then `std/serve` is the accept
loop and `std/fetch` the client.

### `std/dns`

A stub resolver (#9855): the RFC 1035 wire codec, the two files glibc's
resolver reads, a query over UDP that retries over TCP when the reply is
truncated, the lookups on top, and the dialer that races a name's
addresses. Reaches `fs` for the files, `tcp` for the sockets, `random`
for query ids, `now` for the wait and `reactor` for the race.

- `Message`, `Question`, `Record`, `RData` — a message as data, `RData`
  being `A(IpAddr)`, `AAAA(IpAddr)`, `CNAME(name)`, `PTR(name)` or
  `Raw(bytes)` for a type the module does not read. `query(id, name,
  qtype)` is a recursion-desired question; `with_edns(m)` appends the
  EDNS0 OPT record advertising `udp_payload()` (4096) bytes; `encode(m)`
  writes the wire form, names uncompressed; `decode(bytes)` reads one,
  following compression pointers that only point backwards, and answers
  `Malformed` for anything that does not fit. `records_for(m, name,
  rtype)` reads the answer section for a name, following its CNAME chain
  to the owner that has the records, and `answer_records` turns a reply's
  rcode into the lookup's result.
- `parse_hosts(contents)`, `hosts_lookup(entries, name)` (every address
  of a name, both families, caselessly) and `hosts_canonical(entries,
  name)` read `/etc/hosts`; `parse_resolv_conf(contents, host)` reads
  `/etc/resolv.conf` with res_init's defaults and caps (`nameserver` of
  either family, at most three, one with an IPv6 zone skipped; `domain`
  and `search`, the last one winning, the hostname's domain when neither
  is given; `options ndots:n` up to 15, `timeout:n` up to 30 s,
  `attempts:n` up to 5, `rotate`). `RES_OPTIONS`, `LOCALDOMAIN` and
  `HOSTALIASES` are not read.
- `search_plan(name, conf)` and `search_candidates` are res_search's order:
  a name with at least `ndots` dots is asked as it stands before the
  search domains, one with fewer after them, a name ending in a dot only
  as it stands.
- `exchange(ns, q, timeout)` is one query to one nameserver within a
  `Duration`: UDP with EDNS0, a reply to another id or question ignored,
  a truncated reply asked again over TCP (`exchange_tcp`);
  `exchange_many(ns, qs, timeout)` sends several at once, each on its own socket, and awaits
  the replies as one set. `ask(conf, q)` and `ask_many(conf, qs)` are
  res_send over the nameservers, `attempts` times round, from a rotating
  start under `rotate`; SERVFAIL, REFUSED, silence and an unreachable
  server hand a query to the next, and the queries a server did answer
  stay answered while the rest go on.
- `lookup_a`, `lookup_aaaa` and `canonical_name` walk the search plan over
  `ask`; `lookup_records_with` and `search_with` take the asker as a
  function, which is how the walk is tested with a scripted nameserver.
  `lookup_addresses(conf, name)` asks for the A and AAAA records together
  at each step of the walk (`combine_pair` is how their two results
  become one) and answers the addresses in RFC 6724 order. `DnsError` is
  `NoSuchName` (NXDOMAIN), `NoData` (a clean reply without the record),
  `NoReply` (the timeout), `ServerFailure`, `Refused`, `Malformed`,
  `NoNameserver` or `Socket(NetError)`, and implements `error.Error`.
- `order_addresses(dsts, srcs)` is RFC 6724's destination address
  selection: each destination beside the source the host would use for
  it (`source_for(dst)`, a datagram socket connected to it and asked for
  its local address; `None` for no route, which puts the destination
  last), then matching scope, matching label, higher precedence, smaller
  scope and the longest common prefix, ties keeping the server's order.
  `policy_of`, `scope_of` and `common_prefix_len` are the table and the
  measures the rules read; `sort_addresses(dsts)` probes and orders.
- `connect_race(addrs, port, opts)` is the RFC 8305 dialer: the first
  address is tried alone for `DialOptions.fallback` (300 ms, Go's
  attempt delay), then the next beside it, and so on, an attempt that
  fails handing its turn to the next at once; the first to connect wins
  and the rest are closed, `TimedOut` once `DialOptions.timeout` (10 s)
  passes.
  `interleave_families(addrs)` is §4's order, the families alternating
  from the first address's. `dial(name, port, opts)` resolves and races.
- `nat64_prefixes(conf)` reads the prefixes a NAT64 translator answers
  under from the AAAA records of `ipv4only.arpa` (RFC 7050;
  `nat64_prefix_of` reads one record, the well-known 192.0.0.170 or .171
  embedded under one of the six prefix lengths), and `synthesize(prefix,
  v4)` embeds an IPv4 address under one as RFC 6052 lays it out.
- `system_conf()` and `system_hosts()` read this machine's files, and
  `resolve(name)` is the lookup a program wants by default: an address
  literal as it is, then the hosts file, then DNS, the addresses in RFC
  6724 order. On a host with no IPv4 route, IPv4 addresses with no IPv6
  beside them are synthesized under the prefixes the nameservers reveal
  (RFC 8305 §7.3). `resolve_plain(name)` is the same lookup before that
  synthesis and the ordering, for a policy that judges the addresses as
  the sources name them, and `synthesized(addrs)` applies both to its
  answer; `nat64_carried(addr)` is the IPv4 address `addr` carries under
  a prefix the nameservers reveal. `.local` names go to the nameservers
  like any other (no mDNS).

`examples/tests/dns_test.fern` covers the codec, the files, the plan, the
walk and the ordering rules; `TestDnsExchangeX86_64`, `TestDnsPairX86_64`
and their self-host twins drive the exchange against a nameserver on the
loopback interface, over UDP, through the TCP retry, against one that
stays silent, and with the A and AAAA queries together. Under the
interpreter, whose poll is a stub, `TestDnsPairInterp` and
`TestDnsPairInterpReadsTheReadySocket` cover the sweep the paired wait
falls back to; the second answers AAAA alone, which only the sweep reads.

### `std/serve`

The HTTP/1.1 server: `Config`, its defaults `config()`, and the entry
points that run a handler under it.

- `run(port, cfg, handler)` — HTTP/1.1 serve loop on `::`, every
  interface of both families, with an IPv4 peer counted by
  `max_connections_per_ip` as the IPv4 address it is; on `0.0.0.0` where
  the host has no IPv6 (the family refused, or the address not there to
  bind) and on wasm, whose `::` listener would be IPv6-only. Calls
  `handler(req: HttpRequest, plat: platform.Host): HttpResponse` once
  per request, with the worker's host platform. The loop
  is a reactor: one readiness set from the Driver seam watches the
  listener and every open connection, so slow clients are read side
  by side, and each connection's request read is bounded by a 10 s
  deadline (the slow-loris guard). A response the kernel does not
  take whole stays with its connection, watched for writability
  until it drains within the same span. Connections persist (RFC
  9112 §9.3: HTTP/1.1 unless the request says `Connection: close`,
  HTTP/1.0 only when it says `Connection: keep-alive`), requests
  pipelined on one connection are answered in order, 32 per readiness
  event before the loop returns to the wait (the rest are answered on
  the next wait, which returns at once, so one pipeline cannot hold
  the other connections off the reactor), the responses to one event
  corked into a single write (a response with a streamed or file body,
  or one behind which no complete request is buffered, ends the cork),
  and every response names
  the outcome in its `Connection` header: once the peer's end of
  stream has been read, the last buffered request's response says
  `close`, since the close follows it. A partial request behind a
  complete one waits under the read deadline, armed once the loop turns
  to it; only an empty buffer waits under the idle span; and a
  connection with a complete request still to answer is not waiting at
  all, so no deadline closes it. A write the kernel refuses closes the
  connection, and so does a malformed request: without a response when
  it arrived first, and behind an answered one whose response then says
  `close`, since the close follows it.
- `Config { backlog, reuse_port, recv_deadline, min_data_rate,
  data_rate_grace, response_min_data_rate, response_data_rate_grace,
  keep_alive_idle, keep_alive_requests, max_connections,
  max_connections_per_ip, max_in_flight, workers, shutdown_grace,
  readiness_path, drain_deadline, limits, stop_with_parent, stream_bodies }`
  (`config()` is
  128, one listener per port, the 10 s deadline, 240 bytes per second after
  5 s for a request body and the same for a response, 130 s, 1000, 1024, 100
  and 1024): the
  accept queue
  depth, port sharing between listeners (`SO_REUSEPORT`, ignored on
  wasm; under `supervise` each worker then binds a
  listener of its own instead of inheriting the supervisor's, the group
  steered by the CPU a connection arrived on where the host can, Linux,
  and by the kernel's hash elsewhere; what a worker's listener holds
  unaccepted when the worker dies is lost with it unless the kernel
  migrates it, `net.ipv4.tcp_migrate_req=1`, where the inherited
  listener keeps it for the next worker; a listener handed in through
  `LISTEN_FDS` stays the shared one), the read deadline, the least rate a
  request body must keep arriving at once its header block is in (after
  the grace, the body may take as long as its bytes buy at that rate
  beyond the read deadline, so a large upload that keeps flowing is read
  and a trickle is closed), the least rate a response must keep being
  drained at once a write came up short, under a grace of its own (a
  response the peer keeps taking above the rate goes out whole however
  long that takes, and one the peer stops reading is cut off after the
  grace; 0 turns either rate off), how long an idle persistent connection waits for
  its next request, how many requests one connection may carry before
  its last response says `Connection: close` (a value below 1 behaves as
  1), how many connections the loop holds open at once (a value below 1
  behaves as 1; at the cap the
  listener is not read, so further connections wait in its accept queue,
  `backlog` deep, the kernel refusing past it, until one closes), how
  many handlers the loop keeps parked on their waits at once
  (`max_in_flight`: a handler whose `plat.http` waits on its upstream parks
  and the loop serves other connections meanwhile; at the cap the listener
  is not read until one finishes), how
  many of them one client may hold (`max_connections_per_ip`, counted by
  the peer's address key, `net.peer_key`, and by each worker's loop
  alone: a connection past it is closed as it is accepted, without a
  response; on by default at 100, which clients behind one NAT or one
  reverse proxy share, so a server behind either sets it to 0 for no
  cap), and
  how many workers `supervise` forks (`workers`; 0, the
  default, is one per processing unit the process may use, what
  `cpu_count()` answers), and the shutdown SIGTERM or SIGINT starts (SIGHUP
  keeps its default, ending the process): the loop keeps
  accepting for `shutdown_grace` (2 s, since whoever routes to it removes
  it in parallel), answers 503 on `readiness_path` ("" for none), then
  closes the listener, ends keep-alive, closes the connections with
  nothing in flight and gives the rest `drain_deadline` (30 s) from the
  signal to finish before closing them; the loop returns 0 once every
  connection is gone and 1 when it cut one off, so `main` exits with it.
  `limits` (`http.http_limits()`) are the parser's caps on each request,
  so `serve.Config { ...serve.config(), limits: http.HttpLimits {
  ...http.http_limits(), body: 65536 } }` refuses a body past 64 KiB with
  413 before the handler runs. `stop_with_parent` (false) starts the same
  shutdown when the process's parent exits, as a SIGTERM would; each
  worker `supervise` forks has it set, so a supervisor
  killed outright (SIGKILL, a crash) takes its workers down rather than
  leaving them serving as orphans. `stream_bodies` (false) starts a handler
  on its request's header block rather than once the body has arrived
  whole: the body is a `Stream` that pulls the bytes as the handler reads
  them, parking the handler on the connection under the minimum data rate
  while the loop serves other connections (docs/NET-P3-SUSPENSION-PLAN.md
  §3.9). The body cap still holds (a declared length past it is refused 413
  before the handler, a chunked body past it ends the stream with 413), a
  body under the data rate ends it with 408, `Expect: 100-continue` is
  answered when the handler first reads, so a handler that refuses never
  invites the body, and a connection whose body the handler did not read
  whole closes after the response. A `Stream` that ended early faults
  (`(s).fault()`); `body_string()` reports it as `EndedEarly`. The stateful
  loop (`run_with`) reads bodies whole either way.
  A listener it cannot bind is `serve: cannot listen on ADDR:PORT:` —
  `[::]` where the host has IPv6, `0.0.0.0` where it has not and on
  wasm — and the error's text on stderr, and the entry returns 98 (every
  entry, and a supervised worker that binds its own).
  A listener the process was started with (`LISTEN_FDS` at least 1,
  descriptor 3) is served instead of a fresh one.
- `run_shutdown(port, cfg, handler, shutdown)` and
  `run_with_shutdown(port, cfg, init, handler, shutdown)` —
  `run` and `run_with` with a hook the loop calls
  once it has stopped, before returning: `shutdown(reason)`, or
  `shutdown(reason, state)` with the state as the last request left it,
  where a counter is flushed or a store closed. The reason is the signal
  that started the shutdown, "sigterm" or "sigint", when every request in
  flight was answered after it and "drain-deadline" when one was cut off.
- `run_with(port, cfg, init, handler)` — the same loop with a
  caller-owned state value threaded through it: the handler is
  `(S, HttpRequest, Platform) => (S, HttpResponse)` and the state
  it returns is what the next request receives. The loop's frame
  owns it, so it lasts as long as the process — this is how a
  handler keeps a cache or a counter, the language having no
  module-level mutable state.
- `supervise(port, cfg, handler)` — crash-only serving: the
  accept loop runs in forked workers the parent reforks on
  death (docs/CRASH-ONLY-SERVE.md). It forks `workers` workers
  (one per processing unit by default), each running its own loop over
  the one listener, watched exclusively (epoll's `EPOLLEXCLUSIVE`) so a
  connection wakes one of them, or with `reuse_port` over a listener of
  its own; whichever dies is replaced, and SIGTERM or SIGINT is forwarded
  to every worker and waited for, the exit being the worst code a worker answered
  it with. Eight deaths in a row within 100 ms of a fork are a give-up:
  the workers still serving are stopped the same way, and the exit is
  the last death's code. `supervise_with(port, cfg, init,
  handler)` threads a state as `run_with` does: built once before
  the first fork, every worker inherits a copy, and a worker forked
  again after a death starts from that copy, not from where the dead one
  left it — a counter is per worker and lost on refork; state that must
  outlive a crash belongs in a store the handler reaches through `plat`.
  `supervise_shutdown(port, cfg, handler, shutdown)` and
  `supervise_with_shutdown(port, cfg, init, handler, shutdown)`
  take the hook of the `_shutdown` entries, which each worker's loop calls
  on its way out. Where there is no fork (the interpreter) every one of
  them serves single-process. A worker runs one handler at a time, and
  what a waiting handler costs it depends on the compiler: built by the
  self-host compiler, a handler that waits on `plat.http` or on a streamed
  request body parks, and the worker serves its other connections until
  the wait is answered (`TestSelfHostServeHandlersOverlap`;
  `docs/NET-P3-SUSPENSION-PLAN.md`); built by the Go compiler, every wait
  blocks and the handler holds the worker until it returns. A handler
  that computes holds its worker either way
  (`TestSelfHostSupervisedServeHandlerStallsItsWorker`), so workers, not
  connections, absorb CPU-bound handlers.
- `__port_from_env(name, fallback)` — env-var port lookup used
  by the auto-`main`-from-`handle()` synthesis so handler-shaped
  programs can be tuned via `PORT=N ./bin`. That synthesis serves
  `handle` under the supervisor: `supervise` with
  `config()`, or `supervise_with` when the program
  defines an `init` answering the state a state-taking `handle` threads
  (docs/PLATFORM-RESEARCH.md Rec §3). `init` takes nothing or the
  platform (`__init_platform()`, the host bag with no reactor, since it
  runs once in the supervising parent), and answers nothing, the state
  `S`, the `serve.Config` alone, or `(serve.Config, S)`; a config it
  answers replaces the defaults. Both compilers know the config by its
  module, whatever the program imports std/serve as, so a struct of the
  program's own named `Config` is a state like any other. On a target
  without processes (wasm32-wasi) the synthesis serves through `run` and its
  `_with` / `_shutdown` twins instead. Mismatching `init` and `handle` about the
  state, or an `init` taking anything else, is E075. A top-level
  `shutdown(reason)`, or `shutdown(reason, state)` beside a
  state-threading handler, sends the synthesis to the `_shutdown` entries
  with the hook; a hook whose state parameter disagrees with the
  handler's is E075 too. A `handle` declared as `Result[HttpResponse, E]` (or
  `(S, Result[HttpResponse, E])` with state), so its body fails with
  `?`, is accepted by both compilers: they rename it
  `__fern_handle_result` and synthesise the plain `handle` calling
  `http.respond` (or `respond_with`) over it — `respond_error` (or
  `respond_error_with`) with the handler's platform when `E` is
  `dyn error.Error` — so every consumer, the
  synthesised main and the wasi-http entry included, keeps the
  HttpResponse-shaped entry.

### `std/tcp`

- `tcp_recv_deadline(fd, max, deadline): Recv` —
  recv bounded by a readability deadline: `Chunk(bytes)` in time
  (empty = EOF), `Elapsed` at the deadline, `Abandoned` when the task
  waiting was cancelled first (`async.cancelled()`). On interp (where
  `poll` is a stub) it degrades to a blocking recv.

The raw socket primitives `tcp_listen` / `tcp_accept` /
`tcp_local_port` / `tcp_recv` / `tcp_send` / `tcp_close` are
runtime-provided, emitted by codegen from extern stubs at module
boundary — not declared in this module.

`tcp_local_port(sock)` answers the port a socket is bound to, or a
negative errno. It is what makes `tcp_listen(0)` usable: the host
picks the port there, and a server that wants a free one can now
bind and then report where it is instead of naming a port and
hoping nothing else holds it.

### `std/fetch`

Outbound HTTP/1.1 client (the upstream-fetch half of the edge use
case). A request is a value built from a URL and sent blocking; the
answer is `Result[HttpResponse, FetchError]`.

- **Requests:** `get(url)` / `request(method, url)`, then
  `(req).with_header(name, value)`, `(req).with_text(s)`,
  `(req).with_bytes(bs)`, `(req).with_body(body)` (text, bytes, a stream
  or chunks; a `BodyFile` is refused, since the client never reads a
  file), `(req).with_timeouts(t)`, `(req).with_limits(l)`. `std/url`
  parses the URL; the host is resolved by `std/dns` (a literal, the hosts
  file, then DNS) and its addresses raced as `dns.connect_race` does. An
  IPv4 address in the URL is four decimal octets or refused
  (`2130706433`, `0177.0.0.1`, `127.1` read as loopback to some resolvers
  and as a name to others).
  Before it connects the client checks the method, the path and query
  and every header against `std/http`'s `http_method_ok` /
  `http_target_ok` / `http_field_name_ok` / `http_field_value_ok`, so a
  CRLF in a URL or a field cannot split the request on the wire. It
  writes `Host` and
  `Content-Length` itself and strips hop-by-hop fields from what it sends.
  No TLS where the client dials (`https` fails with `Tls`). On
  `wasm32-wasi-http` the client dials nothing:
  the request goes to the host's wasi:http/outgoing-handler (`std/wasi_http`),
  which resolves the name, connects, speaks TLS (so `https` works there)
  and HTTP/2 where it can, under the connect and inactivity bounds as
  `request-options`; the response comes back whole under the body cap,
  and every rule above the transport (redirects, decoding, the request
  checks, the retry) is the same.
- **Redirects:** a 301, 302, 303, 307 or 308 with a `Location` is
  followed, `(req).with_redirects(hops)` bounding the hops (10 by
  default; 0 answers the 3xx as data), every hop under the one total
  bound. The `Location` is resolved against the request's URL by
  `url.url_resolve`. A 307 and 308 keep the method and body; a 303, and
  a 301 or 302 answering a POST, become a GET without the body. When a
  hop leaves the origin (scheme, host, effective port) `Authorization`,
  `Proxy-Authorization` and `Cookie` are dropped; `Host` is written per
  hop.
- **Decoding:** the client writes `Accept-Encoding: gzip` unless the
  caller wrote an `Accept-Encoding` of their own, and undoes the `gzip`
  (`x-gzip`) codings a response names, from the last applied, under
  `Decoding { depth, ratio }` (`decoding()` is one coding and a
  hundredfold growth; `(req).with_decoding(d)`; `depth: 0` asks for none
  and undoes none) and the body cap, which the decoder never exceeds
  (`BodyLimit`); `Content-Encoding` and `Content-Length` are dropped from
  a decoded response. The ratio is judged on the response as a whole once
  every coding is undone, and a decoded body of 64 KiB or less is never
  refused on it (a small body compresses far past any plausible ratio,
  and the cap bounds it; a `ratio` of 0 or less admits only that much).
  More codings than `depth`, a body that is not gzip, or one grown past
  what `ratio` allows fail with `Decode(what)`, a refused body naming the
  allowance that refused it. An empty body (a
  HEAD or 204 answer may still name a coding), a coding the client did
  not ask for, and a caller's own `Accept-Encoding` leave the body as it
  came. Request bodies are never compressed.
- **Retry:** a request the peer resets before any response byte (a
  reset or abort on the socket, a broken pipe, a close with nothing
  read) is sent once more when its method is idempotent
  (`http.http_method_idempotent`: GET, HEAD, PUT, DELETE, OPTIONS,
  TRACE), on a fresh connection, under the same total bound. A POST is
  never resent. On `wasm32-wasi-http` nothing is: the host reports a
  connection closed before any response byte as its protocol error, the
  same as a malformed response, so the client cannot tell that nothing
  happened.
- **Sending:** `send(req)` from a program with a `main`;
  `plat.http(req)` from a handler, the capability-scoped route (a
  `MockPlatform` answers what `http_set` canned).
  The handler's route reaches global addresses only: once the host has
  resolved, an address `net.is_global` refuses (loopback, a private or
  link-local block, the cloud metadata address, an IPv4 address carried
  inside an IPv6 one) fails the request with `Blocked` before anything is
  dialled, so a rebinding record is caught too. The check reads the
  addresses as the network names them, before NAT64 synthesis, and an
  address under a NAT64 prefix the nameservers reveal is judged by the
  address it carries (`dns.nat64_carried`). `send` has no such rule. On
  `wasm32-wasi-http` neither route has one: the host resolves names and
  owns the network, so its outbound policy (Spin's
  `allowed_outbound_hosts`, what `wasmtime serve` is run with) is the
  rule there, and a loopback or private address is the host's to refuse.
- **Proxies:** both routes go through the forward proxy the environment
  names, read through `config_get` as `ProxyEnv` (`proxy_env()`,
  `proxy_env_from(...)` for the pure form): the lowercase `http_proxy`
  only, as curl reads it (a CGI host maps a client's `Proxy:` header onto
  the uppercase name), and none under `REQUEST_METHOD`; `no_proxy` (or
  `NO_PROXY`) lists the hosts reached directly as `*`, a domain (with a
  leading dot, its subdomains only), an address, a CIDR block of either
  family, any of them with a `:port`, zones ignored. `localhost` and
  loopback are never proxied. `(p).proxy_for(url)` is the pure decision.
  A proxied request carries the absolute-form target, the origin's
  `Host`, and `Proxy-Authorization: Basic` from the proxy URL's
  credentials. On the handler's route the origin is still resolved and
  checked before the request goes to the proxy (the proxy's own address
  is the deployment's choice and goes unchecked), so a deployment where
  only the proxy can resolve names reaches it through `send`. On
  `wasm32-wasi-http` the environment's proxy is the host's own to go
  through, and the client reads none.
- **Responses:** the same `HttpResponse` the server side builds, with
  `BodyBytes`, headers as the server spelled them less the hop-by-hop fields, a
  chunked body decoded and its trailers in `trailers`, interim 1xx
  responses stepped over, a bodiless 204 / 304 / HEAD answer honoured.
  The status is data: `(resp).ok_or_status(): Result[HttpResponse, i32]`
  turns anything outside 2xx into `Err(status)`. `(resp).body_text()`
  is the checked UTF-8 read, since an upstream can serve anything.
- **Errors:** `FetchError` is a closed sum over the phase that failed:
  `InvalidUrl(what)` (unparseable, no scheme or host, a scheme other than
  `http` / `https`, or a path or query that cannot stand on a request
  line), `InvalidRequest(what)` (a method that is not a token, a header
  that cannot be written as one line, or a file body; `what` names the
  rule, never the value), `Dns(DnsError)`, `Connect(NetError)`,
  `Blocked(what)` (a host the handler's route may not reach, naming the
  address), `Tls(what)` (`https` where no host speaks TLS, or the
  host's handshake failure),
  `Timeout(Phase)` with `Phase` one of `Connecting` / `Inactivity` /
  `Total`, `Protocol(what)` (a response the parser refuses, interim
  1xx responses past one `limits.header_bytes` between them, or a 101
  the client did not ask for),
  `Io(NetError)` (a send or read the kernel refused, and a peer that
  closed before any response byte, as `ConnectionReset`), `BodyLimit` (a
  body past `limits.body`, decoded or not), `Decode(what)` (a content
  coding the client could not undo), `Redirect(what)` (more hops than
  `redirects`, a 3xx without a `Location`, or a `Location` that is not a
  URL), `Host(what)` (a wasi-http host refusing or failing the request
  outside any phase this client owns: denied, a loop detected, its
  configuration, an internal error), and `Cancelled`, which no path
  produces yet. On `wasm32-wasi-http` the host's `error-code` is read
  into these by its case: DNS cases to `Dns`, the connect and TLS
  cases to `Connect`, `Timeout` and `Tls`, what the host would not send
  to `InvalidRequest` / `InvalidUrl`, what it could not read to
  `Protocol`, `BodyLimit` and `Decode`. `(e).message()`. It is
  `http.ToResponse`, so a handler fetching upstream fails with `?`
  (504 / 500 / 502, under "Errors a handler answers with" above).
- **Timeouts:** `Timeouts { connect, inactivity, total }`, each a
  `Duration`; `timeouts()` gives 10 s / 30 s / 60 s. The connect bound covers the
  whole address race; inactivity is the longest wait for the next byte
  of the response; total runs from the start to the last byte read.
- **Transport:** the dialled route reaches the network only through
  `trait Transport` (`now_ns`, `lookup`, `connect`, `write`, `read` under
  a wait answering `tcp.Recv`, `close`, and `idle`, its pool), and
  `send_on(tr, req, policy)` is
  `send` over any transport under a `Policy { public_only, proxies }`.
  `sockets()` is the machine's (the system resolver, `dns.connect_race`,
  `tcp_recv_deadline`), which `send` and `plat.http` use; `std/sim_fetch`
  scripts one in virtual time.
- **Pool:** a connection whose response was framed (a length or chunked,
  not ended by the close), that the peer did not ask to close, and that
  nothing followed is kept in the transport's `Idle` pool under its host,
  port and route (the open route's connections are never handed to the
  guarded one). The next idempotent request to the same place takes the
  connection kept last; a POST always dials, since a kept connection the
  peer has closed shows itself only after the request is written, and an
  idempotent request that finds it closed is sent once more on a new one.
  `idle()` keeps up to 64 connections for 90 s each, `idle_limited(max,
  idle)` sets both, the second a `Duration`, and `sockets_with(pool)` dials with a given pool.
  `send` keeps a pool for the one call (so a redirect back to the same
  origin is followed on its connection) and closes it after; a caller that
  holds a `Sockets` across `send_on` calls reuses across them and closes
  what is left with `close_idle(tr)`; `send_public_on(tr, req)` is the
  public-only route over a caller's transport, which the host platform's
  `plat.http` takes over the platform's own pool. Every rule above (the block list, the bounds, the retry,
  redirects, decoding) stays above the seam, so a scripted transport
  exercises the same client a real one does. `lookup` answers a `Lookup`:
  the addresses to dial and, when asked to be judged, the addresses the
  network named with the ones they carry, which the block list reads.
- **Awaitable:** `fetch_future(host_be, port, path):
  async.Future[u8[]]` resolves to the response body (empty on any
  failure, a `path` that `http_target_ok` refuses included, which never
  connects) — fan out through `async.gather` / `async.race` /
  `async.with_deadline`. `ipv4(a,b,c,d)` packs the dotted-quad it and
  `tcp_connect` take.

### `std/headers`

HTTP `HeaderMap` with case-insensitive lookup, multi-valued
entries, and insertion-ordered iteration. Backs `HttpRequest`'s
`headers` and `trailers` and `HttpResponse`'s `headers` and `trailers`.

- `header_map_new()` — empty map.
- `(h).set(name, value)` / `(h).append(name, value)` — replace vs.
  add a value. A name keeps the spelling it was given, and names compare
  ASCII-case-insensitively (RFC 9110 §5.1), so `set` replaces any
  spelling of the name and the entry takes the spelling passed to it.
  The parser keeps the client's spelling, and the serializer writes each
  name as the handler spelled it.
- `(h).get(name): Option[string]` (first value) /
  `(h).get_all(name): string[]` (every value) / `(h).len()`.

### `std/stream`

Byte-stream value backing `HttpRequest.body: Stream`: a `data: u8[]`
buffer with a `pos` cursor, and for a body that arrives as it is read a
`BodySource` the reads pull the next chunk from once the buffer is
exhausted (docs/NET-P3-SUSPENSION-PLAN.md §3.9), so the whole is never
held. `BodySource { next: () => Option[u8[]], fault: Cell[i32] }`:
`next` answers None at the end and on every call after, `fault` why the
stream ended early (an HTTP status, or `async.cancelled()`).

- Constructors: `stream_from_bytes(bs)`, `stream_from_string(s)`,
  `stream_empty()`, `stream_from_source(src)`.
- Readers: `(s).read_byte()`, `(s).read_n(n)`, `(s).read_line()`,
  `(s).read_all()`, `(s).read_all_string()`. `read_line` reads an
  ill-formed sequence as U+FFFD, since its `None` means end of input.
- `read_all_string(): (Option[string], Stream)` validates only the unread
  bytes. It returns `Some(text)` for valid UTF-8 and `None` for malformed
  bytes, advancing the returned cursor to EOF either way. EOF yields
  `Some("")`. The original value and its bytes remain available.
- Introspection: `(s).len()`, `(s).remaining()`, `(s).is_empty()`
  describe the buffer — the whole body of an in-memory Stream, the chunk
  being read of a sourced one — and `(s).fault()` is a sourced stream's
  fault, 0 until it ends early.

### `std/io_buffered`

In-memory `BytesWriter`: accumulate bytes and strings, then extract the
buffer as bytes or validate it as text. Extraction leaves the writer usable.

- `bytes_writer_new()`; `(w).write_string(s)`, `(w).write_bytes(bs)`,
  `(w).write_byte(b)`.
- `(w).into_bytes(): u8[]` preserves arbitrary bytes.
- `(w).into_string(): Option[string]` yields `Some` for valid UTF-8, including
  an empty buffer, or `None` for malformed bytes. Validation happens after all
  writes, so one scalar may span multiple writes.
- `(w).len()`, `(w).is_empty()`, `(w).reset()`.

`ByteLineReader` reads arbitrary byte records without decoding them as text.

- `byte_line_reader_new(reader, term, chunk_size)` borrows the reader. Close
  the reader yourself after consuming the cursor.
- `(lr).next_line_bytes(): (Option[u8[]], ByteLineReader)` returns the next
  record, including its terminating byte when present. It preserves a final
  unterminated record. Rebind the returned cursor after each call.
- `(lr).next_chunk_bytes()` has the same return type. It first returns any
  unread suffix of the buffered chunk, then subsequent chunks from the reader.
- Each result owns its bytes and remains valid after later reads or closing
  the reader. Records spanning chunks use a growing buffer.
- `None` means EOF or a read failure; `(lr).error()` distinguishes them. Read
  failures are sticky. A partial record accumulated before a failure is
  returned once, with the error already available on the returned cursor.

`LineReader` provides validated UTF-8 records through the same cursor pattern.
Construct it with `line_reader_new(reader, term, chunk_size)` and close the
borrowed reader after use.

- `(lr).next_line(): (Option[string], LineReader)` validates each complete
  record, including its terminator or final unterminated tail. Scalars may
  span physical reads without producing an error.
- `(lr).next_chunk()` returns complete scalars. It retains an incomplete
  suffix for the next call and may read again when a scalar exceeds the read
  size. Returned chunk boundaries can differ from physical read boundaries.
- Both operations preserve unread bytes when switching between them. Every
  returned string remains valid after later reads or closing the reader.
- Malformed or truncated UTF-8 stops the cursor with `InvalidUtf8("")`
  available through `(lr).error()`. An earlier I/O failure takes precedence.
  A valid partial line preceding an I/O failure is returned once. `None`
  without an error means EOF. Use `ByteLineReader` for arbitrary bytes.

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

### `std/tz`

The local time zone as `tzset(3)` finds it, for a program that prints a
wall-clock time: a TZif file (RFC 8536, the v2 64-bit table and the POSIX
rule in its footer) or a POSIX TZ string, answering the UTC offset and
the abbreviation in force at an instant. `std/time`'s `TimeZone` is a
fixed offset by construction; a `Zone` is the function from an instant to
that offset.

- **Loading:** `local_zone()` (`TZ`, else `/etc/localtime`, else UTC),
  `zone_from_tz(spec)` (a file under `TZDIR` or `/usr/share/zoneinfo`,
  else a rule such as `EST5EDT,M3.2.0,M11.1.0`), `parse_tzif(bytes)`,
  `parse_posix(s)`, `fixed_zone(off, name)`, `utc_zone()`.
- **At an instant:** `(z: Zone).offset_at(sec)`, `.abbrev_at(sec)`,
  `.is_dst_at(sec)`, `.entry_at(sec)` (all three at once), `.at(sec)`
  (the `time.TimeZone` valid then), `.civil(sec)` / `.local_fields(sec)`
  (the broken-down local time).
- A header count the file cannot hold, a type index past the table or a
  truncated block is a parse failure, never an allocation of the claimed
  size. Reaches `env`, `fs` and `now`.
- `parse_tzif` validates complete designation strings and the bounded POSIX
  footer as UTF-8. Malformed text returns `None`; bytes outside those fields
  do not become strings. Empty or absent fields keep their existing behavior.

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
- `with_deadline(deadline, fs)` — await all within a `Duration`, yielding
  `Option[T][]`: `Some(v)` for each that resolved in time, `None` for
  one abandoned at the deadline.
- `Driver` (trait) and `real_driver()` — the waiting seam every
  combinator's `*_on(drv, …)` sibling takes (`docs/DST-PLATFORM-BRIEF.md`):
  `poll_ready`, `now_ns`, `timer` and `drop_token` are the one-shot waits,
  and `watch(fd, interest)`, `unwatch(fd)`, `wait(max, timeout_ms)` and
  `close()` are the reactor, a readiness set that outlives one wait
  (interest and readiness bits 1 readable, 2 writable, readiness 4 an
  error or hang-up; `wait` is the (fd, readiness) pairs of up to `max`
  ready descriptors, empty on the timeout). The real driver's reactor is
  the `reactor_*` floor, made on the first watch; `std/sim`'s answers from
  the readiness a test scripts with `ready_at(fd, at_ms, bits)`, its
  virtual clock advancing to the earliest one an interest selects. A host
  may report readiness spuriously, so a reader reads until -EAGAIN, and a
  descriptor is unwatched before it is closed. `watch_signal(sig)` makes a
  signal a readiness event, reported as the pair (-sig, 1) and no longer
  ending the process (-ENOTSUP on wasm), `unwatch_signal(sig)` restores
  its default; in the sim `ready_at(-sig, at_ms, 1)` scripts a delivery.
  `watch_parent()` reports the parent's exit the way a watched SIGTERM is
  reported, (-15, 1): -ESRCH when the parent is already gone, found
  reparented to init (a subreaper other than init, Linux, hides that),
  -ENOTSUP on wasm; the sim's parent never exits.
  `std/serve`'s serve loops run on it.
- `Task[T]`, `task_new(entry)`, `task_start(t)`, `task_resume(t, woke)`,
  `task_cancel(t)`, `task_free(t)`, `TaskStatus[T]` (`Done(T)`,
  `Suspended(Wait)`, `Cancelled`), `Wait` (`set`, `timeout_ms`) and
  `wait_any(set, timeout_ms)` — a call chain that parks on a set of (fd,
  interest) pairs under a bound and runs on when its scheduler resumes it
  with the index of the ready pair, -1 at the timeout (`docs/ASYNC.md` §8,
  `docs/NET-P3-SUSPENSION-PLAN.md`). `wait_any` blocks when no task is
  current, which is how std/tcp's recv-with-deadline and std/dns's connect
  and exchange waits work in a plain program; under a scheduler the
  self-host compiler lowers every function that reaches it to a resumable
  form, and the native compiler keeps the blocking fallback, where
  `task_start` runs its entry to completion. `cancelled()` is what a
  cancelled task's waits answer from then on.
- `gather_tasks(entries, on_incomplete)`, `race_tasks(entries, none_val)`
  and `with_deadline_tasks(deadline, entries)` — the task combinators: each
  runs its `() => T` entries as tasks of the calling task and parks on the
  union of their waits. `gather_tasks` answers every result in order,
  `race_tasks` the `(index, value)` of the first to finish with the rest
  cancelled, `with_deadline_tasks` `Some(value)` for each finished in time
  and `None` for each cancelled at the deadline. A cancelled child leaves
  through its own exit paths, `defer`s included, and a task that is itself
  cancelled cancels its children. The `Future` combinators above park too
  when called inside a task. Under the blocking fallback the entries run to
  their end in order, so the first wins every race.

### `std/platform`

The capabilities a handler is handed: `platform.Platform` is a trait, and
every handler takes an implementation of it as its last parameter
(`handle(req: HttpRequest, plat: platform.Platform)` —
[`docs/PLATFORM-RESEARCH.md`](./PLATFORM-RESEARCH.md) Rec §1). Host
effects are reached as methods on the value the handler was handed, so a
handler can only reach what it was given. The free functions
(`eprint`, `now_unix_ms`, …) stay for programs that are not handlers;
a function handed a platform that reaches one, directly or through a
helper, is refused at check time (E080, `internal/ambient`, mirrored by
`examples/self_host/ambient.fern`).

A parameter typed by the trait makes the handler generic over it
([`docs/TRAITS.md`](./TRAITS.md)), so each implementation is compiled in
statically: `platform.Host`, which std/serve's accept loops and the
wasi-http entry hand a handler, and `mock_platform.MockPlatform`, which
records what a test's handler tried to do. Each keeps its own state as
ordinary fields (`Host { reactor }`, the worker's reactor, 0 outside a
serving worker).

Each method needs its target capability (`internal/platforms`), so
what a handler may call depends on where it is going: the `wasi-http`
proxy world grants log / now / random / config / fetch, and `.env` is an
E066 there.

Under std/serve's loop a handler's `plat.http` is a wait the loop can
park: in a self-host-built server the handler's call chain saves itself
at the wait and the worker serves its other connections until the
upstream answers, up to `Config.max_in_flight` parked handlers at once
(`docs/ASYNC.md` §8, `docs/NET-P3-SUSPENSION-PLAN.md`); in a Go-built
server the same call blocks the worker.

```
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    plat.log(req.method + " " + req.path);
    return http.ok("ok");
}
```

- `host()` — the host platform with no reactor, for a test driving a
  handler directly or a caller of `serve.run` handing its own handler
  what the accept loop would; `host_on(reactor)` is the one std/serve
  builds per worker loop.
- `(plat).log(msg)` — one line to the platform's log sink (`log`).
- `(plat).now_ms()` — wall-clock ms since the epoch (`now`).
- `(plat).elapsed_ns()` — monotonic ns, for measuring (`now`).
- `(plat).env(name)` — one variable from the invocation environment,
  `Option[string]` (`env`).
- `(plat).config(name)` — one deploy-time configuration value,
  `Option[string]` (`config`). On a target with an environment it is the
  variable of that name; on the `wasi-http` proxy world it is
  `wasi:config/store`, which `wasmtime serve -S config
  -S config-var=NAME=VALUE` serves.
- `(plat).secret(name)` — `config` for a value that must not be logged:
  the same lookup, and a mock records only the name.
- `(plat).random_i32()` — one draw from the platform CSPRNG
  (`random`).
- `(plat).http(req)` — `req` sent and its response read (`fetch`); the
  host sends it with `fetch.send_public_on` over the platform's own pool,
  so a handler reaches global addresses only, and a connection the peer
  kept open carries the platform's next request to the same place — for
  the life of the worker loop under std/serve.

### `std/sim_fetch`

A scripted network for `std/fetch`'s client, in virtual time. A
`sim_fetch.Net` is a `fetch.Transport`, so `fetch.send_on(n, req, policy)`
runs the whole client against names, listeners and answers the test
scripts, on a `sim.Sim`'s clock:

```fern
let d: sim.Sim = sim.new(1);
let n: sim_fetch.Net = sim_fetch.net(d)
    .host("example.test", ["93.184.216.34"])
    .listen("93.184.216.34", 80, 5)
    .route("93.184.216.34", 80, "/", [sim_fetch.reply(200, "hi").after(20)]);
let got = fetch.send_on(n, fetch.get("http://example.test/"), policy);
// d.now_ns() is 25 ms: 5 to connect, 20 to the first byte.
```

Nothing waits: a read that would block moves the clock to when the
scripted bytes arrive, or by the whole wait when none do, so each bound
passes at its exact virtual time. Every move is `Sim.advance_to`, a park
under `sim.run_tasks(drv, entries, cancel_at_ms)`, which runs its entries
as tasks in virtual time and cancels task `i` at `cancel_at_ms[i]` (-1 for
never), a scripted disconnect; a task cancelled while it waits reads
`Abandoned`, the client's `Cancelled` (`docs/ASYNC.md` §8).
`host(name, addrs)` names addresses (an
IP literal resolves to itself, anything else is `NoSuchName`);
`listen(addr, port, connect_ms)` accepts after a delay, and an address
with no listener refuses at once. A name's addresses are raced as
`dns.connect_race` races them, each attempt starting the fallback delay
after the one before (at once after a refusal), the first to connect
winning and none within the client's connect bound a timeout at it; a
bound of zero times out before any attempt, refusing addresses included.
`route(addr, port, path, answers)` answers the
n-th request for `path` (`*` for any) with its n-th `Answer`, the last
repeating; an unrouted request gets a 404. Answers: `reply(status, body)`,
`redirect(status, location)`, `raw(text)` / `raw_bytes(bytes)` for
anything else on the wire, `reset()` (closed before a byte) and `silent()`
(never answers), shaped by `.after(ms)`, `.in_chunks(size, every_ms)`,
`.held()` (left open after the last byte) and `.kept()` (no `Connection:
close`, and the connection takes the next request). `reply` and
`redirect` announce the close that follows them. The client's pool is the
net's own, `fetch.idle()` unless `.pooling(pool)` gives another. The net
records what the client
did: `connects()`, `open()`, `sent_count()` and `sent(i)`, the i-th request
as written. A sibling of `std/sim` rather than part of it, so `std/sim`
keeps the clock and randomness alone; `examples/tests/sim_fetch_test.fern`
is the client's parity suite.

### `std/sim_platform`

A `platform.Platform` over the simulation: a handler runs on it as it runs
on the host, in virtual time. `sim_platform.new(d, n)` reads the clock and
the seeded PRNG of the `sim.Sim` `d` (`now_ms`, `elapsed_ns`,
`random_i32`), sends `plat.http` over the `sim_fetch.Net` `n` by the
host's public-only route with the network's pool keeping the connection
(parking under `sim.run_tasks`, so handlers interleave in virtual time),
and answers `env`, `config` and `secret` with what the test set
(`env_set`, `config_set`, `secret_set`). Every call is recorded as a
mock's: `calls()` and `reset()` read and clear the log, a secret logged by
its name alone.

```fern
let d: sim.Sim = sim.new(1);
let n: sim_fetch.Net = sim_fetch.net(d).host("example.test", ["93.184.216.34"]).listen("93.184.216.34", 80, 5);
let plat: sim_platform.SimPlatform = sim_platform.new(d, n);
plat.config_set("REGION", "eu-west");
let resp: HttpResponse = handle(req, plat);
```

`examples/tests/sim_platform_test.fern` is the platform's parity suite.

### `std/mock_platform`

A `platform.Platform` that records instead of acting. Handed to a handler
in place of the host, every capability call the handler makes lands in
the mock's log and nothing reaches the host:

```fern
let m: mock_platform.MockPlatform = mock_platform.mock_platform_new();
let resp: HttpResponse = handle(req, m);
assert_eq(m.calls()[0].name, "log");
```

Mocked capabilities answer what the test canned, else a fixed value — 0
for `now_ms` / `elapsed_ns` / `random_i32`, `None` for `env` / `config` /
`secret`, `Err(Connect(ConnectionRefused))` for `http`, and `log`
swallows the line.

- `mock_platform_new()`.
- `(m).env_set(name, value)`, `(m).config_set(name, value)`,
  `(m).secret_set(name, value)`, `(m).now_set(ms)`, `(m).elapsed_set(ns)`,
  `(m).random_set(v)`, `(m).http_set(method, url, status, body)` — the
  answer the mock gives from then on (the last one canned wins; a request
  with another method or URL still answers the refusal). A canned answer is a row
  of the same cell the log lives in (`canned`, tab, key, tab, value, the
  value escaped), which `calls()` skips and `reset()` keeps; the mock's
  own `(plat).can(key, value)` / `(plat).canned(key)` are what the
  setters and the capability methods use
  (`examples/tests/mock_platform_canned_test.fern`).
- `(m).record(name, args)`, `(m).reset()` — mutate through the shared cell,
  so a bag already handed to a handler writes to the same log.
- `(m).calls()`, `(m).call_count()`, `(m).has_call(name)`,
  `(m).find_call(name)`.

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
    let r: test.TestRunner = test.test_new("arithmetic");
    r = r.it("addition", test_addition);
    return r.finish();
}
```

Free functions are reached through the `test.` module prefix
(`test.test_new`, `test.assert_eq`, `test.fail`); the runner type
is `test.TestRunner`; receiver methods (`.it`, `.finish`, `.skip`)
stay bare.

- **Runner:** `TestRunner` (struct), `test_new(suite)`,
  `test_new_verbose(suite)`, `(r).it(name, body)` — `body` is the
  test passed UNEVALUATED, a bare function name or `() => …`, so the
  runner's filter and fail-fast controls can decline to run it —
  `(r).finish() -> i32`
- **Skips & subsuites:** `(r).skip(name, reason)`,
  `(r).skip_if(cond, name, reason, body)`,
  `(r).subsuite(name)` — toolchain-gated
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
  messages wrap both the actual and the expected `Display` form
  in `"…"`, so an empty or whitespace-only value is still
  visible in the diagnostic
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
  inclusive bounds; all four reject an inverted range
  (`lo > hi`) as a caller bug rather than blaming the side
  the value fell outside, and the float variants fail on NaN
  in the value **or in either bound** — every comparison
  against a NaN bound is false, so "not below and not above"
  would otherwise report a pass having asserted nothing
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
- **HTTP:** `assert_status(resp, status)`, `assert_header(resp, name,
  value)` (the name in any case; a missing header fails too),
  `assert_no_header(resp, name)`, `assert_body(resp, text)` — a
  handler's response, built from `http.request(method, path)` and a
  `MockPlatform`'s bag
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
    let r: TestRunner = test_new("fuzz");
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
- Columns: `__map_i32_column`, `__map_bool_column`, `__map_u8_column`,
  `__map_keys_impl`, `__map_values_impl`, `__map_string_column`,
  `__map_ptr_column`
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

## Built-in functions

Free functions every program can call without an import — `print`, `args`,
`read_file`, `isatty`, … — are declared by the checker
(`internal/checker/checker.go`) and classified per target in
`docs/FREESTANDING-CORE.md` and per package in
`docs/PACKAGE-CAPABILITIES-BRIEF.md`. Two of them are compile-time constants
rather than runtime calls, one per half of the `-target` name:

### `target_os(): string`

The environment half of the `-target` name the program is compiled for —
`"linux"`, `"darwin"`, `"android"`, `"wasi"`, `"wasi-http"` or
`"freestanding"` — and never the compiler's host: `fern -target arm64-linux`
on a Mac says `linux`. Android is its own value because it is its own
environment (a different object format and loader); the two wasm worlds are
named as the target spells them.

The compiler replaces the call with a string literal before type-checking
(`internal/constfold`, `examples/self_host/constfold.fern`), and the IR fold
turns `"linux" == "darwin"` into a constant and drops the dead arm, so a
branch on it costs nothing at runtime and the other arm's code and strings
never reach the binary:

```fern
function block_bytes(): i32 {
    if (target_os() == "darwin") { return 1024; }
    return 4096;
}
```

Compare the call itself, as above. A string held in a local is not
propagated into a comparison, so `let os: string = target_os(); if (os == …)`
evaluates the comparison at runtime — correctly, just not for free.

Under `fern -interp` the program runs where the compiler runs, so the value
is the host's operating system as Go names it. `fern -check` with no
`-target` leaves the call alone and types it as a `string`.

Capability enforcement (E066) runs on the tree-shaken AST, before the fold,
so a builtin the target lacks is refused inside a dead arm too:
`if (target_os() != "wasi") { proc_fork(); }` does not compile for
`wasm32-wasi`. The constant selects between behaviours every target
provides; it does not gate a capability.

It needs no capability (core in `internal/platforms`, ungated in
`internal/caps`) and has no `std/` wrapper: `std/platform` is the `Platform`
bag a handler is handed at run time, and a fact fixed at compile time does
not belong on a value a mock can substitute.

### `target_arch(): string`

The ISA half of the same name — `"arm64"`, `"x86-64"` or `"wasm32"`. Everything
above holds for it unchanged: same fold before the check, same dead-arm drop,
same absence of a capability, same host answer under `-interp` (with Go's
`amd64` spelled `x86-64`, the name the target uses).

The two halves answer different questions and a program usually wants only one.
Reach for `target_arch()` when the difference is the machine rather than the
host — register widths, an instruction only one ISA has, or a C ABI detail like
the format of `long double`, which is 80-bit x87 on x86-64 and IEEE binary128 on
arm64 and wasm32. Reach for `target_os()` when it is the kernel or the loader.
Some questions need both halves: `long double` is that same ISA question
everywhere except Darwin, where the environment narrows the type to plain
`double` — `coreutils/lib/ld.fern` is the worked example.
