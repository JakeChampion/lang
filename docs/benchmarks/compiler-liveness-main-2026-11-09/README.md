# Liveness map reuse after main updates

Status: [record] fixed-input comparison from 2026-11-09.

This comparison measures liveness map reuse after the branch merges newer compiler changes from main.
The certifier supplies its block positions and definition sites for the same unchanged graph.
Dependency and graph checks remain active.
The merged code uses the shared `ssa.position_of` helper.

## Results

| Measurement | Baseline | Candidate | Reduction |
| --- | ---: | ---: | ---: |
| Cold allocation events | 52,466,130 | 52,449,478 | 0.0317% |
| Cold guest instructions | 41,633,295,021 | 41,508,220,845 | 0.3004% |
| Warm guest instructions | 29,470,445,406 | 29,458,590,386 | 0.0402% |

Both allocation runs free every allocation and report zero live bytes.
The assembly hashes match in the allocation comparison.
The native executable hashes match in all instruction runs and cache probes.

These results measure work on one fixed checker snapshot.
Each compiler has one completed instruction run per cache mode.
They do not establish a wall-time speedup or an external rank.
This comparison does not measure peak memory.

## Sources and controls

The baseline source is `d04e79179fda465d5897d25a00f9a2f34c4694ab`.
The candidate code source is `2d7d444e94032710fad63ccb6d290d02475c72d7`.
Both compile the checker and its imports from the baseline checkout.
The [raw measurements](measurements.json) record every command and exit status.
The [manifest](manifest.json) records the producer, compiler hashes, and data hashes.
The [source patch](reuse.patch) contains the compiler difference.

The same qualified stage0 producer builds all four measurement binaries.
Its source is `38f241ca26366e5ef39a898c780bbfbc3a4694f5`.
The builds omit `-g` and disable CPU and allocation profiles.
The two allocation binaries use `FERN_LEAKCHECK=1` during their build.
Ownership certification remains enabled in every run.

Allocation runs emit assembly.
Instruction runs emit native executables.

Cold runs disable only the proof cache.
Warm runs use separate primed caches with equal-length paths.
Each compiler primes its own entries because the cache keys include compiler bytes.
The cache probes report 2,391 baseline hits and 2,391 candidate hits, with zero misses.
The measured runs use normal verifier defaults without the extra report flag.

Cold instruction runs execute baseline then candidate.
Warm instruction runs execute candidate then baseline.
Both compilers use the same output path within each comparison.
Callgrind counts the compiler process with child tracing disabled.
It disables cache and branch simulation.

The [earlier comparison](../compiler-liveness-2026-11-09/README.md) uses older compiler and input snapshots.
Its measurements remain intact.
Do not add reductions from these different snapshots.

## Validation

The merged liveness and certifier suites pass in 137.401 seconds with an explicit Go exit status of zero.
The six liveness fixtures pass on x86-64, ARM64 under QEMU, and WebAssembly.
Filtered live sets agree through the legacy and reused-map entry points.
Fourteen invalid-metadata cases retain their expected diagnostics.
The existing certifier cases pass on x86-64.
No case skips.

The compiler source check, Go vet, and CI self-tests pass.
Native tests use the documented coredump-filter overlay.
It removes the unavailable filter write and retains `ulimit -c 0`.
The fixture and source format checks pass.
Verbatim logs and unified patch context retain their original whitespace.

## Reproduction

Create clean checkouts of the recorded baseline and candidate revisions.
Use the producer identified by its source and hash in the manifest.
Set the paths at the start of [measure.py](measure.py) for those checkouts and a new output directory.
Run the script with Python 3.

The script builds each compiler, checks allocation balance, primes caches, verifies cache hits, and runs the instruction comparisons.
It checks output hashes and records each command before execution.
It refuses to overwrite an existing measurement record.
