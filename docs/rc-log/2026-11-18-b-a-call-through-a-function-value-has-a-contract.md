# A call through a function value has a contract

2026-11-18. Each target. The change is in `ir`, `ssarc`, `ssa_lift` and
`rccertify`.

## The gap

A call through a function value names no body, so the certifier had no
contract for it. It made each argument unproved, and it gave the result an
unknown origin. Over `fern_main`, 315 unproved values were results of such
calls, and 276 more were phis of such a result and a direct call.

The unit plan has the contract: `ssaunits.call_value_supplied` gives the
operands that hand their unit to the callee, from the `own` marks of the
function type. A counted result is a unit that the call hands out.

## The change

- The lowering writes the contract on the op (`ssarc.with_units`). One bit
  says that the contract is known, and one says that the result holds a
  unit. Bit j says that operand j hands its unit to the callee
  (`ir.call_units`). The bits are in the width of the op, which no backend
  reads for this op.
- The lift copies the contract to the instruction.
- The certifier spends each operand that the contract hands to the callee,
  and it gives a counted result a fresh unit. A raw call, such as
  `__raw_call1`, has no contract, so its rule stays as it was.

The emitted code is the same: the assembly of `fern_main` is byte for byte
the same before and after.

Four new cases in `self_host_rccertify_test.go` check the contract. They
are an operand that the call takes, a result that is released, a planted
leak of that result, and a raw call.

## Measured after

The census rows, re-banked in `.github/certify-baseline.txt`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 4,386 | 3,448 |
| `wasm32/checker.unproved` | 1,284 | 739 |
| `x86_64/checker.refuted` | 5,042 | 5,166 |
| `x86_64/checker.weakened` | 236 | 236 |
| `x86_64/checker.missed` | 378 | 254 |

The certifier finds nothing over `fern_main`.
