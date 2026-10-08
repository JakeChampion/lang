# Standard Perceus comparison inputs

The Fern kernels port Koka commit
`9c55695dd2f7d4db8d93011693d37295e2b76c53`, preserving its ADTs, recursive
algorithms, rewrite ordering and checkpoint frequency. `upstream/` contains
the verbatim Koka and available Lean inputs from that commit. The adjacent
Apache license covers these sources and adaptations.

`koka/` only adds command-line scale selection. `fern/` supplies equivalent
drivers around the tested kernels. Each program prints the same answers,
including both constant-fold values and every derivative's leaf count.
Checkpoint construction retains every fifth tree; its final result counts
true values in the final tree, as Koka does.

`lean/` adapts the older Lean inputs to Lean 4.34.1. Changes qualify constructor
names that now conflict with type classes, make recursive arguments explicit,
update imports and entry points, and accept command-line sizes. The ordinary
tree always blackens its root to match Koka. The checkpoint tree inserts keys
from n down to 1, matching Koka rather than the older Lean n-1 through 0, and
omits Lean's extra checkpoint-list-length traversal/output. Existing Lean
balancing helpers retain their upstream inline attributes and Nat keys. All
workload keys are nonnegative; Fern/Koka ordinary-tree keys are int32, and
checkpoint keys use Fern i64 and Koka arbitrary-precision Int.

The pinned source has no Lean n-queens or constant-fold implementation.
Those two files directly translate the Koka algorithms. N-queens materializes
the same breadth-wise lists of partial solutions, without a bitset solver.
The harness uses a bitset solver only as an independent answer oracle.
Constant folding retains reassociation, evaluation of the original tree,
rewriting and evaluation of the rewritten tree. Lean derivative retains its
upstream UInt32 leaf count and integer-power helper; the exercised workload
has no constant-power calls and its final count fits UInt32. These are workload
comparisons, not claims of identical general numeric APIs.

`bench/perceus_*.fern` contains the small no-I/O performance entry points. Their
exact checksums are checked on all three targets. Native ARM Callgrind
calibration placed them between 118 and 148 million instructions; half-round
controls approximately halve every count. The manual `Perceus calibration`
workflow measures both native ISAs and primary emitted sizes. Native
instruction/size baselines and self-host emitted-size baselines now cover
these root performance entries,
using measurements from calibration run 37716073565. Existing tolerances are
unchanged.

`scripts/bench-perceus.py` records compiler hashes/versions, source snapshots,
compile commands, executable hashes, raw output and per-run measurements.
Koka uses `-O2`; Lean emits C and links with `leanc -O3`; Fern uses its primary
compiler's normal native output. A verified warm-up process for each case and
language precedes repeated timing. Execution order rotates between repeats.
Wall time, CPU time and peak RSS cover startup, output and teardown. Fern
allocations come from a separate binary compiled with `FERN_LEAKCHECK=1`, with
balanced frees and zero live bytes required. Instrumented runs are excluded
from timing comparisons. No allocator or ownership annotations tune the Fern
kernels. Compiler build time is excluded.

The workload deadline also applies to warm-ups and allocation-census runs.
On timeout, the harness saves a failure record before sending SIGKILL and
allows two seconds for cleanup. If the kernel cannot reap the child, the
record retains its PID and a null exit status; the pipeline fails without
publishing successful completion or invented timing/RSS measurements.

`--unlimited-stack` is an explicit Linux-only policy applied equally to every
language's warm-up, timed run and Fern census. Metadata records inherited and
effective stack limits. Runs using this policy are separate from default-stack
Darwin observations, including the incomplete depth-18 Fern fold whose child
remained in a kernel exit wait. The workload timeout is unchanged. Linux VM
measurements must identify their host and resource constraints and pass fresh
small pilots before scaling.

Native pilots must pass before increasing sizes. The full workloads are
4,200,000 inserts for both trees, 13 queens, 10 derivatives and depth 20 constant
folding. `--reuse-builds` requires identical host, compiler and kernel/stdlib
hashes and checks every reused executable hash. Historical runs retain their
original source and compiler identities. Performance conclusions and causes
of losing rows require repeated full-size measurements; pilot results alone
do not establish them.
