import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: [
    "--no-sandbox",
    "--autoplay-policy=no-user-gesture-required",
    "--enable-unsafe-swiftshader",
  ],
});
const base = process.argv[2] || "http://localhost:8080";
const errors = [];
for (const fallback of [false, true]) {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
  });
  context.on("page", (p) => p.on("pageerror", (e) => errors.push(e.message)));
  if (fallback)
    await context.addInitScript(() => {
      Object.defineProperty(Element.prototype, "requestFullscreen", {
        value: undefined,
        configurable: true,
      });
      Object.defineProperty(document, "fullscreenEnabled", {
        value: false,
        configurable: true,
      });
    });
  for (const name of [
    "rally",
    "mosaic",
    "wave",
    "swarm",
    "pulse",
    "gather",
    "blockyard",
  ]) {
    const page = await context.newPage();
    await page.goto(`${base}/${name}/`);
    await page.locator(".fullscreen-toggle").tap();
    await page.waitForFunction(
      () => !!document.querySelector(".is-fullscreen"),
    );
    assert.equal(
      await page.locator(".fullscreen-toggle").getAttribute("aria-pressed"),
      "true",
    );
    await page.setViewportSize({ width: 844, height: 390 });
    await page.waitForTimeout(100);
    const size = await page.evaluate(() => ({
      width: document.querySelector(".is-fullscreen").getBoundingClientRect()
        .width,
      screen: document.documentElement.clientWidth,
      scroll: document.querySelector(".is-fullscreen").scrollWidth,
    }));
    assert.ok(
      size.scroll <= size.screen + 1,
      `${name} fullscreen overflow: ${JSON.stringify(size)}`,
    );
    await page.locator(".fullscreen-toggle").tap();
    await page.waitForFunction(() => !document.querySelector(".is-fullscreen"));
    console.log(
      name,
      fallback ? "expanded" : "native",
      "fullscreen enter / rotate / exit OK",
    );
    await page.close();
  }
  await context.close();
}
const context = await browser.newContext({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
});
const p = await context.newPage();
p.on("pageerror", (e) => errors.push(e.message));
await p.goto(base + "/rally/");
await p.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Live"),
);
let r = await p.locator("#court").boundingBox();
const before = Number(await p.locator("#court").getAttribute("data-paddle"));
await p.touchscreen.tap(r.x + r.width / 2, r.y + r.height * 0.85);
await p.waitForTimeout(100);
assert.ok(
  Number(await p.locator("#court").getAttribute("data-paddle")) > before + 0.04,
);
await p.locator("#start").tap();
await p.waitForFunction(() => document.querySelector("#court-message").hidden);
await p.locator("#pause").tap();
await p.goto(base + "/mosaic/?room=touch-" + Date.now());
await p.waitForFunction(() =>
  document.querySelector("#revision").textContent.includes("0"),
);
await p.locator("#palette button").nth(7).tap();
r = await p.locator("#board").boundingBox();
await p.locator("#board").tap();
await p.waitForFunction(
  () => !document.querySelector("#revision").textContent.endsWith("0"),
);
assert.ok(
  await p
    .locator("#palette")
    .evaluate((e) => e.scrollWidth <= e.clientWidth + 1),
);
await p.goto(base + "/swarm/");
await p.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Live"),
);
await p.locator("#pause").tap();
await p.waitForFunction(
  () => document.querySelector("#fps").textContent === "PAUSED",
);
const image = (await p.locator("#field").screenshot()).toString("base64");
await p.locator("#interaction").selectOption("-1");
r = await p.locator("#field").boundingBox();
await p
  .locator("#field")
  .tap({ position: { x: r.width * 0.7, y: r.height * 0.6 } });
await p.waitForTimeout(100);
assert.ok(
  (await p.locator("#field").screenshot()).toString("base64") !== image,
  "touch interaction must change the field",
);
await p.locator("#interaction").selectOption("0");
await p.goto(base + "/wave/");
await p.locator("#sequencer button").nth(0).tap();
await p.locator("#play").tap();
await p.waitForFunction(() =>
  document.querySelector("#connection").textContent.includes("Playing"),
);
await p.locator("#stop").tap();
await p.goto(base + "/gather/");
await p.locator("#join").tap();
await p.waitForFunction(() => document.querySelector("#arena").dataset.x);
const initialPosition = await p.locator("#arena").getAttribute("data-x");
const arenaBox = await p.locator("#arena").boundingBox();
await p
  .locator("#arena")
  .tap({ position: { x: arenaBox.width - 20, y: arenaBox.height - 20 } });
await p.waitForFunction(
  (value) => document.querySelector("#arena").dataset.x !== value,
  initialPosition,
);
await p.locator("#join").tap();
await p.waitForFunction(() =>
  document.querySelector("#join").textContent.includes("Join"),
);
console.log(
  "touch-only paddle, start/pause, palette, paint, attract/repel/free flow, and sequencer playback OK",
);
assert.deepEqual(errors, []);
await browser.close();
