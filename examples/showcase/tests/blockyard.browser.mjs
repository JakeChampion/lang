import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: ["--no-sandbox", "--enable-unsafe-swiftshader"],
});
const base = process.argv[2] || "http://localhost:8080",
  world = process.env.BLOCKYARD_TEST_WORLD || "harbor",
  errors = [],
  edits = [];
try {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
  });
  context.on("page", (p) => p.on("pageerror", (e) => errors.push(e.message)));
  const a = await context.newPage(),
    b = await context.newPage();
  const messages = [];
  a.on("websocket", (socket) =>
    socket.on("framereceived", ({ payload }) => {
      if (typeof payload === "string") messages.push(JSON.parse(payload));
    }),
  );
  b.on("websocket", (socket) =>
    socket.on("framereceived", ({ payload }) => {
      if (typeof payload === "string") {
        const value = JSON.parse(payload);
        if (value.kind === "edits") edits.push(...value.blocks);
      }
    }),
  );
  for (const p of [a, b]) {
    await p.goto(base + "/blockyard/?world=" + encodeURIComponent(world));
    await p.locator("#play").tap({ timeout: 30000 });
    await p.waitForFunction(() => document.querySelector("#welcome").hidden);
  }
  await a.waitForFunction(() =>
    document.querySelector("#players").textContent.startsWith("2 /"),
  );
  assert.ok(
    await a.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth + 1,
    ),
  );
  const cdp = await context.newCDPSession(a);
  async function drag(x, y, dx, dy, hold = 0) {
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ x, y }],
    });
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [{ x: x + dx, y: y + dy }],
    });
    if (hold) await a.waitForTimeout(hold);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
  }
  await a.waitForTimeout(600);
  await a.waitForFunction(
    () =>
      Math.abs(Number(document.querySelector("#view").dataset.y) - 14) < 0.02,
  );
  await a.locator("#jump").tap();
  await a.waitForFunction(
    () => Number(document.querySelector("#view").dataset.y) > 14.2,
  );
  await a.waitForFunction(
    () =>
      Math.abs(Number(document.querySelector("#view").dataset.y) - 14) < 0.02,
  );
  const initial = Number(await a.locator("#view").getAttribute("data-x")),
    pad = await a.locator("#joystick").boundingBox();
  await drag(pad.x + pad.width / 2, pad.y + pad.height / 2, 28, 0, 600);
  await a.waitForTimeout(600);
  assert.ok(
    Number(await a.locator("#view").getAttribute("data-x")) > initial + 0.5,
    "touch pad did not move the player",
  );
  let r = await a.locator("#view").boundingBox();
  await drag(r.x + r.width * 0.5, r.y + r.height * 0.35, 0, 115);
  await a.locator('[data-material="7"]').tap();
  assert.equal(
    await a.locator('[data-material="7"]').getAttribute("aria-pressed"),
    "true",
    "touch palette selection was delayed",
  );
  await a.locator("#place").tap();
  await b.waitForFunction(() =>
    document.querySelector("#render-stats").textContent.includes("chunks"),
  );
  const end = Date.now() + 5000;
  while (!edits.some((c) => c.material === 7) && Date.now() < end)
    await a.waitForTimeout(50);
  assert.ok(
    edits.some((c) => c.material === 7),
    "second player did not receive the touch block placement: " +
      JSON.stringify({
        messages,
        edits,
        position: await a.locator("#view").evaluate((e) => ({ ...e.dataset })),
        toast: await a.locator(".toast").allTextContents(),
      }),
  );
  await a.locator("#break").tap();
  const removed = Date.now() + 5000;
  while (!edits.some((c) => c.material === 0) && Date.now() < removed)
    await a.waitForTimeout(50);
  assert.ok(
    edits.some((c) => c.material === 0),
    "second player did not receive the touch block removal",
  );
  await a.locator("#fly").tap();
  await a.waitForTimeout(100);
  const y = Number(await a.locator("#view").getAttribute("data-y")),
    up = await a.locator("#up").boundingBox();
  await drag(up.x + up.width / 2, up.y + up.height / 2, 0, 0, 500);
  assert.ok(
    Number(await a.locator("#view").getAttribute("data-y")) > y + 1,
    "touch flight did not ascend",
  );
  await a.locator("#explore").selectOption("2");
  await a.waitForFunction(
    () =>
      Number(document.querySelector("#view").dataset.x) > 400 &&
      document.querySelector("#welcome").hidden,
  );
  await a.waitForFunction(
    () => Number(document.querySelector("#view").dataset.chunks) === 49,
  );
  const refreshed = [];
  a.on("request", (request) => {
    const url = new URL(request.url());
    if (
      url.pathname === "/blockyard/chunk" &&
      url.searchParams.get("x") === "28"
    )
      refreshed.push(url.href);
  });
  const movement = await a.locator("#joystick").boundingBox();
  await drag(
    movement.x + movement.width / 2,
    movement.y + movement.height / 2,
    30,
    0,
    1000,
  );
  await a.waitForFunction(
    () => Number(document.querySelector("#view").dataset.x) > 416,
  );
  const crossed = Date.now() + 5000;
  while (!refreshed.length && Date.now() < crossed) await a.waitForTimeout(50);
  assert.ok(
    refreshed.length,
    "the new update area did not refresh prefetched chunks",
  );
  await a.locator(".fullscreen-toggle").tap();
  await a.waitForFunction(() => !!document.querySelector(".is-fullscreen"));
  await a.setViewportSize({ width: 844, height: 390 });
  assert.ok(
    await a.evaluate(
      () =>
        document.querySelector(".is-fullscreen").scrollWidth <=
        document.documentElement.clientWidth + 1,
    ),
  );
  await a.locator(".fullscreen-toggle").tap();
  await a.setViewportSize({ width: 320, height: 844 });
  assert.ok(
    await a.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth + 1,
    ),
  );
  if (process.env.BLOCKYARD_SCREENSHOT)
    await a.screenshot({
      path: process.env.BLOCKYARD_SCREENSHOT,
      fullPage: true,
    });
  for (const p of [a, b]) await p.close();
  await context.close();
  assert.deepEqual(errors, []);
  console.log(
    "two browsers, shared touch edits, movement, flight, landmarks, mobile widths, and fullscreen OK",
  );
} finally {
  await browser.close();
}
