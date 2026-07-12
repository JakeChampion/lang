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
  `is_digit`, `is_alpha`, `is_alnum`, `is_ascii`,
  `is_ascii_white_space`, `is_newline`, `is_vowel`,
  `is_printable`, `is_control`, `is_letter`, `is_hex_digit`,
  `is_punct`, `is_lower`, `is_upper`, `matches_any`,
  `hex_digit`, `digit_value`, `hex_value`, `to_lower`,
  `to_upper`, `to_ascii_string`
- **Sign / classification:** `signum`, `is_positive`, `is_negative`,
  `is_zero`, `is_in_range`, `is_between`, `is_multiple_of`,
  `is_perfect_square`, `is_palindrome`, `is_even`, `is_odd`,
  `is_power_of_2`, `is_prime`
- **Scalar:** `abs`, `min`, `max`, `clamp`, `min_zero`, `sign_str`,
  `percent_of`, `reverse_digits`, `sum_of_digits`, `has_digit`,
  `saturating_add`, `saturating_sub`, `checked_add`,
  `checked_sub`, `checked_div`, `pow`, `gcd`, `lcm`, `factorial`,
  `next_power_of_2`, `log2_floor`, `sqrt_floor`, `ceil_div`,
  `round_up_to`, `round_down_to`, `divmod`
- **Bit ops:** `count_ones`, `leading_zeros`, `trailing_zeros`,
  `bit`, `set_bit`, `clear_bit`, `toggle_bit`, `byte_swap`,
  `rotate_left`, `rotate_right`
- **String formatting:** `to_string`, `to_string_padded`,
  `to_string_with_sep`, `to_hex`, `to_binary`, `to_oct`,
  `to_rgb_hex`, `digits`, `pluralize`

### `std/i64`

- **Scalar:** `abs`, `min`, `max`, `clamp`, `pow`, `gcd`, `lcm`
- **Parity:** `is_even`, `is_odd`
- **Sign:** `signum` (-1/0/1), `is_positive`, `is_negative`, `is_zero`
- **Overflow-aware:** `saturating_add`/`saturating_sub` (clamp to
  i64::MAX/MIN), `checked_add`/`checked_sub` (`Option[i64]`, `None` on
  overflow)
- **String:** `to_string`

### `std/u32`

- **Scalar:** `min`, `max`, `clamp`, `pow`
- **Predicates:** `is_zero`, `is_even`, `is_odd`
- **Overflow-aware (unsigned):** `saturating_add`/`saturating_sub`
  (clamp to u32::MAX / 0), `checked_add`/`checked_sub` (`Option[u32]`,
  `None` on overflow/underflow)
- **String:** `to_string`

### `std/u64`

- **Scalar:** `min`, `max`, `clamp`, `pow`
- **Predicates:** `is_zero`, `is_even`, `is_odd`
- **Overflow-aware (unsigned):** `saturating_add`/`saturating_sub`
  (clamp to u64::MAX / 0), `checked_add`/`checked_sub` (`Option[u64]`,
  `None` on overflow/underflow)
- **String:** `to_string`

### `std/float`

- **String:** `(n: f32) to_string()`, `(n: f64) to_string()` —
  up to 7 / 15 fractional digits, trailing zeros trimmed, NaN
  / ±Inf handled. `(n) to_string_prec(prec)` — fixed `prec`
  fractional digits (no trimming), rounded half away from zero.
- **Math primitives** (on both f32 and f64; f32 wrappers
  promote to f64, apply, demote): `abs`, `floor`, `ceil`,
  `round`, `trunc`, `sqrt`, `pow(y)`, `log`, `exp`, `sin`,
  `cos`. Routed through the checker-injected
  `__<op>_f64` builtins so every backend can use its
  hardware-precise op.
