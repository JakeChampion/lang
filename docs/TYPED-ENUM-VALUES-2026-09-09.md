# Typed enum values and active-payload ownership

The opt-in `-backend typed-ssa -target arm64-linux` pipeline now executes enum
construction and guarded payload matches through typed semantic ownership IR.
This is another complete source-to-native migration slice, not production AST
ownership retirement. The default native and self-host ownership routes remain
unchanged. Related work: #8920 and #8278.

## Semantic boundary

The builder consumes the complete checked constructor witness introduced by
#8958. It validates the nominal result, complete arguments, variant identity,
payload arity and substituted types. A constructor spelling alone is not a
contract. Ordinary enum-returning calls still use typed function contracts.

Records and enums import through one transactional nominal graph traversal.
Neither catalogue publishes partially copied peers when a recursive import
fails. Enum interfaces are keyed by complete instantiated type and contain
private copied arguments, variants and fields. Later frontend mutation cannot
change semantic signatures or payload interfaces. Unsupported phantom arguments
are rejected even when no variant stores that argument.

Three semantic operations preserve the relevant identities before RC lowering:

| Operation | Identity | Meaning |
| --- | --- | --- |
| `SumMake` | Nominal type and variant ordinal | Construct one active payload alternative |
| `SumIs` | Exact container SSA value and variant ordinal | Borrowed tag test |
| `SumGet` | Exact container SSA value and nominal field ordinal | Project a field of a proven active alternative |

Payload field ordinals are flattened across variants in declaration order. Two
variants' first fields are distinct semantic projections even if their types
and eventual machine offsets happen to match. A projection is containment, not
identity with its container and not an acquired reference-count unit.

The ordered match builder reuses existing binding, guard and cleanup CFG
machinery. It admits flat positional payload binders, unused binders, whole-value
`@` bindings, wildcards and guards. Nested/named payload patterns, alternatives
and ranges remain explicit unsupported cases. It independently checks unguarded
variant coverage; guarded arms do not count as covering a variant.

The semantic verifier independently proves every payload read from the actual
immutable container value. Acceptable evidence is a matching direct constructor,
a dominating exclusive true edge of a matching tag test, or dominating exclusive
false edges excluding every other variant. Branch targets must differ and the
evidence-bearing successor must have exactly that branch as its predecessor.
This permits a final exhaustive arm without trusting frontend coverage flags or
inventing trap/return blocks. Verification runs across binding promotion and
cleanup expansion, as well as at ownership lowering.

## Finite recursive instantiation

Generic enum recursion is not restricted to identical instantiated types:
`Flip[T,U] -> Flip[U,T]` is finite and valid. Conversely,
`E[T] -> E[T[]]` must be rejected before unfolding interfaces.

The preflight builds a finite declaration-parameter dependency graph. Passing a
parameter directly to another enum parameter creates a non-growing edge;
wrapping it inside a type constructor creates a growing edge. A growing edge
within a strongly connected component implies unbounded instantiation. Direct
permutations remain finite; constant substitutions can break dependency cycles.
Declarations and nested type arguments reachable from the requested type are
included, while unrelated declarations are ignored. Generic records must already
have been made concrete by monomorphization.

There is no arbitrary instantiated-depth limit. Tests include mutual recursion,
nested enum arguments, permutations, constant resets, a separate concrete
substitution oracle and a 128-declaration recursive import.

## Ownership and native layout

Payload construction stores the active fields using the existing counted-unit
plan. Borrowed reads keep their container anchors; escaping children acquire the
units needed to outlive the enum. Counted parameters and results do not assert
unique storage. Shared containers retain the usual runtime uniqueness checks.

Payloadless variants reuse immortal static sentinels and allocate no heap box.
Other variants use the existing eight-byte RC header, a four-byte tag and aligned
active fields. Deep-drop helpers dispatch by tag and release only the active
payload. Recursive helper identities are reserved before generating their bodies,
so enum/record cycles reuse helpers rather than recursively generating code.

Target layouts belong to the ARM64 lowering instance, not the semantic type.
Each variant's layout is computed once and reused by constructors, projections
and drops. The guard verifier indexes only constructors and tag tests, not every
unrelated operation in the function. Mutable type arguments detach at frontend
signature/expression boundaries; internal operations and phis propagate private
semantic types instead of repeating canonicalization.

The optional eager-tree return-provenance analysis keeps variant-distinct field
paths for acyclic enums. Recursive return-flow remains an explicit unsupported
analysis with no fabricated summary. Executable ownership lowering uses its
separate, independently verified counted-result ABI and does not depend on that
optional analysis.

## Validation

Local semantic IR, SSA and source-lint packages pass, along with `make lint-all`
and semantic-IR/SSA race tests. The full strict ARM64 semantic matrix and all typed
CLI tests pass on native Linux ARM64, using Go 1.26.8 in the development image.
New runtime cases run both raw and optimized code with allocation balance checks.
They cover Option/Result, nullary values, mixed scalar/reference layouts, guarded
matches, shared and escaping payloads, generic permutations, enum/record cycles,
loop-carried alternatives and conditional/deferred cleanup. The CLI regression
also exercises monomorphization of a recursive generic enum containing generic
records. The independent interpreter oracle now registers checked enum declarations.

