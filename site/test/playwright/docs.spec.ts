// Scripted regressions for the lang docs site. Asserts behaviour
// the build step doesn't catch: navigation, sidebar groups, the
// embedded playground iframe loading, stdlib pages reachable.
//
// page.goto() paths are RELATIVE (no leading slash) so they
// resolve against the baseURL's `/lang/` prefix. A leading `/`
// would be absolute and drop the prefix — see playwright.config.ts.

import { test, expect } from "@playwright/test";

test("home page renders the hero + landing card grid", async ({ page }) => {
  await page.goto("./");
  await expect(page.locator("h1")).toContainText("Fern");
  // The card grid uses Starlight's Card component — asserting on
  // the "Get started" CTA is more robust than poking at the card
  // wrapper class which changes between Starlight versions.
  await expect(
    page.getByRole("link", { name: /Get started/i }),
  ).toBeVisible();
});

test("tutorial sidebar group lists the install page", async ({ page }) => {
  await page.goto("tutorial/install/");
  await expect(page.locator("h1")).toContainText("Install");
  const sidebar = page.locator("nav, aside").first();
  await expect(sidebar.getByText("Install", { exact: true })).toBeVisible();
  await expect(sidebar.getByText("First steps", { exact: true })).toBeVisible();
});

test("HTTP guide sidebar group lists its pages in order", async ({ page }) => {
  await page.goto("http/");
  await expect(page.locator("h1")).toContainText("HTTP in Fern");
  const sidebar = page.locator("nav, aside").first();
  await expect(sidebar.getByText("Your first server", { exact: true })).toBeVisible();
  await expect(sidebar.getByText("Testing HTTP code", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Routing and middleware" }).first().click();
  await expect(page.locator("main")).toContainText("router.new[P]()");
});

test("reference > tooling page describes the language server", async ({ page }) => {
  await page.goto("reference/tooling/");
  await expect(page.locator("main")).toContainText("fern -lsp");
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
  // is_empty is the first decl in std/string.fern.md.
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
    "data-src",
    /\/lang\/playground\/\?[^#]*embed=1[^#]*#src=/,
  );
});

test("an embed loads once, with the theme, when it nears the viewport", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  const docs: import("@playwright/test").Frame[] = [];
  page.on("request", (r) => {
    if (r.resourceType() === "document" && r.url().includes("/playground/?")) docs.push(r.frame());
  });
  await page.goto("./");
  const grep = page.locator("figure.fern-playground iframe[data-src*='&args=']");
  // Far below the first screen, so not loaded yet.
  await expect(grep).not.toHaveAttribute("src", /./);
  await grep.scrollIntoViewIfNeeded();
  await expect(grep).toHaveAttribute("src", /[?&]theme=dark/);
  const grepFrame = await (await grep.elementHandle())!.contentFrame();
  await page.waitForTimeout(1500);
  expect(docs.filter((f) => f === grepFrame).length).toBe(1);
});

test("the home page grep embed runs with its arguments and stdin", async ({
  page,
}) => {
  await page.goto("./");
  const iframe = page.locator("figure.fern-playground iframe[data-src*='&args=']");
  await expect(iframe).toHaveAttribute("data-src", /#src=[^&]+&args=[^&]+&stdin=/);
  await iframe.scrollIntoViewIfNeeded();
  const frame = page.frameLocator("figure.fern-playground iframe[data-src*='&args=']");
  await expect(frame.locator("#out")).toHaveText("fern frond\nfiddlehead fern\n", {
    timeout: 60_000,
  });
});

test("a fern code fence uses the shared token colours", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("./");
  // web/editor/fern-themes.json gives keywords #24603f in the light theme.
  const kw = page
    .locator(".expressive-code span", { hasText: /^function$/ })
    .first();
  await expect(kw).toHaveCSS("color", "rgb(36, 96, 63)");
});

test("the home page install card points at this site's install script", async ({
  page,
}) => {
  await page.goto("./");
  const card = page.locator(".fern-install");
  await expect(card.locator(".fern-cmd__text").first()).toHaveText(/^curl -fsSL \S+\/lang\/install\.sh \| sh$/);
  const res = await page.request.get("install.sh");
  expect(res.status()).toBe(200);
  expect(await res.text()).toMatch(/^#!\/bin\/sh\n/);
});

test("the home page shows the captured assembly for both targets", async ({ page }) => {
  await page.goto("./");
  const compiled = page.locator(".fern-compiled");
  await expect(compiled).toContainText("area.fern");
  await expect(compiled.getByRole("tab", { name: "arm64-linux" })).toBeVisible();
  await expect(compiled.getByRole("tab", { name: "x86-64-linux" })).toBeVisible();
  // The folded constant is marked.
  await expect(compiled.locator("mark").first()).toContainText("75");
  await expect(compiled).toContainText(/static executable: [\d,]+ bytes/);
});

test("the home page shows the bench ratios, the usage text and the editor features", async ({ page }) => {
  await page.goto("./");
  await expect(page.locator(".fern-bench-head__value").first()).toHaveText(/^\d+\.\d\d×$/);
  await expect(page.locator(".fern-captured")).toContainText("usage: fern");
  await expect(page.locator(".fern-editor li")).not.toHaveCount(0);
  await expect(page.locator(".fern-editor")).toContainText("Go to definition");
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
    "data-src",
    /\/lang\/playground\/\?[^#]*minimal=1[^#]*#src=/,
  );
  await expect(iframe).toHaveAttribute(
    "data-src",
    /\/lang\/playground\/\?[^#]*autorun=1[^#]*#src=/,
  );
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
  // An embed loads when it nears the viewport.
  await page.locator("figure.fern-playground[data-fern-minimal='1'] iframe").first().scrollIntoViewIfNeeded();
  const frame = page.frameLocator(
    "figure.fern-playground[data-fern-minimal='1'] iframe",
  );
  // Boot sentinel: status flips to a "ready" prefix once the wasm runtime is
  // up. Since #4590 the boot runs in its own script, decoupled from the
  // esm.sh CodeMirror import, so a CDN stall can no longer strand this at
  // "loading runtime…". The remaining variable is the ~12 MB playground.wasm
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

test("search modal opens via Ctrl/Cmd-K", async ({ page }) => {
  await page.goto("./");
  // Starlight ships pagefind-backed search; the trigger is
  // labelled "Search" and opens a dialog.
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

test("coreutils page sorts, filters and opens a utility's workloads", async ({
  page,
}) => {
  await page.goto("coreutils/");
  await expect(page.locator("h1")).toContainText("Coreutils");
  await expect(page.locator(".fern-spec")).toContainText("Faster than GNU");

  const table = page.locator("[data-bench-table]");
  const names = table.locator("tbody tr:not([hidden]) th code");
  await expect(names.first()).toHaveText("[");

  await page.getByRole("button", { name: "GNU median" }).click();
  const first = Number(await table.locator("tbody tr").first().getAttribute("data-mg"));
  const second = Number(await table.locator("tbody tr").nth(1).getAttribute("data-mg"));
  expect(first).toBeGreaterThanOrEqual(second);

  await page.locator("[data-bench-filter]").fill("sha256");
  await expect(names).toHaveText(["sha256sum"]);

  await names.first().click();
  const details = page.locator("details#bench-sha256sum");
  await expect(details).toHaveAttribute("open", "");
  await expect(details.locator("tbody tr").first()).toBeVisible();
});

test("the sidebar numbers the tutorial steps without renaming the links", async ({ page }) => {
  await page.goto("tutorial/install/");
  const install = page.getByRole("link", { name: "Install", exact: true }).first();
  await expect(install).toBeVisible();
  const before = await install.evaluate((el) => getComputedStyle(el, "::before").content);
  expect(before).toContain("counter(fern-step");
});

test("the stdlib sidebar nests a module family under its parent", async ({ page }) => {
  await page.goto("stdlib/opus_silk_tables/");
  const current = page.locator('a[aria-current="page"]');
  await expect(current).toHaveText("opus_silk_tables");
  // The page sits in the opus_silk group, inside the opus group.
  const groups = await current.evaluate((el) => {
    const out = [];
    for (let d = el.closest("details"); d; d = d.parentElement?.closest("details") ?? null) {
      out.push(d.querySelector(":scope > summary")?.textContent?.trim());
    }
    return out;
  });
  expect(groups.slice(0, 2)).toEqual(["opus_silk", "opus"]);
});

test("the favicon link resolves", async ({ page }) => {
  await page.goto("./");
  const href = await page.locator('link[rel="shortcut icon"]').getAttribute("href");
  const res = await page.request.get(new URL(href!, page.url()).href);
  expect(res.status()).toBe(200);
  expect(await res.text()).toContain("<svg");
});
