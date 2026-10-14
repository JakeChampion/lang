# 2026-10-13 — borrowed array updates test uniqueness before retaining

Refs #8920 and #8278; the local pilot addressed #12332, #12359, #12360 and #12363,
which were subsequently fixed independently upstream. This is a collection-operation pilot, not completion
of the ownership epic or a claim that all coreutils are fastest.

## Reproduction and implementation

A borrowed function that replaces two elements with chained `.with` calls
copied a uniquely held array. Its frame retained the receiver before the
runtime uniqueness test, making an otherwise unique buffer look shared.
The isolated reproduction performed 100 swaps of a two-element dynamic array:
100 allocations before, zero after. Retaining the caller's original value
still requires one allocation and leaves that original unchanged.

Extend the existing append deferred-unit protocol to non-field `.with`
chains. `ssaunits` admits the chain only under the existing owned/kept-use
guards and includes the update in the caller-side grow inventory. The latter
is essential: an older caller alias must make the runtime count shared.
`ssarc` tests the borrowed receiver's actual uniqueness, preserves the caller's
unit, and settles the result's owed unit using the existing chain machinery.
A shared input is sliced into a fresh owned array; reference elements receive
their own units before the replaced element is released. Record-field update
admission and bounds checks are unchanged.

`std/array.reverse` uses paired swaps through this protocol, rather than
appending every element into a fresh growing buffer. Both namespace and method
spellings keep their borrowed public API. Unique inputs reuse their buffer;
shared inputs copy once on the first swap. Empty and singleton inputs need no
update. No new builtins, target-specific ownership rules, or relaxed verifier
checks are introduced.

An `own` annotation experiment on `NameIndex.added` produced byte-identical
binaries and identical instruction counts and was discarded. A consuming
helper/`fip` experiment was also discarded: the static verifier does not
establish this borrowed API's allocation contract. There is no `fip` promise
on public reversal, and no diagnostic was weakened to add one.

The first swapping prototype reduced allocation but was slower: its linked
updates repeated runtime uniqueness checks throughout the loop. It was not
accepted on allocation counts alone. The first guarded swap now precedes the
loop, so its result seeds the loop's existing `sole_boxes` proof. A linked
update marked by the existing `unique_receivers` proof uses its sole receiver
directly while leaving the result's owed unit deferred. This removes redundant
guards; it does not assert uniqueness from ownership or bypass a missing proof.

## Correctness and resource gates

Permanent tests cover unique arrays of lengths 0, 1, 2, 3, 32 and 10000, a
mixed unique/shared specialization, retained caller aliases, reads of the
original input after an update, heap strings and reclamation after allocator
warmup on both unique and shared heap-string arrays, and packed `u8[]`
storage including bytes 127 and 255. The direct
compiler regression does not import `std/array`.
The structural stdlib suite additionally covers nested arrays, records with
heap strings, closure captures, and retained loop snapshots.

The resource gates require zero allocations for repeated unique reversal,
exactly one for shared reversal, no reference-count underflow, and a flat
second-churn heap high-water mark. They run on x86-64 Linux, ARM64 Linux under
QEMU, and WASI under Wasmtime. QEMU timings are not performance evidence.

## Reproducible measurement fixture

`docs/benchmarks/array-reverse-2026-10-13/reverse.fern` performs 2000 reversals
of 32 or 10000 i32 elements. `small` selects 32; otherwise 10000. A second
argument `shared` retains and checks the previous value during every reversal.
Every run verifies the final contents and exits zero without output.
Release timing has no heap/RC instrumentation; allocation traces are separate
builds with `FERN_RC_TRACE=1 FERN_LEAKCHECK=1`.

Both comparison compilers are built by the verified stage0
`stage0-20261009-823f7b1`, with the baseline source/stdlib at
`3243400380ec94cc3ff825d21b1894b8c5d93587` on both sides, with only the
archived `production.patch` applied to the candidate. The three production
paths are unchanged between that baseline and the later `ee091d16f`
validation base; unrelated later main updates are not timing inputs.
Compile the same fixture with each compiler using `-O -target
x86-64-linux`, explicitly passing the corresponding stdlib directory.
The host is Linux 6.18.44 x86-64, AMD EPYC 9V74; Go 1.27.1 provides the test
runner. Cross-backend correctness uses QEMU 10.0.2 and Wasmtime 46.0.1.

