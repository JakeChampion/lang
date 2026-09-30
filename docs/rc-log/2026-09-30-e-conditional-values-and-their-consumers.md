# 2026-09-30 — a conditional's value and its consumers (#10552, #10553, #10554)

Native (`internal/ir`: `rhsTainted`, `matchArmYieldCounted`,
`countedConditionalType`, `lentConditionalArgs`, `dynReceiverLowering`).
Three gaps left by #10529's counted-conditional work.

- **#10552.** A match arm yielding its own payload binding (`Some(xs) => xs`)
  is retained by `emitCountedYield`, but the analysis asked
  `needsRcIncOnAlias` of `xs`. A binding is out of `exprType`'s scope there,
  so the question answered no. With the binding's borrow taint, the result read
  as a borrow: `v` never freed, and the argument temp never released.
  `matchArmYieldCounted` answers from `BindingTypes`.
- **#10553.** An array-view arm taints its source for the rest of the
  function. A conditional that is itself an argument of a user-function call
  whose result cannot alias it is lent, as a bare view argument is, so its
  view arms leave the source releasable.
- **#10554.** A dyn method call's receiver (`mkd(j).area()`) had no release
  path at all. An owned receiver temp is now parked, registered for an early
  exit, and released after the call when the result cannot be the receiver.

## Measured (allocs / frees; x86-64 sanitize, wasm leakcheck)

| shape | before | after |
|---|---|---|
| #10552 issue program, x86-64 and wasm | 15 / 11 | 15 / 15 |
| #10552 match as a call argument | 8 / 4 | 8 / 8 |
| #10553 view arms, `if` and `match` | 16 / 12 | 16 / 16 |
| #10554 receiver temp + conditional receiver, x86-64 | 13 / 1 | 13 / 13 |

Witnessed by `match-payload-yield`, `lent-view-arms` and `dyn-owned-receiver`
in `TestConditionalValueIsReleasedByItsConsumer`, on x86-64 and arm64 under
the sanitizer and on wasm under leakcheck. Each fails without its change.

## Kept as a sound leak

A view arm passed to a callee that returns it (`idv(if (c) { a[0:2] } else
{ a[1:3] })`) keeps the taint: 12 / 8, as before.
