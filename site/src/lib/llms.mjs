// Plain-text copies of the docs for language models, served as /llms.txt
// (an index, in the llmstxt.org format), /llms-full.txt (every guide and
// reference page) and /llms-stdlib.txt (every standard-library module).

import { readingOrder, sectionOf } from "./doc-pages.mjs";

// The Markdown of an .md or .mdx page, without the MDX parts that have no
// meaning as text. A live snippet becomes a fern code block, a feature card
// becomes a heading, and other component tags are removed.
export function plainMarkdown(body) {
  return body
    .replace(/^import .* from .*;\n/gm, "")
    .replace(/<FernPlayground\b[\s\S]*?code=\{`([\s\S]*?)`\}[\s\S]*?\/>/g, (_, code) => "```fern\n" + code.replace(/\\`/g, "`") + "\n```")
    .replace(/^[ \t]*<Facet\b[^>]*\btitle="([^"]*)"[^>]*>/gm, "### $1")
    .replace(/<[A-Z]\w*\b[^>]*?\/>/g, "")
    .replace(/^\s*<\/?[A-Za-z][\w.-]*\b[^>]*>\s*$/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

const isStdlib = (e) => e.id.startsWith("stdlib/") && e.id !== "stdlib";

export function pageUrl(site, entry) {
  return `${site}/${entry.id === "index" ? "" : `${entry.id}/`}`;
}

function document(site, entries) {
  return readingOrder(entries)
    .map((e) => `# ${e.data.title}\n\nSource: ${pageUrl(site, e)}\n\n${plainMarkdown(e.body ?? "")}\n`)
    .join("\n---\n\n");
}

export function llmsIndex(site, entries, description) {
  const lines = [
    "# Fern",
    "",
    `> ${description}`,
    "",
    "Fern compiles to native binaries for Linux and macOS on arm64, Linux on x86-64, and to WebAssembly.",
    `The full text of the guides and the reference is at ${site}/llms-full.txt, and every`,
    `standard-library module is at ${site}/llms-stdlib.txt.`,
    "",
  ];
  let section = null;
  for (const e of readingOrder(entries.filter((e) => !isStdlib(e)))) {
    const name = sectionOf(e.id) === "Fern" ? "Docs" : sectionOf(e.id);
    if (name !== section) {
      if (section !== null) lines.push("");
      section = name;
      lines.push(`## ${name}`, "");
    }
    const about = e.data.description ? `: ${e.data.description}` : "";
    lines.push(`- [${e.data.title}](${pageUrl(site, e)})${about}`);
  }
  lines.push("", "## Optional", "", `- [Standard library modules](${site}/llms-stdlib.txt): one section for each module`);
  return lines.join("\n") + "\n";
}

export const llmsFull = (site, entries) => document(site, entries.filter((e) => !isStdlib(e)));
export const llmsStdlib = (site, entries) => document(site, entries.filter(isStdlib));
