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
    viewport: { width: 1280, height: 1000 },
  });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  assert.equal((await page.goto(base + "/contour")).status(), 200);
  assert.match(page.url(), /\/contour\/$/);
  await page.waitForFunction(() =>
    document.querySelector("#status").textContent.includes("ready"),
  );
  assert.equal(await page.locator("#pause").textContent(), "Resume flight");
  const capture = () =>
    page.locator("#terrain").evaluate((canvas) => canvas.toDataURL());
  const first = await capture();
  const palette = await page.locator("#terrain").evaluate((canvas) => {
    const data = canvas
      .getContext("2d")
      .getImageData(0, 0, canvas.width, canvas.height).data;
    const colors = new Map();
    for (let i = 0; i < data.length; i += 4) {
      const key = [...data.slice(i, i + 4)].join(",");
      colors.set(key, (colors.get(key) || 0) + 1);
    }
    return Object.fromEntries(colors);
  });
  assert.deepEqual(Object.keys(palette).sort(), [
    "16,6,184,255",
    "243,245,43,255",
  ]);
  assert.ok(
    palette["243,245,43,255"] > 500,
    "visible terrain has yellow ridges",
  );
  await page.waitForTimeout(200);
  assert.equal(
    await capture(),
    first,
    "reduced motion starts with a still landscape",
  );
  await page.locator("#terrain").press("ArrowRight");
  assert.notEqual(await capture(), first, "keyboard steering changes terrain");
  await page.locator("#reset").click();
  assert.equal(await capture(), first, "reset restores starting landscape");
  const box = await page.locator("#terrain").boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 80, box.y + box.height / 2);
  await page.mouse.up();
  assert.notEqual(await capture(), first, "dragging steers the landscape");
  await page.locator("#reset").click();
  await page.locator("#altitude").fill("170");
  assert.equal(await page.locator("#altitude-value").textContent(), "170");
  assert.notEqual(await capture(), first, "altitude changes projection");
  await page.locator("#detail").selectOption("480");
  assert.equal(await page.locator("#terrain").evaluate((c) => c.width), 480);
  await page.locator("#pause").click();
  await page.waitForTimeout(250);
  await page.locator("#pause").click();
  const still = await capture();
  await page.waitForTimeout(200);
  assert.equal(await capture(), still, "pause stops drawing");
  await page.locator("#reset").click();
  await page.locator("#detail").selectOption("320");
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: process.env.CONTOUR_SCREENSHOT || "/tmp/contour-desktop.png",
    fullPage: true,
  });
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => {
      document.documentElement.dataset.theme = theme;
    }, theme);
    for (const width of [360, 768, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      assert.ok(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
        `no overflow: ${theme} ${width}`,
      );
    }
  }
  await page.goto(base + "/");
  await page
    .getByRole("button", { name: "Make something", exact: true })
    .click();
  assert.equal(
    await page.locator('.example[href="/contour/"]:visible').count(),
    1,
  );
  assert.equal(await page.locator(".example:visible").count(), 13);
  await page.goto(base + "/_src/showcase/contour.fern");
  assert.ok(
    await page
      .getByText("two colours, a whole landscape", { exact: false })
      .count(),
  );
  assert.deepEqual(errors, []);
  console.log(
    "Contour: two-colour terrain, reduced motion, flight controls, pause, reset, detail, layouts, gallery and source OK",
  );
} finally {
  await browser.close();
}
