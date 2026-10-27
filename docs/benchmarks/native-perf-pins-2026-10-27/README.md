# Native performance pins after merged safety checks

Native child #13712 qualifies the complete reports from main b42.
Both strict native comparisons fail against the previous pins.
The x86-64 report has 36 findings outside tolerance, and the ARM64 report has 38.
The update records the measured cost of merged safety checks with the original strict comparator.

Each verified official artifact contains all 78 expected rows for 39 unchanged benchmarks.
The report includes one static and one dynamic metric for every benchmark on each architecture.
The artifact digests, run 40577171916, and source b42 match the official metadata.
The local proof retains the ZIP archives, extracted reports, sanitized artifact manifest, and digest checks.
Only the returned file IDs serve the artifact download workflow.

All benchmark inputs exactly match the earlier pin source 45148.
Current main 25dc also preserves the b42 compiler, standard library, measurement scripts, and comparator exactly.
An unmodified local static measurement produces the complete 117-row three-target report.
All 78 native static rows match the official CI reports exactly.
The current clean compiler binary and raw compiler retain their recorded hashes without another compiler build.

The static metric counts indented emitted assembly lines.
It includes indented data and directives and is not a literal machine-instruction count or binary-byte count.
The dynamic metric records Callgrind retired instructions from actual CI execution.
No local Callgrind execution or local dynamic measurement accompanies this proof.
Neither metric establishes elapsed time or resident memory.

Before/current assembly for int_loop and call_overhead identifies a fixed native increase.
Each program adds 48 indented lines on x86-64 and 54 on ARM64.
The emitter adds mandatory signal-stack result checks, cold failure handlers, and the backtrace-top guard.
The merged compiler also checks allocation sizes, raw frees, and wrapped indices.
All production checks remain active and unchanged by the pin update.

The dynamic outliers are allocation-heavy Perceus benchmarks.
The x86-64 constant-fold benchmark rises approximately 1.05 percent in retired instructions.
ARM64 constant-fold, derivative, and n-queens rise approximately 1.86, 1.37, and 1.62 percent respectively.
The proof retains every other increase and decrease in the complete reports.
These costs accompany the merged compiler rather than a benchmark-input or comparator change.

The three dynamic outlier programs preserve their observed checksums under old and current compilers on native and ARM64/QEMU10.
All twelve executions retain the benchmark semantic checks without an error sentinel.
This finite observation does not establish general compiler compatibility or performance parity.
The raw observations retain the actual binary and compiler hashes.

The update changes all 156 measured native values and preserves every metric key.
The strict flag, default one-percent tolerance, and explicit map tolerances remain unchanged.
Both complete reports fail against the old pins and pass against the new pins under the unchanged comparator.
A controlled three-percent increase still fails the strict comparator.
A controlled map increase within its original two-percent tolerance still passes.

Executable completeness checks accept both complete reports and reject a missing metric with the required error.
The initial pin metadata records a set-difference assertion.
The distinct subprocess records establish the actual completeness-control exits without a report or comparator change.
No missing metric can enter this pin update.

The comparator self-tests pass all 22 cases with an actual recorded zero exit.
The initial shell log also reports 22 successful cases but does not separately retain the Node exit.
The later distinct record preserves that exit and the unchanged script hashes.
The complete source-audit suite passes 219 tests with zero skips.
Bash syntax checks and diff checks pass without a measurement-script change.

The separate allocation baseline remains 34,951,426 with its original advisory and failure policy.
The separate self-host emitted-size baseline and all other baselines remain unchanged.
The update changes no production source, benchmark, workflow, strict setting, tolerance, or security behavior.
Repository instructions require a measured pin refresh when code generation moves the baseline.

The qualification precedes the source commit and retains explicit uncommitted-feature metadata for the pin update.
The static measurement ran before that update on a clean current-main checkout.
The source inventories and exact report values retain their actual identities.
Historical records remain unchanged.

Actual main CI recovery requires its own successful result.
All actual M5 and epic acceptance gates remain open.
Finite counts do not establish latency, throughput, RSS, physical/process bounds, or production readiness.
