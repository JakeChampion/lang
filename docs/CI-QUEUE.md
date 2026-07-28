# CI admission queue

The heavy CI lanes are ~90 jobs between them (two architectures × e2e shards ×
self-host bootstrap × fuzzers). Running that set for every open pull request at
once doesn't make anything finish sooner — the runners are a fixed pool, so
every PR just waits behind every other PR's matrix, and a superseded commit
burns a full matrix before anyone notices it was superseded.

The **CI queue** puts exactly one PR's worth of heavy lanes on the runners at a
time, in FIFO order, and lets the fast lane (`Lint`) run immediately for
everyone.

```
pull_request ──►  ci-queue.yml  ──► front of queue? ──yes──► dispatch 11 lanes
                       ▲                    │
                       │                    └──no──► post "position N", exit
   lane finished ──────┤
   every 15 min ───────┘
```

## The queue is not stored anywhere

There is no database, no issue comment holding state, no lock file. The queue
is recomputed from the GitHub API every time a decision is needed:

> **the open, non-draft, same-repo pull requests, oldest first.**

That definition is what makes the whole thing self-healing. A merged or closed
PR stops appearing. A new PR joins the back by virtue of its creation time. A
decider that crashes leaves nothing to clean up, because it wrote nothing down.

Each queued PR is in one of three states, derived from the lane runs that exist
for its **head SHA**:

| state     | meaning                                                     |
| --------- | ----------------------------------------------------------- |
| `running` | a lane run for this head SHA is queued or in progress        |
| `done`    | lanes ran for this head SHA and finished — pass **or** fail  |
| `waiting` | this head SHA has had no CI yet                              |

A failing PR is `done`: the queue moves on. Whether to merge, fix or abandon it
is a human decision, not the scheduler's.

## Moving parts

| file                             | role                                                      |
| -------------------------------- | --------------------------------------------------------- |
| `.github/queue/lanes.json`       | which workflows the queue serialises                       |
| `.github/queue/queue.js`         | the decision — pure, no I/O                                |
| `.github/queue/runner.js`        | the API calls either side of the decision                  |
| `.github/queue/queue.test.js`    | unit tests for the decision (`node --test`)                |
| `.github/workflows/ci-queue.yml` | the triggers, permissions, and serialisation               |
| `internal/sourcelint`            | `TestCIQueue*` — keeps the manifest and workflows in sync  |

### Triggers

All four funnel into the same decision, which is idempotent:

- **`pull_request`** — a PR joined, changed, or left the queue.
- **`workflow_run: completed`** on every lane — the runners may now be free.
- **`schedule` (`*/15`)** — the safety net. `workflow_run` is best-effort and
  the `concurrency` group keeps only one pending run, so a decision *can* be
  dropped. Without the sweep a dropped event would wedge the queue until the
  next push; with it, the worst case is 15 minutes of idle runners.
- **`workflow_dispatch`** — kick it by hand.

Decisions are serialised with `concurrency: {group: ci-queue,
cancel-in-progress: false}`. Two deciders reading an idle lane at the same
instant would both dispatch; one at a time can't. Duplicates piling up behind
the running one are harmless — they all ask the same question, and GitHub keeps
the newest pending run, which is the one that sees the freshest state.

### The lanes

The 11 lanes in `lanes.json` are triggered by `workflow_dispatch` on the PR's
head branch. They keep a `pull_request:` trigger too, but their `gate` job runs
it for **fork PRs only**:

```yaml
if: >-
  github.event_name != 'pull_request'
  || github.event.pull_request.head.repo.full_name != github.repository
```

Every other job in those workflows hangs off `needs: gate`, so for a same-repo
PR the whole workflow skips in seconds without touching a runner.

Two consequences of dispatching rather than triggering on `pull_request`:

- **Lanes test the branch head, not the `refs/pull/N/merge` commit.** A PR that
  is green but stale against `main` is possible; that is what the queue's FIFO
  ordering and the usual "merge main in" flow are for.
