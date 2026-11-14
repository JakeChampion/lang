#!/usr/bin/env node
// ci-failure-report — say what failed in a CI run, with the lines of the log
// that show it.
//
//   ci-failure-report.mjs summary RUN_ID   print the report of the failed jobs
//                                          of RUN_ID as Markdown. No failed
//                                          job prints nothing.
//   ci-failure-report.mjs comment RUN_ID   put that report in one comment on
//                                          the pull request of RUN_ID. A later
//                                          run changes the same comment, and a
//                                          run that passes says so there.
//
// A red pull request showed one `failure` among many `cancelled` checks, and
// the reason was some hundred lines into the log of a job. pr-ci-report.yml
// runs `comment` when a CI run of a pull request ends. The merge queue runs
// `summary` for the comment on a pull request that it removes.
//
// THE EXCERPT. For a Go test failure it is the output of each test that
// failed, up to the `--- FAIL` line. A long output keeps its first and its
// last lines. For any other failure it is the lines before the first
// `##[error]` line. A job gives at most MAX_TESTS tests and each excerpt at
// most MAX_LINES lines, so the comment stays short. The link
// of the job has the whole log.
//
// GITHUB_REPOSITORY names the repository and GITHUB_TOKEN reads it.
import { pathToFileURL } from "node:url";

export const MARKER = "<!-- ci-failure-report -->";
const MAX_JOBS = 6;
const MAX_TESTS = 3;
const MAX_LINES = 25;
const MAX_WIDTH = 400;
// A job that fails only because a job before it failed.
const DERIVED = /(^|\/ )(CI verdict|Land the batch)$/;

