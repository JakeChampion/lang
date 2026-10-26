// Shares a file between two browser pages through the showcase tracker, and
// checks that the tracker refuses a swarm that the page did not register.
import assert from "node:assert/strict";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const base = process.argv[2] || "http://localhost:8080";
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  // Two pages on one host connect over host candidates, which Chromium
  // otherwise hides behind mDNS names.
  args: ["--no-sandbox", "--disable-features=WebRtcHideLocalIpsWithMdns"],
});
const errors = [];
const page = async () => {
  const context = await browser.newContext();
  const p = await context.newPage();
  p.on("pageerror", (e) => errors.push(e.message));
  return p;
};

const seeder = await page();
await seeder.goto(base + "/torrent/");
await seeder.waitForFunction(() => document.querySelector("#connection").textContent.includes("Ready"));
await seeder.locator("#source").selectOption("1048576");
await seeder.locator("#seed-run").click();
await seeder.waitForFunction(() => document.querySelector("#connection").textContent.includes("Seeding"), null, { timeout: 60000 });
const link = await seeder.locator("#share-link").getAttribute("href");
assert.match(link, /\/torrent\/#[0-9a-f]{40}$/);
assert.match(await seeder.locator("#magnet").inputValue(), /^magnet:\?xt=urn:btih:[0-9a-f]{40}&dn=fern-sample-1\.txt&tr=ws/);
assert.equal(await seeder.locator("#stat-pieces").textContent(), "64 / 64");

const leecher = await page();
await leecher.goto(link);
await leecher.waitForFunction(
  () => document.querySelector("#connection").textContent.includes("Downloaded and verified"),
  null,
  { timeout: 90000 },
);
assert.equal(await leecher.locator("#stat-pieces").textContent(), "64 / 64");
const digest = (p) =>
  p.evaluate(async () => {
    const url = document.querySelector("#save").href || null;
    const bytes = url
      ? await (await fetch(url)).arrayBuffer()
      : await (await fetch(document.querySelector("#torrent-file").href)).arrayBuffer();
    return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))).join(",");
  });
const original = await seeder.evaluate(async () => {
  const { kernel } = await import("/bench/kernel.mjs");
  const k = await (await kernel("/bench/torrent.wasm", "fern:showcase/torrent"))();
  const data = k.list(k.call("sample", 1, 1048576));
  return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", data))).join(",");
});
assert.equal(await digest(leecher), original);
assert.ok(Number.parseFloat(await seeder.locator("#stat-up").textContent()) > 0);
console.log("torrent: metadata exchange, piece transfer, and SHA-1 checks between two pages OK");

const refused = await leecher.evaluate(
  (url) =>
    new Promise((resolve) => {
      const ws = new WebSocket(url);
      const id = String.fromCharCode(...new Uint8Array(20).fill(7));
      ws.onopen = () => ws.send(JSON.stringify({ action: "announce", info_hash: id, peer_id: id, offers: [] }));
      ws.onmessage = (e) => resolve(JSON.parse(e.data)["failure reason"] ?? "");
      setTimeout(() => resolve("timeout"), 5000);
    }),
  base.replace(/^http/, "ws") + "/torrent/announce",
);
assert.match(refused, /only torrents that the Fern showcase registered/);
const statuses = await leecher.evaluate(async () => {
  const post = (body) =>
    fetch("/torrent/register", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).then((r) => r.status);
  return [
    await post({ info_hash: "XYZ", name: "a", length: 1 }),
    await post({ info_hash: "a".repeat(40), name: "a", length: 64 * 1024 * 1024 + 1 }),
  ];
});
assert.deepEqual(statuses, [400, 413]);
console.log("torrent: the tracker refuses unregistered swarms and oversized files OK");

assert.deepEqual(errors, []);
await browser.close();
