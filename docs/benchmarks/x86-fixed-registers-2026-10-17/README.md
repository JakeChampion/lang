# x86 SSA fixed-register constraints

The initial comparison uses source
`99285a95357849b6d2db317f71cdc9df012bf228`, which adds `rcx`, `rdx` and
`r11` to the caller pool. The six argument registers remain a separate ABI
constant. Saved-register and floating-register indices move with the pool.
No target CPU requirement changes.

`ssafixed.Table` records a prefix count per constrained register. Allocation
checks the entire inclusive live interval when choosing a preferred home,
sharing an operand or phi home, taking a free register, and evicting an active
value. A 97-case execution model covers clobbers at definitions, uses and
returns, register pressure, and the eviction path. Removing the eviction
constraint in a scratch copy makes its pressure case fail.

The emitter permits `rcx` and `rdx` only across audited integer constants,
copies, direct arithmetic, constant shifts, width conversions, and rotates.
Comparisons reserve all scratch registers because their actual operand reads
can be deferred to a branch or select. Every terminator, entry position, phi,
and unaudited instruction reserves all three registers. Calls, variable
shifts, division, memory operations, floating operations and runtime helper
paths retain that conservative policy.

`r11` is available only in a wholly audited integer function when both the
initial and proposed allocations have no spills. A subtraction still reserves
it for the case where a right-operand home becomes the result home. The
second allocation is attempted only when it can remove a saved register.

`ssa-register-inventory.json` inventories the local emitter call graph:
272 reachable functions, of which 78 mention these scratch registers. It
includes reads, comments, and emitted runtime strings; it does not infer
clobbers from textual mentions. The conservative mask policy covers paths
outside the audited instruction forms.

Execution regressions exercise a nine-value leaf with all three new homes and
no saved registers or frame spills, a twenty-value spilling body, and a
spilled right operand read directly into an operation whose destination is
`rcx`. Text assembly linked by GNU, native output, and sanitized output agree
with an independent wrapping-integer oracle for two argument counts.
The last fixture caught a pre-existing assumption that the fallback scratch
name for a spilled operand was its home: reverting the fix produces 33 instead
of 122. Debug and plain native instructions match on all three native targets.

The baseline compiler SHA-256 is
`f395df09bc0d2fb9fa77c410384411708ac7fe941e7d56b3d6e3aeab6c5eb6b7`.
Candidate stage 2 and stage 3 are byte-identical, SHA-256
`fd7edafabc48b92c2f6895f4c37dcba9051bb5d5bb44e6bdd0892640b7ce0291`.
The first generation differs as expected: it was emitted with the baseline's
older register allocator.

For the software SHA-256 comparison, a private stdlib copy changes only
`if (!__sha256_hw())` to `if (true)`, identically for both compilers. The
production source retains its hardware selection. The 8 MiB input is
`bytes(range(256)) * 32768`, with SHA-256
`7d212b9c884f5c77896de960ae17cc341cda43b14d6a971f34ca29ebd4badf7f`.
Both emitted programs produce that digest. With `-O`, software compression
function frame references fall from 1,136 to 868; its static instruction count
falls from 3,022 to 2,991. Cachegrind 3.24, with cache and branch simulation
disabled, counts 380,848,323 versus 376,653,933 executed instructions (1.10%
fewer). The production hardware path has essentially unchanged instruction
counts. Concurrent targeted tests overlapped these initial native timing
samples, so they are not evidence for an elapsed-time improvement.

Focused extra-register/constraint/complexity tests pass in 21.552 seconds;
broader SSA semantic, units, carry, select, tail-call, phi/spill, division,
shift, float and scalar-record tests pass in 85.227 seconds. The new debug
fixtures and existing pressure cases pass in 1.740 seconds.

The later idle run (`idle-native.json`) processes sixteen copies of the 8 MiB
file per native sample, alternating order for 21 pairs. Its software median
candidate/baseline ratio is 0.8952; hardware is 0.9813. A rerun pinned to logical
CPU 0 (`pinned-native.json`) gives software 0.9148 and hardware 1.0015. These
measurements are on an AMD EPYC 9V74 host. They are distinct from the earlier
timings that overlapped tests. Reproduce the idle comparison with:

```sh
python3 docs/benchmarks/x86-fixed-registers-2026-10-17/reproduce.py \
  /absolute/path/to/baseline /absolute/path/to/candidate . /tmp/x86-results
```

The deliberately small nine-live-value multiplication benchmark is a
counterexample to inferring speed from fewer saved registers. Both programs
print `4339612313273984`, but its 15 idle pairs give ratio 1.1274; the 21-pair
pinned rerun gives 1.1292. GNU-linked controls aligning the function entry,
caller loop, or both to 32 bytes still give 1.10–1.12 (`alignment.json`). The
cause is unresolved; this change does not claim that every leaf runs faster.

