# Bounded allocation guard proofs

The current array-box body contains a mandatory external size guard.
The old decoder refused that target before the constructor proof could inspect the body.
The first-mapping model also lost carry information after a valid sum inside its mapped region.

The decoder now retains the exact external target and branch condition.
The array-box model requires proof that this branch is not taken.
Unknown flags, taken branches, and unrelated external targets still fail.
The first-mapping model derives clear carry only for checked nonnegative sums inside the valid mapped region.
Its existing coordinates and allocator requests remain unchanged.

The initialized fast-path model remains byte-identical to the base.
That path returns before the later mapping guard.
A new combined control requires the first-mapping and combined proofs to reject an incorrect carry guard.
The initialization fixture now checks all three existing wide size loads and their exact destinations.

The focused driver adds the current guard controls to the existing Go target matrix.
Both original drivers remain in that matrix.
All original mutation assertions remain unchanged.
The freelist comparison fixture now establishes unknown flags explicitly before its original comparison.
The earlier size guard otherwise supplies known flags when a mutation removes that comparison.
The complete current body also requires a separate successful proof.

The initial diagnostic runs retained the original failure and all failed candidates.
One new control incorrectly required the initialized path to reject an unreachable mutation.
The correction restored that model and its driver exactly, then tested the combined dependency instead.
Other setup corrections fixed an instruction pattern, a helper argument, and an unsupported fixture instruction.
These failures do not establish compiler defects or persistent leaks.

The selected original driver and focused driver require final qualification on all three targets.
The final run must use the original Go harness with strict IR, sanitizer, and balanced zero-live checks.
The wider retained-machine group still requires separate review of its large-free model.
Issues #13717 and #13687 retain the broader recovery criteria.
All actual image-engine milestone and epic gates remain open.

This change preserves every production emitter, runtime instruction, baseline, threshold, and model domain.
It does not establish a general allocator proof or actual CI recovery.
The final evidence record will contain the clean build and selected target results.
