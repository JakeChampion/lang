// Unit tests for the plain-text docs (src/lib/llms.mjs) and their order.

import assert from "node:assert/strict";
import { test } from "node:test";

import { readingOrder, sectionOf } from "../src/lib/doc-pages.mjs";
import { llmsIndex, plainMarkdown } from "../src/lib/llms.mjs";

test("turns MDX components into plain Markdown", () => {
  const body = [
    'import Facet from "../../components/Facet.astro";',
    "",
    "Intro.",
    "",
    "<FernPlayground",
    "  minimal",
    "  code={`function main(): i32 {",
    "    return 0;",
    "}`}",
    "/>",
    "",
    '  <Facet index="01" title="Starts instantly">',
    "",
    "Fast.",
    "",
    "  </Facet>",
    '<NextSteps items={[{ label: "x" }]} />',
  ].join("\n");
  assert.equal(plainMarkdown(body), [
    "Intro.",
    "",
    "```fern",
    "function main(): i32 {",
    "    return 0;",
    "}",
    "```",
    "",
    "### Starts instantly",
    "",
    "Fast.",
  ].join("\n"));
});

const page = (id, title, order) => ({ id, body: "", data: { title, description: `${title}.`, sidebar: { order } } });

test("orders pages by section, then sidebar order", () => {
  const order = readingOrder([
    page("reference/types", "Types", 2),
    page("tutorial/testing", "Testing", 7),
    page("http", "HTTP", 0),
    page("tutorial/install", "Install", 1),
    page("index", "Fern"),
  ]).map((e) => e.id);
  assert.deepEqual(order, ["index", "tutorial/install", "tutorial/testing", "http", "reference/types"]);
  assert.equal(sectionOf("http"), "HTTP guide");
  assert.equal(sectionOf("stdlib/json"), "Standard library");
});

test("writes an llmstxt.org index with one heading for each section", () => {
  const text = llmsIndex("https://example.org", "", [page("index", "Fern"), page("tutorial/install", "Install", 1), page("stdlib/json", "std/json")], "A language.");
  assert.match(text, /^# Fern\n\n> A language\.\n/);
  assert.match(text, /## Tutorial\n\n- \[Install\]\(https:\/\/example\.org\/tutorial\/install\/\): Install\./);
  // Standard-library modules are in llms-stdlib.txt, not in the index.
  assert.doesNotMatch(text, /std\/json/);
});
