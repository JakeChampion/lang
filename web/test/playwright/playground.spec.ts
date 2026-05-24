// Scripted regressions for the Fern playground. Complements the
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

test("View assembly compiles the default source for arm64", async ({ page }) => {
  await gotoReady(page);
  // Default target dropdown selection is arm64. Click View
  // assembly; the panel should appear with non-empty asm text.
  await page.locator("#viewAsm").click();
  await expect(page.locator("#asmPanel")).toHaveClass(/shown/);
  // The compile is synchronous (wasm call) but the status update
  // is async via setTimeout(0); poll for the final state.
  await expect(page.locator("#asmStatus")).toContainText(/arm64.*line/, {
    timeout: 10_000,
  });
  // ARM64 assembly is GAS syntax; .text + ret are universal
  // markers in any non-trivial emit.
  await expect(page.locator("#asmOut")).toContainText(".text");
});

test("changing the asm target re-emits for that backend", async ({ page }) => {
  await gotoReady(page);
  await page.locator("#viewAsm").click();
  await expect(page.locator("#asmPanel")).toHaveClass(/shown/);
  await page.locator("#targetSelect").selectOption("x86-64");
  await expect(page.locator("#asmStatus")).toContainText(/x86-64.*line/, {
    timeout: 10_000,
  });
  // x86-64 System V output always opens with a `.text` directive.
  await expect(page.locator("#asmOut")).toContainText(".text");
});

test("theme toggle flips body.dark and updates the glyph", async ({ page }) => {
  await gotoReady(page);
  // Force light mode first so the toggle has a deterministic
  // before-state regardless of the CI runner's prefers-color-
  // scheme.
  await page.evaluate(() => {
    document.body.classList.remove("dark");
    document.getElementById("themeGlyph")!.textContent = "🌙";
  });
  await expect(page.locator("body")).not.toHaveClass(/dark/);
  await page.locator("#themeToggle").click();
  await expect(page.locator("body")).toHaveClass(/dark/);
  await expect(page.locator("#themeGlyph")).toHaveText("☀");
  // Click back to light.
  await page.locator("#themeToggle").click();
  await expect(page.locator("body")).not.toHaveClass(/dark/);
  await expect(page.locator("#themeGlyph")).toHaveText("🌙");
});

test("?theme=dark URL param boots into dark mode", async ({ page }) => {
  await page.goto("/index.html?theme=dark");
  await expect(page.locator("#status")).toContainText("ready", { timeout: 30_000 });
  await expect(page.locator("body")).toHaveClass(/dark/);
});

test("embed mode shows the pane-tab strip; Assembly tab swaps content", async ({
  page,
}) => {
  await page.goto("/index.html?embed=1");
  await expect(page.locator("#status")).toContainText("ready", { timeout: 30_000 });
  // Tab strip visible only under body.embed.
  await expect(page.locator(".pane-tabs")).toBeVisible();
  // Default is the Output tab — body shouldn't carry the asm class.
  await expect(page.locator("body")).not.toHaveClass(/tab-asm/);
  await expect(page.locator(".asm-panel")).not.toBeVisible();
  // Switch to Assembly. Body class flips; asm pane appears with
  // emitted content for the default target (arm64).
  await page.locator('.pane-tabs .tab[data-tab="asm"]').click();
  await expect(page.locator("body")).toHaveClass(/tab-asm/);
  await expect(page.locator(".asm-panel")).toBeVisible();
  await expect(page.locator("#asmOut")).toContainText(".text", { timeout: 10_000 });
  // Output pane hidden while Assembly is active.
  await expect(page.locator(".pane-output pre#out")).not.toBeVisible();
  // Embed target dropdown becomes visible only on the Asm tab.
  await expect(page.locator("#embedTarget")).toBeVisible();
});

test("standalone mode does NOT show the embed tab strip", async ({ page }) => {
  await gotoReady(page);
  // Tab strip is gated on body.embed in CSS; standalone hides it.
  await expect(page.locator(".pane-tabs")).not.toBeVisible();
});
