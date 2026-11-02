# Fern Stopwatch for Android

A calm stopwatch and clock for long sessions, such as meditation. The UI
and UX follow [Tranquil Stopwatch](https://github.com/tibarj/tranquilstopwatch):

- The screen is black, to save power on AMOLED displays.
- A dim clock (`HH:MM`) shows above the stopwatch (`H:MM:SS`).
- Tap the stopwatch to start or pause it. It is white while it runs and red
  while it is paused.
- Long-tap the stopwatch to reset it. The run time goes into the total on
  the About screen.
- The display updates once per second, or once per minute when the seconds
  are hidden. There are no milliseconds.
- The screen keeps on while the stopwatch runs.
- The system bars stay hidden.
- Tap anywhere to show the menu button for 5 seconds.
- The menu opens Settings and About.
- Burn-in protection moves the content by a random amount every minute.
- The beeper sounds at each multiple of an interval on the stopwatch.
- The stopwatch keeps its time when the app closes, because the state is two
  numbers: the start time of the current run and the sum of earlier runs.

The clock and the stopwatch each have these settings: show or hide, thin
font, opacity and size. The stopwatch can also hide its seconds. The other
settings are the beep interval, the alarm volume for beeps, and the burn-in
amount.

## Where the code is

| file | what it does |
|---|---|
| `stopwatch.fern` | All of the behaviour: the state changes, the text, the colours, when to update next, the burn-in position, the beep times and the settings table. Every function is pure. |
| `native.fern` | The JNI methods of `Core`. Each one changes JNI types into Fern types and calls `stopwatch.fern`. |
| `stopwatch_test.fern` | The tests for `stopwatch.fern` (`std/test`). |
| `java/dev/fern/stopwatch/` | The Android shell: views, touch events, timers and stored values. It asks `Core` for every decision. |
| `host/HostCheck.java` | Calls every native method in a desktop JVM. |
| `build.sh` | Builds the signed APK. |

The settings screen has no layout file. It makes one row for each entry of
the table in `stopwatch.fern`. Thus a new entry in that table needs no Java
change.

## Build

```sh
make build
examples/android/stopwatch/build.sh
adb install -r build/android-stopwatch/fern-stopwatch.apk
```

`build.sh` uses the Android SDK at `ANDROID_HOME`. On Ubuntu with no SDK,
install these packages. They put an SDK at `/usr/lib/android-sdk`:

```sh
apt-get install aapt apksigner zipalign dalvik-exchange android-sdk-platform-23
```

The APK has `libfern.so` for `arm64-v8a` (`-target arm64-android`) and for
`x86_64` emulators (`-target x86-64-android`). It needs Android 6.0 (API 23)
or later. `build.sh` signs it with a new debug key unless `KEYSTORE` is set.

## Tests

`internal/testing/e2e/android_stopwatch_test.go` runs three checks:

- `TestAndroidStopwatchLogic` runs `stopwatch_test.fern`.
- `TestAndroidStopwatchExports` checks that each `native` method in
  `Core.java` has an export, and that the arm64 library is one that
  Android's loader accepts.
- `TestAndroidStopwatchHostJVM` loads the host build of `libfern.so` into a
  desktop JVM and runs `HostCheck`.

The Android CI lane builds the APK and keeps it as the `fern-stopwatch-apk`
artifact. Then it runs the APK on an x86-64 emulator with
`emulator_check.py`. The check requires these results:

- The content is at the screen centre.
- A tap starts the stopwatch, and a long tap resets it.
- The settings screen has its rows.
- logcat shows no crash.

It keeps the screenshots, the view trees and logcat as the
`fern-stopwatch-emulator` artifact. To run the check on a phone, connect the
phone with `adb`. Then run the script with the APK and an output directory.
