# The drop thunks' pointer parameter becomes `usize` (#7866)

Every synthesised drop thunk declared its argument — the heap pointer
it releases — as `ast.NumberType{}`. Width 0 is not pointer-width, so
`ssa/lift.go` set `ParamAddrs[0] = false`, the ownership solver's
phase A short-circuited before asking `demandsUnit`, and the parameter
read Borrowed whatever the body did. `generatedDropPrefixes` masked
the whole family: the table answered for the thunks before the solver
was reached, which is why nothing showed until `__closure_drop_` —
the one member missing from the list — produced two false leak
findings (#7865).

## The issue's codegen concern did not survive contact with the code

#7866 predicted the fix "moves widthOfAstType from 32 to pointer
width, which reaches backend parameter widths". It does not:
`widthOfAstType` returns 32 for `WidthPtr` (the stack-slot width —
its own comment says so), wasm's `valtypeFor` still answers i32, and
the flat backends size frames and spill parameters width-blind. The
right type is `usize` — `NumberType{Width: ast.WidthPtr}` — which
flips address-ness and nothing else, and matches the hand-written
sibling `__map_drop_values(m: usize): usize`.

Measured rather than argued: the whole conformance corpus emitted
under the before and after compilers on all three targets —
x86-64 asm, arm64 asm, wasm — is byte-identical, every row.

The one place output could legitimately move is the opt-in SSA-direct
backends, where `ResolveWidths`' full-length-ParamAddrs guard was
REFUSING to widen the thunk's pointer chain — a latent truncation of
any heap pointer above 0x7fffffff on `-backend ssa`. Those backends'
own suites and the arm64 flat-vs-SSA differential stay green.

## The sites

15 declarations in `rc_insert.go` (the issue counted 6; only
`__cdenv` matched its grep, the other families use different names)
plus the three `__drop_dyn_` params in `ir.go` (`__dcell`, `__ddata`,
`__dvtbl`). All now share one `dropThunkParamType`.

Out of scope, each its own follow-up: the thunks' `ReturnType` (same
defect on the return axis, wider blast radius through
`callee.ReturnAddr`), `dropSig` at the `OpCallDyn` dispatch (its own
comment says "(ptr)->ptr" while typing it number), `__boxptr` in
`buildDynboxWrappers` (declared i32 outright), and `closureconv`'s
`__env` (deliberately compensated by `width.go`'s backwards
propagation).

## The gate

`TestGeneratedDropThunksLiftAsConsumedPointerTakers` lowers a program
exercising the struct / enum / tuple / array-of-struct families and
asserts every generated thunk lifts with `ParamAddrs[0]` true and
settles Consumed on the solver's own evidence. Verified non-vacuous:
with the fix reverted, 4 of 4 families fail with `Pointer[0] = false`.
The `generatedDropPrefixes` table stays — it is the only record on the
result axis and for the flat-IR verifier, which runs before SSA
exists — but a new drop family that forgets the pointer type now
fails the gate whether or not anyone remembers the table.
