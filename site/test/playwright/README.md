# Docs site browser tests

Scripted Playwright regressions for the lang docs site
(`site/` → Astro + Starlight). Covers what the
`docs-build` job can't: navigation, sidebar grouping, the
embedded playground iframe loading, stdlib pages reachable,
search trigger working.

## Run locally

```bash
# Build everything the docs depend on first.
cd ..               # back to site/
./build-deps.sh     # or run the pages.yml steps manually:
                    #   ../web/build.sh
                    #   mkdir -p public/playground && cp ...
                    #   go run ../cmd/ferndoc -out src/content/docs/stdlib/
npm install
npm run build       # → site/dist/

# Run the suite.
cd test/playwright
npm install
npx playwright install --with-deps chromium
npm test
```

`playwright.config.ts` launches `astro preview` against
`site/dist/` on port 4321, serving the site at the configured
`base` (`/lang/`). Tests navigate against that prefix via the
`baseURL` config.

## What's covered

[`docs.spec.ts`](docs.spec.ts):

1. Home page renders the hero + the "Get started" CTA.
2. Tutorial sidebar group lists the install + first-steps pages.
3. Reference / tooling page describes `lang-lsp`.
4. Stdlib index sidebar lists at least string + json modules.
5. Stdlib string page renders the `is_empty` signature.
6. Embedded playground iframe on the first-steps tutorial loads
   with a `/playground/#src=…` URL.
7. Ctrl/Cmd-K opens the Starlight search dialog.

Add a spec by dropping another `*.spec.ts`. One test = one
feature = one assertion; the test name should read as the bug
report.

## CI

`.github/workflows/docs-build.yml` runs this suite on every PR
that touches docs inputs. Failures block merging.
