# Verified ownership plans to production IR

Under #8920, `ssarc.lower` connects the independent counted-unit verifier to
the production stack IR used by the self-hosted native and Wasm backends.
For example, returning a borrowed row from a local array retains that row
before releasing its parent. The emitted function uses the existing runtime
ABI, including its target-specific array and tuple layouts.

## Boundary

The input is a `ssasem.Func`, explicit parameter modes and an `ssaunits.Plan`.
Lowering re-verifies the entire plan against freshly derived semantic facts.
The physical vocabulary accepts reachable acyclic control-flow graphs with
32-bit integer/boolean values and recursively nested arrays and tuples.
It rejects reachable cycles and other physical representations, returning no
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
Validation, physical frame construction and instruction selection are separate
internal boundaries; the public lowering function sequences the verified plan.
Type admission lists only the implemented representation families and checks
tuple fields recursively. Unsupported types still fail before emission; this
also keeps the combined parent stack within the unchanged complexity limit.

## Branches and phi transfers

Verified predecessor edges determine a topological layout. Nested stack-IR
blocks provide forward labels, and jumps to the next layout block fall through.
Conditionals execute only the selected edge's transfers. Identical true/false
targets execute that edge once. Unreachable blocks are still verified but emit
no code and do not contribute to the layout's predecessor counts.

Each plan step is selected by block, instruction/edge point and target. Before
an edge releases old units, it retains borrowed phi supplies and saves every
phi input on the operand stack. It then assigns destinations in reverse stack
order. This preserves simultaneous phi-copy semantics and keeps projected
children alive when their parent dies on the edge. Scalar phis copy values;
reference phis receive the independently verified counted supplies.

## Executable validation

The tests replace matching parsed function stubs with the verified physical
result in the production emitters' lowering cache. Eighteen fixtures cover
projected returns, shared parents, duplicate children, empty parents, unused
deep aggregates, copied projections, mixed scalar/reference tuple fields,
borrowed/counted/unused parameters, both branch outcomes, reordered blocks,
duplicate and scalar phis, parent drops on edges, early returns, same-target
conditionals, unreachable predecessors and nested branches. Each
fixture executes 32 rounds with heap churn. Native runs require nonzero,
exactly balanced allocation/free counts and zero live bytes; all targets
check values and the runtime over-release counter after the exercise frame's
final cleanup. X86 also runs sanitized.
Wasm runs check semantics and over-release, not a native heap census.

A negative control removes the physical retains from the parent-drop edge
fixture, verifies that the mutation was applied, and requires runtime failure
on every target. Checking underflow before the exercise frame's cleanup missed
that error, even with balanced allocation/free totals; the post-frame check
detects it. The mutation applies only to generated test output.

The same fixture bundle runs with both a Go-built lowering driver and an
ARM64 driver compiled by the actual self-host CLI. Each emits programs for
ARM64, x86-64 and Wasm. Rejection cases exercise failed/corrupt plans, a valid
cyclic graph, and abstractly valid string/wide-array parameter contracts.

The result-type and opcode checks are defensive guards for future semantic
extensions. Semantic verification makes each return type equal to a value type
already checked by the physical boundary. The current physical vocabulary
covers every admitted semantic opcode, including phi. Consequently these
guards cannot independently reject a currently valid plan after earlier guards
pass. New semantic operations must add a direct physical refusal test before
relying on these guards.

```sh
scripts/devbox go test ./internal/e2eselfhost \
  -run '^TestSelfHostSSAPhysicalRC($|IRArm64$|Rejects$)' -count=1 -v
scripts/devbox make lint-all
scripts/devbox go test ./internal/lint -count=1
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
constitutes that fix. Loop lowering, typed frontend import,
coverage expansion, production cutover and deletion of obsolete ownership
analyses remain required. Main's enum-return leaks were separately repaired
by #8990, with exact heap-balance assertions and no baseline relaxation.
