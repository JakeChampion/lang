# Recursive nominal records in typed IR

The opt-in ARM64 typed pipeline now represents and executes direct and mutually
recursive concrete record types, including monomorphized generic instances.
This extends the [record-value slice](TYPED-RECORD-VALUES-2026-09-09.md); it does
not switch production compilation to this route or retire the native/self-host
AST ownership implementations.

## Correct pass boundaries

The previous recursive-type restriction came from the optional `solveReturnFlow`
analysis, which expands complete type trees and exact parameter field paths.
The executable pipeline does not call that analysis. It uses `planProgramUnits`
and independent whole-module verification of the counted-result call convention,
local ownership units, projection anchors and callee obligations.

Requiring the semantic representation to fit the optional analysis unnecessarily
prevented execution that the independent ownership contract could support.
Recursive types now belong to the finite nominal interface graph. The optional
tree analysis separately checks its own acyclic-type requirement before expanding
any flow, returning an explicit error and no summary for recursive input. It
does not silently substitute unknown, empty, generated or owned facts. An unused
recursive catalogue entry does not obstruct analysis of acyclic function values.
All existing exact acyclic provenance tests remain in place.

This is not the final recursive provenance analysis. Memoizing flow nodes only
by nominal type would conflate roots with descendants. General recursive field-
path relations also need more than an assumption that every relation is regular:
a function returning `root` at depth zero and otherwise
`pick(root.left[0], n - 1).right[0]` can reach a path with `n` left steps followed
by `n` right steps. A permanent execution case checks the depth-two instance,
but no exact recursive summary or borrow-return optimization is claimed.
Finite symbolic constraints and their required queries remain subsequent work.

## Complete typed interfaces

Imports predeclare temporary private contracts by nominal identity, then copy
their ordered field interfaces. Back edges retain nominal names rather than
recursing into an AST declaration or inventing a structural tuple. The new
contracts enter the program catalogue only after the complete reachable import
succeeds. A missing nested declaration cannot publish a partial component or
modify previously imported interfaces. Retry after correcting the source metadata
is covered, as is detachment from mutable checker field-type slices.

Semantic verification uses invocation-local visiting/done states. Encountering
a back edge is valid recursion, but does not mark the active record done: the
remaining fields are still checked. Malformed owner/name/type/field identities
after a recursive edge must still reject. Full nominal operation and call
contracts, counted ownership rules and lifetime verification remain unchanged.

## Runtime ownership and physical lowering

Recursive types do not imply cyclic runtime objects. The admitted operations
construct new records and arrays from existing values, preserving an immutable
runtime DAG. They do not mutate heap fields or create reference cycles. Shared
children still require independent counted stores and live projections still
retain their anchors. This change adds no cycle collector or speculative reuse.

Immediate field layout is finite. The existing physical helper builder reserves
a helper's identity before emitting its body, so recursive deep-drop calls reuse
that identity instead of recursively generating more helpers. A mutually
recursive fixture verifies exactly one drop helper for each of its five distinct
reference-bearing types, separately from the source functions. No new drop
algorithm or AST ownership heuristic was necessary.

Runtime deep drop still uses recursive calls, as the existing aggregate lowering
does. The tests establish the exercised depths and balanced ownership, not
arbitrary-depth stack safety. Iterative destruction, where warranted, needs its
own measured implementation and lifetime proof.

## Validation and measurements

Four shared interpreter/native cases cover an escaped recursive child under
allocator pressure; 64-level iterative construction and shared immutable update;
mutually recursive records with own/borrow calls and conditional cleanup; and
recursive call/projection paths. Raw and optimized native Linux ARM64 executions
agree with the interpreter and have balanced allocations/frees and zero live
bytes. The CLI compiles and executes distinct generic recursive instances with
an escaped array surviving subsequent allocation churn.

Additional tests cover transactional import failure/retry, checker detachment,
invalid fields after back edges, foreign or missing recursive interfaces, finite
drop-helper generation and the separate optional-analysis capability boundary.
The initial full semantic suite passed in 0.673 s, all four raw/optimized native
cases in 0.073 s, and the generic CLI case in 0.568 s. Final broader validation
also passed:

- Full semir, sourcelint and CLI suites: 1.117 s, 25.177 s and 43.887 s.
- Full SSA suite: 106.035 s.
- Strict native Linux ARM64 typed/runtime and CLI suites: 5.495 s and 14.284 s.
- Targeted race tests: 1.491 s. `make lint-all` passed.

Cross-target integration CI remains a current-head merge gate.

Compile measurements used native Darwin ARM64 on Apple M3 Pro, Go 1.26.0,
parent `4ca1e7cc9`, and five 100 ms samples. Parent and candidate ran sequentially
before the heavy validation suites. Parsing/checking is outside the timer;
typed construction, ownership verification and ARM64 SSA lowering are inside.
Recursive inputs form a cycle of 1, 8 or 64 nominal declarations and lower a
consuming function, requiring the full finite drop-helper graph.

| Recursive type count | Time range | Bytes per operation | Allocations per operation |
| --- | --- | --- | --- |
| 1 | 29.506-40.939 us | 52,552-52,554 | 673 |
| 8 | 100.803-110.266 us | 155,826-155,834 | 1,931 |
| 64 | 675.349-717.011 us | 1,017,207-1,017,268 | 11,756-11,757 |

The parent rejects these inputs, so these are costs of new capability, not
speedup comparisons. Existing ordinary cleanup inputs retain 1,208 and 3,666
allocations at 1/8 actions, and 19,720-19,721 at 64 actions. Existing acyclic
record inputs retain 950, 1,924 and 7,871-7,872 allocations at 1/8/64 fields.
Byte-count ranges overlap between parent and candidate. Timing samples are noisy
and mixed across those controls; they do not establish a speedup or exact timing
parity. No performance or binary-size baseline is changed.

Comparable `-trimpath -buildvcs=false` compiler builds both occupy 29,075,154
file bytes. Mach-O `__text` changes from 10,177,348 to 10,177,284 bytes (-64).
Symbol-level attribution accounts for that instruction delta: transactional
publication grows the importer entry from 192 to 480 bytes, its copying closure
shrinks from 1,792 to 1,552, and the record verifier shrinks from 1,424 to 1,312.
The optional analysis capability guard is not linked into the executable.
These are size measurements, not runtime performance claims.

Reproduction:

```sh
go test ./internal/semir -run '^$' \
  -bench 'BenchmarkRecursiveRecordBuild|BenchmarkTypedCleanupActions|BenchmarkRecordValuesBuild' \
  -benchtime=100ms -count=5
go build -trimpath -buildvcs=false -o /tmp/fern-recursive-records ./cmd/fern
/usr/bin/size -m /tmp/fern-recursive-records
```

Raw local results use `/private/tmp/lang-recursive-records-*`.
