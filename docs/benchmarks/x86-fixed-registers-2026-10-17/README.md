# x86 SSA fixed-register constraints

Source `99285a95357849b6d2db317f71cdc9df012bf228` adds `rcx`, `rdx` and
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
