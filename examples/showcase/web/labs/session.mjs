import { room, notice, error } from "./shell.mjs";
export class Session {
  constructor(app, changed) {
    this.app = app;
    this.changed = changed;
    this.revision = -1;
    this.cells = null;
    this.seq = 0;
    this.pending = new Map();
    this.sent = new Map();
    this.latencies = [];
    this.serverUs = 0;
    this.closed = false;
    this.delay = 250;
    this.connect();
    this.timer = setInterval(() => this.flush(), 20);
    addEventListener("pagehide", () => this.dispose(), { once: true });
  }
  connect() {
    const url = new URL(`/${this.app}/ws`, location.href);
    url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
    url.searchParams.set("room", room());
    const ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    this.socket = ws;
    ws.onmessage = (event) => {
      try {
        if (event.data instanceof ArrayBuffer) {
          this.factory(event.data);
          return;
        }
        const data = JSON.parse(event.data);
        if (data.kind === "snapshot") {
          this.cells = Int32Array.from(data.cells);
          this.revision = data.revision;
          this.delay = 250;
          notice(`Connected · ${room()} · revision ${this.revision}`);
          this.changed(this.cells, [], true);
        } else if (data.kind === "change") {
          if (data.revision !== this.revision + 1) {
            ws.close();
            return;
          }
          for (let i = 0; i < data.points.length; i++)
            this.cells[data.points[i]] = data.values[i];
          this.revision = data.revision;
          this.changed(this.cells, data.points, false);
        } else if (data.kind === "ack") {
          const sent = this.sent.get(data.seq);
          if (sent) {
            this.latencies.push(performance.now() - sent.at);
            if (this.latencies.length > 256) this.latencies.shift();
            this.sent.delete(data.seq);
          }
          this.serverUs = data.server_us;
        }
      } catch (e) {
        error(e);
        ws.close();
      }
    };
    ws.onclose = (event) => {
      for (const sent of this.sent.values())
        if (sent.entries)
          for (const [at, value] of sent.entries)
            if (!this.pending.has(at)) this.pending.set(at, value);
      this.sent.clear();
      if (event.code === 1008) {
        this.closed = true;
        error(Error(event.reason || "The server rejected a project command."));
        return;
      }
      if (!this.closed) {
        notice("Connection lost. Reconnect pending.");
        this.reconnect = setTimeout(() => this.connect(), this.delay);
        this.delay = Math.min(10000, this.delay * 2);
      }
    };
    ws.onerror = () =>
      notice("Cannot connect yet. Your unsent edits stay in this tab.");
  }
  factory(buffer) {
    const view = new DataView(buffer);
    if (
      buffer.byteLength < 64 ||
      view.getUint8(0) !== 70 ||
      view.getUint8(1) !== 70 ||
      view.getUint8(2) !== 1
    )
      throw Error("Invalid factory snapshot.");
    const count = view.getUint32(48, true);
    if (count !== 65536 || buffer.byteLength !== 64 + count / 2)
      throw Error("Invalid factory dimensions.");
    const values = new Int32Array(count);
    for (let i = 0; i < count; i += 2) {
      const v = view.getUint8(64 + i / 2);
      values[i] = v & 15;
      values[i + 1] = v >>> 4;
    }
    this.onFactory?.({
      kind: "frame",
      native: true,
      tick: Number(view.getBigUint64(8, true)),
      produced: Number(view.getBigUint64(16, true)),
      consumed: Number(view.getBigUint64(24, true)),
      removed: Number(view.getBigUint64(32, true)),
      stepMs: view.getUint32(40, true) / 1000,
      revision: view.getUint32(44, true),
      width: view.getUint32(52, true),
      count,
      running: !!view.getUint32(4, true),
      values,
      packetBytes: buffer.byteLength,
    });
  }
  edit(points, values) {
    if (!this.cells) return;
    if (points.length !== values.length)
      throw Error("Mismatched project edits.");
    let extra = 0;
    for (let i = 0; i < points.length; i++) {
      const at = points[i],
        value = values[i];
      if (
        !Number.isInteger(at) ||
        at < 0 ||
        at >= this.cells.length ||
        !Number.isInteger(value) ||
        value < -2147483648 ||
        value > 2147483647
      )
        throw Error("Invalid project edit.");
      if (!this.pending.has(at)) extra++;
    }
    if (this.pending.size + extra > 4096)
      throw Error("Wait for the connection before more edits.");
    for (let i = 0; i < points.length; i++)
      this.pending.set(points[i], values[i]);
  }
  command(kind) {
    if (this.socket?.readyState !== WebSocket.OPEN)
      throw Error("Connect to the server first.");
    const seq = ++this.seq;
    this.socket.send(JSON.stringify({ kind, seq, points: [], values: [] }));
    this.sent.set(seq, { at: performance.now() });
  }
  flush() {
    if (
      this.socket?.readyState !== WebSocket.OPEN ||
      !this.cells ||
      !this.pending.size ||
      this.socket.bufferedAmount > 32768 ||
      this.sent.size >= 16
    )
      return;
    const entries = [...this.pending].slice(0, 128);
    const seq = ++this.seq;
    this.socket.send(
      JSON.stringify({
        kind: "edit",
        seq,
        points: entries.map((x) => x[0]),
        values: entries.map((x) => x[1]),
      }),
    );
    this.sent.set(seq, { at: performance.now(), entries });
    for (const [at] of entries) this.pending.delete(at);
  }
  dispose() {
    this.closed = true;
    clearInterval(this.timer);
    clearTimeout(this.reconnect);
    this.socket?.close();
  }
}
