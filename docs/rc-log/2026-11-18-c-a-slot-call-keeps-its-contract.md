# A slot call keeps the contract of its call

2026-11-18. Each target. The change is in `ssa`, `rccertify` and `ssarc`.

## The gap

`ssa.slot_calls` rewrites a call through a closure slot, and it gives the
call the text `"slot"`. That text replaced the contract that the lift gave
the call (`ir.call_units`). The certifier first read `"slot"` as the
contract 0, which gave false findings in 14 programs. The repair of that
fault made such a call unknown again, so most calls through a function
value in `fern_main` had no contract.

The contract also marked the callee of a `once` call as an operand that
hands its unit. The body of a `once` closure borrows its environment, and
the caller drops it after the call (#16170). A contract with that mark
makes the certifier spend the closure two times. Most `once` calls are slot
calls, so the mark showed only when slot calls kept their contract: 11
programs with tasks or continuations gave findings.

## The change

- `ssa.slot_calls` writes `"slot:"` and the contract, and `ssa.slot_call`
  reads both forms. The certifier reads the contract after `"slot:"`.
- `ssarc.with_units` never marks the callee as handed.
- A new case, `slot-call-keeps-its-contract`, checks a slot call whose
  contract hands its parameter.

The emitted code is the same.

## Measured after

The 2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities give 0 findings on x86-64, arm64 and wasm32.
