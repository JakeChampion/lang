import test from "node:test";
import assert from "node:assert/strict";
import { TileCache, TileDelivery, percentile, serverTiming } from "../web/delivery.mjs";

const tick = () => new Promise((resolve) => setImmediate(resolve));
const job = (x) => ({ z: 20, x, y: 5 });
const png = new Blob([new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10])], { type: "image/png" });
function setup(extra = {}) {
  const requests = [], sockets = [], shown = [], errors = [];
  const delivery = new TileDelivery({ version: "test-version", origin: "https://fern.test", session: "0123456789abcdef",
    fetch: (url, options = {}) => {
      if (options.method === "POST") { requests.push({ url, options }); return Promise.resolve({ ok: true }); }
      return new Promise((resolve, reject) => {
        const request = { url, options, resolve: () => resolve({ ok: true, headers: new Headers({ "server-timing": "render;dur=12, compute;dur=8, color;dur=1, png;dur=3" }), blob: async () => png }) };
        options.signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
        requests.push(request);
      });
    },
    socket: (url) => { const socket = { url, sent: [], send(data) { this.sent.push(data); }, close() { this.closed = true; } }; sockets.push(socket); return socket; },
    onTile: (...args) => shown.push(args), onError: (...args) => errors.push(args), ...extra,
  });
  return { delivery, requests, sockets, shown, errors };
}
function response(id, ms = 12) {
  const data = new ArrayBuffer(40), view = new DataView(data);
  view.setUint32(0, id); view.setUint32(4, ms * 1000);
  view.setUint32(20, 8000); view.setUint32(24, 1000); view.setUint32(28, 3000);
  view.setUint32(32, 0x89504e47); view.setUint32(36, 0x0d0a1a0a);
  return data;
}

test("LRU cache respects byte and entry limits and refreshes reads", () => {
  const cache = new TileCache(16, 2), item = { blob: png, info: {} };
  cache.put("a", item); cache.put("b", item); cache.get("a"); cache.put("c", item);
  assert.equal(cache.get("b"), undefined); assert.equal(cache.bytes, 16);
  cache.put("a", { blob: new Blob([new Uint8Array(3)]), info: {} });
  assert.equal(cache.bytes, 11);
  cache.put("huge", { blob: new Blob([new Uint8Array(32)]), info: {} });
  assert.equal(cache.bytes, 11); assert.equal(cache.entries.size, 2);
});

test("HTTP dispatch prioritizes the viewport and bounds concurrency", async () => {
  const { delivery, requests, shown } = setup({ priority: (task) => task.x });
  delivery.setTransport("http");
  delivery.request(job(9)); delivery.request(job(2)); delivery.request(job(1));
  await tick();
  assert.equal(requests.length, 2);
  assert.match(requests[0].url, /\/tile\/test-version\/20\/1\/5$/);
  assert.match(requests[1].url, /\/20\/2\/5$/);
  requests[0].resolve(); await tick();
  assert.equal(shown.length, 1); assert.equal(shown[0][2].stages.png, 3);
  assert.match(requests[2].url, /\/20\/9\/5$/);
  delivery.close();
});

test("queued cancellation sends no request and active cancellation aborts and signals the server", async () => {
  const { delivery, requests, shown } = setup({ concurrency: 1 });
  delivery.setTransport("http");
  const cancelActive = delivery.request(job(1));
  const cancelQueued = delivery.request(job(2));
  cancelQueued(); await tick(); cancelActive(); await tick();
  assert.equal(requests.filter((r) => !r.options.method).length, 1);
  assert.equal(requests[0].options.signal.aborted, true);
  assert.match(requests[1].url, /\/cancel\/0123456789abcdef\/1$/);
  assert.equal(shown.length, 0); assert.equal(delivery.active.size, 0);
  delivery.close();
});

test("duplicate consumers share one request and cancel independently", async () => {
  const { delivery, requests, shown } = setup();
  delivery.setTransport("http");
  const a = job(1), b = job(1);
  const cancel = delivery.request(a); delivery.request(b); await tick(); cancel();
  assert.equal(requests.length, 1); assert.equal(requests[0].options.signal.aborted, false);
  requests[0].resolve(); await tick(); assert.equal(shown.length, 1); assert.equal(shown[0][0], b);
  delivery.request(job(1)); await tick(); assert.equal(requests.length, 1); assert.equal(shown[1][2].cached, "local");
  delivery.close();
});

