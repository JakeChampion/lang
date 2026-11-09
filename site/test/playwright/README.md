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
( cd .. && ./web/build.sh && web/stage.sh site/public/playground )
../bin/fern-selfhost -doc -o src/content/docs/stdlib ../internal/stdlib/std
../bin/fern-selfhost -doc -o src/content/docs/stdlib ../internal/stdlib/core
npm install
npm run build       # → site/dist/

# Run the suite.
cd test/playwright
npm install
npx playwright install --with-deps chromium
npm test
```

`playwright.config.ts` launches `astro preview` against
`site/dist/` on port 4321.

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

## Lighthouse

`node lighthouse.mjs` builds no site: run it after `npm run build`. It
serves `site/dist/` with `astro preview`, runs Lighthouse with the
Playwright Chromium on a few pages, and fails when a score is below its
threshold or a page shifts its layout by more than 0.1. The pages and
the thresholds are at the top of the script, and the Docs build lane
runs it after the suite.

