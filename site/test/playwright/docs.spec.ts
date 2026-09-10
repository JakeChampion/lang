// Scripted regressions for the lang docs site. Asserts behaviour
// the build step doesn't catch: navigation, sidebar groups, the
// embedded playground iframe loading, stdlib pages reachable.
//
// page.goto() paths are RELATIVE (no leading slash) so they
// resolve against the baseURL's `/lang/` prefix. A leading `/`
// would be absolute and drop the prefix — see playwright.config.ts.

import { test, expect } from "@playwright/test";

test("home page renders the bespoke landing hero", async ({ page }) => {
  await page.goto("./");
  await expect(page.locator("h1")).toContainText("Fern");
  await expect(
    page.getByRole("link", { name: /Get started/i }),
  ).toBeVisible();
  // `/` is src/pages/index.astro, NOT a Starlight page — a static route
  // outranks Starlight's `[...slug]`. If that ever stops being true the
  // Starlight chrome reappears here, so assert its absence.
  await expect(page.locator(".sidebar-pane")).toHaveCount(0);
});

test("tutorial sidebar group lists the install page", async ({ page }) => {
  await page.goto("tutorial/install/");
  await expect(page.locator("h1")).toContainText("Install");
  const sidebar = page.locator("nav, aside").first();
  await expect(sidebar.getByText("Install", { exact: true })).toBeVisible();
  await expect(sidebar.getByText("First steps", { exact: true })).toBeVisible();
});

test("reference > tooling page describes fern-lsp", async ({ page }) => {
  await page.goto("reference/tooling/");
  await expect(page.locator("main")).toContainText("fern-lsp");
  await expect(page.locator("main")).toContainText("textDocument/formatting");
});

test("stdlib index links to at least one auto-generated module", async ({
  page,
}) => {
  await page.goto("stdlib/");
  const sidebar = page.locator("nav, aside").first();
  await expect(sidebar).toContainText(/string/i);
  await expect(sidebar).toContainText(/json/i);
});

test("stdlib string page renders its first public function", async ({ page }) => {
  await page.goto("stdlib/string/");
  // is_empty is the first decl in std/string.fern.
  await expect(page.locator("main")).toContainText("is_empty");
  await expect(page.locator("main")).toContainText("pub function");
});

