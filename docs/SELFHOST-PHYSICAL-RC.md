# Verified ownership plans to production IR

Under #8920, `ssarc.lower` connects the independent counted-unit verifier to
the production stack IR used by the self-hosted native and Wasm backends.
For example, returning a borrowed row from a local array retains that row
before releasing its parent. The emitted function uses the existing runtime
ABI, including its target-specific array and tuple layouts.

## Boundary

The input is a `ssasem.Func`, explicit parameter modes and an `ssaunits.Plan`.
Lowering re-verifies the entire plan against freshly derived semantic facts.
The initial physical vocabulary accepts single-block return graphs with
32-bit integer/boolean values and recursively nested arrays and tuples.
It rejects other control flow and physical representations, returning no
operations or locals on rejection. Strings, wide scalars, nominal schemas,
closures and semantic calls are not admitted by this physical boundary.

SSA value IDs map to distinct physical locals, with parameter IDs occupying
the existing ABI's parameter positions. Retained supplies precede construction
and return. Moves transfer existing counts without runtime operations; there
is no additional implicit local sweep. Drops follow the operation which last
uses their values. A parent releases children only when its reference count
is unique, then releases its own box. Repeated fields each own a count, even
when they refer to the same child. Recursive child walks use stack-IR array
and tuple operations, not hard-coded byte offsets.

Scratch locals are reused across sequential drops and separated by recursive
depth. The result runs through the existing production IR optimizer before
being handed to the backend's normal IR verification and emission path.

## Executable validation

The tests replace matching parsed function stubs with the verified physical
result in the production emitters' lowering cache. Ten fixtures cover
projected returns, shared parents, duplicate children, empty parents, unused
deep aggregates, copied projections, mixed scalar/reference tuple fields,
and borrowed/counted/unused parameters. Each
fixture executes 32 rounds with heap churn. Native runs require nonzero,
exactly balanced allocation/free counts and zero live bytes; all targets
check values and the runtime over-release counter. X86 also runs sanitized.
Wasm runs check semantics and over-release, not a native heap census.

The same fixture bundle runs with both a Go-built lowering driver and an
ARM64 driver compiled by the actual self-host CLI. Each emits programs for
ARM64, x86-64 and Wasm. Rejection cases exercise failed/corrupt plans, a valid
multi-block graph, and abstractly valid string/wide-array parameter contracts.

```sh
scripts/devbox go test ./internal/e2eselfhost \
  -run '^TestSelfHostSSAPhysicalRC($|IRArm64$|Rejects$)' -count=1 -v
scripts/devbox make lint-all
```

## Remaining production work

This module is physical lowering infrastructure. The default frontend does
not yet import it, and it does not retire AST ownership. A production caller
must establish the same verified types and parameter/return contracts as the
callee before substituting its body. The parameter ABI tests therefore use
explicit physical caller supplies. They do not infer counted call contracts
from the parsed stubs' ASTs.

An earlier experiment substituting these bodies under AST-derived callers
leaked one allocation for a borrowed parameter and failed at runtime for a
counted parameter. The explicit caller contracts pass. This is evidence that
default integration needs closed-module call verification, not permission to
assume the old caller analysis agrees with a new callee contract.

The nested-array `.with` source failure found while building semantic
dependency rows also remains unresolved: the existing store does not retain
the row before loop reinitialization releases it. Its reduced source fails on
all three targets. Correct replacement needs copied-child supplies,
replacement drops, alias preservation and parent cleanup together. Neither
these physical construction/projection operations nor an AST escape exception
constitutes that fix. General control-flow lowering, typed frontend import,
coverage expansion, production cutover and deletion of obsolete ownership
analyses remain required. The two existing enum-return leak assertions also
remain merge blockers; no baseline or assertion is relaxed here.
