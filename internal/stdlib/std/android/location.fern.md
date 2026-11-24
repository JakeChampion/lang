# Location

This module gives the place of the phone, through the location manager of Android.

## Model

The app declares a @jni class that implements `android.location.LocationListener`. Its `onLocationChanged` calls `changed`, its `onProviderEnabled` calls `enabled`, and its `onProviderDisabled` calls `disabled`. Below API level 30 Android also calls `onStatusChanged`, so declare it too, with an empty body.

`start` makes one listener of that class, and asks for a fix about each `period_ms` milliseconds. The listener sends each fix to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event, and `event` decodes it. `stop` stops the fixes.

A `Fix` has the latitude and the longitude in degrees, and the altitude in metres above the WGS 84 ellipsoid. The accuracy is a radius in metres. It also has the speed in m/s, the bearing in degrees, and the time in milliseconds since 1970. `On` and `Off` come when the person turns the location of the phone on or off.

`last` gives the last fix that the phone knows, at once. It can be old, and it is None when the phone knows no fix.

A `precise` request uses the permission `ACCESS_FINE_LOCATION`. Other requests use `ACCESS_COARSE_LOCATION`, which gives a place to about 2 km. Declare the permissions with `-apk-permission` or in the `[android]` table. `allowed` tells whether the app has the permission, and `ask` shows its dialog.

From API level 31 the module uses the fused provider, which mixes the satellites, Wi-Fi and the cell network. Below that level, a precise request uses the satellites and other requests use the network.

## Contracts

Call each function except `changed`, `enabled` and `disabled` on the loop thread. `start` and `last` give None when the app does not have the permission, or when the phone has no provider. Call `stop` before Destroy.

The callbacks of the listener run on the UI thread.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<fixes>>

<<the listener>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives the place of the phone.
import "std/android";
import "std/android/location/bindings";
import "std/jni";
```

### `fixes`

The kinds of the `Posted` events from 36 to 38 belong to this module.

```fern
<<fixes>>=
pub struct Fix { latitude: f64, longitude: f64, altitude: f64, accuracy: f32, speed: f32, bearing: f32, ms: i64 }

pub enum Change {
  Moved(Fix),
  On,
  Off,
}

pub struct Location { manager: usize, listener: usize }

const FIX: i32 = 36;

const ON: i32 = 37;

const OFF: i32 = 38;

const FINE: string = "android.permission.ACCESS_FINE_LOCATION";

const COARSE: string = "android.permission.ACCESS_COARSE_LOCATION";

// The slots of 8 bytes of a fix for the loop thread.
const SLOTS: i32 = 7;

// The change of a Posted event of this module, or None for each other
// event.
pub function event(e: android.Event): Option[Change] {
  if let android.Posted(kind, arg) = e {
    if (kind == FIX) {
      return Some(Moved(taken_fix(arg)));
    }
    if (kind == ON) {
      return Some(On);
    }
    if (kind == OFF) {
      return Some(Off);
    }
  }
  return None;
}

function fix_buffer(f: Fix): usize {
  unsafe {
    let buf: usize = __alloc(SLOTS * 8);
    __store_i64(buf, f64_bits(f.latitude));
    __store_i64(buf + 8 as usize, f64_bits(f.longitude));
    __store_i64(buf + 16 as usize, f64_bits(f.altitude));
    __store_i64(buf + 24 as usize, f32_bits(f.accuracy) as i64);
    __store_i64(buf + 32 as usize, f32_bits(f.speed) as i64);
    __store_i64(buf + 40 as usize, f32_bits(f.bearing) as i64);
    __store_i64(buf + 48 as usize, f.ms);
    return buf;
  }
}

function taken_fix(buf: usize): Fix {
  unsafe {
    let f = Fix {
      latitude: f64_from_bits(__load_i64(buf)),
      longitude: f64_from_bits(__load_i64(buf + 8 as usize)),
      altitude: f64_from_bits(__load_i64(buf + 16 as usize)),
      accuracy: f32_from_bits(__load_i64(buf + 24 as usize) as i32),
      speed: f32_from_bits(__load_i64(buf + 32 as usize) as i32),
      bearing: f32_from_bits(__load_i64(buf + 40 as usize) as i32),
      ms: __load_i64(buf + 48 as usize),
    };
    __free(buf, SLOTS * 8);
    return f;
  }
}
```

### `the listener`

```fern
<<the listener>>=
pub function allowed(ctx: usize, precise: boolean): boolean {
  if (android.has_permission(ctx, FINE)) {
    return true;
  }
  return !precise && android.has_permission(ctx, COARSE);
}

// Shows the dialog of the permission. The answer comes with the next Resume.
pub function ask(ctx: usize, precise: boolean): void {
  if (allowed(ctx, precise)) {
    return;
  }
  if (precise) {
    android.request_permissions(ctx, [FINE, COARSE]);
  } else {
    android.request_permissions(ctx, [COARSE]);
  }
}

function provider(ctx: usize, precise: boolean): string {
  if (android.sdk_version(ctx) >= 31) {
    return "fused";
  }
  if (precise) {
    return "gps";
  }
  return "network";
}

