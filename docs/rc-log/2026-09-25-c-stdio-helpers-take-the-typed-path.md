# The stdio writers take the typed path

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering
(`SELFHOST-SEMANTIC-SOURCE.md`, "Retiring the AST lowering"), continued from
`2026-09-25-b-string-helpers-take-the-typed-path.md`.

## What changed

`__fern_print_str`, `__fern_eprint_str` and `__fern_putchar` (behind `write`,
`eprint` and `putchar`) type-check against the raw floor. They are the first
retyped helpers that make a syscall. Their scratch pointer is a `usize`,
each syscall word is an `i64` (`__raw_data(s) as i64`, `s.len() as i64`), and
the `i64` result is narrowed with `as i32`.

The AST lowering had to learn both spellings, since it still lowers the same
sources when the typed path is off:

- A syscall operand whose width is 64 lowers through `lower_i64`. Before
  this, every operand went through the 32-bit `lower_expr`, and `p as i64`
  bailed there (`unary as_i64`). The four `__syscallN` arms, which were
  copies of one another, are one arm now.
- A local declared `usize` carries `is_usize` on its slot. `expr_is_usize`
  also knows the address-returning raw-floor intrinsics and `as usize`.
  `p as i64` on one widens with `op_int_extend_ptr`, which moves nothing on
  the register backends. The old path sign-extended the low 32 bits, which
  truncates an address above 4 GiB (arm64-darwin puts the image there).

## Left out on purpose

`__fern_print_int` and `__fern_read_int` retype the same way, and both
lowerings agree on them. The builtins that reach them, `print_int` and
`read_int`, exist only in the self-host front end: native answers E001, and
nothing in the repository calls them. The typed path has no contract for
them, so a module calling one refuses. #10244 retires the names and the two
helpers instead of giving them contracts.

## Measured

- `TestSelfHostSemanticProduction` row `stdio-helpers-take-the-typed-path`:
  `produced` for all three on x86-64 and arm64, with the same output as the
  AST leg and a leak-free sanitize leg. Before the `irlower` change, the AST
  leg failed to compile the retyped sources.
- Row `usize-widens-to-i64-whole`: an address widened to `i64` round-trips
  through `__store_i64` / `__raw_load_ptr`. On Linux the address is below
  4 GiB, so this pins that both lowerings accept the cast; truncating it
  would show only on arm64-darwin.
