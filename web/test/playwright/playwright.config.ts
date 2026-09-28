// Playwright config for the Fern playground regression suite.
//
// Spins up a local `python3 -m http.server` against `web/` and runs
// every spec under ./ against it. Single chromium project — the
// playground exercises the same bundle everywhere, so per-browser
// matrix is overkill.
//
// The server only serves: `web/build.sh` takes minutes, so it runs before
// the suite (`npm test` runs it first; CI as its own step) rather than
// inside `webServer.timeout`. retries=1 absorbs the occasional
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
    command: `python3 -m http.server --bind 127.0.0.1 --directory web ${port}`,
    url: `http://127.0.0.1:${port}/index.html`,
    cwd: "../../..",
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
