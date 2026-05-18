# Playground regression tests (Playwright)

Scripted golden-path tests for the lang playground. Complements the
property-based [Bombadil suite](../README.md) by gating per-PR on a
small set of deterministic scenarios the playground must always
pass.

## Run locally

```bash
cd web/test/playwright
npm install
npx playwright install --with-deps chromium
npm test
```

`npm test` shells out to `playwright test`, which (per
[`playwright.config.ts`](playwright.config.ts)) runs
`./web/build.sh` to refresh the wasm bundle, starts a local
`python3 -m http.server` on port 8742, and walks the spec files in
this directory.

## What's covered

[`playground.spec.ts`](playground.spec.ts):

- Page boot completes and the Run button enables.
- The default "hello" example runs and prints to the output pane.
- Selecting an example from the dropdown loads it into the editor
  and Run produces the expected output.
- Typing a syntax error surfaces a diagnostic in the Problems
  panel.
- A clean source clears the Problems panel.
- Clicking Share updates the URL hash with the `#src=` payload.

Add more specs by dropping a `*.spec.ts` file in this directory.
Each test should scope itself to one feature with one assertion —
when CI fails, the test name is the bug report.

## CI

`.github/workflows/playground-e2e.yml` runs both this suite and the
Bombadil run on every PR that touches playground inputs. Playwright
is the deterministic regression gate; Bombadil is the exploratory
fuzzer. Failures from either block the PR.
