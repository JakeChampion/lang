# Bootstrap — building the compiler without Go

`make bootstrap` builds the self-host compiler from a clean checkout with no Go
toolchain and no native backend involved: a pinned earlier compiler (stage0)
compiles `examples/self_host/fern.fern`, the result compiles and runs a small
program, and is installed as `bin/fern-selfhost` — the same artifact `make
selfhost-cli` produces via `./bin/fern`. `make distcheck` is the reproducibility
half: that compiler recompiles its own source and the two binaries must be
byte-identical. This is `NATIVE-CONVERGENCE.md §3a` precondition 1, the shape
`BOOTSTRAP-RESEARCH.md §2` specified, and the runbook its §10 asked for.

```
make bootstrap                       # pinned stage0 -> stage1, smoke-tested, installed
make distcheck                       # stage1 -> stage2, byte-identical (red today; below)
STAGE0=bin/fern-selfhost make bootstrap   # run the chain from a local candidate
```

Everything lands in `build/bootstrap/`: the cached stage0 under
`stage0/<release>/`, `stage1`, `stage2`, and the smoke program. Nothing here
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
stage1 has to run to be smoke-tested and (in `distcheck`) to compile stage2.

**The artifact is a native binary per host, hosted as a release asset, not a
file in the tree.** Settled by measurement on 2026-09-01:

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
the lock names the source commit, and `make selfhost-cli` at that commit
rebuilds the exact binary — the bytes are deterministic per commit
(`TEST-GATES.md`'s emit hashes rest on the same fact) — so anyone can check the
pinned sha256 against a build of their own.

WASM as the snapshot format (`BOOTSTRAP-RESEARCH.md §7`) is ruled out twice over
today. Native cannot compile `fern.fern` to wasm while `sleep_ms` has no wasm
lowering (#7947). And compiling the compiler peaks at 4.0 GB of resident memory
under a 16 GiB `MAP_NORESERVE` arena — at wasm32's 4 GiB address ceiling with
no room for growth. Revisit if both move.

## Refreshing the pin

Refresh when:

- `make bootstrap` fails in `stage1` because the source now uses a construct
  the pinned compiler does not know. The failure message says so.
- `make distcheck` goes green for the first time (below) — the refresh after
  that one is the first that can be produced without the native toolchain.
- On a cadence, so the pin does not rot: after roughly fifty PRs touching
  `examples/self_host`, and before a tagged release.

How: dispatch `.github/workflows/bootstrap.yml` with `publish` ticked, **on the
branch that needs the refresh** — a PR whose source needs a new construct
publishes from its own branch and pins it in the same PR. The job builds a
candidate on each host with `make selfhost-cli`, proves it compiles the compiler
at that commit and that the result runs (`STAGE0=candidate make bootstrap`),
and tags an immutable `stage0-<yyyymmdd>-<sha7>` release carrying the three
`.gz` binaries and a ready-made `stage0.lock`. Copy that lock over
`bootstrap/stage0.lock` and commit; the `verify` job on the PR then proves the
pin from a Go-less runner. Releases are never deleted or moved: every commit
that ever pinned one must stay bootstrappable.

The candidate is built by the **native** toolchain, and that is the honest
state of precondition 1: the *procedure* needs no native backend, the pin's
*provenance* still does, because only the native-built compiler can compile the
compiler within 16 GB (next section). The day `distcheck` passes, switch the
publish job to upload `build/bootstrap/stage2` — the self-built fixed point —
and the chain is Go-free end to end.

## `make distcheck` is red, and what that measures

Measured 2026-09-02 on a 4-core, 16 GB x86-64 host:

| step | compiler | wall | peak RSS | result |
|---|---|---|---|---|
| stage1 | native-built stage0 compiling `fern.fern` | 79 s | 4.0 GB | 7.3 MB binary, works |
| stage2 | stage1 compiling `fern.fern` | ~140 s | 13.7 GB | **OOM-killed** by the host |

The self-built compiler no longer crashes on its own source — it did, at 12.2
GB with a read of address 1, until `rc-log/2026-09-02-param-strarr-elem-counted-share.md`
— it runs out of memory. Its native assembler no longer does either: a
sanitized stage1 assembles `lexer.fern`, `parser.fern` and `checker.fern`
natively (`rc-log/2026-09-02-own-struct-update-reuse.md`), where each
exhausted the 16 GiB arena. What remains is the compile's own retention.
Leak-check-instrumented builds of both compilers on `checker.fern` show it: the
same number of allocations, a third of the frees (stage0 441 MB live, stage1
2.96 GB). The self-host's reclaim frees less than native's on the compiler's
own code, so a compiler compiled by it leaks its way to the host's ceiling; the
rc-log entries of 2026-09-02 list the leaking sites in order and what each
slice closed. This is the RECLAIM side of roadmap goal 2 measured as a
bootstrap, and the first green
`distcheck` is what makes a refresh Go-free. `TestSelfHostPerModuleEmitAllFixpointX86_64`
is green on the same tree because it compiles the compiler eight units per
process; the whole-program compile is the configuration nothing else gates.

**Do not add `distcheck` to the `verify` CI job until it passes here; when it
does, add it, and move the publish job to stage2.**

## Debugging a stage1 != stage2 divergence

Both binaries are kept in `build/bootstrap/`. Two shapes:

- **stage2 crashes or exhausts memory** (today). Build a symbolised stage1:
  `bin/fern -target x86-64-linux -emit asm examples/self_host/fern.fern` is the
  same code as GAS text, and gcc links it with symbols, so gdb names the frame in
  one step where the stripped binary gives an address. Exit codes tell the walls
  apart: 125 is the arena (`LOCAL-DEV-LOOP.md`), 137 the host's RAM, 139 a real
  fault.
- **stage2 differs but works.** The divergence is in what stage1 *emits*, so
  find the input that exposes it: `scripts/selfhost-emit-hashes` run once with
  stage1 and once with stage2 over the conformance corpus diffs to the fixture
  and target that differ, and a fixture is a bisectable input where the
  compiler is not. A difference with no fixture exposing it is nondeterminism
  in the emit order — hash-map iteration, address-dependent sorting — and
  `-emit asm` of `fern.fern` from each side, diffed, shows where.

## Sanity-checking the pin by hand

```
curl -fsSL <url>/fern-selfhost-x86-64-linux.gz | gzip -dc | sha256sum
```

must print the lock's `x86-64-linux` line. To go further, check out the lock's
`source` commit, `make selfhost-cli`, and compare `bin/fern-selfhost` to the
download byte for byte.
