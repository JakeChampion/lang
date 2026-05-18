// Scripted regressions for the lang docs site. Asserts behaviour
// the build step doesn't catch: navigation, sidebar groups, the
// embedded playground iframe loading, stdlib pages reachable.
//
// Each test scopes itself to one feature with one assertion so a
// CI failure's test name reads as the bug report.

import { test, expect } from "@playwright/test";

test("home page renders the hero + landing card grid", async ({ page }) => {
  await page.goto("/");
  // Hero tagline (Starlight's `splash` template).
  await expect(page.locator("h1")).toContainText("lang");
  // The card grid uses Starlight's Card component — each card
  // gets an `astro-island` wrapper or a plain `.card` element.
  // Asserting on the "Get started" CTA is more robust.
  await expect(
    page.getByRole("link", { name: /Get started/i }),
  ).toBeVisible();
});

test("tutorial sidebar group lists the install page", async ({ page }) => {
  await page.goto("/tutorial/install/");
  await expect(page.locator("h1")).toContainText("Install");
  // The tutorial group should be expanded with multiple entries.
  // Search the sidebar by accessible role for "Tutorial".
  const sidebar = page.locator("nav, aside").first();
  await expect(sidebar.getByText("Install", { exact: true })).toBeVisible();
  await expect(sidebar.getByText("First steps", { exact: true })).toBeVisible();
});

test("reference > tooling page describes lang-lsp", async ({ page }) => {
  await page.goto("/reference/tooling/");
  await expect(page.locator("main")).toContainText("lang-lsp");
  await expect(page.locator("main")).toContainText("textDocument/formatting");
});

test("stdlib index links to at least one auto-generated module", async ({
  page,
}) => {
  await page.goto("/stdlib/");
  // The langdoc generator emits per-module pages; the sidebar
  // should list at least string + json (densest modules).
  const sidebar = page.locator("nav, aside").first();
  // Sidebar may use abbreviated entry titles ("std/string");
  // check both forms loosely.
  await expect(sidebar).toContainText(/string/i);
  await expect(sidebar).toContainText(/json/i);
});

test("stdlib string page renders its first public function", async ({ page }) => {
  await page.goto("/stdlib/string/");
  // is_empty is the first decl in std/string.lang.
  await expect(page.locator("main")).toContainText("is_empty");
  // The codegen renders the signature inside a code block; the
  // pub function prefix is a stable marker.
  await expect(page.locator("main")).toContainText("pub function");
});

test("embedded playground iframe loads on the first-steps tutorial", async ({
  page,
}) => {
  await page.goto("/tutorial/first-steps/");
  // The <LangPlayground> Astro component renders <figure class="lang-playground">
  // wrapping an iframe pointing at /lang/playground/?embed=1...#src=…
  const figure = page.locator("figure.lang-playground").first();
  await expect(figure).toBeVisible();
  const iframe = figure.locator("iframe");
  // Embed-mode flag + hash payload — both have to be present.
  await expect(iframe).toHaveAttribute(
    "src",
    /\/lang\/playground\/\?[^#]*embed=1[^#]*#src=/,
  );
});

test("search modal opens via Ctrl/Cmd-K", async ({ page }) => {
  await page.goto("/");
  // Starlight ships pagefind-backed search; the trigger is
  // labelled "Search" and opens a dialog.
  await page.keyboard.press("ControlOrMeta+k");
  await expect(page.getByRole("dialog")).toBeVisible({ timeout: 5_000 });
});
