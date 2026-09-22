# 2026-09-22 — the stack machine's function driver is gone

`docs/SELFHOST-SSA-BACKEND.md`'s retirement order ended at "the deletion
itself": the stack machine's per-function driver on both native ISAs, kept
as the fall-back for a function the register path declined, after the
corpus lane had held the decline count at zero on 866 modules per ISA. This
entry is that deletion.

## What changed

On arm64 and x86-64 the register path is the only emitter. Each backend's
`emit_function_via_ir_named` verifies the lowered ops and hands them to
`emit_ssa_function`; a function the lift or the emitter cannot take is a
refusal naming the function and the op (`ircore.ssa_refuse`, exit 3), where
it used to fall back to the stack machine. Gone with the driver:

- the stack machine's prologue, zero-initialised local slots, parameter
  copy, op-index labels (`.Lir_*` / `.Lira_*`) and epilogue, on both ISAs;
- every arm of `emit_stack_op` for an op the lift lowers itself — the
  spine, the box layouts, the host floor, the f64 ops, the string ops the
  lift makes stack-ABI calls — 119 of the 218 arms on x86-64 and 116 of
  215 on arm64, plus the six lifted string ops in each `emit_str_op`. What
  stays is one arm per op the register path bridges through `ssa_flat_op`
  (the OS floor, the map ops, the byte kernels, the byte-buffer builder),
  and a refusal (`ircore.flat_arm_missing`) for an op that reaches the
  table without one, where the old catch-all emitted a binary op;
- the helpers only those arms reached: the cond-jump and flag-branch
  scanners (`emit_ir_cond_jump`, `ircore.flag_branch`,
  `ircore.flag_branch_consumer`, `ircore.br_target`), the shift-by-previous-
  constant readers, the constant pushers, and the per-op emitters for
  calls, dispatch, constructions and conversions;
- `-backend flat` on the native ISAs (refused, naming the retirement; it
  still names wasm's one emitter), the `FERN_SSA_ONLY` / `FERN_SSA_SKIP`
  bisect knobs (a declined function has nowhere to go), the
  `ssa_backend` / `ssa_emitted` / `ssa_declined` fields and the module
  tally they fed, and the corpus coverage gate and `TestSSACoverageProblems`
  that read the tally: a decline is now a compile failure the fixture legs
  report on their own. `FERN_SSA_REPORT=1` keeps the one line it still has
  a use for, the function whose lift or emit took over 200 ms.

2,453 lines leave `examples/self_host/`, 161 arrive.

## The one thing the driver had that the register path did not

The x86-64 stack machine lowered a divide by a literal (`ir_div_const`: a
power of two is a shift with the round-toward-zero bias, an i32 divisor
neither zero nor a power of two the multiply-high reciprocal, an i64 one
the unguarded divide) and the register path did not: its division arm kept
every divisor in a register and ran the guarded `idiv`. Deleting the driver
would have deleted the only copy. It moved instead: `ssa.Imms` now says
which values are integer constants at all (`konst`) and what each is worth,
the four div/rem kinds are immediate-operand consumers on x86-64 so the
constant is never materialised, and `ssa_binary` renders `ir_div_const` for
a constant divisor. `TestSelfHostConstDivisorShapesX86_64` pins the shapes
through the `asm_ir_run` driver, which now emits through the register path.

## Purity

The compiler compiling itself, `-emit asm` of `fern.fern`, the compiler
built from the parent commit against the compiler built from this one:

| | old | new |
|---|---|---|
| arm64-linux | 3,188,217 lines | **byte-identical** |
| x86-64-linux | 3,241,459 lines | 3,240,057 lines, 272 functions differ |

Every differing x86-64 function is a divide by a literal now taking the
literal-divisor form (`util__i32_to_string`'s `/ 10` and `% 10` are the
first two), which is the change above and nothing else: the driver
deletion itself moved no byte on either ISA. The same three small programs
run to native's stdout and exit status on both ISAs.

## Gates

`TestSelfHostSSABackendAgreesWithStackMachine` compared the register path
with the stack machine; with one side gone it is
`TestSelfHostSSABackendAgreesWithNative`, the same programs compiled by the
native compiler for the other side, on both ISAs. `TestSelfHostSSAConstants
AreImmediates` pinned the divisor 97 in a register on x86-64; it now pins
the literal-divisor form loading it and the two run-time guards absent.
The unwind test lost its flat leg, `-backend flat` is pinned refused on the
native ISAs and accepted on wasm, and the tests that read `.Lir_` labels
as "went through the IR" read `.Lssa_`.

## Found on the way

The `floats` differential program called `f32_bits` on an f64. Native
refuses that (E038, argument 1: expected f32) and the self-host accepts it:
`checker.fern` types the four float-bits builtins' results
(`free_builtin_result`) and never their parameters, so a bare-ident call of
one is not argument-checked. The program is corrected to `f32_bits(y as
f32)`; the checker gap is #9987, the next fix.