test("embedded playground iframe loads on the first-steps tutorial", async ({
  page,
}) => {
  await page.goto("tutorial/first-steps/");
  // The <FernPlayground> Astro component renders <figure class="fern-playground">
  // wrapping an iframe pointing at /lang/playground/?embed=1...#src=…
  const figure = page.locator("figure.fern-playground").first();
  await expect(figure).toBeVisible();
  const iframe = figure.locator("iframe");
  // Embed-mode flag + hash payload — both have to be present.
  await expect(iframe).toHaveAttribute(
    "src",
    /\/lang\/playground\/\?[^#]*embed=1[^#]*#src=/,
  );
});

test("minimal playground embed is wired read-only + autorun on the home page", async ({
  page,
}) => {
  await page.goto("./");
  // The landing "Hello, world" embed uses <FernPlayground minimal/>,
  // which renders <figure data-fern-minimal="1"> and a header reading
  // "▸ snippet" (not "▸ live snippet").
  const figure = page
    .locator("figure.fern-playground[data-fern-minimal='1']")
    .first();
  await expect(figure).toBeVisible();
  await expect(figure.locator("header")).toContainText("▸ snippet");
  const iframe = figure.locator("iframe");
  // Both the minimal flag and the forced autorun must reach the embed
  // URL — minimal snippets have no Run button, so autorun is mandatory.
  await expect(iframe).toHaveAttribute(
    "src",
    /\/lang\/playground\/\?[^#]*minimal=1[^#]*#src=/,
  );
  await expect(iframe).toHaveAttribute(
    "src",
    /\/lang\/playground\/\?[^#]*autorun=1[^#]*#src=/,
  );

  // …and the embed must actually put the playground into minimal chrome.
  // This is deliberately checked inside the frame rather than on the URL:
  // the classes used to be set by the editor module, whose static esm.sh
  // imports resolve before any of its body runs, so an unreachable CDN
  // rendered the FULL playground — title, toolbar, example picker — inside
  // a 180px docs iframe. They are set by the boot script now, ahead of the
  // imports, and this assertion holds with or without a network.
  const body = page
    .frameLocator("figure.fern-playground[data-fern-minimal='1'] iframe")
    .locator("body");
  await expect(body).toHaveClass(/\bminimal\b/);
  await expect(body).toHaveClass(/\bembed\b/);
});

test("embedded playground actually boots — the staged bundle is complete", async ({
  page,
}) => {
  await page.goto("./");
  // Enter the minimal "Hello, world" embed iframe and wait for the
  // playground to boot end-to-end. This guards the *staged* bundle:
  // index.html statically imports ./wasi-shim.js + ./wasi-http-shim.js,
  // so if the Pages/docs staging drops an asset the ES module aborts
  // and the status hangs forever on "loading runtime…". The other
  // embed tests only inspect the iframe's src attribute and would miss
  // that — this one loads the iframe.
  const frame = page.frameLocator(
    "figure.fern-playground[data-fern-minimal='1'] iframe",
  );
  // Boot sentinel: status flips to a "ready" prefix once the wasm runtime is
  // up. Since #4590 the boot runs in its own script, decoupled from the
  // esm.sh CodeMirror import, so a CDN stall can no longer strand this at
  // "loading runtime…". The remaining variable is the ~19 MB fern.wasm
  // streaming-compile, which grows with every compiler feature and can spike
  // on a loaded shared runner — hence 90 s, not 30 s (a genuine boot failure
  // surfaces as "wasm load failed: …", so the longer window costs nothing on
  // the failure path).
  await expect(frame.locator("#status")).toContainText("ready", {
    timeout: 90_000,
  });
  // The minimal embed autoruns, so its output renders with no click.
  await expect(frame.locator("#out")).toContainText("hello, world", {
    timeout: 30_000,
  });
});

// Search lives on the docs half only: `/` is a bespoke page outside
// Starlight, so it has no pagefind bundle and no search trigger. Exercise
// it from a docs page instead.
test("search modal opens via Ctrl/Cmd-K on a docs page", async ({ page }) => {
  await page.goto("why/");
  await page.keyboard.press("ControlOrMeta+k");
  await expect(page.getByRole("dialog")).toBeVisible({ timeout: 5_000 });
});

// Regression guard for the "links 404 on the deployed site"
// bug: markdown body links written with absolute paths (`/foo/`)
// emit `href="/foo/"` verbatim, which 404s under the `/lang/`
// base path. Clicking each link surfaces the issue — the prior
// suite navigated directly via page.goto so it missed this.
//
// Same bug bites Starlight's hero actions: the YAML `link:` is
// passed verbatim to `<a href>` (no base prefix), so a bare
// `/tutorial/install/` 404s. The hero-button click tests below
// cover that path — toBeVisible() alone wouldn't catch it.
test("home → hero Get started button navigates correctly", async ({ page }) => {
  await page.goto("./");
  await page.getByRole("link", { name: /Get started/i }).first().click();
  await expect(page).toHaveURL(/\/lang\/tutorial\/install\/?$/);
  await expect(page.locator("h1")).toContainText("Install");
});

test("home → hero Try in browser button targets the playground", async ({
  page,
}) => {
  await page.goto("./");
  const link = page.getByRole("link", { name: /Try in browser/i }).first();
  // The playground bundle is a separate Astro `public/` drop-in,
  // not a Starlight content page — asserting the href is enough
  // (clicking would race the wasm boot and isn't worth the flake).
  await expect(link).toHaveAttribute("href", /^\/lang\/playground\/?$/);
});

test("home → tutorial link navigates correctly", async ({ page }) => {
  await page.goto("./");
  await page.getByRole("link", { name: /^Tutorial$/ }).first().click();
  await expect(page).toHaveURL(/\/lang\/tutorial\/install\/?$/);
  await expect(page.locator("h1")).toContainText("Install");
});

test("home → why link navigates correctly", async ({ page }) => {
  await page.goto("./");
  await page.getByRole("link", { name: /^Why Fern$/ }).first().click();
  await expect(page).toHaveURL(/\/lang\/why\/?$/);
  await expect(page.locator("h1")).toContainText("Why Fern");
});

test("home → cookbook link navigates correctly", async ({ page }) => {
  await page.goto("./");
  await page.getByRole("link", { name: /^Cookbook$/ }).first().click();
  await expect(page).toHaveURL(/\/lang\/cookbook\/?$/);
  await expect(page.locator("h1")).toContainText("Cookbook");
  // Recipes are Fern code fences; the grammar registration means they
  // render as highlighted <code>, not as plain text.
  await expect(page.locator("main code").first()).toBeVisible();
});

test("releases page is reachable and links the nightly tag", async ({ page }) => {
  await page.goto("releases/");
  await expect(page.locator("h1")).toContainText("Releases");
  await expect(
    page.locator('main a[href*="releases/tag/nightly"]').first(),
  ).toBeVisible();
});

// The stdlib sidebar is built by stdlibSidebar() in astro.config.mjs
// rather than autogenerated, so the grouping is worth a guard: a module
// that loses its group would silently fall into "Other".
test("stdlib sidebar groups modules by purpose", async ({ page }) => {
  await page.goto("stdlib/string/");
  const sidebar = page.locator("nav, aside").first();
  await expect(sidebar.getByText("Networking", { exact: true })).toBeVisible();
  await expect(sidebar.getByText("WebAssembly", { exact: true })).toBeVisible();
  await expect(sidebar.getByText("Other", { exact: true })).toHaveCount(0);
});

test("home → reference link navigates correctly", async ({ page }) => {
  await page.goto("./");
  await page.getByRole("link", { name: /^Reference$/ }).first().click();
  await expect(page).toHaveURL(/\/lang\/reference\/syntax\/?$/);
});

test("home → standard library link navigates correctly", async ({ page }) => {
  await page.goto("./");
  await page.getByRole("link", { name: /Standard library/ }).first().click();
  await expect(page).toHaveURL(/\/lang\/stdlib\/?$/);
});

test("tutorial install → first-steps next-link navigates", async ({ page }) => {
  await page.goto("tutorial/install/");
  // The "Next: First steps →" link at the bottom of the install
  // page is the most-likely-to-regress case (relative-path
  // navigation inside a tutorial).
  await page
    .getByRole("link", { name: /Next: First steps/ })
    .first()
    .click();
  await expect(page).toHaveURL(/\/lang\/tutorial\/first-steps\/?$/);
});

// --- The bespoke landing page ------------------------------------------
//
// `/` is src/pages/index.astro rather than a Starlight content page, so it
// has none of Starlight's guarantees behind it. These four cover the parts
// that would break silently.

test("landing page language tour switches specimens", async ({ page }) => {
  await page.goto("./");
  const tablist = page.getByRole("tablist", { name: "Language tour" });
  await expect(tablist).toBeVisible();

  // First panel is shown, later ones hidden — that much holds with JS off.
  await expect(page.getByRole("tabpanel").first()).toBeVisible();

  await tablist.getByRole("tab", { name: /Ownership modes/ }).click();
  const panel = page.locator("#panel-own");
  await expect(panel).toBeVisible();
  await expect(panel).toContainText("fip");
  await expect(page.locator("#panel-match")).toBeHidden();
});

test("landing page code specimens are highlighted, not plain text", async ({
  page,
}) => {
  await page.goto("./");
  // Plain Shiki (not Expressive Code) renders `.astro-code`; a span inside
  // it proves the Fern grammar loaded rather than the block falling back
  // to unstyled text.
  const block = page.locator("figure.specimen .astro-code").first();
  await expect(block).toBeVisible();
  await expect(block.locator("span").first()).toBeVisible();
});

test("theme choice on the landing page carries into the docs", async ({
  page,
}) => {
  await page.goto("./");
  // The landing page writes the same localStorage key Starlight's
  // ThemeProvider reads, so a reader who picks light here stays in light
  // when they click through. Two separate mechanisms would desynchronise.
  const before = await page.evaluate(
    () => document.documentElement.dataset.theme,
  );
  // Pin down that there IS a starting theme, so the flip assertion below
  // cannot pass vacuously on two undefineds.
  expect(["dark", "light"]).toContain(before);
  await page.locator("[data-theme-toggle]").click();
  const after = await page.evaluate(
    () => document.documentElement.dataset.theme,
  );
  // Assert the flip rather than a fixed value: with no stored preference
  // the landing page starts from prefers-color-scheme, which differs
  // between a developer's machine and CI.
  expect(after).not.toBe(before);

  await page.goto("why/");
  await expect(page.locator("html")).toHaveAttribute("data-theme", after);
});

test("landing masthead reaches the docs, stdlib and status", async ({
  page,
}) => {
  await page.goto("./");
  const nav = page.getByRole("navigation", { name: "Main" });
  await expect(nav.getByRole("link", { name: "Docs" })).toHaveAttribute(
    "href",
    /\/lang\/tutorial\/install\/?$/,
  );
  await expect(nav.getByRole("link", { name: "Stdlib" })).toHaveAttribute(
    "href",
    /\/lang\/stdlib\/?$/,
  );
  await nav.getByRole("link", { name: "Status" }).click();
  await expect(page).toHaveURL(/\/lang\/status\/?$/);
  await expect(page.locator("h1")).toContainText("Project status");
});

// --- The compiler section ----------------------------------------------

test("compiler section pages are reachable and in the sidebar", async ({
  page,
}) => {
  await page.goto("compiler/");
  await expect(page.locator("h1")).toContainText("How the compiler works");
  const sidebar = page.locator("nav, aside").first();
  await expect(
    sidebar.getByText("Targets and backends", { exact: true }),
  ).toBeVisible();
  await expect(
    sidebar.getByText("Self-hosting and bootstrap", { exact: true }),
  ).toBeVisible();
});

test("targets page names every shipped target", async ({ page }) => {
  await page.goto("compiler/targets/");
  const main = page.locator("main");
  for (const t of [
    "arm64-linux",
    "arm64-darwin",
    "arm64-android",
    "x86-64-linux",
    "wasm32-wasi",
    "wasm32-wasi-http",
  ]) {
    await expect(main).toContainText(t);
  }
  // ARM32 was retired; it must never come back as a documented target.
  await expect(main).not.toContainText("arm32-");
});
