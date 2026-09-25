# print_int, eprint_int and read_int are gone (#10244, part 2)

`print_int(n)`, `eprint_int(n)` and `read_int()` were builtins of the self-host
alone. The native checker and the self-host checker both answer E001 for all
three, so only test drivers that skip the checker ever compiled them. A
program that defines a function with one of these names already got its own
function.

Removed:
- the irlower arms, including the i64 dispatch to `op_print_i64`;
- IR ops `print_int`, `print_i64` and `read_int`. Their ids went to the last
  three dense ids, so the space stays dense: `wasm_pollable_drop` 190 → 161,
  `wasm_block` 191 → 162, `wasm_poll` 192 → 167. `kind_count` is 189;
- `rt_src_print_int`, `rt_src_read_int`, `mark_print_int`, `mark_read_int`,
  and the need gates and emission blocks on x86-64 and arm64;
- on wasm, `$__fern_print_int`, `$__fern_print_int64` and `$__fern_read_int`
  with their need gates. `print_scratch_bytes`, the 40 bytes above the string
  literals that only the two print buffers used, went with them, so the
  constant region now starts right after the literals;
- the names from `parser.builtin_function_names`, `caps.frontend_ungated`,
  `ircore`'s runtime-need roots and `asmcore`'s builtin result table.

## Tests

The test programs used `print_int` as their number printer. They now call a
Fern definition instead: `e2eharness.WithPrintInt(src)` appends `print_int`,
`print_i64` or `read_int` for each one a program calls without defining. The
printers use recursion and `putchar`, so they allocate nothing and handle
i32 and i64 minimum. `read_int` parses one `read_line()`. There is no implicit
i32 → i64 widening, so the calls that printed an i64 now call `print_i64`. An
instrumented copy of the old irlower found them: 17 rows in
`TestSelfHostWasmRun`, one in `TestSelfHostWasmIRWasiHelpers`, and none in the
semsource RC program, which defines `print_int` in its own text.

`const-i64` in `TestSelfHostWasmIRWasiHelpers` is oracle-checked now, since
`print_i64` is ordinary Fern. The i32-result `const-i64-oracled` row it used to
need is deleted.

Deleted, because they tested the builtins themselves:
`self_host_print_int_ir_test.go`, `self_host_read_int_ir_test.go`, the
`eprint_int` rows, the `read_int_is_zero` wasm row, the `print_int` component
rows, and the `print_int` / `read_int` rows of the runtime-helper tests.
`TestWasmSelfHostI64Print` keeps its i64 arithmetic cases through `print_i64`.
Its print-then-allocate case guarded the print scratch overlap and went with
the scratch.
