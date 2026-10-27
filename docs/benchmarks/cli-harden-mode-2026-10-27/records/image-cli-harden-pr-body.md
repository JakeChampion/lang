The old refusal fixture passed native `-harden` without `-shared` and incorrectly expected exit two.
The current CLI correctly builds an executable, even with a `.so` filename.
This change requests explicit shared mode for that refusal and adds a positive executable control.
The website flag table now matches the current CLI help text.

The positive control requires executable ELF type, no writable-executable load segment, a non-executable GNU stack declaration, and actual exit 42.
Other invalid-flag cases retain their required exit codes.
Additional cases cover explicit ARM64 shared-mode hardening and unsupported Wasm hardening.
Production compiler sources, runtime sources, standard-library sources, baselines, thresholds, and refusal policies remain unchanged.

Validation:

- Clean main reproduces the original failure with zero skips.
- The focused correction passes three nodes with zero skips.
- The complete native CLI coverage contains 127 unique nodes with no missing cases or failures.
- The first full command had two ARM skips because its launch omitted the installed cross-toolchain paths.
- A separate command passed only those two unfinished cases with zero skips and no repeated completed case.
- All 2,084 format inputs, Go build and vet, SourceLint's 219 nodes, and supporting workflow checks pass.

The final evidence-only commit preserves the original failure, both full-coverage commands, and the dependency correction.
The first full command does not count as a complete pass.
Actual CI recovery, the wider retained-machine proof, and all actual M5 and epic gates remain open.

Refs #13721.
