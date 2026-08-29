# The rc corpus leaks differently on arm64 than on x86-64

First whole-corpus leak measurement (#7790). `rcCorpus`'s 216 cases, each built
with `ast.LeakCheckEnabled` and run, reading `live_bytes` off the
`leakcheck: allocs=N frees=M live_bytes=K` line at the exit seam.

| | clean | leaking |
|---|---|---|
| x86-64 | 176 | 40 |
| arm64 | 169 | 47 |

Byte counts are **identical across repeat runs** on both backends, which is what
makes pinning them per case viable rather than a flapping gate. Whole census:
13.5 s on x86-64, ~16 s on arm64 under qemu.

## The divergence

Eight cases leak on arm64 and not on x86-64:

```
accumulator_seeded_from_array_element
map_inline_string_kv_retain_no_crash
map_string_keys_churn_free
map_string_value_overwrite_pre_drop_churn
map_string_values_churn_free
stdlib_query_parse_roundtrip
struct_map_field_churn_free
struct_map_field_escapes
```

One leaks on x86-64 and not arm64: `cell_string_read_aliased` (32 bytes).

Eight more leak on both, by different amounts:

| case | x86-64 | arm64 |
|---|---|---|
| `with_reassign_local_alias_threaded` | 27888 | 55152 |
| `string_closure_capture_churn_free` | 3200 | 6400 |
| `map_delete_tuple_churn_free` | 288000 | 312000 |
| `stdlib_json_cursor_idiom` | 1440 | 1744 |
| `stdlib_json_roundtrip` | 624 | 752 |
| `string_closure_capture_aliased` | 16 | 48 |
| `forin_elem_escape_return_keeps_retain` | 96 | 112 |
| `string_array_append_grow_struct_field` | 2800 | 2784 |

## What the numbers say

Six of the eight arm64-only cases are strings inside maps, and
`string_closure_capture_churn_free` and `with_reassign_local_alias_threaded` are
both **exactly 2×** on arm64. A clean doubling on a string-carrying shape points
at the string representation rather than at the map or closure drop path — the
two-word string ABI (`ast.UseTwoWordStrings`, `TypeIsTwoWord`) is the obvious
suspect, and a two-word string whose second word is released on one backend and
not the other would produce exactly this signature.

That is a *lead, not a finding*. It has not been confirmed: nothing here
attributes a single leaked block to an alloc site. `FERN_RC_TRACE=1` on
`string_closure_capture_churn_free` is the next step, since it names the alloc
site a leak came from and the 2× makes the pairing easy to read.

`string_array_append_grow_struct_field` is the counter-example that stops the
story being tidy: arm64 leaks *less* there (2784 vs 2800), so whatever differs is
not uniformly "arm64 releases less".

## The trap

Nothing had looked at this before, and the reason is worth recording.
`docs/TEST-GATES.md` describes `TestSelfHostLeakMatrixX86_64` and its two sibling
grids, all three x86-64 only. The whole leak-side apparatus was single-backend,
so a divergence of this size was not hidden by a passing gate — there was no gate
to pass. Reading the leak matrix as "the leak-side coverage" and concluding
arm64 was covered too is the misreading available here.

Both backends are now pinned separately in
`internal/e2e/rc_leak_gate_test.go`; the tables are the live gap list.
