// Runs the JSON lab, the compression bench, and the post-quantum handshake
// against the live server.
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
const measured = () =>
  page.waitForFunction(
    () => /Measured|did not|error/i.test(document.querySelector("#connection").textContent),
    null,
    { timeout: 120000 },
  );

await page.goto(base + "/json/");
await page.waitForFunction(() => document.querySelector("#connection").textContent.includes("Ready"));
await page.locator("#corpus").selectOption("65536");
await page.locator("#run").click();
await measured();
assert.equal(await page.locator("#connection").textContent(), "Measured");
assert.equal(await page.locator("#results tr").count(), 5);
assert.equal(await page.locator("#results").getAttribute("data-agree"), "true");
assert.match(await page.locator("#summary").textContent(), /identical bytes/);
console.log("JSON lab: five engines agree on identical bytes OK");

await page.waitForTimeout(1600);
await page.goto(base + "/compress/");
await page.waitForFunction(() => document.querySelector("#connection").textContent.includes("Ready"));
await page.locator("#corpus").selectOption("65536");
await page.locator("#run").click();
await measured();
assert.equal(await page.locator("#connection").textContent(), "Measured");
assert.equal(await page.locator("#results tr").count(), 7);
assert.equal(await page.locator("#results").getAttribute("data-lossless"), "true");
assert.match(await page.locator("#summary").textContent(), /identical bytes/);
assert.equal(await page.locator("#interop").getAttribute("data-ok"), "true");
console.log("compression bench: seven codecs lossless, browser decodes Fern output OK");

await page.goto(base + "/handshake/");
await page.waitForFunction(() => document.querySelector("#connection").textContent.includes("Ready"));
for (let attempt = 0; attempt < 3; attempt++) {
  await page.locator("#start").click();
  await page.waitForFunction(
    () => !document.querySelector("#start").disabled,
    null,
    { timeout: 60000 },
  );
  assert.match(await page.locator("#connection").textContent(), /Shared key agreed/);
  assert.match(await page.locator("#secret").textContent(), /Hello from native Fern/);
  assert.equal(await page.locator('[data-state="done"]').count(), 5);
}
console.log("post-quantum handshake: three fresh key agreements OK");

const tampered = await page.evaluate(async () => {
  const response = await fetch("/handshake/exchange", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ek: "00", x: "00" }),
  });
  return response.status;
});
assert.equal(tampered, 400);
assert.deepEqual(errors, []);
await browser.close();
console.log("labs: JSON, compression, and handshake OK");
