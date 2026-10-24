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
await a.goto(base + "/");
await a.getByRole("button", { name: "Make something", exact: true }).click();
assert.equal(await a.locator(".example:visible").count(), 4);
await a.getByRole("button", { name: "All seven", exact: true }).click();
assert.equal(await a.locator(".example:visible").count(), 7);
console.log("gallery filters OK");
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
console.log("shared canvas + WebSocket OK");
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
await a.goto(base + "/swarm/");
await a.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Live"),
);
await a.locator("#pause").click();
await a.waitForTimeout(80);
const stopped = await a.locator("#field").getAttribute("data-frames");
await a.waitForTimeout(120);
assert.equal(await a.locator("#field").getAttribute("data-frames"), stopped);
await a.locator("#pause").click();
await a.locator("#audio").click();
await a.waitForFunction(
  () => document.querySelector("#field").dataset.audio === "playing",
);
await a.locator("#audio").click();
console.log("browser Wasm, pause, and Fern worker audio OK");
const delayed = await browser.newContext();
await delayed.addInitScript(() => {
  const Native = WebSocket;
  window.__rallySent = 0;
  window.WebSocket = class extends Native {
    #receive;
    constructor(...args) {
      super(...args);
      super.addEventListener("message", (event) =>
        setTimeout(() => this.#receive?.(event), 60),
      );
    }
    set onmessage(value) {
      this.#receive = value;
    }
    get onmessage() {
      return this.#receive;
    }
    send(value) {
      window.__rallySent++;
      setTimeout(() => {
        if (this.readyState === Native.OPEN) super.send(value);
      }, 60);
    }
  };
});
const lagged = await delayed.newPage();
await lagged.goto(base + "/rally/");
await lagged.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Live"),
);
const beforeInput = await lagged.evaluate(() => [
  Number(document.querySelector("#court").dataset.paddle),
  window.__rallySent,
]);
await lagged.keyboard.down("ArrowDown");
await lagged.waitForTimeout(45);
await lagged.keyboard.up("ArrowDown");
const afterInput = await lagged.evaluate(() => [
  Number(document.querySelector("#court").dataset.paddle),
  window.__rallySent,
]);
assert.ok(
  afterInput[0] > beforeInput[0] + 0.015,
  "local paddle must move before the 120 ms reply",
);
await lagged.waitForTimeout(40);
assert.ok(
  (await lagged.evaluate(() => window.__rallySent)) >= beforeInput[1] + 4,
  "input must not wait for round trips",
);
await delayed.close();
console.log("paddle prediction and pipelined input at 120 ms simulated RTT OK");
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
  "/swarm/",
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
