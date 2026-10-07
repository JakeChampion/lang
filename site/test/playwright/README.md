# Docs site browser tests

Scripted Playwright regressions for the Fern docs site
(`site/` → Astro + Starlight). Covers what the
`docs-build` job can't: navigation, sidebar grouping, the
embedded playground iframe loading, stdlib pages reachable,
search trigger working.

## Run locally

```bash
# Build everything the docs depend on first, as pages.yml does.
cd ../..            # back to site/
( cd .. && ./web/build.sh )
mkdir -p public/playground
cp -L ../web/*.html ../web/*.js ../web/*.wasm public/playground/
go run ../cmd/ferndoc -out src/content/docs/stdlib/
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

[`docs.spec.ts`](docs.spec.ts): the landing page and its links, the
tutorial, reference and stdlib sidebars (including the stdlib's grouping,
with nothing left under "Other"), a generated stdlib page, the embedded
playground loading and running, the releases page, and the search dialog.
Each test's name says what it checks.

Add a spec by dropping another `*.spec.ts`. One test = one
feature = one assertion; the test name should read as the bug
report.

## CI

`.github/workflows/docs-build.yml` runs this suite on every PR
that touches docs inputs. Failures block merging.
