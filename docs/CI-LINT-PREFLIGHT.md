# Lint feedback outside the full-suite queue

The root CI workflow previously acquired the repository's PR queue before
starting any job. A PR waiting behind other full suites therefore received
no lint feedback. PR #9359 exhibited this while queued behind #9357 and
#9358: no non-Netlify checks had started. Empty check lists must not be
interpreted as successful validation.

## Scheduling

The root workflow now starts lint and changed-lane selection immediately.
Changed-lane selection calls a reusable full-suite workflow with the exact
lane map. That workflow holds the existing PR queue while its selected
lanes execute. Main keeps its existing outer latest-pending queue.

The suite depends on lane selection only. Lint and test lanes retain their
independent execution semantics, including on main. The existing lint
failure reaper still reads live PR labels before deciding whether to cancel
the run, so adding `ci-full` after a run starts still works. Fork permission
handling is unchanged. Reapers for other lanes move with those lanes.

The PR's required `Lint / lint` check keeps its name and runs unconditionally.
Heavy checks acquire a `Full suite /` prefix. The main failure reporter
removes that prefix so existing lane issue identities and Bootstrap hints
remain stable, including when older workflow runs finish during rollout.
The missing-shard classifier matches the leaf job name beneath reusable
workflow prefixes and retains the full name in its diagnosis. Regression
cases cover direct, called, nested and custom-prefix jobs, and reject an
unrelated leaf whose caller happens to have a shard-like name.

The suite receives inherited secrets and explicit permissions sufficient for
its existing write-capable lanes. Ordinary jobs retain read-only defaults.
All explicit lane path allowlists include the new workflow file, so editing
the suite schedules every lane. No leaf test workflow or test command changes.

This removes the full-suite concurrency lock from lint's dependency path.
Lint still competes for runner capacity and has its own execution cost.
It does not guarantee immediate feedback under runner saturation. Running
lint while another suite is active may consume capacity that suite could
otherwise use; rollout measurements must capture that tradeoff.

## Validation

- The source guard suite checks complete lane coverage, queue ownership,
  dependencies, required lint identity, reapers and permissions.
- Execution of the real lane-selection script passes 31 scenarios, including
  an isolated edit to the new suite file selecting every lane.
- Execution of the real failure reporter passes 28 scenarios, covering old
  and nested check names, persistent issue identity and Bootstrap hints.
- A parsed comparison against the previous workflow confirms all 16 heavy
  lane callers and 16 reapers are unchanged except for passing the lane map
  through the new boundary. Root lint and its reaper are identical.
- The call graph has four levels including the main caller, 20 unique
  reusable workflows and no cycles. This is within GitHub's documented
  [reusable workflow limits](https://docs.github.com/en/actions/reference/workflows-and-actions/reusing-workflow-configurations).
- Full source lint, package vet and pinned actionlint pass locally. GitHub
  execution remains necessary to validate the new scheduling boundary.

## Rollout measurements

Keep the existing full-suite queue policy for this change. Record workflow
creation, lint start and completion, first full-suite job start, final suite
completion, and summed runner execution time. Separate waiting time from
execution time. Compare both quiet and contended periods, with runner type,
changed lanes, cache state and revision recorded alongside each run.

Verify that a waiting PR can finish lint while the preceding suite runs;
that heavy PR suites retain their mutual exclusion; and that stale-run
cancellation, live `ci-full` handling, main reporting and required-check
names still work. Measure full-suite slowdown and runner consumption as
well as time saved before lint feedback. No numerical speedup is claimed
until actual GitHub runs provide that evidence.
