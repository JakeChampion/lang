'use strict';

// Pure decision logic for the CI admission queue (docs/CI-QUEUE.md).
//
// No network, no Actions context: everything here is a function of the open
// pull requests and the lane workflow runs that already exist. runner.js does
// the API calls and feeds the results in; queue.test.js exercises this file
// directly with plain objects.
//
// The queue is not stored anywhere. It is recomputed from the GitHub API every
// time a decision is needed, which is what makes it self-healing: a closed or
// merged PR simply stops appearing, and a new one joins the back by virtue of
// its creation time.

// Statuses of a lane run we treat as "the runner is busy with this SHA".
// Anything that is not `completed` counts — `queued` included, since a run
// waiting on a runner has already claimed the slot.
const RUN_ACTIVE = run => run.status !== 'completed';

// Conclusions that mean a completed run tested nothing, so the commit still
// needs CI. `cancelled` is here because a superseded or reaped run leaves the
// SHA unverified; `skipped` because that is what a same-repo `pull_request`
// run of a lane looks like (its gate job is fork-only — see docs/CI-QUEUE.md).
const NO_VERDICT = new Set(['cancelled', 'skipped']);

/** True when `run` came from a fork's pull request rather than a base-repo ref. */
function isForkRun(run, repo) {
  const head = run.head_repository && run.head_repository.full_name;
  return Boolean(head) && head !== repo;
}

/**
 * True when a lane run is one the queue owns or defers to:
 *
 *   workflow_dispatch — the queue dispatched it.
 *   pull_request from a fork — fork branches can't be dispatched onto a
 *     base-repo ref, so those lanes stay on the `pull_request` trigger and run
 *     unqueued. They still occupy the runners, so they still make us busy.
 *
 * A same-repo `pull_request` lane run is neither: it exists only because the
 * trigger is still there for the fork case, and every one of its jobs is
 * skipped. Counting it would wedge the queue — the PR's own no-op run would
 * read as "this SHA already had CI" and it would never be dispatched.
 */
function isQueueRun(run, repo) {
  if (run.event === 'workflow_dispatch') return true;
  return run.event === 'pull_request' && isForkRun(run, repo);
}

function isFork(pr, repo) {
  const head = pr.head && pr.head.repo && pr.head.repo.full_name;
  return Boolean(head) && head !== repo;
}

/** FIFO: oldest PR first, breaking ties on number so the order is total. */
function byAge(a, b) {
  const at = Date.parse(a.created_at);
  const bt = Date.parse(b.created_at);
  if (at !== bt) return at - bt;
  return a.number - b.number;
}

/**
 * decide({prs, runs, repo}) -> {entries, unqueued, busy, next}
 *
 * `prs`  — open pull requests (as returned by pulls.list).
 * `runs` — lane workflow runs: every in-flight one repo-wide, plus every run
 *          for the head SHA of each queued PR. Order and duplicates don't
 *          matter.
 * `repo` — "owner/name" of the base repository.
 *
 * `stale` is the in-flight lane runs testing a commit that is no longer the
 * head of the PR branch they belong to — a push superseded them. They're
 * cancelled: finishing a matrix for a commit nobody will merge is exactly the
 * waste the queue exists to remove. Matching on branch AND on being behind
 * that branch's PR keeps a hand-dispatched run on a non-PR branch safe.
 *
 * `entries` is the queue in FIFO order, each tagged:
 *   running — a lane run for this PR's head SHA is in flight
 *   done    — every lane run for this head SHA has finished (pass or fail —
 *             a red PR does not hold the queue)
 *   waiting — this head SHA has had no CI yet
 * and carries a 1-based `position` counting only the entries still to finish.
 *
 * `next` is the PR to dispatch right now, or null when the lane is busy or
 * nothing is waiting. It is always the front of the queue rather than "any
 * waiting PR", which is also what makes concurrent deciders safe: two runs
 * evaluating at the same instant pick the same PR, and the second dispatch is
 * suppressed by `busy` on the next pass.
 */
function decide({ prs, runs, repo }) {
  const owned = runs.filter(r => isQueueRun(r, repo));
  const active = owned.filter(RUN_ACTIVE);
  const activeShas = new Set(active.map(r => r.head_sha));
  const testedShas = new Set(
    owned.filter(r => !RUN_ACTIVE(r) && !NO_VERDICT.has(r.conclusion)).map(r => r.head_sha),
  );

  const open = prs.filter(pr => pr.state === 'open');
  const entries = open
    .filter(pr => !pr.draft && !isFork(pr, repo))
    .sort(byAge)
    .map(pr => ({
      number: pr.number,
      sha: pr.head.sha,
      ref: pr.head.ref,
      state: activeShas.has(pr.head.sha)
        ? 'running'
        : testedShas.has(pr.head.sha)
          ? 'done'
          : 'waiting',
    }));

  let position = 0;
  for (const e of entries) {
    if (e.state !== 'done') e.position = ++position;
  }

  // Same-repo PRs only: a fork's branch name lives in a different namespace
  // and could collide with one of ours, and fork runs are outside the queue's
  // remit anyway.
  const headOf = new Map(
    open.filter(pr => !isFork(pr, repo)).map(pr => [pr.head.ref, pr.head.sha]),
  );
  const stale = active.filter(
    r =>
      !isForkRun(r, repo) &&
      headOf.has(r.head_branch) &&
      headOf.get(r.head_branch) !== r.head_sha,
  );

  // Stale runs still count as busy. Cancelling is asynchronous, so dispatching
  // in the same pass would briefly double up on the runners; the cancellations
  // land as `workflow_run: completed` events, which wake the queue right back
  // up to dispatch properly.
  const busy = active.length > 0;
  return {
    entries,
    stale,
    // Fork PRs (and drafts) never join the queue; they get an admission status
    // saying so rather than being left pending forever.
    unqueued: open
      .filter(pr => pr.draft || isFork(pr, repo))
      .map(pr => ({
        number: pr.number,
        sha: pr.head.sha,
        ref: pr.head.ref,
        reason: isFork(pr, repo) ? 'fork' : 'draft',
      })),
    busy,
    next: busy ? null : entries.find(e => e.state === 'waiting') || null,
  };
}

// The commit status the queue publishes on each head SHA. It is the one signal
// that distinguishes "this PR's heavy lanes are absent because they are still
// queued" from "they passed": while a PR waits, its lane check runs are either
// missing or skipped, and a skipped check counts as a *pass* for branch
// protection. Requiring `ci-queue/admission` closes that hole.
const STATUS_CONTEXT = 'ci-queue/admission';

function statusFor(entry) {
  if (entry.reason === 'fork') {
    return { state: 'success', description: 'fork PR — CI runs unqueued' };
  }
  if (entry.reason === 'draft') {
    return { state: 'success', description: 'draft — not queued for full CI' };
  }
  switch (entry.state) {
    case 'running':
      return { state: 'pending', description: 'full CI running' };
    case 'done':
      return { state: 'success', description: 'full CI ran for this commit' };
    default:
      return {
        state: 'pending',
        description: `waiting for full CI — position ${entry.position} in queue`,
      };
  }
}

module.exports = { decide, statusFor, isQueueRun, isForkRun, isFork, byAge, STATUS_CONTEXT };
