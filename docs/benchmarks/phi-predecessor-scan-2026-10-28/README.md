# Phi predecessor search comparison

Both stage-2 binaries use the same stage-1 producer, fixed checker and fixed standard library.
The baseline sources match `8bcb344e3beb2ba81d0037e2059b48f8ff2ad8df`.
The candidate changes only `compiler/ssalive.fern` in that snapshot.
The source archive records the identical compiler tree from merge `40e5dd24adb3ddfd619ef3d6f53cae7b8235059f`.

The baseline executes 24,995,670,175 instructions, and the candidate executes 24,977,303,274 instructions.
The decrease is 0.073480%.
Both binaries call `__fern_arr_box` 17,398,916 times.
Calls to `ssalive.pred_index` decrease from 1,767,384 to 1,014,941.
All 681,696 input-validation calls remain unchanged.
The complete checker output remains byte-identical on all three targets.

The raw Callgrind files and resolved reports retain instruction and allocation attribution.
The symbol tables describe the profiled binaries.
The load-equivalence records compare each release binary with its counterpart with symbols.
The archive manifest records raw byte counts and hashes.

The baseline source archive and candidate patch identify the measured source trees.
The [fixed subject archive](../unit-successor-count-2026-10-28/fixed-subject-source.tar.gz) retains the frozen checker and standard library.
The [subject manifest](../unit-successor-count-2026-10-28/fixed-subject-hashes.json) records those input hashes.
The build records and scripts retain commands, compiler hashes and producer identity.
The [SSA log](../../ssa-log/2026-10-28-phi-predecessor-scan.md) explains the change and its limits.

This result does not establish a native latency improvement.
The primary validation reports 145 successful test nodes, zero failures and zero skips.
It covers liveness, dependency verification and ownership units on x86-64, ARM64 and Wasm.
The source, feature, unchanged complexity and full formatter checks pass.
The validation records contain the actual process exit codes and source hashes.
Full current-head CI, retained-machine integration and the architecture roadmap remain incomplete.
