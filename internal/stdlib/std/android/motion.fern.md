# Android motion

This module moves things on the screen as Android moves its own views: the speed of a finger, the fling of a list, the spring of a list edge, and the curves of animations. Every function is pure, so a test can step through a motion with no device.

## Model

A touch gives a `Sample` for each position of the finger. `track` keeps the samples of the last 100 ms, and `velocity` gives the speed of the finger in pixels per second from them: the slope of the least-squares line through the samples.

When the finger lifts, `fling` starts a fling at that speed. A `Fling` moves along the spline of Android's `OverScroller`, with its friction and its deceleration. So a list in Fern goes as far and stops as slowly as a list in Java. `position` gives the position of a fling at a time, and whether the fling is at its end.

When a list goes past its end, `spring` brings it back. A `Spring` is a critically damped spring of the stiffness of `SpringForce.STIFFNESS_MEDIUM`, so it does not bounce.

The curves take the part of an animation that is done, from 0 to 1, and give the part of the motion that is done:

- `ease`: slow at both ends. It is `AccelerateDecelerateInterpolator`, the default curve of `ViewPropertyAnimator`.
- `decelerate`: fast at the start. It is `DecelerateInterpolator` with a factor of 1.
- `fast_out_slow_in`, `linear_out_slow_in` and `fast_out_linear_in`: the standard curves of Material, which are cubic Bézier curves.

Times are nanoseconds on the monotonic clock, as the `Vsync` event of `std/android` gives them.

## Contracts

A density `d` is the pixels of one density-independent pixel, as `std/android.density` gives it divided by 160. Each curve gives 0 for a part at or below 0, and 1 for a part at or above 1.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<velocity>>

<<fling>>

<<spring>>

<<curves>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module moves things on the screen as Android moves its own views:
// the speed of a finger, the fling of a list, the spring of a list edge,
// and the curves of animations.
import "std/f64";
```

### `velocity`

```fern
<<velocity>>=
// A position of the finger at a time on the monotonic clock, in ns.
pub struct Sample { t: i64, y: f64 }

// The samples with `s` added and those older than 100 ms before it gone.
pub function track(samples: Sample[], s: Sample): Sample[] {
  let out: Sample[] = [];
  for x in samples {
    if (s.t - x.t <= 100000000i64) {
      out = out.append(x);
    }
  }
  return out.append(s);
}

// The speed of the finger in pixels per second: the slope of the line that
// fits the samples best.
pub function velocity(samples: Sample[]): f64 {
  let n = samples.len();
  if (n < 2) {
    return 0.0;
  }
  let t0 = samples[n - 1].t;
  let mt = 0.0;
  let my = 0.0;
  for s in samples {
    mt += (s.t - t0) as f64 / 1.0e9;
    my += s.y;
  }
  mt /= n as f64;
  my /= n as f64;
  let num = 0.0;
  let den = 0.0;
  for s in samples {
    let dt = (s.t - t0) as f64 / 1.0e9 - mt;
    num += dt * (s.y - my);
    den += dt * dt;
  }
  if (den <= 0.0) {
    return 0.0;
  }
  return num / den;
}
```

### `fling`

These are the constants of `OverScroller`. A fling of speed `v` goes for `1000 * exp(l / (r - 1))` ms and a distance of `f * c * exp(r / (r - 1) * l)` pixels. Here `l` is `ln(0.35 * v / (f * c))`, `f` is the scroll friction, `r` is the deceleration rate, and `c` is the physical coefficient: the gravity of the earth in inches, at the dots per inch of the screen, times 0.84.

The spline gives the part of the distance at each hundredth of the time. Each point comes from a bisection on the Bézier curve that `OverScroller` uses, as Android works it out when the class loads.

```fern
<<fling>>=
const INFLEXION: f64 = 0.35;

const START_TENSION: f64 = 0.5;

const END_TENSION: f64 = 1.0;

const SCROLL_FRICTION: f64 = 0.015;

const SAMPLES: i32 = 100;

