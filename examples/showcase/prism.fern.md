# Prism: a progressive path tracer in native Fern and Wasm

Each pixel sample follows a camera ray through a small scene. The floor and colored spheres scatter light. A metal sphere reflects it.
A worker accumulates independent samples in linear light, then applies exposure and tone mapping.

The browser can divide the image into disjoint stripes across one, two, or four workers. Each worker owns an isolated Wasm instance.
The same seed and pixel coordinates produce the same image at every worker count. Cancellation rejects obsolete image messages.

```fern
<<*>>=
<<Implementation>>
<<Entry>>
```

## Read the program

The named sections form one Fern module. Native code and browser Wasm use the same numerical rules.

```fern
<<Implementation>>=
<<Vector math>>
<<Deterministic samples>>
<<Scene geometry>>
<<Ray intersection>>
<<Renderer export>>
```

## Vector math

Small vector helpers keep the geometry readable. Normalization guards a zero-length vector.

```fern
<<Vector math>>=
import "std/f64";

struct V { x: f64, y: f64, z: f64 }

function add(a: V, b: V): V {
  return V { x: a.x + b.x, y: a.y + b.y, z: a.z + b.z };
}

function scale(a: V, b: f64): V {
  return V { x: a.x * b, y: a.y * b, z: a.z * b };
}

function dot(a: V, b: V): f64 {
  return a.x * b.x + a.y * b.y + a.z * b.z;
}

function unit(a: V): V {
  return scale(a, 1.0 / dot(a, a).sqrt().max(0.000001));
}
```

## Deterministic samples

An integer hash supplies repeatable samples. Pixel position and sample number determine its input.
The random sphere direction gives a cosine-weighted diffuse direction after addition to the surface normal.

```fern
<<Deterministic samples>>=
function seed(value: i32): f64 {
  let h = value as u32 * 747796405 + 2891336453;
  h = (h ^ h >> 16) * 2246822519;
  return (h & 16777215) as f64 / 16777216.0;
}
```

## Scene geometry

The scene contains a large floor sphere and three smaller spheres. Controls move the green sphere.
Three bytes pack its coordinates so the native reference route and browser use the same scalar ABI.

```fern
<<Scene geometry>>=
function center(index: i32, scene: i32): V {
  if (index == 0) {
    return V { x: 0.0, y: -1000.5, z: 0.0 };
  }
  if (index == 2) {
    return V { x: ((scene & 255) - 128) as f64 / 32.0, y: ((scene >> 8 & 255) - 128) as f64 / 32.0, z: ((scene >> 16 & 255) - 128) as f64 / 32.0 };
  }
  return V { x: (index - 2) as f64 * 1.15, y: 0.0, z: -1.0 };
}
```

## Ray intersection

The quadratic intersection chooses the nearest positive root. A small threshold avoids a hit on the surface that emitted the ray.
A missing intersection returns a distant sentinel inside this numerical kernel. HTTP parsing still uses Result errors.

```fern
<<Ray intersection>>=
function intersect(origin: V, direction: V, index: i32, scene: i32): f64 {
  let offset = add(origin, scale(center(index, scene), -1.0));
  let radius = 0.5;
  if (index == 0) {
    radius = 1000.0;
  }
  let b = dot(offset, direction);
  let discriminant = b * b - dot(offset, offset) + radius * radius;
  if (discriminant < 0.0) {
    return 1e20;
  }
  let t = -b - discriminant.sqrt();
  if (t > 0.001) {
    return t;
  }
  t = -b + discriminant.sqrt();
  if (t > 0.001) {
    return t;
  }
  return 1e20;
}
```

## Renderer export

The export takes a pixel, image dimensions, sample number, packed scene, and camera angle.
The sections below expand inside this one function.

```fern
<<Renderer export>>=
@export("fern:showcase/prism", "pixel")
pub function pixel(x: i32, y: i32, width: i32, height: i32, sample: i32, scene: i32, angle: f64): i32 {
  <<Camera ray>>
  <<Trace the path>>
  <<Pack linear light>>
}
```

## Camera ray

A pixel sample jitters its camera coordinates. An orthogonal basis maps the view plane into the scene.
The camera angle changes the origin and viewing direction.