const clean = (line) =>
  line.replace(/^﻿?\d{4}-\d\d-\d\dT[\d:.]+Z /, "").replace(/\u001b\[[0-9;]*m/g, "").replace(/\s+$/, "");

const narrow = (lines) => lines.map((l) => (l.length > MAX_WIDTH ? `${l.slice(0, MAX_WIDTH)} ...` : l));
const clip = (lines) => narrow(lines.slice(-MAX_LINES));
// The first lines of a test output name the assertion, and the last lines
// have the verdict.
const HEAD_LINES = 8;
const clipTest = (lines) => (lines.length <= MAX_LINES ? narrow(lines)
  : narrow([...lines.slice(0, HEAD_LINES), `... ${lines.length - MAX_LINES + 1} lines are left out ...`, ...lines.slice(HEAD_LINES - MAX_LINES + 1)]));

// failedTests returns the Go tests that the log marks as failed, each with the
// lines of its output. A test with a failed subtest is left out: the subtest
// has the reason.
export function failedTests(log) {
  const lines = log.split("\n").map(clean);
  const fails = [];
  lines.forEach((line, i) => {
    const m = /^\s*--- FAIL: (\S+)/.exec(line);
    if (m && !fails.some((f) => f.name === m[1])) fails.push({ name: m[1], at: i });
  });
  const leaves = fails.filter((f) => !fails.some((g) => g.name.startsWith(`${f.name}/`)));
  return leaves.map(({ name, at }) => {
    let from = at;
    for (let i = at - 1; i >= 0 && at - i <= 400; i--) {
      if (new RegExp(`^=== (RUN|CONT|NAME)\\s+${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`).test(lines[i])) {
        from = i;
        break;
      }
    }
    if (from === at) from = Math.max(0, at - 8);
    return { name, lines: clipTest(lines.slice(from, at + 1)) };
  });
}

// errorExcerpt returns the lines before the first error of a log with no Go
// test failure in it.
export function errorExcerpt(log) {
  const lines = log.split("\n").map(clean).filter((l) => !/^##\[(group|endgroup)\]/.test(l));
  const at = lines.findIndex((l) => l.startsWith("##[error]"));
  if (at < 0) return clip(lines.filter((l) => l !== ""));
  return clip(lines.slice(Math.max(0, at - MAX_LINES + 1), at + 1));
}

const escape = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
// Each line is escaped and put in `pre`. GitHub removes a span that reads as
// an HTML tag from a comment, in a fenced block too.
const block = (lines) => `<pre>${escape(lines.join("\n"))}</pre>`;

// render gives the Markdown of the failed jobs: [{ name, url, step, log }].
export function render(jobs) {
  const out = [];
  for (const job of jobs.slice(0, MAX_JOBS)) {
    out.push(`### [${job.name}](${job.url})`, "");
    if (job.step) out.push(`Failed step: \`${job.step}\``, "");
    const tests = job.log === null ? [] : failedTests(job.log);
    if (job.log === null) {
      out.push("The log of this job could not be read.", "");
    } else if (tests.length > 0) {
      out.push(`${tests.length} test(s) failed:`, "", ...tests.map((t) => `- \`${t.name}\``), "");
      for (const t of tests.slice(0, MAX_TESTS)) {
        out.push(`<details><summary><code>${escape(t.name)}</code></summary>`, "", block(t.lines), "", "</details>", "");
      }
      if (tests.length > MAX_TESTS) out.push(`The log has the output of the other ${tests.length - MAX_TESTS}.`, "");
    } else {
      out.push("<details><summary>The lines before the error</summary>", "", block(errorExcerpt(job.log)), "", "</details>", "");
    }
  }
  if (jobs.length > MAX_JOBS) {
    out.push(`${jobs.length - MAX_JOBS} more job(s) failed: ${jobs.slice(MAX_JOBS).map((j) => `[${j.name}](${j.url})`).join(", ")}.`, "");
  }
  return out.join("\n").trimEnd();
}

// body gives the whole comment for a run that concluded `conclusion`.
export function body({ conclusion, sha, runUrl, jobs }) {
  const head = `\`${sha.slice(0, 8)}\``;
  if (conclusion === "success") {
    return `${MARKER}\n## CI passed on ${head}\n\n[The run](${runUrl}) has no failed job.`;
  }
  const what = jobs.length === 0
    ? "No job has the conclusion `failure`. Read the run for a job that was cancelled or did not start."
    : `${jobs.length} job(s) failed. The other jobs that show \`cancelled\` stopped because of the first failure.\n\n${render(jobs)}`;
  return `${MARKER}\n## CI failed on ${head}\n\n[The run](${runUrl}): ${what}`;
}

// The functions below talk to GitHub.

const repository = () => process.env.GITHUB_REPOSITORY;

async function api(path, { method = "GET", data, raw = false } = {}) {
  const res = await fetch(path.startsWith("http") ? path : `https://api.github.com/repos/${repository()}/${path}`, {
    method,
    headers: {
      authorization: `Bearer ${process.env.GITHUB_TOKEN}`,
      accept: "application/vnd.github+json",
      "x-github-api-version": "2022-11-28",
      ...(data ? { "content-type": "application/json" } : {}),
    },
    body: data ? JSON.stringify(data) : undefined,
  });
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  return raw ? res.text() : res.json();
}

async function failedJobs(runId) {
  const jobs = [];
  for (let page = 1; ; page++) {
    const got = await api(`actions/runs/${runId}/jobs?filter=latest&per_page=100&page=${page}`);
    jobs.push(...got.jobs);
    if (got.jobs.length < 100) break;
  }
  const failed = jobs.filter((j) => (j.conclusion === "failure" || j.conclusion === "timed_out") && !DERIVED.test(j.name));
  return Promise.all(failed.map(async (j, i) => ({
    name: j.name,
    url: j.html_url,
    step: (j.steps || []).find((s) => s.conclusion === "failure")?.name ?? "",
    log: i < MAX_JOBS ? await api(`actions/jobs/${j.id}/logs`, { raw: true }).catch(() => null) : "",
  })));
}

async function comment(runId) {
  const run = await api(`actions/runs/${runId}`);
  if (!["success", "failure"].includes(run.conclusion)) {
    console.log(`the run concluded ${run.conclusion}: no comment`);
    return;
  }
  let prs = run.pull_requests || [];
  if (prs.length === 0) prs = (await api(`commits/${run.head_sha}/pulls`)).filter((p) => p.state === "open");
  for (const { number } of prs) {
    const pr = await api(`pulls/${number}`);
    if (pr.head.sha !== run.head_sha) {
      console.log(`#${number} has a newer head than the run: no comment`);
      continue;
    }
    const comments = await api(`issues/${number}/comments?per_page=100`);
    const mine = comments.find((c) => c.body.startsWith(MARKER));
    if (run.conclusion === "success" && !mine) continue;
    const text = body({
      conclusion: run.conclusion,
      sha: run.head_sha,
      runUrl: run.html_url,
      jobs: run.conclusion === "failure" ? await failedJobs(runId) : [],
    });
    if (mine) await api(`issues/comments/${mine.id}`, { method: "PATCH", data: { body: text } });
    else await api(`issues/${number}/comments`, { method: "POST", data: { body: text } });
    console.log(`#${number}: the comment says that CI concluded ${run.conclusion}`);
  }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [command, runId] = process.argv.slice(2);
  if (command === "summary" && runId) {
    const jobs = await failedJobs(runId);
    if (jobs.length > 0) console.log(render(jobs));
  } else if (command === "comment" && runId) {
    await comment(runId);
  } else {
    console.error("usage: ci-failure-report.mjs summary RUN_ID | comment RUN_ID");
    process.exit(2);
  }
}
