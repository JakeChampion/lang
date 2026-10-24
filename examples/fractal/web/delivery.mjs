// Tile transport, cancellation, and a bounded cache shared by both transports.
export function percentile(values, fraction) {
  if (!values.length) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.max(0, Math.ceil(sorted.length * fraction) - 1)];
}

export function serverTiming(header) {
  const stages = {};
  for (const field of header.split(",")) {
    const name = field.trim().split(";")[0];
    const duration = /;\s*dur=([\d.]+)/.exec(field);
    if (duration && Number.isFinite(Number(duration[1]))) stages[name] = Number(duration[1]);
  }
  return stages;
}

export class TileCache {
  constructor(maxBytes = 16 * 1024 * 1024, maxEntries = 256) {
    this.maxBytes = maxBytes;
    this.maxEntries = maxEntries;
    this.bytes = 0;
    this.entries = new Map();
  }
  get(key) {
    const value = this.entries.get(key);
    if (value) { this.entries.delete(key); this.entries.set(key, value); }
    return value;
  }
  put(key, value) {
    const previous = this.entries.get(key);
    if (previous) { this.bytes -= previous.blob.size; this.entries.delete(key); }
    if (value.blob.size > this.maxBytes) return;
    this.entries.set(key, value);
    this.bytes += value.blob.size;
    while (this.bytes > this.maxBytes || this.entries.size > this.maxEntries) {
      const oldest = this.entries.keys().next().value;
      this.bytes -= this.entries.get(oldest).blob.size;
      this.entries.delete(oldest);
    }
  }
}

