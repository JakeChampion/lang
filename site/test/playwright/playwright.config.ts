// Playwright config for the lang docs site.
//
// Drives `astro preview` (serves the built site/dist/ under the
// configured `base` of `/lang/`) on a fixed port. Single chromium
// project — docs render the same everywhere, so per-browser
// matrix is overkill.
//
// `webServer.url` polls the docs root until 200 so the suite
// doesn't race a slow Astro startup.

import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_DOCS_PORT
  ? Number(process.env.PLAYWRIGHT_DOCS_PORT)
  : 4321;

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts$/,
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? "github" : "list",
  use: {
    // The docs site builds under `base: "/lang"`, so navigation
    // happens via this prefix. The webServer command below
    // arranges Astro preview to serve it at /lang/ on `port`.
    baseURL: `http://127.0.0.1:${port}/lang`,
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
    // `astro preview` serves site/dist/ under the site's
    // configured base. `--port` overrides the default 4321.
    // Run from the site/ directory; cwd is two levels up from
    // this config (site/test/playwright/ → site/).
    command: `astro preview --host 127.0.0.1 --port ${port}`,
    url: `http://127.0.0.1:${port}/lang/`,
    cwd: "../..",
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