// ln(0.78) / ln(0.9).
const DECELERATION_RATE: f64 = 2.358201815;

// The gravity of the earth, in m/s².
const GRAVITY: f64 = 9.80665;

// A fling from `start` by `distance` pixels in `dur` ns from `t0`, along the
// spline of OverScroller.
pub struct Fling { start: f64, distance: f64, t0: i64, dur: i64, spline: f64[] }

pub function no_fling(): Fling {
  return Fling { start: 0.0, distance: 0.0, t0: 0i64, dur: 0i64, spline: [] };
}

// The fling moves at all.
pub function moving(f: Fling): boolean {
  return f.dur > 0i64;
}

// The part of the distance at each hundredth of the time.
function spline(): f64[] {
  let out: f64[] = [];
  let p1 = START_TENSION * INFLEXION;
  let p2 = 1.0 - END_TENSION * (1.0 - INFLEXION);
  let x_min = 0.0;
  for i in 0..SAMPLES {
    let alpha = i as f64 / (SAMPLES as f64);
    let x_max = 1.0;
    let x = 0.0;
    let coef = 0.0;
    while (true) {
      x = x_min + (x_max - x_min) / 2.0;
      coef = 3.0 * x * (1.0 - x);
      let tx = coef * ((1.0 - x) * p1 + x * p2) + x * x * x;
      if ((tx - alpha).abs() < 1.0e-5) {
        break;
      }
      if (tx > alpha) {
        x_max = x;
      } else {
        x_min = x;
      }
    }
    out = out.append(coef * ((1.0 - x) * START_TENSION + x) + x * x * x);
  }
  return out.append(1.0);
}

// The physical coefficient of OverScroller on a screen of `d` pixels for
// each density-independent pixel, at 160 dots per inch for each one.
function physical(d: f64): f64 {
  return GRAVITY * 39.37 * (160.0 * d) * 0.84;
}

// A fling from `start` at `v` pixels per second on a screen of density `d`,
// at `t0`. A fling under 50 dp/s does not move, and one over 8000 dp/s moves
// at 8000 dp/s, as the ViewConfiguration of Android sets.
pub function fling(start: i32, v: f64, d: f64, t0: i64): Fling {
  let speed = v.abs();
  if (speed < 50.0 * d) {
    return no_fling();
  }
  speed = speed.min(8000.0 * d);
  let c = SCROLL_FRICTION * physical(d);
  let l = (INFLEXION * speed / c).log();
  let dur = 1000.0 * (l / (DECELERATION_RATE - 1.0)).exp();
  let distance = c * (DECELERATION_RATE / (DECELERATION_RATE - 1.0) * l).exp();
  if (v < 0.0) {
    distance = -distance;
  }
  return Fling { start: start as f64, distance: distance, t0: t0, dur: (dur * 1.0e6) as i64, spline: spline() };
}

// The position of the fling at `t`, and whether it has ended.
pub function position(f: Fling, t: i64): (f64, boolean) {
  if (f.dur <= 0i64 || t >= f.t0 + f.dur) {
    return (f.start + f.distance, true);
  }
  let x = ((t - f.t0) as f64 / (f.dur as f64)).max(0.0);
  let i = (x * (SAMPLES as f64)) as i32;
  let t_inf = i as f64 / (SAMPLES as f64);
  let t_sup = (i + 1) as f64 / (SAMPLES as f64);
  let d = f.spline[i] + (x - t_inf) / (t_sup - t_inf) * (f.spline[i + 1] - f.spline[i]);
  return (f.start + d * f.distance, false);
}
```

### `spring`

A critically damped spring with stiffness `k` and a mass of 1 has the angular frequency `w = sqrt(k)`. Its offset from the rest position is `(a + b t) exp(-w t)`, where `a` is the first offset and `b` is the first speed plus `w a`. The spring ends when it is less than half a pixel from the rest position and slower than 50 pixels per second. The last frame then puts it at the rest position.

```fern
<<spring>>=
// SpringForce.STIFFNESS_MEDIUM.
const STIFFNESS: f64 = 1500.0;

