# Where Fern stands for Android apps

This document compares Fern with the other stacks for Android apps, as of
November 2026. The other stacks are Kotlin and Jetpack Compose, Java and
Views, Flutter, React Native, Kotlin Multiplatform and .NET MAUI. The
document tells what holds Fern back, and in which order the gaps close.

Three reviews made this document:

- An audit of the code of `std/android`, `fern -apk`, the examples and
  the tests.
- Research of the current documentation of the other stacks and of the
  rules of Google Play.
- Measurements of the APKs and the builds of the examples on a container
  with four cores.

## Summary

Fern is not yet the best stack for Android apps. It is already the best in
a small number of things, and it is far behind in others.

Fern is strong where the others are weak:

- **No toolchain to install.** `fern -apk OUT app.fern` writes a signed
  APK with no Android SDK, no Gradle and no JDK. `fern -apk-run` installs
  it on the phone that adb sees, starts it and shows its log.
- **Small apps.** The counter example is 1.05 MB with two ABIs. The
  stopwatch is 589 KB, or 131 KB with `-apk-compress`.
  - A minimal Compose app after R8 is about 2 MB to download.
  - A Flutter app is several MB for each ABI.
  - A React Native app is about 16 MB.
- **Tests with no phone.** `std/android/ui/sim` runs the whole app on the
  computer.
  - It sends taps, typing, drags and the back key.
  - It sets the time, the theme and the font size.
  - It reads the screen texts and what TalkBack reads.
  - Kotlin needs Robolectric or an emulator for the same.
- **Native media.** Fern has AAudio, MediaCodec, a media session, audio
  focus and decoders written in Fern. The Tune example plays MP3, FLAC,
  M4A, Ogg Vorbis and Opus with no codec of the system.

Fern is behind in six areas, and the first two stop a release on Google
Play today:

1. **Play Store packaging.** Play needs an AAB, a version code that goes
   up, and a target API level of 36 for new apps and updates since
   31 August 2026. `fern -apk` writes an APK only, and its target API
   level is 34.

2. **No Java or Kotlin libraries.** The APK path cannot include a JAR or
   an AAR. So AndroidX, Google Play services, Firebase, Play Billing and
   the SDKs of other vendors are out of reach.

3. **Text and languages.** There is no text shaping, no right-to-left
   text, no CFF fonts (most CJK fonts), no color emoji, no font fallback
   and no localized strings.

4. **A thin set of components.** There is no checkbox, radio button, menu,
   snackbar, tab bar, app bar, navigation stack or date picker. Each app
   builds these.

5. **Device features.** There is no camera, no sensors, no location, no
   Bluetooth LE, no NFC, no notifications other than those of a
   foreground service, no push, and no scheduled background work.

6. **Developer tools.** There is no hot reload, no preview, no documented
   debugger workflow on a phone, and each build compiles both ABIs again.

## Comparison

| Area | Fern | Kotlin + Compose | Flutter | React Native |
|---|---|---|---|---|
| Toolchain to install | None | Android Studio, Gradle, JDK | Flutter SDK, Android SDK | Node, Android SDK, Gradle |
| Release format | Signed APK | AAB with R8 | AAB | AAB through EAS |
| Target API 36 | No (34) | Yes | Yes | Yes |
| Download size of a small app | About 0.1 to 1 MB | About 2 MB | Several MB per ABI | About 16 MB |
| Use of Android and Java libraries | JNI and generated bindings, no JAR or AAR | Direct | Plugins and channels | Native modules |
| Hot reload | No | Live Edit, limited | Yes, stateful | Fast Refresh |
| UI model | Model, view and update | Composables | Widgets | React components |
| Material 3 components | Colors only, few components | Full | Full | Third party |
| Navigation | By hand | Navigation 3 | Navigator, go_router | React Navigation |
| Accessibility | TalkBack tree, font scale | Semantics | Semantics | Native views |
| Right-to-left text and shaping | No | Yes | Yes | Yes |
| UI tests on the computer | Full simulator | Robolectric | Widget tests | Jest |
| Screenshot tests | PNG of the simulator | Paparazzi, Roborazzi | Golden tests | Third party |
| Camera, location, push | No | Yes | Plugins | Expo modules |

The figures for the other stacks come from their documentation and from
the measured comparison of Software Mansion (2026). The figures for Fern
come from the examples of this repository.

## What changes first

The order puts first what stops a release, then what most apps need.

1. **Play Store packaging.**
   - The version code and the version name: `-apk-version-code`,
     `-apk-version-name`, and `version_code` and `version_name` in the
     `[android]` table. Done.
   - Target API 36. An app that targets API 36 does not get the back key
     as a key event, so it needs a back callback. Its window goes under
     the system bars, so it needs the insets of the bars.
   - An AAB, with the manifest and the resources in the protocol buffer
     format of aapt2.

2. **A key-value store and the state after the death of the process.**
   Small, and each app needs them.

3. **Effects that do not block the loop.** A task that runs on a worker
   and gives a message when it ends.

4. **Notifications and device features.** Notifications with channels,
   sensors through the NDK, then location and the camera.

5. **Components.** Checkbox, radio button, menu, snackbar, tabs, app bar
   and a navigation stack in `std/android/ui`.

6. **Java and Kotlin libraries.** Merge DEX files and manifests of a JAR
   or an AAR into the APK.

7. **Text.** Shaping, right-to-left text, CFF and color glyphs, fallback
   fonts, and localized strings.

8. **Developer tools.** A build of one ABI for development, a debugger
   workflow with lldb, and then a reload of the view and update code with
   the model kept.