## Controlled release results

Hyperfine 2.0.0, 5 warmups and exactly 100 measurements per binary/case, no
shell, no concurrent heavy suite or profile. Means ± standard deviation, ms:

| Elements | Input | Baseline | Candidate |
| ---: | --- | ---: | ---: |
| 32 | unique | 0.294 ± 0.070 | 0.201 ± 0.053 |
| 32 | retained snapshot | 0.297 ± 0.061 | 0.247 ± 0.066 |
| 10000 | unique | 22.543 ± 1.857 | 15.174 ± 2.359 |
| 10000 | retained snapshot | 24.424 ± 6.498 | 19.191 ± 6.582 |

The large unique workload improves 32.7%; the retained-snapshot workload
improves 21.4%, with considerably more variance. The 32-element cases include
substantial process-startup cost; these are not isolated operation latencies.
Peak process RSS is slightly higher (about 2.1 versus 2.3 MiB for the large
unique case), despite lower logical live heap. Do not infer RSS from allocation
counts or claim a compiler-wide/coreutils-wide speedup from this fixture.

Separate trace builds count the entire successful process, including argv
and input construction. All rows have allocations equal to frees and zero
live bytes at exit:

| Elements / input | Allocations, base → new | Allocated bytes, base → new | Peak live bytes, base → new |
| --- | ---: | ---: | ---: |
| 32 / unique | 8007 → 7 | 1152664 → 664 | 712 → 432 |
| 32 / snapshot | 8008 → 2008 | 1152696 → 560696 | 712 → 560 |
| 10000 / unique | 26016 → 16 | 525110512 → 262512 | 327752 → 196656 |
| 10000 / snapshot | 26017 → 2017 | 525110544 → 160310544 | 327752 → 211120 |

Thus 2000 large unique reversals allocate zero operation buffers instead of
26000; snapshots allocate 2000 rather than 26000. Shared copying is not removed
or credited as reuse. Native executable sizes are 11328 → 12072 bytes; the
same-producer compiler sizes are 11803992 → 11806704 bytes. The added fast-path
machinery has a real code-size cost.

Compiler SHA-256 (baseline, candidate):
`2284ebaea9001502884865aeaeca76045cf879ba72bfa745cc59fea404900d0b`,
`40baec840f1dd428073b997c732b66a822aa161b2a1ec1953965ca92b3a249be`.
Fixture executable SHA-256 (baseline, candidate):
`867de50813063be3c5dd84bac14c213b73590dd08469c48421c803ac2ad579d3`,
`1fe515963a54e11e360c986f9235e535ee44c830775834757d30206e0374452f`.
Compressed raw timing exports and resource summaries accompany the fixture.
`summarize-profile.py` reproduces the instruction/header-write summaries
from each matching profile binary and compressed Callgrind export; it checks
that self costs sum to the recorded total before attributing header writes.

Two checker timing rounds use 10 samples and 2 warmups per binary, a cold
certifier memo (`FERN_RC_CERTIFY_MEMO=0`), one frozen baseline source/stdlib,
and assembly output at `/dev/null`. Base-first is 3.806 ± 0.150 s versus
3.566 ± 0.084 s; candidate-first is 3.701 ± 0.174 s versus 3.713 ± 0.245 s.
The apparent improvement does not survive order reversal, so no compiler
speedup is claimed. Peak RSS is about 376.9 MiB on both. These are
same-stage0-produced compilers, not stage-2 self-compile measurements.

Separate Callgrind 3.27.1 runs on GNU-as-linked assembly copies count dynamic
instructions and stores to the runtime's 32-bit RC word at pointer minus 8.
Increment/decrement stores include inline operations and helpers' register
updates. This is **header-write traffic**, not every attempted retain/release
(guards may exit without writing):

