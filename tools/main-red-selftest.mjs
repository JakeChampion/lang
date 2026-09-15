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

async function run({ name = "CI main", jobs, issues = [] }) {
  const calls = [];
  const github = {
    rest: {
      actions: { listJobsForWorkflowRun: "jobs" },
      issues: {
        listForRepo: "issues",
        createComment: async (args) => calls.push(["comment", args]),
        update: async (args) => calls.push(["update", args]),
        createLabel: async (args) => calls.push(["label", args]),
        create: async (args) => { calls.push(["create", args]); return { data: { number: 9 } }; },
      },
    },
    paginate: async (api) => {
      assert.ok(api === "jobs" || api === "issues", `unexpected listing: ${api}`);
      return api === "jobs" ? jobs : issues;
    },
  };
  const context = {
    repo: { owner: "o", repo: "r" },
    payload: { workflow_run: { id: 1, name, head_sha: "a".repeat(40), html_url: "https://example.test/run" } },
  };
  await report(github, context, { notice() {}, warning() {} });
  return calls;
}

const issue = { number: 7, title: "main is red: Test units" };
let cases = 0;
for (const name of ["CI", "CI main"]) {
  const prefix = name === "CI main" ? "Validate / " : "";
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
console.log(`main-red-selftest: ${cases} cases pass against the reporter script`);
