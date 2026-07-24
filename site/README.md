# Fern docs site

Astro + Starlight build of the documentation that publishes to
GitHub Pages at the repo root, with the playground bundled at
`/playground/`.

## Layout

```
site/
├── astro.config.mjs            Starlight + sidebar + fonts/meta config
├── package.json
├── src/
│   ├── content/docs/
│   │   ├── index.mdx           Landing page
│   │   ├── tutorial/*.md(x)    Narrative learn-the-language
│   │   ├── reference/*.md      Syntax, types, tooling
│   │   └── stdlib/*.md         Auto-generated (gitignored)
│   ├── components/
│   │   ├── FernPlayground.astro  Embedded playground iframe
│   │   ├── CommandCard.astro     Copyable shell commands
│   │   ├── Facet.astro           One entry in the landing rail
│   │   ├── FacetRail.astro       The landing feature rail
│   │   ├── NextSteps.astro       Closing signpost grid
│   │   └── SpecSheet.astro       Label/value fact panel
│   └── styles/fern.css         Theme: palette, type, landing layout
└── public/playground/          Playground bundle (gitignored,
                                  copied in at build time)
```

## Design system

`src/styles/fern.css` is the whole theme, in five commented sections:
tokens, Starlight variable overrides, prose, landing page, playground.
It rides on Starlight's own custom properties — the accent and gray
ramps are redefined once, so the sidebar, nav, search dialog and asides
inherit the palette rather than each needing an override.

The identity is a botanist's field guide: pressed-frond greens on warm
paper, a muted spore ochre as the single sharp accent (section ticks,
specimen indices, focus ring), hairline rules instead of boxes, and
small-caps monospace for metadata. Headings are set in Fraunces and code
in IBM Plex Mono, both loaded `display=swap` from the font CDN behind
real fallback stacks (see the `head` array in `astro.config.mjs`) — if
the fonts never arrive the page still reads correctly.

Two rules worth keeping:

- **Landing components carry `not-content`.** Starlight's markdown
  stylesheet adds a 1rem margin between *any* adjacent siblings inside
  `.sl-markdown-content`, which pulls tight custom layouts apart. The
  components opt out, and `fern.css` styles their links and inline code
  itself.
- **Every reveal is optional.** The landing page's staggered entrance and
  all hover transforms are disabled under `prefers-reduced-motion`.

## Develop locally

```bash
cd site
npm install

# Build the playground bundle once + stage it under public/.
( cd .. && ./web/build.sh )
mkdir -p public/playground
# index.html statically imports both shims, so a missing one breaks the
# whole ES module and the playground hangs on "loading runtime…".
cp -L ../web/index.html ../web/wasm_exec.js ../web/wasi-shim.js \
      ../web/wasi-http-shim.js ../web/fern.wasm public/playground/

# Generate the stdlib reference pages.
go run ../cmd/ferndoc -out src/content/docs/stdlib/

# Run the dev server (live-reload on .md changes).
npm run dev
```

Open `http://localhost:4321/lang/` (Starlight serves under the
configured `base`).

## Build for production

```bash
npm run build    # → site/dist/
```

The GitHub Actions `Pages` workflow runs this on every push to
main that touches the playground build inputs, the docs source,
or the stdlib.

The same bundle also deploys to Netlify, via `netlify.toml` at the
repo root and `scripts/netlify-build` (which mirrors the `Pages`
workflow's steps). The one host-specific wrinkle is `base`: Astro
bakes the `/lang` prefix into every internal link but does *not*
nest `dist/` to match, so Pages lines the two up by serving `dist/`
at `/lang/`, while Netlify serves `dist/` at the domain root and
rewrites `/lang/*` back down. Keep the two build recipes in step.

## Embed a live snippet

```mdx
---
title: My page
---

import FernPlayground from "../../components/FernPlayground.astro";

<FernPlayground
  code={`function main(): i32 {
    print("hello");
    return 0;
}`}
/>
```

The component base64-URL-encodes the snippet into the iframe's
hash — same codec the standalone playground's Share button uses,
so URLs are interchangeable.

## Add a new tutorial / reference page

1. Drop a `*.md` or `*.mdx` under `src/content/docs/tutorial/` or
   `src/content/docs/reference/`.
2. Set `title` + `description` in the frontmatter. Optionally
   `sidebar.order: N` to control sidebar position.
3. The sidebar regenerates on the next `npm run dev` / `npm run
   build`.

## Stdlib reference is auto-generated

`cmd/ferndoc` parses `internal/stdlib/std/*.fern` for every public
declaration + the doc comment immediately above it, then emits
one Markdown page per module. The output lives under
`src/content/docs/stdlib/` and is gitignored — committing it
would put generated content under review with no source-of-truth.

If you want to expand or correct the reference, edit the doc
comments in the source `.fern` files. The next build will pick
them up.
