'use strict';

// node --test .github/queue
//
// Unit tests for the pure queue decider. internal/ciqueue runs this file as
// part of `go test`, so a broken queue fails the normal Go suite too.

const test = require('node:test');
const assert = require('node:assert');

const { decide, statusFor, STATUS_CONTEXT } = require('./queue.js');

const REPO = 'jakechampion/lang';
const LANE = '.github/workflows/test-e2e-x86_64.yml';

let clock = 0;
function pr(number, sha, opts = {}) {
  clock += 1000;
  return {
    number,
    state: 'open',
    draft: false,
    created_at: new Date(opts.createdAt ?? clock).toISOString(),
    head: {
      sha,
      ref: opts.ref ?? `feature-${number}`,
      repo: { full_name: opts.forkOf ?? REPO },
    },
    ...opts.extra,
  };
}

function run(sha, opts = {}) {
  return {
    id: opts.id ?? Math.floor(Math.random() * 1e9),
    path: opts.path ?? LANE,
    event: opts.event ?? 'workflow_dispatch',
    status: opts.status ?? 'completed',
    conclusion: opts.conclusion ?? 'success',
    head_sha: sha,
    head_branch: opts.branch,
    head_repository: { full_name: opts.headRepo ?? REPO },
  };
}

const at = (result, number) => result.entries.find(e => e.number === number);

test('an idle lane dispatches the oldest open PR', () => {
  const d = decide({
    prs: [pr(101, 'bbb'), pr(100, 'aaa', { createdAt: 1 })],
    runs: [],
    repo: REPO,
  });
  assert.deepEqual(
    d.entries.map(e => e.number),
    [100, 101],
  );
  assert.equal(d.busy, false);
  assert.equal(d.next.number, 100);
  assert.equal(d.next.ref, 'feature-100');
  assert.equal(at(d, 100).position, 1);
  assert.equal(at(d, 101).position, 2);
});

test('a PR with CI in flight makes the lane busy and blocks the rest', () => {
  const d = decide({
    prs: [pr(100, 'aaa', { createdAt: 1 }), pr(101, 'bbb', { createdAt: 2 })],
    runs: [run('aaa', { status: 'in_progress', conclusion: null })],
    repo: REPO,
  });
  assert.equal(d.busy, true);
  assert.equal(d.next, null);
  assert.equal(at(d, 100).state, 'running');
  assert.equal(at(d, 101).state, 'waiting');
});

test('a queued (not yet started) run still counts as busy', () => {
  const d = decide({
    prs: [pr(100, 'aaa')],
    runs: [run('aaa', { status: 'queued', conclusion: null })],
    repo: REPO,
  });
  assert.equal(d.busy, true);
  assert.equal(d.next, null);
});

test('a finished PR yields to the next one — even when it failed', () => {
  const d = decide({
    prs: [pr(100, 'aaa', { createdAt: 1 }), pr(101, 'bbb', { createdAt: 2 })],
    runs: [run('aaa', { conclusion: 'failure' })],
    repo: REPO,
  });
  assert.equal(at(d, 100).state, 'done');
  assert.equal(at(d, 100).position, undefined);
  assert.equal(at(d, 101).position, 1, 'finished PRs drop out of the positions');
  assert.equal(d.next.number, 101);
});

test('a push to a tested PR re-queues it at its original place', () => {
  const first = [pr(100, 'aaa', { createdAt: 1 }), pr(101, 'bbb', { createdAt: 2 })];
  const runs = [run('aaa', { conclusion: 'success' })];
  // #100 is pushed to: same PR, new head SHA, no CI for it yet.
  const pushed = [{ ...first[0], head: { ...first[0].head, sha: 'aaa2' } }, first[1]];
  const d = decide({ prs: pushed, runs, repo: REPO });
  assert.equal(at(d, 100).state, 'waiting');
  assert.equal(d.next.number, 100, 'still the oldest PR, so still the front');
});

test('drafts and forks are not in the queue', () => {
  const d = decide({
    prs: [
      pr(100, 'aaa', { createdAt: 1, extra: { draft: true } }),
      pr(101, 'bbb', { createdAt: 2, forkOf: 'someone/lang' }),
      pr(102, 'ccc', { createdAt: 3 }),
    ],
    runs: [],
    repo: REPO,
  });
  assert.deepEqual(
    d.entries.map(e => e.number),
    [102],
  );
  assert.deepEqual(
    d.unqueued.map(e => [e.number, e.reason]),
    [
      [100, 'draft'],
      [101, 'fork'],
    ],
  );
  assert.equal(d.next.number, 102);
});