```fern
<<Camera ray>>=
if (width < 1 || height < 1) {
  return 0;
}
let key = (y * width + x) * 4099 + sample * 7919;
let origin = V { x: angle.sin() * 4.0, y: 1.3, z: angle.cos() * 4.0 };
let forward = unit(add(V { x: 0.0, y: 0.0, z: -1.0 }, scale(origin, -1.0)));
let right = unit(V { x: -forward.z, y: 0.0, z: forward.x });
let up = V { x: -forward.y * right.z, y: right.z * forward.x - right.x * forward.z, z: forward.y * right.x };
let u = ((x as f64 + seed(key)) / (width as f64) * 2.0 - 1.0) * (width as f64) / (height as f64) * 0.35;
let v = (1.0 - (y as f64 + seed(key + 1)) / (height as f64) * 2.0) * 0.35;
let direction = unit(add(forward, add(scale(right, u), scale(up, v))));
```

## Trace the path

Each bounce finds the nearest sphere. A shadow ray tests the direct directional light.
Diffuse paths use cosine-weighted samples. A mirror path uses the reflected direction. The loop stops after five bounces.
The floor pattern makes camera motion and reflected geometry easy to see.

```fern
<<Trace the path>>=
let throughput = V { x: 1.0, y: 1.0, z: 1.0 };
let result = V { x: 0.0, y: 0.0, z: 0.0 };
let bounce = 0;
while (bounce < 5) {
  let distance = 1e20;
  let hit = 0 - 1;
  let i = 0;
  while (i < 4) {
    let t = intersect(origin, direction, i, scene);
    if (t < distance) {
      distance = t;
      hit = i;
    }
    i = i + 1;
  }
  if (hit < 0) {
    let sky = 0.5 * (direction.y + 1.0);
    result = add(result, V { x: throughput.x * (0.85 - 0.45 * sky), y: throughput.y * (0.9 - 0.25 * sky), z: throughput.z });
    break;
  }
  origin = add(origin, scale(direction, distance));
  let normal = unit(add(origin, scale(center(hit, scene), -1.0)));
  let albedo = V { x: 0.65, y: 0.65, z: 0.65 };
  if (hit == 0) {
    let checker = origin.x.floor() as i32 + origin.z.floor() as i32 & 1;
    if (checker == 0) {
      albedo = V { x: 0.78, y: 0.72, z: 0.58 };
    } else {
      albedo = V { x: 0.22, y: 0.28, z: 0.36 };
    }
  }
  if (hit == 1) {
    albedo = V { x: 0.85, y: 0.18, z: 0.10 };
  }
  if (hit == 2) {
    albedo = V { x: 0.12, y: 0.65, z: 0.35 };
  }
  if (hit == 3) {
    albedo = V { x: 0.7, y: 0.75, z: 0.9 };
  }
  throughput = V { x: throughput.x * albedo.x, y: throughput.y * albedo.y, z: throughput.z * albedo.z };
  let light = unit(V { x: -0.6, y: 0.9, z: 0.4 });
  let shadow = false;
  i = 1;
  while (i < 4) {
    if (intersect(add(origin, scale(normal, 0.002)), light, i, scene) < 1e19) {
      shadow = true;
    }
    i = i + 1;
  }
  if (!shadow) {
    result = add(result, scale(throughput, dot(normal, light).max(0.0) * 0.6));
  }
  let random_z = seed(key + bounce * 17 + 2) * 2.0 - 1.0;
  let random_angle = seed(key + bounce * 17 + 3) * 6.283185307179586;
  let random_radius = (1.0 - random_z * random_z).max(0.0).sqrt();
  let random = V { x: random_radius * random_angle.cos(), y: random_radius * random_angle.sin(), z: random_z };
  if (hit == 3) {
    direction = unit(add(direction, scale(normal, -2.0 * dot(direction, normal))));
  } else {
    direction = unit(add(normal, random));
  }
  origin = add(origin, scale(normal, 0.002));
  bounce = bounce + 1;
}
```

## Pack linear light

Each channel uses ten bits with a fixed scale of 255 units per radiance value.
The host averages these linear samples before tone mapping. Averaging gamma-corrected samples would bias the result.

```fern
<<Pack linear light>>=
let r = (result.x.min(4.0).max(0.0) * 255.0) as i32;
let g = (result.y.min(4.0).max(0.0) * 255.0) as i32;
let b = (result.z.min(4.0).max(0.0) * 255.0) as i32;
return r << 20 | g << 10 | b;
```

## Entry

The browser calls scalar exports directly. The command entry returns without work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

Run this example with `fern -doctest`. It uses the implementation above.

```fern test name=prism-example
<<Implementation>>
function main(): i32 {
 let value=pixel(16,10,32,20,0,6324352,0.0);
 if(value<0 || value!=pixel(16,10,32,20,0,6324352,0.0)){return 1;}
 return 0;
}
```
