# The string helpers take the typed path

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering
(`SELFHOST-SEMANTIC-SOURCE.md`, "Retiring the AST lowering"), continued from
`2026-09-25-a-a-runtime-helper-takes-the-typed-path.md`.

## What changed

Ten more runtime helpers type-check, so `semlower.runtime_bodies` produces
them and the AST lowering no longer does: `str_cmp`, `str_to_upper`,
`str_to_lower`, `str_repeat`, `str_trim`, `str_replace` (with its bundled
`str_dup`), `str_split` (with its bundled `utf8_step`), `str_bytes`,
`string_from_bytes`, `arr_str_join`. `str_lines` already checked and now has a
test that says so.

The changes are the two the checker asked for and nothing else:

- a block from `__raw_alloc` / `__raw_arr_box` is held as `usize`, not `i32`;
- a string byte `s[i]` is a `u8`, so it is widened with `as i32` where an
  `i32` local or `__raw_store8` operand takes it, and with `as usize` where
  `__raw_store_ptr` stores it.

## How the rest stand

Checking each helper's source alone with the self-host checker:
29 of 128 pass. Of the rest, nearly all fail on one of

- an address held as `i32` where the raw floor takes `usize`;
- an `i32` word where a syscall takes `i64`, or a syscall's `i64` result
  returned or stored as `i32`.

Two do not fit that pattern:

- `__fern_arr_slice` takes the address of an array's box
  (`__raw_addr(a, 8)` on an `i32[]`). No raw-floor intrinsic gives an array's
  address, so it needs one before it can be retyped.
- A helper that calls another helper is checked alone, so the callee is
  undefined: `open_with` calls `__fern_open_res`, and `i32_lcm` spells its call
  to `gcd` as the `n.gcd(other)` method, which only `std/i32` defines.

## Measured

- `TestSelfHostSemanticProduction`'s new `string-helpers-take-the-typed-path`
  row: the report carries `produced` for those ten and `str_lines` on x86-64
  and arm64, the output matches the AST lowering's, and the sanitize leg
  reclaims everything.
  Adding a report the compiler does not print fails the row, so the list is
  asserted.
- A program using every one of them prints the same bytes when built by native
  and by the self-host compiler with `FERN_SEM_IR=1`.
