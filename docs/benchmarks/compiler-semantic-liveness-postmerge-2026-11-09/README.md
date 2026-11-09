# Semantic map reuse after later main changes

Status: [record] cold allocation and output controls from 2026-11-09.

The baseline compiler source is `b9ba0a1a685d84103abd377440f41217dc3d33b6`.
The candidate source is `20a9a0dbed2d27a06ae61abae93c416e3b034bcd`.
The fixed checker input source is `12be8f11864c00d71bc3ae9e0c3bc1ddabea75ef`.
The pinned producer source is `38f241ca26366e5ef39a898c780bbfbc3a4694f5`.
The [manifest](manifest.json) records exact binary and data hashes.

## Allocation and output controls

| Complete cold compilation | Baseline allocations | Candidate allocations |
| --- | ---: | ---: |
| Emit assembly | 52,703,024 | 52,685,747 |
| Emit native executable | 51,660,258 | 51,642,981 |

Each comparison uses 17,277 fewer allocation events.
All four runs free every allocation and report zero live bytes.
The [raw commands and results](measurements.json) retain each invocation.
The [native allocation check](native-allocation-check.json) records the separate native counts.
Assembly and native output hashes match within each comparison.

Both compiler builds enable `FERN_LEAKCHECK=1` and disable CPU and allocation profiles.
The builds omit `-g` and retain ownership certification.
The measured cold runs use `FERN_RC_CERTIFY_MEMO=0` and normal verifier defaults.
Counter-enabled binaries change allocator fast paths.
These controls establish allocation-event reductions and output agreement.
They establish no runtime allocator cost, instruction reduction, or timing gain for these source identities.

The [earlier complete comparison](../compiler-semantic-liveness-main-2026-11-09/README.md) uses its own exact source pair.
Its instruction and timing samples do not measure these later main changes.

## Correctness and environment

The targeted dependency and liveness tests pass in 275.227 seconds with explicit Go exit zero.
The log contains 67 passing test and subtest results and no skips.
The tests cover x86-64, ARM64 under QEMU, and WebAssembly.
Legacy and reused flows, invalid metadata, and verifier-off controls remain active.
The source check, Go vet, and CI self-tests also pass.

The executor changed before the first attempt retained a result.
This comparison uses new checkouts, binaries, and output paths.
No ignored artifact from the previous executor was recovered.
The [toolchain record](toolchain.json) identifies this fresh environment.
The native test wrapper retains its core-dump controls without an overlay.
These local checks do not replace the exact-head full CI verdict.

## Reproduction

Create clean checkouts of the recorded candidate, baseline, and fixed input revisions.
Use the producer identified by source and hash in the manifest.
Set local paths in [measure.py](measure.py) for those checkouts and a new output directory.
Run the script with Python 3.
Run the commands from [validation-result.json](validation-result.json) with the recorded tools.
Retain all raw logs and allocation counts.
