# A slot call keeps its contract

2026-11-19. Each target. The change is in `ssa`, `ssarc` and `rccertify`.

## The gap

`ssa.slot_calls` rewrites a call through a function value that reads its
target out of its own environment. The rewrite set the text of the call to
`slot`, so the contract that the lift wrote there (`ir.call_units`) was
lost. Most calls of a closure are slot calls, so the certifier read most
calls with no contract. An `own` operand then stayed a held unit, and the
four Android builds failed with a leak that the census does not show
(#16472).

With the contract kept, a second error showed. The contract said that a call
of a `once` function hands over the callee. The plan supplies the callee
(`ssaunits.call_value_supplied`), but the lowering drops it after the call,
because the body only borrows the environment. So the certifier saw the
release after the call as a release with no unit. Nine fixtures of the
corpus and two wasm sim tests failed with this.

## The change

- The rewrite writes `slot:` and the contract, and `ssa.slot_call` takes
  both forms. The certifier reads the contract after `slot:`.
- `ssarc.with_units` does not set the bit of a `once` callee.

The emitted code is the same. No backend reads the contract.

`TestSelfHostCertifiesASlotCall` compiles a `once` continuation and an `own`
operand through a slot call on each target, and checks the leak census. The
`once` case fails without the second change. Two cases in
`TestSelfHostRcCertifyCases` check the `slot:` text.

## Measured after

On main `e33a584c`:

| row | before | after |
|---|---:|---:|
| `x86_64/fern_main.unproved` | 3,449 | 2,787 |
| `wasm32/checker.unproved` | 739 | 312 |

The other rows do not move. The second change moves no row.
