# 2026-11-05 — a map column retains its boxes

The send helper for a map, PR #14209, under SEC-009.

## The shape

The helper reads the key and value columns of the source map with
`__map_string_column` and `__map_box_column`. It inserts a copy of each
pair into a fresh map. The first version released each column with
`release`, which names `__fern_rc_dec` for an array of records. The
sanitizer then reported one live box per heap value of the source map.
Every test shape with record values passed, because a record literal of
constants is static and has no count. The leak showed only with a value
built by a spread of a shared record, which is a heap box.

## The rule

A column read retains each box it lists (`retain` is true for a boxed
value column), and `__fern_rc_dec` of a `usize[]` column frees the array
without a release of its elements. The planner drops such an array with
`drop_value_inline`, which releases each element and then the array. A
hand-written IR helper that reads a column must do the same.

## Tests

`conformance/cases/send_copy_deep` copies a map of records whose boxes
share a spread source, and `TestConformanceLeakCensusX86_64` pins it at
zero unpaired allocations. The sanitizer balances on x86-64 and arm64 for
the case and for `tests/stdlib/worker_test.fern`.
