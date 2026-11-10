# HTTP baseline controls

CI run `42754163314` reports inherited HTTP benchmark drift at head `36d1f7b5`.
The failed jobs are [x86-64](https://github.com/JakeChampion/lang/actions/runs/42754163314/job/129627638421) and [ARM64](https://github.com/JakeChampion/lang/actions/runs/42754163314/job/129627638638).
This record banks 24 measured rows and retains every existing tolerance.

## Fixed sources

The baseline compiler source is `da3282d0f3bc2614d45667b17edad8f844eb6a1b`.
The candidate compiler source is `597b6e782f5d2825baf3ed07dcf9eeba3dc57be2`.
Both compilers use the same qualified producer, `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
Both read the benchmark and standard library from the fixed baseline checkout.
Normal compiler builds retain certification and disable profiling and allocation counters.

## Output controls

All nine target and profile pairs produce identical assembly between main and the candidate.
All eight native pairs also produce identical executable bytes.
The [results](result.json) retain every command, binary hash, count, and exit status.
All 34 compilation commands return zero.
Every executed benchmark returns checksum 30.

The x86-64 safe profile emits 16,696 indented lines and executes 22,212,415 guest instructions through either compiler.
The ARM64 safe profile emits 17,566 lines.
The WebAssembly output contains 20,294 indented lines.
The latter count measures WAT body lines, not native instructions.
Local ARM64 and WebAssembly controls cover emission, without execution.

Twenty-two local row values match the exact CI values.
Both local diagnostic image sizes exceed their CI values by 88 bytes.
The archive retains those differences and the absolute source paths.
The baseline updates use the exact CI values, including diagnostic sizes.

## Retained controller failure

The [initial controller](controls/reproduce-http.py) requires equal instruction counts from byte-identical hardened images.
The two counts are 30,149,641 and 30,162,103.
That assertion fails, and the original [result](initial-result.json) and [failure log](controls/reproduce-http.log) remain intact.
No hardened instruction row requires an update.

The [resume controller](controls/resume-http.py) retains the completed controls and collects the remaining static and minimal-profile controls.
The final record includes all 43 commands, including the initial 14 commands.
It preserves both hardened counts without a cause claim.
No product benchmark, comparator, or tolerance changes.

## Validation and reproduction

The existing comparator self-test passes all 22 cases.
These local controls do not replace the complete exact-head CI gate.
The native HTTP cost changes are inherited from main, without an interpreter performance credit.

The scripts retain the exact paths and environment from collection.
Use the recorded producer to build both source revisions before a fresh reproduction.
Use one fixed benchmark and standard library for both compilers.
The commands in [result.json](result.json) specify each target, profile, and output.

The archive compresses assembly, Callgrind data, and logs without changes to their raw bytes.
[Raw identities](raw-identities.json) verify each decompressed file.
[Baseline snapshots](baselines/) retain the old values and the final updates.
Run `python3 verify.py --commit HEAD` to verify the committed data.
