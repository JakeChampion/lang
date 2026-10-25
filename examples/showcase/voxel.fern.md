# Blockyard: shared terrain and collision

The native world server and the browser use this same Fern module.
The build also emits a WebAssembly core module with scalar exports.
No JavaScript copy of the terrain or collision rules exists.

The world has 32 by 32 chunks. Each chunk contains 16 by 48 by 16 blocks.
Coordinates use meters. The player position is the center of the feet.

```fern
<<*>>=
<<Terrain>>
<<Collision>>
<<Entry>>
```

## Terrain

Integer interpolation keeps native and WebAssembly terrain identical.
A clear plaza gives new players room to meet. Trees fit inside their eight-block feature cells.
Materials are air, grass, dirt, stone, sand, wood, leaves, brick, glass, slate, light, and water.

```fern
<<Terrain>>=
import "std/f64";

pub const SIZE: i32 = 512;

pub const HEIGHT: i32 = 48;

pub const CHUNK: i32 = 16;

fip function hash(x: i32, z: i32): i32 {
  let h: u32 = x as u32 * 374761393 + z as u32 * 668265263 + 1831565813;
  h = (h ^ h >> 13) * 1274126177;
  return ((h ^ h >> 16) & 2147483647) as i32;
}

fip function noise(x: i32, z: i32, scale: i32, range: i32): i32 {
  let gx = x / scale;
  let gz = z / scale;
  let dx = x % scale;
  let dz = z % scale;
  let a = hash(gx, gz) % range * (scale - dx) + hash(gx + 1, gz) % range * dx;
  let b = hash(gx, gz + 1) % range * (scale - dx) + hash(gx + 1, gz + 1) % range * dx;
  return (a * (scale - dz) + b * dz) / (scale * scale);
}

function plaza(x: i32, z: i32, radius: i32): boolean {
  return (x - 256).abs() <= radius && (z - 256).abs() <= radius
    || (x - 96).abs() <= radius && (z - 96).abs() <= radius
    || (x - 416).abs() <= radius && (z - 96).abs() <= radius
    || (x - 96).abs() <= radius && (z - 416).abs() <= radius;
}

@export("fern:showcase/voxel", "height")
pub function height(x: i32, z: i32): i32 {
  if (plaza(x, z, 14)) {
    return 13;
  }
  return 6 + noise(x, z, 32, 15) + noise(x, z, 8, 4);
}

@export("fern:showcase/voxel", "terrain")
pub function terrain(x: i32, y: i32, z: i32): i32 {
  if (x < 0 || z < 0 || x >= SIZE || z >= SIZE || y < 0) {
    return 3;
  }
  if (y >= HEIGHT) {
    return 0;
  }
  let top = height(x, z);
  if (y <= top) {
    if (y == 0 || y < top - 3) {
      return 3;
    }
    if (y < top) {
      return 2;
    }
    if (top <= 10) {
      return 4;
    }
    if (plaza(x, z, 14) && (x % 5 == 0 || z % 5 == 0)) {
      return 9;
    }
    return 1;
  }
  if (y <= 10) {
    return 11;
  }
  if (y > top + 9 || plaza(x, z, 20)) {
    return 0;
  }
  let feature = hash(x / 8, z / 8);
  if (feature % 5 > 1) {
    return 0;
  }
  let tx = x / 8 * 8 + 3 + (feature & 1);
  let tz = z / 8 * 8 + 3 + (feature >> 1 & 1);
  let base = height(tx, tz);
  let crown = base + 5 + feature % 2;
  if (base <= 10) {
    return 0;
  }
  if (x == tx && z == tz && y <= crown && y > base) {
    return 5;
  }
  let dx = (x - tx).abs();
  let dz = (z - tz).abs();
  let dy = (y - crown).abs();
  if (dx <= 2 && dz <= 2 && dy <= 2 && dx + dz + dy <= 4) {
    return 6;
  }
  return 0;
}

@export("fern:showcase/voxel", "solid")
pub function solid(material: i32): i32 {
  if (material > 0 && material != 11) {
    return 1;
  }
  return 0;
}
```

