# Audio spectrum

This module gives the loudness of bands of frequencies in the last samples of a stream. A player can draw these levels as bars that move with the sound.

## Model

`levels` takes the last `SIZE` frames of the samples and mixes their channels to one. It multiplies them by a Hann window and does a fast Fourier transform of radix 2. The bands are from `LOW` Hz to `HIGH` Hz, or to half of the sample rate when that is less. Each band is a step of the same ratio of frequency, so each octave has the same number of bands.

The level of a band is that of its loudest frequency. A sine wave at full scale gives the level 0 dB. `levels` changes the decibels from `FLOOR` to 0 to the values 0 to 1.

## Contracts

The samples are interleaved 16-bit values in `i32`. Fewer than `SIZE` frames are taken with zeros before them. No samples, a count that is not above 0, and a sample rate that is not above 0 give levels of 0. A band that has no frequency of the transform in it takes the frequency nearest to its middle.

## Executable example

```fern test name=std-audio-spectrum-example
import "std/audio/spectrum";

function main(): i32 {
  // A sine wave at 1 kHz, at half of full scale, in one channel.
  let samples: i32[] = [];
  for k in 0..2048 {
    samples = samples.append((16384.0 * (6.283185307179586 * 1000.0 * (k as f64) / 48000.0).sin()) as i32);
  }
  let bands = spectrum.levels(samples, 1, 48000, 8);
  // The loudest band holds 1 kHz, and the bands far from it are quiet.
  let top = 0;
  for i in 0..bands.len() {
    if (bands[i] > bands[top]) {
      top = i;
    }
  }
  if (spectrum.band_hz(top, 8, 48000) < 1000.0 && spectrum.band_hz(top + 1, 8, 48000) > 1000.0 && bands[0] < 0.2) {
    return 0;
  }
  return 1;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<transform>>

<<bands>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives the loudness of bands of frequencies in the last
// samples of a stream.
import "std/f64";
import "std/i32";

// The number of frames of the transform.
pub const SIZE: i32 = 1024;

// The lowest and the highest frequency of the bands, in Hz.
pub const LOW: f64 = 40.0;

pub const HIGH: f64 = 16000.0;

// The level in dB that gives the value 0.
pub const FLOOR: f64 = -60.0;

const TAU: f64 = 6.283185307179586;
```

### `transform`

The transform works in place on two arrays: the real parts and the imaginary parts. It puts the values in the order of the reversed bits of their index, then does the stages of butterflies. Each stage doubles the length of the transforms that it joins.

```fern
<<transform>>=
// The values in the order of the reversed bits of their index.
function reordered(own re: f64[], n: i32): f64[] {
  let j = 0;
  for i in 1..n {
    let bit = n >> 1;
    while ((j & bit) != 0) {
      j ^= bit;
      bit >>= 1;
    }
    j ^= bit;
    if (i < j) {
      let v = re[i];
      re = re.with(i, re[j]);
      re = re.with(j, v);
    }
  }
  return re;
}

// The transform of the real values `input`, whose length is a power of 2:
// the real parts and the imaginary parts.
function transform(own input: f64[]): (f64[], f64[]) {
  let n = input.len();
  let re = reordered(input, n);
  let im: f64[] = [];
  for _ in 0..n {
    im = im.append(0.0);
  }
  let half = 1;
  while (half < n) {
    let step = -TAU / ((2 * half) as f64);
    for k in 0..half {
      let wr = (step * (k as f64)).cos();
      let wi = (step * (k as f64)).sin();
      let at = k;
      while (at < n) {
        let b = at + half;
        let tr = re[b] * wr - im[b] * wi;
        let ti = re[b] * wi + im[b] * wr;
        let ar = re[at];
        let ai = im[at];
        re = re.with(b, ar - tr);
        im = im.with(b, ai - ti);
        re = re.with(at, ar + tr);
        im = im.with(at, ai + ti);
        at += 2 * half;
      }
    }
    half *= 2;
  }
  return (re, im);
}
```

### `bands`

```fern
<<bands>>=
// The highest frequency of the bands at the rate `rate`.
function top(rate: i32): f64 {
  return HIGH.min(rate as f64 / 2.0);
}

// The lowest frequency of the band `i` of `count`, in Hz. The band
// `count` gives the highest frequency of the last band.
pub function band_hz(i: i32, count: i32, rate: i32): f64 {
  if (count <= 0) {
    return LOW;
  }
  return LOW * (top(rate) / LOW).pow(i as f64 / (count as f64));
}

// The last `SIZE` frames of `samples` as one channel from -1 to 1, with
// the Hann window.
function windowed(samples: [i32], channels: i32): f64[] {
  let c = channels.max(1);
  let frames = samples.len() / c;
  let out: f64[] = [];
  for k in 0..SIZE {
    let frame = frames - SIZE + k;
    let sum = 0.0;
    if (frame >= 0) {
      for ch in 0..c {
        sum += samples[frame * c + ch] as f64;
      }
    }
    let w = 0.5 - 0.5 * (TAU * (k as f64) / ((SIZE - 1) as f64)).cos();
    out = out.append(sum / (32768.0 * (c as f64)) * w);
  }
  return out;
}

// The level from 0 to 1 of the band `i` of `count`, from the squares of
// the magnitudes `power` of the bins of the transform.
function band_level(power: f64[], i: i32, count: i32, rate: i32): f64 {
  let width = rate as f64 / (SIZE as f64);
  let lo = band_hz(i, count, rate);
  let hi = band_hz(i + 1, count, rate);
  let first = ((lo / width).ceil() as i32).max(1);
  let last = ((hi / width).ceil() as i32 - 1).min(SIZE / 2);
  let most = 0.0;
  if (first > last) {
    let middle = (((lo * hi).sqrt() / width).round() as i32).max(1).min(SIZE / 2);
    most = power[middle];
  }
  for b in first..last + 1 {
    most = most.max(power[b]);
  }
  // A full sine of amplitude 1 gives a magnitude of SIZE / 4 with the
  // window.
  let scale = SIZE as f64 / 4.0;
  if (most <= 0.0) {
    return 0.0;
  }
  let db = 10.0 * (most / (scale * scale)).log10();
  return ((db - FLOOR) / -FLOOR).max(0.0).min(1.0);
}

// The levels from 0 to 1 of `count` bands of the last frames of
// `samples`, which has `channels` channels at the rate `rate`.
pub function levels(samples: [i32], channels: i32, rate: i32, count: i32): f64[] {
  let out: f64[] = [];
  if (count <= 0) {
    return out;
  }
  if (rate <= 0 || samples.len() == 0) {
    for _ in 0..count {
      out = out.append(0.0);
    }
    return out;
  }
  let (re, im) = transform(windowed(samples, channels));
  let power: f64[] = [];
  for b in 0..SIZE / 2 + 1 {
    power = power.append(re[b] * re[b] + im[b] * im[b]);
  }
  for i in 0..count {
    out = out.append(band_level(power, i, count, rate));
  }
  return out;
}
```
