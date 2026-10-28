# Pair-return CI repair evidence

The repair starts from remote merge `0b506a797f6b1e4afde7d169412f0a009927764a` on PR #13718.
That merge contains the prior optimization work and concurrent changes from main.
The raw CI logs record the original self-host fixture failures and the current-head failures.
The branch preserves all merged commits.

The boxed storage probe fails before the fixture change with status 91.
Its diagnostic says that the caller origin has an unsupported effect.
The explicit whole-record consumer restores the intended boxed ABI.
The corrected probe exits with status zero, passes all controls and frees every block.
The typed storage regression also requires refusal of the split-return ABI.

The Darwin signal probe extracts the actual handler from emitted Darwin assembly.
It changes only relocation syntax to assemble that handler in a Linux AArch64 harness.
The harness supplies synthetic Darwin signal and machine contexts, with `x13` set to zero.
It checks the report arguments, tagged frame values, frame pointer and cleared stack-top marker.
The original guard-page case faults with SIGSEGV, while the near-stack-pointer control succeeds.
The production fix moves the context-pointer load before either branch can recover the frame.

The QEMU regression validates handler instructions and context offsets.
It does not validate native Darwin signal delivery.
The real overflow, million-deep recursion and unrelated-fault cases still run in macOS CI.
Linux overflow controls remain in the targeted local suite.

The small heap-metric test emits separate modules and links their assembly into one executable.
Before the fix, the imported function cannot resolve `__fern_heap_base`.
The runtime already exports the heap pointer and heap end, but the heap-base symbol was local.
The production fix exports that symbol.
The executable control constructs an array through the imported module and checks that heap usage increases.
The complete per-module compiler fixpoint checks the larger integration path separately.

The second retained-machine driver still fails on the current wide guard.
Issue #13717 owns that proof integration, with the arithmetic prerequisite in closed issue #13725.
The private prefix-only diagnostic exposes a further decoder mismatch and remains unadopted.
This repair does not weaken the legacy proof or claim retained-machine recovery.

The archive manifest records raw hashes and byte counts for the preserved CI logs.
The red test logs retain the expected failures before each production fix.
The validation logs distinguish completed checks from any checks still in progress.
The [RC log](../../rc-log/2026-10-28-pair-return-ci-controls.md) explains the fixture changes and remaining CI requirement.

The follow-up logs retain the remaining failures from the superseded `0b506a797` CI run.
The ARM64 runtime has the same heap-base export correction as x86-64.
Both native counter executables pass after that correction.
The IR registry now includes the `prctl_query` operation in all exact census controls.

The branch classifier distinguishes a required literal-pool barrier from a block with only a jump.
The positive and negative classifier controls accompany the unchanged executable DEFLATE round trip.
The playground target correction retains exact output comparison across native, Wasm and HTTP forms.
The raw native assembly comparison identifies the missing target in the build record.
The older manual CLI also lacks the heap export, so that comparison records the additional symbol directive separately.
