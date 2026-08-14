# Local signoff of CI lanes

Run a lane on your machine, record it against the pushed commit, and let CI skip
that lane. `scripts/signoff` posts the record; `.github/actions/signoff-gate`
reads it.

```
scripts/signoff --list
scripts/signoff macos-arm64
scripts/signoff wasm --dry-run     # run and check, post nothing
```

## Why

75% of a PR's CI time here is spent waiting for a runner rather than using one:
945 job-minutes queued against 317 running, measured on run 31819148053. The
pool is shared across every SHA in flight, so a lane you ran locally and told CI
about hands a slot back to every other lane. That is the whole benefit — it is a
queueing win, not a "tests are slow" win.

## What may be signed off

A lane qualifies **only when a local pass means what a CI pass means.** The
signable set is deliberately small:

| lane | why it is faithful |
|---|---|
| `lint` | host-independent whole-repo gates |
| `units` | pure-Go packages |
| `wasm` | wasm executes host-independently, and the signoff refuses unless the wasmtime version matches the pin exactly. **Needs an x86-64 host**: 12 of the tests matching `TestWasm*` drive the x86-64 backend and skip without `qemu-x86_64`, where CI runs them on an x86_64 runner. On a Mac this lane refuses, correctly — signing it off here would claim 88 tests ran when 76 did |
| `macos-arm64` | this machine IS the CI lane's platform (Apple Silicon macOS); covers both the `internal/e2e` and `internal/e2eselfhost` native-execution steps |

Expect a modest saving, and do not oversell it: these lanes are ~19 of the 317
job-minutes a PR spends running. The reason to bother is the queue, not the
compute. Four to six fewer jobs contending for the pool is worth more than the
minutes suggest, because the pool is what is scarce.

**The x86-64 and arm64 Linux ELF lanes are not signable from a Mac.** They run
only under qemu here (`scripts/devbox`), and `docs/LOCAL-DEV-LOOP.md` is
explicit that qemu locally is for debugging a failure CI surfaced, never a gate.
Let CI run them.

Do not widen the table because a lane is slow and annoying. Widen it only when
you can say what makes the local run faithful. A signoff on a lane you cannot
reproduce manufactures precisely the vacuous green this project keeps digging
out: #6849 (five suites that could not fail), #6310 (a lane that ran 15 of the
17 tests it listed and reported green), #5914.

## What the signoff refuses

`scripts/signoff` will not post when:

- the working tree is dirty, or `HEAD` is not what `origin/<branch>` points at.
  A signoff names one commit; if what ran here is not what was pushed, it means
  nothing;
- the lane exits non-zero, or any `--- FAIL` appears;
- the lane produces fewer `--- PASS` than its floor. `go test -run` exits 0 when
  its selector matches **nothing**, so exit status alone cannot tell "all green"
  from "ran zero tests" — that is #6310's shape, and a signoff stands in for a
  CI run, so it has to know the difference;
- the lane produces more `--- SKIP` than it declares. The rule is **not** zero
  skips, it is *the same skips CI has*: `macos-15` has no qemu either, so that
  lane's cross-arch cases skip on the runner exactly as they do here. An extra
  skip is a missing dependency; the declared count going stale fails loudly,
  which is wanted, because a change in what skips is something to look at;
- for `wasm`, the host `wasmtime` is not the pinned version, or
  `FERN_WASI_ADAPTER` is unset or missing. Every wasm test skips on a failed
  tool lookup, so without this check the lane would sign off having run nothing.

A signoff is scoped to one SHA. Push a new commit and it needs a new signoff —
there is no way to sign off a branch.

## Wiring a lane into a workflow

The gate reports; it cannot end its own job (a composite action can't), so guard
the steps:

```yaml
- uses: ./.github/actions/signoff-gate
  id: gate
  with:
    lane: macos-arm64

- name: go test (arm64-darwin native execution)
  if: steps.gate.outputs.signed != 'true'
  run: ...
```

The job still starts and takes a runner slot, but releases it in seconds instead
of minutes, which is what the queue actually cares about. Guarding at the *job*
level via `needs:` would be worse: it adds a dependency hop, and a job that waits
on another job queues twice — the mistake that had `permodule-fixpoint` starting
34 minutes into a run.

The gate reads `github.event.pull_request.head.sha` on a `pull_request` event,
not `github.sha`, because the latter is the ephemeral merge commit that nobody
can sign off locally.

## Trust model

A signoff is a commit status, so anyone with write access to the repo can post
one, with or without running anything. This is a convenience for the people who
already control the branch, not a security boundary. It is worth saying plainly:
**a signed-off lane did not run in CI**, and the only thing standing behind it is
whoever ran `scripts/signoff`.

Two consequences:

- Do not sign off a lane you did not watch pass.
- Branch protection requires no named checks on this repo today, so a signoff
  gates nothing by itself. If required checks are ever turned on, decide
  deliberately whether `signoff/*` contexts count.

## Getting more lanes runnable locally

`scripts/devbox` builds a Linux container with the toolchain a Mac cannot host:
`qemu-x86_64`, `qemu-aarch64`, the cross compilers, and the pinned wasm tools.
It makes those lanes *runnable* for debugging. It does not make them
*signable* — see the table above.
