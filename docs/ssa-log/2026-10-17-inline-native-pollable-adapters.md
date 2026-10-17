# Select native pollable adapters without runtime helpers

Native socket readiness tokens are descriptors. Native deadlines use the
ordinary poll timeout, and pollable resources have no separate lifetime.
`tcp_pollable` therefore preserves its operand; `wasm_timer_pollable` and
`wasm_poll` return -1; `wasm_pollable_drop` and `wasm_block` return zero.

Both native instruction selectors produce these values directly. Their stack
paths replace the argument slot or preserve it; their SSA paths select the
result without marshalling an argument or calling a helper. Argument evaluation
and array lifetime remain in the typed lowering. The five runtime definitions,
need gates and exported symbols are removed. The public IR operations remain
for WASI's real host-resource operations.

Preserving the descriptor also fixes x86-64's zero extension of negative i32
values through the old register-ABI wrapper. A baseline probe with -1 fails;
the direct identity preserves -1 and INT32_MIN.

The new probe checks all three native emissions and executes normal, debug
and sanitized builds on the host. It observes every used and discarded call's
argument effects through a buffer, keeps a scalar live across the adapters,
and checks dynamic array arguments and balanced releases. Real loopback TCP
and timer readiness checks pass. Bootstrap stage 2 equals stage 3; the compiler
is 2,160 bytes smaller than the struct-copy cleanup head.

All 74 paired native corpus emissions have no increase in static instruction
count. All 38 WASI comparisons (the complete corpus and the adapter probe)
are byte-identical. Runtime-helper closure, cross-module need aggregation and
live WASI socket, timer and block checks pass. The smaller native static
ceilings are recorded without changing dynamic counts or tolerances.
