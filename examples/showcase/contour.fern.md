# Contour: two colours, a whole landscape

Inspired by [0xC0DE's terrain flyover](https://x.com/0xC0DE6502/status/1784520071009677812).
Fern builds a repeating heightfield from three scales of smooth value noise.
The browser projects samples from near to far, keeping the highest visible point
in each screen column. Only newly revealed edges get a yellow pixel; everything
else stays blue. There are no filled polygons, textures, or simulation requests.

```fern
<<*>>=
<<Heightfield>>
<<Entry>>
```

## Heightfield

Coordinates wrap every 4096 units, including negative coordinates. Smoothstep
interpolation removes seams between cells. Integer hashing gives each lattice
point a stable height. The scalar export needs no heap allocations.

```fern
<<Heightfield>>=
import "std/f64";

fip function lattice(x: i32, z: i32): f64 {
  let h: u32 = x as u32 * 374761393 + z as u32 * 668265263 + 1831565813;
  h = (h ^ h >> 13) * 1274126177;
  return ((h ^ h >> 16) & 65535) as f64 / 65535.0;
}

function noise(x: f64, z: f64, scale: i32): f64 {
  let px = x / (scale as f64);
  let pz = z / (scale as f64);
  let ix = px.floor() as i32;
  let iz = pz.floor() as i32;
  let fx = px - ix as f64;
  let fz = pz - iz as f64;
  let u = fx * fx * (3.0 - 2.0 * fx);
  let v = fz * fz * (3.0 - 2.0 * fz);
  let cells = 4096 / scale;
  let a = lattice(ix, iz);
  let b = lattice((ix + 1) % cells, iz);
  let c = lattice(ix, (iz + 1) % cells);
  let d = lattice((ix + 1) % cells, (iz + 1) % cells);
  return (a + (b - a) * u) * (1.0 - v) + (c + (d - c) * u) * v;
}

@export("fern:showcase/contour", "height")
pub function height(x: f64, z: f64): f64 {
  let px = x - (x / 4096.0).floor() * 4096.0;
  let pz = z - (z / 4096.0).floor() * 4096.0;
  return 12.0 + noise(px, pz, 128) * 70.0
    + noise(px, pz, 32) * 22.0 + noise(px, pz, 8) * 5.0;
}
```

## Entry

The browser calls the height export. JavaScript handles projection, visibility,
and Canvas output. Animation advances by elapsed time with a bounded step.
Pause, hidden tabs and reduced-motion preferences stop the animation loop.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```

## Check wrapping and bounds

```fern test name=contour-heightfield
<<Heightfield>>
function main(): i32 {
  let h = height(17.5, 42.0);
  if (h < 12.0 || h > 109.0) { return 1; }
  if ((h - height(4113.5, 42.0)).abs() > 0.000001) { return 2; }
  if ((height(-0.001, 42.0) - height(0.001, 42.0)).abs() > 0.01) { return 3; }
  return 0;
}
```
