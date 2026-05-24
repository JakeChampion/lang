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

import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

const base = process.env.SITE_BASE ?? "/lang";

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
        "Fern — a small, fast-startup language with native arm64 / x86-64 / wasm backends.",
      // Fern has no dedicated Shiki grammar yet; it started TS-flavoured,
      // so alias ```fern code fences to TypeScript highlighting. Keeps
      // the snippets coloured and silences the "language not found" warn.
      expressiveCode: {
        shiki: {
          langAlias: { fern: "typescript" },
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
