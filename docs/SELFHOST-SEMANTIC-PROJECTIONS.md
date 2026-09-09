# Typed projection dependencies in the self-hosted compiler

Part of #8920. `ssasem.fern` verifies semantic values before reference counting,
then derives the dependencies consumed by `ssadeps` and `ssalive`. Its `Func`
wrapper retains the existing SSA graph plus shared `typeinfo.Type` values and
the function signature. No AST reconstruction or physical RC recognition is
used.

`semtypes.fern` compares exact recursive type identity: integer width,
signedness and character identity; owned strings versus views; float width
and polymorphism; nominal generic arguments; tuple fields; map key/value
types; and known callable parameters and results. Checker assignability and
declaration spelling cannot substitute for this comparison. Unknown types,
polymorphic floats, opaque callable signatures and malformed structural types
are rejected at this boundary. Resolved record instances are supplied through
`semrecords.Record`: exact nominal type identity plus ordered, uniquely named
fields. The verifier rejects duplicate instances, inconsistent field names or
generic arities between instances, unresolved fields and missing nested record
schemas. Recursive nominal field references are checked as a finite table.
Opaque nominal handles may still be copied, but cannot be constructed or
projected without their schema, or receive a counted-unit plan without one.

The initial operation vocabulary is deliberately closed: parameters, exact
copies and phis, i32/boolean constants, array and tuple construction, and array
and tuple projection, and schema-checked record construction and projection.
Aggregate operations occupy a negative tag range within
this semantic wrapper; physical SSA load/store/alloc and calls are rejected.
The wrapper must never be passed to a physical optimizer or emitter by
extracting its graph. Parameter/result types, operand arities, element types,
tuple indices, record field indices and names, i32 array indices, return types
and boolean conditions must
agree exactly. Array bounds remain a runtime obligation, not a static claim.

Dependencies are derived, not accepted as ownership assertions. A managed
projection borrows its actual container; a copy preserves its source anchor.
Chains keep nested parents live by value identity. Scalar projections have
no continuing anchor after reading their value. Construction and phi results
have no parent anchor because the later unit planner must establish their
independent counted supplies. This pass does not certify those supplies,
uniqueness, physical layout, or reference counts. The dependency table is
built by appending complete rows in value-id order.

Every rejected analysis returns no dependency table or live sets. Validation
uses the same graph on the bootstrap and self-hosted compiler paths, with a
corpus of valid nested projections and phis plus malformed signatures, types,
projections, returns, conditions and physical operations. The nested case
checks every derived dependency, including construction/scalar independence.
Separate type checks distinguish types with identical declaration spellings
and nested generic/callable structures.

Validation on parent `213276e6b`: `scripts/devbox go test
./internal/e2eselfhost -run '^TestSelfHostSSASemantic' -count=1 -v` passed
in 43.424 seconds without skips. The 23 cases run individually under bootstrap
and as one corpus per self-host target. `scripts/devbox make lint-all` passed.
The Linux ARM64 devbox image was
`sha256:137361dac44a546d331db93775cce7dfa899b2fb75eae57c00df16a1fb2e6187`;
x86-64 execution used QEMU and Wasm used Wasmtime. These are correctness tests,
not runtime performance measurements. Full current-head CI/review are pending.

## Remaining cutover

Production lowering does not yet consume this wrapper. Record schemas now
extend the existing counted-unit planner and independent verifier. Variant
schemas, guard proofs, call and cleanup effects, and physical record lowering
remain required. Then the frontend must import
typed values before RC lowering, production consumers must switch, and the
replaced AST analyses must be deleted. This prerequisite is not a permanent
optional compiler route. Main's enum-return leaks were repaired separately by
#8990, whose exact heap-balance regressions pass after parent integration.
No size baseline is changed.
