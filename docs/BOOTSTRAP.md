# Bootstrap — building the compiler without Go

Status: [reference] how `make bootstrap` and `make distcheck` work today.

`make bootstrap` builds the self-host compiler from a clean checkout, with no
Go toolchain:

1. A pinned earlier compiler (stage0) compiles `compiler/fern.fern`.
2. The result compiles and runs a one-line program and `coreutils/tr`.
3. The result becomes `bin/fern-selfhost`. `make selfhost-cli` makes the same
   artifact through `./bin/fern-oracle`.
4. `make build` compiles `compiler/fern_main.fern` with that compiler into the
   shipped `bin/fern`.

`make distcheck` is the reproducibility half. That compiler compiles its own
source, the result does so once more, and the last two binaries must be
byte-identical. This is precondition 1 of `NATIVE-CONVERGENCE.md §3a`, the
shape that `BOOTSTRAP-RESEARCH.md §2` specified, and the runbook that its §10
asked for. Both run in CI on all three hosts (`bootstrap.yml`, `verify`).

```
make bootstrap                       # pinned stage0 -> stage1, smoke-tested, installed
make distcheck                       # stage1 -> stage2 -> stage3, stage2 == stage3
STAGE0=bin/fern-selfhost make bootstrap   # run the chain from a local candidate
bootstrap/bootstrap.sh stage0        # print the verified stage0's path, nothing else
```

`stage0` is for a caller that compiles with the pin itself: the self-host test
harness (`internal/testing/e2eharness/self_host_compiler.go`) builds every test driver
with it, so the drivers are held to the pin the way `fern.fern` is.

Everything lands in `build/bootstrap/`: the cached stage0 under
`stage0/<release>/`, `stage1`, `stage2`, `stage3`, and the smoke programs. CI
restores `stage0/` from the Actions cache, keyed on the lock
(`.github/actions/stage0`). Only a cache miss downloads the release asset.

