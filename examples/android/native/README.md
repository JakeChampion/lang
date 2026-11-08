# A Fern Android app with no Java code

This app is a stopwatch. It runs in `android.app.NativeActivity`, so the APK
has no Java code and no `classes.dex`. A tap starts or pauses the stopwatch,
and the back key closes the app. The face shows minutes and seconds in
seven-segment digits.

- `app.fern` is the app. `ANativeActivity_onCreate` calls `android.start`, and
  the loop thread calls `android.run` with the state and the `step` function of
  the app.
- `face.fern` lays out the digits. It has no Android code, and
  `face_test.fern` tests it on the host.

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-native.apk` from `main`. Each nightly uses the same key, so it installs over the one before it.

Build and sign the APK:

```sh
fern -apk stopwatch.apk -apk-label "Fern Stopwatch" app.fern
adb install stopwatch.apk
```

`fern -apk` builds `app.fern` for `arm64-android` and `x86-64-android`. It
writes a binary `AndroidManifest.xml` with one NativeActivity, stores each
library at a 16 KB boundary, and signs the APK with APK Signature Scheme v2.
The default package is `dev.fern.app`. Set it with `-apk-package`.

The signing key is a P-256 key in a PEM file: `stopwatch.key.pem` next to the
APK, or the file that `-apk-key` names. When the file does not exist,
`fern -apk` makes a new key and writes it there. Keep the key: Android
installs an update only when the same key signs it. With the same key, the
same source gives the same APK, byte for byte.

To try a change on a phone or an emulator, add `-apk-run`:

```sh
fern -apk stopwatch.apk -apk-run app.fern
```

`-apk-run` builds the library for the ABI of the device that adb sees only. It installs the APK, starts it, and shows the log lines of the app until the app stops.

`emulator_check.py` drives the APK on an emulator in CI: the face, a tap
to start, a tap to pause, and the back key. `std/android` describes the event
model. The app writes its log lines to logcat with the tag `fern`.
