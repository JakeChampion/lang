// Astro + Starlight config for the Fern documentation site.
//
// Site lives at the repo root URL (https://<user>.github.io/lang/)
// with the playground bundle nested at /playground/ — keeps the
// canonical landing URL on docs (standard for a language project)
// without breaking any existing playground deep links.
//
// Three top-level sections in the sidebar:
//   - Tutorial: narrative learn-the-language pages, ordered.
//   - Reference: syntax + types + tooling.
//   - Stdlib: auto-generated from internal/stdlib/*.fern by
//     `cmd/ferndoc`; pages exist under src/content/docs/stdlib/
//     after that build step. Keeping the section here means the
//     sidebar shape is stable even on a clean checkout where
//     ferndoc hasn't run yet.
//
// `base` is set so the site works under GitHub Pages' project
// subpath (https://<user>.github.io/lang/) without absolute-URL
// breakage; override via `SITE_BASE` env if deploying elsewhere.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

const base = process.env.SITE_BASE ?? "/lang";

// Load the real Fern TextMate grammar (the same one the VS Code
// extension ships) so ```fern code fences highlight as actual Fern
// rather than borrowing TypeScript's grammar. Read from the repo
// source so the grammar has a single source of truth — editing the
// extension's grammar updates the docs colours too.
const fernGrammar = JSON.parse(
  readFileSync(
    fileURLToPath(
      new URL(
        "../editors/vscode/syntaxes/fern.tmLanguage.json",
        import.meta.url,
      ),
    ),
    "utf8",
  ),
);

export default defineConfig({
  site: process.env.SITE_URL ?? "https://jakechampion.github.io",
  base,
  trailingSlash: "ignore",
  integrations: [
    starlight({
      title: "Fern",
      logo: {
        src: "./src/assets/fern-logo.svg",
        alt: "Fern",
      },
      description:
        "Fern — a small, general-purpose, fast-startup language with native arm64 / x86-64 / wasm backends.",
      // Register Fern's own TextMate grammar (loaded above) so ```fern
      // fences get language-accurate highlighting — method receivers,
      // `match`, sized integer types, generics, f-strings, and the
      // pipe operator all colour correctly instead of being approximated
      // by TypeScript's grammar.
      expressiveCode: {
        shiki: {
          langs: [fernGrammar],
        },
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/JakeChampion/lang",
        },
      ],
      sidebar: [
        { label: "Overview", link: "/" },
        // Starlight 0.39 removed the `{label, autogenerate}`
        // shorthand — groups now wrap autogenerate inside their
        // items list. Same end result; one extra layer.
        {
          label: "Tutorial",
          items: [{ autogenerate: { directory: "tutorial" } }],
        },
        {
          label: "Reference",
          items: [{ autogenerate: { directory: "reference" } }],
        },
        {
          label: "Standard library",
          items: [{ autogenerate: { directory: "stdlib" } }],
        },
        { label: "Contributing", link: "/contributing/" },
        {
          label: "Playground",
          // Bare `/playground/` — Starlight prepends `base` to
          // absolute sidebar links automatically, so don't double-
          // prefix it here. (CI's lychee link-check caught the
          // `/lang/lang/playground/` shape the previous `${base}`
          // produced.)
          link: "/playground/",
          attrs: { target: "_blank" },
        },
      ],
      customCss: ["./src/styles/fern.css"],
      lastUpdated: true,
      editLink: {
        baseUrl:
          "https://github.com/JakeChampion/lang/edit/main/site/",
      },
    }),
  ],
});
