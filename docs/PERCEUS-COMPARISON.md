# Standard Perceus workloads: Fern, Koka and Lean

These five-repeat measurements use the same native ARM Linux VM and the
standard workload sizes from [#11907](https://github.com/JakeChampion/lang/issues/11907).
Fern's median wall time is higher than both comparison languages for both
trees and differentiation. Constant folding's median is also higher, but its
range overlaps Lean's. Fern falls between Koka and Lean on n-queens.

Wall time includes process startup, computation, answer printing and teardown.
Each cell is median milliseconds, followed by the observed minimum and maximum.

| Workload | Size | Fern | Koka | Lean |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 4,200,000 | 3207.26 (3192.33-3249.18) | 423.73 (411.39-427.31) | 1414.57 (1401.78-1682.35) |
| rbtree-ck | 4,200,000; retain every fifth tree | 3744.67 (3732.41-3929.69) | 764.28 (754.69-1078.00) | 1608.71 (1598.57-1633.13) |
| deriv | 10 derivatives | 684.11 (681.46-696.21) | 516.25 (508.37-533.10) | 498.30 (492.46-505.14) |
| nqueens | 13 | 699.88 (689.81-720.87) | 501.60 (486.76-506.04) | 764.87 (741.37-791.30) |
| cfold | depth 20 | 146.14 (140.28-150.19) | 92.04 (89.14-94.05) | 139.27 (131.66-173.72) |

Peak resident memory is the median of five whole-process peaks, in MiB.
Fern allocation counts come from a separate instrumented run of the same
workload. Every allocation census had equal frees and zero live bytes at exit.

| Workload | Fern MiB | Koka MiB | Lean MiB | Fern allocations |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 290.02 | 132.02 | 199.91 | 136,026,039 |
| rbtree-ck | 2058.01 | 1150.15 | 1382.00 | 136,026,039 |
| deriv | 938.03 | 458.10 | 552.04 | 38,843,655 |
| nqueens | 186.01 | 98.02 | 131.83 | 9,349,784 |
| cfold | 170.01 | 156.01 | 165.77 | 3,858,390 |

## Method and scope

Measurements were taken on 2026-10-08 in Docker Desktop's native AArch64 Linux
VM on Apple Silicon. The guest reports Linux 7.0.12, 12 logical CPUs and
28,587,606,016 bytes of memory. The container has no cgroup CPU or memory cap.
The host and VM can still contend with other work. These are measurements on
this environment, not universal rankings or hardware cache measurements.

The harness explicitly gives every language's warm-up and timed process, and
Fern's separate census process, an unlimited Linux stack. The inherited soft
limit is 8 MiB; the hard limit is already unlimited. No workload timeout was
increased. Each process has a 60-second deadline. A small all-language pilot
passed in 21.64 seconds including compilation, then three size steps passed
before full-size repeats. One verified warm-up per language/workload is excluded
from results. The execution order rotates between repeats. All 75 measured
answers and five Fern censuses passed independent result checks.

Fern uses the primary compiler built from
`ac0f7841773bc10fb2f960be5b2e97b70194a7d4`. The source snapshot includes the
uncommitted comparison drivers; its exact hashes are recorded in
[metadata](benchmarks/perceus-2026-10-08/metadata.json). Koka is 3.2.9; Lean is
4.34.1, commit `5045d0056413266e57c625dcd7c365b10e377c52`. The Linux release
archive SHA-256 values are:

- Koka: `0cb2c033f57afad475d637fe4c016ff5ba2f5ba3cfa364a89d9c410fbfd5dcd6`.
- Lean: `fdb974c2cdb4627e090d5d4007b913e09d13c4868720fb5594e22808b3de9e37`.

Koka uses `-O2` with GCC 12.2.0. Lean emits C and uses `leanc -O3` with its
bundled clang 22.1.4. Fern uses normal native compilation. Build commands and
binary hashes are in [builds.json](benchmarks/perceus-2026-10-08/builds.json).
Compilation and instrumented allocation runs are excluded from timing.
CPU time and page faults are retained in the raw records. Page faults do not
measure hardware cache misses.

The [comparison inputs](../bench/perceus/COMPARISON.md) document provenance,
numeric representations and the Lean adaptations. Kernels retain the Koka
algorithms without storage annotations, hand-built arenas or memoization.
The independent answer oracles do not participate in the timed programs.

An earlier Darwin run with its default stack did not complete Fern's depth-18
fold. Sampling showed repeated right-recursive fold frames; the process then
remained in an uninterruptible kernel exit wait despite SIGKILL. Its harness
was terminated at the user's request, and the child's final exit status is
unknown. That incomplete run is not included in this table. The Linux stack
policy and environment are explicit changes, not evidence that default-stack
Darwin completed successfully.

## Cost investigation

Separate native ARM Callgrind profiles use smaller inputs and symbol-bearing
Fern executables. They attribute retired instructions, not elapsed time or
the percentage of the cross-language timing gap. The complete annotated
profiles are retained in [profiles.json](benchmarks/perceus-2026-10-08/profiles.json).

For 10,000 ordinary tree inserts, insertion accounts for 23.24% of instructions,
one recursion context of the generated drop helper for 22.21%, and left balancing
for 10.92%. Allocation and box construction account for another 7.03% and
5.12%. Reuse is present in the generated code; this is not a claim that Fern
never reuses nodes. Both full-size tree variants nevertheless make 136,026,039
allocation requests in the measured census.

At seven derivatives, leaf counting accounts for 19.58% of instructions,
one recursion context of the generated drop helper for 14.51%, multiplication rewriting for
10.80%, and allocation for 9.76%. At fold depth 14, reassociation accounts for
30.22%, one recursion context of the generated drop helper for 18.73%, and folding for 8.09%.
These identify concrete costs to investigate; they do not establish how much
a future optimization will improve full-size wall time.

Two follow-ups isolate generated work without changing the benchmark kernels:

- [#11943](https://github.com/JakeChampion/lang/issues/11943) records lost node
  reuse across nested constructor patterns. An isolated 1,000-update control
  allocates 1,000 boxes with nested patterns and one with a flat pattern;
  shared snapshots remain correct on all three required targets.
- [#11944](https://github.com/JakeChampion/lang/issues/11944) records repeated
  parent uniqueness checks while taking adjacent expression payloads in deriv
  and cfold, with native instruction-level execution counts. Eliminating a
  shared-path check still requires an alias and effect proof.

These explain specific costs, not the entire cross-language timing gaps.

## Regular performance gates

The five `bench/perceus_*.fern` entries use smaller inputs and repeated rounds.
Their checksums are independently checked on x86-64 Linux, ARM Linux and WASI.
Native [calibration run 37716073565](https://github.com/JakeChampion/lang/actions/runs/37716073565)
measured this exact compiler tree at `226b5d71636b42258e2d4d35688d455d21abc218`:

| Entry | Rounds | x86-64 instructions | ARM instructions |
| --- | ---: | ---: | ---: |
| cfold | 400 | 108,160,067 | 118,173,402 |
| deriv | 2,400 | 106,587,208 | 118,616,504 |
| nqueens | 1,400 | 116,800,533 | 132,085,204 |
| rbtree | 160 | 128,541,020 | 139,751,314 |
| rbtree-ck | 160 | 136,899,157 | 148,206,250 |

The ARM counts exactly match the earlier native calibration; halving the rounds
produced 59,089,802, 59,308,904, 66,043,704, 69,877,314 and 74,107,850 instructions,
respectively. Root-path moves preserve executable bytes. Both ordinary baseline
files now include the measured instruction counts and emitted sizes, with their
existing default tolerance. The regular performance lanes enforce these entries.
[x86 evidence](benchmarks/perceus-2026-10-08/calibration/perceus-calibration-x86_64/perceus-native.txt),
[ARM evidence](benchmarks/perceus-2026-10-08/calibration/perceus-calibration-aarch64/perceus-native.txt),
and the adjacent self-host reports and provenance files retain the exact results.
These small instruction gates do not replace the full-size comparison above.

## Reproduce

Install the pinned Linux ARM toolchains and build the pinned Fern compiler.
On a native Linux host whose hard stack limit is unlimited, run:

```sh
uv run scripts/bench-perceus.py \
  --fern /absolute/path/to/fern \
  --koka /absolute/path/to/koka \
  --lean /absolute/path/to/lean \
  --out /absolute/path/to/new-results \
  --unlimited-stack --environment-label 'describe the native host or VM' \
  --repeats 5 \
  --case nqueens:13 --case rbtree:4200000 --case rbtree-ck:4200000 \
  --case deriv:10 --case cfold:20
```

Run a small pilot first on a new machine, as described in the comparison inputs.
The output directory must not already exist. Preserve the source snapshots,
metadata, compilation logs, executable hashes and per-process raw output.
[Results](benchmarks/perceus-2026-10-08/results.json),
[summary](benchmarks/perceus-2026-10-08/summary.json) and
[raw answer/census output](benchmarks/perceus-2026-10-08/outputs.json) accompany
this report. Local full evidence is `/tmp/fern-perceus-linux-repeated`.
