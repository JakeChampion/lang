import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const base = process.argv[2] || "http://localhost:8080";
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: ["--no-sandbox", "--autoplay-policy=no-user-gesture-required"],
});
const context = await browser.newContext({
  viewport: { width: 1280, height: 1000 },
});
const errors = [];
context.on("page", (p) => p.on("pageerror", (e) => errors.push(e.message)));
const a = await context.newPage(),
  b = await context.newPage();
await a.goto(base + "/rally/");
await a.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Live"),
);
await a.locator("#duo").click();
await a.waitForFunction(() => location.hash.length > 4);
const invite = a.url();
await b.goto(invite);
await b.waitForFunction(
  () => document.querySelector("#seat").textContent === "RIGHT",
);
await a.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("2/2"),
);
await a.locator("#start").click();
await a.waitForFunction(() => document.querySelector("#court-message").hidden);
await b.waitForFunction(() => document.querySelector("#court-message").hidden);
console.log("two-player shared game OK");
const room = "browser-" + Date.now();
await a.goto(base + "/mosaic/?room=" + room);
await b.goto(base + "/mosaic/?room=" + room);
await a.waitForFunction(() =>
  document.querySelector("#revision").textContent.includes("0"),
);
await b.waitForFunction(() =>
  document.querySelector("#revision").textContent.includes("0"),
);
await a.locator("#palette button").nth(7).click();
await a.locator("#board").focus();
await a.keyboard.press("Space");
await b.waitForFunction(
  () => !document.querySelector("#revision").textContent.endsWith("0"),
);
console.log("shared canvas + SSE OK");
await a.goto(base + "/wave/");
await a.locator("#play").click();
await a.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Playing"),
);
console.log("native WAV playback OK");
await a.locator("#stop").click();
await a.goto(base + "/pulse/");
await a.waitForFunction(
  () => document.querySelector("#rss").textContent !== "—",
);
console.log("RSS:", await a.locator("#rss").textContent());
await a.locator("#pause").click();
assert.match(await a.locator("#pause").textContent(), /Resume/);
const nojs = await browser.newContext({ javaScriptEnabled: false });
const form = await nojs.newPage();
await form.goto(base + "/notes/");
await form.locator("#name").fill("Browser <test>");
await form.locator("#message").fill("A real form <script>alert(1)</script>");
await form.locator("button[type=submit]").click();
assert.match(form.url(), /posted=1/);
assert.match(await form.locator(".note-wall").textContent(), /<script>alert/);
assert.equal(await form.locator(".note-wall script").count(), 0);
console.log("no-JS signed form + escaping OK");
await nojs.close();
const mobile = await browser.newContext({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
});
for (const route of [
  "/",
  "/rally/",
  "/mosaic/",
  "/wave/",
  "/notes/",
  "/pulse/",
  "/_src/showcase/mosaic.fern",
]) {
  const p = await mobile.newPage();
  await p.goto(base + route);
  await p.waitForTimeout(250);
  const size = await p.evaluate(() => [
    document.documentElement.scrollWidth,
    innerWidth,
  ]);
  assert.ok(size[0] <= size[1] + 1, route + " overflow " + size);
  await p.close();
}
console.log("mobile widths OK");
assert.deepEqual(errors, []);
await browser.close();
