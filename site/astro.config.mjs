// Astro + Starlight config for the lang documentation site.
//
// Site lives at the repo root URL (https://<user>.github.io/lang/)
// with the playground bundle nested at /playground/ — keeps the
// canonical landing URL on docs (standard for a language project)
// without breaking any existing playground deep links.
//
// Three top-level sections in the sidebar:
//   - Tutorial: narrative learn-the-language pages, ordered.
//   - Reference: syntax + types + tooling.
//   - Stdlib: auto-generated from internal/stdlib/*.lang by
//     `cmd/langdoc`; pages exist under src/content/docs/stdlib/
//     after that build step. Keeping the section here means the
//     sidebar shape is stable even on a clean checkout where
//     langdoc hasn't run yet.
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
      title: "lang",
      description:
        "A small, fast-startup language with native arm64 / x86-64 / wasm backends.",
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/JakeChampion/lang",
        },
      ],
      sidebar: [
        { label: "Overview", link: "/" },
        {
          label: "Tutorial",
          autogenerate: { directory: "tutorial" },
        },
        {
          label: "Reference",
          autogenerate: { directory: "reference" },
        },
        {
          label: "Standard library",
          autogenerate: { directory: "stdlib" },
        },
        {
          label: "Playground",
          link: `${base}/playground/`,
          attrs: { target: "_blank" },
        },
      ],
      customCss: ["./src/styles/lang.css"],
      lastUpdated: true,
      editLink: {
        baseUrl:
          "https://github.com/JakeChampion/lang/edit/main/site/",
      },
    }),
  ],
});
