export function destination(seq, x, y) {
  const bytes = new ArrayBuffer(8),
    view = new DataView(bytes);
  view.setUint32(0, seq, true);
  view.setUint16(4, Math.round(Math.max(0, Math.min(1024, x)) * 16), true);
  view.setUint16(6, Math.round(Math.max(0, Math.min(640, y)) * 16), true);
  return bytes;
}
export function decode(bytes) {
  const view = new DataView(bytes);
  if (view.byteLength < 4 || view.getUint8(0) !== 70 || view.getUint8(2) !== 1)
    throw Error("Invalid arena packet");
  if (view.getUint8(1) === 65) {
    if (view.byteLength !== 12) throw Error("Invalid input acknowledgement");
    return {
      kind: "ack",
      seq: view.getUint32(4, true),
      serverUs: view.getUint32(8, true),
    };
  }
  if (view.getUint8(1) !== 67 || view.byteLength < 32)
    throw Error("Invalid arena snapshot");
  const count = view.getUint16(10, true),
    seedCount = view.getUint16(24, true);
  if (
    count > 512 ||
    seedCount !== 64 ||
    view.byteLength !== 32 + count * 12 + seedCount * 5
  )
    throw Error("Invalid snapshot length");
  const players = [],
    seeds = [];
  let at = 32;
  for (let i = 0; i < count; i++, at += 12)
    players.push({
      id: view.getUint16(at, true),
      x: view.getUint16(at + 2, true) / 16,
      y: view.getUint16(at + 4, true) / 16,
      score: view.getUint16(at + 6, true),
      seq: view.getUint32(at + 8, true),
    });
  for (let i = 0; i < seedCount; i++, at += 5)
    seeds.push({
      x: view.getUint16(at, true) / 16,
      y: view.getUint16(at + 2, true) / 16,
      active: view.getUint8(at + 4) === 1,
    });
  return {
    kind: "snapshot",
    tick: view.getUint32(4, true),
    round: view.getUint16(8, true),
    paused: view.getUint8(3) !== 0,
    remainingMs: view.getUint32(12, true),
    score: view.getUint32(16, true),
    goal: view.getUint32(20, true),
    players,
    seeds,
  };
}
export function move(point, target, seconds) {
  const dx = target.x - point.x,
    dy = target.y - point.y,
    distance = Math.hypot(dx, dy),
    amount = Math.min(distance, Math.max(0, Math.min(0.1, seconds)) * 180);
  if (!distance) return { ...point };
  return {
    x: point.x + (dx * amount) / distance,
    y: point.y + (dy * amount) / distance,
  };
}
export function reconcile(player, history) {
  return history.reduce(
    (point, input) =>
      input.seq > player.seq ? move(point, input, input.dt) : point,
    { x: player.x, y: player.y },
  );
}
