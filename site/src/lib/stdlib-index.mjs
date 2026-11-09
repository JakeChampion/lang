// The entries of the standard-library function index: one for each
// declaration on a page that `fern -doc` generates, with the heading id
// that the page gives it.

import GithubSlugger from "github-slugger";

// The entries of one module page. `page` is its path under /stdlib/, and
// `module` its title, such as "std/json". Every heading takes a slug in
// page order, as Astro gives heading ids, so a repeated name gets the same
// "-1" suffix here as on the page.
export function pageEntries(page, module, body) {
  const slugger = new GithubSlugger();
  const out = [];
  const lines = body.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const m = /^(#+)\s+(.*)$/.exec(lines[i]);
    if (!m) continue;
    const text = m[2].replace(/`/g, "");
    const id = slugger.slug(text);
    if (m[1] !== "##") continue;
    const fence = lines.indexOf("```fern", i + 1);
    const signature = fence === i + 2 ? lines[fence + 1] : "";
    out.push({ name: text.split(" ").pop(), module, page, id, signature });
  }
  return out;
}

// The source file of a module, relative to the repository root.
export function moduleSource(module, exists) {
  for (const ext of [".fern.md", ".fern"]) {
    const path = `internal/stdlib/${module}${ext}`;
    if (exists(path)) return path;
  }
  return null;
}
