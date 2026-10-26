// The browser half of the torrent page: a WebTorrent tracker client and the
// BitTorrent wire protocol over WebRTC data channels. The Fern kernel builds
// and reads every protocol message; this file frames the byte stream and
// drives the WebRTC connections.
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const BLOCK = 16384;
const PIPELINE = 16;
const ICE = [{ urls: "stun:stun.l.google.com:19302" }];

export const toHex = (bytes) =>
  Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
export const fromHex = (text) =>
  Uint8Array.from(text.match(/../g) ?? [], (pair) => parseInt(pair, 16));
// The tracker protocol writes 20 bytes as a string of 20 characters.
const toBinary = (bytes) => String.fromCharCode(...bytes);
const fromBinary = (text) => Uint8Array.from(text, (c) => c.charCodeAt(0));
const randomBytes = (n) => crypto.getRandomValues(new Uint8Array(n));

function u32(bytes, at) {
  return ((bytes[at] << 24) | (bytes[at + 1] << 16) | (bytes[at + 2] << 8) | bytes[at + 3]) >>> 0;
}
function message(id, ...words) {
  const out = new Uint8Array(5 + words.length * 4);
  new DataView(out.buffer).setUint32(0, 1 + words.length * 4);
  out[4] = id;
  words.forEach((w, i) => new DataView(out.buffer).setUint32(5 + i * 4, w));
  return out;
}
function frame(id, payload) {
  const out = new Uint8Array(5 + payload.length);
  new DataView(out.buffer).setUint32(0, 1 + payload.length);
  out[4] = id;
  out.set(payload, 5);
  return out;
}
function concat(parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

// One file shared in one swarm. A seeder starts with the data and its info
// dictionary. A downloader starts with the info hash only.
export class Swarm extends EventTarget {
  constructor({ kernel, infoHash, info = null, data = null, tracker }) {
    super();
    this.kernel = kernel;
    this.infoHash = infoHash;
    this.peerId = concat([encoder.encode("-FE0001-"), randomBytes(12)]);
    this.tracker = tracker;
    this.wires = new Set();
    this.pending = new Map();
    this.uploaded = 0;
    this.downloaded = 0;
    this.metadata = null;
    if (info) this.setInfo(info, data);
  }

  async call(name, ...args) {
    return (await this.kernel()).call(name, ...args);
  }
  async bytes(name, ...args) {
    const k = await this.kernel();
    return k.list(k.call(name, ...args));
  }
  async text(name, ...args) {
    return decoder.decode(await this.bytes(name, ...args));
  }

  async setInfo(info, data) {
    const meta = JSON.parse(await this.text("describe", info));
    this.info = info;
    this.meta = meta;
    this.have = new Uint8Array(meta.pieces);
    this.data = data ?? new Uint8Array(meta.length);
    this.blocks = new Map();
    this.requested = new Set();
    if (data) this.have.fill(1);
    this.dispatchEvent(new Event("info"));
    for (const wire of this.wires) wire.ready();
  }

  get complete() {
    return !!this.have && this.have.every((x) => x);
  }
  pieceSize(index) {
    const start = index * this.meta.piece_length;
    return Math.min(this.meta.piece_length, this.meta.length - start);
  }
  piece(index) {
    const start = index * this.meta.piece_length;
    return this.data.subarray(start, start + this.pieceSize(index));
  }
  bitfield() {
    const out = new Uint8Array(Math.ceil(this.have.length / 8));
    this.have.forEach((h, i) => {
      if (h) out[i >> 3] |= 0x80 >> (i & 7);
    });
    return out;
  }

  // Tracker client: announce, offer, and answer through one WebSocket.
  start() {
    this.socket = new WebSocket(this.tracker);
    this.socket.onopen = () => this.announce("started");
    this.socket.onmessage = (event) => this.onTracker(JSON.parse(event.data));
    this.socket.onclose = () => this.dispatchEvent(new Event("tracker-closed"));
    this.timer = setInterval(() => this.announce(), 30000);
  }
  stop() {
    clearInterval(this.timer);
    this.socket?.close();
    for (const wire of this.wires) wire.close();
  }

  async announce(event) {
    if (this.socket?.readyState !== WebSocket.OPEN) return;
    const offers = [];
    const want = Math.max(0, 5 - this.wires.size - this.pending.size);
    for (let i = 0; i < want; i++) {
      const id = toBinary(randomBytes(20));
      const pc = new RTCPeerConnection({ iceServers: ICE });
      const channel = pc.createDataChannel("fern");
      this.pending.set(id, { pc, channel });
      setTimeout(() => {
        if (this.pending.get(id)?.pc === pc) {
          this.pending.delete(id);
          pc.close();
        }
      }, 50000);
      offers.push(this.describe(pc, await pc.createOffer()).then((offer) => ({ offer, offer_id: id })));
    }
    const message = {
      action: "announce",
      info_hash: toBinary(this.infoHash),
      peer_id: toBinary(this.peerId),
      numwant: offers.length,
      uploaded: this.uploaded,
      downloaded: this.downloaded,
      left: this.meta ? this.meta.length - this.downloaded : null,
      offers: await Promise.all(offers),
    };
    if (event) message.event = event;
    this.socket.send(JSON.stringify(message));
  }

  // Waits for every ICE candidate, as WebTorrent does without trickle.
  async describe(pc, description) {
    await pc.setLocalDescription(description);
    if (pc.iceGatheringState !== "complete")
      await new Promise((resolve) => {
        const done = () => pc.iceGatheringState === "complete" && resolve();
        pc.addEventListener("icegatheringstatechange", done);
        setTimeout(resolve, 5000);
      });
    return { type: pc.localDescription.type, sdp: pc.localDescription.sdp };
  }

  async onTracker(msg) {
    if (msg["failure reason"]) {
      this.dispatchEvent(new CustomEvent("failure", { detail: msg["failure reason"] }));
      return;
    }
    if (msg.action !== "announce") return;
    if (typeof msg.complete === "number") {
      this.swarmSize = msg.complete + msg.incomplete;
      this.dispatchEvent(new Event("progress"));
    }
    if (msg.offer && msg.peer_id) {
      const pc = new RTCPeerConnection({ iceServers: ICE });
      pc.ondatachannel = (event) => this.attach(pc, event.channel);
      await pc.setRemoteDescription(msg.offer);
      const answer = await this.describe(pc, await pc.createAnswer());
      this.socket.send(
        JSON.stringify({
          action: "announce",
          info_hash: toBinary(this.infoHash),
          peer_id: toBinary(this.peerId),
          to_peer_id: msg.peer_id,
          answer,
          offer_id: msg.offer_id,
        }),
      );
    }
    if (msg.answer && this.pending.has(msg.offer_id)) {
      const { pc, channel } = this.pending.get(msg.offer_id);
      this.pending.delete(msg.offer_id);
      await pc.setRemoteDescription(msg.answer);
      this.attach(pc, channel);
    }
  }

  attach(pc, channel) {
    channel.binaryType = "arraybuffer";
    const wire = new Wire(this, pc, channel);
    if (channel.readyState === "open") wire.open();
    else channel.onopen = () => wire.open();
  }

  // Requests blocks of missing pieces from each wire that has them.
  schedule() {
    if (!this.meta) return;
    for (const wire of this.wires) {
      if (wire.choked || !wire.bitfield) continue;
      while (wire.inflight < PIPELINE) {
        const next = this.nextBlock(wire);
        if (!next) break;
        wire.request(...next);
      }
    }
  }
  nextBlock(wire) {
    for (let i = 0; i < this.have.length; i++) {
      if (this.have[i] || !wire.bitfield[i]) continue;
      for (let begin = 0; begin < this.pieceSize(i); begin += BLOCK) {
        const key = `${i}:${begin}`;
        if (this.requested.has(key)) continue;
        this.requested.add(key);
        return [i, begin, Math.min(BLOCK, this.pieceSize(i) - begin)];
      }
    }
    return null;
  }

  async received(index, begin, block) {
    if (!this.meta || this.have[index]) return;
    this.data.set(block, index * this.meta.piece_length + begin);
    this.downloaded += block.length;
    const got = (this.blocks.get(index) ?? 0) + block.length;
    this.blocks.set(index, got);
    if (got < this.pieceSize(index)) return;
    this.blocks.delete(index);
    if ((await this.call("piece-ok", this.info, index, this.piece(index))) === 1) {
      this.have[index] = 1;
      for (const wire of this.wires) wire.send(message(4, index));
    } else {
      this.failed = (this.failed ?? 0) + 1;
      for (let b = 0; b < this.pieceSize(index); b += BLOCK) this.requested.delete(`${index}:${b}`);
    }
    this.dispatchEvent(new Event("progress"));
    if (this.complete) {
      this.dispatchEvent(new Event("done"));
      this.announce("completed");
    }
  }

  // Joins the metadata pieces that a peer sends and checks them against the
  // info hash before it trusts them.
  async metadataPiece(size, piece, bytes) {
    if (this.info) return;
    if (!this.metadata || this.metadata.length !== size) this.metadata = new Uint8Array(size);
    this.metadata.set(bytes, piece * BLOCK);
    this.metadataGot = (this.metadataGot ?? new Set()).add(piece);
    if (this.metadataGot.size < Math.ceil(size / BLOCK)) return;
    const hash = await this.bytes("infohash", this.metadata);
    if (toHex(hash) !== toHex(this.infoHash)) {
      this.metadata = null;
      this.metadataGot = null;
      return;
    }
    await this.setInfo(this.metadata);
  }
}

// One peer connection: a byte stream of length-prefixed messages after a
// 68-byte handshake.
class Wire {
  constructor(swarm, pc, channel) {
    this.swarm = swarm;
    this.pc = pc;
    this.channel = channel;
    this.buffer = new Uint8Array(0);
    this.shaken = false;
    this.choked = true;
    this.inflight = 0;
    this.bitfield = null;
    this.metadataId = 0;
    this.outstanding = new Set();
    // Chunks are handled in order: a handler awaits the kernel.
    this.queue = Promise.resolve();
    channel.onmessage = (event) => {
      const chunk = new Uint8Array(event.data);
      this.queue = this.queue.then(() => this.data(chunk));
    };
    channel.onclose = () => this.close();
  }

  send(bytes) {
    if (this.channel.readyState === "open") this.channel.send(bytes);
  }
  close() {
    if (!this.swarm.wires.delete(this)) return;
    for (const key of this.outstanding ?? []) this.swarm.requested.delete(key);
    this.pc.close();
    this.swarm.dispatchEvent(new Event("progress"));
    this.swarm.schedule();
  }

  async open() {
    this.swarm.wires.add(this);
    this.send(await this.swarm.bytes("handshake", this.swarm.infoHash, this.swarm.peerId));
    this.send(await this.swarm.bytes("ext-handshake", this.swarm.info?.length ?? 0));
    this.ready();
    this.swarm.dispatchEvent(new Event("progress"));
  }

  // Sends what a peer needs once the info dictionary is known.
  ready() {
    if (!this.swarm.meta || this.announced) return;
    this.announced = true;
    if (this.swarm.have.some((h) => h)) this.send(frame(5, this.swarm.bitfield()));
    this.send(message(1));
    this.send(message(2));
    if (!this.bitfield) this.bitfield = new Uint8Array(this.swarm.meta.pieces);
    this.swarm.schedule();
  }

  async data(chunk) {
    this.buffer = concat([this.buffer, chunk]);
    if (!this.shaken) {
      if (this.buffer.length < 68) return;
      if (toHex(this.buffer.subarray(28, 48)) !== toHex(this.swarm.infoHash)) return this.close();
      this.shaken = true;
      this.buffer = this.buffer.slice(68);
    }
    while (this.buffer.length >= 4) {
      const length = u32(this.buffer, 0);
      if (length > 1 << 20) return this.close();
      if (this.buffer.length < 4 + length) return;
      const body = this.buffer.slice(4, 4 + length);
      this.buffer = this.buffer.slice(4 + length);
      if (length) await this.handle(body[0], body.subarray(1));
    }
  }

  async handle(id, payload) {
    const swarm = this.swarm;
    if (id === 0) this.choked = true;
    else if (id === 1) this.choked = false;
    else if (id === 4) {
      if (this.bitfield) this.bitfield[u32(payload, 0)] = 1;
    } else if (id === 5) {
      const count = swarm.meta?.pieces ?? payload.length * 8;
      this.bitfield = Uint8Array.from({ length: count }, (_, i) => (payload[i >> 3] >> (7 - (i & 7))) & 1);
    } else if (id === 6 && swarm.meta) {
      const [index, begin, length] = [u32(payload, 0), u32(payload, 4), u32(payload, 8)];
      if (!swarm.have[index] || length > BLOCK) return;
      const block = swarm.piece(index).subarray(begin, begin + length);
      this.send(frame(7, concat([message(7, index, begin).subarray(5), block])));
      swarm.uploaded += block.length;
      swarm.dispatchEvent(new Event("progress"));
    } else if (id === 7) {
      const [index, begin] = [u32(payload, 0), u32(payload, 4)];
      this.inflight = Math.max(0, this.inflight - 1);
      this.outstanding.delete(`${index}:${begin}`);
      await swarm.received(index, begin, payload.subarray(8));
    } else if (id === 20) await this.extended(payload[0], payload.subarray(1));
    swarm.schedule();
  }

  request(index, begin, length) {
    this.inflight++;
    this.outstanding.add(`${index}:${begin}`);
    this.send(message(6, index, begin, length));
  }

  async extended(extId, body) {
    const swarm = this.swarm;
    const text = await swarm.text("ext-fields", body);
    if (!text) return;
    const f = JSON.parse(text);
    if (extId === 0) {
      this.metadataId = f.ut_metadata ?? 0;
      if (!swarm.info && this.metadataId && f.metadata_size > 0 && f.metadata_size < 1 << 20)
        for (let p = 0; p < Math.ceil(f.metadata_size / BLOCK); p++)
          this.send(await swarm.bytes("metadata-request", this.metadataId, p));
      return;
    }
    if (f.msg_type === 0 && this.metadataId && swarm.info)
      this.send(await swarm.bytes("metadata-piece", this.metadataId, swarm.info, f.piece));
    else if (f.msg_type === 1 && f.total_size)
      await swarm.metadataPiece(f.total_size, f.piece, body.subarray(f.data));
  }
}

export function magnet(infoHash, name, tracker) {
  return `magnet:?xt=urn:btih:${toHex(infoHash)}&dn=${encodeURIComponent(name)}&tr=${encodeURIComponent(tracker)}`;
}
