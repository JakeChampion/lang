# Counted enum payload returns

Main's enum helper-return and alias-return regressions retained the escaping
array correctly but stranded its parent-owned reference. The previous tests
expected one leaked allocation per iteration. With the additional return unit,
both parent and child leaked. Restoring an uncounted return would free buffers
that live parents still reference.

`enumcontract` imports a closed typed region before physical RC. Semantic values
carry exact types and immutable identities. Lexical bindings map to those
values, so enum aliases keep the original parent identity and shadowed names
resolve separately. Variant guards contain typed projections. The independent
verifier checks definition order, exact field types and guard identity, unique
fresh child supplies, root bindings and parameter identities. It reads no AST.

Array returns are fresh arrays, projections or results of verified leaf calls,
using the existing counted array-return ABI. Enum roots cannot be returned, stored,
captured or passed to unknown calls. Parameters remain borrowed roots. A local
root retains its fields until the final parent cleanup; an escaping projection
acquires a separate count first. Verified parent cleanup replaces the old
consuming-match drop and moved-payload skip for that binding. The existing
runtime's uniqueness guard determines whether a parent release walks children.

The syntax adapter currently supports scalar and i32-array returns, immutable scalar/enum
bindings, scalar reads, fresh literal payload construction, matches and if
branches. Calls require exact argument and result types and an independently
verified leaf region; recursive and unknown calls receive no contract.
It refuses assignments, arbitrary calls, closures, loops, nested
patterns, guarded arms, other payload representations and multiple local roots
that would require a storage-reuse contract. Refused regions gain no cleanup
rights. This is a production repair within the broader typed-IR migration;
the remaining AST ownership analyses still need retirement.

The two original regression assertions now require exact heap balance. Runtime
coverage also preserves old aliases and shared children under heap churn,
checks both conditional-return paths and empty variants, and tests lexical
shadowing. Over-release is checked after the exercise frame returns. A mutation
that removes the payload return retain must fail with that underflow exit.
Verifier tests reject omitted guards, wrong fields, invalid parameters and
unsupported source effects. Native and Wasm emitters run the same fixtures;
the actual self-host-built compiler has a separate cross-target matrix.

```sh
scripts/devbox go test ./internal/e2eselfhost \
  -run '^TestSelfHost(RcEnum(BorrowHelper|AliasBind)X86_64|ArrayClaimContracts|ArrayClaimLiveParentX86_64|EnumContract.*)$' -count=1 -v
scripts/devbox make lint-all
scripts/devbox go test ./internal/lint -count=1
```

The compiler-size gate is unchanged. The new semantic verifier and source
adapter are included in the production lowering dependency graph; their size
is functionality cost, not a baseline exemption. Record matched revisions and
raw size reports before making a size or performance claim.
