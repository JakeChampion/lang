# Wall and monotonic clocks

This module reads the clocks.

## Model

`now` reads the wall clock as a `time.Timestamp`. `instant` reads the monotonic clock as an `Instant`, which never goes back and suits deadlines and elapsed time.

## Contracts

The wall clock can move backwards when the host sets it. The monotonic clock cannot, and only the difference between two of its readings has a meaning.
These functions are the only part of the time library that reaches the host's clock, so a module that only computes with time imports `std/time` alone.

## Executable example

```fern test name=std-clock-example
import "std/clock";
import "std/time";

function main(): i32 {
  let start: Instant = clock.instant();
  if (clock.elapsed(start).is_negative()) { return 1; }
  if (clock.now().sec < 1700000000) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub function now(): time.Timestamp>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module reads the wall clock and the monotonic clock.
import "std/time";
```

### `2 pub function now(): time.Timestamp`

```fern
<<2 pub function now(): time.Timestamp>>=
pub function now(): time.Timestamp {
  let d: Duration = time.duration_millis(now_unix_ms());
  return time.Timestamp { sec: d.sec, nsec: d.nsec };
}

pub function instant(): Instant {
  let d: Duration = time.duration_nanos(monotonic_ns());
  return Instant { sec: d.sec, nsec: d.nsec };
}

// The monotonic time since `start`.
pub function elapsed(start: Instant): Duration {
  return instant().since(start);
}
```
