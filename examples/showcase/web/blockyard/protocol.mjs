export const SIZE = 512,
  HEIGHT = 48,
  CHUNK = 16;
export const voxelKey = (x, y, z) => (y * SIZE + z) * SIZE + x;
export const chunkKey = (x, z) => z * 32 + x;
export const localKey = (x, y, z) => (y * 16 + (z & 15)) * 16 + (x & 15);
export const solid = (material) => material > 0 && material !== 11;
export function enteringChunks(keys, previous, next) {
  if (previous < 0 || previous === next) return [];
  const contains = (key, cell) =>
    Math.abs(((key % 32) >> 1) - (cell % 16)) <= 1 &&
    Math.abs(Math.floor(key / 64) - Math.floor(cell / 16)) <= 1;
  return [...keys].filter(
    (key) => contains(key, next) && !contains(key, previous),
  );
}
export function controls(seq, input) {
  const bytes = new ArrayBuffer(16),
    v = new DataView(bytes);
  v.setUint8(0, 1);
  v.setUint8(1, (input.jump ? 1 : 0) | (input.fly ? 2 : 0));
  v.setInt8(2, Math.round(Math.max(-1, Math.min(1, input.mx)) * 127));
  v.setInt8(3, Math.round(Math.max(-1, Math.min(1, input.mz)) * 127));
  v.setInt8(4, Math.round(Math.max(-1, Math.min(1, input.up)) * 127));
  v.setUint16(
    6,
    ((((input.yaw % (Math.PI * 2)) + Math.PI * 2) % (Math.PI * 2)) * 65536) /
      (Math.PI * 2),
    true,
  );
  v.setInt16(
    8,
    Math.round(
      Math.max(-15500, Math.min(15500, (input.pitch * 32768) / Math.PI)),
    ),
    true,
  );
  v.setUint32(10, seq, true);
  return bytes;
}
export function edit(nonce, action, material, point) {
  const bytes = new ArrayBuffer(16),
    v = new DataView(bytes);
  v.setUint8(0, 2);
  v.setUint8(1, action);
  v.setUint8(2, material);
  v.setUint16(4, point.x, true);
  v.setUint16(6, point.y, true);
  v.setUint16(8, point.z, true);
  v.setUint32(10, nonce, true);
  return bytes;
}
export function travel(seq, landmark) {
  const bytes = new ArrayBuffer(16),
    v = new DataView(bytes);
  v.setUint8(0, 3);
  v.setUint8(1, landmark);
  v.setUint32(10, seq, true);
  return bytes;
}
export function decode(bytes) {
  const v = new DataView(bytes);
  if (v.byteLength < 4 || v.getUint8(0) !== 66 || v.getUint8(2) !== 1)
    throw Error("Invalid Blockyard packet");
  if (v.getUint8(1) === 65) {
    if (v.byteLength !== 12) throw Error("Invalid input acknowledgement");
    return {
      kind: "ack",
      seq: v.getUint32(4, true),
      serverUs: v.getUint32(8, true),
    };
  }
  if (v.getUint8(1) !== 80 || v.byteLength < 16)
    throw Error("Invalid world snapshot");
  const count = v.getUint16(8, true),
    total = v.getUint16(10, true),
    players = [];
  if (
    count > 512 ||
    total > 512 ||
    count > total ||
    v.byteLength !== 16 + count * 16
  )
    throw Error("Invalid player count");
  for (let i = 0; i < count; i++) {
    const at = 16 + i * 16;
    const player = {
      id: v.getUint16(at, true),
      x: v.getUint16(at + 2, true) / 64,
      y: v.getUint16(at + 4, true) / 64,
      z: v.getUint16(at + 6, true) / 64,
      yaw: (v.getUint16(at + 8, true) * Math.PI * 2) / 65536,
      fly: !!(v.getInt16(at + 10, true) & 1),
      grounded: !!(v.getInt16(at + 10, true) & 2),
      vy: (v.getInt16(at + 10, true) >> 2) / 256,
      seq: v.getUint32(at + 12, true),
    };
    if (
      player.id < 1 ||
      player.id > 512 ||
      player.x >= 512 ||
      player.y >= 48 ||
      player.z >= 512
    )
      throw Error("Invalid player coordinate");
    players.push(player);
  }
  if (v.getUint8(12) > 15 || v.getUint8(13) > 15)
    throw Error("Invalid interest area");
  return {
    kind: "snapshot",
    tick: v.getUint32(4, true),
    sector: v.getUint8(13) * 16 + v.getUint8(12),
    total,
    players,
  };
}
export function collisionMask(get, x, y, z) {
  const ox = Math.floor(x) - 1,
    oy = Math.floor(y) - 1,
    oz = Math.floor(z) - 1;
  let low = 0,
    high = 0;
  for (let i = 0; i < 45; i++)
    if (
      solid(
        get(ox + (i % 3), oy + Math.floor(i / 9), oz + (Math.floor(i / 3) % 3)),
      )
    ) {
      if (i < 32) low |= 1 << i;
      else high |= 1 << (i - 32);
    }
  return [low, high];
}
export function predict(kernel, get, player, input, seconds) {
  const p = { ...player },
    dt = Math.max(0, Math.min(0.1, seconds)) / 4,
    speed = input.fly ? 7 : 5.5;
  const length = Math.max(1, Math.hypot(input.mx, input.mz)),
    mx = input.mx / length,
    mz = input.mz / length;
  for (let i = 0; i < 4; i++) {
    let vy = kernel.vertical(
      p.vy || 0,
      p.grounded ? 1 : 0,
      input.jump ? 1 : 0,
      input.fly ? 1 : 0,
      input.up,
      dt,
    );
    const x = kernel.clip(
      p.x,
      p.y,
      p.z,
      mx * speed * dt,
      0,
      ...collisionMask(get, p.x, p.y, p.z),
    );
    const z = kernel.clip(
      x,
      p.y,
      p.z,
      mz * speed * dt,
      2,
      ...collisionMask(get, x, p.y, p.z),
    );
    const y = kernel.clip(
      x,
      p.y,
      z,
      vy * dt,
      1,
      ...collisionMask(get, x, p.y, z),
    );
    const clipped = Math.abs(y - p.y - vy * dt) > 0.000001;
    Object.assign(p, {
      x,
      y,
      z,
      vy: clipped ? 0 : vy,
      grounded: clipped && vy < 0,
    });
  }
  return p;
}
export function ray(get, p, yaw, pitch, reach = 6) {
  const direction = [
      -Math.sin(yaw) * Math.cos(pitch),
      Math.sin(pitch),
      -Math.cos(yaw) * Math.cos(pitch),
    ],
    origin = [p.x, p.y + 1.55, p.z];
  const at = origin.map(Math.floor),
    step = direction.map((x) => (x < 0 ? -1 : 1)),
    delta = direction.map((x) =>
      Math.abs(x) > 0.0000001 ? 1 / Math.abs(x) : 1e30,
    );
  const next = direction.map((d, i) =>
    Math.abs(d) > 0.0000001
      ? (at[i] + (step[i] + 1) / 2 - origin[i]) / d
      : 1e30,
  );
  let distance = 0,
    previous = [...at];
  while (distance <= reach) {
    if (solid(get(...at)))
      return {
        x: at[0],
        y: at[1],
        z: at[2],
        adjacent: { x: previous[0], y: previous[1], z: previous[2] },
      };
    previous = [...at];
    const axis =
      next[0] <= next[1] && next[0] <= next[2] ? 0 : next[1] <= next[2] ? 1 : 2;
    at[axis] += step[axis];
    distance = next[axis];
    next[axis] += delta[axis];
  }
  return null;
}
export async function instantiate(bytes) {
  const { instantiateCore } = await import("../shared/wasm.mjs"),
    e = await instantiateCore(bytes);
  return {
    memory: e.memory,
    ...Object.fromEntries(
      ["height", "terrain", "solid", "clip", "vertical"].map((name) => [
        name,
        e[`fern:showcase/voxel#${name}`],
      ]),
    ),
  };
}
