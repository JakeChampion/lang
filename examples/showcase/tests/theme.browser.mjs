// Checks every page in both color themes: WCAG text contrast against the
// rendered background, no horizontal overflow at phone width, and the
// theme control's stored choice.
import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const base = process.argv[2] || "http://localhost:8080";
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: ["--no-sandbox"],
});

const routes = [
  "/",
  "/rally/",
  "/mosaic/",
  "/wave/",
  "/notes/",
  "/pulse/",
  "/swarm/",
  "/contour/",
  "/uxn/",
  "/gather/",
  "/blockyard/",
  "/foundry/",
  "/patchbay/",
  "/prism/",
  "/fernbase/",
  "/circuit/",
  "/lantern/",
  "/json/",
  "/compress/",
  "/handshake/",
  "/image/",
  "/torrent/",
  "/fractal/",
  "/_src/",
  "/_src/showcase/rally.fern.md",
  "/_src/fractal/kernel.fern",
];

// Runs in the page. Returns each text element whose contrast is too low.
// Decorative text (aria-hidden), disabled controls, and text over an
// image without a background color are out of scope.
function audit() {
  const probe = document.createElement("canvas").getContext("2d", {
    willReadFrequently: true,
  });
  const rgba = (css) => {
    probe.clearRect(0, 0, 1, 1);
    probe.fillStyle = "#000";
    probe.fillStyle = css;
    probe.fillRect(0, 0, 1, 1);
    const [r, g, b, a] = probe.getImageData(0, 0, 1, 1).data;
    return [r, g, b, a / 255];
  };
  const over = ([r, g, b, a], [R, G, B]) => [
    r * a + R * (1 - a),
    g * a + G * (1 - a),
    b * a + B * (1 - a),
  ];
  const luminance = (rgb) => {
    const [r, g, b] = rgb.map((v) => {
      v /= 255;
      return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const background = (element) => {
    const layers = [];
    for (let e = element; e; e = e.parentElement) {
      const style = getComputedStyle(e),
        color = rgba(style.backgroundColor);
      if (style.backgroundImage !== "none" && color[3] === 0) return null;
      if (color[3] > 0) layers.push(color);
      if (color[3] >= 1) break;
    }
    let result = [255, 255, 255];
    for (const layer of layers.reverse()) result = over(layer, result);
    return result;
  };
  const failures = [];
  for (const element of document.body.querySelectorAll("*")) {
    const own = [...element.childNodes]
      .filter((n) => n.nodeType === 3)
      .map((n) => n.textContent.trim())
      .join(" ")
      .trim();
    if (!own || element.closest("svg, canvas, [aria-hidden='true'], script, style"))
      continue;
    if (element.closest(":disabled, .skip-link, noscript")) continue;
    const rect = element.getBoundingClientRect(),
      style = getComputedStyle(element);
    if (!rect.width || !rect.height || style.visibility !== "visible")
      continue;
    let opacity = 1;
    for (let e = element; e; e = e.parentElement)
      opacity *= Number(getComputedStyle(e).opacity);
    if (opacity < 0.05) continue;
    const bg = background(element);
    if (!bg) continue;
    const fg = rgba(style.color);
    fg[3] *= opacity;
    const a = luminance(over(fg, bg)),
      b = luminance(bg),
      ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05),
      size = parseFloat(style.fontSize),
      large = size >= 24 || (size >= 18.66 && Number(style.fontWeight) >= 700),
      needed = large ? 3 : 4.5;
    if (ratio < needed)
      failures.push(
        `${element.tagName.toLowerCase()}.${[...element.classList].join(".")} "${own.slice(0, 40)}" ${ratio.toFixed(2)} < ${needed} (${style.color} on rgb(${bg.map(Math.round)}))`,
      );
  }
  return failures;
}

const problems = [];
for (const colorScheme of ["dark", "light"]) {
  for (const [label, viewport] of [
    ["desktop", { width: 1280, height: 900 }],
    ["mobile", { width: 390, height: 844 }],
    ["narrow", { width: 320, height: 640 }],
  ]) {
    const context = await browser.newContext({ colorScheme, viewport });
    for (const route of routes) {
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.goto(base + route, { waitUntil: "load" });
      await page.waitForTimeout(700);
      const where = `${route} ${colorScheme} ${label}`;
      for (const failure of await page.evaluate(audit))
        problems.push(`${where}: contrast ${failure}`);
      const [scroll, client] = await page.evaluate(() => [
        document.documentElement.scrollWidth,
        document.documentElement.clientWidth,
      ]);
      if (scroll > client + 1)
        problems.push(`${where}: overflows by ${scroll - client}px`);
      for (const error of errors) problems.push(`${where}: ${error}`);
      await page.close();
    }
    await context.close();
  }
}

// The control cycles auto → light → dark → auto and remembers the choice.
const context = await browser.newContext({ colorScheme: "dark" });
const page = await context.newPage();
await page.goto(base + "/rally/");
const toggle = page.locator(".theme-toggle");
const theme = () => page.evaluate(() => document.documentElement.dataset.theme);
const paper = () =>
  page.evaluate(() => getComputedStyle(document.body).backgroundColor);
assert.equal(await theme(), undefined);
await toggle.click();
assert.equal(await theme(), "light");
assert.equal(await paper(), "rgb(250, 247, 240)");
await page.goto(base + "/foundry/");
await page.waitForSelector(".theme-toggle");
assert.equal(await theme(), "light", "the choice carries to the next page");
await page.goto(base + "/_src/");
assert.equal(await theme(), "light", "the source browser applies the choice");
await page.goto(base + "/");
await page.locator(".theme-toggle").click();
assert.equal(await theme(), "dark");
await page.locator(".theme-toggle").click();
assert.equal(await theme(), undefined);
await context.close();
await browser.close();

if (problems.length) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log(
  `${routes.length} pages × 2 themes × 3 widths: contrast, overflow, and theme control OK`,
);
