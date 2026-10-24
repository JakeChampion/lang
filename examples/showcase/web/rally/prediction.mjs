// Visual prediction never changes the score or the server state.
export const clampPaddle = (value) => Math.max(0.105, Math.min(0.895, value));
export function paddle(value, target, seconds) {
  const distance = Math.max(0, Math.min(0.05, seconds)) * 1.6;
  return (
    value + Math.max(-distance, Math.min(distance, clampPaddle(target) - value))
  );
}
export function reconcile(authoritative, history, ack) {
  return history.reduce(
    (value, input) =>
      input.seq > ack ? paddle(value, input.y, input.dt) : value,
    authoritative,
  );
}
export function ball(game, seconds) {
  let { x, y, vx, vy } = game;
  if (!game.running) return { x, y };
  let remaining = Math.max(0, Math.min(0.1, seconds));
  while (remaining > 0) {
    const dt = Math.min(remaining, 0.008),
      oldX = x;
    x += vx * dt;
    y += vy * dt;
    if (y < 0.014) {
      y = 0.028 - y;
      vy = Math.abs(vy);
    }
    if (y > 0.986) {
      y = 1.972 - y;
      vy = -Math.abs(vy);
    }
    if (
      vx < 0 &&
      oldX >= 0.067 &&
      x < 0.067 &&
      Math.abs(y - game.left) < 0.12
    ) {
      x = 0.134 - x;
      vx = Math.min(0.85, Math.abs(vx) * 1.035);
      vy = (y - game.left) * 3.2;
    }
    if (
      vx > 0 &&
      oldX <= 0.933 &&
      x > 0.933 &&
      Math.abs(y - game.right) < 0.12
    ) {
      x = 1.866 - x;
      vx = -Math.min(0.85, Math.abs(vx) * 1.035);
      vy = (y - game.right) * 3.2;
    }
    remaining -= dt;
  }
  return { x: Math.max(-0.02, Math.min(1.02, x)), y };
}
