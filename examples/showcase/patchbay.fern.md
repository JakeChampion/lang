# Patchbay: a shared sequencer and a Fern synthesizer

The browser hosts the synthesizer inside an AudioWorklet. Fern computes the oscillator, envelope, filter, and drive.
The audio clock schedules samples independently of page animation.

Participants share the sequence, effects, and live keys over WebSocket. A common epoch and round-trip samples align playback clocks.
The interface reports clock uncertainty. Network distance still affects a live performance.

```fern
<<*>>=
<<Implementation>>
<<Entry>>
```

## Read the program

The named sections form one Fern module. Native code and browser Wasm use the same numerical rules.

```fern
<<Implementation>>=
<<Math>>
<<Band-limited edges>>
<<Filter and drive>>
<<Prepare a voice>>
```

## Math

Scalar exports avoid an array ABI at the audio boundary. The host retains its voice and delay buffers.
The Wasm tests repeatedly call each kernel and check that its memory size remains fixed.

```fern
<<Math>>=
import "std/f64";
```

## Band-limited edges

PolyBLEP corrects saw and pulse discontinuities near the start and end of a cycle.
This reduces aliasing without a large oscillator table.

```fern
<<Band-limited edges>>=
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
```

## Filter and drive

The low-pass filter uses one previous sample. The host computes its coefficient once per audio block.
Soft clipping bounds the driven signal. The delay uses a separate fixed ring buffer in the host.

```fern
<<Filter and drive>>=
@export("fern:showcase/patchbay", "coefficient")
pub function coefficient(cutoff: f64, rate: i32): f64 {
  if (rate < 8000 || rate > 192000) {
    return 0.0;
  }
  return 1.0 - (-6.283185307179586 * cutoff.max(20.0).min(rate as f64 * 0.45) / (rate as f64)).exp();
}

@export("fern:showcase/patchbay", "filter")
pub function filter(sample: f64, previous: f64, cutoff: f64, rate: i32): f64 {
  return filter_step(sample, previous, coefficient(cutoff, rate));
}

@export("fern:showcase/patchbay", "filter-step")
pub function filter_step(sample: f64, previous: f64, coefficient: f64): f64 {
  return previous + coefficient.max(0.0).min(1.0) * (sample - previous);
}

@export("fern:showcase/patchbay", "drive")
pub function drive(sample: f64, gain: f64): f64 {
  let x = sample * gain.max(1.0).min(8.0);
  return x / (1.0 + x.abs());
}
```

## Prepare a voice

A MIDI note determines frequency. The host computes that frequency once when a voice starts.
The prepared-sample export avoids a power operation for every sample. The simple sample export remains useful for tests.
An attack and exponential decay form the envelope. A released key adds a faster decay.

```fern
<<Prepare a voice>>=
@export("fern:showcase/patchbay", "frequency")
pub function frequency(note: i32, rate: i32): f64 {
  if (note < 0 || note > 127 || rate < 8000 || rate > 192000) {
    return 0.0;
  }
  return (440.0 * 2.0.pow((note - 69) as f64 / 12.0)).min(rate as f64 * 0.45);
}

@export("fern:showcase/patchbay", "sample")
pub function sample(frame: i32, rate: i32, note: i32, wave: i32, age: f64, release: f64): f64 {
  return sample_hz(frame, rate, frequency(note, rate), wave, age, release);
}

@export("fern:showcase/patchbay", "sample-hz")
pub function sample_hz(frame: i32, rate: i32, frequency: f64, wave: i32, age: f64, release: f64): f64 {
  if (frame < 0 || rate < 8000 || rate > 192000 || frequency <= 0.0 || frequency > rate as f64 * 0.45 || age < 0.0) {
    return 0.0;
  }
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

The browser calls scalar exports directly. The command entry returns without work.

```fern
<<Entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

Run this example with `fern -doctest`. It uses the implementation above.

```fern test name=patchbay-example
<<Implementation>>
function main(): i32 {
 if(sample(20,0,69,0,0.01,-1.0)!=0.0){return 1;}
 let direct=sample(20,48000,69,0,0.01,-1.0);
 let prepared=sample_hz(20,48000,frequency(69,48000),0,0.01,-1.0);
 if((direct-prepared).abs()>0.000000001 || drive(1000.0,8.0)>=1.0){return 2;}
 return 0;
}
```