Seven alternating whole-compiler timing pairs, after targeted tests ended,
have median ratio 1.0112 (`compiler-cost.json`): approximately 1.1% slower.
Each variant's output is stable across repetitions. Their native instruction
sequences differ because their allocators differ; the candidate output has
the fixed-point hash above. The fixed-register bookkeeping and the differing
code generated for the compiler are both included in this process cost.
The candidate compiler occupies 12,703,872 bytes, 4,400 more than the scalar
baseline. These are measured costs alongside the SHA-256 and register-traffic
improvements, not a whole-compiler speed claim.

The cross-target SSA semantic, units, phi/spill, parameter-interval, float,
constraint, inventory and complexity checks pass in 49.398 seconds. All eleven
strict driver-size checks pass with complete coverage and the unchanged 5%
tolerance (90.896 seconds). The CI lane-coverage check passes too (1.222
seconds). Raw size results are archived in this directory.

The subsequent direct-read audit is source
`663625d2e1aa3f5225a7d3eef18ba7fa22fde6eb`, including the existing #12590 ARM
helper fix. It also permits homes across field loads, array reads, and constant
string reads. Field and constant-array reads reserve `r11`; variable-array reads
also reserve `rcx`; constant-string reads also reserve `rdx`. Variable-string
reads, stores, and the other unaudited paths retain the full reservation.
Functions with memory operations remain ineligible to borrow `r11`.

Ten additional record/array/string cases execute both nine- and twenty-value
pressure in GNU-linked assembly, native output, and sanitized output, with two
argument counts. The record case pins both `rcx` and `rdx` as product homes
across its reads; the larger cases require real spills. The combined focused
suite passes in 30.965 seconds. All eleven strict size checks pass in 77.090
seconds. The matching stage-2/stage-3 image is 12,703,936 bytes, SHA-256
`b69e61b2190fccac49e80b8d1bc596994c608a6c6770a0f4f138a1ef8f8133ad`.
`read-policy-scope.json` records individual source hashes.

The fresh comparison uses scalar baseline
`a0cd57950240bd9c171e7a7de7113972581c40e2d574e233834212b2da1d2013`
and pins all processes to CPU 0 on an otherwise idle local host. Hardware
SHA-256 remains essentially unchanged; software SHA-256 uses 380,850,070 versus
376,655,752 instructions and its 21-pair native median ratio is 0.9310.
The pure nine-value benchmark remains slower (1.1087). Seven alternating
whole-compiler pairs have median ratio 0.9942, with individual ratios from
0.9424 to 1.1043; no whole-compiler elapsed-time improvement is claimed.

`nine-live-record.fern` isolates the additional read policy. The scalar baseline
and arithmetic-only compiler emit identical native binaries. The read policy
removes their two frame references and reduces saved-register pushes from five
to four. Across twenty million calls, Cachegrind counts 940,001,438 instructions
for the baseline, 940,001,450 for arithmetic-only, and 780,001,420 for reads
(about 17% fewer). All print `4339612313273984`. Fifteen alternating native
triples have median read/baseline ratio 0.9223 and read/arithmetic ratio 0.9297.
These observations support the additional read cases. The pure multiplication
counterexample remains a measured tradeoff.

Reproduce the read-policy comparison on an idle host with:

```sh
python3 docs/benchmarks/x86-fixed-registers-2026-10-17/measure-reads.py \
  /absolute/path/to/scalar-baseline /absolute/path/to/arithmetic-only \
  /absolute/path/to/read-policy . /tmp/read-results
```

Set `FERN_BENCH_CPU=0` for the general `reproduce.py` comparison to pin it too.
Raw results for this source use the `read-policy-` filename prefix.

The final broad x86 semantic/SSA/float/call/division/shift/scalar integration
suite passes in 58.702 seconds. An explicit run of all six added debug corpus
entries on the three native targets passes in 1.938 seconds; CI test-lane
coverage passes in 1.276 seconds.


## Integration with merged scalar-return main

Starting fresh from main `2c23674c2`, source `52328b7d7` reaches identical
stages 2 and 3 at 12,676,624 bytes, SHA-256
`8d2d0c59bd20500f351c59334022ab856090c9e39ec3811aab26527837dd044d`.
The focused execution, scalar-return, ARM-helper and complexity tests pass
in 49.012 seconds. The fixed-register allocator's graph constraints pass
separately in 1.949 seconds, and all six extra-caller debug fixtures pass
in 2.167 seconds. Every one of eleven strict driver-size checks passes with
complete coverage and unchanged baselines/tolerance (146.778 seconds).

The fresh deterministic read-heavy comparison is `main-2c23-record.json`:
current main `c7d93477…` takes 940,001,312 instructions and the candidate
780,001,294, a 17% reduction, with identical checksum 4339612313273984.
The hot function changes from five callee-saved pushes and two frame accesses
to four pushes and no frame accesses. This integration repeat records no
elapsed timings; the earlier speed measurements retain their original scope.