- **IEEE-754 classification:** `is_nan`, `is_finite`, `is_inf`
- **Combinators:** `min(y)`, `max(y)`, `clamp(lo, hi)` — NaN
  propagates (any NaN input → NaN output), matching Go's
  `math.Min` / `math.Max` semantics

### `std/string`

Receiver methods on strings — the biggest module (~120 helpers).
Includes the byte-level free function `__is_ascii_ws` used by
`trim` / `fields` / `is_blank` and by `std/i32`'s
`is_ascii_white_space`.

Grouped by family:

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
- **Casing / transform:** `capitalize`, `to_lower`, `to_upper`,
  `snake_case`, `kebab_case`, `title_case`, `to_acronym`,
  `word_count`, `eq_ignore_ascii_case`, `slugify` (free-form text →
  URL slug: lowercased, non-`[a-z0-9]` runs collapsed to `-`, ends
  trimmed — distinct from `kebab_case`, which only folds camelCase)
- **Escape / encode:** `escape_html`, `escape_c`, `escape_shell`
- **Strip / trim:** `strip_quotes`, `strip_prefix`,
  `strip_suffix`, `remove_prefix`, `remove_suffix`, `trim`,
  `trim_start`, `trim_end`, `trim_chars`, `trim_start_chars`,
  `trim_end_chars`, `trim_start_matches`, `trim_end_matches`,
  `rstrip_newline`
- **Hashing:** `hash_fnv32`, `hash_djb2`
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
  `split_once`, `pad_start`, `pad_end`, `pad_start_str`,
  `pad_end_str`, `center`, `wrap`, `indent`, `repeat_with_sep`
- **Slice / count / reverse:** `take`, `drop`, `chunks`, `at`,
  `chars`, `to_array`, `reverse_bytes`, `count`, `count_byte`,
  `bytes`, `first`, `last`, `before`, `after`, `between`,
  `truncate`, `ellipsis`, `first_line`
- **Parse:** `parse_bool`, `parse_int`, `parse_hex_int`,
  `parse_bin_int`, `parse_float`
- **Build:** `repeat_char`

### `std/array`

Receiver methods on arrays. Both `i32[]` and `string[]` element
types are covered. `Array.push` stays a built-in IR primitive
(intercepted by codegen) and is registered by the checker — every
other `__method_Array_*` here is auto-discovered from the naming
convention.

- **i32[] reductions:** `sum`, `max`, `min`, `product`, `avg`,
  `range`, `count`, `gcd_all`, `lcm_all`, `abs_each`,
  `first_index_of`, `pairwise_diffs`, `min_max`, `reversed`,
  `every_positive`, `sorted_asc`, `sorted_desc`, `cumsum`,
  `sum_squared`, `median`, `mode`
