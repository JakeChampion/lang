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
const siteUrl = process.env.SITE_URL ?? "https://jakechampion.github.io";

// One-line summary reused for the meta description, the Open Graph
// card, and Starlight's own `description` — so search results, chat
// unfurls, and the site itself all say the same thing.
const tagline =
  "Fern is a small statically typed language that compiles to a fast standalone binary — or to WebAssembly, from the same source. No runtime, no garbage collector, nothing else to install.";

// Social-card image. Crawlers won't resolve a relative URL, so this has to
// be absolute — built from the same two vars the rest of the site's URLs
// come from, so a Netlify deploy (SITE_URL set, base rewritten to root)
// points at its own copy rather than the Pages one. Rendered by
// `npm run og` from src/assets/og-card.svg.
const ogImage = `${siteUrl.replace(/\/$/, "")}${base.replace(/\/$/, "")}/og.png`;

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
      // Two webfonts, both `display=swap` behind real fallback stacks
      // so a blocked or slow font-CDN costs layout only, never content:
      // Fraunces (variable, with its SOFT + WONK axes) sets headings and
      // the landing page's display type, IBM Plex Mono carries code and
      // the small-caps metadata labels. Plus the Open Graph / Twitter
      // card tags Starlight doesn't emit itself.
      head: [
        {
          tag: "link",
          attrs: { rel: "preconnect", href: "https://fonts.googleapis.com" },
        },
        {
          tag: "link",
          attrs: {
            rel: "preconnect",
            href: "https://fonts.gstatic.com",
            crossorigin: "anonymous",
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "stylesheet",
            href:
              "https://fonts.googleapis.com/css2" +
              "?family=Fraunces:opsz,wght,SOFT,WONK@9..144,400..700,0..100,0..1" +
              "&family=IBM+Plex+Mono:wght@400;500;600" +
              "&display=swap",
          },
        },
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
          attrs: { name: "theme-color", content: "#143026" },
        },
      ],
      // Register Fern's own TextMate grammar (loaded above) so ```fern
      // fences get language-accurate highlighting — method receivers,
      // `match`, sized integer types, generics, f-strings, and the
      // pipe operator all colour correctly instead of being approximated
      // by TypeScript's grammar.
      expressiveCode: {
        shiki: {
          langs: [fernGrammar],
        },
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
