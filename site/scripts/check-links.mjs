// Find broken internal links in the docs site.
//
//   node scripts/check-links.mjs --dist dist
//   node scripts/check-links.mjs --site https://fern-lang.netlify.app
//
// --dist checks a build: each href and src that starts with / must name a
// file in the build. --site checks a deployed site: it reads every
// page in the sitemap and requests each link on that site's origin.
// Links to other sites are not checked, because their status changes
// for reasons that have no relation to this site.

import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const LINK = /\s(?:href|src)="([^"#?]*)/g;

export function linksIn(html) {
  return [...html.matchAll(LINK)].map((m) => m[1]).filter((l) => l !== "");
}

function htmlFiles(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return htmlFiles(p);
    return name.endsWith(".html") ? [p] : [];
  });
}

// The file in `dist` that a site path names, or null.
export function fileFor(dist, path) {
  const rel = decodeURIComponent(path).replace(/^\//, "");
  for (const candidate of [rel, join(rel, "index.html"), `${rel.replace(/\/$/, "")}.html`]) {
    const p = join(dist, candidate);
    if (existsSync(p) && statSync(p).isFile()) return p;
  }
  return null;
}

function checkDist(dist) {
  const broken = new Map();
  let checked = 0;
  for (const page of htmlFiles(dist)) {
    for (const link of linksIn(readFileSync(page, "utf8"))) {
      if (!link.startsWith("/") || link.startsWith("//")) continue;
      checked++;
      if (fileFor(dist, link) === null) {
        if (!broken.has(link)) broken.set(link, page.slice(dist.length + 1));
      }
    }
  }
  return { checked, broken };
}

async function fetchText(url) {
  const res = await fetch(url, { redirect: "follow" });
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
  return res.text();
}

async function sitemapPages(site) {
  const index = await fetchText(`${site}/sitemap-index.xml`);
  const maps = [...index.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]);
  const pages = [];
  for (const map of maps) {
    const xml = await fetchText(map);
    pages.push(...[...xml.matchAll(/<loc>([^<]+)<\/loc>/g)].map((m) => m[1]));
  }
  return pages;
}

async function checkSite(site) {
  const origin = new URL(site).origin;
  const pages = await sitemapPages(site);
  const firstSeen = new Map();
  for (let i = 0; i < pages.length; i += 16) {
    const batch = pages.slice(i, i + 16);
    const htmls = await Promise.all(batch.map(fetchText));
    htmls.forEach((html, j) => {
      for (const link of linksIn(html)) {
        const url = new URL(link, batch[j]);
        if (url.origin !== origin) continue;
        if (!firstSeen.has(url.href)) firstSeen.set(url.href, batch[j]);
      }
    });
  }
  const broken = new Map();
  const urls = [...firstSeen.keys()];
  for (let i = 0; i < urls.length; i += 16) {
    await Promise.all(urls.slice(i, i + 16).map(async (url) => {
      const res = await fetch(url, { method: "HEAD", redirect: "follow" }).catch((e) => ({ status: String(e) }));
      if (res.status !== 200) broken.set(`${url} (${res.status})`, firstSeen.get(url));
    }));
  }
  return { checked: urls.length, broken, pages: pages.length };
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);
  const opt = (name) => {
    const i = args.indexOf(name);
    return i >= 0 ? args[i + 1] : undefined;
  };
  const site = opt("--site")?.replace(/\/$/, "");
  const result = site
    ? await checkSite(site)
    : checkDist(opt("--dist") ?? "dist");
  const what = site ? `${result.checked} links from ${result.pages} pages of ${site}` : `${result.checked} links`;
  console.log(`check-links: checked ${what}, ${result.broken.size} broken`);
  for (const [link, page] of result.broken) console.log(`  ${link}  (first on ${page})`);
  process.exit(result.broken.size === 0 ? 0 : 1);
}
