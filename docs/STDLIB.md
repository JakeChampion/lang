# Standard library

The lang stdlib lives in two namespaces:

- **`std/…`** — high-level helpers user code reaches for directly.
  Receiver methods (`(5).abs()`, `"hello".split(",")`, `arr.sum()`)
  resolve here.
- **`core/…`** — low-level primitives the `std/…` modules build on
  top of. Raw-memory routines (allocator probes, scratch buffers
  written backwards, `__memcpy` plumbing) live here. User code
  normally shouldn't reach for these.

During the prelude-to-modules migration the magic
`internal/prelude/prelude.lang` `import`s every module below
automatically, so existing programs that call helpers by bare
name keep working. Phase 5 of the migration (see
[`docs/PRELUDE-TO-MODULES.md`](./PRELUDE-TO-MODULES.md)) drops
the auto-import — user programs declare what they need via
`import "std/…";` lines.

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
- **String:** `to_string`

### `std/u32`

`min`, `max`, `clamp`, `to_string`.

### `std/u64`

`min`, `max`, `clamp`, `to_string`.

### `std/float`

`(n: f32) to_string()`, `(n: f64) to_string()` (up to 7 / 15
fractional digits, trailing zeros trimmed, NaN / ±Inf handled).

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
  `starts_with_any`, `ends_with_any`
- **Casing / transform:** `capitalize`, `to_lower`, `to_upper`,
  `snake_case`, `kebab_case`, `title_case`, `to_acronym`,
  `word_count`, `eq_ignore_ascii_case`
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
- **string[] core:** `join`, `index_of`, `contains`, `reverse`,
  `filter_non_empty`, `count_non_empty`, `distinct`,
  `distinct_count`, `max_by_len`, `min_by_len`, `sum_lens`,
  `take`, `drop`, `all_non_empty`, `any_contains`,
  `count_str`, `all_starts_with`, `all_ends_with`,
  `sorted_str_asc`, `sorted_str_desc`, `join_with_last`

### `std/math`

Free helpers — random, ranges, numeric constants, RGB packing.

- `random_int(lo, hi)`
- `range(start, end)`, `range_step(start, end, step)`
- `i32_max()`, `i32_min()`, `i64_max()`, `i64_min()`
- `pack_rgb(r, g, b)`

### `std/sort`

Free sort / compare helpers (insertion-sort).

- `sort_i32_asc(arr)`, `sort_i32_desc(arr)`
- `sort_strings_asc(arr)`, `sort_strings_desc(arr)`,
  `sort_strings_asc_ci(arr)`
- `string_cmp(a, b)`, `string_cmp_ci(a, b)`

### `std/format`

- `format(fmt, args)` — template substitution with `{}`
  placeholders.
- `format_bytes(n)` — `"1024 → 1 KiB"` shape (binary prefixes).
- `format_duration_ms(ms)` — `"1h 23m 45s"` shape.

### `std/csv`

RFC 4180 escape / join / single-line parse.

- `csv_escape(s)`, `csv_join(arr)`, `csv_parse_line(s)`.

### `std/log`

Three thin stderr wrappers with level prefix.

- `log_info(msg)`, `log_warn(msg)`, `log_error(msg)`.

### `std/io`

- `read_all_stdin()` — read until EOF into a single string.

### `std/path`

POSIX path manipulation (string-level only).

- `path_join(parts)`, `path_parent(p)`, `path_file_name(p)`,
  `path_extension(p)`.

### `std/base64`

RFC 4648 (standard alphabet) round-trip.

- `base64_encode(s)`, `base64_decode(s)`.

### `std/hex`

Lowercase hex round-trip.

- `hex_encode(s)`, `hex_decode(s)`.

### `std/url`

Percent-encoding, URL parsing, query parsing.

- `url_encode(s)`, `url_decode(s)`
- `url_parse(s) Option[Url]`
- `query_parse(s) Map[string, string[]]`

### `std/json`

- `json_encode(v: JsonValue): string`
- `json_parse(s: string): Option[JsonValue]`

### `std/http`

HTTP/1.1 request parsing, response builders, wire-format
serializer.

- **Response builders:** `http_response_ok`,
  `http_response_text`, `http_response_not_found`,
  `http_response_bad_request`, `http_response_internal_error`,
  `http_response_redirect`, `http_response_no_content`
- **Status / classifiers:** `http_status_text`,
  `is_valid_http_status`
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

### `std/test`

Pure-Lang unit-test runner. Tests are functions returning
`Option[string]` (None = pass, Some(msg) = fail). The shape
the project plans to migrate to once the compiler is self-
hosted and the Go-side `*_test.go` harness retires; see
`docs/ROADMAP-AND-SELF-HOSTING.md`. Output is TAP-13 so
existing test runners (`prove`, `tape`, jUnit converters)
can consume it directly.

```
function test_addition(): Option[string] {
    return assert_eq_i32(2 + 2, 4);
}

function main(): i32 {
    var r: TestRunner = test_new("arithmetic");
    r = r.it("addition", test_addition());
    return r.finish();
}
```

- **Runner:** `TestRunner` (struct), `test_new(suite)`,
  `test_new_verbose(suite)`, `(r).it(name, result)`,
  `(r).finish() -> i32`
- **Outcome constructors:** `pass()`, `fail(msg)`
- **Boolean assertions:** `assert_true(cond)`, `assert_false(cond)`
- **i32 assertions:** `assert_eq_i32`, `assert_neq_i32`,
  `assert_lt_i32`, `assert_le_i32`, `assert_gt_i32`,
  `assert_ge_i32`
- **bool / string assertions:** `assert_eq_bool`,
  `assert_eq_string`, `assert_neq_string`, `assert_empty_string`,
  `assert_non_empty_string`
- **Substring:** `assert_contains`, `assert_not_contains`,
  `assert_starts_with`, `assert_ends_with`
- **Array assertions:** `assert_len_i32`, `assert_len_string`,
  `assert_eq_i32_array`, `assert_eq_string_array`

Examples live under `examples/tests/`; the runner's own
meta-test (`runner_self_test.lang`) walks every assertion
helper on both pass and fail paths.

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

### `core/no_prelude`

Sentinel import — empty module. `import "core/no_prelude";`
in a user program signals the checker to skip the auto-
injected magic prelude, so the program needs explicit
`import "std/…";` lines for every helper it uses. Phase 5
of the migration (see `docs/PRELUDE-TO-MODULES.md`) makes
the no-prelude path the default; until then this is the
opt-out for programs that want to verify their imports are
complete.

Free-function calls into stdlib become qualified under
no-prelude — `int.int_to_string_radix(s, 16)` rather than
the bare `int_to_string_radix(s, 16)` the auto-prelude
flattens. Bare receiver-method calls (`.abs()`,
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