Malformed-IR tests reject wrong containers, wrong same-typed variants, premature
projections, inverted or non-exclusive guard edges, malformed operation metadata,
invalid interfaces and missing or inconsistent constructor witnesses. Additional
tests cover frontend detachment, transactional failure/retry, immortal nullary
storage and the separate recursive return-flow gate.

Full current-head CI is still required before merging, excluding Netlify as
directed by the user. No test or performance baseline is weakened.

## Measurements

Host: Darwin ARM64, Apple M3 Pro, Go 1.26.0. Compiler builds use identical
`-trimpath -buildvcs=false` flags. The parent is #8958's source tree, unchanged
by its merge into `9c21098f1`.

The compiler file changes from 29,075,506 to 29,162,642 bytes (+87,136). Mach-O
`__text` changes from 10,180,084 to 10,206,996 bytes (+26,912). Symbol deltas
attribute 26,768 instruction bytes to semantic IR and its generated type helpers,
and 144 to SSA operation handling/classification. The new code implements the
nominal importer, finite-instantiation proof, enum contract and active-variant
verifiers, source/match construction and native layout/drop consumers. Keeping
canonicalization at import boundaries also preserves Go's inlining of the common
operation builder. File-size growth additionally includes metadata, debug
information and alignment. These are functionality and verification costs, not
a new production optimization or a reason to change any size baseline.

Initial profiling identified repeated per-field layout allocation, an overly broad
guard-definition index and redundant internal type canonicalization. The final
comparison below was repeated after heavy validation stopped, using five 100 ms
samples per case. "Initial" is the unpublished first executable enum implementation,
not the merged parent, which cannot execute this benchmark. Medians:

| Enum payload fields | Initial ns/op | Final ns/op | Initial B/op | Final B/op | Initial allocations | Final allocations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 72,939 | 69,337 | 94,243 | 94,219 | 1,205 | 1,200 |
| 8 | 184,641 | 174,689 | 268,938 | 251,713 | 2,190 | 2,125 |
| 64 | 1,019,067 | 890,025 | 1,345,970 | 1,238,107 | 8,042 | 7,888 |

The same ordinary-workload benchmarks ran against the actual merged parent's
source and this final implementation, also sequentially without heavy tests:

| Workload | Parent ns/op | Final ns/op | Parent B/op | Final B/op | Parent allocations | Final allocations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Loops 1 | 92,501 | 98,616 | 123,893 | 123,908 | 1,479 | 1,479 |
| Loops 8 | 543,033 | 506,271 | 675,811 | 675,831 | 5,603 | 5,604 |
| Loops 64 | 4,217,970 | 4,167,939 | 5,039,201 | 5,039,255 | 33,746 | 33,746 |
| Record fields 1 | 57,792 | 58,477 | 83,308 | 83,323 | 950 | 950 |
| Record fields 8 | 173,610 | 182,295 | 266,000 | 266,017 | 1,924 | 1,924 |
| Record fields 64 | 994,898 | 968,745 | 1,292,626 | 1,292,647 | 7,871 | 7,871 |
| Recursive types 1 | 36,408 | 29,993 | 52,553 | 52,569 | 673 | 673 |
| Recursive types 8 | 105,775 | 96,590 | 155,826 | 155,844 | 1,931 | 1,931 |
| Recursive types 64 | 699,391 | 710,088 | 1,017,225 | 1,017,227 | 11,756 | 11,756 |

Ordinary timings vary in both directions; these short samples do not establish
either a general speedup or zero regression. Allocation counts remain essentially
unchanged for these non-enum controls. No coreutils or production runtime speedup
is claimed. [Raw benchmark samples](measurements/typed-enum-values-2026-09-09.txt)
preserve the full runs, not only their medians.

Reproduction:

```sh
go test ./internal/semir ./internal/ssa ./internal/sourcelint -count=1
go test -race ./internal/semir -count=1
FERN_REQUIRE_ARM64_SSA_DIFF=1 go test ./internal/semir -count=1
FERN_REQUIRE_ARM64_SSA_DIFF=1 go test ./cmd/fern -run '^TestTypedSSA' -count=1
go test ./internal/semir -run '^$' -bench BenchmarkEnumValuesBuild -benchtime=100ms -count=5
make lint-all
go build -trimpath -buildvcs=false -o /tmp/fern-enum-values ./cmd/fern
```

## Remaining retirement work

This removes the enum execution gap in the bounded native typed pilot. It does
not complete nested pattern coverage, maps, closures, full string/runtime and
external/indirect-call contracts, other target ABIs or self-host parity. Complete
those production-consumer contracts, switch ownership consumers to verified IR,
and delete their obsolete AST analyses and fallback routing. The retirement
inventory remains in [the migration plan](TYPED-OWNERSHIP-IR-MIGRATION.md).
