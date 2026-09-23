#!/usr/bin/env node
// Runs the `changes` job's script out of .github/workflows/ci.yml against
// stubbed API responses, so the JS copy of GitHub's filter grammar and the
// job's fail-open rules are pinned the way TestGlobMatchesFollowsGitHubGrammar
// pins the Go copy. The lint lane runs it (`make ci-selftest`); it needs only
// node, which the runner image has.
//
// The script and the lane table are read out of the workflow textually: the
// `script: |` and `LANES: |` blocks of the step whose id is `filter`.
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), "..");
const ci = fs.readFileSync(path.join(root, ".github/workflows/ci.yml"), "utf8").split("\n");

// block returns the body of the `key: |` scalar that follows the first line
// matching `after`, dedented.
function block(after, key) {
  let i = ci.findIndex((l) => l.trim() === after);
  if (i < 0) throw new Error(`ci.yml has no line ${JSON.stringify(after)}`);
  for (; i < ci.length; i++) if (ci[i].trim() === `${key}: |`) break;
  if (i >= ci.length) throw new Error(`ci.yml has no \`${key}: |\` after ${after}`);
  const indent = ci[i].length - ci[i].trimStart().length;
  const out = [];
  for (let j = i + 1; j < ci.length; j++) {
    const l = ci[j];
    if (l.trim() !== "" && l.length - l.trimStart().length <= indent) break;
    out.push(l);
  }
  const strip = Math.min(...out.filter((l) => l.trim()).map((l) => l.length - l.trimStart().length));
  return out.map((l) => l.slice(strip)).join("\n");
}

const script = block("- id: filter", "script");
const table = block("- id: filter", "LANES");
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const run = new AsyncFunction("github", "context", "core", script);

const suiteYml = fs.readFileSync(path.join(root, ".github/workflows/ci-suite.yml"), "utf8");

// One invocation of the script: `files` is what the API lists, `throwOn`
// makes the listing fail, `lanes` replaces the real table. `proof` stubs what
// a main push reads about the pull request it merged: the pushed tree, the
// associated PRs with their head trees, and each head's successful runs and
// their jobs. `proofThrows` makes the first of those calls fail.
async function decide({ event = "pull_request", files = [], throwOn = false, lanes = table, proof = null, proofThrows = false } = {}) {
  process.env.LANES = typeof lanes === "string" ? lanes : JSON.stringify(lanes);
  const outputs = {}, warnings = [], failed = [], infos = [];
  let listings = 0;
  const core = {
    setOutput: (k, v) => (outputs[k] = v),
    setFailed: (m) => failed.push(m),
    warning: (m) => warnings.push(m),
    info: (m) => infos.push(m),
    summary: { addHeading() { return this; }, addRaw() { return this; }, addTable() { return this; }, async write() {} },
  };
  const context = {
    eventName: event,
    sha: "c".repeat(40),
    repo: { owner: "o", repo: "r" },
    payload: event === "pull_request"
      ? { pull_request: { number: 1 } }
      : { before: "a".repeat(40), after: "b".repeat(40) },
  };
  const p = proof || { tree: "t", prs: [], runs: {}, jobs: {} };
  const trees = { [context.sha]: p.tree, ...Object.fromEntries(p.prs.map((pr) => [pr.head.sha, pr.tree])) };
  const github = {
    rest: {
      pulls: { listFiles: "listFiles" },
      repos: {
        compareCommitsWithBasehead: async ({ basehead }) => ({
          data: (p.compare || {})[basehead] || { status: "diverged", files: [] },
        }),
        listPullRequestsAssociatedWithCommit: async () => ({ data: p.prs }),
        getContent: async () => ({ data: { content: Buffer.from(suiteYml).toString("base64") } }),
      },
      git: {
        getCommit: async ({ commit_sha }) => {
          if (proofThrows) throw new Error("api down");
          return { data: { tree: { sha: trees[commit_sha] } } };
        },
      },
      actions: {
        listWorkflowRuns: async ({ workflow_id, head_sha }) => {
          if (workflow_id === "ci-main.yml") {
            if (p.mainThrows) throw new Error("runs api down");
            return { data: { workflow_runs: p.mainRuns || [] } };
          }
          return { data: { workflow_runs: p.runs[head_sha] || [] } };
        },
        listJobsForWorkflowRun: "listJobs",
      },
    },
    paginate: async (fn, opts, map) => {
      if (fn === "listJobs") return p.jobs[opts.run_id] || [];
      listings++;
      if (throwOn) throw new Error("boom");
      const list = files.map((f) => (typeof f === "string" ? { filename: f } : f));
      return map ? map({ data: { status: "ahead", files: list } }) : list;
    },
  };
  await run(github, context, core);
  return { lanes: outputs.lanes ? JSON.parse(outputs.lanes) : null, warnings, failed, listings, infos };
}

