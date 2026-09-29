# 2026-09-29 — a defer in a struct-union member arm reads the member (#10466)

Typed path (`semsource.fern`: `learn_escaping`, `narrowed`).

The lifted pattern binding of 2026-09-27 (a defer in a match arm replaying
the arm's binding outside it) learned its slot's type from `arm_payloads`,
which for a struct-union member arm answers the member's FIELD types. So a
member arm primed the slot at the zero of the member's first field, and
`narrowed`, which binds the whole member, bound a new name that shadowed the
slot instead of replacing it.

`learn_escaping` now learns the member type for a member arm, the type
`narrowed` binds, and `narrowed` replaces a prebound slot as `payloads`
already did.

## Witness

`TestSelfHostDeferInLoopIR/match_arm_in_loop_member`: `A { name: string, x:
i32 }`, the defer passes the arm's `A` to `g(e: E)`. Before, the typed
lowering refused `f` with "call argument type" (the slot was a `string`)
and the CLI fell back to the AST lowering. Now it is produced whole under
`FERN_SEM_IR_STRICT`, answers the interpreter's 55 on x86-64 and wasm, and
balances its census, 4 allocations and 4 frees. The AST lowering answers 55
too but leaks 144 bytes, one reason not to fall back to it.
