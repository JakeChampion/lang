# Primitive protocol checks

Current source: `15a3881824c771fadd1227c831feaa22aa5beb64`.
Package dependency: `26c0a362e397506c8bc4c65b104ffc7d3b346cb1`.

Native compiler source: `56690b99c644ee5ef39595beb472dc4c4fe7cf18`.
Native test source: `2d845222a5f06a8a21f1e27519cb5995b92b1d96`.

Primitive display needs no import.
Declared methods retain precedence over builtin methods.
Comparison, hash, display, and numeric instances cover the required scalar types.
Both frontends preserve contextual type arguments and associated function calls.
The interpreter retains byte receiver identity in arrays, views, captures, and cells.
Invalid builtin method arguments receive normal diagnostics.

All 2,478 current Go checks pass across five packages without skips.
The fresh Go oracle records the clean source commit.
Eight positive cases cover 144 fresh commands and 24 balanced allocation counts.
Three negative cases add six fresh diagnostic checks.
Package compatibility adds 195 fresh commands and 30 balanced allocation counts.
These checks use the current Go oracle and the certified native image.

The source certificate verifies unchanged native inputs after the Go ownership analysis and corpus worker updates.
The earlier native suite passes all 4,566 checks without skips.
Eight trace runs have 188 events and no unpaired allocations.
The installed compiler adds 26 checks.
Eight Darwin images are built but are not executed locally.

These records retain their stated source commits.
The nested-function count is 87 because two capture-free visitors use module scope.
The failed earlier census run remains in the local records.

The kernel benchmark compares the package compiler with the primitive compiler.
Both runs produce the same output and allocation count.
The primitive image uses 29,667,649 instructions against 29,937,444 before the change.
The record includes both compiler hashes, program hashes, and raw profiles.
This count measures instructions, not elapsed time.

All 11 driver sizes retain their limits.
Compiler complexity is 16,323 against the unchanged limit of 16,324.35.
The full format check covers 1,234 files.
The bootstrap record identifies the official producer and all stages.
Stages 2 and 3 have identical bytes.
The capsule preserves raw records with their hashes and validation scopes.
