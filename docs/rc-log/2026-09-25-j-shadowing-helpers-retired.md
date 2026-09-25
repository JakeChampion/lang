# The helpers that shadowed stdlib methods are gone (#10244, part 1)

These runtime helpers were lowered only by `irlower`'s builtin-method arms,
and the typed path could never produce them:

- `__fern_i32_pow`, `_gcd`, `_lcm`, behind `n.pow(e)`, `n.gcd(m)`, `n.lcm(m)`;
- the `__fern_arr_i32_*` reducers, behind `xs.sum()`, `.product()`,
  `.index_of(x)`, `.contains(x)`, `.min()`, `.max()` on an `i32[]`;
- `__fern_str_to_i32`, behind a bare `str_to_i32(s)`;
- `__fern_str_reverse`, behind `s.reverse()` on a string.

Without an import, the native checker and the self-host checker both reject
every one of these spellings. With `import "std/i32"` or `import "std/array"`,
native accepts them and calls the stdlib's own method. So the self-host
reached a helper only through test drivers that skip the checker, and there it
**shadowed** the method the program had imported. The shadow was not
equivalent: `std/i32`'s `pow` answers 0 for a negative exponent, and
`__fern_i32_pow` answered 1.

Removed:
- the irlower arms, `builtin_arr_opt_ret_type`, and `arr_i32_recv`;
- the `rt_src_*` generators and their `mark_*` needs;
- the need gates and emission blocks on x86-64 and arm64;
- the WAT bodies and their `@uses_*` gates on wasm;
- IR op `str_reverse`. Kind 110 now holds `tcp_connect`, which moved down from
  193 to keep the id space dense (`kind_count` is 192);
- the typed path's `arr_sum` / `arr_product` kinds, with the `string.reverse`
  contract. The semantic ids after them shift down by two.

`semantic_kind_name` had no entry for `dyn_box` (-45 now, -47 before), so the
printer named it `invalid`. It is named now.

## Tests

- `TestSelfHostStdlibModulesIR/i32-methods` runs `import "std/i32"` with
  `n.pow(0 - 1)`, `gcd` and `lcm` through the unchecked loader, against the
  native interpreter. It fails on the old helpers: pow answers 1 there.
- `array-index-of` in the same test now runs `std/array`'s generic on the
  local and field receivers that the intercept used to take.
- Deleted: `self_host_str_to_i32_ir_test.go`,
  `self_host_arr_field_builtin_recv_ir_test.go`, and the helper rows in the
  AST/IR path, predicate, wasm and runtime-helper tests. Each ran a spelling
  the checkers reject.

## Found on the way

With the intercepts gone, two stdlib method calls stopped lowering, and in
both the monomorphiser could not type the receiver, so the `__arrm_` fold
never ran. A `string[]` receiver had always failed the same way; the i32[]
intercepts hid it. The two commits before this one fix them:

- `array-index-of`'s field receiver `h.xs`: the struct name `H` was read as a
  type variable (#10256);
- the `map_keys` conformance case, `m.keys().sum()` under `import
  "std/array"`: a map's `keys()` / `values()` had no inferred type. This one
  is checked code, so "no checked compile reaches these helpers" was true of
  native only; the self-host's lowering reached them.

Part 2 of #10244 is `print_int`, `eprint_int` and `read_int`, which the test
programs use as their number printer.
