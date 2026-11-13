# Audio equalizer

This module changes the loudness of bands of frequencies in a stream of samples. Each band is a second-order filter, a biquad, with the coefficients of the Audio EQ Cookbook of Robert Bristow-Johnson.

## Model

A `Band` has a shape, a frequency in Hz, a gain in dB and a Q:

- `Peak` changes the frequencies near `hz`. A larger Q gives a narrower band.
- `LowShelf` changes the frequencies below `hz`.
- `HighShelf` changes the frequencies above `hz`.

An `Equalizer` runs its bands one after the other on each channel. It keeps the last two values of each filter for each channel, so the samples of one call continue those of the call before. `run` gives a new equalizer with the new values and does not change its receiver. An equalizer is a value, so a program can keep it in its state. `retuned` gives the equalizer new gains and keeps those values, so a change of gain while the sound plays makes no click.

## Contracts

The samples are interleaved 16-bit values in `i32`. A frame has one sample for each channel. `run` writes its results in the place of its input. It rounds each result to the nearest integer and limits it to -32768 to 32767. An equalizer whose gains are all 0 dB gives its samples back unchanged and does no arithmetic.

Some bands cannot work at a sample rate, and these bands do not change the sound. Such a band has a frequency at or above half of the sample rate, or a Q that is not above 0. A sample rate that is not above 0 also gives such bands.

`response` gives the change in dB that the bands make at one frequency. A program can use it to draw the curve of the equalizer.

## Executable example

```fern test name=std-audio-eq-example
import "std/audio/eq";

function main(): i32 {
  let bands = [eq.Band { shape: eq.Peak, hz: 1000.0, db: 6.0, q: 1.0 }];
  let e = eq.new(48000, 1, bands);
  // The peak gives 6 dB at its frequency, and almost nothing far from it.
  let at = e.response(1000.0);
  let far = e.response(50.0);
  if ((at - 6.0).abs() < 0.01 && far.abs() < 0.1) {
    return 0;
  }
  return 1;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<coefficients>>

<<running>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module changes the loudness of bands of frequencies in a stream of
// 16-bit samples.
import "std/array";
import "std/f64";
import "std/i32";

pub enum Shape { Peak, LowShelf, HighShelf }

pub struct Band { shape: Shape, hz: f64, db: f64, q: f64 }

// The coefficients of a biquad, divided by its a0.
pub struct Filter { b0: f64, b1: f64, b2: f64, a1: f64, a2: f64 }

// `state` has two values for each filter on each channel: those of the
// channel 0 first, then those of the channel 1, and so on.
pub opaque struct Equalizer { rate: i32, channels: i32, bands: Band[], filters: Filter[], state: f64[], flat: boolean }
```

### `coefficients`

The cookbook writes each filter with `A = 10^(db / 40)`, `w0 = 2 pi hz / rate` and `alpha = sin(w0) / (2 Q)`. A filter that cannot work at the rate is the identity.

```fern
<<coefficients>>=
const TAU: f64 = 6.283185307179586;

// The filter that does not change the sound.
pub function identity(): Filter {
  return Filter { b0: 1.0, b1: 0.0, b2: 0.0, a1: 0.0, a2: 0.0 };
}

// The coefficients over `a0`.
function scaled(b0: f64, b1: f64, b2: f64, a0: f64, a1: f64, a2: f64): Filter {
  return Filter { b0: b0 / a0, b1: b1 / a0, b2: b2 / a0, a1: a1 / a0, a2: a2 / a0 };
}

// The biquad of the band `b` at the sample rate `rate`.
pub function filter(rate: i32, b: Band): Filter {
  if (rate <= 0 || b.q <= 0.0 || b.hz <= 0.0 || b.hz * 2.0 >= rate as f64 || b.db == 0.0) {
    return identity();
  }
  let a = (b.db / 40.0).exp10();
  let w0 = TAU * b.hz / (rate as f64);
  let cw = w0.cos();
  let alpha = w0.sin() / (2.0 * b.q);
  let k = 2.0 * a.sqrt() * alpha;
  return match (b.shape) {
    Peak => scaled(1.0 + alpha * a, -2.0 * cw, 1.0 - alpha * a, 1.0 + alpha / a, -2.0 * cw, 1.0 - alpha / a),
    LowShelf => scaled(a * (a + 1.0 - (a - 1.0) * cw + k), 2.0 * a * (a - 1.0 - (a + 1.0) * cw), a * (a + 1.0 - (a - 1.0) * cw - k), a + 1.0 + (a - 1.0) * cw + k, -2.0 * (a - 1.0 + (a + 1.0) * cw), a + 1.0 + (a - 1.0) * cw - k),
    HighShelf => scaled(a * (a + 1.0 + (a - 1.0) * cw + k), -2.0 * a * (a - 1.0 + (a + 1.0) * cw), a * (a + 1.0 + (a - 1.0) * cw - k), a + 1.0 - (a - 1.0) * cw + k, 2.0 * (a - 1.0 - (a + 1.0) * cw), a + 1.0 - (a - 1.0) * cw - k),
  };
}

// The change in dB that the filter `f` makes at `hz`, at the rate `rate`.
pub function gain_at(f: Filter, rate: i32, hz: f64): f64 {
  if (rate <= 0) {
    return 0.0;
  }
  let w = TAU * hz / (rate as f64);
  let c1 = w.cos();
  let s1 = w.sin();
  let c2 = (2.0 * w).cos();
  let s2 = (2.0 * w).sin();
  let nr = f.b0 + f.b1 * c1 + f.b2 * c2;
  let ni = f.b1 * s1 + f.b2 * s2;
  let dr = 1.0 + f.a1 * c1 + f.a2 * c2;
  let di = f.a1 * s1 + f.a2 * s2;
  return 10.0 * ((nr * nr + ni * ni) / (dr * dr + di * di)).log10();
}
```

