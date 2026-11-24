# Sensors

This module reads the motion, position and environment sensors of the phone, through the sensor manager of the NDK.

## Model

`start` makes a queue of events on the looper of the loop thread, and turns on the default sensor of each kind in `kinds`. The sensors send their values at about `period_us` microseconds apart, or faster. The loop gives the event `android.Ready(android.IDENT_SENSORS)` when the queue has values. `event` reads them from the queue as `Reading`s, the oldest first.

A `Reading` has the kind of its sensor, its values and its time on the monotonic clock in nanoseconds. The values of a kind are those of `ASensorEvent` of the NDK. For example the accelerometer gives the acceleration with gravity on the axes x, y and z, in m/s². The light sensor gives one value in lux.

`stop` turns the sensors off and frees the queue. A sensor that stays on uses power, so stop the sensors at each Pause, and start them again at the Resume.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. Give each event of the loop to `event`: the queue keeps its values until `event` reads them, and the looper gives the `Ready` event again while values wait.

`start` gives None when the phone has no sensor manager, or no sensor of the kinds. `has` tells whether the phone has a sensor of a kind. A kind that the phone does not have gives no readings.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<kinds>>

<<C functions>>

<<queue>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the sensors of the phone.
import "std/android";
import "std/android/sensors/bindings";
import "std/jni";
```

### `kinds`

The kinds are the sensor types of the NDK.

```fern
<<kinds>>=
pub const ACCELEROMETER: i32 = 1;

pub const MAGNETIC_FIELD: i32 = 2;

pub const GYROSCOPE: i32 = 4;

pub const LIGHT: i32 = 5;

pub const PRESSURE: i32 = 6;

pub const PROXIMITY: i32 = 8;

pub const GRAVITY: i32 = 9;

pub const LINEAR_ACCELERATION: i32 = 10;

pub const ROTATION_VECTOR: i32 = 11;

pub const STEP_COUNTER: i32 = 19;

pub struct Reading { kind: i32, values: f32[], ns: i64 }

pub struct Sensors { manager: usize, queue: usize, events: usize }
```

### `C functions`

`ASensorManager_getInstanceForPackage` came in API level 26, so its import is weak, and the module calls `ASensorManager_getInstance` below that level.

```fern
<<C functions>>=
@import("libandroid.so", "ASensorManager_getInstanceForPackage", weak)
function c_manager_for(package: usize): usize;

@import("libandroid.so", "ASensorManager_getInstance")
function c_manager(): usize;

@import("libandroid.so", "ASensorManager_getDefaultSensor")
function c_default_sensor(manager: usize, kind: i32): usize;

@import("libandroid.so", "ASensorManager_createEventQueue")
function c_create_queue(manager: usize, looper: usize, ident: i32, callback: usize, data: usize): usize;

@import("libandroid.so", "ASensorManager_destroyEventQueue")
function c_destroy_queue(manager: usize, queue: usize): i32;

@import("libandroid.so", "ASensorEventQueue_enableSensor")
function c_enable(queue: usize, sensor: usize): i32;

@import("libandroid.so", "ASensorEventQueue_setEventRate")
function c_set_rate(queue: usize, sensor: usize, period_us: i32): i32;

@import("libandroid.so", "ASensorEventQueue_disableSensor")
function c_disable(queue: usize, sensor: usize): i32;

@import("libandroid.so", "ASensorEventQueue_getEvents")
function c_get_events(queue: usize, events: usize, count: usize): i64;

@import("libandroid.so", "ALooper_forThread")
function c_looper_for_thread(): usize;
```

### `queue`

An `ASensorEvent` has 104 bytes: the version, the sensor and the type, a reserved word, the time at byte 16, and 16 values of 4 bytes from byte 24. The module reads up to `BATCH` events at a time.

```fern
<<queue>>=
const EVENT_SIZE: i32 = 104;

const BATCH: i32 = 32;

// The number of values of a reading of each kind.
function value_count(kind: i32): i32 {
  if (kind == LIGHT || kind == PRESSURE || kind == PROXIMITY || kind == STEP_COUNTER) {
    return 1;
  }
  if (kind == ROTATION_VECTOR) {
    return 5;
  }
  return 3;
}

function manager_of(ctx: usize): usize {
  unsafe {
    if (android.sdk_version(ctx) < 26) {
      return c_manager();
    }
    let e: usize = android.env(ctx);
    // The local frame bounds the local references during class and method lookup.
    if (!jni.push_local_frame(e, 8)) {
      return 0 as usize;
    }
    defer jni.pop_local_frame(e, 0 as usize);
    let Ok(c) = bindings.context_class(e) else {
      return 0 as usize;
    };
    defer c.release(e);
    let activity = bindings.Context { ref: android.activity_object(ctx) };
    let Ok(name) = c.get_package_name(e, activity) else {
      return 0 as usize;
    };
    let package = jni.cstr(name);
    let m: usize = c_manager_for(package);
    jni.free_buffer(package);
    return m;
  }
}

pub function has(ctx: usize, kind: i32): boolean {
  let m = manager_of(ctx);
  unsafe {
    return m != 0 as usize && c_default_sensor(m, kind) != 0 as usize;
  }
}

pub function start(ctx: usize, kinds: i32[], period_us: i32): Option[Sensors] {
  let m = manager_of(ctx);
  if (m == 0 as usize) {
    return None;
  }
  unsafe {
    let queue: usize = c_create_queue(m, c_looper_for_thread(), android.IDENT_SENSORS, 0 as usize, 0 as usize);
    if (queue == 0 as usize) {
      return None;
    }
    let on = 0;
    for kind in kinds {
      let sensor: usize = c_default_sensor(m, kind);
      if (sensor != 0 as usize && c_enable(queue, sensor) >= 0) {
        let rate: i32 = c_set_rate(queue, sensor, period_us);
        on += 1;
      }
    }
    if (on == 0) {
      let gone: i32 = c_destroy_queue(m, queue);
      return None;
    }
    return Some(Sensors { manager: m, queue: queue, events: __alloc(EVENT_SIZE * BATCH) });
  }
}

function reading_at(at: usize): Reading {
  unsafe {
    let kind: i32 = __load_i32(at + 8 as usize);
    let values: f32[] = [];
    for i in 0..value_count(kind) {
      values = values.append(f32_from_bits(__load_i32(at + (24 + 4 * i) as usize)));
    }
    return Reading { kind: kind, values: values, ns: __load_i64(at + 16 as usize) };
  }
}

// The readings that wait in the queue of `s`, when `e` is the Ready event
// of the sensors, and none for each other event.
pub function event(s: Sensors, e: android.Event): Reading[] {
  if let android.Ready(ident) = e {
    if (ident == android.IDENT_SENSORS) {
      return drained(s);
    }
  }
  return [];
}

function drained(s: Sensors): Reading[] {
  let out: Reading[] = [];
  unsafe {
    while (true) {
      let n: i64 = c_get_events(s.queue, s.events, BATCH as usize);
      if (n <= 0i64) {
        break;
      }
      for i in 0..n as i32 {
        out = out.append(reading_at(s.events + (i * EVENT_SIZE) as usize));
      }
      if (n < BATCH as i64) {
        break;
      }
    }
  }
  return out;
}

// Turns the sensors of `s` off and frees its queue.
pub function stop(s: Sensors): void {
  unsafe {
    let gone: i32 = c_destroy_queue(s.manager, s.queue);
    __free(s.events, EVENT_SIZE * BATCH);
  }
}
```
