import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const base = process.argv[2] || "http://localhost:8080";
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: ["--no-sandbox"],
});
try {
  const page = await browser.newPage({
    reducedMotion: "reduce",
    viewport: { width: 1400, height: 1000 },
    acceptDownloads: true,
  });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(base + "/uxn");
  assert.match(page.url(), /\/uxn\/$/);
  const ready = () =>
    page.waitForFunction(
      () =>
        !document.querySelector("#assemble").disabled &&
        !document.querySelector("#reset").disabled,
    );
  await ready();
  assert.equal(
    await page.locator("#steps").textContent(),
    "0",
    "reduced motion starts paused",
  );
  await page.locator("#demo").selectOption("arithmetic");
  await ready();
  await page.locator("#step").click();
  await page.waitForFunction(
    () => document.querySelector("#pc").textContent === "0103",
  );
  assert.match(await page.locator("#wst").textContent(), /00 01/);
  assert.equal(await page.locator("#steps").textContent(), "1");
  assert.equal(await page.locator("#memory mark").textContent(), "a0");
  await page.locator("#run").click();
  await page.waitForFunction(() =>
    document.querySelector("#wst").textContent.includes("02 d0"),
  );
  assert.equal(await page.locator("#next-op").textContent(), "BRK / YIELDED");
  await page.locator("#memory-page").fill("0200");
  assert.match(await page.locator("#memory").textContent(), /^0200  02 d0/);
  await page.locator("#reset").click();
  await ready();
  assert.equal(await page.locator("#steps").textContent(), "0");
  await page.locator("#demo").selectOption("hello");
  await ready();
  await page.locator("#run").click();
  await page.waitForFunction(() =>
    document
      .querySelector("#console")
      .textContent.includes("Running inside Fern WebAssembly."),
  );
  const download = page.waitForEvent("download");
  await page.locator("#download").click();
  assert.equal((await download).suggestedFilename(), "fern-uxn.rom");
  // Real uploaded bytecode executes through the Fern VM, not the assembler.
  await page.locator("#upload").setInputFiles({
    name: "hi.rom",
    mimeType: "application/octet-stream",
    buffer: Buffer.from([0x80, 0x48, 0x80, 0x18, 0x17, 0]),
  });
  await ready();
  await page.locator("#run").click();
  await page.waitForFunction(
    () => document.querySelector("#console").textContent === "H",
  );
  await page.locator("#source").fill("|0100 ;missing BRK");
  await page.locator("#assemble-step").click();
  await page.locator("#diagnostic").waitFor({ state: "visible" });
  assert.match(
    await page.locator("#diagnostic").textContent(),
    /Unknown label/,
  );
  await page.locator("#source").fill("|0100 #02 #03 ADD BRK");
  await page.locator("#assemble").click();
  await ready();
  await page.waitForFunction(() =>
    document.querySelector("#wst").textContent.includes("05"),
  );
  await page.locator("#demo").selectOption("echo");
  await ready();
  await page.locator("#console-input").fill("Hello, event vector!");
  await page.locator("#send").click();
  await page.waitForFunction(
    () =>
      document.querySelector("#console").textContent ===
      "Hello, event vector!\n",
  );
  await page.locator("#demo").selectOption("garden");
  await ready();
  await page.locator("#run").click();
  await page.waitForFunction(
    () =>
      document.querySelector("#screen").width === 128 &&
      Number(document.querySelector("#steps").textContent.replaceAll(",", "")) >
        1200000,
  );
  await page.locator("#pause").click();
  const count = await page.locator("#steps").textContent();
  await page.waitForTimeout(150);
  assert.equal(
    await page.locator("#steps").textContent(),
    count,
    "pause stops committing VM slices",
  );
  const colors = await page.locator("#screen").evaluate((canvas) => {
    const pixels = canvas
        .getContext("2d")
        .getImageData(0, 0, canvas.width, canvas.height).data,
      colors = new Set();
    for (let i = 0; i < pixels.length; i += 4)
      colors.add([...pixels.slice(i, i + 4)].join(","));
    return colors.size;
  });
  assert.ok(colors >= 3, "garden ROM paints multiple palette colours");
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: process.env.UXN_SCREENSHOT || "/tmp/uxn-desktop.png",
    fullPage: true,
  });
  await page.locator("#demo").selectOption("paint");
  await ready();
  await page.locator("#run").click();
  await page.waitForFunction(() =>
    document.querySelector("#next-op").textContent.includes("YIELDED"),
  );
  const before = await page.locator("#screen").evaluate((c) => c.toDataURL());
  const box = await page.locator("#screen").boundingBox();
  await page.mouse.move(box.x + box.width * 0.25, box.y + box.height * 0.25);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width * 0.4, box.y + box.height * 0.4, {
    steps: 5,
  });
  await page.mouse.up();
  await page.waitForTimeout(250);
  assert.notEqual(
    await page.locator("#screen").evaluate((c) => c.toDataURL()),
    before,
    "mouse vector paints sprites",
  );
  const touchBefore = await page
    .locator("#screen")
    .evaluate((c) => c.toDataURL());
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [{ x: box.x + box.width * 0.7, y: box.y + box.height * 0.3 }],
  });
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
  await page.waitForTimeout(150);
  assert.notEqual(
    await page.locator("#screen").evaluate((c) => c.toDataURL()),
    touchBefore,
    "touch events paint sprites",
  );
  await page.locator("#pause").click();
  await page
    .locator("#source")
    .fill(
      "|0100 #80 #0f DEO #0000 @loop INC2 DUP2 #ffff NEQ2 ?loop POP2 #41 #18 DEO BRK",
    );
  await page.locator("#assemble").click();
  await ready();
  await page.waitForFunction(
    () => document.querySelector("#mode").textContent === "EXITED",
  );
  assert.equal(
    await page.locator("#console").textContent(),
    "A",
    "exit waits for the current vector across budget slices",
  );
  assert.ok(
    await page.locator("#run").isDisabled(),
    "an exited ROM requires reset",
  );
  // Infinite bytecode remains pausable; reset aborts outstanding worker work.
  await page.locator("#source").fill("|0100 @loop INC !loop");
  await page.locator("#assemble").click();
  await ready();
  await page.waitForFunction(
    () =>
      Number(
        document.querySelector("#steps").textContent.replaceAll(",", ""),
      ) >= 20000,
  );
  await page.locator("#pause").click();
  await page.locator("#reset").click();
  await ready();
  assert.equal(await page.locator("#steps").textContent(), "0");
  for (const theme of ["light", "dark"])
    for (const width of [360, 768, 1280]) {
      await page.evaluate((t) => {
        document.documentElement.dataset.theme = t;
      }, theme);
      await page.setViewportSize({ width, height: 900 });
      assert.ok(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
        `no overflow ${theme} ${width}`,
      );
    }
  await page.goto(base + "/");
  await page
    .getByRole("button", { name: "Make something", exact: true })
    .click();
  assert.equal(await page.locator('.example[href="/uxn/"]:visible').count(), 1);
  assert.equal(await page.locator(".example:visible").count(), 13);
  await page.goto(base + "/_src/showcase/uxn.fern");
  assert.ok(
    await page
      .getByText("a little computer, implemented in Fern", { exact: false })
      .count(),
  );
  assert.deepEqual(errors, []);
  console.log(
    "Uxn browser: assembler, stepping, stacks/RAM, ROM upload/download, console, screen/mouse events, pause/reset, reduced motion, layouts, gallery and source OK",
  );
} finally {
  await browser.close();
}
