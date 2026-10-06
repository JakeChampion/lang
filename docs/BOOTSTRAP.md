# Bootstrap — building the compiler without Go

`make bootstrap` builds the self-host compiler from a clean checkout with no Go
toolchain and no native backend involved: a pinned earlier compiler (stage0)
compiles `compiler/fern.fern`, the result compiles and runs a one-line
program and `coreutils/tr`, and is installed as `bin/fern-selfhost` — the same artifact `make
selfhost-cli` produces via `./bin/fern`. `make distcheck` is the reproducibility
half: that compiler recompiles its own source, the result does so once more,
and the last two binaries must be byte-identical. This is
`NATIVE-CONVERGENCE.md §3a` precondition 1, the shape `BOOTSTRAP-RESEARCH.md
§2` specified, and the runbook its §10 asked for. Both run in CI on all three
hosts (`bootstrap.yml`, `verify`).

```
make bootstrap                       # pinned stage0 -> stage1, smoke-tested, installed
make distcheck                       # stage1 -> stage2 -> stage3, stage2 == stage3
STAGE0=bin/fern-selfhost make bootstrap   # run the chain from a local candidate
bootstrap/bootstrap.sh stage0        # print the verified stage0's path, nothing else
```

`stage0` is for a caller that compiles with the pin itself: the self-host test
harness (`internal/e2eharness/self_host_compiler.go`) builds every test driver
with it, so the drivers are held to the pin the way `fern.fern` is.

