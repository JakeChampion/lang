# Declared enum qualifiers and constructor names

Issue: #13642. This prerequisite blocks the Exif metadata reader in #13638.

## Failure

The reduced source declares `ByteOrder { Little, Big }`.
An unrelated enum also declares a variant named `ByteOrder`.
The source then selects `ByteOrder.Little` or `ByteOrder.Big`.
Both checkers accept the source, and the Go interpreter returns zero.
The self-host compiler refuses the expression with `unsupported projection`.

The clean before compiler uses source
`c03b8473e4a1458210d978ca2f4f581c7cba090f`.
Its CLI SHA-256 is
`13157d85d76cb5ab1790dcc06ad9575db60cd0cc4794ec6e3209e6a1ca9d50e2`.
The reduced source fails on x86-64, ARM64, and Wasm.
Removal of only the unrelated variant name permits compilation.

## Cause and change

The source classifier checked variant ownership before the declared type.
Constructor metadata also took precedence over a declared enum in the type classifier.
The classifier now resolves a declared enum before constructor metadata.
Local, parameter, and captured values retain precedence over type names.
Constructor values without a declared type retain their method behavior.

The regression also exposes two checker defects.
The ambiguity walk treated a declared enum qualifier as an unqualified value.
The walk now stops at a known type qualifier, but still examines call arguments.
An ambiguous value retains its ambiguity diagnostic without a payload diagnostic from an arbitrary constructor owner.

Qualified nullary constructors lack payload metadata, so their argument check returned early.
Qualified calls now use the enum payload count when payload metadata is absent.
Unqualified calls retain their non-function diagnostic without an extra constructor diagnostic.
The Go checker remains unchanged.

## Regression controls

Eight positive cases cover nullary constructors, generic payloads, multiple variant owners, and owned record payloads.
They also cover local, parameter, and captured shadows, plus methods on constructor values.
Each case runs through the Go interpreter and all three compiled targets.
The compiled probes require nonzero, balanced heap counts and zero live blocks.

Twelve negative cases retain member, argument, payload, shadow, and ambiguity diagnostics.
The Go oracle supplies the exact diagnostic code sequence for each case.
Qualified nullary calls with extra arguments require E036.
Unqualified calls to nullary values require E038.
Ambiguous values remain errors, also as arguments to qualified constructors.

The focused suite requires exact diagnostic code sequences on all three compiled targets.
All 82 test nodes pass without failures or skips.
The Exif application source remains frozen during this prerequisite.
No codec syntax change or name-specific exception substitutes for the compiler fix.

## Qualification

The clean compiler uses source `7c2e5cd50a17cc50c63b4f8f1941429e3046d865`.
Its CLI SHA-256 is
`13d459d05529bd7b31f267bc16b720766dde01a87b69cf55d1222acd24fa81fe`.
The standard build replay and stage1 smoke pass.
The replay produces the same bytes as the preserved first output.
The first build session reported exit1, which remains in the record.

The full Go checker passes 2,000 test nodes.
The direct checker and diagnostic suites pass 4,756 nodes, with native ARM64 execution under QEMU10.
Both suites report no failures or skips.
The 15 selected checker and enum roots report another 1,857 passes and zero skips.
Both unchanged complexity gates pass.

The first combined CLI sweep reaches its 20-minute package limit during examples.
That invocation remains a failure.
Five roots complete before the timeout.
A second run passes every unfinished root with the same inputs, assertions, and 20-minute limit.
It does not repeat the five completed roots.

The full published image scripts pass all 214 TAP cases on native and Wasm.
Their 69 native and 45 Wasm heap censuses balance with zero live blocks.
All 1,280 hashed Fern sources and the compiler binary remain unchanged during each script.
These results do not claim full ARM repository coverage.

The default allocation workload reports 35,029,113 allocations and frees against the unchanged baseline of 34,951,426.
The 0.22% drift meets the 1% tolerance and the 10% ceiling.
These counters do not measure CPU time or physical memory.
Fern format checks cover 2,032 sources.
Build, vet, workflow lint, runtime bundle, and test selection checks pass.

The [qualification record](../benchmarks/qualified-type-heads-2026-10-26/qualification.json)
contains compiler hashes, test totals, and the completed root inventory.
The [allocation report](../benchmarks/qualified-type-heads-2026-10-26/alloc-report.txt)
preserves the measured counters.
