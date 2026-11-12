# Audio speed and pitch

This module plays sound faster or slower with the same pitch, and changes the pitch of sound with the same speed.

## Model

The samples are those of `std/audio/pcm`: the samples of each frame are one after the other, one for each channel.

A `Tempo` changes the speed. `push` takes samples and gives the samples that are ready, and `flush` gives the rest at the end of the sound. At the speed 2, the output has half of the frames of the input. At the speed 1, the output is the input.

A `Tempo` puts parts of the input one after the other, and each part fades into the next one. A part is 20 ms long. The next part starts where the speed says, or as far as 12 ms from there: at the place where the sound is most like the sound that the part before it goes on with. So a wave of the sound stays whole across two parts, and the pitch stays the same.

A `Resampler` reads the input at a step. At the step 2, it gives each second frame, so the sound is two times as fast and one octave higher. A frame between two frames of the input comes from the four frames around it.

To change the pitch only, change the speed and then read at a step. For the speed `s` and the pitch `p`, give the `Tempo` the speed `s / p` and give the `Resampler` the step `p`.

## Contracts

The number of the samples of each `push` is a multiple of the number of the channels. A speed is from 0.25 to 4, and a step is from 0.25 to 4: a value outside is the nearest of them.

A `Resampler` has no filter. A step above 1 folds the sound above half of the new rate down into the sound.

## Executable example

```fern test name=std-audio-tempo-example
import "std/audio/tempo";

function main(): i32 {
  // One second of a ramp in one channel, at 8000 frames for each second.
  let sound: i32[] = [];
  for i in 0..8000 {
    sound = sound.append(i % 100 * 50);
  }
  let (fast, head) = tempo.new(8000, 1).at(2.0).push(sound);
  let (done, tail) = fast.flush();
  // Half of the frames, and the frames that the last part goes on with.
  let frames = head.len() + tail.len();
  if (frames >= 4000 && frames < 4400) {
    return 0;
  }
  return 1;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<speed>>

<<pitch>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module changes the speed and the pitch of sound.
import "std/f64";
import "std/i32";
```

### `speed`

`next` is the frame of the input that the sound goes on with. `ideal` is the frame where the speed says that the next part starts. The two are the same at the speed 1, where each part is the one that the sound goes on with.

The search compares the sum of the channels of each frame, in `mono`. It looks at each fourth place and then at each place around the best one, and it compares each third frame of a part.

```fern
<<speed>>=
// Sound that plays at a speed with the pitch that it has. `input` has the
// samples that the output can need, and `mono` has the sum of the channels
// of each of its frames.
pub struct Tempo { channels: i32, half: i32, reach: i32, speed: f64, input: i32[], mono: i32[], next: i32, ideal: f64 }

const PART_MS: i32 = 20;

const REACH_MS: i32 = 12;

const COARSE: i32 = 4;

const SPARSE: i32 = 3;

// A value from 0.25 to 4.
function ratio(x: f64): f64 {
  if (x < 0.25) {
    return 0.25;
  }
  if (x > 4.0) {
    return 4.0;
  }
  return x;
}

// A `Tempo` at the speed 1 for sound of `rate` frames for each second in
// `channels` channels.
pub function new(rate: i32, channels: i32): Tempo {
  return Tempo { channels: channels.max(1), half: (rate * PART_MS / 1000).max(8), reach: (rate * REACH_MS / 1000).max(4), speed: 1.0, input: [], mono: [], next: 0, ideal: 0.0 };
}

// The `Tempo` at the speed `speed`: 2 is two times as fast.
pub function (own t: Tempo) at(speed: f64): Tempo {
  return Tempo { ...t, speed: ratio(speed) };
}

// How much the part of `mono` at `at` is not the part at `want`: 0 for the
// same part. It stops when the sum is `limit` or more.
function apart(mono: [i32], at: i32, want: i32, n: i32, limit: i64): i64 {
  let sum = 0i64;
  let i = 0;
  while (i < n) {
    let d = mono[at + i] - mono[want + i];
    if (d < 0) {
      d = 0 - d;
    }
    sum += d as i64;
    if (sum >= limit) {
      return sum;
    }
    i += SPARSE;
  }
  return sum;
}

// The place from `lo` to `hi`, near `ideal`, where the part of `mono` is
// most like the part at `want`.
function best_place(mono: [i32], want: i32, n: i32, lo: i32, hi: i32, ideal: i32): i32 {
  let best = ideal.max(lo).min(hi);
  let least = apart(mono, best, want, n, 9223372036854775807i64);
  let at = lo;
  while (at <= hi && least > 0i64) {
    let d = apart(mono, at, want, n, least);
    if (d < least) {
      least = d;
      best = at;
    }
    at += COARSE;
  }
  let from = (best - COARSE + 1).max(lo);
  let to = (best + COARSE - 1).min(hi);
  at = from;
  while (at <= to && least > 0i64) {
    let d = apart(mono, at, want, n, least);
    if (d < least) {
      least = d;
      best = at;
    }
    at += 1;
  }
  return best;
}

// The `Tempo` with no frame before the frame `drop` of its input.
function without(own t: Tempo, drop: i32): Tempo {
  if (drop <= 0) {
    return t;
  }
  let Tempo { channels, half, reach, speed, input, mono, next, ideal } = t;
  let kept: i32[] = [];
  for i in drop * channels..input.len() {
    kept = kept.append(input[i]);
  }
  let sums: i32[] = [];
  for i in drop..mono.len() {
    sums = sums.append(mono[i]);
  }
  return Tempo { channels: channels, half: half, reach: reach, speed: speed, input: kept, mono: sums, next: next - drop, ideal: ideal - drop as f64 };
}

// The `Tempo` after one more part, and the samples of that part. The
// `Tempo` has the frames that the part needs.
function stepped(t: Tempo): (Tempo, i32[]) {
  let ideal = t.ideal as i32;
  let lo = (ideal - t.reach).max(0);
  let best = best_place(t.mono, t.next, t.half, lo, ideal + t.reach, ideal);
  let n = t.half;
  let c = t.channels;
  let made: i32[] = [];
  for i in 0..n {
    for ch in 0..c {
      let a = t.input[(t.next + i) * c + ch] as i64;
      let b = t.input[(best + i) * c + ch] as i64;
      made = made.append(((a * ((n - i) as i64) + b * (i as i64)) / (n as i64)) as i32);
    }
  }
  let ahead = t.ideal + n as f64 * t.speed;
  let drop = (best + n).min(ahead as i32 - t.reach);
  return (without(Tempo { ...t, next: best + n, ideal: ahead }, drop), made);
}

// The `Tempo` with the samples `samples` after its input, and the samples
// of the output that are ready.
pub function (own t: Tempo) push(samples: [i32]): (Tempo, i32[]) {
  let Tempo { channels, half, reach, speed, input, mono, next, ideal } = t;
  let all = input;
  let sums = mono;
  let at = 0;
  while (at + channels <= samples.len()) {
    let sum = 0;
    for ch in 0..channels {
      all = all.append(samples[at + ch]);
      sum += samples[at + ch] / channels;
    }
    sums = sums.append(sum);
    at += channels;
  }
  let state = Tempo { channels: channels, half: half, reach: reach, speed: speed, input: all, mono: sums, next: next, ideal: ideal };
  let out: i32[] = [];
  while (state.mono.len() >= state.next.max(state.ideal as i32 + state.reach) + state.half) {
    let (after, part) = stepped(state);
    state = after;
    for v in part {
      out = out.append(v);
    }
  }
  return (state, out);
}

// The `Tempo` with no input, and the samples that the sound goes on with.
pub function (own t: Tempo) flush(): (Tempo, i32[]) {
  let out: i32[] = [];
  for i in t.next.max(0) * t.channels..t.input.len() {
    out = out.append(t.input[i]);
  }
  return (Tempo { ...t, input: [], mono: [], next: 0, ideal: 0.0 }, out);
}
```

