# 2026-09-04 — a nested match scrutinee is excused only where the inner enum owns its payloads

`nestedMatchConfines` (2026-09-03-nested-enum-scrutinee-native.md) excuses a
nested `match (o)` so the outer join reclaims the call-result box. That reclaim
is a **deep** drop: it reaches the inner box through the instantiation's
generated `__drop_enum_`, which releases the inner payloads. The excusal did
not check that the inner enum ever took a reference to them.

## Measured

Twelve lines, `pre` a live local read again after the loop:

```
@noinline
function mk(pre: string[]): Option[Option[string[]]] { return Some(Some(pre)); }
```

matched as `match (mk(pre)) { Some(o) => { match (o) { Some(xs) => … } } … }`.

| model | before | after |
|---|---|---|
| EnumRcPayloads on | exit 0 | exit 0 |
| move (EnumRcPayloads off) | exit 226 — `pre`'s buffer freed under the caller | exit 0 |

CI caught it as `TestX86_64EnumRcPayloadsMatchesMove/audit_std_json`: the move
leg exiting -1 where the rc leg printed `OK`. `std/json` aliases its payloads;
the five positions the original commit measured all build theirs fresh inside
the producer, so none of them could see it.

## Why the gate is where it is

`enumRcPayloadsEligible`'s contract already says an ineligible enum "keeps the
move model (flag-off behaviour) at every site", and `reclaimableTryScrutinee` —
the value-consuming sibling — already requires exactly this fact before its own
box free, for exactly this reason. The match position was the one path that
deep-dropped without asking.

## Trap

`b.exprType(m.Tag)` is **nil** for the nested scrutinee at this point in the
analysis, so the type has to come from the caller. It is threaded as `bt`
through `bindingConfinedToArm` / `bindingConfinedInAll` — the type of the
binding being checked, which every caller already holds.

Resolving the enum from the arm's `VariantName` instead was rejected:
`lookupVariantIn` with an empty enum qualifier scans `info.Enums` in map order,
so two enums sharing a variant name would make the verdict non-deterministic.

A first attempt gated on the OUTER scrutinee rather than the inner one and
refused everything: `TestX86_64NestedEnumScrutineeReclaim` went to `frees=0`.
That failure is the proof the reclaim tests are live — the gate has to be
narrow enough to keep them green.

## Pin

`Test{X86_64,Arm64,WASM}NestedEnumScrutineeAliasedPayload` runs the aliased
shape under BOTH models. The in-arm `xs[0].len()` pins the payload readable
through the copy; the post-loop `pre[0].len()` pins the caller's array
surviving. Without the gate it fails on all three backends (x86-64 99,
arm64 1, wasm 1).