| Large input | Instructions, base → new | Increment stores | Decrement stores | Zero stores |
| --- | ---: | ---: | ---: | ---: |
| unique | 336308894 → 250338888 | 0 → 2000 | 0 → 2000 | 26016 → 16 |
| snapshot | 336333073 → 400731073 | 0 → 2000 | 0 → 2000 | 26017 → 2017 |

Shared reversal executes more instructions but has lower measured wall time.
Growing-buffer copying and allocation matter too, and the borrowed API has
real retain/decrement traffic: zero allocation is not zero work. Trace-confirmed
capacities and the 8-byte i32 slot give a derived payload-copy volume:
baseline growth copies 131040 bytes per large reversal (capacities 4 through
8192), candidate unique reversal copies zero, and candidate snapshot reversal
copies 80000 bytes once. Over 2000 reversals that is 262080000 → 0 or
160000000 bytes. This is derived volume, not a dedicated runtime byte-copy
counter; input construction is excluded.

## Validation status

The final candidate passes the eighteen backend/resource regression legs,
including the independent borrowed-update test, and the full semantic SSA,
reuse differential and source-RC gates (x86-64, ARM64, WASI and x86 sanitizer).
Borrowed receiver projections from nested arrays and shared record fields
also preserve their containers on all three targets. The earlier structural
stdlib suite passes its 19 cases under interpretation and self-hosting.
The six focused resource/regression tests (eighteen backend legs), the
pipeline controls and simulated-proxy regression pass again on `b9c5573db`
(24.249 s). That main revision retires the Go IR oracle; it is not restored
or used as a current validation gate.

The full local `go test ./...` run reached genuine baseline GNU 9.12 failures
(`nproc`, `pr`, `who`) and host fixture failures (missing block device and a
cross-filesystem root-move fixture). The environment restart then interrupted
the remaining packages; that run is **not** a completed full-suite validation.
The nproc repair is separately in #12336. GNU 9.12's ABI/zone-data-dependent
cases still require attention. Current main also has unrelated emitted-RC
refusals tracked by #12334/#12339; no certification gate is disabled to avoid
them. Those fixes and #12336 are now merged. The candidate has been brought
onto `ee091d16f`; the implementation paths are unchanged by the intervening
main commits. The full suite is restarted in a disposable Ubuntu 24.04
container with GNU 9.12 built against the same glibc ABI as CI, the pinned
Go/QEMU/Wasmtime, and read-only source. Root-operand fixtures stay inside that
disposable filesystem. A host rerun was stopped before those fixtures, not
counted as completed validation. This candidate remains uncommitted pending
completion of the local bar. That container run was also interrupted by an
environment restart after reporting CLI builds unable to obtain VCS status.
The 2026-10-14 rerun explicitly trusts only the two mounted Git worktrees for
the container's root user; its Git preflight succeeds without disabling VCS
stamping. Source remains read-only, with only generated `build`/`bin` paths
writable. A named cache volume preserves compiler/build caches across restarts.

The source-format checks, full source-lint suite, `go vet ./...` and targeted
ownership/allocation diagnostic tests pass on 2026-10-14 before the subsequent
main update. The container sweep clears the complete GNU 9.12 coreutils corpus
(714.452 s) and printer suite (999.153 s), but **the overall sweep fails**:
e2e reports failures after 5344.698 s and e2ecompiler times out at 90 minutes.
It is not a completed passing full-suite validation. The earlier
host `pr`/`who` differences do not reproduce against the CI glibc/zone-data
environment; GNU remains 9.12 throughout.
The packed-byte regression was added while that sweep's e2e binary was already
running and passes separately on all three backends (19.600 s). The unique
heap-string reuse/reclamation regression also passes separately (18.907 s).
No production implementation changed during that sweep. Its failures led to
three additional changes before the next complete validation:

- #12359: verbatim parsing no longer invents a contextual lambda return
  annotation during formatting. Compilation still gets semantic annotations;
  native and corpus formatter parity regressions cover this distinction.
- The pipeline's borrowed unique-buffer control now correctly expects zero
  allocations. A fourth, genuinely shared control retains and rereads the old
  receiver, verifies unchanged contents and still requires allocations. The
  owned and FIP assertions remain intact.