function new_listener(e: usize, c: bindings.Classes, activity: bindings.Context, name: string): Result[bindings.LocationListener, jni.JavaError] {
  let loader = c.context.get_class_loader(e, activity)?;
  let class = c.class_loader.load_class(e, loader, name)?;
  let instance = c.class.new_instance(e, class)?;
  return Ok(bindings.LocationListener { ref: instance.ref });
}

// Fixes about each period_ms milliseconds for the app's LocationListener.
pub function start(ctx: usize, class: string, precise: boolean, period_ms: i64): Option[Location] {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return None;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  if (!allowed(ctx, precise)) {
    return None;
  }
  let Ok(c) = bindings.classes(e) else {
    return None;
  };
  defer c.release(e);
  let activity = bindings.Context { ref: android.activity_object(ctx) };
  let Ok(service) = c.context.get_system_service(e, activity, "location") else {
    return None;
  };
  if (service.ref == 0 as usize) {
    return None;
  }
  let manager = bindings.LocationManager { ref: service.ref };
  let Ok(listener) = new_listener(e, c, activity, class) else {
    return None;
  };
  let Ok(looper) = c.context.get_main_looper(e, activity) else {
    return None;
  };
  let kept = jni.new_global_ref(e, manager.ref);
  if (kept == 0 as usize) {
    let cleared = jni.exception_clear(e);
    return None;
  }
  let receiver = jni.new_global_ref(e, listener.ref);
  if (receiver == 0 as usize) {
    let cleared = jni.exception_clear(e);
    jni.delete_global_ref(e, kept);
    return None;
  }
  if let Err(_) = c.location_manager.request_location_updates(e, manager, provider(ctx, precise), period_ms, 0f32, listener, looper) {
    jni.delete_global_ref(e, receiver);
    jni.delete_global_ref(e, kept);
    return None;
  }
  return Some(Location { manager: kept, listener: receiver });
}

// Stops the fixes and releases the references retained by start.
pub function stop(ctx: usize, l: Location): void {
  let e = android.env(ctx);
  defer jni.delete_global_ref(e, l.manager);
  defer jni.delete_global_ref(e, l.listener);
  if (!jni.push_local_frame(e, 32)) {
    return;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.location_manager_class(e) else {
    return;
  };
  defer c.release(e);
  let removed = c.remove_updates(e, bindings.LocationManager { ref: l.manager }, bindings.LocationListener { ref: l.listener });
}

pub function last(ctx: usize, precise: boolean): Option[Fix] {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return None;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  if (!allowed(ctx, precise)) {
    return None;
  }
  let Ok(context) = bindings.context_class(e) else {
    return None;
  };
  defer context.release(e);
  let Ok(service) = context.get_system_service(e, bindings.Context { ref: android.activity_object(ctx) }, "location") else {
    return None;
  };
  if (service.ref == 0 as usize) {
    return None;
  }
  let Ok(manager) = bindings.location_manager_class(e) else {
    return None;
  };
  defer manager.release(e);
  let Ok(loc) = manager.get_last_known_location(e, bindings.LocationManager { ref: service.ref }, provider(ctx, precise)) else {
    return None;
  };
  if (loc.is_null()) {
    return None;
  }
  return fix_of(e, loc);
}

function fix_of(e: usize, loc: bindings.Location): Option[Fix] {
  let Ok(c) = bindings.location_class(e) else {
    return None;
  };
  defer c.release(e);
  let Ok(latitude) = c.get_latitude(e, loc) else {
    return None;
  };
  let Ok(longitude) = c.get_longitude(e, loc) else {
    return None;
  };
  let Ok(altitude) = c.get_altitude(e, loc) else {
    return None;
  };
  let Ok(accuracy) = c.get_accuracy(e, loc) else {
    return None;
  };
  let Ok(speed) = c.get_speed(e, loc) else {
    return None;
  };
  let Ok(bearing) = c.get_bearing(e, loc) else {
    return None;
  };
  let Ok(ms) = c.get_time(e, loc) else {
    return None;
  };
  return Some(Fix { latitude: latitude, longitude: longitude, altitude: altitude, accuracy: accuracy, speed: speed, bearing: bearing, ms: ms });
}

// The onLocationChanged of the app's listener: `env`, `listener` and
// `location` are its arguments. It sends the fix to the loop thread.
pub function changed(e: usize, listener: usize, location: usize): void {
  if (!jni.push_local_frame(e, 32)) {
    return;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Some(fix) = fix_of(e, bindings.Location { ref: location }) else {
    return;
  };
  let buf = fix_buffer(fix);
  if (!android.tell(e, FIX, buf)) {
    let dropped = taken_fix(buf);
  }
}

// The onProviderEnabled of the app's listener.
pub function enabled(e: usize, listener: usize, provider: usize): void {
  let sent = android.tell(e, ON, 0 as usize);
}

// The onProviderDisabled of the app's listener.
pub function disabled(e: usize, listener: usize, provider: usize): void {
  let sent = android.tell(e, OFF, 0 as usize);
}
```
