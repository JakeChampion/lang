Parent: #13687.

Fresh main `8d336a2e66912b20a0ed1d6174b6402d0ceb9e41` fails `TestSelfHostAbortBareFrameX86_64`.
The clean run reports one failure and zero skips with unchanged source hashes.
The fixture expects only `__fern_str_oob_abort.l1:`.
The current array-box helper also requires the exact `__fern_alloc_size_abort.c:` stub for its mandatory size guard.
The old stub assertion is the only reported failure.
The unchanged frameless-function, link, exit-134, and backtrace checks still execute.

Scope:

- Require exactly the two named mandatory stubs in their emitted order.
- Preserve the existing string-trap name and every frameless-function, link, runtime-exit, and frame assertion.
- Do not accept an arbitrary second stub or an arbitrary larger count.
- Preserve the production emitters, runtime, standard library, helper guard, workload, baselines, and thresholds exactly.
- Preserve the original failure, actual source identities, and all controlled failure evidence.
- Test missing, renamed, and duplicate stubs with diagnostic overlays that change only the inspected assembly view.
- Require each controlled defect to fail the exact stub assertion.
- Run the complete existing abort-test prefix with the installed native, ARM64, and Wasm tools and zero skips.

Acceptance:

- [ ] Record the clean main failure and the actual mandatory-stub cause.
- [ ] Require both exact stubs while all original frame and runtime checks pass.
- [ ] Reject missing, renamed, and duplicate stubs for the exact expected reason.
- [ ] Pass the complete abort-test prefix and appropriate format, Go build, vet, and source-audit checks without skips.
- [ ] Preserve every production source, baseline, threshold, and helper guard exactly.
- [ ] Merge one READY PR normally at the exact full head.

Actual CI recovery and the wider retained-machine proof remain open under #13687 and #13717.
All actual M5 and epic acceptance gates remain open.
