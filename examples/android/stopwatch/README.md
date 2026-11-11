# Fern Stopwatch for Android

A calm stopwatch and clock for long sessions, such as meditation. The UX
follows [Tranquil Stopwatch](https://github.com/tibarj/tranquilstopwatch).
The look is the dot-matrix style of the [walkie-talkie](../walkie/README.md):
a black screen, white and grey round dots, and red only for the state.

The app has no Java code. It is a NativeActivity app, and `fern -apk`
writes its APK with no Android SDK.

- The screen is black, to save power on AMOLED displays.
- Labels and numbers are in the dot font of `../matrix.fern`, 5 by 7 round
  dots for each character.
- A clock (`HH:MM`) shows above the stopwatch (`H:MM:SS`).
- The stopwatch is a dark disc in a ring of 60 dots.
- Tap the stopwatch to start or pause it.
- Long-tap the stopwatch to reset it. The run time goes into the total on
  the About screen.
- The display updates once per second, or once per minute when the seconds
  are hidden. There are no milliseconds.
- The screen keeps on while the stopwatch runs.
- The system bars stay hidden.
- Tap anywhere to show the title and the MENU button for 5 seconds.
- The menu opens Settings and About.
- Burn-in protection moves the content by a random amount every minute.
- The beeper sounds at each multiple of an interval on the stopwatch.
- The stopwatch keeps its time when the app closes, because the state is two
  numbers: the start time of the current run and the sum of earlier runs.

The ring shows the seconds of the current minute. With the seconds hidden,
it shows the minutes of the current hour. While the stopwatch runs, the
passed dots are white and the current dot is red. The digits are red while
the stopwatch is paused with time on it, and white at other times. The
words under the digits tell the next action. The settings and the About
text show in dark cards, which scroll.

The clock and the stopwatch each have these settings: show or hide, thin
font (smaller dots), opacity and size. The stopwatch can also hide its
seconds. The other settings are the beep interval, the alarm volume for
beeps, and the burn-in amount. The volume keys change the volume of the
beeps.

## Where the code is

| file | what it does |
|---|---|
| `stopwatch.fern` | The behaviour: the state changes, the text, the colours of the dial, when to update next, the burn-in position, the beep times and samples, the files of the app and the settings table. Every function is pure. |
| `screen.fern` | The layout of each screen. |
| `ui.fern` | The look of each screen and of the launcher icon. `nodes` gives a screen as nodes of `std/android/ui/scene`: one node that draws the screen, and one node for each part that takes a tap. It does no other I/O. |
| `control.fern` | The behaviour of the app on its events: the touches, the keys, the timers, the files, the beeper and the window. Each input and output goes through a `Device`. |
| `app.fern` | The NativeActivity, and the `Device` of the phone. |
| `stopwatch_test.fern` | The tests for `stopwatch.fern` (`std/test`). |
| `ui_test.fern` | Draws each screen on a frame in memory and checks the colours of its parts and its layout. |
| `control_test.fern` | Gives `control.fern` touches, keys and times on the host through a fake `Device`, and checks the state and the frame. |
| `icon.png` | The launcher icon that `ui.icon` draws. |

The settings screen makes one card for each entry of the table in
`stopwatch.fern`. Thus a new entry in that table needs no other change.

The app keeps its state and its settings in two files of its private
directory, as lines of `key=value`.

## Build

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-stopwatch.apk` from `main`. Each nightly uses the same key, so it installs over the one before it.

```sh
make build
./bin/fern -apk fern-stopwatch.apk examples/android/stopwatch/app.fern
adb install -r fern-stopwatch.apk
```

The `[android]` table of `fern.toml` gives the package, the label, the icon and the API level.

The APK has the library for `arm64-v8a` and for `x86_64` emulators. It
needs Android 9 (API 28) or later, for the alarm usage of the beeps.

To write `icon.png` again after a change to `ui.icon`, run `ui_test.fern`
with `STOPWATCH_ICON` set to the path of `icon.png`. With
`STOPWATCH_UI_PNG` set to a directory, the test also writes each screen
there.

## Tests

`internal/testing/e2e/android_stopwatch_test.go` runs three checks:

- `TestAndroidStopwatchLogic` runs `stopwatch_test.fern`.
- `TestAndroidStopwatchUI` runs `ui_test.fern`, and requires that
  `icon.png` is the icon that `ui.icon` draws.
- `TestAndroidStopwatchApk` builds the APK and checks its entries and the
  export of each library.

The Android CI lane keeps the APK as the `fern-stopwatch-apk` artifact.
Then it runs the APK on an x86-64 emulator with `emulator_check.py`. The
check reads the log lines of the app and the colours of the screen. It
requires these results:

- The dial shows.
- A tap starts the stopwatch, and the ring shows its red dot.
- A long tap resets it.
- The menu opens the settings, and a tap on a card changes a setting.
- The back key goes back, and then closes the app.
- logcat shows no crash.

It keeps the screenshots and logcat as the `fern-stopwatch-emulator`
artifact. To run the check on a phone, connect the phone with `adb`. Then
run the script with the APK and an output directory.

`control_test.fern` writes the last frame of each case to the directory in
`STOPWATCH_CONTROL_PNG`, when it is set.