export class TileDelivery {
  constructor({ version, origin, session = globalThis.crypto.randomUUID(), fetch: fetcher = globalThis.fetch.bind(globalThis),
    socket = (url) => new WebSocket(url), now = () => performance.now(), priority = () => 0,
    resourceTiming = (url, since) => globalThis.performance?.getEntriesByName(url).findLast((entry) => entry.startTime >= since),
    onTile, onError, onVersionChange = () => {}, onHello = () => {}, onState = () => {}, onTransport = () => {}, cache = new TileCache(), concurrency = 2 }) {
    if (!/^[a-zA-Z0-9_-]+$/.test(version)) throw new Error("Invalid renderer version");
    Object.assign(this, { version, origin, session, fetcher, socketFactory: socket, now, priority, onTile, onError, onVersionChange, onHello, onState, onTransport, resourceTiming, cache, concurrency });
    this.transport = null;
    this.tasks = new Map();
    this.queue = new Set();
    this.active = new Map();
    this.nextId = 0;
    this.generation = 0;
    this.connection = null;
    this.ready = false;
    this.pumpQueued = false;
  }
  request(job) {
    job.requestedAt = this.now();
    job.cancelled = false;
    const key = `${this.version}/${job.z}/${job.x}/${job.y}`;
    const cached = this.cache.get(key);
    if (cached) {
      queueMicrotask(() => {
        if (!job.cancelled) this.onTile(job, cached.blob, { ...cached.info, cached: "local", ms: 0, queueMs: 0, transferMs: 0 });
      });
      return () => { job.cancelled = true; };
    }
    let task = this.tasks.get(key);
    if (!task) {
      task = { key, z: job.z, x: job.x, y: job.y, jobs: new Set(), run: 0, retries: 0, phase: "queued" };
      this.tasks.set(key, task);
      this.queue.add(task);
    }
    task.jobs.add(job);
    this.kick();
    return () => {
      job.cancelled = true;
      task.jobs.delete(job);
      if (task.jobs.size) return;
      this.queue.delete(task);
      this.tasks.delete(key);
      if (task.phase === "active") {
        task.controller?.abort();
        this.cancelRemote(task);
        // A WebSocket slot stays occupied until its response or cancel acknowledgement.
        // Removing it here would allow new work to bypass the concurrency bound.
      }
    };
  }
  cancelRemote(task) {
    if (!task.id) return;
    Promise.resolve(this.fetcher(`${this.origin}/cancel/${this.session}/${task.id}`, {
      method: "POST", cache: "no-store", keepalive: true,
    })).catch(() => {});
  }
  setTransport(transport) {
    if (this.transport === transport) return;
    if (transport !== "http" && transport !== "ws") throw new Error("Invalid transport");
    this.generation++;
    clearTimeout(this.reconnectTimer);
    clearTimeout(this.fallbackTimer);
    if (this.connection) {
      this.connection.onclose = this.connection.onmessage = this.connection.onopen = null;
      this.connection.close();
      this.connection = null;
    }
    this.ready = false;
    for (const task of this.active.values()) {
      task.run++;
      task.controller?.abort();
      this.cancelRemote(task);
      task.phase = "queued";
      if (task.jobs.size) this.queue.add(task);
    }
    this.active.clear();
    this.transport = transport;
    this.onTransport(transport);
    if (transport === "ws") this.connect();
    this.kick();
  }
  connect() {
    const generation = this.generation;
    const url = new URL(`/ws/${this.version}/${this.session}`, this.origin);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    const connection = this.socketFactory(url.href);
    this.connection = connection;
    connection.binaryType = "arraybuffer";
    let opened = false;
    this.fallbackTimer = setTimeout(() => {
      if (generation === this.generation && !this.ready) this.setTransport("http");
    }, 5000);
    connection.onopen = () => {
      if (generation !== this.generation || connection !== this.connection) return;
      opened = true;
      this.ready = true;
      clearTimeout(this.fallbackTimer);
      this.kick();
    };
    connection.onmessage = ({ data }) => {
      if (generation !== this.generation || connection !== this.connection) return;
      try {
        if (typeof data === "string") {
          const message = JSON.parse(data);
          if (Number.isInteger(message.cancelled)) this.complete(this.active.get(message.cancelled));
          else {
            if (message.render_version !== this.version) throw new Error("Renderer version changed");
            this.onHello(message);
          }
        } else this.receive(data);
      } catch (error) {
        this.onState(error.message, false);
        this.setTransport("http");
      }
    };
    connection.onclose = () => {
      if (generation !== this.generation || connection !== this.connection) return;
      this.ready = false;
      clearTimeout(this.fallbackTimer);
      this.onState("Socket closed", false);
      let retry = opened;
      for (const task of this.active.values()) {
        task.phase = "queued";
        if (task.jobs.size) { this.queue.add(task); retry = retry && ++task.retries <= 2; }
      }
      this.active.clear();
      if (!retry) this.setTransport("http");
      else this.reconnectTimer = setTimeout(() => this.connect(), 500);
    };
  }
  kick() {
    if (this.pumpQueued) return;
    this.pumpQueued = true;
    queueMicrotask(() => { this.pumpQueued = false; this.pump(); });
  }
  pump() {
    if (!this.transport || (this.transport === "ws" && !this.ready)) return;
    // One WebSocket request lets a cancel reach the renderer before another starts.
    const limit = this.transport === "ws" ? 1 : this.concurrency;
    while (this.active.size < limit && this.queue.size) {
      const task = [...this.queue].sort((a, b) => this.priority(a) - this.priority(b))[0];
      this.queue.delete(task);
      if (!task.jobs.size) continue;
      do { this.nextId = (this.nextId + 1) >>> 0; } while (!this.nextId || this.active.has(this.nextId));
      task.id = this.nextId;
      task.phase = "active";
      task.sentAt = this.now();
      task.run++;
      this.active.set(task.id, task);
      if (this.transport === "ws") this.connection.send(`${task.id} ${task.z} ${task.x} ${task.y}`);
      else this.fetchHttp(task, task.run);
    }
  }
  async fetchHttp(task, run) {
    task.controller = new AbortController();
    try {
      const url = `${this.origin}/tile/${task.key}`;
      const response = await this.fetcher(url, {
        signal: task.controller.signal,
        headers: { "X-Fern-Session": this.session, "X-Fern-Request": String(task.id) },
      });
      if (response.status === 404) {
        this.refreshing ??= this.fetcher(`${this.origin}/api/config`, { cache: "no-store" })
          .then((config) => config.json()).then((config) => {
            if (config.render_version !== this.version) this.onVersionChange(config.render_version);
          }).catch(() => {}).finally(() => { this.refreshing = null; });
        await this.refreshing;
      }
      if (!response.ok) throw new Error(`Tile request failed (${response.status})`);
      const stages = serverTiming(response.headers.get("server-timing") || "");
      const status = response.headers.get("cache-status") || "";
      const cached = /;\s*hit\b/i.test(status) || Number(response.headers.get("age")) > 0;
      const blob = await response.blob();
      const timing = this.resourceTiming(url, task.sentAt);
      const browserHit = timing?.transferSize === 0 && timing.decodedBodySize > 0;
      if (task.run !== run) return;
      this.deliver(task, blob, { stages, ms: stages.render || 0,
        iterations: Number(response.headers.get("x-fern-iterations") || 0), cached: browserHit ? "browser" : cached ? "edge" : false });
    } catch (error) {
      if (task.run === run && error.name !== "AbortError") this.fail(task, error);
    } finally {
      if (task.run === run) this.complete(task);
    }
  }
  receive(data) {
    if (!(data instanceof ArrayBuffer) || data.byteLength < 32) throw new Error("Invalid tile response");
    const view = new DataView(data);
    const task = this.active.get(view.getUint32(0));
    if (!task) return;
    if (data.byteLength < 40 || view.getUint32(32) !== 0x89504e47 || view.getUint32(36) !== 0x0d0a1a0a) throw new Error("Invalid tile PNG");
    const ms = view.getUint32(4) / 1000;
    const stages = { render: ms, compute: view.getUint32(20) / 1000, color: view.getUint32(24) / 1000, png: view.getUint32(28) / 1000 };
    this.deliver(task, new Blob([data.slice(32)], { type: "image/png" }), {
      ms, stages, iterations: view.getUint32(8) * 4294967296 + view.getUint32(12), containerTiles: view.getUint32(16), cached: false,
    });
    this.complete(task);
  }
  deliver(task, blob, info) {
    if (!task.jobs.size) return;
    const receivedAt = this.now();
    const measured = { ...info, transferMs: Math.max(0, receivedAt - task.sentAt - (info.cached ? 0 : info.ms)) };
    this.cache.put(task.key, { blob, info: measured });
    for (const job of task.jobs) {
      if (!job.cancelled) this.onTile(job, blob, { ...measured, queueMs: task.sentAt - job.requestedAt });
    }
  }
  fail(task, error) {
    for (const job of task.jobs) if (!job.cancelled) this.onError(job, error);
  }
  complete(task) {
    if (!task) return;
    if (this.active.get(task.id) === task) this.active.delete(task.id);
    if (this.tasks.get(task.key) === task) this.tasks.delete(task.key);
    task.phase = "done";
    this.kick();
  }
  close() {
    this.generation++;
    clearTimeout(this.reconnectTimer);
    clearTimeout(this.fallbackTimer);
    if (this.connection) {
      this.connection.onclose = this.connection.onmessage = this.connection.onopen = null;
      this.connection.close();
    }
    for (const task of this.tasks.values()) {
      for (const job of task.jobs) job.cancelled = true;
      task.jobs.clear();
      task.run++;
      task.controller?.abort();
      if (task.phase === "active") this.cancelRemote(task);
    }
    this.tasks.clear(); this.queue.clear(); this.active.clear();
    this.transport = null;
  }
}