test("a fork's unqueued run occupies the lane like any other", () => {
  const d = decide({
    prs: [pr(100, 'aaa')],
    runs: [
      run('fff', {
        event: 'pull_request',
        status: 'in_progress',
        conclusion: null,
        headRepo: 'someone/lang',
      }),
    ],
    repo: REPO,
  });
  assert.equal(d.busy, true);
  assert.equal(d.next, null);
});

test('a same-repo pull_request lane run is ignored entirely', () => {
  // These exist only because the lanes keep a `pull_request` trigger for
  // forks; every job in them is skipped. If they counted, the PR's own no-op
  // run would look like "this SHA already had CI" and it would never run.
  const d = decide({
    prs: [pr(100, 'aaa')],
    runs: [
      run('aaa', { event: 'pull_request', status: 'queued', conclusion: null }),
      run('aaa', { event: 'pull_request', conclusion: 'skipped' }),
    ],
    repo: REPO,
  });
  assert.equal(d.busy, false);
  assert.equal(at(d, 100).state, 'waiting');
  assert.equal(d.next.number, 100);
});

test('a cancelled run leaves the commit untested', () => {
  const d = decide({
    prs: [pr(100, 'aaa')],
    runs: [run('aaa', { conclusion: 'cancelled' })],
    repo: REPO,
  });
  assert.equal(at(d, 100).state, 'waiting');
  assert.equal(d.next.number, 100);
});

test('runs from workflows outside the lane set never reach the decider', () => {
  // runner.js filters by lane path before calling decide(); this pins the
  // consequence of that filter — a Lint run must not make the lane look busy.
  const d = decide({
    prs: [pr(100, 'aaa')],
    runs: [],
    repo: REPO,
  });
  assert.equal(d.busy, false);
});

test('closed PRs drop out of the queue', () => {
  const closed = pr(100, 'aaa', { createdAt: 1 });
  closed.state = 'closed';
  const d = decide({ prs: [closed, pr(101, 'bbb', { createdAt: 2 })], runs: [], repo: REPO });
  assert.deepEqual(
    d.entries.map(e => e.number),
    [101],
  );
  assert.equal(d.next.number, 101);
});

test('PRs created in the same second order by number', () => {
  const d = decide({
    prs: [pr(102, 'ccc', { createdAt: 5 }), pr(101, 'bbb', { createdAt: 5 })],
    runs: [],
    repo: REPO,
  });
  assert.deepEqual(
    d.entries.map(e => e.number),
    [101, 102],
  );
});

test('a run superseded by a push to its own branch is cancelled', () => {
  const d = decide({
    prs: [pr(100, 'aaa2', { ref: 'feature-100' })],
    runs: [
      run('aaa', {
        id: 7,
        branch: 'feature-100',
        status: 'in_progress',
        conclusion: null,
      }),
    ],
    repo: REPO,
  });
  assert.deepEqual(
    d.stale.map(r => r.id),
    [7],
  );
  assert.equal(d.busy, true, 'stay busy until the cancellation lands');
  assert.equal(d.next, null);
});

test('a run on the current head is not stale', () => {
  const d = decide({
    prs: [pr(100, 'aaa', { ref: 'feature-100' })],
    runs: [run('aaa', { branch: 'feature-100', status: 'in_progress', conclusion: null })],
    repo: REPO,
  });
  assert.deepEqual(d.stale, []);
});

test('a run on a branch with no open PR is left alone', () => {
  // A maintainer dispatching a lane by hand on a scratch branch must not have
  // it reaped out from under them.
  const d = decide({
    prs: [pr(100, 'aaa', { ref: 'feature-100' })],
    runs: [run('zzz', { branch: 'scratch', status: 'in_progress', conclusion: null })],
    repo: REPO,
  });
  assert.deepEqual(d.stale, []);
  assert.equal(d.busy, true);
});

test("a fork's run is never reaped as stale, even on a colliding branch name", () => {
  const d = decide({
    prs: [pr(100, 'aaa2', { ref: 'feature-100' })],
    runs: [
      run('fff', {
        event: 'pull_request',
        branch: 'feature-100',
        status: 'in_progress',
        conclusion: null,
        headRepo: 'someone/lang',
      }),
    ],
    repo: REPO,
  });
  assert.deepEqual(d.stale, [], 'branch names in a fork are a different namespace');
  assert.equal(d.busy, true);
});

test('admission statuses describe each queue state', () => {
  assert.equal(STATUS_CONTEXT, 'ci-queue/admission');
  assert.deepEqual(statusFor({ state: 'waiting', position: 3 }), {
    state: 'pending',
    description: 'waiting for full CI — position 3 in queue',
  });
  assert.equal(statusFor({ state: 'running' }).state, 'pending');
  assert.equal(statusFor({ state: 'done' }).state, 'success');
  assert.equal(statusFor({ reason: 'fork' }).state, 'success');
  assert.equal(statusFor({ reason: 'draft' }).state, 'success');
});