// A spring from `from` to `to`, at the speed `v` in pixels per second at
// the time `t0`.
pub struct Spring { from: f64, to: f64, v: f64, t0: i64 }

pub function spring(from: f64, to: f64, v: f64, t0: i64): Spring {
  return Spring { from: from, to: to, v: v, t0: t0 };
}

// The position of the spring at `t`, and whether it has ended.
pub function spring_position(s: Spring, t: i64): (f64, boolean) {
  let w = STIFFNESS.sqrt();
  let x = ((t - s.t0) as f64 / 1.0e9).max(0.0);
  let a = s.from - s.to;
  let b = s.v + w * a;
  let e = (-w * x).exp();
  let offset = (a + b * x) * e;
  let speed = (b - w * (a + b * x)) * e;
  if (offset.abs() < 0.5 && speed.abs() < 50.0) {
    return (s.to, true);
  }
  return (s.to + offset, false);
}
```

### `curves`

A cubic Bézier curve from (0, 0) to (1, 1) with the control points (x1, y1) and (x2, y2) gives x and y as functions of a parameter. `bezier` finds the parameter for the part `t` on the x axis with Newton steps. When a Newton step goes outside the curve, it uses a bisection. It then gives y at that parameter.

```fern
<<curves>>=
// The default curve of Android's animations: slow at both ends.
pub function ease(t: f64): f64 {
  if (t <= 0.0) {
    return 0.0;
  }
  if (t >= 1.0) {
    return 1.0;
  }
  return ((t + 1.0) * 3.14159265358979).cos() / 2.0 + 0.5;
}

// Fast at the start and slow at the end.
pub function decelerate(t: f64): f64 {
  let x = t.max(0.0).min(1.0);
  return 1.0 - (1.0 - x) * (1.0 - x);
}

// The standard curve of Material: cubic-bezier(0.4, 0, 0.2, 1).
pub function fast_out_slow_in(t: f64): f64 {
  return bezier(0.4, 0.0, 0.2, 1.0, t);
}

// The curve of Material for things that come in: cubic-bezier(0, 0, 0.2, 1).
pub function linear_out_slow_in(t: f64): f64 {
  return bezier(0.0, 0.0, 0.2, 1.0, t);
}

// The curve of Material for things that go out: cubic-bezier(0.4, 0, 1, 1).
pub function fast_out_linear_in(t: f64): f64 {
  return bezier(0.4, 0.0, 1.0, 1.0, t);
}

// One coordinate of the curve with the control values c1 and c2, at the
// parameter s.
function cubic(c1: f64, c2: f64, s: f64): f64 {
  let r = 1.0 - s;
  return 3.0 * r * r * s * c1 + 3.0 * r * s * s * c2 + s * s * s;
}

// The slope of `cubic` at the parameter s.
function slope(c1: f64, c2: f64, s: f64): f64 {
  let r = 1.0 - s;
  return 3.0 * r * r * c1 + 6.0 * r * s * (c2 - c1) + 3.0 * s * s * (1.0 - c2);
}

// The y of the curve where its x is `t`.
pub function bezier(x1: f64, y1: f64, x2: f64, y2: f64, t: f64): f64 {
  if (t <= 0.0) {
    return 0.0;
  }
  if (t >= 1.0) {
    return 1.0;
  }
  let lo = 0.0;
  let hi = 1.0;
  let s = t;
  for i in 0..24 {
    let x = cubic(x1, x2, s) - t;
    if (x.abs() < 1.0e-7) {
      break;
    }
    if (x > 0.0) {
      hi = s;
    } else {
      lo = s;
    }
    let m = slope(x1, x2, s);
    let next = s - x / m;
    if (m.abs() < 1.0e-6 || next <= lo || next >= hi) {
      next = (lo + hi) / 2.0;
    }
    s = next;
  }
  return cubic(y1, y2, s);
}
```
