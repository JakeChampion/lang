# Playground end-to-end tests (Bombadil)

[Bombadil](https://github.com/antithesishq/bombadil) is a property-
based browser tester from Antithesis. It opens the page in a real
Chromium, autonomously fuzzes it (random clicks, typing, scrolling),
and checks a set of invariants against every state it reaches.

The spec lives in [`playground.spec.ts`](playground.spec.ts) and
encodes things that must always be true about the playground:

- The wasm runtime always loads (no "wasm load failed").
- The output pane never shows a JS-side exception ("JS error:").
- The Problems count text matches the visible list-item count.
- Once mounted, the CodeMirror editor stays mounted.
- After boot, the Run button is reachable (not disabled).

## Run locally

1. Install Bombadil from the [release page](https://github.com/antithesishq/bombadil/releases):

   ```bash
   curl -L -o /usr/local/bin/bombadil \
     https://github.com/antithesishq/bombadil/releases/download/v0.4.2/bombadil-x86_64-linux
   chmod +x /usr/local/bin/bombadil
   ```

   Pick the binary matching your host (`bombadil-x86_64-linux`,
   `bombadil-aarch64-linux`, `bombadil-x86_64-darwin`,
   `bombadil-aarch64-darwin`).

2. Install a Chromium-compatible browser (Chrome, Chromium, Brave,
   Edge — Bombadil will auto-detect, or you can point it via the
   `CHROME` env var below).

3. Install the TypeScript typings if you plan to edit the spec
   with editor support (optional — the binary doesn't need them):

   ```bash
   cd web/test && npm install
   ```

4. Run:

   ```bash
   ./web/test/run.sh
   ```

   Results land in `web/test/results/`. Property violations
   surface as a non-zero exit code plus a reproducer trace in the
   results directory.

## Knobs

| Env var               | Meaning                                                   |
| --------------------- | --------------------------------------------------------- |
| `BOMBADIL`            | Path to the bombadil binary (defaults to `bombadil`).     |
| `CHROME`              | Path to the Chromium-compatible browser.                  |
| `BOMBADIL_OUTPUT`     | Results directory (defaults to `web/test/results`).       |
| `BOMBADIL_EXTRA_ARGS` | Extra flags appended verbatim to the bombadil invocation. |

## CI

The `.github/workflows/playground-e2e.yml` workflow runs this same
script on every PR that touches the playground build inputs. It
downloads the Bombadil binary cached on
`(host-arch, BOMBADIL_VERSION)` so subsequent runs skip the fetch.
A property violation fails the workflow with the reproducer attached
as a build artefact.
