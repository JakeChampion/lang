# Warm compiler profile after main updates

Status: [record] diagnostic CPU profile from 2026-11-09.

The compiler source is `6bca1d48f3eaa9e4a1d82f0af05cb076862fda16`.
The fixed checker input is `d04e79179fda465d5897d25a00f9a2f34c4694ab`.
The qualified producer source is `38f241ca26366e5ef39a898c780bbfbc3a4694f5`.
The producer builds the CPU profiler without `-g` and with ownership certification enabled.
The [manifest](profile-manifest.json) records the exact build command, flags, and binary hashes.

The CPU binary primes its own separate proof cache.
A reporting probe confirms 2,391 hits and zero misses.
The measured profile uses normal verifier defaults without the reporting flag.
Cache files and the emitted executable retain their hashes through the probe and profile.
The executable hash also matches the normal compiler comparison on the same fixed input.

The [warm profile](cpu-warm.folded) contains 1,095 samples at 100 Hz, with no dropped samples.
Inclusive samples overlap.
Exclusive samples exclude child calls.
Inlining and tail calls can affect the visible call stacks.

| Function | Inclusive samples | Exclusive samples |
| --- | ---: | ---: |
| `semlower.substitution` | 511 | 0 |
| `ssadeps.analyze_checked` | 14 | 0 |
| `ssalive.compute_checked_kept` | 32 | 2 |
| `ssalive.compute_prepared` | 27 | 16 |
| `ssa.block_positions` | 5 | 5 |
| `ssa.inst_defs` | 14 | 11 |
| `semreach.walk` | 27 | 23 |
| `util.NameIndex.chain` | 32 | 32 |

Semantic substitution accounts for 46.67% of inclusive samples in this run.
This profile identifies candidates for experiments.
It does not establish a speedup or an external rank.
A separate Callgrind comparison used the same shared host during collection.
These samples are diagnostic and are not a controlled timing comparison.

The [metadata audit](metadata-audit.txt) examines three callers that retain checked graph facts.
All three can supply existing block positions and definition sites while their graph remains unchanged.
A possible reuse entry still needs correctness controls and a paired measurement.
No semantic or liveness implementation change follows from this profile alone.

For reproduction, set the local paths in [collect-warm.sh](collect-warm.sh).
Build the CPU compiler with the command and environment in the manifest.
Run the collector with Bash.
The collector primes the cache, checks hits, and checks cache and output hashes.
The binary and cache files remain local artifacts.
Their hashes remain in the manifest.