### `running`

Each filter is in the transposed direct form II: `y = b0 x + z1`, then `z1 = b1 x - a1 y + z2` and `z2 = b2 x - a2 y`. This form has the fewest values to keep and works well with `f64`.

```fern
<<running>>=
function zeros(count: i32): f64[] {
  let out: f64[] = [];
  for _ in 0..count {
    out = out.append(0.0);
  }
  return out;
}

function all_flat(bands: Band[]): boolean {
  for b in bands {
    if (b.db != 0.0) {
      return false;
    }
  }
  return true;
}

// An equalizer of `bands` for `channels` channels at the rate `rate`.
pub function new(rate: i32, channels: i32, bands: Band[]): Equalizer {
  let c = channels.max(1);
  return Equalizer { rate: rate, channels: c, bands: bands, filters: bands.map((b: Band): Filter => filter(rate, b)), state: zeros(2 * bands.len() * c), flat: all_flat(bands) };
}

// The equalizer with the bands `bands`. It keeps the values of its
// filters when it has the same number of bands.
pub function (e: Equalizer) retuned(bands: Band[]): Equalizer {
  let kept = if (e.bands.len() == bands.len()) { e.state } else { zeros(2 * bands.len() * e.channels) };
  return Equalizer { rate: e.rate, channels: e.channels, bands: bands, filters: bands.map((b: Band): Filter => filter(e.rate, b)), state: kept, flat: all_flat(bands) };
}

pub function (e: Equalizer) bands(): Band[] {
  return e.bands;
}

// The gains of all the bands are 0 dB, so `run` changes nothing.
pub function (e: Equalizer) flat(): boolean {
  return e.flat;
}

pub function (e: Equalizer) rate(): i32 {
  return e.rate;
}

pub function (e: Equalizer) channels(): i32 {
  return e.channels;
}

// The change in dB that all the bands make at `hz`.
pub function (e: Equalizer) response(hz: f64): f64 {
  let total = 0.0;
  for f in e.filters {
    total += gain_at(f, e.rate, hz);
  }
  return total;
}

// The equalizer after the samples `samples`, and the samples that it
// gives for them, in the place of the input.
pub function (e: Equalizer) run(own samples: i32[]): (Equalizer, i32[]) {
  if (e.flat) {
    return (e, samples);
  }
  let channels = e.channels;
  let filters = e.filters;
  let s = e.state;
  let out = samples;
  let n = filters.len();
  for k in 0..out.len() {
    let at = 2 * n * (k % channels);
    let x = out[k] as f64;
    for j in 0..n {
      let f = filters[j];
      let z = at + 2 * j;
      let y = f.b0 * x + s[z];
      s = s.with(z, f.b1 * x - f.a1 * y + s[z + 1]);
      s = s.with(z + 1, f.b2 * x - f.a2 * y);
      x = y;
    }
    out = out.with(k, (x.round() as i32).max(-32768).min(32767));
  }
  return (Equalizer { ...e, state: s }, out);
}
```
