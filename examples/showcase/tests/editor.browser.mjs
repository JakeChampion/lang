// Edits the Swarm kernel in the browser: a working change, a compile error,
// a module with an import the page cannot provide, and a restore.
import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const base = process.argv[2] || "http://localhost:8080";
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: ["--no-sandbox"],
});
const page = await browser.newPage();
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
await page.goto(base + "/swarm/");
await page.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Live"),
);
const kernel = () => page.locator("#field").getAttribute("data-kernel");
const statusText = () => page.locator("#compile-status").textContent();
assert.equal(await kernel(), "original");

await page.locator("#editor-open").click();
await page.waitForFunction(
  () => document.querySelector("#compile-status").textContent.includes("ready"),
  null,
  { timeout: 120000 },
);
const source = await page.locator("#kernel-source").inputValue();
assert.match(source, /@export\("fern:showcase\/swarm", "particle"\)/);

async function compile(text) {
  await page.locator("#kernel-source").fill(text);
  await page.locator("#compile").click();
  await page.waitForFunction(
    () => !document.querySelector("#compile").disabled,
    null,
    { timeout: 120000 },
  );
}

await compile(source.replace("0.08 + c * 0.32", "0.3 + c * 0.1"));
assert.match(await statusText(), /compiled in \d+ ms/);
assert.equal(await kernel(), "edited");
assert.ok(await page.locator("#diagnostics").isHidden());
console.log("edited kernel compiled and installed OK");

await compile(source.replace("seed(index, 1)", "seed(index)"));
assert.match(await page.locator("#diagnostics").textContent(), /error\[E004\]/);
assert.match(await statusText(), /did not compile/);
assert.equal(await kernel(), "edited", "a failed compile keeps the running kernel");
console.log("compile error reported at its line OK");

await compile(
  source.replace(
    "let a: f64 = seed(index, 1);",
    "let a: f64 = seed(index, monotonic_ns() as i32);",
  ),
);
assert.match(
  await page.locator("#diagnostics").textContent(),
  /imports wasi_snapshot_preview1\.clock_time_get/,
);
assert.match(await statusText(), /cannot run here/);
console.log("unsupported import refused OK");

await page.locator("#revert").click();
await page.waitForFunction(
  () => document.querySelector("#field").dataset.kernel === "original",
);
assert.equal(await page.locator("#kernel-source").inputValue(), source);
assert.deepEqual(errors, []);
await browser.close();
console.log("kernel editor: compile, diagnostics, import check, and restore OK");
