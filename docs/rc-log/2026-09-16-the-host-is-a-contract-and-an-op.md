# 2026-09-16 — the host is a contract AND an op

Seven host builtins had no semantic contract, so every module reaching one kept
the AST lowering: the three clocks (`monotonic_ns`, `now_ns`, `now_unix_ms`),
the kernel's randomness (`random_bytes`, `random_i32`), and the two sleeps
(`sleep_ms`, `sleep_ns`).

**The contract.** None of them is handed a reference, so none borrows. The
clocks and `random_i32` read a host counter and own nothing; `random_bytes`
hands back a fresh buffer this frame owns, the way `args` hands back a fresh
array; the sleeps take a count and answer nothing.

**And the op.** A contract alone is half the work, which the comment on
`intrinsic_site` already said in as many words: without the op the name reaches
the backends as a direct call to a symbol no runtime defines, and the prune rule
correctly turns the body off as one the AST lowering must define. Each of the
seven is an op of its own, the same one the AST lowering emits, so the new
`host_builtin_site` sits beside the existing value and builder sites.

Whole modules produced: 425 → 430. `audit_env_time_random`, `audit_now_ns`,
`audit_std_uuid` and `random_bytes` join, and no refusal naming any of the seven
remains.

## Two wasm bugs behind the sleeps

Neither is the semantic lowering's; both are pre-existing, reproduce on the AST
lowering, and do not affect native. The first hid the second.

**#9476 — the assembler had no 16-bit memory opcode.** A preview1 `subscription`
writes its `subclockflags` field with an `i32.store16`, and `watbin.fern` could
not encode it: "unknown instruction: i32.store16". So no wasm BINARY containing
a sleep could be produced at all. `memarg_align` already classified the whole
16-bit family; `mem_load_opcode` and `mem_store_opcode` never got it. All three
rows go in together — adding only the store would leave the same latent gap on
the two loads the alignment table equally claims.

**#9477 — `$__fern_sleep_ms` declared an i32 parameter.** `sleep_ms`'s parameter
is i64 and every backend passes one, so the module failed validation with a type
mismatch once the assembler could encode it. Its nanosecond twin
`$__fern_sleep_ns` already had the shape right. The widening of `sleep_ms` to
i64 reached the register backends — `TestSelfHostSleepMsI64IR` pins that — and
the wasm helper was missed, because that test reads the emitted ASM and nothing
looked at wasm.

## Pinned

The four conformance cases the leaf unblocks — `audit_env_time_random`,
`audit_now_ns`, `audit_std_uuid` and `random_bytes` — run on every target
through the self-host fixture legs, and their rows in the conformance leak
census pin what each of them leaks, so a change to that is caught. Each was
also checked by hand on wasm on BOTH lowerings, matching its expected exit.

Two new wasm tests, because the existing wasm sleep tests pass `sleep_ms(50)` —
an unsuffixed literal, which is i32, and so matched the wrong parameter type and
never saw #9477:

- `TestSelfHostSleepWasm` emits WAT both ways and runs it, with an i64 count.
  It fails with "type mismatch: expected i32, found i64" without the fix.
- `TestSelfHostSleepWasmBinary` builds a wasm BINARY through the CLI, which is
  the only thing that runs the self-host's own assembler — the WAT test hands
  wasmtime text and wasmtime's parser assembles it. It fails with "unknown
  instruction: i32.store16" without the fix.

Both were verified to fail with their fix backed out and to pass with it.

A footnote worth keeping: the first spelling of that test file was
`self_host_sleep_wasm_test.go`, and Go silently excluded it. `wasm` is a GOARCH,
so a file ending `_wasm_test.go` is architecture-gated and never compiles on a
linux/amd64 host. It read as green while running nothing. The name ends
`_wasm_ir_test.go` now, matching the sibling files, and `make testnames` is the
gate that catches the general case.

## What is NOT pinned here, and why

`TestSelfHostSemanticSourceRC` gets nothing from this change, which is not the
usual shape for a leaf and is worth stating plainly. Three separate things stood
in the way, each filed:

- `random_bytes` cannot be in a fixture that asserts allocs == frees. Its array
  box is released correctly — the lowering emits the `__fern_arr_dec` an array
  literal gets, and I read the emitted asm to be sure — but the runtime's
  getrandom scratch buffer leaks one block per call, on every target and both
  lowerings (#9478), and `__free` is a no-op under the bump heap.
- The two sleeps leak their 88-byte poll_oneoff subscription on wasm, for the
  same reason (#9480). Adding one `sleep_ns` moved that leg's standing leak
  from 8976 to 9064 bytes, exactly 88.
- Adding a call to any of the remaining five — the three clocks or `random_i32`
  — to that fixture's `main` makes its wasm leg TRAP, in a drop of an unrelated
  struct, while x86-64 and arm64 pass (#9481). Two extra calls to functions the
  fixture already has do not do it, and both helpers alone in a small module are
  correct on wasm on both lowerings. It needs that one ~113 KB module to show,
  and it is not understood yet.

So the fixture entry is what waits on #9481, not the contracts: every program in
the corpus that uses these builtins is verified correct on wasm both ways.
