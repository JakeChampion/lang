#!/usr/bin/env node
// Execute the reporter's actual script with an in-memory GitHub API. No
// checkout or network access is needed in the privileged reporting workflow.
import assert from "node:assert/strict";
import fs from "node:fs";

const source = fs.readFileSync(new URL("../.github/workflows/main-red.yml", import.meta.url), "utf8");
const script = source.split("          script: |\n")[1];
assert.ok(script, "main-red.yml must contain the reporter script");
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const report = new AsyncFunction("github", "context", "core", script);

async function run({ name = "CI main", jobs, issues = [], annotations = [] }) {
  const calls = [];
  const github = {
    rest: {
      actions: { listJobsForWorkflowRun: "jobs" },
      checks: { listAnnotations: "annotations" },
      issues: {
        listForRepo: "issues",
        createComment: async (args) => calls.push(["comment", args]),
        update: async (args) => calls.push(["update", args]),
        createLabel: async (args) => calls.push(["label", args]),
        create: async (args) => { calls.push(["create", args]); return { data: { number: 9 } }; },
      },
    },
    paginate: async (api, args) => {
      assert.ok(["jobs", "issues", "annotations"].includes(api), `unexpected listing: ${api}`);
      if (api === "annotations") {
        assert.equal(args.check_run_id, 42, "annotations must be read off the changes job");
        if (annotations instanceof Error) throw annotations;
        return annotations;
      }
      return api === "jobs" ? jobs : issues;
    },
  };
  const context = {
    serverUrl: "https://example.test",
    repo: { owner: "o", repo: "r" },
    payload: { workflow_run: { id: 1, name, head_sha: "a".repeat(40), html_url: "https://example.test/run" } },
  };
  await report(github, context, { notice() {}, warning() {} });
  return calls;
}

const issue = { number: 7, title: "main is red: Test units" };
let cases = 0;
for (const [name, prefix] of [["CI", ""], ["CI main", "Validate / "], ["CI main", "Validate / Full suite / "]]) {
  const job = (lane, conclusion) => ({ name: prefix + lane, conclusion });
  let calls = await run({ name, jobs: [job("Test units / x86", "failure")] });
  assert.equal(calls.find(([kind]) => kind === "create")[1].title, issue.title);
  cases++;

  calls = await run({ name, jobs: [job("Test units / x86", "failure")], issues: [issue] });
  assert.deepEqual(calls.map(([kind]) => kind), ["comment"]);
  assert.equal(calls[0][1].issue_number, issue.number);
  cases++;

  calls = await run({ name, jobs: [job("Test units / x86", "success")], issues: [issue] });
  assert.deepEqual(calls.map(([kind]) => kind), ["comment", "update"]);
  assert.deepEqual([calls[1][1].issue_number, calls[1][1].state], [7, "closed"]);
  cases++;

  for (const conclusions of [["skipped"], ["cancelled"], ["success", "cancelled"]]) {
    calls = await run({ name, jobs: conclusions.map((c, i) => job(`Test units / ${i}`, c)), issues: [issue] });
    assert.deepEqual(calls, [], `${name}: inconclusive jobs must not close the issue`);
    cases++;
  }

  calls = await run({ name, jobs: [job("Test units / x86", "failure"), job("Test units / arm", "success")], issues: [issue] });
  assert.deepEqual(calls.map(([kind]) => kind), ["comment"], "a successful sibling must not hide failure");
  cases++;

  calls = await run({ name, jobs: [job("Bootstrap / stage1", "failure")] });
  assert.match(calls.find(([kind]) => kind === "create")[1].body, /stage0 pin going stale/);
  cases++;

  calls = await run({ name, jobs: [job("changes", "failure"), job("reap-lint", "skipped")] });
  assert.deepEqual(calls.filter(([kind]) => kind === "create").map(([, args]) => args.title), ["main is red: changes"]);
  cases++;
}
assert.deepEqual(await run({ jobs: [], issues: [issue] }), [], "replaced pending runs have no verdict");
cases++;

// A lane main skipped because the merged PR's run passed it at the same tree.
const changes = { id: 42, name: "Validate / changes", conclusion: "success" };
const skipped = (lane) => ({ name: `Validate / Full suite / ${lane} / x86`, conclusion: "skipped" });
const proof = (lanes) => [{ title: "Lanes proven by the merged pull request", message: JSON.stringify({ pr: 5, run: 6, lanes }) }];

let calls = await run({ jobs: [changes, skipped("Test units")], issues: [issue], annotations: proof(["Test units"]) });
assert.deepEqual(calls.map(([kind]) => kind), ["comment", "update"], "a lane the merged PR proved closes its issue");
assert.match(calls[0][1].body, /#5's \[run\]\(https:\/\/example\.test\/o\/r\/actions\/runs\/6\)/);
assert.equal(calls[1][1].state, "closed");
cases++;

calls = await run({ jobs: [changes, skipped("Test units")], issues: [issue], annotations: proof(["Bootstrap"]) });
assert.deepEqual(calls, [], "a proof for another lane must not close this one");
cases++;

calls = await run({ jobs: [changes, skipped("Test units")], issues: [issue], annotations: [{ title: "other", message: "{}" }] });
assert.deepEqual(calls, [], "a skip with no proof is no verdict");
cases++;

calls = await run({
  jobs: [changes, skipped("Test units"), { name: "Validate / Full suite / Test units / arm", conclusion: "cancelled" }],
  issues: [issue], annotations: proof(["Test units"]),
});
assert.deepEqual(calls, [], "a cancelled job is no verdict, proven or not");
cases++;

calls = await run({ jobs: [changes, skipped("Test units")], issues: [issue], annotations: new Error("boom") });
assert.deepEqual(calls, [], "an unreadable proof proves nothing");
cases++;
console.log(`main-red-selftest: ${cases} cases pass against the reporter script`);
