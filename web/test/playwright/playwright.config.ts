// Playwright config for the lang playground regression suite.
//
// Spins up a local `python3 -m http.server` against `web/` and runs
// every spec under ./ against it. Single chromium project — the
// playground exercises the same bundle everywhere, so per-browser
// matrix is overkill.
//
// `webServer.url` polls until the page returns 200 so the suite
// doesn't race the wasm build. retries=1 absorbs the occasional
// flaky paint timing without masking real bugs (a 2-retries-then-fail
// pattern would).

import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT
  ? Number(process.env.PLAYWRIGHT_PORT)
  : 8742;

export default defineConfig({
  testDir: ".",
  // The default is "**/*.spec.{ts,js}" but explicit beats implicit
  // here so a stray .ts in the dir doesn't get picked up.
  testMatch: /.*\.spec\.ts$/,
  fullyParallel: false, // one server, sequential keeps log lines coherent
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: "on-first-retry",
    actionTimeout: 5_000,
    navigationTimeout: 15_000,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    // Build the wasm bundle then serve web/ on the configured port.
    // build.sh is idempotent — re-running it overwrites lang.wasm
    // with the latest source.
    command: `bash -c "./web/build.sh && python3 -m http.server --bind 127.0.0.1 --directory web ${port}"`,
    url: `http://127.0.0.1:${port}/index.html`,
    cwd: "../../..",
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
