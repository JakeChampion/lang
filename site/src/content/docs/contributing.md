---
title: Contributing to the docs
description: How this documentation site is built, and how to add or edit a page.
---

This site is an [Astro](https://astro.build) +
[Starlight](https://starlight.astro.build) project living under `site/`
in the [repository][repo]. It publishes to GitHub Pages, with the
interactive [playground](/lang/playground/) bundled alongside it. This
page is for anyone improving the docs themselves.

## Run the site locally

```bash
cd site
npm install

# Build the in-browser playground bundle and stage it under public/.
( cd .. && ./web/build.sh )
mkdir -p public/playground
cp -L ../web/index.html ../web/wasm_exec.js ../web/fern.wasm public/playground/

# Generate the standard-library reference pages (see below).
go run ../cmd/ferndoc -out src/content/docs/stdlib/

npm run dev      # live-reload dev server
```

Open `http://localhost:4321/lang/` — Starlight serves under the
configured `base`. `npm run build` produces the production output in
`site/dist/`.

## Add or edit a page

Tutorial and reference pages are Markdown (`.md`) or MDX (`.mdx`) files
under `src/content/docs/`:

- **Tutorials** — `src/content/docs/tutorial/`
- **Reference** — `src/content/docs/reference/`

Each page needs `title` and `description` frontmatter; `sidebar.order`
controls its position within the section:

```mdx
---
title: My new page
description: One-line summary for search + social cards.
sidebar:
  order: 5
---
```

The sidebar regenerates on the next `npm run dev` / `npm run build`. To
add a whole new top-level section, edit the `sidebar` array in
`astro.config.mjs`.

## Embed a runnable snippet

Use the `FernPlayground` component from an MDX page (note the `.mdx`
extension) to drop a live, in-browser-runnable editor inline:

```mdx
import FernPlayground from "../../components/FernPlayground.astro";

<FernPlayground
  code={`function main(): i32 {
    print("hello");
    return 0;
}`}
/>
```

It base64-URL-encodes the snippet into the playground iframe's hash —
the same codec the standalone playground's **Share** button uses, so the
URLs are interchangeable. Pass `autoRun={false}` for snippets where the
reader should predict the output before running, or `client:visible`-style
`height="320px"` to size the frame.

## The standard-library reference is generated

Pages under `stdlib/` are **machine-generated** by
[`cmd/ferndoc`][ferndoc] from the source modules — it parses every public
declaration in `internal/stdlib/std/*.fern` **and** `internal/stdlib/core/*.fern`
(functions, structs, enums, consts, and traits + a per-trait
implementations table) along with the doc comment immediately above each
one. The output is gitignored, so it never lands in review.

That means you **don't edit the `stdlib/*.md` files** — to fix or expand
the reference, edit the doc comment on the declaration in the source
`.fern` file. The next build regenerates the page. (`stdlib/index.md` is
the one hand-written page in that folder.)

## Syntax highlighting

` ```fern ` code fences are highlighted with Fern's own TextMate grammar
(`editors/vscode/syntaxes/fern.tmLanguage.json`, the same one the VS Code
extension ships), wired into Shiki in `astro.config.mjs`. Editing that
grammar improves highlighting across both the docs and the editor at
once; the docs build is set to re-run when it changes.

## Contributing to the language itself

Code changes (compiler, standard library, tooling) live in the same
[repository][repo]. Start from the project notes in `CLAUDE.md` and the
design documents under `docs/`, and follow the existing test
conventions — every feature ships with a test at the layer it touches.

[repo]: https://github.com/JakeChampion/lang
[ferndoc]: https://github.com/JakeChampion/lang/tree/main/cmd/ferndoc
