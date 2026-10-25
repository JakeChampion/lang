# Swarm: Fern in the browser

Swarm is an interactive particle field with an optional synthesized soundtrack.
Fern computes every particle position and every audio sample inside the browser through WebAssembly.
JavaScript connects these scalar exports to WebGL and Web Audio. The native server only delivers files.

The kernel uses a procedural flow field. Time determines each orbit, so it needs no mutable particle history.
A pointer bends the field. Audio energy changes its radius and speed.
This model has no integration drift and no frame-rate dependency.

```fern
<<*>>=
<<Math and seeds>>
<<Particle field>>
<<Audio samples>>
<<Command entry>>
```

## Math and seeds

Each index has a stable seed. Integer operations supply deterministic variation without a random runtime dependency.
All exports take and return scalar numbers. Calls need no array conversion, heap allocation, or result cleanup.
The JavaScript host reuses one GPU upload buffer.

```fern
<<Math and seeds>>=
import "std/f64";

const TAU: f64 = 6.283185307179586;

fip function seed(index: i32, salt: i32): f64 {
  let value: u32 = index as u32 * 747796405 + salt as u32 * 2891336453 ^ 277803737;
  value = (value ^ value >> 16) * 2246822519;
  value = value ^ value >> 13;
  return (value & 65535) as f64 / 65535.0;
}
```

## Particle field

Three nested waves form each orbit. The pointer attracts or repels nearby particles through a smooth radial weight.
Coordinates stay inside the unit square. The result packs two unsigned 16-bit coordinates in one 32-bit word.
This gives subpixel precision on a 4K canvas and one Wasm call per particle.

The host decodes coordinates and submits them to WebGL. It contains no copy of the field equations.

```fern
<<Particle field>>=
@export("fern:showcase/swarm", "particle")
pub function particle(index: i32, seconds: f64, px: f64, py: f64, force: f64, energy: f64): i32 {
  let a: f64 = seed(index, 1);
  let b: f64 = seed(index, 2);
  let c: f64 = seed(index, 3);
  let t: f64 = seconds * (0.08 + b * 0.12);
  let angle: f64 = a * TAU + t;
  let radius: f64 = 0.08 + c * 0.32 + energy.max(0.0).min(1.0) * 0.045;
  let x: f64 = 0.5 + angle.cos() * radius + (angle * 3.0 + t * 0.7).sin() * (0.02 + b * 0.05);
  let y: f64 = 0.5 + angle.sin() * radius * 0.83 + (angle * 2.0 - t * 0.8).cos() * (0.03 + a * 0.04);
  let dx: f64 = px - x;
  let dy: f64 = py - y;
  let weight: f64 = force.max(-1.0).min(1.0) * 0.16 / (0.16 + dx * dx + dy * dy);
  x = (x + dx * weight * 0.55).max(0.0).min(1.0);
  y = (y + dy * weight * 0.55).max(0.0).min(1.0);
  return (x * 65535.0) as i32 << 16 | (y * 65535.0) as i32;
}
```

## Audio samples

A four-note pentatonic pattern repeats every eight seconds. Each note has an attack and an exponential decay.
A quiet fifth and octave add harmonics. A limiter bounds the output to a valid PCM sample.
A worker computes an eight-second PCM loop. Web Audio controls playback independently of animation frames.

Audio starts only after the visitor selects Enable audio. No microphone or network service supplies the sound.

```fern
<<Audio samples>>=
@export("fern:showcase/swarm", "sample")
pub function sample(frame: i32, rate: i32, tone: f64): f64 {
  if (rate < 8000 || rate > 192000 || frame < 0) {
    return 0.0;
  }
  let seconds: f64 = (frame % (rate * 8)) as f64 / (rate as f64);
  let beat: i32 = (seconds * 2.0) as i32;
  let phase: f64 = seconds - beat as f64 * 0.5;
  let ratio: f64 = 1.0;
  match (beat % 4) {
    1 => {
      ratio = 1.5;
    },
    2 => {
      ratio = 1.25;
    },
    3 => {
      ratio = 2.0;
    },
    _ => {}
  }
  let frequency: f64 = tone.max(110.0).min(440.0) * ratio;
  let envelope: f64 = (phase * 200.0).min(1.0) * ((0.5 - phase) * 100.0).min(1.0) * (-phase * 6.0).exp();
  let fundamental: f64 = (TAU * frequency * phase).sin();
  let harmony: f64 = (TAU * frequency * 1.5 * phase).sin() * 0.22 + (TAU * frequency * 2.0 * phase).sin() * 0.12;
  return ((fundamental + harmony) * envelope * 0.28).max(-1.0).min(1.0);
}
```

## Command entry

The browser instantiates the core module and calls its exports directly. It never calls `_start`.
The minimal WASI import reports a process exit as an error. The kernel performs no file or socket operations.

```fern
<<Command entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

The same kernel also compiles as native Fern. Its numerical contract does not depend on the host graphics APIs.

```fern test name=swarm-example
<<Math and seeds>>
<<Particle field>>
<<Audio samples>>
function main(): i32 {
  let at: i32 = particle(42, 1.0, 0.5, 0.5, 0.0, 0.0);
  if (at != particle(42, 1.0, 0.5, 0.5, 0.0, 0.0)) { return 1; }
  if (at == particle(42, 2.0, 0.5, 0.5, 0.0, 0.0)) { return 2; }
  if (sample(0, 24000, 220.0) != 0.0) { return 3; }
  if (sample(10, 0, 220.0) != 0.0) { return 4; }
  return 0;
}
```
