#!/usr/bin/env node
// Run the gate of ci-suite.yml, which waits for a batch of `ci-priority` pull
// requests, against API stubs.
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), "..");
const lines = fs.readFileSync(path.join(root, ".github/workflows/ci-suite.yml"), "utf8").split("\n");

// The body of the first `script: |` scalar after the gate's name, dedented.
function gateScript() {
  let i = lines.findIndex((l) => l.trim() === "name: Wait for a priority batch");
  if (i < 0) throw new Error("ci-suite.yml has no job named \"Wait for a priority batch\"");
  for (; i < lines.length; i++) if (lines[i].trim() === "script: |") break;
  if (i >= lines.length) throw new Error("the gate of ci-suite.yml has no `script: |`");
  const indent = lines[i].length - lines[i].trimStart().length;
  const out = [];
  for (let j = i + 1; j < lines.length; j++) {
    if (lines[j].trim() !== "" && lines[j].length - lines[j].trimStart().length <= indent) break;
    out.push(lines[j]);
  }
  const strip = Math.min(...out.filter((l) => l.trim()).map((l) => l.length - l.trimStart().length));
  return out.map((l) => l.slice(strip)).join("\n");
}

const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const run = new AsyncFunction("github", "context", "core", gateScript());

// `batches` is the branch head that each successive poll reads: null for no
// branch, an Error to throw, or a commit message. `prs` maps a number to its
// state and labels.
async function gate({ batches, prs = {}, waitMinutes = 100 }) {
  process.env.CI_PRIORITY_POLL_MS = "1";
  process.env.CI_PRIORITY_WAIT_MINUTES = String(waitMinutes);
  const warnings = [];
  let polls = 0;
  const github = {
    rest: {
      repos: {
        getBranch: async ({ branch }) => {
          if (branch !== "merge-queue/batch") throw new Error(`unexpected branch ${branch}`);
          const head = batches[Math.min(polls++, batches.length - 1)];
          if (head instanceof Error) throw head;
          if (head === null) throw Object.assign(new Error("Branch not found"), { status: 404 });
          return { data: { commit: { commit: { message: head } } } };
        },
      },
      pulls: {
        get: async ({ pull_number }) => {
          const pr = prs[pull_number] || { state: "open", labels: [] };
          return { data: { state: pr.state, labels: pr.labels.map((name) => ({ name })) } };
        },
      },
    },
  };
  const core = { warning: (m) => warnings.push(m), info: () => {} };
  await run(github, { repo: { owner: "o", repo: "r" } }, core);
  return { polls, warnings };
}

let failures = 0;
function check(name, got, want) {
  const ok = JSON.stringify(got) === JSON.stringify(want);
  if (!ok) failures++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${ok ? "" : `: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`}`);
}

const priority = { 7: { state: "open", labels: ["ci-priority", "merge-queue"] } };
const batch = "Merge pull request #7 from claude/fix\n\nbody";

check("no batch: the suite runs at once",
  await gate({ batches: [null] }), { polls: 1, warnings: [] });
check("a batch with no priority pull request: the suite runs at once",
  await gate({ batches: [batch], prs: { 7: { state: "open", labels: ["merge-queue"] } } }), { polls: 1, warnings: [] });
check("a priority batch: the suite waits until the batch ends",
  await gate({ batches: [batch, batch, batch, null], prs: priority }), { polls: 4, warnings: [] });
check("a priority pull request that merged ends the wait",
  await gate({ batches: [batch, batch], prs: { 7: { state: "closed", labels: ["ci-priority"] } } }), { polls: 1, warnings: [] });
check("a batch head that is not a merge of a pull request: the suite runs",
  await gate({ batches: ["Merge main into the batch of the merge queue"], prs: priority }), { polls: 1, warnings: [] });

const broken = await gate({ batches: [batch, new Error("HTTP 502")], prs: priority });
check("an API error: the suite runs after one warning", [broken.polls, broken.warnings.length], [2, 1]);
const late = await gate({ batches: [batch], prs: priority, waitMinutes: 1e-6 });
check("the wait ends at its budget with a warning", [late.warnings.length, /still in flight/.test(late.warnings[0])], [1, true]);

if (failures) {
  console.error(`${failures} check(s) failed`);
  process.exit(1);
}
