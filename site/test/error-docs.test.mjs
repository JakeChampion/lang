// Unit tests for scripts/error-docs.mjs.

import assert from "node:assert/strict";
import { test } from "node:test";

import { explanationPage, indexPage } from "../scripts/error-docs.mjs";

const text = "# E030 — `match` is not exhaustive\n\nA rule.\n\n## Example\n\n```\nbad\n```\n\n## Fix\n\n```\ngood\n```\n";

test("makes a page with the code in its title and fern code blocks", () => {
  const p = explanationPage(text);
  assert.equal(p.code, "E030");
  assert.equal(p.title, "match is not exhaustive");
  assert.match(p.markdown, /^---\ntitle: "E030: match is not exhaustive"\n/);
  assert.match(p.markdown, /```fern\nbad\n```\n/);
  assert.match(p.markdown, /```fern\ngood\n```\n/);
  assert.match(p.markdown, /fern -explain E030/);
});

test("refuses a file whose first line is not a code heading", () => {
  assert.throws(() => explanationPage("# Something else\n"), /CODE — title/);
});

test("groups the index by kind of code", () => {
  const idx = indexPage([explanationPage(text), explanationPage("# W001 — unused\n\nx\n")]);
  assert.match(idx, /## Errors[\s\S]*\| \[E030\]\(e030\/\) \| match is not exhaustive \|/);
  assert.match(idx, /## Warnings[\s\S]*\| \[W001\]\(w001\/\) \| unused \|/);
  assert.doesNotMatch(idx, /## Parse errors/);
});