### `pitch`

`at` is the frame of `held` at or before the next frame of the output. `part` is how far the output is from that frame to the next one. `held` starts with one frame before `at`.

```fern
<<pitch>>=
// Sound that a reader takes at a step. `held` has the frames that the
// output can need.
pub struct Resampler { channels: i32, held: i32[], at: i32, part: f64 }

// A `Resampler` for sound in `channels` channels.
pub function resampler(channels: i32): Resampler {
  return Resampler { channels: channels.max(1), held: [], at: 1, part: 0.0 };
}

// The sample at the part `t` of the way from `b` to `c`, on the curve
// through `a`, `b`, `c` and `d`. It is not outside `b` and `c`.
function between(a: i32, b: i32, c: i32, d: i32, t: f64): i32 {
  let (p0, p1, p2, p3) = (a as f64, b as f64, c as f64, d as f64);
  let y = p1 + 0.5 * t * (p2 - p0 + t * (2.0 * p0 - 5.0 * p1 + 4.0 * p2 - p3 + t * (3.0 * (p1 - p2) + p3 - p0)));
  let v = y.round() as i32;
  return v.max(b.min(c)).min(b.max(c));
}

// The `Resampler` with the samples `samples` after its input, and the
// output that is ready: the input at each `step` frames. The step 2 is
// two times as fast and one octave higher.
pub function (own r: Resampler) push(samples: [i32], step: f64): (Resampler, i32[]) {
  let Resampler { channels, held, at, part } = r;
  let c = channels;
  let all = held;
  if (all.len() == 0) {
    for ch in 0..c.min(samples.len()) {
      all = all.append(samples[ch]);
    }
  }
  for i in 0..samples.len() - samples.len() % c {
    all = all.append(samples[i]);
  }
  let frames = all.len() / c;
  let by = ratio(step);
  let i = at;
  let t = part;
  let out: i32[] = [];
  while (i + 2 < frames) {
    for ch in 0..c {
      out = out.append(between(all[(i - 1) * c + ch], all[i * c + ch], all[(i + 1) * c + ch], all[(i + 2) * c + ch], t));
    }
    t += by;
    let whole = t as i32;
    i += whole;
    t -= whole as f64;
  }
  let drop = (i - 1).min(frames - 1).max(0);
  let kept: i32[] = [];
  for k in drop * c..all.len() {
    kept = kept.append(all[k]);
  }
  return (Resampler { channels: c, held: kept, at: i - drop, part: t }, out);
}
```
