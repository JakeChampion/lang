# Native hardening CLI contract

Fresh main failed the CLI flag-refusal fixture.
The fixture expected exit two for a native executable with `-harden` and an output filename that ended in `.so`.
The command did not request `-shared`.
The current CLI permits native executable hardening, so the successful compile was correct.

The corrected refusal row requests explicit shared mode.
All other original invalid-flag rows retain their required exit codes.
Additional rows cover ARM64 shared-mode hardening and unsupported Wasm hardening.
The positive control uses the same `.so` suffix without shared mode.
It requires an executable ELF type, no writable-executable load segment, a non-executable GNU stack declaration, and actual exit 42.

The focused before run failed with zero skips on clean main.
The focused after run passed three nodes with zero skips on the uncommitted candidate.
The independent capability record confirms the current output mode and all three refusal cases.
The website flag table now matches the current CLI help text.
The [final evidence](../benchmarks/cli-harden-mode-2026-10-27/README.md) records complete native CLI coverage after the source commit.
The first command retained two ARM skips and did not qualify as a complete pass.
Only those unfinished cases ran again, with zero skips and no repeated completed semantic case.

This change preserves every production compiler source, runtime source, standard-library source, baseline, threshold, and refusal policy.
It does not establish general hardening or production security qualification.
Issue #13721 tracks this fixture repair under #13687.
The wider retained-machine proof and actual CI recovery remain open.
All actual M5 and epic acceptance gates remain open.
