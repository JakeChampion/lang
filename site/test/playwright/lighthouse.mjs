// Lighthouse scores for a few pages of the built site, against fixed
// thresholds. The Docs build lane runs it after the Playwright suite.
//
//   node lighthouse.mjs        (from site/test/playwright, after `npm run build`)
//
// The thresholds come from measured scores. Accessibility, best practices
// and SEO were 100 on every page here, and performance was 95 on each, so
// its threshold keeps 5 points for a slower runner. A layout shift above
// 0.1 is outside the "good" range of Core Web Vitals.

import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

import * as chromeLauncher from "chrome-launcher";
import lighthouse from "lighthouse";
import { chromium } from "playwright";

const PAGES = ["/", "/tutorial/types/", "/cookbook/", "/stdlib/json/", "/playground/"];
const MIN = { performance: 90, accessibility: 100, "best-practices": 100, seo: 100 };
const MAX_CLS = 0.1;
const port = Number(process.env.LIGHTHOUSE_PORT ?? 4351);
const site = fileURLToPath(new URL("../..", import.meta.url));

const server = spawn("npx", ["astro", "preview", "--host", "127.0.0.1", "--port", String(port)], {
  cwd: site,
  stdio: "ignore",
});
const origin = `http://127.0.0.1:${port}`;
for (let i = 0; ; i++) {
  if (await fetch(origin).then((r) => r.ok, () => false)) break;
  if (i === 120) throw new Error("astro preview did not start");
  await new Promise((r) => setTimeout(r, 500));
}

const chrome = await chromeLauncher.launch({
  chromePath: chromium.executablePath(),
  chromeFlags: ["--headless=new", "--no-sandbox"],
});
let failed = 0;
try {
  for (const page of PAGES) {
    const { lhr } = await lighthouse(origin + page, { port: chrome.port, output: "json", logLevel: "error" });
    const scores = Object.fromEntries(Object.entries(lhr.categories).map(([k, v]) => [k, Math.round(v.score * 100)]));
    const cls = lhr.audits["cumulative-layout-shift"].numericValue;
    const low = Object.entries(MIN).filter(([k, min]) => scores[k] < min).map(([k, min]) => `${k} ${scores[k]} < ${min}`);
    if (cls > MAX_CLS) low.push(`layout shift ${cls.toFixed(3)} > ${MAX_CLS}`);
    for (const [k, min] of Object.entries(MIN)) {
      if (k === "performance" || scores[k] >= min) continue;
      for (const ref of lhr.categories[k].auditRefs) {
        const audit = lhr.audits[ref.id];
        if (ref.weight > 0 && audit.score !== null && audit.score < 1) low.push(`  ${k}: ${audit.id}`);
      }
    }
    console.log(`${page.padEnd(18)} ${Object.entries(scores).map(([k, v]) => `${k} ${v}`).join(", ")}, layout shift ${cls.toFixed(3)}`);
    for (const line of low) console.log(`  FAIL ${line}`);
    if (low.length > 0) failed++;
  }
} finally {
  await chrome.kill();
  server.kill();
}
process.exit(failed === 0 ? 0 : 1);
