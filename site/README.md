# Fern docs site

Astro + Starlight build of the documentation at
[fern-lang.netlify.app](https://fern-lang.netlify.app), with the
playground bundled at `/playground/`.

## Layout

```
site/
├── astro.config.mjs            Starlight + sidebar + meta config
├── package.json
├── scripts/
│   ├── capture.mjs             Record compiler output for the home page
│   ├── fetch-fonts.mjs         Refresh the self-hosted webfonts
│   └── render-og.mjs           Render the social card to public/og.png
├── src/
│   ├── content/docs/
│   │   ├── index.mdx           Landing page
│   │   ├── *.md                Cookbook, why Fern, releases, contributing
│   │   ├── coreutils.mdx       Coreutils parity + bench tables
│   │   ├── tutorial/*.md(x)    Narrative learn-the-language
│   │   ├── http/*.md(x)        The HTTP guide, basic to advanced
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
│   │   ├── CoreutilsBench.astro  Tables from the newest coreutils bench log
│   │   └── SpecSheet.astro       Label/value fact panel
│   ├── lib/
│   │   └── coreutils-bench.mjs Parses docs/coreutils-log/*-bench.md
│   └── styles/
│       ├── fern.css            Theme: palette, type, landing layout
│       └── fonts.css           @font-face rules (generated)
├── public/og.png               Social card (generated)
├── public/playground/          Playground bundle (gitignored,
│                                 copied in at build time)
├── test/*.test.mjs             Unit tests (`npm test`) run by the Docs build lane
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
( cd .. && web/stage.sh site/public/playground )

# Generate the stdlib reference pages.
scripts/stdlib-docs.sh

# Run the dev server (live-reload on .md changes).
npm run dev
```

`web/build.sh` writes `playground.wasm`: Fern's compiler built for WASI,
which checks, runs and compiles the editor's program and serves its
diagnostics, hover and completion.

Open `http://localhost:4321/`.

## Build for production

```bash
npm run build    # → site/dist/
```

`npm run build` runs `scripts/capture.mjs` first. The script runs the
compiler (`$FERN_SELFHOST`, else `../bin/fern-selfhost`) and records its
output in `src/generated/captures.json`, which is not in git. The home
page shows that output: the assembly for `src/captures/area.fern`, the
size of the executable, the `fern -help` usage text, and the
capabilities of `fern -lsp`, and the diagnostics for the programs in
`src/captures/diagnostics/`. The script stops the build when `main` no
longer loads the folded constant that the home page names, or when a
diagnostic program stops failing.

`scripts/startup-bench` times a hello-world program from process start
to exit in Fern and in other languages. `.github/workflows/startup-bench.yml`
runs it each week on a Linux x86-64 runner and opens a pull request with
the log in `docs/startup-log/`. The home page shows the newest log, and
shows no startup table before the first one.

`public/install.sh` is the install script that the home page and the
install tutorial give. `test/install.test.mjs` runs it against a fake
release.

Netlify builds and deploys the site from `main`, through
`netlify.toml` at the repo root and `scripts/netlify-build`. On pull
requests the `Docs build` lane runs the same build and then the
Playwright suite in `test/playwright/`. `netlify.toml` redirects old
`/lang/...` links to the root.

`scripts/check-links.mjs --dist dist` finds each internal link in a
build that names no file. The Docs build lane runs it. With
`--site URL`, it reads the sitemap of a deployed site and requests each
internal link. `.github/workflows/site-links.yml` runs that against the
site every 6 hours, and opens an issue when a link is broken.
`.github/workflows/preview-links.yml` runs it against the Netlify deploy
preview of each pull request, when Netlify reports the preview as ready,
and sets a `site-links/preview` status on the commit.

## Social cards and llms.txt

Each docs page except the home page has a social card at
`/og/PAGE.png`, which `src/pages/og/[...slug].png.ts` renders during
the build. `src/lib/og-card.mjs` draws the text as SVG paths from the
static TTFs in `src/assets/fonts/card/`, so a card does not depend on
the fonts of the build machine. The home page keeps `public/og.png`.

`/llms.txt` is an index of the docs in the llmstxt.org format.
`/llms-full.txt` holds the text of every guide and reference page, and
`/llms-stdlib.txt` holds every standard-library module.
`src/lib/llms.mjs` makes them from the Markdown sources, and changes
MDX components to plain Markdown.

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
so URLs are interchangeable. Give `args="…"` and `stdin={`…`}` to
run the snippet with arguments and standard input. The embed then
shows its program-input panel, so the reader can change both.

Code fences and the playground editor take their token colours from
`web/editor/fern-themes.json`, so a snippet and a fence colour the same
token the same way.

## Add a new tutorial / reference page

1. Drop a `*.md` or `*.mdx` under `src/content/docs/tutorial/` or
   `src/content/docs/reference/`.
2. Set `title` + `description` in the frontmatter. Optionally
   `sidebar.order: N` to control sidebar position.
3. The sidebar regenerates on the next `npm run dev` / `npm run
   build`.

Every complete program in a ```` ```fern ```` fence — one that imports
something and defines `main` or `handle` — is compiled by
`scripts/check-site-examples` in the Docs build lane, so an example that
stops building fails the PR. Run it locally after `make build`; a fence
written ```` ```fern target=wasm32-wasi-http ```` is compiled for that
target.

## Stdlib reference is auto-generated

`fern -doc` parses `internal/stdlib/std/` and `internal/stdlib/core/`
for every public declaration + the doc comment immediately above it,
then emits one Markdown page per module. A nested module's page
name joins its path with `_` (`std/crypto/x25519.fern` →
`crypto_x25519.md`). The output lives under
`src/content/docs/stdlib/` and is gitignored — committing it
would put generated content under review with no source-of-truth.

If you want to expand or correct the reference, edit the doc
comments in the source `.fern` files. The next build will pick
them up.

`scripts/stdlib-docs.sh` runs `fern -doc` with `-doc-source`, so each
declaration links to its line in the source on GitHub. For a literate
`.fern.md` module, the link names the line in the document, not in the
tangled source. The "Edit page" link of a module page names the
module's source too. `/stdlib/functions/` searches every declaration,
from the `/stdlib-index.json` that the build makes.

The stdlib sidebar is grouped by purpose through `STDLIB_GROUPS` in
`astro.config.mjs`. A module missing from that table still gets a
page, listed under "Other", so add a new module to a group.
