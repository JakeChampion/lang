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

Build the library for each ABI:

```sh
fern -target arm64-android -shared -export ANativeActivity_onCreate -o libnative.so app.fern
fern -target x86-64-android -shared -export ANativeActivity_onCreate -o libnative.so app.fern
```

`std/android` describes the event model. The library writes its log lines
to logcat with the tag `fern`.
