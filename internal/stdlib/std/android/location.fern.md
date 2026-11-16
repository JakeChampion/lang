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

function manager_of(e: usize, obj: usize): usize {
  return jni.call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "location")]);
}

function new_object(e: usize, class: usize): usize {
  let none: usize = jni.jvalue_alloc(0);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  return obj;
}

// Fixes about each `period_ms` milliseconds for one listener of `class`, the
// Java name of the app's @jni class that implements LocationListener.
pub function start(ctx: usize, class: string, precise: boolean, period_ms: i64): Option[Location] {
  if (!allowed(ctx, precise)) {
    return None;
  }
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let manager: usize = manager_of(e, obj);
  if (manager == 0 as usize) {
    return None;
  }
  let listener: usize = new_object(e, jni.load_class(e, obj, class));
  let looper: usize = jni.call_object(e, obj, "getMainLooper", "()Landroid/os/Looper;", []);
  let sig = "(Ljava/lang/String;JFLandroid/location/LocationListener;Landroid/os/Looper;)V";
  if (!jni.call_void(e, manager, "requestLocationUpdates", sig, [jni.new_string(e, provider(ctx, precise)), period_ms as usize, 0 as usize, listener, looper])) {
    return None;
  }
  return Some(Location { manager: jni.new_global_ref(e, manager), listener: jni.new_global_ref(e, listener) });
}

// Stops the fixes of `l` and frees its listener.
pub function stop(ctx: usize, l: Location): void {
  let e: usize = android.env(ctx);
  let removed: boolean = jni.call_void(e, l.manager, "removeUpdates", "(Landroid/location/LocationListener;)V", [l.listener]);
  jni.delete_global_ref(e, l.listener);
  jni.delete_global_ref(e, l.manager);
}

pub function last(ctx: usize, precise: boolean): Option[Fix] {
  if (!allowed(ctx, precise)) {
    return None;
  }
  let e: usize = android.env(ctx);
  let manager: usize = manager_of(e, android.activity_object(ctx));
  if (manager == 0 as usize) {
    return None;
  }
  let loc: usize = jni.call_object(e, manager, "getLastKnownLocation", "(Ljava/lang/String;)Landroid/location/Location;", [jni.new_string(e, provider(ctx, precise))]);
  if (jni.exception_occurred(e) != 0 as usize) {
    jni.exception_clear(e);
    return None;
  }
  if (loc == 0 as usize) {
    return None;
  }
  return Some(fix_of(e, loc));
}

function method_of(e: usize, obj: usize, name: string, sig: string): usize {
  return jni.get_method_id(e, jni.get_object_class(e, obj), name, sig);
}

function fix_of(e: usize, loc: usize): Fix {
  let none: usize = jni.jvalue_alloc(0);
  let f = Fix {
    latitude: jni.call_double_method_a(e, loc, method_of(e, loc, "getLatitude", "()D"), none),
    longitude: jni.call_double_method_a(e, loc, method_of(e, loc, "getLongitude", "()D"), none),
    altitude: jni.call_double_method_a(e, loc, method_of(e, loc, "getAltitude", "()D"), none),
    accuracy: jni.call_float_method_a(e, loc, method_of(e, loc, "getAccuracy", "()F"), none),
    speed: jni.call_float_method_a(e, loc, method_of(e, loc, "getSpeed", "()F"), none),
    bearing: jni.call_float_method_a(e, loc, method_of(e, loc, "getBearing", "()F"), none),
    ms: jni.call_long_method_a(e, loc, method_of(e, loc, "getTime", "()J"), none) as i64,
  };
  jni.free_buffer(none);
  return f;
}

// The onLocationChanged of the app's listener: `env`, `listener` and
// `location` are its arguments. It sends the fix to the loop thread.
pub function changed(e: usize, listener: usize, location: usize): void {
  let buf = fix_buffer(fix_of(e, location));
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
