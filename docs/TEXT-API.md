# Text API ownership

Status: [reference] Module ownership and current text operations.

Import the module that owns the operation.
Methods on strings retain their receiver syntax when the module changes.

| Module | Operations |
|---|---|
| `std/string` | Byte search, replacement, and explicit ASCII transforms |
| `std/utf8` | UTF-8 validation, scalar conversion, and character iteration |
| `std/unicode` | Unicode case transforms, classifiers, normalization, and segmentation |

`s.len()` counts bytes.
With `std/utf8`, `s.codepoint_count()` counts Unicode scalar values and `s.chars()` decodes them.
These two methods replace each malformed maximal subpart with one U+FFFD.
The strict `utf8.decode_at` function returns `None` for malformed input.
Unicode case transforms retain their separate rule of one U+FFFD per invalid byte.

`s.whitespace_word_count()` counts fields with ASCII whitespace separators.
`unicode.word_count(s)` counts words under UAX #29.
For `"hello,world foo"`, the results are 2 and 3.

| Previous API | Current API |
|---|---|
| `s.count_matches(sub)` | `s.count(sub)` |
| `s.replacen(old, new, n)` | `s.replace_n(old, new, limit)` |
| `string.string_from_codepoint(c)` | `utf8.encode(c)` |
| `string.string_from_codepoints(cs)` | `utf8.encode_all(cs)` |
| `utf8.utf8_encode(c)` | `utf8.encode(c)` |
| `utf8.utf8_decode_at(s, i)` | `utf8.decode_at(s, i)` |
| `utf8.is_valid_utf8(s)` | `utf8.is_valid(s)` |
| `utf8.codepoints(s)` | `s.chars()` with `std/utf8` |
| `utf8.codepoint_count(s)` | `s.codepoint_count()` with `std/utf8` |
| `unicode.to_upper(s)` | `s.to_upper()` with `std/unicode` |
| `unicode.to_lower(s)` | `s.to_lower()` with `std/unicode` |
| `unicode.case_fold(s)` | `s.case_fold()` with `std/unicode` |
| `unicode.swap_case(s)` | `s.swap_case()` with `std/unicode` |
| `unicode.capitalize(s)` | `s.capitalize()` with `std/unicode` |
| `unicode.title_case(s)` | `s.title_case()` with `std/unicode` |
| `unicode.eq_ignore_case(a, b)` | `a.eq_ignore_case(b)` with `std/unicode` |
| `unicode.is_alpha_only(s)` | `s.is_letters()` with `std/unicode` |
| `unicode.is_numeric(s)` | `s.is_digits()` with `std/unicode` |
| `unicode.is_alnum_only(s)` | `s.is_alphanumeric()` with `std/unicode` |
| `s.is_alpha_only()` | `s.is_letters()` with `std/unicode` |
| `s.is_numeric()` | `s.is_digits()` with `std/unicode` |
| `s.is_alnum_only()` | `s.is_alphanumeric()` with `std/unicode` |
| `c.is_alnum()` | `c.is_alphanumeric()` with `std/unicode` |
| `b.is_ascii_alpha()` | `b.is_ascii_letter()` with `std/i32` |
| `b.is_ascii_alnum()` | `b.is_ascii_alphanumeric()` with `std/i32` |
| `b.is_ascii_white_space()` | `b.is_ascii_whitespace()` with `std/i32` |
| `s.is_ascii_alpha_only()` | `s.is_ascii_letters()` |
| `s.is_ascii_numeric()` | `s.is_ascii_digits()` |
| `s.is_ascii_alnum_only()` | `s.is_ascii_alphanumeric()` |
| `s.word_count()` | `s.whitespace_word_count()` |
| `s.word_count_min(n)` | `s.whitespace_word_count_min(n)` |

The previous APIs have no compatibility aliases.
The ASCII string classifiers stay in `std/string`.
Unicode case methods on strings also require `std/unicode` when callers previously imported only `std/string`.

`replace_n` replaces at most `limit` non-overlapping matches.
An empty search string or a nonpositive limit returns the input.
`replace` replaces all matches, and `replace_first` replaces at most one match.
All three functions share one buffer algorithm.

`utf8.encode_all` fills one exact-size buffer.
Unicode transforms collect scalar values and use this encoder for their result.
Grapheme reversal fills one exact-size byte buffer after it finds the cluster boundaries.