- #12360: simulated HTTP policy reads proxy variables from the simulated
  environment, not host configuration. A poisoned child-only host proxy must
  not affect the first virtual request; setting the simulated proxy must affect
  the second. Public-only policy is preserved; no host network guard changes.

The corrected runner copies a read-only incoming checkout into a writable,
disposable filesystem for tests that create package-local fixtures. It mounts
both mise installs and their HTTP-tarball symlink targets, installs jq and ARM
libc headers, and uses an unconfined seccomp profile plus SYS_PTRACE inside a
private container PID namespace. This lets the sandbox tests install and test
their own restrictions rather than inheriting an unrelated outer filter.
Results, source hashes, complete discovery lists, shard assignments and exit
codes are persisted in a named volume. Shard assignments are checked against
the actual discovery list, including fuzz seeds, for exact coverage.

Two worker limitations require explicitly recorded execution environments:
the host kernel omits `/proc/self/coredump_filter`, so the unchanged
`TestRunWithStackLimit` is executed in a Linux 6.8 software VM and its explicit
PASS/result marker checked; stock QEMU 10.0.2 drops unnamed ARM64 termios
control-character slots, reproducing with baseline and candidate. The local
QEMU runner preserves the raw `c_cc` bytes before its existing named-character
mapping, scoped to ARM64 guests on x86-64 hosts. The unchanged ARM64 termios
test passes there (25.546 s). Neither workaround is a production compiler
change or a performance input. Stock-QEMU success is not claimed.

The candidate remains uncommitted pending the complete current-source suite,
including the separately accounted VM kernel test. No test failure, timeout
or interruption is counted as a pass.

Persisted results recovered after the next workspace restart establish that
the `b9c5573db` e2e shard 0 did finish successfully (1835.162 s). Compiler
shard 0 completed (3154.209 s) with exactly one failure, the supervised-worker
test reading `/proc/PID/task/PID/children`; missing host-client output was not
evidence that either package was interrupted. The other shards did not start.

The managed kernel also omits CONFIG_CHECKPOINT_RESTORE, which supplies that
optional children interface. #12363 adds strict direct-child PPid enumeration
as a fallback for the single-threaded Fern supervisors. Existing exact worker
counts, supervisor termination, idle connection EOF, worker exit and closed
listener assertions remain unchanged. Missing parents, unreadable records and
malformed records remain failures. Pure fixtures cover direct children versus
grandchildren, zombies, complex process names, missing/malformed metadata and
the original kernel interface; a real forked child covers fallback enumeration.
Both unchanged self-host supervised-worker tests pass on updated main
`34360bd01` (44.223 s), including the previously failing test.

The next full validation uses `34360bd01` plus this candidate. Each suite's
coordinator runs all four shards inside one disposable container, records each
shard's explicit result, and continues coverage even if another shard fails.
The e2e coordinator also runs the remaining Go packages with one package
process to bound overlap with the compiler suite. This avoids relying on a
host client to launch later shards after an environment restart. The unchanged
kernel VM test passes again with the current-source static test binary. These
later main and harness changes are not inputs to the frozen timing experiment.

That frozen sweep finishes with all four e2e shards and all 71 other Go
packages passing (GNU 9.12 coreutils: 681.584 s). Compiler shards 0, 1 and 3
pass in 3224.470, 2763.408 and 3625.271 s; shard 2 finishes in 3249.416 s
with exactly one failing top-level test: `TestSelfHostSSAPhysicalRCRejects`,
exit 166. The whole sweep is therefore **failed**, not a passing full bar.
All 15 additional opt-in self-host differential/sanitizer/fixture/quarantine,
sandbox, fuzz-seed and coverage-smoke gates plus the generation gate pass.
The coverage-guided smoke test uses its default 30-second budget; the feedback
gate uses five streams of 300 guided and 300 blind mutations each. Neither is
claimed as the 25-minute nightly campaign. Platform/debugger/kernel-feature
skips in the ordinary suite remain explicitly recorded.