- **`paths:` filters would stop applying.** They are ignored on
  `workflow_dispatch`, which is exactly why the path-filtered workflows
  (`docs-build`, `macos`, `playground-e2e`, `vscode-extension`) are *not*
  lanes: queueing them would make them run on every front-of-queue PR instead
  of only the relevant ones. `internal/sourcelint` rejects a path-filtered lane
  for this reason.

`Lint` is deliberately unqueued. A missing `gofmt` should come back in a minute,
not after a self-host bootstrap.

## Superseded commits

Push to a PR whose matrix is already running and the in-flight runs are testing
a commit nobody will merge. The queue reaps them: any in-flight lane run whose
`head_branch` belongs to an open same-repo PR but whose `head_sha` is no longer
that PR's head is cancelled. A run on a branch with no open PR — a lane you
dispatched by hand to debug something — doesn't match, and is left alone.

The cancelled runs stay counted as busy for that pass. Cancelling is
asynchronous, so dispatching immediately would briefly double up on the
runners; the cancellations arrive as `workflow_run: completed` events, which
wake the queue right back up to dispatch the new head properly.

This is the queue's version of what the lanes' per-ref `concurrency` groups do
for direct `pull_request` runs, and it complements `cancel-on-merge.yml`, which
reaps the runs of a PR that has been closed entirely.

## `ci-queue/admission`

The queue publishes one commit status on each open PR's head SHA:

| state     | description                                    |
| --------- | ---------------------------------------------- |
| `pending` | `waiting for full CI — position N in queue`     |
| `pending` | `full CI running`                              |
| `success` | `full CI ran for this commit`                  |
| `success` | `fork PR — CI runs unqueued`                   |
| `success` | `draft — not queued for full CI`               |

It exists for one specific reason beyond visibility. While a PR waits, its lane
check runs are *absent* — and where they do appear (the skipped fork-fallback
runs), **a skipped check counts as a pass for branch protection**. Nothing else
on the PR distinguishes "these lanes haven't run yet" from "these lanes
passed". `ci-queue/admission` does, and it is `pending` for exactly as long as
that is true.

**If you enable branch protection on `main`, make `ci-queue/admission` a
required check.** It is the one that can't be satisfied by a PR that hasn't
been through the queue. `pr-status-comment` already picks it up for free — it
counts commit statuses, so a queued PR reports ⏳ rather than ✅.

## Forks

A fork's branch doesn't exist in the base repo, so `workflow_dispatch` can't
target it. Fork PRs therefore **stay on the `pull_request` trigger and run
unqueued**, as they did before the queue. They are still visible to the queue
in one direction: an in-flight fork run makes the lane busy, so the queue won't
stack a second matrix on top of it.

The alternative — pushing fork code to a base-repo branch so it can be
dispatched — would hand a fork PR a base-repo token, and is not worth it here.
Closing the gap properly means passing the PR number into each lane and having
every `actions/checkout` take `ref: refs/pull/N/merge`.

## Working on the queue

```sh
node --test .github/queue/queue.test.js   # the decision logic
go test ./internal/sourcelint/            # manifest ↔ workflow consistency
```

`TestCIQueueDecider` runs the node tests as part of the Go suite, so the normal
`go test` gate covers both.

Adding a workflow that runs on every PR without adding it to `lanes.json` (or
giving it a `paths:`/`types:` filter) fails `TestCIQueueCoversEveryHeavyLane` —
that's the forcing function against silently re-creating the pile-up.

Two things to know when changing this:

- `workflow_run` and `schedule` triggers only fire for the copy of a workflow on
  the **default branch**. Changes to them are not exercised by their own PR.
- Runs dispatched with `GITHUB_TOKEN` do start (`workflow_dispatch` is one of
  the two documented exceptions to Actions' recursion prevention), but if
  `workflow_run` ever stops firing for them the scheduled sweep is what keeps
  the queue moving. Don't remove it.

## Interaction with local sign-off

[`docs/CI-SIGNOFF.md`](CI-SIGNOFF.md) is unaffected: a signed-off lane still
gets dispatched, and its `gate` job still skips the work in a few seconds. Sign
off everything and your PR passes through the front of the queue almost
instantly — the queue costs you nothing you hadn't already paid locally.
