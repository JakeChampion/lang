# lang docs site

Astro + Starlight build of the documentation that publishes to
GitHub Pages at the repo root, with the playground bundled at
`/playground/`.

## Layout

```
site/
├── astro.config.mjs            Starlight + sidebar config
├── package.json
├── src/
│   ├── content/docs/
│   │   ├── index.mdx           Landing page
│   │   ├── tutorial/*.md(x)    Narrative learn-the-language
│   │   ├── reference/*.md      Syntax, types, tooling
│   │   └── stdlib/*.md         Auto-generated (gitignored)
│   ├── components/
│   │   └── LangPlayground.astro  Embedded playground iframe
│   └── styles/lang.css         Brand accent overrides
└── public/playground/          Playground bundle (gitignored,
                                  copied in at build time)
```

## Develop locally

```bash
cd site
npm install

# Build the playground bundle once + stage it under public/.
( cd .. && ./web/build.sh )
mkdir -p public/playground
cp -L ../web/index.html ../web/wasm_exec.js ../web/lang.wasm public/playground/

# Generate the stdlib reference pages.
go run ../cmd/langdoc -out src/content/docs/stdlib/

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

## Embed a live snippet

```mdx
---
title: My page
---

import LangPlayground from "../../components/LangPlayground.astro";

<LangPlayground
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

`cmd/langdoc` parses `internal/stdlib/std/*.lang` for every public
declaration + the doc comment immediately above it, then emits
one Markdown page per module. The output lives under
`src/content/docs/stdlib/` and is gitignored — committing it
would put generated content under review with no source-of-truth.

If you want to expand or correct the reference, edit the doc
comments in the source `.lang` files. The next build will pick
them up.
