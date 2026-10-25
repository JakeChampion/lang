import { room, notice, error } from "./shell.mjs";
export class Session {
  constructor(app, changed) {
    this.app = app;
    this.changed = changed;
    this.revision = -1;
    this.cells = null;
    this.seq = 0;
    this.pending = new Map();
    this.draft = new Map();
    this.sent = new Map();
    this.latencies = [];
    this.serverUs = 0;
    this.closed = false;
    this.delay = 250;
    this.clockOffset = 0;
    this.lastSync = 0;
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
    this.factoryState = null;
    ws.onmessage = (event) => {
      try {
        if (event.data instanceof ArrayBuffer) {
          this.factory(event.data);
          return;
        }
        const data = JSON.parse(event.data);
        if (data.kind === "snapshot") {
          this.cells = Int32Array.from(data.cells);
          this.persistent = !!data.persistent;
          this.program = Int32Array.from(data.program || []);
          this.clientID = data.client_id;
          this.epoch = data.epoch_ms;
          this.clockOffset = data.server_ms - Date.now();
          const save = document.getElementById("checkpoint");
          if (save) save.disabled = !this.persistent;
          this.revision = data.revision;
          this.delay = 250;
          notice(
            `Connected · ${room()} · revision ${this.revision} · ${this.persistent ? "checkpoint storage" : "temporary memory"}`,
          );
          this.changed(this.cells, [], true);
          this.onProgram?.(this.program);
        } else if (
          data.kind === "change" ||
          data.kind === "note" ||
          data.kind === "program"
        ) {
          if (data.revision !== this.revision + 1) {
            ws.close();
            return;
          }
          this.revision = data.revision;
          if (data.kind === "note") this.onNote?.(data);
          else if (data.kind === "program") {
            for (let i = 0; i < data.points.length; i++)
              this.program[data.points[i]] = data.values[i];
            this.onProgram?.(this.program);
          } else {
            for (let i = 0; i < data.points.length; i++) {
              const at = data.points[i],
                value = data.values[i];
              this.cells[at] = value;
              if (data.sender === this.clientID && this.draft.get(at) === value)
                this.draft.delete(at);
            }
            this.changed(this.cells, data.points, false);
          }
        } else if (data.kind === "ack") {
          const sent = this.sent.get(data.seq);
          if (sent) {
            this.latencies.push(performance.now() - sent.at);
            if (this.latencies.length > 256) this.latencies.shift();
            if (Number.isFinite(data.server_ms) && sent.wall)
              this.clockOffset = data.server_ms - (Date.now() + sent.wall) / 2;
            if (sent.entries)
              for (const [at, value] of sent.entries)
                if (this.cells[at] === value && this.draft.get(at) === value)
                  this.draft.delete(at);
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
      for (const [at, value] of this.draft)
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
      buffer.byteLength < 96 ||
      view.getUint8(0) !== 70 ||
      view.getUint8(1) !== 70 ||
      view.getUint8(2) !== 2
    )
      throw Error("Factory protocol changed. Reload this page.");
    const count = view.getUint32(76, true),
      items = view.getUint32(84, true),
      delta = view.getUint8(3) === 1,
      sequence = Number(view.getBigUint64(56, true)),
      base = Number(view.getBigUint64(64, true));
    if (
      count !== 65536 ||
      items > count ||
      buffer.byteLength !== 96 + items * (delta ? 5 : 3) ||
      (!delta && items !== count)
    )
      throw Error("Invalid factory snapshot dimensions.");
    if (delta && (!this.factoryState || this.factoryState.sequence !== base))
      throw Error("Factory snapshot gap. Reconnect for the current state.");
    const values = delta ? this.factoryState.values : new Int32Array(count),
      powered = delta ? this.factoryState.powered : new Uint8Array(count),
      changed = [];
    for (let i = 0; i < items; i++) {
      const offset = 96 + i * (delta ? 5 : 3),
        index = delta ? view.getUint16(offset, true) : i,
        at = offset + (delta ? 2 : 0);
      values[index] = view.getUint16(at, true);
      powered[index] = view.getUint8(at + 2);
      changed.push(index);
    }
    this.factoryState = { sequence, values, powered };
    this.onFactory?.({
      kind: "frame",
      native: true,
      tick: Number(view.getBigUint64(8, true)),
      produced: Number(view.getBigUint64(16, true)),
      consumed: Number(view.getBigUint64(24, true)),
      removed: Number(view.getBigUint64(32, true)),
      refined: Number(view.getBigUint64(40, true)),
      stepMs: view.getUint32(72, true) / 1000,
      revision: Number(view.getBigUint64(48, true)),
      width: view.getUint32(80, true),
      count,
      running: !!view.getUint32(4, true),
      values,
      powered,
      packetBytes: buffer.byteLength,
      delta,
      changed,
      sequence,
    });
  }
  edit(points, values) {
    if (!this.cells) throw Error("Wait for the project connection.");
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
    const unique = points.filter((at) => !this.draft.has(at)).length;
    if (this.pending.size + extra > 4096 || this.draft.size + unique > 4096)
      throw Error("Wait for the connection before more edits.");
    for (let i = 0; i < points.length; i++) {
      this.pending.set(points[i], values[i]);
      this.draft.set(points[i], values[i]);
    }
    this.onDraft?.();
  }
  currentCells() {
    if (!this.cells) throw Error("Wait for the project connection.");
    const cells = this.cells.slice();
    for (const [at, value] of this.draft) cells[at] = value;
    return cells;
  }
  command(kind, points = [], values = []) {
    if (this.socket?.readyState !== WebSocket.OPEN)
      throw Error("Connect to the server first.");
    const seq = ++this.seq;
    this.socket.send(JSON.stringify({ kind, seq, points, values }));
    this.sent.set(seq, { at: performance.now(), wall: Date.now() });
  }
  flush() {
    if (
      this.socket?.readyState === WebSocket.OPEN &&
      this.cells &&
      Date.now() - this.lastSync > 5000
    ) {
      this.lastSync = Date.now();
      this.command("sync");
      return;
    }
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
    this.sent.set(seq, { at: performance.now(), wall: Date.now(), entries });
    for (const [at] of entries) this.pending.delete(at);
  }
  dispose() {
    this.closed = true;
    clearInterval(this.timer);
    clearTimeout(this.reconnect);
    this.socket?.close();
  }
}
