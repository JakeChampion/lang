// Generate a docs page for each diagnostic code from the explanations that
// `fern -explain` prints (internal/syntax/diag/explanations/), and an index
// of them, in src/content/docs/errors/. The pages are not in git, as the
// stdlib pages are not. `npm run build` runs this first.

import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const site = fileURLToPath(new URL("..", import.meta.url));
const source = join(site, "../internal/syntax/diag/explanations");
const out = join(site, "src/content/docs/errors");

const KINDS = [
  ["E", "Errors", "The program is not valid Fern, so it does not build."],
  ["P", "Parse errors", "The text is not Fern syntax, so nothing after the parser runs."],
  ["W", "Warnings", "The program builds, but probably does not do what it says."],
];

// One explanation as a page. The file starts "# CODE — title". Its code
// examples have no language, and they are Fern, so they get `fern`.
export function explanationPage(text) {
  const [first, ...rest] = text.split("\n");
  const m = /^# (\w+) — (.*)$/.exec(first);
  if (!m) throw new Error(`explanation heading is not "# CODE — title": ${first}`);
  const [, code, title] = m;
  const body = rest.join("\n").replace(/^```\s*$/gm, (fence, offset, all) => {
    // An opening fence is one with an even number of fences before it.
    const before = all.slice(0, offset).match(/^```/gm)?.length ?? 0;
    return before % 2 === 0 ? "```fern" : "```";
  });
  const plain = title.replace(/`/g, "");
  return {
    code,
    title: plain,
    markdown: `---\ntitle: "${code}: ${plain.replace(/"/g, '\\"')}"\ndescription: "What ${code} means, an example, and how to fix it."\n---\n\n${body.trim()}\n\nRun \`fern -explain ${code}\` to read this in a terminal.\n`,
  };
}

export function indexPage(pages) {
  const sections = KINDS.map(([prefix, name, about]) => {
    const rows = pages
      .filter((p) => p.code.startsWith(prefix))
      .map((p) => `| [${p.code}](${p.code.toLowerCase()}/) | ${p.title.replace(/\|/g, "\\|")} |`);
    if (rows.length === 0) return "";
    return `## ${name}\n\n${about}\n\n| Code | Meaning |\n|---|---|\n${rows.join("\n")}\n`;
  });
  return `---\ntitle: Error codes\ndescription: Every diagnostic that the Fern compiler can give, with an example and the fix.\nsidebar:\n  order: 99\n---\n\nEach diagnostic has a code, such as \`error[E030]\`. Its page explains\nthe rule, shows a program that breaks it, and gives the fix.\n\`fern -explain CODE\` prints the same text in a terminal.\n\n${sections.join("\n")}`;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const pages = readdirSync(source)
    .filter((f) => f.endsWith(".md"))
    .sort()
    .map((f) => explanationPage(readFileSync(join(source, f), "utf8")));
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });
  for (const p of pages) writeFileSync(join(out, `${p.code.toLowerCase()}.md`), p.markdown);
  writeFileSync(join(out, "index.md"), indexPage(pages));
  console.log(`error-docs: wrote ${pages.length} pages to src/content/docs/errors/`);
}