test("cache keys separate renderer versions", async () => {
  const cache = new TileCache();
  cache.put("old-version/20/1/5", { blob: png, info: {} });
  const { delivery, requests } = setup({ cache });
  delivery.setTransport("http"); delivery.request(job(1)); await tick();
  assert.equal(requests.length, 1); delivery.close();
});

test("WebSocket cancellation retains its slot until acknowledgement", async () => {
  const { delivery, sockets, requests, shown } = setup();
  delivery.setTransport("ws"); const socket = sockets[0]; socket.onopen();
  const cancel = delivery.request(job(1)); delivery.request(job(2)); await tick();
  assert.equal(socket.sent.length, 1); assert.match(socket.url, /\/ws\/test-version\/0123456789abcdef$/);
  cancel(); await tick(); assert.equal(socket.sent.length, 1); assert.match(requests[0].url, /\/cancel\//);
  socket.onmessage({ data: '{"cancelled":1}' }); await tick(); assert.equal(socket.sent.length, 2);
  socket.onmessage({ data: response(2) }); await tick();
  assert.equal(shown.length, 1); assert.equal(shown[0][0].x, 2); assert.equal(shown[0][2].stages.compute, 8);
  delivery.close();
});

test("transport switches suppress old HTTP replies and retry live consumers", async () => {
  const { delivery, requests, sockets, shown } = setup();
  delivery.setTransport("http"); delivery.request(job(1)); await tick();
  delivery.setTransport("ws"); sockets[0].onopen(); await tick();
  requests[0].resolve(); await tick(); assert.equal(shown.length, 0);
  const id = Number(sockets[0].sent[0].split(" ")[0]);
  sockets[0].onmessage({ data: response(id) }); await tick(); assert.equal(shown.length, 1);
  delivery.close();
});

test("malformed WebSocket frames fall back to bounded HTTP delivery", async () => {
  const { delivery, sockets, requests } = setup();
  delivery.setTransport("ws"); sockets[0].onopen(); delivery.request(job(1)); await tick();
  sockets[0].onmessage({ data: new ArrayBuffer(2) }); await tick();
  assert.equal(delivery.transport, "http"); assert.equal(requests.filter((r) => !r.options.method).length, 1);
  delivery.close();
});

test("request ids wrap without emitting zero", async () => {
  const { delivery, sockets } = setup();
  delivery.setTransport("ws"); sockets[0].onopen(); delivery.nextId = 0xffffffff;
  delivery.request(job(1)); await tick(); assert.match(sockets[0].sent[0], /^1 /); delivery.close();
});

test("statistics use nearest-rank percentiles and named server timings", () => {
  assert.equal(percentile([], 0.95), 0);
  assert.equal(percentile([100, 1, 4, 2, 3], 0.5), 3);
  assert.equal(percentile([100, 1, 4, 2, 3], 0.95), 100);
  assert.deepEqual(serverTiming("render;dur=12.5, png;dur=2, wrong;dur=NaN"), { render: 12.5, png: 2 });
});


test("browser cache responses do not count historical render timings as fresh work", async () => {
  const { delivery, requests, shown } = setup({ resourceTiming: () => ({ transferSize: 0, decodedBodySize: 8 }) });
  delivery.setTransport("http"); delivery.request(job(1)); await tick();
  requests[0].resolve(); await tick();
  assert.equal(shown[0][2].cached, "browser");
  delivery.close();
});

test("an obsolete renderer URL refreshes configuration and reports the new version", async () => {
  const versions = [], urls = [];
  const { delivery, errors } = setup({ onVersionChange: (version) => versions.push(version), fetch: async (url) => {
    urls.push(url);
    if (url.endsWith("/api/config")) return { json: async () => ({ render_version: "new-version" }) };
    return { ok: false, status: 404 };
  } });
  delivery.setTransport("http"); delivery.request(job(1)); await tick(); await tick();
  assert.deepEqual(versions, ["new-version"]); assert.match(urls[1], /api\/config$/);
  assert.equal(errors.length, 1); delivery.close();
});


test("a consumer that joins an active request has no negative queue time", async () => {
  let now = 5;
  const { delivery, requests, shown } = setup({ now: () => now });
  delivery.setTransport("http"); delivery.request(job(1)); await tick();
  now = 10; delivery.request(job(1)); requests[0].resolve(); await tick();
  assert.equal(requests.length, 1); assert.equal(shown.length, 2);
  assert.equal(shown[1][2].queueMs, 0); delivery.close();
});