- **Wider int / float reductions** (free functions —
  array-method dispatch can't yet overload by element type,
  so these aren't receiver methods):
  `sum_i64`, `max_i64`, `min_i64`, `avg_i64`;
  `sum_u32`, `max_u32`, `min_u32`;
  `sum_u64`, `max_u64`, `min_u64`;
  `sum_f64`, `max_f64`, `min_f64`, `avg_f64`
- **string[] core:** `join`, `index_of`, `position`, `contains`,
  `reverse`,
  `filter_non_empty`, `count_non_empty`, `distinct`,
  `distinct_count`, `max_by_len`, `min_by_len`, `sum_lens`,
  `take`, `drop`, `all_non_empty`, `any_contains`,
  `count_str`, `all_starts_with`, `all_ends_with`,
  `sorted_str_asc`, `sorted_str_desc`, `join_with_last`
- **generic `[T]` combinators** (free + method forms): `map`,
  `filter`, `fold`, `reduce`, `any`, `all`, `find`, `position`,
  `take`/`drop`/`take_while`/`drop_while`, `slice`, `chunks`,
  `windows`, `zip`, `enumerate`, `reverse`, `intersperse`,
  `flat_map`, `flatten` (`T[][]` → `T[]`), `partition`
  (→ `(kept, rejected)`), `scan` (running left fold, same length
  as input). Eq/Ord-bounded: `contains`, `index_of`,
  `index_of_last`, `distinct`, `count`, `is_sorted`, `equal`,
  `starts_with`/`ends_with`.

### `std/unicode`

Unicode-aware **simple (1:1) case mapping** — the complement to
`std/string`'s byte-wise, ASCII-only `to_upper`/`to_lower`. Decodes
UTF-8, maps each code point (Latin, Greek, Cyrillic, Armenian,
fullwidth, …) via a table generated from the Go stdlib's `unicode`
package, and re-encodes.

- `to_upper(s)` / `to_lower(s)` — whole-string mapping
- `to_upper_char(cp)` / `to_lower_char(cp)` — single code point
- `eq_ignore_case(a, b)` — case-insensitive equality (simple fold)
- **character classes** (over a code point, via range binary search):
  `is_letter`, `is_digit` (Nd), `is_alnum`, `is_whitespace`,
  `is_upper`, `is_lower`

Caveats: SIMPLE mappings only (multi-code-point expansions like
`ß`→`SS` are left unchanged, matching Go); not locale-aware. The
tables are regenerated by `cmd/unicodegen`.

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

Free helpers — random, ranges, numeric constants, RGB packing.

- `random_int(lo, hi)`
- `range(start, end)`, `range_step(start, end, step)`
- `i32_max()`, `i32_min()`, `i64_max()`, `i64_min()`
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
- `sort_strings_asc(arr)`, `sort_strings_desc(arr)`,
  `sort_strings_asc_ci(arr)`
- `string_cmp(a, b)`, `string_cmp_ci(a, b)`

### `std/set`

A generic, value-semantic set of distinct elements,
`Set[T: cmp.Eq]`. Every operation returns a NEW set and leaves
its receiver untouched. Element type only needs `cmp.Eq`
(membership is decided by `==`); iteration / `to_array()` is in
first-inserted order.

- `set_new()`, `set_of(xs)` — empty set / dedup an array
- `(s).add(x)`, `(s).remove(x)` — insert / delete, returning a
  new set (a no-op returns the receiver)
- `(s).contains(x)`, `(s).len()`, `(s).is_empty()`,
  `(s).to_array()`
- `(s).union(o)`, `(s).intersect(o)`, `(s).difference(o)`
- `(s).is_subset(o)`, `(s).equals(o)` (order-insensitive)

Backed by a linear-scan array, so `contains` / `add` are O(n)
(an n-element build is O(n²)) — right-sized for CLI-scale working
sets, not for large collections.

### `std/format`

- `format(fmt, args)` — template substitution with `{}`
  placeholders and Rust-style `{:[[fill]align][width][.precision]}`
  specs (`{:>8}`, `{:*^10}`, `{:.3}`, `{:>8.2}`).
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

- `path_join(parts)`, `path_parent(p)`, `path_file_name(p)`,
  `path_extension(p)`, `path_clean(p)`.
- `path_is_absolute(p)` — true iff `p` begins at the root (`/`).
- `path_stem(p)` — last component minus its final extension
  (`"archive.tar.gz"` → `"archive.tar"`, `".bashrc"` → `".bashrc"`).
- `path_with_extension(p, ext)` — replace/append the final extension
  (`ext` without a leading dot; empty `ext` drops it), preserving the
  directory (`"a/b/foo.txt"`, `"md"` → `"a/b/foo.md"`).

### `std/base64`

- `base64_encode(s)` / `base64_decode(s)` / `base64_decode_strict(s)` — standard RFC 4648 alphabet, `=` padding.
- `base64url_encode(s)` / `base64url_decode(s)` — URL-safe variant (`-`/`_` alphabet, no padding; decode tolerates padded input). The JWT / URL-token encoding.

### `std/base32`

RFC 4648 base32 (standard `A–Z 2–7` alphabet, `=` padding).

- `base32_encode(s)` / `base32_decode(s)` — decode is lenient (stops at
  the first non-base32 / non-`=` byte). Round-trips any content; the
  case-insensitive, digit-safe alphabet suits TOTP secrets, filenames,
  and DNS labels.

### `std/hex`

Lowercase hex round-trip.

- `hex_encode(s)`, `hex_decode(s)`.

### `std/crypto`

From-scratch SHA-256 (FIPS 180-4) and HMAC-SHA256 (RFC 2104), verified
against NIST / RFC 4231 known-answer vectors.

- `sha256_bytes(s)` / `sha256_hex(s)`.
- `hmac_sha256_bytes(key, msg)` / `hmac_sha256_hex(key, msg)`.
- `consteq(a, b)` — constant-time byte-string compare; `hmac_verify` /
  `hmac_verify_hex` — the timing-safe way to check a MAC.
- `pbkdf2_sha256(password, salt, iterations, dk_len)` /
  `pbkdf2_sha256_hex(...)` — PBKDF2-HMAC-SHA256 (RFC 8018) password-based
  key derivation. Use a random per-password salt and a high iteration
  count for password storage.
- `pbkdf2_verify(password, salt, iterations, expected)` /
  `pbkdf2_verify_hex(...)` — re-derive and compare against a stored key
  in constant time (`consteq`). Use these to verify a password, never a
  plain `pbkdf2_sha256(...) == stored` (a timing oracle).
- `hkdf_extract(salt, ikm)` / `hkdf_expand(prk, info, length)` /
  `hkdf_sha256(salt, ikm, info, length)` / `hkdf_sha256_hex(...)` —
  HKDF-SHA256 (RFC 5869) key derivation for high-entropy input keying
  material (a shared secret / random key), for key separation and
  subkey derivation. Distinct from PBKDF2, which stretches a low-entropy
  password.
- `hotp_sha256(key, counter, digits)` /
  `totp_sha256(key, unix_time, period, digits)` — one-time passwords for
  2FA (RFC 4226 / RFC 6238, SHA-256 mode). `key` is the raw secret bytes
  (base32-decode a base32 secret via `std/base32` first); returns the
  code as an integer to zero-pad to `digits`.

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

### `std/http`

HTTP/1.1 request parsing, response builders, wire-format
serializer.

- **Response builders:** `http_response_ok`,
  `http_response_text`, `http_response_not_found`,
  `http_response_bad_request`, `http_response_internal_error`,
  `http_response_redirect`, `http_response_no_content`
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
  connection.
- `__port_from_env(name, fallback)` — env-var port lookup used
  by the auto-`main`-from-`handle()` synthesis so handler-shaped
  programs can be tuned via `PORT=N ./bin`.

The raw socket primitives `tcp_listen` / `tcp_accept` / `tcp_recv`
/ `tcp_send` / `tcp_close` are runtime-provided, emitted by
codegen from extern stubs at module boundary — not declared in
this module.

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
  `duration_millis(ms)`.
- **Humanised relative time:** `(i: Instant).relative_to(now)` — the
  `fromNow` shape, e.g. `"5 minutes ago"`, `"in 2 days"`, `"just now"`
  (coarse units: month ≈ 30 days, year = 365 days).
- Named constants: `NANOS_PER_SECOND`, `SECONDS_PER_DAY`,
  `DAYS_PER_WEEK`, etc.

### `std/task`

Cooperative single-threaded task runtime — the backend-independent
core of Fern's colorless structured-concurrency model (see
`docs/ASYNC-IMPLEMENTATION-PLAN.md`).

- `reactor_new()`; `(rx).register(...)`, `(rx).poll(...)`,
  `(rx).pending()`.
- `run(states, reactor)` drives every task to completion;
  `select(states, reactor)` returns on the first to finish.
- `Step` (enum) is the per-task state; `Reactor` owns the wait set.

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