let failures = 0;
let cases = 0;
const check = (what, got, want) => {
  cases++;
  if (JSON.stringify(got) === JSON.stringify(want)) return;
  failures++;
  console.error(`FAIL ${what}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
};
// Which lanes run for `files`, under a one-lane table with `filter`.
const runs = async (filter, files) => (await decide({ files, lanes: { L: filter } })).lanes.L;

// GitHub's grammar: `?` is zero or one of the character before it, `+` one
// or more; `*` stops at `/`, `**` does not; `[…]` is a class; a quantifier
// with nothing before it, and an unclosed class, are literal.
check("x? zero", await runs({ paths: ["src/x?.txt"] }, ["src/.txt"]), true);
check("x? one", await runs({ paths: ["src/x?.txt"] }, ["src/x.txt"]), true);
check("x? two", await runs({ paths: ["src/x?.txt"] }, ["src/xx.txt"]), false);
check("a+ two", await runs({ paths: ["a+b.txt"] }, ["aab.txt"]), true);
check("a+ none", await runs({ paths: ["a+b.txt"] }, ["b.txt"]), false);
check("class quantified", await runs({ paths: ["[ab]+.go"] }, ["abba.go"]), true);
check("class miss", await runs({ paths: ["[ab]x.go"] }, ["cx.go"]), false);
check("leading ? literal", await runs({ paths: ["?x"] }, ["?x"]), true);
check("leading + literal", await runs({ paths: ["+x"] }, ["+x"]), true);
check("unclosed class literal", await runs({ paths: ["[a"] }, ["[a"]), true);
check("*.md root", await runs({ paths: ["*.md"] }, ["README.md"]), true);
check("*.md not nested", await runs({ paths: ["*.md"] }, ["docs/a.md"]), false);
check("docs/** deep", await runs({ paths: ["docs/**"] }, ["docs/a/b.md"]), true);
check("dot literal", await runs({ paths: ["a.b"] }, ["axb"]), false);
// Filter semantics: negation order, paths-ignore, renames.
check("negation excludes", await runs({ paths: ["lib/**", "!lib/vendor/**"] }, ["lib/vendor/z.go"]), false);
check("negation scoped", await runs({ paths: ["lib/**", "!lib/vendor/**"] }, ["lib/z.go"]), true);
check("paths-ignore all docs", await runs({ "paths-ignore": ["docs/**", "*.md"] }, ["docs/a.md", "README.md"]), false);
check("paths-ignore one source", await runs({ "paths-ignore": ["docs/**", "*.md"] }, ["README.md", "internal/x.go"]), true);
check("rename old name counts", await runs({ paths: ["docs/**"] }, [{ filename: "web/n.js", previous_filename: "docs/o.md" }]), true);
// The real table.
const all = (r) => Object.values(r.lanes).every(Boolean);
let r = await decide({ files: ["editors/vscode/x.ts"] });
check("real: editor change", [r.lanes["vscode-extension"], r.lanes.macos, r.lanes["test-units"]], [true, false, true]);
r = await decide({ files: ["docs/x.md", "CLAUDE.md"] });
check("real: doc-only PR runs no filtered lane", Object.values(r.lanes).some(Boolean), false);
r = await decide({ files: [".github/workflows/ci.yml"] });
check("real: ci.yml edit runs every lane", all(r), true);
r = await decide({ files: [".github/workflows/ci-suite.yml"] });
check("real: ci-suite.yml edit runs every lane", all(r), true);
r = await decide({ files: [".github/workflows/ci-main.yml"] });
check("real: ci-main.yml edit runs every lane", all(r), true);
// A skipped code push followed by a docs-only push must still validate code.
// Even a successful, empty compare response must not suppress main lanes.
for (const files of [["docs/x.md"], ["editors/vscode/x.ts"], []]) {
  r = await decide({ event: "push", files });
  check(`main validates full tip: ${JSON.stringify(files)}`, [all(r), r.listings], [true, 0]);
}
// A main push at a tree identical to the merged PR's head skips each lane
// whose every job passed on that head's successful run; perf always runs.
const display = Object.fromEntries([...suiteYml.matchAll(/^  ([a-z0-9_-]+):\n    name: (.+)$/gm)].map((m) => [m[1], m[2].trim()]));
const laneKeys = Object.keys(JSON.parse(table));
check("every lane has a Full suite name to prove it by", laneKeys.filter((l) => !display[l]), []);
const jobsFor = (keys, conclusion = "success") => keys.map((l) => ({ name: `Full suite / ${display[l]} / job`, conclusion }));
const merged = (tree) => ({ number: 7, merged_at: "2026-09-23T00:00:00Z", base: { ref: "main" }, head: { sha: "h".repeat(40) }, tree });
const proofOf = ({ tree = "t", headTree = "t", jobs = jobsFor(laneKeys), prs } = {}) => ({
  tree, prs: prs || [merged(headTree)], runs: { ["h".repeat(40)]: [{ id: 99 }] }, jobs: { 99: jobs },
});
r = await decide({ event: "push", proof: proofOf() });
check("identical tree: every passed lane skips, perf runs",
  Object.keys(r.lanes).filter((l) => r.lanes[l]), ["perf"]);
r = await decide({ event: "push", proof: proofOf({ headTree: "other" }) });
check("different tree: every lane runs, and says why",
  [all(r), r.infos.some((m) => m.includes("the pushed tree differs from the head of #7"))], [true, true]);
r = await decide({ event: "push", proof: proofOf({ prs: [] }) });
check("no associated PR: every lane runs, and says why",
  [all(r), r.infos.some((m) => m.includes("no merged pull request is associated"))], [true, true]);
r = await decide({ event: "push", proof: { ...proofOf(), runs: {} } });
check("no successful PR run: every lane runs, and says why",
  [all(r), r.infos.some((m) => m.includes("#7's head has no successful CI run"))], [true, true]);
r = await decide({ event: "push", proof: proofOf({ jobs: jobsFor(laneKeys.filter((l) => l !== "macos")) }) });
check("a lane the PR run did not run still runs on main", [r.lanes.macos, r.lanes["test-units"]], [true, false]);
r = await decide({ event: "push", proof: proofOf({ jobs: [...jobsFor(laneKeys), { name: `Full suite / ${display["test-units"]} / extra`, conclusion: "skipped" }] }) });
check("a lane with any job not passed still runs", [r.lanes["test-units"], r.lanes["test-coreutils"]], [true, false]);
r = await decide({ event: "push", proof: proofOf({ prs: [{ ...merged("t"), base: { ref: "other" } }] }) });
check("a PR merged elsewhere proves nothing", all(r), true);
r = await decide({ event: "push", proof: proofOf({ prs: [{ ...merged("t"), merged_at: null }] }) });
check("an unmerged PR proves nothing", all(r), true);
r = await decide({ event: "push", proof: proofOf(), proofThrows: true });
check("an API error proves nothing", [all(r), r.warnings.some((w) => w.includes("merged PR"))], [true, true]);
r = await decide({ event: "workflow_dispatch", proof: proofOf() });
check("a dispatch is never trimmed", all(r), true);

// A main push skips each lane whose inputs did not change since the newest
// main run that ran it passed it; a lane red or cancelled there runs.
const mainJobs = (keys, conclusion = "success") => keys.map((l) => ({ name: `Validate / Full suite / ${display[l]} / job`, conclusion }));
const head = "c".repeat(40);
const mainProof = ({ runs = [{ id: 50, head_sha: "m1" }], jobs = { 50: mainJobs(laneKeys) }, compare = {}, mainThrows = false } = {}) =>
  ({ tree: "t", prs: [], runs: {}, jobs, mainRuns: runs, compare, mainThrows });
const cmp = (files, status = "ahead") => ({ status, files: files.map((f) => ({ filename: f })) });
const running = (r) => Object.keys(r.lanes).filter((l) => r.lanes[l]);
r = await decide({ event: "push", proof: mainProof({ compare: { [`m1...${head}`]: cmp(["docs/x.md"]) } }) });
check("main: docs-only since the last pass runs only perf", running(r), ["perf"]);
r = await decide({ event: "push", proof: mainProof({ compare: { [`m1...${head}`]: cmp(["examples/self_host/lexer.fern"]) } }) });
check("main: a self-host-only change skips the lanes that cannot see it",
  [r.lanes["test-e2e-x86_64"], r.lanes["test-e2e-differential"], r.lanes["test-units"], r.lanes["test-e2e-selfhost"], r.lanes.bootstrap, r.lanes.macos],
  [false, false, true, true, true, true]);
r = await decide({ event: "push", proof: mainProof({ compare: { [`m1...${head}`]: cmp(["internal/checker/x.go"]) } }) });
check("main: a compiler change runs the lanes it reaches", [r.lanes["test-units"], r.lanes["test-e2e-x86_64"], r.lanes.macos], [true, true, true]);
r = await decide({ event: "push", proof: mainProof({
  jobs: { 50: [...mainJobs(laneKeys.filter((l) => l !== "test-units")), ...mainJobs(["test-units"], "failure")] },
  compare: { [`m1...${head}`]: cmp(["docs/x.md"]) } }) });
check("main: a lane red on its last run runs again", [r.lanes["test-units"], r.lanes["test-coreutils"]], [true, false]);
r = await decide({ event: "push", proof: mainProof({
  runs: [{ id: 51, head_sha: "m2" }, { id: 50, head_sha: "m1" }],
  jobs: { 51: mainJobs(laneKeys.filter((l) => l !== "test-units")), 50: mainJobs(laneKeys) },
  compare: { [`m2...${head}`]: cmp(["docs/x.md"]), [`m1...${head}`]: cmp(["docs/x.md", "internal/checker/x.go"]) } }) });
check("main: a lane skipped on the newest run is diffed from where it last ran", [r.lanes["test-units"], r.lanes["test-coreutils"]], [true, false]);
r = await decide({ event: "push", proof: mainProof({
  runs: [{ id: 51, head_sha: "m2" }, { id: 50, head_sha: "m1" }],
  jobs: { 51: [...mainJobs(laneKeys.filter((l) => l !== "test-units"), "cancelled"), ...mainJobs(["test-units"])], 50: mainJobs(laneKeys) },
  compare: { [`m2...${head}`]: cmp(["docs/x.md"]), [`m1...${head}`]: cmp(["docs/x.md", "internal/checker/x.go"]) } }) });
check("main: a lane a later push cancelled is diffed from where it last passed", [r.lanes["test-units"], r.lanes["test-e2e-x86_64"]], [false, true]);
r = await decide({ event: "push", proof: mainProof({ compare: { [`m1...${head}`]: cmp(["docs/x.md"], "diverged") } }) });
check("main: a base that is not an ancestor proves nothing", all(r), true);
r = await decide({ event: "push", proof: mainProof({ compare: { [`m1...${head}`]: cmp(Array.from({ length: 300 }, (_, i) => `docs/${i}.md`)) } }) });
check("main: a truncated file list proves nothing", all(r), true);
r = await decide({ event: "push", proof: mainProof({ runs: [{ id: 52, head_sha: head }], jobs: { 52: mainJobs(laneKeys) } }) });
check("main: a run of this same commit is not a base", all(r), true);
r = await decide({ event: "push", proof: mainProof({ runs: [] }) });
check("main: no earlier main run proves nothing", all(r), true);
r = await decide({ event: "push", proof: mainProof({ mainThrows: true }) });
check("main: an API error proves nothing", [all(r), r.warnings.some((w) => w.includes("main's earlier runs"))], [true, true]);

// Fail-open, and the one loud exception.
check("listing failure runs everything", all(await decide({ throwOn: true })), true);
check("dispatch runs everything", all(await decide({ event: "workflow_dispatch" })), true);
r = await decide({ files: [{ get filename() { throw new Error("shape"); } }], lanes: { L: { paths: ["a"] } } });
check("error inside the filter fails open", [r.lanes.L, r.warnings.some((w) => w.includes("filter failed"))], [true, true]);
r = await decide({ files: ["x"], lanes: "{ not json" });
check("unparsable table fails loudly", [r.lanes, r.failed.some((m) => m.includes("not valid JSON"))], [null, true]);

if (failures) {
  console.error(`ci-changes-selftest: ${failures} failure(s)`);
  process.exit(1);
}
console.log(`ci-changes-selftest: ${cases} cases pass against the changes script in ci.yml`);
