# The certifier gives a literal no unit, and a lane counts what it cannot decide

2026-11-16. x86-64, arm64 and wasm32. The change is in `rccertify`,
`irverifygate`, `rcsigs` and `rcmemo`, and in the Perf lane.

## The gap

The certifier refuses a compile only for a definite imbalance. A value it
cannot prove balanced is unproved, and nothing counted those values outside a
manual `FERN_RC_CERTIFY=report` run. This is the census of
`compiler/fern_main.fern` on x86-64 before this change:

| functions | tracked | unproved | findings | skipped | opaque calls |
|---:|---:|---:|---:|---:|---:|
| 11,194 | 263,337 | 33,273 (in 3,663 functions) | 0 | 59 | 65, to 4 callees |

## What the unproved values were

A walk can mark where each unproved value became `maybe`. Before this
change, the split was:

| where the value became maybe | values |
|---|---:|
| a join inside a loop | 19,202 |
| a join outside any loop | 10,525 |
| not a join (an unknown call, an unknown origin, a count above the cap) | 3,818 |

The 2026-10-11 entry gave one shape as the cause of most joins: a retain on
one arm, and a consume on a later arm of the same test. That shape is now
rare.

A test of the idea: for each key that a function tests more than once,
walk the function again under each value of the key. A key is one comparison
of the same operands, defined in a block that no cycle passes through. This
walk proved 173 more values, 1% of the rest. It was not kept, because the
gain does not pay for the code.

Such a walk also cannot refute. Some of its
paths are impossible for facts that it does not track, so its findings were
false. An example is a new array that a test compares with a borrowed
parameter for identity.

The samples of the joins showed one shape most often: a phi that merges an
owned value with a string or aggregate literal. The literal's edge fed 0
units, the owned edge 1, and the meet was `maybe`. But a release or a return
of such a phi is correct on both paths. Every retain and release skips an
immortal box.

## The change

- The lattice has a free state for a string or aggregate literal
  (`STR_LIT`, `AGG_LIT`), and "c units or free" for a merge of one with a
  counted value. A spend or a retain of the free part does nothing. A unit
  still held on the counted part is a leak. A spend of 0 on it is an
  over-release. A borrow and a null constant are not free. A release of a
  borrow is a bug, and a release with the boxed mark has no null guard.

- `irverifygate.decided` also decides `lt`, `le`, `gt` and `ge`, signed and
  unsigned, of 32 or 64 bits, and any comparison of a value with itself.

- The census line gives the most rounds a walk took to settle.

- A runtime unit's gate prints its census. Before, the 289 runtime functions
  of `fern_main` were certified but left out of every report.

- `rcsigs` classifies `__fern_task_fd_gen`, the byte-view CRC helpers and
  every abort routine, so the compiler's own sources make no opaque call.

- `FERN_RC_CERTIFY=mutate` certifies three mutants of each function that
  passes: its middle release deleted, its middle retain deleted, and its
  middle release done twice.

- `scripts/certify-census` and `.github/certify-baseline.txt` pin the
  unproved values, the skipped functions, the opaque calls and the mutant
  outcomes in the Perf lane, strict with no tolerance.

- The memo key is two 64-bit digests, and the memo is written beside its file
  and renamed over it.

## Measured after

Over `fern_main` on x86-64, on the same set of functions as before, 17,641
values are unproved (from 33,273), with 0 findings. With the runtime unit in
the report:

| functions | tracked | unproved | findings | skipped | opaque calls |
|---:|---:|---:|---:|---:|---:|
| 11,483 | 264,654 | 17,760 (in 2,554 functions) | 0 | 59 | 0 |
 The census compile
took 77.4 s, against 77.6 s before. The most rounds any walk took is 11,
against a limit of 64.

Over `compiler/checker.fern` on x86-64, 5,580 mutants: 4,886 refuted, 153
weakened, 541 missed. A missed mutant is often a release of a value that a
callee with an unsure effect already made unproved: `__fern_arr_concat`
returns its argument or a new box, and a later `!=` test of the two decides
the release.

Over the 2,739 programs under `conformance/`, `tests/`, `examples/` and the
utilities, compiled for x86-64, arm64 and wasm32: 0 findings.

## What is left

- A gate knows the ABIs of its own unit only, so a program's call to a
  generic instantiation or a runtime helper of another unit is opaque. The
  programs above make many such calls. The next lead on the unproved count
  of programs is to give each gate the ABIs of all units.
- The 59 functions that resume from a task frame are not certified. Their
  bodies before `suspend.transform_state` have the same retains and releases,
  and no frame. The gate can certify those bodies when the lowered state
  carries them to it.
- `__fern_arr_concat` and the other callees with an unsure effect have no
  model of "the argument or a new box".
