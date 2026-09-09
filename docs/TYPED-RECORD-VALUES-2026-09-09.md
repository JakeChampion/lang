# Nominal record values in typed ownership IR

The opt-in typed ARM64 pipeline now supports acyclic concrete record values:
construction, field projection, immutable spread updates, function calls and
existing control-flow/cleanup composition. Nominal type identity survives until
physical layout lowering. This is not full record coverage or production AST
retirement: recursive record provenance, record patterns, field mutation, broader
types and native/self-host target parity remain explicit migration work.

## Typed declaration and operation boundary

Source construction imports only used record interfaces from checked declarations.
The private program catalogue owns copied field names, full field types and
source positions in declaration order. Nested array/tuple field types are copied;
no AST declaration or checker-info pointer is retained for downstream consumers.
The common CLI performs monomorphization first, so concrete generic instances
retain their distinct nominal identities too. Unused builtin/generic declarations
do not expand the pilot's admitted type surface.

`RecordMake` takes already-evaluated operands in declaration order and produces
the actual `StructType`, not an anonymous tuple. The builder evaluates explicit
field expressions in their original order before arranging the operands.
`RecordGet` has a real container operand and a checked field ordinal. It expresses
containment, not identity aliasing or a counted acquisition. Numeric tuple field
access uses the existing `TupleGet` contract without weakening tuple types.

The verifier checks programme ownership and nominal identity of each interface,
unique nonempty field names, complete supported field types, operation arity,
field ordinals and exact operand/result types. Its record traversal memo exists
only within one verification call over unchanged input. It is not a retained
successful-verification certificate and cannot hide subsequent IR corruption.

Immutable update evaluates its base once, then overrides in source order. Missing
fields project from the original base SSA value, even when an override changes
the source binding that originally supplied that base. A fresh record combines
those fields with the override values. The existing unit planner acquires or
transfers the independently required child references. No AST ownership heuristic
decides whether a shared child can be moved or whether a projection keeps its
container alive.

Record construction and projection reuse the verified counted-aggregate store
effects, projection-aware liveness and independent unit verification. Direct
own/borrow calls, phi joins, optional snapshots and conditional cleanup therefore
compose through their existing typed contracts. Counted ownership does not imply
uniqueness. This slice deliberately makes no in-place record-reuse claim.

ARM64 layout shares the existing internal aggregate allocation/header convention
and field width/alignment rules. Explicit record field contracts drive stores,
loads and deep drops. Physical lowering consumes the typed catalogue, not source
spelling scans or checker declarations. The pre-existing string final-free
limitation is unchanged; this change does not invent ownership for borrowed
string storage.

## Provenance and recursive-type boundary

The return-flow analysis now tracks record fields separately using its existing
exact finite field-path vocabulary. A generated container does not imply its
children are generated: a returned right field retains the right argument's
provenance, not the left argument's or the container identity. Call substitution
preserves those distinctions through record construction and projection.

Recursive nominal types require a further representation change. The current
return-flow structures are finite trees. Adding recursive records by naively
recursing, truncating at a chosen depth or replacing children with a positive
ownership fact would be unsound. Both construction and independent verification
therefore reject recursive field graphs explicitly, including indirect cycles
through arrays. A finite recursive projection/flow graph is a subsequent
acceptance gate, not something this slice claims to have solved. There is no
depth cap on admitted acyclic records and no fallback to AST ownership within
the typed pipeline.

## Validation

Nine shared source cases exercise basic projection, own/borrow calls, shared
children, immutable updates, base-snapshot and field evaluation order, branch/
loop joins, duplicate child fields, nested records, empty records and conditional
cleanup. The nested case keeps a child array alive after replacing every record
and direct binding that previously contained it.
Interpreter results and raw/optimized native Linux ARM64 executions agree, with
balanced allocation/free counts and zero live bytes. Allocator-pressure cases
keep returned child arrays alive after replacing or dropping their records.

The actual CLI test uses multiple concrete generic record instantiations,
immutable update and an escaped child across subsequent allocator churn.
Malformed-interface tests cover missing/foreign/misnamed catalogues, duplicate
fields, unsupported field types and recursive graphs. Operation tests reject
wrong arity, field types and ordinals, tuple erasure and same-layout foreign
nominal call arguments while retaining ordinary SSA validity. Mutating/removing
checker declarations after construction leaves verification and lowering intact,
demonstrating that the typed field interface is independently owned. An exact
return-provenance test pins field distinctions across a call.
An acyclic 32-level nominal chain retains the full field path through lowering;
missing and unmonomorphized interfaces are rejected explicitly.

Local validation on the candidate implementation passed:

