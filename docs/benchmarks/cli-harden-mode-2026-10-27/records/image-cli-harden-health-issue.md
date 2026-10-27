Parent: #13687.

Fresh main `50e121fbab40c3ffe7f0172cf0ac2f51723e04e4` fails the unchanged CLI flag-refusal test.
The test expects exit two for native `-harden` with an output filename that ends in `.so`.
It omits `-shared`, so the current CLI correctly builds an executable.
The test does not match current native x86-64 hardening support.
The current help text permits native executable hardening and rejects explicit shared mode.
The website table still describes ARM64 support only.

The clean before run reports zero passing nodes, two failing nodes, and zero skips.
Its source hashes remain unchanged.
An independent finite capability probe confirms that the same command produces an executable ELF image and returns 42.
That image has no writable-executable load segment and has a non-executable GNU stack declaration.
Explicit native shared mode and Wasm hardening each return exit two.
This probe establishes the current CLI contract, not general hardening or production security qualification.

Scope:

- Correct the refusal fixture to request explicit shared mode.
- Preserve all other invalid-flag cases and their required exit codes.
- Add a positive native executable case with the same `.so` suffix.
- Require successful compilation, an executable ELF type, existing segment permission checks, and actual exit 42.
- Cover explicit shared-mode hardening refusals on both native targets and the unsupported Wasm target.
- Update the website flag description to match the existing CLI contract.
- Preserve every production compiler source, runtime source, standard-library source, baseline, workload, threshold, and refusal policy.
- Preserve the original failure, finite capability observations, and actual tool identities.

Acceptance:

- [ ] Record the clean main failure and the actual output-mode cause.
- [ ] Pass the corrected refusal cases and positive executable case without skips.
- [ ] Pass the complete native CLI driver group and appropriate source, format, Go build, and vet checks.
- [ ] Preserve all production source and gate values exactly.
- [ ] Merge one READY PR normally at the exact full head.

The other main failures and actual CI recovery remain open under #13687.
The wider retained-machine proof remains open under #13717.
All actual M5 and epic acceptance gates remain open.
