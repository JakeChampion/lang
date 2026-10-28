# Standalone decoder for wide large-free instructions

Issue #13738 adds a separate decoder before the wide machine proof.
The current emitted helper starts with a full 64-bit literal and a register comparison.
The old decoder cannot represent that literal.
Its old interfaces and bounded models remain unchanged.

The new `WideOp` contains the existing operation record and a separate signed `i64` immediate.
The new `WideProgram` contains the decoded operations and an error.
A small adapter exposes the existing decoder for unchanged instructions.
Production emitters, runtime instructions, and standard library files remain unchanged.

The initial subset admits canonical signed decimal `movabs` literals across the full signed `i64` range.
It requires a 64-bit general-purpose destination register.
Register comparisons retain the exact source, destination, and width.
This parser subset does not define all valid x86 assembly spellings.

Every selected body line contributes one operation, including labels and returns.
Branches resolve only to unique labels inside the selected helper.
Unknown instructions, malformed operands, invalid literals, duplicate labels, and unresolved transfers return an error with no operations.
Empty and cyclic bodies remain data without a semantic or termination proof.

The independent Python reference uses integer arithmetic and regular expressions.
Its fixed catalog contains 4,681 cases, with 4,224 successful decodes and 457 expected errors.
The catalog covers signed endpoints, power boundaries, all 16 registers, every comparison pair, namespace controls, and the current helper.
Every integer and operation field must match exactly.

The current helper contributes 29 operations without omission.
The first operation retains the exact literal `17179869184`.
The second operation compares the original registers at width 64.
The decoder does not establish the later machine contract.

The worker receives framed UTF-8 source through standard input.
It emits complete ordered records for each frame and rejects malformed packets.
The collector requires exact records, zero exit status, and one nonzero balanced census with zero live bytes.
It preserves checkpoints and the complete failure prefix before rejection.

Private controls truncate the literal, swap comparison operands, or accept an unknown instruction.
Each control must compile and run normally before the collector rejects its incorrect record.
Protocol self-tests first establish a valid complete fixture before each deliberate fault.
No case filter, error waiver, tolerance, or threshold changes the criterion.

The fresh stock retained-machine failure remains evidence for the parent issue.
The independent native capability probe remains a finite observation.
This decoder does not repair the full retained-machine driver or qualify a symbolic machine proof.
Actual CI recovery and all M5 and epic gates remain open.

The [final evidence](../benchmarks/wide-large-free-decoder-2026-10-28/README.md) records the source-pinned campaigns and qualification checks.
All three targets completed the same 4,681 cases with exact operation records.
All 1,758 fresh workers reported nonzero balanced censuses with zero live bytes.
The full retained-machine proof remains a later scope.