- Full `internal/semir`, `internal/sourcelint` and `cmd/fern` suites:
  1.793 s, 24.951 s and 38.892 s respectively.
- Full `internal/ssa`: 104.006 s.
- Strict native Linux ARM64 typed runtime and CLI suites: 2.582 s and 10.330 s.
- Targeted semantic race checks: 1.383 s. `make lint-all` passed.
- Final additional record/interface/deep-chain tests: 0.264 s; all nine native
  record cases in both raw and optimized modes: 0.076 s.
- After integrating the parent review fix `65ea8df7a`, full semir passed again
  in 0.620 s and `make lint-all` passed again.

The full cross-target integration suite remains a current-head CI merge gate.

## Cost discipline

The first compile smoke exposed extra allocations on existing ordinary cleanup
workloads. Importing record interfaces had copied array/tuple types even when no
record was present. A no-record traversal now returns before that copying, and
the ordinary allocation counts are restored. A profile also identified repeated
temporary field-type slices; consumers now borrow an aggregate field view instead.
This view preserves nominal fields and allocates no temporary type list.

Benchmark inputs construct a record with 1/8/64 array fields, make an immutable
update and project one result. Parsing/checking is outside the timer; typed
construction, verification, ownership and ARM64 SSA lowering are inside. Record
execution was unsupported at the parent, so these measure a new capability,
not a generated-program speedup. Existing ordinary cleanup benchmarks provide
the regression comparison. No performance or binary-size baseline is raised.

Native local measurements used Go 1.26.0 on Darwin ARM64, Apple M3 Pro, with
five 100 ms samples per input. Parent `d8f58e754` and candidate benchmarks ran
sequentially without concurrent validation suites. The short timing samples have
visible noise and mixed directions; they do not establish a speedup or timing
parity. Allocation counts provide the stronger ordinary-path regression check.

| Existing cleanup input | Parent time range | Candidate time range | Parent / candidate allocations |
| --- | --- | --- | --- |
| 1 action | 69.829-75.308 us | 68.683-88.124 us | 1,208 / 1,208 |
| 8 actions | 286.951-305.733 us | 290.064-330.216 us | 3,666 / 3,666 |
| 64 actions | 2.143-2.816 ms | 2.121-2.194 ms | 19,720-19,721 / 19,720-19,721 |

Ordinary allocation bytes per operation were 98,479-98,483 versus 98,500-98,502;
328,192-328,194 versus 328,210-328,219; and 2,438,511-2,438,630 versus
2,438,568-2,438,659 respectively. The new program catalogue and lowerer pointer
extend the structures used on that path without adding a record-map allocation
when no record is present.

| New record input | Time range | Bytes per operation | Allocations per operation |
| --- | --- | --- | --- |
| 1 field | 55.894-76.251 us | 83,306-83,309 | 950 |
| 8 fields | 171.602-190.381 us | 265,998-266,006 | 1,924 |
| 64 fields | 915.797-1,023.808 us | 1,292,587-1,292,641 | 7,871-7,872 |

The initial 64-field profile run used 1,369,653 bytes and 7,938 allocations per
operation. Removing the temporary field-type lists eliminated avoidable work;
the final figures above are from the refined implementation. A short allocation
profile is useful for locating these sites, not assigning precise percentages.

Comparable compiler builds used `-trimpath -buildvcs=false`. File size changed
from 29,057,698 to 29,075,154 bytes (+17,456); Mach-O `__text` changed from
10,165,236 to 10,177,348 bytes (+12,112). This adds the concrete record importer,
builder, nominal verifier and their provenance/layout integrations. For example,
the importer and recursive copying closure occupy 192 and 1,792 instruction bytes,
the record constructor builder 2,704 and the nominal record verifier 1,424.
Some shared helpers replace existing tuple/type helpers rather than being wholly
new. The remaining file delta also includes generated type/map support, metadata
and alignment. This is measured feature growth, with the identified redundant
copying removed, not an unexplained baseline increase.
Repeating both builds after the parent review fix `65ea8df7a` produced the same
file and instruction sizes listed above.

Reproduce against the parent and candidate worktrees:

```sh
go test ./internal/semir -run '^$' \
  -bench 'BenchmarkRecordValuesBuild|BenchmarkTypedCleanupActions' \
  -benchtime=100ms -count=5
go build -trimpath -buildvcs=false -o /tmp/fern-record-values ./cmd/fern
/usr/bin/size -m /tmp/fern-record-values
go tool nm -size /tmp/fern-record-values
```

Raw local validation, benchmark and profile artifacts use
`/private/tmp/lang-record-values-*`.
