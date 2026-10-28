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

## Final qualification

The final campaigns use source `a928e68f14ede0df5afe9b046bb866eb4455b82e`.
The standard build passed make, version, and smoke checks in 107.742 seconds.
Its raw compiler matches the prior qualified production compiler byte for byte.
The published CLI records its actual source version.

All three targets completed 4,681 cases and 586 fresh workers per target.
All 1,758 workers exited successfully with nonzero balanced censuses and zero live bytes.
All decoded records match exactly across targets.
Every campaign preserved its source and compiler hashes.

The final stock decoder and packet tests passed 29 nodes across the three targets.
The unchanged complexity checks passed three nodes.
The source gate passed its five runtime sets, fixture checks, compiler check, standard library checks, and standalone driver checks.
The nonverbose source-gate command does not establish a separate test-node count.

Format, Go build, Go vet, workflow lint, test names, CI self-tests, runtime generation, and all 219 source-audit nodes passed.
The reference bundle reproduced exactly, and all 12 protocol self-tests passed.
A fresh Go oracle built from the final source passed check and interpreter commands.
The Go record contains its post-run binary hash.

The archives preserve the fresh stock failure, native capability, initial runs, provisional corpus, fault controls, corrections, and final records.
Executables remain at their original paths, with hashes in `omitted-executable-identities.json`.
The three final raw records use new names and preserve their exact decompressed bytes.
`evidence.json` binds the archive hashes and qualification facts.

The first private-control compile failed before execution because the command omitted the separate cache exports.
The corrected command used a distinct output path and the configured cache exports.
All three deliberate decoder defects then compiled and ran normally before exact collector rejection.
The failure-prefix control preserved eight completed records, its checkpoint, and the failing worker output.

The current raw compiler is unchanged, so this scope does not repeat the separate image corpus.
PR #13734 preserves that prior current-compiler image qualification.
This scope establishes the standalone decoder only.
The full retained-machine recovery, actual CI recovery, and all actual M5 and epic gates remain open.
