# Bare-frame trap-stub evidence

The source commit is `6c462549052932a976a7d4d146422add772a6f4c`.
The clean base is `8d336a2e66912b20a0ed1d6174b6402d0ceb9e41`.
The manifest binds each copied raw artifact to its original path and exact hash.

Clean main reported one failed bare-frame test and zero skips.
Its old assertion expected only `__fern_str_oob_abort.l1:`.
The current array-box size guard also requires `__fern_alloc_size_abort.c:`.
The old stub assertion was the only reported failure.

The fixture now requires exactly both named stubs in their emitted order.
It preserves every frameless-function, link, exit-134, and backtrace assertion.
The focused candidate passed with zero skips before the source commit.
The committed complete abort-test prefix passed 71 nodes with zero failures and zero skips.

Three diagnostic overlays change only the inspected assembly view.
They preserve the linked assembly file and runtime code.
Missing, renamed, and duplicate stubs each produced Go exit one and only the exact expected stub assertion.
The coordinator returned zero after it verified all three required failures and unchanged source hashes.
These are controlled failures, not three ordinary passing Go tests.

The supporting checks passed all 2,084 format inputs, Go build and vet, workflow checks, and SourceLint.
SourceLint reported 219 passing nodes and zero skips.
All guarded sources and the recorded formatter compiler remained unchanged.
The stock Go harness compiled the runtime fixtures through its normal source cache.

The formatter came from clean build `3a70b650d36331d2febe0f88d207bf0702e09ef4`.
Its compiler and standard-library sources match this candidate exactly.
This Go fixture and documentation change did not require a compiler rebuild.
The record does not claim a separate hash for every private harness executable.

Every production emitter, runtime source, standard-library source, helper guard, workload, baseline, and threshold remains unchanged.
Actual CI recovery and the wider retained-machine proof remain open under #13687 and #13717.
All actual M5 and epic acceptance gates remain open.
