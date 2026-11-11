# A counter with `std/android/ui`

This app shows a number and two buttons. It is the smallest app of
`std/android/ui`: a state, a `view` that makes the screen from the state,
and an `update` that changes the state for each message. The app has no
drawing code and no layout arithmetic.

- `counter.fern` is the program: the `Model`, the `view` and the `update`.
  It has no Android code.
- `app.fern` is the NativeActivity. `std/android/ui/app` runs the program
  on the phone: it draws on the GPU, uses the font of the system, follows
  the dark theme, and gives the screen to TalkBack.
- `counter_test.fern` runs the program on the host with
  `std/android/ui/sim`. It taps the buttons by the texts that they show,
  and reads the texts of the screen.

Build and run it on a phone or an emulator:

```sh
fern -apk counter.apk -apk-run examples/android/counter/app.fern
```

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-counter.apk` from `main`.

## Tests

`internal/testing/e2e/android_counter_test.go` runs two checks:

- `TestAndroidCounterProgram` runs `counter_test.fern`. With
  `COUNTER_UI_PNG` set to a directory, the test writes the screen there as
  a PNG file, in the light theme and in the dark theme.
- `TestAndroidCounterApk` builds the APK and checks its entries.

The Android CI lane runs `emulator_check.py`. It taps the button three
times, presses the back key, and requires a count of 3 in logcat.
