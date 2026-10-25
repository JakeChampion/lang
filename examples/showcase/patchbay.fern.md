# Patchbay: sample synthesis

Fern computes oscillator samples and an envelope.
An AudioWorklet host schedules the samples against the audio clock.

```fern
<<*>>=
<<Kernel>>
<<Entry>>
```

## Kernel

The same functions compile for native execution and browser WebAssembly.
The browser controls the display. Fern computes the result.

```fern
<<Kernel>>=
import "std/f64";

function blep(phase: f64, step: f64): f64 {
  if (phase < step) {
    let x = phase / step;
    return x + x - x * x - 1.0;
  }
  if (phase > 1.0 - step) {
    let x = (phase - 1.0) / step;
    return x * x + x + x + 1.0;
  }
  return 0.0;
}

@export("fern:showcase/patchbay", "filter")
pub function filter(sample: f64, previous: f64, cutoff: f64, rate: i32): f64 {
  if (rate < 8000) {
    return 0.0;
  }
  let coefficient = 1.0 - (-6.283185307179586 * cutoff.max(20.0).min(rate as f64 * 0.45) / (rate as f64)).exp();
  return previous + coefficient * (sample - previous);
}

@export("fern:showcase/patchbay", "drive")
pub function drive(sample: f64, gain: f64): f64 {
  let x = sample * gain.max(1.0).min(8.0);
  return x / (1.0 + x.abs());
}

@export("fern:showcase/patchbay", "sample")
pub function sample(frame: i32, rate: i32, note: i32, wave: i32, age: f64, release: f64): f64 {
  if (frame < 0 || rate < 8000 || rate > 192000 || note < 0 || note > 127 || age < 0.0) {
    return 0.0;
  }
  let frequency = (440.0 * 2.0.pow((note - 69) as f64 / 12.0)).min(rate as f64 * 0.45);
  let phase = frame as f64 * frequency / (rate as f64);
  let cycle = phase - phase.floor();
  let value = (phase * 6.283185307179586).sin();
  if (wave == 1) {
    value = 1.0 - 4.0 * (cycle - 0.5).abs();
  }
  if (wave == 2) {
    value = 2.0 * cycle - 1.0 - blep(cycle, frequency / (rate as f64));
  }
  if (wave == 3) {
    if (cycle < 0.5) {
      value = 1.0;
    } else {
      value = -1.0;
    }
  }
  if (wave == 3) {
    let shifted = cycle + 0.5;
    if (shifted >= 1.0) {
      shifted = shifted - 1.0;
    }
    value = value + blep(cycle, frequency / (rate as f64)) - blep(shifted, frequency / (rate as f64));
  }
  let envelope = (age * 100.0).min(1.0) * (-age * 2.0).exp();
  if (release >= 0.0) {
    envelope = envelope * (-release * 25.0).exp();
  }
  return value * envelope * 0.12;
}
```

## Entry

The browser calls the exports directly. The command entry returns without work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```
