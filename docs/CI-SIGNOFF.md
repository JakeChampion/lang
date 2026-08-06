# Local CI sign-off

Fern's CI lanes support an opt-in **local sign-off** escape hatch built on
Basecamp's [`gh-signoff`](https://github.com/basecamp/gh-signoff): if you've
already run a suite locally on the exact commit you're pushing, you can sign off
on it and CI will **skip that lane** instead of re-running it on a runner.

This is per-lane and per-commit. You skip only what you sign off, and only for
the one commit you signed — push anything new and the skip evaporates.

## TL;DR

```sh
# one-time
gh extension install basecamp/gh-signoff

# after running the suites locally, on the commit you're about to push:
scripts/signoff --local          # sign off every locally-verifiable lane
# or pick lanes:
scripts/signoff units e2e-x86_64 e2e-wasm
git push
```

CI sees the sign-off statuses on your head commit and skips those lanes. The
`pr-status-comment` rollup still goes green because a skipped job counts as a
pass.

## How it works

`gh signoff <step>` posts a GitHub **commit status** with context
`signoff/<step>` and state `success` onto your current HEAD commit. Each gated
workflow starts with a tiny `gate` job (`.github/actions/signoff-gate`) that
reads the statuses on the PR head commit; if `signoff/<step>` is present and
green, the gate outputs `skip=true` and the heavy job is skipped via
`if: needs.gate.outputs.skip != 'true'`.

A few properties fall out of using commit statuses for this:

- **Pinned to the commit.** Statuses attach to a SHA. Push a new commit (or
  amend) and the old sign-off doesn't carry over, so changed code always gets
  fresh CI until you re-sign. This is also the only "undo": commit statuses
  can't be deleted, so a new commit is how you clear a sign-off.
- **Collaborators only.** Only someone with push access to the base repo can
  create a status on its commits. **Fork PRs can't sign off**, so CI always runs
  in full for outside contributors — the skip is a maintainer convenience, not a
  hole.
- **Fails open.** If the gate's API call hiccups, it defaults to `skip=false`
  and the lane runs. A sign-off can only ever *remove* work; it can never
  silently hide a regression by failing closed.

## Steps → lanes

| step                | CI lane (workflow)            | run locally with |
| ------------------- | ----------------------------- | ---------------- |
| `units`             | Test units                    | `go test ./internal/...` (unit pkgs) |
| `lint`              | Lint (vet/build/fmt/gofmt/deadcode/actionlint) | `make vet && go build ./... && make fmt-check && make gofmt-check && make deadcode && make actionlint` |
| `examples`          | Examples                      | `make examples` |
| `fernsmith`         | Test fernsmith                | `go test ./internal/fernsmith/...` |
| `e2e-x86_64`        | Test e2e x86_64               | `go test -run '^TestX86_64' ./internal/e2e/` |
| `e2e-wasm`          | Test e2e wasm                 | `go test -run '^Test(WASM\|Wasm)' ./internal/e2e/` (needs wasmtime + `FERN_WASI_ADAPTER`) |
| `e2e-other`         | Test e2e other                | `go test -skip '^Test(Arm64\|X86_64\|WASM\|Wasm\|SelfHost\|Differential)' ./internal/e2e/` |
| `e2e-differential`  | Test e2e differential         | `go test -run '^TestDifferential' ./internal/e2e/` |
| `e2e-selfhost`      | Test e2e self-host            | `go test -run '^TestSelfHost' ./internal/e2eselfhost/ ./internal/e2e/` (the residual mixed-fixture tests live in `internal/e2e`) |
| `fuzz-parse`        | Fuzz parse round-trip         | `go test -fuzz=FuzzGenerate_ParseRoundTrips -fuzztime=60s -run='^$' ./internal/fernsmith` |
| `fuzz-diff`         | Fuzz differential execution   | `go test -fuzz=FuzzGenerate_ExecutionAgrees -fuzztime=60s -run='^$' ./internal/e2e` |
| `e2e-arm64`         | Test e2e arm64                | **discouraged** — see below |

`scripts/signoff --list` prints this mapping; `scripts/signoff --local` signs
off everything except `e2e-arm64`.

The path-filtered workflows (`macOS arm64`, `Playground E2E`, `VS Code
Extension`, `Docs build`) and the deploy/meta workflows are **not** gated: they
already only run when their files change, so there's little to skip.

### Don't sign off `e2e-arm64` unless you really ran it on arm64

Project guidance (`CLAUDE.md`) is to gate locally on the **x86-64 + wasm**
equivalents and let CI run the arm64 matrix — running the aarch64 e2e suite
under qemu on a dev box is the slow part of a local sweep. So `e2e-arm64` is
gated for completeness but excluded from `--local`. Only sign it off if you
actually ran the suite on arm64 hardware (an Apple Silicon Mac, a Graviton box,
etc.). The two backends share their entire frontend, so an x86-64-green change
is almost always arm64-green — but "almost always" is why CI runs it by default.

## The trust model, plainly

A sign-off is you telling CI "I ran this and it passed." CI believes you. That's
the whole point, and it's appropriate for a small repo with trusted committers —
it's faster than waiting on runners for changes you've already validated. It is
**not** a substitute for actually running the suite. The engineering bar still
applies: only sign off a lane you ran and saw pass on this commit.
