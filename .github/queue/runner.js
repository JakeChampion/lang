'use strict';

// The I/O half of the CI admission queue (docs/CI-QUEUE.md): read the world,
// hand it to the pure decider in queue.js, then act on the answer.
//
// Called from .github/workflows/ci-queue.yml via actions/github-script, which
// supplies `github` (an authenticated Octokit), `context` and `core`.

const fs = require('fs');
const path = require('path');

const { decide, statusFor, STATUS_CONTEXT } = require('./queue.js');

// How many open PRs to consider. The queue is FIFO, so anything past this is
// waiting behind at least this many PRs anyway; the cap just keeps a runaway
// PR count from turning one decision into hundreds of API calls.
const MAX_PRS = 30;

function loadLanes(dir) {
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'lanes.json'), 'utf8'));
  return manifest.lanes;
}

/**
 * Every lane run that could matter to a decision:
 *   - every in-flight run repo-wide (answers "is the lane busy?")
 *   - every run for each queued PR's head SHA (answers "has this commit had
 *     CI?")
 * Deduped by id, since an in-flight run for a queued SHA appears in both.
 */
async function collectLaneRuns({ github, context, lanePaths, shas }) {
  const { owner, repo } = context.repo;
  const byId = new Map();
  const add = runs => {
    for (const run of runs) {
      if (lanePaths.has(run.path)) byId.set(run.id, run);
    }
  };

  for (const status of ['queued', 'in_progress']) {
    add(
      await github.paginate(github.rest.actions.listWorkflowRunsForRepo, {
        owner,
        repo,
        status,
        per_page: 100,
      }),
    );
  }
  for (const sha of shas) {
    add(
      await github.paginate(github.rest.actions.listWorkflowRunsForRepo, {
        owner,
        repo,
        head_sha: sha,
        per_page: 100,
      }),
    );
  }
  return [...byId.values()];
}

/**
 * Publish `ci-queue/admission` on a head SHA, but only when it would say
 * something new. Commit statuses are append-only and every write shows up in
 * the PR's checks list, so re-posting an identical status on each of a
 * decision's many triggers would bury the PR in noise.
 */
async function syncStatus({ github, context, core }, entry, targetUrl) {
  const { owner, repo } = context.repo;
  const want = statusFor(entry);
  const existing = await github.rest.repos.listCommitStatusesForRef({
    owner,
    repo,
    ref: entry.sha,
    per_page: 100,
  });
  const current = existing.data.find(s => s.context === STATUS_CONTEXT);
  if (current && current.state === want.state && current.description === want.description) {
    return false;
  }
  await github.rest.repos.createCommitStatus({
    owner,
    repo,
    sha: entry.sha,
    context: STATUS_CONTEXT,
    state: want.state,
    description: want.description,
    target_url: targetUrl,
  });
  core.info(`#${entry.number} ${entry.sha.slice(0, 12)}: ${want.state} — ${want.description}`);
  return true;
}

/**
 * Start every lane for one PR, on its head branch. Returns the lanes that
 * failed to start: one refusing (a bad workflow id, a revoked permission) must
 * not stop the other ten, or the PR gets a partial matrix AND no report of
 * why. The caller fails the job once the statuses are posted.
 */
async function dispatchLanes({ github, context, core }, lanes, entry) {
  const { owner, repo } = context.repo;
  const failed = [];
  for (const lane of lanes) {
    try {
      await github.rest.actions.createWorkflowDispatch({
        owner,
        repo,
        workflow_id: lane.file,
        ref: entry.ref,
      });
      core.info(`dispatched ${lane.file} on ${entry.ref} (#${entry.number})`);
    } catch (e) {
      failed.push(`${lane.file}: ${e.message}`);
    }
  }
  return failed;
}

/**
 * Reap lane runs testing a commit a push has already superseded. Best-effort:
 * a run that finished between the decision and the call cancels with a 409,
 * which is not a reason to abandon the rest of the decision.
 */
async function cancelStale({ github, context, core }, stale) {
  const { owner, repo } = context.repo;
  for (const run of stale) {
    try {
      await github.rest.actions.cancelWorkflowRun({ owner, repo, run_id: run.id });
      core.info(`cancelled superseded ${run.path} on ${run.head_branch} (${run.head_sha.slice(0, 12)})`);
    } catch (e) {
      core.warning(`could not cancel ${run.id}: ${e.message}`);
    }
  }
}

async function run({ github, context, core }) {
  const { owner, repo } = context.repo;
  const full = `${owner}/${repo}`;
  const lanes = loadLanes(__dirname);
  const lanePaths = new Set(lanes.map(l => `.github/workflows/${l.file}`));
  const targetUrl = `${context.serverUrl}/${full}/actions/runs/${context.runId}`;

  const prs = (
    await github.paginate(github.rest.pulls.list, {
      owner,
      repo,
      state: 'open',
      sort: 'created',
      direction: 'asc',
      per_page: 100,
    })
  ).slice(0, MAX_PRS);

  // A first pass over the PR list gives the SHAs worth asking about; the
  // decision then runs against the runs those SHAs actually have.
  const shas = [...new Set(prs.map(pr => pr.head.sha))];
  const runs = await collectLaneRuns({ github, context, lanePaths, shas });
  const { entries, unqueued, busy, next, stale } = decide({ prs, runs, repo: full });

  core.info(
    `${entries.length} PR(s) queued, ${unqueued.length} unqueued, lane is ${busy ? 'busy' : 'idle'}`,
  );

  await cancelStale({ github, context, core }, stale);

  let failed = [];
  if (next) {
    failed = await dispatchLanes({ github, context, core }, lanes, next);
    // Reflect the dispatch immediately: the runs won't be visible for a few
    // seconds, and leaving the status on "position 1" until the next decision
    // reads like nothing happened.
    next.state = 'running';
  }

  for (const entry of [...entries, ...unqueued]) {
    await syncStatus({ github, context, core }, entry, targetUrl);
  }

  const rows = entries.map(e => [
    `#${e.number}`,
    e.state === 'done' ? '—' : String(e.position),
    e.state,
    e.sha.slice(0, 12),
  ]);
  await core.summary
    .addHeading('CI queue', 3)
    .addRaw(
      (next
        ? `Dispatched full CI for **#${next.number}** (\`${next.ref}\`).`
        : busy
          ? 'Lane busy — nothing dispatched.'
          : 'Nothing waiting.') +
        (stale.length ? ` Cancelled ${stale.length} superseded run(s).` : ''),
    )
    .addTable([
      [
        { data: 'PR', header: true },
        { data: 'Position', header: true },
        { data: 'State', header: true },
        { data: 'Head', header: true },
      ],
      ...rows,
    ])
    .write();

  if (failed.length) {
    core.setFailed(
      `#${next.number} got a partial matrix — these lanes would not start:\n  ${failed.join('\n  ')}`,
    );
  }
}

module.exports = { run, loadLanes, collectLaneRuns, MAX_PRS };
