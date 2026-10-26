# Function facts in view summaries

The checker prepares parameter names and fixed string locals once per function.
View-summary rounds and final diagnostics reuse these immutable facts.
The function body and scope determine these facts.
Call summaries do not change them.
Functions without string locals skip the assignment scan.

## Fixed input

`fixed-input.json` compares two instrumented compilers on one immutable checkout.
Both compilers use the same builder and runtime libraries.
The collector alternates their order across three repeats.
It hashes the tools and sources before and after the campaign.

| Compiler source | Allocations | Frees |
| --- | ---: | ---: |
| `29028eb88d3eb4d06443e1b7495a57b2ec30df1e` | 34,840,710 | 34,840,710 |
| `f0dd30477657fe6225478a4e606c6f220a93dcd2` | 34,783,892 | 34,783,892 |

All six fresh processes exit successfully with zero live bytes.
Every repeat produces the same allocation count and assembly hash.
The emitted assembly also matches between compilers.
The candidate saves 56,818 allocation events on this input.

The builder reports `29028eb88d3e`.
Its SHA-256 is `0cfd830e60832caf99c3ffddfba66aee6f7669e9833287b1734f58a358c93a40`.
The source commit identifies each instrumented compiler.
The JSON record contains their binary hashes.

## Default gate

`allocation-gate.json` and the two text reports preserve the default workload results.
The default workload compiles each version of the checker itself.
It therefore measures different subjects.
Use the fixed-input comparison to isolate the compiler effect.

The before count is 34,839,418 allocations and frees.
The after count is 34,784,792 allocations and frees.
The recorded baseline remains 31,624,467.
The unchanged 10% ceiling rejects the before result and accepts the after result.
The existing 1% advisory warning persists.
The after result remains close to the ceiling.

No baseline, threshold, exemption, or lifetime rule changes.
The broader allocation issue #13464 remains open.
This result does not recover all the allocation growth from aggregate view analysis.

## Diagnostics and limits

The native diagnostic suite and Go differential suite pass.
The unchanged compiler and stdlib complexity gates pass.
New cases cover call-summary convergence, local strings, parameter storage, reassignment, and independent function scopes.
`portable.json` records eight diagnostic probes on native, Wasm, and ARM64 under QEMU.
Its compiler reports `f0dd30477657`.
The subsequent probe correction changes only the Go fixture, with identical compiler and stdlib sources.

These observations count allocation events on finite inputs.
They do not measure CPU time, peak live bytes, allocator capacity, RSS, or a process memory bound.
ARM64 probes use QEMU and do not constitute the full Go ARM harness.
Full CI status belongs to the PR and main runs.