The failing rejection driver's positive borrowed-array update section still
requires zero caller-bracketing rows, contradicting the new protocol's alias
protection. It is corrected to require the exact whole-buffer row, a
uniqueness guard before the first result retain, and both `arr_slice` shared
fallback and `arr_set`. The original retain requirement is strengthened into
an ordering assertion; all corrupted-plan, token and carry rejections remain
unchanged. The corrected fixture passes in an isolated worktree with identical
production hashes (7.410 s); the original fixture passes with original
production (7.267 s). The correction is then applied to this worktree, with
the failed sweep preserved and full validation restarted before committing.

The corrected frozen `34360bd01` candidate completes passing local validation
on 2026-10-14. The four e2e shards pass in 1836.296, 1075.176, 1137.067 and
910.506 seconds; compiler shards pass in 2981.603, 2632.108, 3004.038 and
3355.960 seconds. All 4481 e2e discoveries (including the unchanged VM kernel
test) and 3578 compiler discoveries are assigned exactly once. All 15 relevant
opt-in gates actually pass, as does the generation gate. The default suite's
unsupported-platform, debugger, kernel-feature and mutation-only skips remain
recorded; this is not native Darwin/ARM-memcheck or nightly fuzz coverage.

The initial remaining-package phase replayed cached results. A subsequent
`-count=1 -p 1 -parallel 2` run executes all 71 remaining Go packages successfully
with no cached package results, including GNU 9.12 coreutils in 630.500 seconds.
Every coordinator has an explicit zero exit marker. The final combined audit
requires complete coverage, actual opt-in passes, fresh remaining-package
coverage and matching SHA-256 for every source file across all three ledgers
and the frozen host checkout. Final vet, Go formatting and Fern formatting
checks also pass. The prior failed sweep is retained separately as failed.

The result archive is `ownership-343-full-fresh-final-1540.tar.gz`, with audit
`ownership-343-full-fresh-final-1540-audit.json`; a separate
`ownership-343-validated-source-1541.tar.gz` preserves the exact source before
this validation-only documentation update. These local tooling artifacts do
not assert CI success or validate later main. The controlled A/B timing inputs
and raw measurements remain unchanged.

While this source was frozen, upstream independently merged #12366 (formatter),
#12367 (proc children), #12375 (simulated proxy), #12377 (dynamic-call grow
brackets) and #12378 (borrowed `.with` reuse). The four prerequisite issues are
already closed. Reconciliation must retain those implementations and their
alias-safety tests, including `lent_withs` restrictions on static sole-box
proofs. The remaining reversal/resource regressions and measurement records
need integrated-head validation and new measurements where applicable; the
old pilot's performance numbers do not describe that newer compiler.

The integration worktree starts at `c78ff2894`. It retains upstream compiler
and prerequisite implementations verbatim, including the dynamic-call brackets
and lent-with sole-box exclusions, and carries only reversal, supplemental
array tests and historical measurements. The old compiler fast path is not
transplanted. Its correctness/resource tests and performance still require
new integrated-head evidence. Archived patch text is stored losslessly as
`production.patch.gz` and `qemu-cc.patch.gz`; decompress before applying.

The integrated candidate passes all seven focused top tests (18 backend legs
and structural cases) in 25.575 seconds, and all 27 selected ownership,
dynamic-call, physical-RC, semantic, reuse and FIP gates in 198.066 seconds,
with no skips. Exact discovery and source hashes are audited; static gates
pass. This is targeted integrated validation, not a full integrated CI suite.

Fresh idle native measurements on `c78ff2894`, using one baseline-produced
compiler and differing only in reversal's stdlib implementation, reject this
candidate as a general performance change. With 100 samples and five warmups
in both command orders, 10000-element unique reversal is 17.7% and 20.9%
slower; retained-snapshot reversal is 19.7% and 8.7% slower. Small workloads
improve but include startup cost. The raw results and exact reproduction are
in `docs/benchmarks/array-reverse-c78-2026-10-14/`. No performance PR is
submitted for this slower candidate. Baselines and safeguards are unchanged;
the older frozen experiment remains historical rather than being relabeled
as current-main evidence.
