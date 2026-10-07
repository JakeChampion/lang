# Fern docs site

Astro + Starlight build of the documentation that publishes to
GitHub Pages at the repo root, with the playground bundled at
`/playground/`.

## Layout

```
site/
├── astro.config.mjs            Starlight + sidebar + meta config
├── package.json
├── scripts/
│   ├── fetch-fonts.mjs         Refresh the self-hosted webfonts
│   └── render-og.mjs           Render the social card to public/og.png
├── src/
│   ├── content/docs/
│   │   ├── index.mdx           Landing page
│   │   ├── *.md                Cookbook, why Fern, releases, contributing
│   │   ├── tutorial/*.md(x)    Narrative learn-the-language
│   │   ├── reference/*.md      Syntax, types, traits, packages, tooling
│   │   └── stdlib/*.md         Auto-generated (gitignored) except index.md
│   ├── assets/
│   │   ├── fonts/              Self-hosted woff2 + OFL.txt
│   │   ├── fern-logo.svg       The frond mark
│   │   └── og-card.svg         Social-card source
│   ├── components/
│   │   ├── FernPlayground.astro  Embedded playground iframe
│   │   ├── CommandCard.astro     Copyable shell commands
│   │   ├── Facet.astro           One entry in the landing rail
│   │   ├── FacetRail.astro       The landing feature rail
│   │   ├── NextSteps.astro       Closing signpost grid
│   │   └── SpecSheet.astro       Label/value fact panel
│   └── styles/
│       ├── fern.css            Theme: palette, type, landing layout
│       └── fonts.css           @font-face rules (generated)
├── public/og.png               Social card (generated)
├── public/playground/          Playground bundle (gitignored,
│                                 copied in at build time)
└── test/playwright/            Browser tests run by the Docs build lane
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
in IBM Plex Mono, both self-hosted: `src/styles/fonts.css` is generated
by `npm run fonts`, which downloads the woff2 files into
`src/assets/fonts/` (latin + latin-ext, SIL OFL 1.1). They load
`display=swap` behind real fallback stacks, so if a font never arrives
the page still reads correctly.

Two rules worth keeping:

- **Landing components carry `not-content`.** Starlight's markdown
  stylesheet adds a 1rem margin between *any* adjacent siblings inside
  `.sl-markdown-content`, which pulls tight custom layouts apart. The
  components opt out, and `fern.css` styles their links and inline code
  itself.
- **Every reveal is optional.** The landing page's staggered entrance and
  all hover transforms are disabled under `prefers-reduced-motion`.

## Develop locally

You need Go and Node 22.12 or later; `eval "$(scripts/toolchain-env)"`
from the repository root installs the pinned Go.

```bash
cd site
npm install

# Build the playground bundle once + stage it under public/.
# build.sh needs bin/fern-selfhost and runs `make selfhost-cli` when it
# is missing (or set FERN_SELFHOST to a compiler you already have).
( cd .. && ./web/build.sh )
mkdir -p public/playground
# Copy every .html/.js/.wasm, as pages.yml does: index.html statically
# imports both wasi shims, so a missing one breaks the whole ES module
# and the playground hangs on "loading runtime…".
cp -L ../web/*.html ../web/*.js ../web/*.wasm public/playground/

# Generate the stdlib reference pages.
go run ../cmd/ferndoc -out src/content/docs/stdlib/

# Run the dev server (live-reload on .md changes).
npm run dev
```

`web/build.sh` writes `playground.wasm` (Fern's compiler built for WASI,
which checks, runs and compiles the editor's program) and `fern.wasm`
(the Go language server behind diagnostics, hover and completion).

Open `http://localhost:4321/lang/` (Starlight serves under the
configured `base`).

## Build for production

```bash
npm run build    # → site/dist/
```

The GitHub Actions `Pages` workflow runs this on every push to
main that touches the site, the playground, the compiler, anything
under `internal/` (the stdlib included), the syntax grammar, or the
toolchain pins. On pull requests the `Docs build` lane runs the same
build and then the Playwright suite in `test/playwright/`.

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

`cmd/ferndoc` parses `internal/stdlib/std/` and `internal/stdlib/core/`
for every public declaration + the doc comment immediately above it,
then emits one Markdown page per module. A nested module's page
name joins its path with `_` (`std/crypto/x25519.fern` →
`crypto_x25519.md`). The output lives under
`src/content/docs/stdlib/` and is gitignored — committing it
would put generated content under review with no source-of-truth.

If you want to expand or correct the reference, edit the doc
comments in the source `.fern` files. The next build will pick
them up.

The stdlib sidebar is grouped by purpose through `STDLIB_GROUPS` in
`astro.config.mjs`. A module missing from that table still gets a
page, listed under "Other", so add a new module to a group.
