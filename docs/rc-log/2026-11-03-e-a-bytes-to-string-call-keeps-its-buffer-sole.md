# 2026-11-03 — a bytes-to-string call keeps its buffer sole

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

`ls` writes each name through `newline_qmark`, which appends the bytes of
the name to `bs` in a loop and returns `string_from_bytes_unchecked(bs)`.
`quoted_name` took 1,761,689 instructions, about 450 for each name of about
12 bytes.

Each append tested the reference count of `bs` and then pushed. The plan
proved no sole box for the loop's phi. `sole_boxes` counts a call that
borrows a value and does not move it as a second holder. The final
`string_from_bytes_unchecked(bs)` is such a call. The flag is set for each
value, so that one borrow after the loop cleared it for every append inside
the loop.

`string_from_bytes_unchecked` and `string_from_bytes_range_unchecked` copy
the bytes into a new string on every target. The string never holds the
array, so the call adds no holder.

## The rule

- `ssasem.holds_no_argument` names the two builtins. `sole_boxes` reads a
  call to a builtin on that list as a read, so its argument stays sole.

| x86-64, `ls` of 4000 names | before | after | GNU 9.4 |
|---|--:|--:|--:|
| `quoted_name` | 1,761,689 | 1,309,751 | |
| whole run | 13,613,159 | 13,227,617 | 11,298,193 |

## What is still behind in `newline_qmark`

The loop now costs about 19 instructions for each byte. The first append
goes to `__fern_arr_push_owned_u8`, because the empty literal has no
capacity. `string_from_bytes_unchecked` then copies the bytes into a second
allocation and releases the array.

## Tests

`TestSelfHostSoleLoopWithX86_64` has two new cases, `bytes-to-string` and
`bytes-to-string-range`. Each builds a byte array in a loop and returns it
through one of the two builtins, and each must carry no uniqueness test.
Both cases fail without the change. `TestSelfHostGrowSoleOccurrenceX86_64`,
`TestConformanceLeakCensusX86_64`, `TestSelfHostRcCertifyCases` and
`TestSelfHostSSAPhysicalRC*` ran green.
