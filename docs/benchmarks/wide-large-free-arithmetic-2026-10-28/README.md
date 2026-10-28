# Exact wide large-free arithmetic

Refs #13725 and PR #13729. Parent: #13717 under #13687.

The qualified source is `5383d709b477ff52737c8915b93a54e4790699d2`.
All sources already present on the base main commit remain byte-identical.
The new APIs are separate proof helpers.
No decoder or machine imports them.

## Contract

The wide range uses signed `i64` endpoints with `0 <= low <= high <= 17179869184`.
The table contains 61 quarter-octave capacities from 524288 through 17179869184.
Its 77 adjacent partitions cover every integer in that range.
Each declared partition has a known class and rounded capacity.

Affine values use factor zero or one.
The offset and both endpoint values must remain within 34359738367 in either direction.
Scalar constants and operands use the same bound.
Invalid inputs and unsupported symbolic facts return exact unknown sentinels.

Low-word and high-word projections require one signed floor quotient across the interval.
Low-word projection preserves the affine factor.
High-word projection returns a constant unsigned word.
A join requires an unsigned low word and a constant unsigned high word, then checks the signed affine bound.

The whole interval from 10737418241 through 12884901888 crosses a word boundary.
Its required projection result is unknown.
That original case remains, with six additional known projection and join checks on exact subintervals.
No helper domain or reference criterion changed.

## Qualification

The independent generator uses unbounded Python integers and a fixed seed.
It derives capacities through an increment-and-boundary table, separate from the kernel quarter-index formula.
Word reconstruction uses unbounded bitwise concatenation and signed interpretation.
Comparison facts check exact predicates at boundaries and relevant interior points.

| Check | Actual result |
| --- | --- |
| Reference | 4474 cases, 77 partitions, 61 capacities, exact reproduction |
| Stock compiler harness | Native, ARM64, Wasm, four passing nodes, zero failures or skips |
| Direct production CLI | 4474 cases per target, three successful workers |
| Direct native allocations | 45291 balanced events, zero live bytes |
| Direct ARM64 allocations | 45291 balanced events, zero live bytes |
| Direct Wasm allocations | 45834 balanced events, zero live bytes |
| Go oracle | Fresh source-pinned build, check and interpreter exit zero |
| Standard compiler build | Make, version, and smoke checks passed in 89.918 seconds |
| Source audit | 219 passing nodes, zero skips |
| Complexity | Unchanged gate, three passing nodes, zero skips |
| Broader source checks | Compiler, standalone stdlib modules, self-host drivers, and stock fixture checks passed |
| Auxiliary checks | 2088 Fern sources, Go build and vet, workflow lint, CI self-tests, runtime bundle, gofmt |
| Runtime checks | Five target sets plus stock feature and fixture checks |

Both unchanged legacy arithmetic drivers execute before the new checks.
The driver checks every reference row in order and requires the final count of 4474.
Every expected integer value and unknown sentinel must match exactly.
The raw standard compiler matches the qualified prerequisite compiler bytes.

The fresh Go oracle comes from this exact committed source.
Its before-execution and after-execution hashes match.
The direct worker records bind each actual binary before and after execution.
The stock harness uses its normal source-based cache, and its private compiler hash was not separately captured.

## Controlled failures and corrections

Three private helper copies introduce signed truncation, a discarded high word, and a truncated wide mask result.
All three compile normally and exit 180 at the exact required failure reason.
A fourth private control changes only the last reference row expectation.
It exits 180 at case 4473 and reports the original correct helper result.

The private controls preserve all original assertions.
Their intentional early exits do not qualify persistent leaks or successful cleanup.
The committed source and reference expectations remain unchanged.
No numerical helper failure occurred.

The first reference builder incorrectly assumed uniform word quotients across every class partition.
It stopped before any output file write.
The correction retained the whole crossing interval and added the six exact subinterval controls.
The original builder and correction record remain intact.

The initial formatter required canonical parentheses for the minimum signed integer expression.
The generator now emits that canonical form, and the reference JSON stayed byte-identical.
The initial shell setup errors preceded their intended test starts.
Those corrections do not represent compiler defects or numerical failures.

The expanded source exposed the ARM64 emitter defects before the parent resumed.
PR #13728 merged normally before these final runs.
All seven arithmetic files remained byte-identical throughout the prerequisite.
Its separate evidence retains the original ARM failure and all rejected compiler candidates.

## Provenance and limits

The original retained-machine failure, emitter diagnostic, and native assembly capability use their recorded historical sources.
The native capability is a finite observation and does not qualify a new Fern machine model.
Its deliberate 32-bit shift mutant returned normally but failed 36 exact output checks.
Additional inventory hashes from that probe are explicitly post-execution.

`evidence.json` binds each raw artifact to its original bytes.
Gzip files decompress to the exact original source, log, or data.
Existing records retain their original source and producer identities.
The additional local binary inventory is post-execution and does not invent earlier hash checks.

The new proof modules do not change production compiler behavior or standard-library sources.
This scope did not repeat or relabel the prerequisite image campaign.
Its image qualification remains bound to source `3ba81dccd2de600e45fa9539247e28352e07118b`.
No whole-Go-ARM, CPU, RSS, physical-memory, compatibility, or production claim follows from these checks.

The decoder, machine, oversized-path policy, and complete retained-driver recovery require later work after this normal merge.
All actual M5 and epic acceptance gates remain open.

`tools-post-run.json` records additional tool versions and hashes after all qualification commands completed.
It does not invent before-execution inventory checks.
