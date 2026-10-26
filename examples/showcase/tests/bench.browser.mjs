// Runs the JSON lab, the compression bench, the post-quantum handshake, and
// the image pipeline against the live server.
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

const piped = () =>
  page.waitForFunction(
    () => !document.querySelector("#run").disabled && /Done|did not|error|rejected|Choose/i.test(document.querySelector("#connection").textContent),
    null,
    { timeout: 120000 },
  );
await page.goto(base + "/image/");
await page.waitForFunction(() => document.querySelector("#connection").textContent.includes("Ready"));
for (const [source, target, filter, tone] of [["640x480", "320", "0", "0"], ["1280x960", "160", "1", "1"]]) {
  await page.waitForTimeout(1600);
  await page.locator("#source").selectOption(source);
  await page.locator("#target").selectOption(target);
  await page.locator("#filter").selectOption(filter);
  await page.locator("#tone").selectOption(tone);
  await page.locator("#run").click();
  await piped();
  assert.equal(await page.locator("#connection").textContent(), "Done");
  assert.equal(await page.locator("#verdict").getAttribute("data-match"), "true");
  assert.equal(await page.locator("#results tr").count(), 5);
  assert.equal(await page.locator("#output").evaluate((img) => img.decode().then(() => img.naturalWidth)), Number(target));
}
console.log("image pipeline: Fern Wasm and native Fern write identical PNG files OK");

const png = Buffer.from(
  await page.evaluate(async () => Array.from(new Uint8Array(await (await fetch(document.querySelector("#output").src)).arrayBuffer()))),
);
const sent = [];
page.on("request", (request) => {
  if (request.url().includes("/image/")) sent.push(request.url());
});
await page.locator("#source").selectOption("file");
await page.locator("#file").setInputFiles({ name: "own.png", mimeType: "image/png", buffer: png });
await page.locator("#target").selectOption("640");
await page.locator("#run").click();
await piped();
assert.equal(await page.locator("#connection").textContent(), "Done");
assert.equal(await page.locator("#verdict").getAttribute("data-match"), "local");
assert.match(await page.locator("#summary").textContent(), /^160 × 120 to 160 × 120/);
assert.deepEqual(sent, []);
console.log("image pipeline: an own image stays in the browser OK");

assert.deepEqual(errors, []);
await browser.close();
console.log("labs: JSON, compression, handshake, and image pipeline OK");
