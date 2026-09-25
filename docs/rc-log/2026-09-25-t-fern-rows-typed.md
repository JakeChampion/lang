# The checker types the runtime helpers the raw floor names

`checker.raw_floor_sigs` holds six `__fern_*` rows beside the `__raw_*` and
`__syscall*` ones: `str_eq`, `str_free`, `str_arr_free`, `rc_dec`, `map_find`
and `map_delete`. `semsource.raw_floor_contracts` turns every row into a
contract. The checker's lookup, `raw_floor_find`, only matched names starting
with `__raw_` or `__syscall`, though, so the checker typed none of the six and
accepted any argument to them. The typed path then refused a call that the
checker had accepted.

`TestSelfHostStrEqSymbolTypeChecks` showed it under `FERN_SEM_IR_STRICT=1`: its
program passed two `string`s to `__fern_str_eq`, which takes two usize box
addresses. The checker accepted that, and strict refused it with "call argument
type".

`raw_floor_find` now matches `__fern_` too, so the checker types all six at the
table's signatures. A `string` passed to `__fern_str_eq` is now E038, as it is
to any other `usize` parameter.

The two tests written in the old spelling now use the right types:

- `strEqSymbolSrc` builds its `{data, len}` boxes on the raw floor and compares
  their addresses, the way `__fern_map_find` compares keys.
- The length sweep in `self_host_str_eq_lengths_test.go` compares its strings
  with `==`. That lowers to the same `op_str_eq`.

## Tests

- `TestSelfHostStrEqSymbolTypeChecks` runs with the typed path pinned on and
  strict. It also asserts that string operands to `__fern_str_eq` are an E038.
- `TestSelfHostStrEqSymbolIRX86_64` / `…Arm64` run the box-building program and
  expect exit 42.
