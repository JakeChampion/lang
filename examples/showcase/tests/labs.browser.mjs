import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: [
    "--no-sandbox",
    "--enable-unsafe-swiftshader",
    "--autoplay-policy=no-user-gesture-required",
  ],
});
const base = process.argv[2] || "http://localhost:8080",
  room =
    process.env.FERN_LABS_TEST_ROOM || "browser-" + Date.now().toString(36),
  errors = [];
const context = await browser.newContext({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
});
context.on("page", (page) =>
  page.on("pageerror", (problem) => errors.push(problem.message)),
);
const a = await context.newPage(),
  b = await context.newPage();
const snapshots = new Map(),
  messages = new Map();
for (const page of [a, b]) {
  messages.set(page, []);
  page.on("websocket", (socket) =>
    socket.on("framereceived", ({ payload }) => {
      if (typeof payload === "string") {
        const data = JSON.parse(payload);
        messages.get(page).push(data);
        if (data.kind === "snapshot") snapshots.set(page, data);
      }
    }),
  );
}
const url = (app) => `${base}/${app}/?room=${room}`;
async function visit(app) {
  for (const p of [a, b]) {
    messages.set(p, []);
    await p.goto(url(app));
    await p.waitForSelector("main");
  }
  if (["foundry", "circuit", "patchbay"].includes(app))
    for (const p of [a, b])
      await p.waitForFunction(() =>
        document.querySelector("#connection").textContent.includes("Connected"),
      );
}
async function until(predicate, message) {
  const end = Date.now() + 10000;
  while (Date.now() < end) {
    if (predicate()) return;
    await a.waitForTimeout(30);
  }
  throw Error(message);
}
async function overflow(p) {
  assert.ok(
    await p.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth + 1,
    ),
    "horizontal page overflow",
  );
}
async function fullscreen(p) {
  await p.locator(".fullscreen-toggle").tap();
  await p.waitForFunction(() => !!document.querySelector(".is-fullscreen"));
  await p.setViewportSize({ width: 844, height: 390 });
  await overflow(p);
  await p.locator(".fullscreen-toggle").tap();
  await p.waitForFunction(() => !document.querySelector(".is-fullscreen"));
  await p.setViewportSize({ width: 320, height: 760 });
  await overflow(p);
  await p.setViewportSize({ width: 390, height: 844 });
}
try {
  await a.goto(base + "/");
  assert.equal(await a.locator(".example").count(), 15);
  await overflow(a);
  await a.getByRole("button", { name: "Make something", exact: true }).tap();
  assert.equal(await a.locator(".example:visible").count(), 10);
  await a.getByRole("button", { name: "All fifteen", exact: true }).tap();
  assert.equal(await a.locator(".example:visible").count(), 15);
  await visit("foundry");
  await a.waitForFunction(
    () => Number(document.querySelector("#view").dataset.tick) >= 0,
  );
  const initial = snapshots.get(a),
    point = 9 * 256 + 9,
    kind = initial.cells[point] === 1 ? 2 : 1;
  await a.locator(`[data-tool="${kind}"]`).tap();
  await a.locator("#view").tap({ position: { x: 25, y: 25 } });
  await until(
    () =>
      messages
        .get(b)
        .some(
          (m) =>
            m.kind === "change" &&
            m.points.includes(point) &&
            m.values[m.points.indexOf(point)] === kind,
        ),
    "touch factory edit did not reach second browser",
  );
  await a.locator("#pause").tap();
  await b.waitForFunction(
    () => document.querySelector("#pause").textContent === "Resume",
  );
  const tick = Number(await b.locator("#view").getAttribute("data-tick"));
  await a.getByRole("button", { name: "Single step", exact: true }).tap();
  await b.waitForFunction(
    (t) => Number(document.querySelector("#view").dataset.tick) > t,
    tick,
  );
  await fullscreen(a);
  await a.locator("#execution").selectOption("wasm");
  await a.waitForFunction(() =>
    document.querySelector("#metrics").textContent.includes("Wasm memory"),
  );
  await visit("circuit");
  await a.locator("#adder").tap();
  await a.waitForFunction(
    () => document.querySelector("#signal").textContent === "HIGH",
  );
  await a.locator("#cpu-run").tap();
  await a.waitForFunction(
    () => document.querySelector("#cpu-state").dataset.halted === "true",
  );
  assert.equal(await a.locator("#cpu-state").getAttribute("data-output"), "6");
  await a.locator('[aria-label="Operand 0"]').selectOption("2");
  await b.waitForFunction(
    () => document.querySelector('[aria-label="Operand 0"]').value === "2",
  );
  await a.locator("#count").selectOption("100000");
  await a.waitForFunction(() =>
    document.querySelector("#metrics").textContent.includes("100000"),
  );
  await fullscreen(a);
  await visit("patchbay");
  for (const p of [a, b]) {
    await p.locator("#enable-audio").tap();
    await p.waitForFunction(
      () =>
        document.querySelector("#connection").textContent === "Audio enabled.",
    );
  }
  const cell = a.locator("#sequence button").first(),
    before = await cell.getAttribute("aria-pressed");
  await cell.tap();
  await b.waitForFunction(
    (value) =>
      document
        .querySelector("#sequence button")
        .getAttribute("aria-pressed") !== value,
    before,
  );
  await a.locator("#play").tap();
  await a.waitForFunction(
    () => document.querySelector("#play").textContent === "Stop loop",
  );
  const cdp = await context.newCDPSession(a),
    key = await a.locator("#piano button").first().boundingBox();
  await a.locator("#piano button").first().scrollIntoViewIfNeeded();
  const live = await a.locator("#piano button").first().boundingBox();
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [{ x: live.x + live.width / 2, y: live.y + live.height / 2 }],
  });
  await until(
    () => messages.get(b).some((m) => m.kind === "note" && m.values[0] === 1),
    "live key did not reach the other browser",
  );
  await b.waitForFunction(
    () => Number(document.querySelector("#metrics strong").textContent) > 0,
  );
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
  await a.locator("#record").tap();
  await a.waitForTimeout(700);
  const downloaded = a.waitForEvent("download");
  await a.locator("#record").tap();
  const recording = await downloaded,
    bytes = await readFile(await recording.path());
  assert.ok(bytes.length > 500, "recorded audio is empty");
  assert.ok(
    bytes.subarray(0, 4).equals(Buffer.from([0x1a, 0x45, 0xdf, 0xa3])) ||
      bytes.toString("ascii", 4, 8) === "ftyp",
    "recording has no media container",
  );
  await fullscreen(a);
  await a.goto(url("prism"));
  await a.locator("#samples").selectOption("8");
  await a.waitForFunction(
    () => Number(document.querySelector("#view").dataset.progress) >= 1,
  );
  const single = await a
    .locator("#view")
    .evaluate((canvas) => canvas.toDataURL());
  await a.locator("#workers").selectOption("4");
  await a.waitForFunction(
    () => Number(document.querySelector("#view").dataset.progress) >= 1,
  );
  const parallel = await a
    .locator("#view")
    .evaluate((canvas) => canvas.toDataURL());
  assert.equal(single, parallel, "parallel workers changed the rendered image");
  await a.locator("#reference").tap();
  await a.waitForFunction(
    () =>
      document.querySelector("#reference").dataset.maxDifference !== undefined,
  );
  assert.ok(
    Number(await a.locator("#reference").getAttribute("data-max-difference")) <=
      1,
    "native and Wasm renderers differ",
  );
  await a.locator("#samples").selectOption("128");
  await a.getByRole("button", { name: "Cancel", exact: true }).tap();
  await a.waitForTimeout(300);
  assert.equal(
    await a.locator("#connection").textContent(),
    "Render cancelled.",
  );
  await fullscreen(a);
  await visit("fernbase");
  const keyName = "browser-" + Date.now().toString(36);
  await a.locator("#begin").tap();
  await a
    .locator("#sql")
    .fill(`INSERT INTO entries VALUES ('${keyName}','private draft');`);
  await a.locator("#run").tap();
  await a.waitForFunction(
    () =>
      document.querySelector("#connection").textContent ===
      "Draft evaluated. Nothing committed.",
  );
  await b.getByRole("button", { name: "Refresh", exact: true }).tap();
  assert.ok(!(await b.locator("#results").textContent()).includes(keyName));
  await a.locator("#commit").tap();
  await a.waitForFunction(
    () =>
      document.querySelector("#connection").textContent === "Commit confirmed.",
  );
  await b.getByRole("button", { name: "Refresh", exact: true }).tap();
  await b.waitForFunction(
    (key) => document.querySelector("#results").textContent.includes(key),
    keyName,
  );
  await a.locator("#begin").tap();
  await a
    .locator("#sql")
    .fill(`INSERT INTO entries VALUES ('${keyName}-stale','pending');`);
  await a.locator("#run").tap();
  await a.waitForFunction(() =>
    document
      .querySelector("#connection")
      .textContent.startsWith("Draft evaluated"),
  );
  await b
    .locator("#sql")
    .fill(`INSERT INTO entries VALUES ('${keyName}-other','concurrent');`);
  await b.locator("#run").tap();
  await b.waitForFunction(
    () =>
      document.querySelector("#connection").textContent ===
      "Transaction complete.",
  );
  await a.locator("#commit").tap();
  await a.waitForFunction(() =>
    document.querySelector("#connection").classList.contains("error"),
  );
  assert.ok(
    (await a.locator("#draft").textContent()).includes(keyName + "-stale"),
  );
  await a.locator("#rollback").tap();
  await fullscreen(a);
  await a.goto(url("lantern"));
  await a.waitForSelector("#search");
  const title = "<img src=x onerror=alert(1)> " + Date.now();
  await a.locator("#title").fill(title);
  await a.locator("#text").fill("violetbrowser CAFÉ 東京");
  await a.locator("#add").tap();
  await a.waitForFunction(
    () =>
      document.querySelector("#connection").textContent === "Document indexed.",
  );
  await a.locator("#query").fill("violetbrowser");
  await a.locator("#search").tap();
  await a.waitForFunction(() =>
    document.querySelector("#results").textContent.includes("violetbrowser"),
  );
  assert.ok((await a.locator("#results").textContent()).includes(title));
  assert.equal(await a.locator("#results img").count(), 0);
  await a.locator("input[type=file]").setInputFiles({
    name: "archive.ndjson",
    mimeType: "application/x-ndjson",
    buffer: Buffer.from(
      JSON.stringify({
        title: "Streamed document",
        text: "streamedbrowser café 東京",
      }) +
        "\n" +
        JSON.stringify({
          title: "Second document",
          text: "streamedbrowser more text",
        }) +
        "\n",
    ),
  });
  await a.waitForFunction(
    () =>
      document.querySelector("#connection").textContent === "Import complete.",
  );
  await a.locator("#query").fill("streamedbrowser");
  await a.locator("#search").tap();
  await a.waitForFunction(
    () => document.querySelectorAll("#results .result").length === 2,
  );
  await fullscreen(a);
  assert.deepEqual(errors, []);
  console.log(
    "six apps: shared touch edits, native factory control, CPU programs, live audio and recording, parallel rendering parity, transaction isolation, streamed search import, XSS escaping, mobile widths, and fullscreen OK",
  );
} finally {
  await browser.close();
}
