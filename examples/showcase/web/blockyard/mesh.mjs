export const palette = [
  [0, 0, 0],
  [0.39, 0.6, 0.35],
  [0.48, 0.32, 0.21],
  [0.51, 0.56, 0.58],
  [0.83, 0.76, 0.55],
  [0.47, 0.32, 0.2],
  [0.31, 0.52, 0.3],
  [0.69, 0.36, 0.28],
  [0.57, 0.83, 0.88],
  [0.27, 0.35, 0.4],
  [1, 0.78, 0.32],
  [0.27, 0.64, 0.77],
];
const dimensions = [16, 48, 16],
  transparent = (m) => m === 8 || m === 11;
export function meshChunk(data, cx, cz, outside = () => 0) {
  const opaque = [],
    alpha = [];
  const get = (x, y, z) =>
    x < 0 || x >= 16 || y < 0 || y >= 48 || z < 0 || z >= 16
      ? outside(cx * 16 + x, y, cz * 16 + z)
      : data[(y * 16 + z) * 16 + x];
  for (let axis = 0; axis < 3; axis++)
    for (const sign of [-1, 1]) {
      const u = (axis + 1) % 3,
        v = (axis + 2) % 3,
        width = dimensions[u],
        height = dimensions[v],
        mask = new Uint8Array(width * height);
      const shade =
        axis === 1 ? (sign > 0 ? 1 : 0.48) : axis === 0 ? 0.82 : 0.68;
      for (let slice = 0; slice < dimensions[axis]; slice++) {
        const p = [0, 0, 0];
        p[axis] = slice;
        for (let j = 0; j < height; j++)
          for (let i = 0; i < width; i++) {
            p[u] = i;
            p[v] = j;
            const m = get(...p);
            p[axis] += sign;
            const n = get(...p);
            p[axis] -= sign;
            mask[j * width + i] =
              m && (!n || (transparent(n) && n !== m)) ? m : 0;
          }
        for (let j = 0; j < height; j++)
          for (let i = 0; i < width;) {
            const m = mask[j * width + i];
            if (!m) {
              i++;
              continue;
            }
            let w = 1,
              h = 1;
            while (i + w < width && mask[j * width + i + w] === m) w++;
            outer: while (j + h < height) {
              for (let k = 0; k < w; k++)
                if (mask[(j + h) * width + i + k] !== m) break outer;
              h++;
            }
            const points = [
                [i, j],
                [i + w, j],
                [i + w, j + h],
                [i, j + h],
              ],
              target = transparent(m) ? alpha : opaque,
              color = palette[m];
            for (const corner of sign > 0
              ? [0, 1, 2, 0, 2, 3]
              : [0, 2, 1, 0, 3, 2]) {
              const point = [0, 0, 0];
              point[axis] = slice + (sign > 0 ? 1 : 0);
              point[u] = points[corner][0];
              point[v] = points[corner][1];
              target.push(
                point[0] + cx * 16,
                point[1],
                point[2] + cz * 16,
                color[0] * shade,
                color[1] * shade,
                color[2] * shade,
                m === 8 ? 0.42 : m === 11 ? 0.74 : 1,
                m === 10 ? 1 : 0,
              );
            }
            for (let y = 0; y < h; y++)
              for (let x = 0; x < w; x++) mask[(j + y) * width + i + x] = 0;
            i += w;
          }
      }
    }
  return { opaque: new Float32Array(opaque), alpha: new Float32Array(alpha) };
}
