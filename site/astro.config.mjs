// Astro + Starlight config for the Fern documentation site.
//
// Site lives at the repo root URL (https://<user>.github.io/lang/)
// with the playground bundle nested at /playground/.
//
// This file configures the DOCS half of the site. `/` is a bespoke
// page outside the docs collection (src/pages/index.astro), which
// takes that route because a static route outranks `[...slug]`.
//
// The sidebar's `sidebar:` key below is the map of the docs. One
// section is not written by hand: Stdlib is auto-generated from
// internal/stdlib/**/*.fern by `cmd/ferndoc`, and `stdlibSidebar()`
// groups whatever pages it finds by purpose. A clean checkout where
// ferndoc hasn't run keeps the section, with just its overview link.
//
// `base` is set so the site works under GitHub Pages' project
// subpath (https://<user>.github.io/lang/) without absolute-URL
// breakage; override via `SITE_BASE` env if deploying elsewhere.

import { existsSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

import { fernGrammar, CODE_THEMES } from "./src/lib/grammar.mjs";

const base = process.env.SITE_BASE ?? "/lang";
const siteUrl = process.env.SITE_URL ?? "https://jakechampion.github.io";

// One-line summary reused for the meta description, the Open Graph
// card, and Starlight's own `description` — so search results, chat
// unfurls, and the site itself all say the same thing.
const tagline =
  "Fern is a statically typed language with no garbage collector and no borrow checker. Its compiler assembles and links binaries in one process — no gcc, no ld — targets arm64, x86-64 and WebAssembly, and is written in Fern.";

// Social-card image. Crawlers won't resolve a relative URL, so this has to
// be absolute — built from the same two vars the rest of the site's URLs
// come from, so a Netlify deploy (SITE_URL set, base rewritten to root)
// points at its own copy rather than the Pages one. Rendered by
// `npm run og` from src/assets/og-card.svg.
const ogImage = `${siteUrl.replace(/\/$/, "")}${base.replace(/\/$/, "")}/og.png`;

// Standard-library sidebar, grouped by what a module is for.
// `autogenerate` gives one flat alphabetical run of ~70 pages, which is
// a list to scroll rather than a thing to browse. Membership is keyed by
// module slug; anything ferndoc emits that isn't listed here still shows
// up, under "Other", so a new stdlib module is never invisible — it just
// wants a home.
const STDLIB_GROUPS = [
  ["Text", ["ansi", "format", "glob", "peg", "regex", "strdist", "string",
    "table", "textwrap", "unicode", "utf8"]],
  ["Data & encoding", ["base32", "base64", "crypto", "csv", "hash", "hex",
    "json", "semver", "url", "uuid"]],
  ["Collections & errors", ["array", "error", "iter", "map", "option",
    "result", "set", "sort", "ordmap", "ordset", "pmap", "pset", "pvec"]],
  ["Traits", ["cmp", "convert", "mem", "num"]],
  ["Numbers", ["bigint", "float", "i32", "i64", "int", "math", "rand", "u32",
    "u64"]],
  ["Files, I/O & time", ["async", "cli", "dotenv", "io", "io_buffered",
    "log", "path", "signal", "stream", "time"]],
  ["Networking", ["fetch", "headers", "http", "platform", "tcp"]],
  ["Testing", ["fuzz", "mock_platform", "sim", "test"]],
  ["WebAssembly", ["wasm_component", "wasm_convert", "wasm_encode",
    "wasm_imports", "wasm_inst", "wasm_leb128", "wasm_memory",
    "wasm_module", "wasm_numeric", "wasm_sections"]],
  ["Interop", ["jni"]],
];

// Reads what ferndoc actually emitted rather than trusting the table
// above, so a clean checkout (pages gitignored, generator not yet run)
// degrades to just the overview link instead of a sidebar full of 404s.
function stdlibSidebar() {
  const dir = fileURLToPath(new URL("./src/content/docs/stdlib", import.meta.url));
  const present = new Set(
    (existsSync(dir) ? readdirSync(dir) : [])
      .filter((f) => f.endsWith(".md") && f !== "index.md")
      .map((f) => f.slice(0, -3)),
  );

  const items = [{ label: "Overview", link: "/stdlib/" }];
  const grouped = new Set();

  for (const [label, slugs] of STDLIB_GROUPS) {
    const found = slugs.filter((s) => present.has(s));
    found.forEach((s) => grouped.add(s));
    if (found.length === 0) continue;
    items.push({
      label,
      collapsed: true,
      items: found.map((s) => ({ label: s, link: `/stdlib/${s}/` })),
    });
  }

  const ungrouped = [...present].filter((s) => !grouped.has(s)).sort();
  if (ungrouped.length > 0) {
    items.push({
      label: "Other",
      collapsed: true,
      items: ungrouped.map((s) => ({ label: s, link: `/stdlib/${s}/` })),
    });
  }
  return items;
}

export default defineConfig({
  site: siteUrl,
  base,
  trailingSlash: "ignore",
  integrations: [
    starlight({
      title: "Fern",
      logo: {
        src: "./src/assets/fern-logo.svg",
        alt: "Fern",
      },
      description: tagline,
      // The Open Graph / Twitter card tags Starlight doesn't emit itself.
      // (The two webfonts are self-hosted — see ./src/styles/fonts.css in
      // `customCss` below.)
      head: [
        {
          tag: "meta",
          attrs: { property: "og:type", content: "website" },
        },
        {
          tag: "meta",
          attrs: { property: "og:site_name", content: "Fern" },
        },
        {
          tag: "meta",
          attrs: { property: "og:description", content: tagline },
        },
        {
          tag: "meta",
          attrs: { property: "og:image", content: ogImage },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:width", content: "1200" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:height", content: "630" },
        },
        {
          tag: "meta",
          attrs: { property: "og:image:alt", content: tagline },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:card", content: "summary_large_image" },
        },
        {
          tag: "meta",
          attrs: { name: "twitter:image", content: ogImage },
        },
        {
          tag: "meta",
          attrs: { name: "theme-color", content: "#0c1512" },
        },
      ],
      // Register Fern's own TextMate grammar — the one the VS Code
      // extension ships, imported from src/lib/grammar.mjs so the docs,
      // the landing page and the editor all colour from one file — so
      // ```fern fences highlight as actual Fern rather than borrowing
      // TypeScript's grammar: method receivers,
      // `match`, sized integer types, generics, f-strings, and the
      // pipe operator all colour correctly instead of being approximated
      // by TypeScript's grammar.
      expressiveCode: {
        shiki: {
          langs: [fernGrammar],
        },
        // The same theme pair the landing page's plain-Shiki blocks use,
        // so a snippet looks identical on either side of the site.
        themes: [CODE_THEMES.dark, CODE_THEMES.light],
        // Match the site's own chrome: the same hairline radius the
        // specimen panels use, and code set in IBM Plex Mono via the
        // shared `--sl-font-mono` token rather than a second stack.
        styleOverrides: {
          borderRadius: "0.4rem",
          codeFontFamily: "var(--sl-font-mono)",
          uiFontFamily: "var(--sl-font-mono)",
          frames: {
            frameBoxShadowCssValue: "none",
          },
        },
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/JakeChampion/lang",
        },
      ],
      // Eight groups, in the order someone meets them: decide whether you
      // want it, learn it, look things up, then read how it is built. The
      // landing page at `/` is not in the docs collection (see
      // src/pages/index.astro) so it has no entry here; Starlight's site
      // title links to it.
      //
      // A label may appear only ONCE across the whole tree — the docs test
      // suite matches sidebar entries by exact text, so a second "Install"
      // would make those matches ambiguous.
      sidebar: [
        {
          label: "Start here",
          items: [
            { label: "Why Fern", link: "/why/" },
            { label: "Project status", link: "/status/" },
            {
              // Bare `/playground/` — Starlight prepends `base` to
              // absolute sidebar links automatically, so don't double-
              // prefix it here; `${base}` produced `/lang/lang/playground/`.
              label: "Playground",
              link: "/playground/",
              attrs: { target: "_blank" },
            },
          ],
        },
        // Starlight 0.39 removed the `{label, autogenerate}`
        // shorthand — groups now wrap autogenerate inside their
        // items list. Same end result; one extra layer.
        {
          label: "Tutorial",
          items: [{ autogenerate: { directory: "tutorial" } }],
        },
        { label: "Cookbook", link: "/cookbook/" },
        {
          label: "Language reference",
          items: [{ autogenerate: { directory: "reference" } }],
        },
        {
          label: "Standard library",
          items: stdlibSidebar(),
        },
        {
          label: "The compiler",
          items: [{ autogenerate: { directory: "compiler" } }],
        },
        {
          label: "Project",
          items: [
            { label: "Releases", link: "/releases/" },
            { label: "Contributing", link: "/contributing/" },
          ],
        },
      ],
      // fonts.css first: the @font-face rules it generates are what
      // fern.css's `--fern-font-display` / `--fern-font-mono` stacks
      // resolve to. Both are bundled into the same stylesheet, so this is
      // one request rather than the three (two preconnects + a
      // cross-origin stylesheet) the Google Fonts link used to cost.
      customCss: [
        "./src/styles/tokens.css",
        "./src/styles/fonts.css",
        "./src/styles/fern.css",
        "./src/styles/embed.css",
      ],
      lastUpdated: true,
      editLink: {
        baseUrl:
          "https://github.com/JakeChampion/lang/edit/main/site/",
      },
    }),
  ],
});