Everything lands in `build/bootstrap/`: the cached stage0 under
`stage0/<release>/`, `stage1`, `stage2`, `stage3`, and the smoke programs. The smoke is
a one-line program and `coreutils/tr` built with `-O` and run: a stage1 that
an old stage0 built through its AST-lowering fallback compiles the first and
aborts on the second (#9763), so a one-liner alone installed a compiler that
could not build real programs. Nothing here
reads `bin/fern`; the target does not depend on it and the CI job that runs it
installs no Go.

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
on; cross-compiling stage1 for another host is not part of the chain, because
stage1 has to run to be smoke-tested and (in `distcheck`) to compile stage2,
which in turn compiles stage3.

**The artifact is a compiler binary per host, hosted as a release asset, not a
file in the tree**: the self-built stage2 the publish job reaches on each host.
Settled by measurement on 2026-09-01, on native builds:

| | size | gzip -9 | xz -9 |
|---|---|---|---|
| `x86-64-linux` compiler, built by native | 27.2 MB | 3.7 MB | 2.2 MB |
| `arm64-linux` | 32.9 MB | 3.8 MB | 2.0 MB |
| `arm64-darwin` | 100.8 MB | 4.1 MB | 2.3 MB |

Three hosts per refresh is 10 MB of history per refresh, forever, in every
clone — the cost the issue (#6644) flagged — where a release asset costs the
tree one line. A checked-in `.s` set is not more auditable in practice (tens of
MB of assembly nobody reads) and needs an external assembler, which the
in-process backends exist to avoid. The audit that does work is regeneration:
the lock names the source commit, and at that commit `make selfhost-cli &&
STAGE0=bin/fern-selfhost make distcheck` rebuilds the host's pin as its
`build/bootstrap/stage2` — the bytes are deterministic per commit
(`TEST-GATES.md`'s emit hashes rest on the same fact) — so anyone can check
the pinned sha256 against a build of their own.

WASM as the snapshot format (`BOOTSTRAP-RESEARCH.md §7`) is ruled out twice over
today. `fern.fern` cannot be compiled to wasm: `write_file_exec` needs
`fsmode`, and E066 refuses it because the component-model filesystem has no
permission bits (#6133) — a target property, not a gap. And compiling the
compiler peaks at 4.0 GB of resident memory under a 16 GiB `MAP_NORESERVE`
arena — at wasm32's 4 GiB address ceiling with no room for growth.

The first reason used to be `sleep_ms`, which had no wasm lowering; #7947
landed it and `providedMissingLowering` is now empty. Both remaining reasons
are structural, so "revisit if both move" is a weaker prospect than it was.

## Refreshing the pin

Refresh when:

- `make bootstrap` fails in `stage1` because the source now uses a construct
  the pinned compiler does not know. The failure message says so.
- On a cadence, so the pin does not rot: after roughly fifty PRs touching
  `compiler`, and before a tagged release.

How: dispatch `.github/workflows/bootstrap.yml` with `publish` ticked on the
branch to pin. The job builds a candidate on each host with `make
selfhost-cli`, proves it compiles the compiler at that commit and that the
result runs (`STAGE0=candidate make bootstrap`), and tags an immutable
`stage0-<yyyymmdd>-<sha7>` release carrying the three `.gz` binaries and a
ready-made `stage0.lock`. Copy that lock over `bootstrap/stage0.lock` and
commit; the `verify` job on the PR then proves the pin from a Go-less runner.
Releases are never deleted or moved: every commit that ever pinned one must
stay bootstrappable.

`make selfhost-cli` runs `bin/fern`, the launcher, which compiles with the
current pin: the candidate is the pin's build of the source. So a publish can
only pin source the current pin compiles, and a new construct reaches the
compiler's own sources in two steps: teach the compiler the construct without
using it, publish and pin from that commit, then use it. This is the "Go 1.4
rule" (`NATIVE-CONVERGENCE.md §1`), with the pin in the place native held.

The candidate is not what gets pinned either: the publish job runs
`STAGE0=candidate make distcheck` and uploads `build/bootstrap/stage2`, the
self-built fixed point, on every host. Those bytes are what the current source
emits for itself, so any correct compiler of that source reproduces them.

When the pin cannot build the source at all — as on 2026-09-28, when the
arm64 pin's stage1 looped on the compiler — the seed has to come from
elsewhere: an earlier release (none is ever deleted) passed as `STAGE0`, or
`bin/fern` built at a commit from before the native backends were deleted
(2026-10-05), whose native compile needs no pin. Either may need a chain of
intermediate commits, one publish each, to reach a source it cannot compile
directly.

## What `make distcheck` measures

Measured 2026-09-28 on a 4-core, 16 GB x86-64 host, pin `stage0-20260925-f81d8d7`:

| step | compiler | wall | peak RSS | result |
|---|---|---|---|---|
| stage1 | native-built stage0 compiling `fern.fern` | 307 s | 6.1 GB | 13.5 MB binary, works |
| stage2 | stage1 compiling `fern.fern` | 209 s | (not sampled) | 12.3 MB binary, works |
| stage3 | stage2 compiling `fern.fern` | 137 s | 5.7 GB | **byte-identical to stage2** |

For comparison, `bin/fern` (native) compiles the same source in 38 s at 1.4 GB.
The self-built compiler uses the memory the native-built self-host compiler
does, and nothing it emits differs between generations. On 2026-09-02 stage2
was OOM-killed at 13.9 GB: the rc-log entries of that date list the leaking
sites the RECLAIM side of roadmap goal 2 closed, and
`docs/SELFHOST-SEMANTIC-SOURCE.md` records the typed lowering, now the
default, that compiles the whole tree in a twelfth of the AST lowering's
memory. `TestSelfHostPerModuleEmitAllFixpointX86_64` compiles the compiler
eight units per process; the whole-program compile is the configuration only
this target gates.

**stage1 is not part of the comparison.** stage1's code was generated by the
pin, stage2's and stage3's by the current source. They differ whenever a code
generator change landed after the pin's `source` commit — on the run above,
from byte 25 and by 1.2 MB — and that is not a failure. The script prints
which case it saw. A pin at the current commit makes all three identical.

## Debugging a stage2 != stage3 divergence

Both binaries are kept in `build/bootstrap/`. Two shapes:

- **stage3 crashes or exhausts memory.** Build a symbolised stage2:
  `bin/fern -target x86-64-linux -emit asm compiler/fern.fern` is the
  same code as GAS text, and gcc links it with symbols, so gdb names the frame in
  one step where the stripped binary gives an address. Exit codes tell the walls
  apart: 125 is the arena (`LOCAL-DEV-LOOP.md`), 137 the host's RAM, 139 a real
  fault.
- **stage3 differs but works.** The divergence is in what stage2 *emits*, so
  find the input that exposes it: `scripts/selfhost-emit-hashes` run once with
  stage2 and once with stage3 over the conformance corpus diffs to the fixture
  and target that differ, and a fixture is a bisectable input where the
  compiler is not. A difference with no fixture exposing it is nondeterminism
  in the emit order — hash-map iteration, address-dependent sorting — and
  `-emit asm` of `fern.fern` from each side, diffed, shows where.

## Sanity-checking the pin by hand

```
curl -fsSL <url>/fern-selfhost-x86-64-linux.gz | gzip -dc | sha256sum
```

must print the lock's `x86-64-linux` line. To go further, check out the lock's
`source` commit and rebuild the pin there: `make selfhost-cli &&
STAGE0=bin/fern-selfhost make distcheck` and compare `build/bootstrap/stage2`
to the host's download, byte for byte.
