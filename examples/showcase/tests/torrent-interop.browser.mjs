// Shares a file between the Fern torrent page and the WebTorrent client, in
// both directions, through the showcase tracker.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
const { chromium } = await import(
  process.env.PLAYWRIGHT_MODULE || "playwright"
);
const base = process.argv[2] || "http://localhost:8080";
const bundle = readFileSync(
  createRequire(import.meta.url).resolve("webtorrent/dist/webtorrent.min.js"),
);
const browser = await chromium.launch({
  executablePath: process.env.CHROMIUM || "/usr/bin/chromium",
  args: ["--no-sandbox", "--disable-features=WebRtcHideLocalIpsWithMdns"],
});
const errors = [];
const page = async () => {
  const p = await (await browser.newContext()).newPage();
  p.on("pageerror", (e) => errors.push(e.message));
  await p.route("**/webtorrent.mjs", (route) =>
    route.fulfill({ body: bundle, contentType: "text/javascript" }),
  );
  return p;
};
const sha256 = "async (bytes) => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))).join(',')";

// WebTorrent downloads from a Fern seeder.
const fern = await page();
await fern.goto(base + "/torrent/");
await fern.waitForFunction(() => document.querySelector("#connection").textContent.includes("Ready"));
await fern.locator("#seed").fill("9");
await fern.locator("#seed-run").click();
await fern.waitForFunction(() => document.querySelector("#connection").textContent.includes("Seeding"), null, { timeout: 60000 });
const magnetLink = await fern.locator("#magnet").inputValue();
const wt = await page();
await wt.goto(base + "/healthz");
const fromFern = await wt.evaluate(
  async ([magnetLink, sha256]) => {
    const { default: WebTorrent } = await import("/webtorrent.mjs");
    const client = new WebTorrent();
    const file = await new Promise((resolve, reject) => {
      client.on("error", (e) => reject(String(e)));
      client.add(magnetLink, (torrent) => torrent.on("done", () => resolve(torrent.files[0])));
    });
    const bytes = new Uint8Array(await file.arrayBuffer());
    const digest = await eval(sha256)(bytes);
    client.destroy();
    return { name: file.name, length: bytes.length, digest };
  },
  [magnetLink, sha256],
);
const expected = await fern.evaluate(async (sha256) => {
  const { kernel } = await import("/bench/kernel.mjs");
  const k = await (await kernel("/bench/torrent.wasm", "fern:showcase/torrent"))();
  return eval(sha256)(k.list(k.call("sample", 9, 1048576)));
}, sha256);
assert.deepEqual(fromFern, { name: "fern-sample-9.txt", length: 1048576, digest: expected });
console.log("torrent interop: WebTorrent downloads from a Fern seeder OK");

// A Fern page downloads from a WebTorrent seeder. The Fern kernel computes
// the same info hash, which the page registers before WebTorrent announces.
const seeded = await wt.evaluate(async (sha256) => {
  const { kernel } = await import("/bench/kernel.mjs");
  const fresh = await kernel("/bench/torrent.wasm", "fern:showcase/torrent");
  const call = async (name, ...args) => {
    const k = await fresh();
    return k.list(k.call(name, ...args));
  };
  const data = new Uint8Array(300000);
  for (let at = 0; at < data.length; at += 65536) crypto.getRandomValues(data.subarray(at, at + 65536));
  const info = await call("info", "random.bin", data, 16384);
  const hash = Array.from(await call("infohash", info), (b) => b.toString(16).padStart(2, "0")).join("");
  const registered = await fetch("/torrent/register", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ info_hash: hash, name: "random.bin", length: data.length }),
  });
  const { default: WebTorrent } = await import("/webtorrent.mjs");
  window.client = new WebTorrent();
  const announce = location.origin.replace(/^http/, "ws") + "/torrent/announce";
  const infoHash = await new Promise((resolve) =>
    window.client.seed(new File([data], "random.bin"), { announce: [announce], pieceLength: 16384, name: "random.bin" }, (t) => resolve(t.infoHash)),
  );
  return { hash, infoHash, status: registered.status, digest: await eval(sha256)(data) };
}, sha256);
assert.equal(seeded.status, 200);
assert.equal(seeded.infoHash, seeded.hash);
const downloader = await page();
await downloader.goto(base + "/torrent/#" + seeded.hash);
await downloader.waitForFunction(
  () => document.querySelector("#connection").textContent.includes("Downloaded and verified"),
  null,
  { timeout: 90000 },
);
const got = await downloader.evaluate(
  async (sha256) => eval(sha256)(await (await fetch(document.querySelector("#save").href)).arrayBuffer()),
  sha256,
);
assert.equal(got, seeded.digest);
assert.equal(await downloader.locator("#file-name").textContent(), "random.bin");
console.log("torrent interop: a Fern page downloads from a WebTorrent seeder OK");

assert.deepEqual(errors, []);
await browser.close();
