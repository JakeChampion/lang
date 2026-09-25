# Every builtin the self-host checker types has a contract

Running `internal/e2eselfhost` under `FERN_SEM_IR_STRICT=1` found programs
calling `udp_send` refused ("call target has no semantic contract"), and the
same held for `wasm_block` and `wasm_poll`. The self-host checker typed all
three and irlower lowered them, but semsource had no contract rows and ssarc
no op for them. So every function calling one, and the module around it, took
the AST lowering.

- semsource gains the three contracts: `udp_send(string, i32, string)`
  borrowing both strings, `wasm_block(i32)`, and `wasm_poll(i32[])` borrowing
  the array, as `poll` does.
- ssarc emits `op_udp_send`, `op_wasm_block` and `op_wasm_poll` for them. A
  contract alone lowers the call as a direct call to a symbol nothing
  defines, which the prune refuses ("calls udp_send, which the AST lowering
  defines").

`TestSelfHostContractsEveryBuiltin` (internal/checker) makes the table whole.
Every name in checker.fern's `builtin_sigs()` outside the `__` intrinsics has
to be named by semsource.fern, as a contract or an arm of its own, or folded
by constfold.fern first. Every contracted name has to have an op in
ssarc.fern. With the rows removed it names all three builtins.

The strict probe also showed native cannot build `udp_send` on x86-64 or arm64
at all: the call reaches the assembler as an undefined label (#10278).
