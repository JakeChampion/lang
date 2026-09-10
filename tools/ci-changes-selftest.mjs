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

// One invocation of the script: `files` is what the API lists, `throwOn`
// makes the listing fail, `lanes` replaces the real table.
async function decide({ event = "pull_request", files = [], throwOn = false, lanes = table } = {}) {
  process.env.LANES = typeof lanes === "string" ? lanes : JSON.stringify(lanes);
  const outputs = {}, warnings = [], failed = [];
  const core = {
    setOutput: (k, v) => (outputs[k] = v),
    setFailed: (m) => failed.push(m),
    warning: (m) => warnings.push(m),
    info: () => {},
    summary: { addHeading() { return this; }, addRaw() { return this; }, addTable() { return this; }, async write() {} },
  };
  const context = {
    eventName: event,
    repo: { owner: "o", repo: "r" },
    payload: event === "pull_request"
      ? { pull_request: { number: 1 } }
      : { before: "a".repeat(40), after: "b".repeat(40) },
  };
  const github = {
    rest: { pulls: { listFiles: "listFiles" }, repos: { compareCommitsWithBasehead: "compare" } },
    paginate: async (fn, opts, map) => {
      if (throwOn) throw new Error("boom");
      const list = files.map((f) => (typeof f === "string" ? { filename: f } : f));
      return map ? map({ data: { status: "ahead", files: list } }) : list;
    },
  };
  await run(github, context, core);
  return { lanes: outputs.lanes ? JSON.parse(outputs.lanes) : null, warnings, failed };
}

let failures = 0;
const check = (what, got, want) => {
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
let r = await decide({ event: "push", files: ["editors/vscode/x.ts"] });
check("real: editor change", [r.lanes["vscode-extension"], r.lanes.macos, r.lanes["test-units"]], [true, false, true]);
r = await decide({ files: ["docs/x.md", "CLAUDE.md"] });
check("real: doc-only PR runs no filtered lane", Object.values(r.lanes).some(Boolean), false);
r = await decide({ files: [".github/workflows/ci.yml"] });
check("real: ci.yml edit runs every lane", all(r), true);
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
console.log("ci-changes-selftest: 26 cases pass against the `changes` script in ci.yml");
