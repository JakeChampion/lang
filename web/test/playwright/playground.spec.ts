// Scripted regressions for the lang playground. Complements the
// Bombadil property-based suite by gating per-PR on a small set of
// deterministic golden paths the playground must always satisfy.
//
// Each test scopes itself to one feature and asserts a single
// outcome — when CI fails, the test name should be enough to know
// what broke. No multi-scenario god-tests.

import { test, expect } from "@playwright/test";

// Test fixture: wait for the page to finish booting. The status
// element flips from "loading runtime…" to "ready · …" once
// wasm_exec.js + the lang wasm + the LSP init have all completed.
async function gotoReady(page) {
  await page.goto("/index.html");
  // Status text settles on a "ready" prefix once initLsp() runs;
  // that's our boot sentinel (mirrors the Bombadil spec).
  await expect(page.locator("#status")).toContainText("ready", {
    timeout: 30_000,
  });
}

test("page boots and Run becomes enabled", async ({ page }) => {
  await gotoReady(page);
  await expect(page.locator("#run")).toBeEnabled();
});

test("hello example runs and prints to output", async ({ page }) => {
  await gotoReady(page);
  // The "hello" example is the default load — Run should produce
  // "hello, world" on stdout.
  await page.locator("#run").click();
  await expect(page.locator("#out")).toContainText("hello, world", {
    timeout: 5_000,
  });
  // Exit code chip should turn green for a clean exit.
  await expect(page.locator("#meta .exit-ok")).toBeVisible();
});

test("selecting the factorial example loads it and runs", async ({ page }) => {
  await gotoReady(page);
  await page.locator("#example").selectOption("fact");
  // The editor should now contain the factorial body.
  await expect(page.locator(".cm-content")).toContainText("fact");
  await page.locator("#run").click();
  await expect(page.locator("#out")).toContainText("5! = 120");
});

test("syntax errors surface in the Problems panel", async ({ page }) => {
  await gotoReady(page);
  // Type into the editor an unterminated function so the parser
  // bails. We click into the editor first to focus, select all,
  // then type.
  await page.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("function main(): i32 { return ");
  // Problem count should be > 0; the panel renders a per-error li.
  await expect(page.locator("#problemList li").first()).toBeVisible({
    timeout: 5_000,
  });
  await expect(page.locator("#problemCount")).not.toHaveText("0");
});

test("clean source clears the Problems panel", async ({ page }) => {
  await gotoReady(page);
  // Start clean — the hello example is well-formed. Wait for the
  // debounced LSP roundtrip to publish the empty-diagnostic list.
  await expect(page.locator("#problemList .empty")).toBeVisible({
    timeout: 5_000,
  });
});

test("Share button updates the URL hash", async ({ page }) => {
  await gotoReady(page);
  // Stub clipboard so the test doesn't depend on permission
  // dialogs in headless contexts.
  await page.evaluate(() => {
    (navigator as any).clipboard = { writeText: async () => {} };
  });
  await page.locator("#share").click();
  await expect.poll(() => page.url()).toMatch(/#src=/);
});