The smoke
test builds a one-line program and `coreutils/tr` with `-O` and runs them. A
stage1 that an old stage0 built through its AST-lowering fallback compiled
the first and stopped on the second (#9763). Thus a one-line program alone
installed a compiler that could not build real programs.

Nothing here reads `bin/fern-oracle`. The target does not depend on it, and
the CI job that runs it installs no Go.

`make distcheck` can use the stage1 of an earlier build again. It does so only
when `stage1.key` next to it names the same stage0 and the same files under
`compiler/` and `internal/stdlib`. If not, it builds stage1 again first. Thus
a stage1 of older sources never compiles a stage2.

## What the pin is

`bootstrap/stage0.lock`:

```
source <commit the stage0 was built from>
url https://github.com/JakeChampion/lang/releases/download/stage0-<yyyymmdd>-<sha7>
x86-64-linux <sha256>
arm64-linux <sha256>
arm64-darwin <sha256>
```

One asset per bootstrap host at `<url>/fern-selfhost-<host>.gz`, and the sha256
is of the **uncompressed** binary — what is verified is the thing that runs.
The three hosts are the machines this project builds on: the x86-64 and arm64
Linux CI runners and Apple Silicon. Each stage compiles for the host it runs
on. A cross-compile of stage1 for another host is not part of the chain.
stage1 must run for the smoke test, and in `distcheck` it must run to compile
stage2, which then compiles stage3.

**The artifact is a compiler binary per host, hosted as a release asset, not a
file in the tree**: the self-built stage2 the publish job reaches on each host.
Settled by measurement on 2026-09-01, on builds by the Go compiler of the day:

| | size | gzip -9 | xz -9 |
|---|---|---|---|
| `x86-64-linux` compiler | 27.2 MB | 3.7 MB | 2.2 MB |
| `arm64-linux` | 32.9 MB | 3.8 MB | 2.0 MB |
| `arm64-darwin` | 100.8 MB | 4.1 MB | 2.3 MB |

Three hosts for each refresh put 10 MB of history into each clone, forever.
That is the cost that #6644 flagged. A release asset costs the tree one line.
A checked-in `.s` set is not easier to audit in practice, because nobody
reads tens of MB of assembly. It also needs an external assembler, and the
in-process backends exist to avoid one.

The audit that works is regeneration. The lock names the source commit. At
that commit, `make selfhost-cli && STAGE0=bin/fern-selfhost make distcheck`
builds the pin of the host again as its `build/bootstrap/stage2`. The bytes
are deterministic for each commit, and the emit hashes of `TEST-GATES.md`
rest on the same fact. Thus anyone can compare the pinned sha256 with a build
of their own.

Two reasons rule out WASM as the snapshot format (`BOOTSTRAP-RESEARCH.md §7`):

- `fern.fern` cannot compile to wasm. `chmod` gives an image its executable
  mode, and it needs `fsmode`. E066 refuses it, because the component-model
  filesystem has no permission bits (#6133). That is a property of the
  target, not a gap.
- A compile of the compiler peaks at about 5.7 GB of resident memory (the
  stage3 row below). That is more than the 4 GiB address space of wasm32.

A third reason was `sleep_ms`, which had no wasm lowering until #7947. Both
reasons that are left are structural.

## Refreshing the pin

Refresh when:

- `make bootstrap` fails in `stage1` because the source now uses a construct
  the pinned compiler does not know. The failure message says so.
- On a cadence, so the pin does not rot: after roughly fifty PRs touching
  `compiler`, and before a tagged release.

How: dispatch `.github/workflows/bootstrap.yml` with `publish` ticked on the
branch to pin. The job:

1. builds a candidate on each host with `make selfhost-cli`
2. proves that the candidate compiles the compiler at that commit and that
   the result runs (`STAGE0=candidate make bootstrap`)
3. tags an immutable `stage0-<yyyymmdd>-<sha7>` release with the three `.gz`
   binaries and a ready-made `stage0.lock`

Copy that lock over `bootstrap/stage0.lock` and commit. The `verify` job on
the PR then proves the pin on a runner with no Go.
Releases are never deleted or moved: every commit that ever pinned one must
stay bootstrappable.

`make selfhost-cli` runs `bin/fern-oracle`, the launcher, which compiles with the
current pin: the candidate is the pin's build of the source. So a publish can
only pin source the current pin compiles, and a new construct reaches the
compiler's own sources in two steps: teach the compiler the construct without
using it, publish and pin from that commit, then use it. This is the "Go 1.4
rule" (`NATIVE-CONVERGENCE.md §1`), with the pin in the place native held.

The candidate is not what gets pinned either: the publish job runs
`STAGE0=candidate make distcheck` and uploads `build/bootstrap/stage2`, the
self-built fixed point, on every host. Those bytes are what the current source
emits for itself, so any correct compiler of that source reproduces them.

Sometimes the pin cannot build the source at all. On 2026-09-28, the stage1
of the arm64 pin looped on the compiler. Then the seed must come from one of
these:

- an earlier release, passed as `STAGE0` (no release is deleted)
- a `bin/fern` built at a commit from before the deletion of the native
  backends (2026-10-05), whose native compile needs no pin

Either can need a chain of intermediate commits, with one publish each, to
reach a source that it cannot compile directly.

## What `make distcheck` measures

`measured` in `bootstrap.sh` times each stage and reads its peak resident
set, through the `getrusage` of python3 for the child. It prints the figures
when the stage finishes. It also writes them as `metric<TAB>value` rows to
`build/bootstrap/distcheck-report.txt`: `bootstrap/<host>/<stage>.wall_s`,
`.peak_rss_mb` and `.size_bytes` for stage1, stage2 and stage3.

The `verify` job records that report in the note of the main commit under
`refs/notes/perf`, as lane `bootstrap-<host>`. Thus the cost of the
self-compile on each host is a trend that you read back with

```
scripts/perf-history show bootstrap/x86-64-linux/stage2.peak_rss_mb
scripts/perf-history show bootstrap/arm64-darwin/stage3.wall_s
```

A regression in the memory or the speed of the compiler shows as a step in
that trend. The step is at the first main commit that touches the compiler,
and nobody has to measure by hand. The lane runs when the compiler's source, the
stdlib or the bootstrap changes (ci.yml's lane table), which is when the
figures can move.

The last reading taken by hand, 2026-09-28 on a 4-core, 16 GB x86-64 host,
pin `stage0-20260925-f81d8d7`:

| step | compiler | wall | peak RSS | result |
|---|---|---|---|---|
| stage1 | native-built stage0 compiling `fern.fern` | 307 s | 6.1 GB | 13.5 MB binary, works |
| stage2 | stage1 compiling `fern.fern` | 209 s | (not sampled) | 12.3 MB binary, works |
| stage3 | stage2 compiling `fern.fern` | 137 s | 5.7 GB | **byte-identical to stage2** |

For comparison, on that date the native Go compiler (deleted on 2026-10-05)
compiled the same source in 38 s at 1.4 GB. The self-built compiler uses the
memory that the native-built self-host compiler uses, and nothing that it
emits changes between generations.

On 2026-09-02 the system stopped stage2 when it ran out of memory at 13.9 GB.
The rc-log entries of that date list the leaking sites that the RECLAIM side
of roadmap goal 2 closed. `docs/SELFHOST-SEMANTIC-SOURCE.md` records the typed
lowering, now the default. It compiles the whole tree in a twelfth of the
memory of the AST lowering. `TestSelfHostPerModuleEmitAllFixpointX86_64`
compiles the compiler eight units for each process. Only this target gates
the whole-program compile.

**stage1 is not part of the comparison.** The pin generated the code of
stage1, and the current source generated the code of stage2 and stage3. They
differ when a code generator change landed after the `source` commit of the
pin. On the run above they differ from byte 25, and by 1.2 MB. That is not a
failure. The script prints
which case it saw. A pin at the current commit makes all three identical.

## Debugging a stage2 != stage3 divergence

Both binaries are kept in `build/bootstrap/`. Two shapes:

- **stage3 crashes or exhausts memory.** Build a symbolised stage2.
  `bin/fern -target x86-64-linux -emit asm compiler/fern.fern` gives the same
  code as GAS text, and gcc links it with symbols. Thus gdb names the frame in
  one step, where the stripped binary gives an address. Exit codes tell the walls
  apart: 125 is the arena (`LOCAL-DEV-LOOP.md`), 137 the host's RAM, 139 a real
  fault.
- **stage3 differs but works.** The divergence is in what stage2 *emits*, so
  find the input that shows it. Run `scripts/selfhost-emit-hashes` one time
  with stage2 and one time with stage3 over the conformance corpus. The diff
  gives the fixture and the target that differ. A fixture is an input that
  you can bisect, and the compiler is not. If no fixture shows a difference,
  the cause is nondeterminism in the emit order, such as hash-map iteration
  or address-dependent sorting. A diff of the `-emit asm` of `fern.fern` from
  each side shows where.

## Sanity-checking the pin by hand

```
curl -fsSL <url>/fern-selfhost-x86-64-linux.gz | gzip -dc | sha256sum
```

must print the lock's `x86-64-linux` line. To go further, check out the lock's
`source` commit and rebuild the pin there: `make selfhost-cli &&
STAGE0=bin/fern-selfhost make distcheck` and compare `build/bootstrap/stage2`
to the host's download, byte for byte.