## Collision

The host supplies a 45-bit mask for a three-by-five-by-three block neighborhood.
The lower corner is one block below and behind the player.
Each movement substep lasts at most 25 milliseconds. This bound prevents passage through a one-block wall or floor.

The collision function clips one axis against occupied blocks. The same scalar function runs in native Fern and browser Wasm.
Hosts resolve x, z, and then y. The vertical rule supplies gravity, jump speed, and creative flight.

```fern
<<Collision>>=
@export("fern:showcase/voxel", "vertical")
pub function vertical(velocity: f64, grounded: i32, jump: i32, flying: i32, up: f64, seconds: f64): f64 {
  if (flying != 0) {
    return up.max(-1.0).min(1.0) * 7.0;
  }
  if (grounded != 0 && jump != 0) {
    return 7.5;
  }
  return (velocity - 22.0 * seconds.max(0.0).min(0.025)).max(-24.0);
}

@export("fern:showcase/voxel", "clip")
pub function clip(x: f64, y: f64, z: f64, delta: f64, axis: i32, low: i32, high: i32): f64 {
  let mask: u64 = (low as u32) as u64 | (high as u32) as u64 << 32;
  let ox = x.floor() as i32 - 1;
  let oy = y.floor() as i32 - 1;
  let oz = z.floor() as i32 - 1;
  let nx = x;
  let ny = y;
  let nz = z;
  if (axis == 0) {
    nx = nx + delta;
  }
  if (axis == 1) {
    ny = ny + delta;
  }
  if (axis == 2) {
    nz = nz + delta;
  }
  let at: i32 = 0;
  while (at < 45) {
    if ((mask >> at as u64 & 1) != 0) {
      let bx = (ox + at % 3) as f64;
      let by = (oy + at / 9) as f64;
      let bz = (oz + at / 3 % 3) as f64;
      if (nx + 0.3 > bx + 0.000001 && nx - 0.3 < bx + 1.0 - 0.000001
        && ny + 1.7 > by + 0.000001 && ny < by + 1.0 - 0.000001
        && nz + 0.3 > bz + 0.000001 && nz - 0.3 < bz + 1.0 - 0.000001) {
        if (axis == 0 && delta > 0.0) {
          nx = nx.min(bx - 0.3);
        }
        if (axis == 0 && delta < 0.0) {
          nx = nx.max(bx + 1.3);
        }
        if (axis == 1 && delta > 0.0) {
          ny = ny.min(by - 1.7);
        }
        if (axis == 1 && delta < 0.0) {
          ny = ny.max(by + 1.0);
        }
        if (axis == 2 && delta > 0.0) {
          nz = nz.min(bz - 0.3);
        }
        if (axis == 2 && delta < 0.0) {
          nz = nz.max(bz + 1.3);
        }
      }
    }
    at = at + 1;
  }
  if (axis == 0) {
    return nx.max(0.31).min(511.69);
  }
  if (axis == 1) {
    return ny.max(1.0).min(45.9);
  }
  return nz.max(0.31).min(511.69);
}
```

## Entry

The browser calls exported functions directly. The command entry performs no work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```

## Check terrain and collision

```fern test name=blockyard-kernel-example
<<Terrain>>
<<Collision>>
function main(): i32 {
  if (height(256, 256) != 13 || terrain(256, 0, 256) != 3 || terrain(256, 30, 256) != 0) { return 1; }
  if (solid(11) != 0 || solid(8) != 1) { return 2; }
  let wall: i32 = 1 << 14;
  let clipped = clip(2.5, 2.0, 2.5, 0.4, 0, wall, 0);
  if (clipped < 2.699 || clipped > 2.701) { return 3; }
  if (vertical(0.0, 1, 1, 0, 0.0, 0.025) != 7.5) { return 4; }
  return 0;
}
```
