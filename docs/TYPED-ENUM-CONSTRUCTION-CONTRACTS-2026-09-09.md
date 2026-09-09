# Checked enum construction contracts

This is a frontend prerequisite for typed enum operations, not executable enum
support in semantic IR or production AST ownership retirement. It preserves
facts that the checked frontend previously discarded before typed lowering.

## Contract

`checker.Info.EnumConstructions` replaces `VariantCallPayloads`. Each actual
constructor expression has one contract containing its nominal result type,
all resolved type arguments, declaration-order variant index and substituted
payload types. Calls, bare payloadless variants and qualified payloadless
variants share this interface. Local references and ordinary enum-returning
calls do not create construction contracts.

The complete result type cannot be reconstructed from payload layout:
`Result[T, E].Ok` does not carry `E`, and phantom parameters need not occur in
any payload. In addition, monomorphization deliberately leaves enums with bare
parameter payloads, including `Option` and `Result`, generic. Enums whose
payloads require cloned generic nominal declarations are rechecked with their
concrete cloned identities. Both paths rebuild this contract from actual
checker resolution.

Context is retained at resolution and refined at existing contextual-settlement
sites. Inferred array joins and checked payload declarations now propagate their
resolved types too. Settlement reuses the owned payload slice instead of
allocating another substituted list. The first map insertion happens only after
its initial contextual refinement; programs without constructors allocate no
constructor map.

Enum settlement no longer resolves a constructor from its spelling. It requires
the checked construction witness, so a function parameter named `Some` returning
`Option[i64]` cannot have its own `i32` arguments widened by that result context.
Nested payload context is applied after actual expression resolution.

Under-inferred constructors retain an incomplete result type, and checked generic
bodies can retain parameters. This change does not invent a default for phantom
parameters or change language diagnostics to disguise under-inference. A future
typed enum importer must independently validate completeness, nominal identity,
variant bounds and substituted payload types before admitting an operation.

## Consumers and boundary

Legacy enum allocation, reuse and tail-recursion-modulo-constructor lowering
consume the new contract's payload types. Handle and borrowed-string erasure
transform both its result arguments and payloads consistently. There is no
second, redundant payload map and no new AST ownership analysis.

Like existing checked signatures and record declarations, this interface belongs
to the mutable checked frontend. The semantic IR importer must copy it before
legacy erasure. The native/self-host ownership retirement inventory is unchanged.

Next: import nominal sum interfaces into semantic IR, introduce verified enum
construction/tag/projection operations, and integrate ordered match CFG lowering.
Payload loads must require independent proof of the active variant of that exact
scrutinee. Active-payload ownership and deep drops must follow those interfaces,
including recursive enum/record type graphs.

## Validation and measurements

Final local validation passes: full checker (2.224 s), monomorph (1.333 s),
legacy IR (67.255 s), semantic IR (1.482 s) and source lint (16.227 s) packages;
`make lint-all`; and targeted race tests in checker, monomorph and legacy IR.
These are test-run durations, not performance comparisons. Full current-head CI
remains a prerequisite for merging.

The regression matrix covers bare/qualified variants, partial and phantom type
arguments, nested enum/array/tuple payloads, inferred joins, match destinations,
function arguments, assignments, fields, lexical shadowing, generic bodies,
under-inference and checked-arithmetic rewrites. Separate tests cover monomorph
rechecking, expression reachability and idempotent surface/handle erasure.

Targeted constructor tests pass in checker, monomorph and IR packages. Native
Linux ARM64 tests execute post-settlement tuple payloads, mixed `Some`/`None`
arrays, enum-valued if/match branches and generic enum-array reclamation. Wasm
executes the generic enum-array reclamation regression too. These tests pass on
the final source; they are not assertions of semantic-IR enum execution.

The same parse-and-check benchmark source ran against parent `bbdfedb88` and this
change on Darwin ARM64, Apple M3 Pro, Go 1.26.0, five 100 ms samples per case.
Each enum fixture size constructs that many pairs of `Option[i64]` and
`Result[string, boolean]`. Median measurements:

| Constructor pairs | Parent allocations | New allocations | Parent B/op | New B/op | Parent ns/op | New ns/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 836 | 832 | 76,112 | 76,429 | 39,658 | 35,953 |
| 16 | 1,801 | 1,737 | 216,024 | 220,671 | 130,231 | 134,610 |
| 128 | 8,707 | 8,195 | 1,552,453 | 1,592,412 | 858,505 | 883,925 |

Retaining full result types and expression identities requires larger map
entries than the old call-to-payload-slice map. Reusing payload storage removes
two allocations per constructor in these fixtures. Non-enum controls at the
same three sizes each remove one allocation by allocating the map lazily.
The short timing samples vary and the larger enum fixtures have higher medians;
this is not evidence of a speedup or a claim of zero cost. The added retained
semantic information is required for typed lowering; duplicate payload maps,
payload-list allocations and immediate insert/lookup cycles are avoided.

Comparable `-trimpath -buildvcs=false` compiler builds occupy 29,075,154 parent
bytes and 29,075,506 new bytes (+352). Mach-O `__text` changes from 10,177,284
to 10,180,084 bytes (+2,800). Symbol sizes attribute the entire instruction delta:
construction recording/refinement helpers +1,792, expression checking +752,
numeric settlement -608, checked-program initialization -48, consistent erasure
+640 and legacy payload consumers +272. File-size change differs from instruction
growth because file sections, debug information and alignment change together.
No performance or binary-size baseline is changed.

Reproduction:

```sh
go test ./internal/checker ./internal/monomorph ./internal/ir ./internal/semir ./internal/sourcelint -count=1
go test -race ./internal/checker ./internal/monomorph ./internal/ir -run TestEnumConstruction -count=1
go test ./internal/checker -run '^$' -bench BenchmarkEnumConstructionFrontend -benchtime=100ms -count=5
make lint-all
go build -trimpath -buildvcs=false -o /tmp/fern-enum-contracts ./cmd/fern
```
