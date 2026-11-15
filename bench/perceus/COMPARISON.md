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
true values in the final tree, as Koka does. The Fern driver also reads the
length of the list of retained trees. Without that read the Fern compiler
removes the list, because nothing reads it, and no insert copies a path.

`lean/` adapts the older Lean inputs to Lean 4.34.1. Changes qualify constructor
names that now conflict with type classes, make recursive arguments explicit,
update imports and entry points, and accept command-line sizes. The ordinary
tree always blackens its root to match Koka. The checkpoint tree inserts keys
from n down to 1, matching Koka rather than the older Lean n-1 through 0, and
omits Lean's extra checkpoint-list-length traversal/output. Existing Lean
balancing helpers retain their upstream inline attributes and Nat keys. All
workload keys are nonnegative; Fern/Koka ordinary-tree keys are int32, and
checkpoint keys use Fern i64 and Koka arbitrary-precision Int.

`roc/` translates the Koka inputs to Roc. The harness builds them only when
`--roc` names a `roc` executable, with `roc build --optimize`. The sources use
the `basic-cli` 0.20.0 platform, and the build downloads it on first use. They
were checked with the Roc nightly of commit `ea210f57` (2025-10-28).

Each program keeps the Koka data types, recursion and rewrite order. The ordinary
tree has `I32` keys, as Fern and Koka do. The checkpoint tree, the derivative
and constant folding use `I64`. Constant folding and the derivative use the
wrapping `add_wrap` and `mul_wrap`, as Fern does, because the checked Roc
operators stop the program on an overflow.

`binarytrees` is not in the pinned source. It is the binary-trees workload of
the Computer Language Benchmarks Game, in the form where each node holds an
item. The four files translate one design. The two children of a node
take different items, so no compiler can make the two subtrees one tree. The
check of a tree is its item plus the check of its left child, less the check
of its right child. The full size is depth 21.

`msort`, `qsort`, `treemap` and `pmap` are not in the pinned source either.
Their inputs come from the generator x' = (1103515245 x + 12345) mod 2^31,
from the seed 42:

- `msort` and `qsort` sort a list of a million values, each reduced mod
  1,000,000. The merge sort splits the list into two lists in turn, and the
  quick sort takes the head as the pivot and appends. Each prints the count and
  the sum of each element times its position.

- `treemap` builds a tree of depth 20 with items, as `binarytrees` does, and
  adds one to each item 20 times. A tree with one count can be rebuilt in place.

- `pmap` inserts a million keys into a red-black map. A key is two words, the
  value mod 1024 and the value divided by 1024, and the compare is in that
  order. Then it looks up the same keys, and a million keys from the seed 7.

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
language precedes repeated timing. The order of the languages in a repeat is
a row of a balanced Latin square. Thus each language runs directly after each
other language equally often. Use a multiple of the number of rows as the count
of repeats: four rows for four languages, six for three.
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
