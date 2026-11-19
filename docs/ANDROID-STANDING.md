# Where Fern stands for Android apps

Status: [research] a comparison of November 2026, and the order of the fixes.

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

- **Small apps.** The counter example is 1.05 MB with two ABIs, and the
  native example is 372 KB. The DEX file is at most 3.8 KB, because the
  APK holds no Java or Kotlin runtime.
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

Fern is behind in six areas. The first two stopped a release on Google
Play, and the first is now fixed:

1. **Play Store packaging.** Play needs an AAB, a version code that goes
   up, and a target API level of 36 for new apps and updates since
   31 August 2026. `fern -apk` wrote an APK only, with a target API level
   of 34. It now writes an AAB too, and it targets API level 36.

2. **No Java or Kotlin libraries.** The APK path cannot include a JAR or
   an AAR. So AndroidX, Google Play services, Firebase, Play Billing and
   the SDKs of other vendors are out of reach.

3. **Text and languages.** Arabic, Hebrew, the Indic scripts, Thai, Lao,
   Khmer, Myanmar, Korean and the scripts of the Universal Shaping Engine
   are shaped, and the marks sit on their bases. Emoji draw in color from the COLR font of the system.
   `std/i18n` now gives localized strings.
   `std/bidi` gives right-to-left text in the order of UAX #9.
   `std/android/text` takes a character that its font does not have from
   the fallback fonts of the system. The layout mirrors for a
   right-to-left language.

4. **A thin set of components.** There was no checkbox, radio button,
   menu, snackbar, tab bar, app bar or navigation stack. `std/android/ui`
   now has each of them. A date picker is still missing.

5. **Device features.** Fern now has the camera, the sensors, the
   location and notifications. There is no Bluetooth LE, no NFC, no push,
   and no scheduled background work.

6. **Developer tools.** There is no hot reload, no preview and no
   documented debugger workflow on a phone. A release build compiles both
   ABIs on one thread. `-apk-run` builds the ABI of the device only.

## Measurements

The examples were built with `fern -apk` on a container with four cores.
Each APK holds the code for arm64-v8a and x86_64, stored and not
compressed.

| App | Lines of source | APK | Build |
|---|---|---|---|
| native | 181 | 372 KB | 1.4 s |
| counter | 61 | 1.05 MB | 4.0 s |
| todo | 125 | 1.12 MB | 4.2 s |
| stopwatch | 1,848 | 985 KB | 2.4 s |
| walkie | 6,517 | 3.84 MB | 13 s |
| tune | 5,792 | 5.70 MB | 20 s |
| plex | 4,920 | 7.15 MB | 25 s |

- The build of a large app uses about 600 MB of memory.
- Deflate makes the native code about three times smaller. With
  `-apk-compress`, plex is about 2.3 MB for the two ABIs.
- The 42 UI tests of Tune run on the computer in about 70 s, which is
  1.7 s for each test. This is slow for tests with no device.

## Comparison

| Area | Fern | Kotlin + Compose | Flutter | React Native |
|---|---|---|---|---|
| Toolchain to install | None | Android Studio, Gradle, JDK | Flutter SDK, Android SDK | Node, Android SDK, Gradle |
| Release format | Signed APK or AAB | AAB with R8 | AAB | AAB through EAS |
| Target API 36 | Yes | Yes | Yes | Yes |
| Download size of a small app | About 0.1 to 1 MB | About 2 MB | Several MB per ABI | About 16 MB |
| Use of Android and Java libraries | JNI and generated bindings, no JAR or AAR | Direct | Plugins and channels | Native modules |
| Hot reload | No | Live Edit, limited | Yes, stateful | Fast Refresh |
| UI model | Model, view and update | Composables | Widgets | React components |
| Material 3 components | Colors only, few components | Full | Full | Third party |
| Navigation | By hand | Navigation 3 | Navigator, go_router | React Navigation |
| Accessibility | TalkBack tree, font scale | Semantics | Semantics | Native views |
| Right-to-left text and shaping | Order, Arabic, Hebrew, Thai, Khmer, Myanmar, Indic, USE and Hangul shaping | Yes | Yes | Yes |
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
   - Target API 36, done. An app that targets API 36 does not get the back key
     as a key event, so it needs a back callback. Its window goes under
     the system bars, so it needs the insets of the bars. `std/android/launch` registers both.
   - An AAB, done. With an OUT that ends in `.aab`, `fern -apk` writes the
     manifest and the resources in the protocol buffer format of aapt2,
     and signs the bundle with a JAR signature.

2. **A key-value store and the state after the death of the process.**
   Done. `std/kv` keeps named values in a file, and a save replaces the
   file in one step. `with_state` of `std/android/ui` writes a part of
   the state at each Pause and restores it when the app starts again.
   `sim.relaunch` tests that on the computer.

3. **Effects that do not block the loop.** Done. `with_jobs` of
   `std/android/ui` starts workers for a message, and the result of each
   worker is a message to `update`. `sim.jobs_done` tests that on the
   computer.

4. **Notifications and device features.** `std/android/notify` makes
   channels, and posts, changes and cancels notifications.
   `std/android/sensors` reads the sensors through the NDK, with a `Ready`
   event of the loop. `std/android/location` gives fixes through a @jni
   `LocationListener`. `std/android/camera` gives the frames of a camera
   through the NDK.

5. **Components.** Done: `std/android/ui` has a checkbox, a radio
   button, a menu, a snackbar, tabs, an app bar and the page stack `Nav`.
   `tinted` gives a canvas the color of a role of the theme.

6. **Java and Kotlin libraries.** Merge DEX files and manifests of a JAR
   or an AAR into the APK.

7. **Text.** A font with no GPOS table. HarfBuzz then moves the marks
   by their combining classes and the extents of their glyphs, and it
   applies the `kern` table. A font with no GDEF table also takes the
   glyph classes that HarfBuzz gives from the Unicode categories. The
   system fonts of Android have GPOS and GDEF tables.

   Indic shaping is done: `std/shape` follows the Indic shaper of HarfBuzz
   for Devanagari, Bengali, Gurmukhi, Gujarati, Oriya, Tamil, Telugu,
   Kannada and Malayalam. It finds the syllables, the base consonant and
   the reph. It reorders the matras, and applies the Indic features in
   their stages. The rules of the new and of the old Indic specification
   both apply, with the configuration of HarfBuzz for each script.

   The Indic shaper puts dotted circles where HarfBuzz puts them. The
   glyphs, the clusters and the positions are those of HarfBuzz 15, for
   the test words and for random strings: more than 200,000 in
   Devanagari and 12,000 in each other script. `std/android/text` shapes
   each run of an Indic script.

   Hebrew, Thai and Lao shaping is done, with the shapers of HarfBuzz for
   them. A Hebrew run goes from right to left, and a bracket in it takes
   the glyph of its mirror. Hebrew sorts its points as HarfBuzz sorts
   them, and composes the presentation forms for a font with no mark
   positioning. Thai and Lao split the sara am. A Thai font with no Thai
   GSUB script uses the glyphs of the private use area that old fonts
   have. The glyphs, the clusters and the positions are those of HarfBuzz
   15 for more than 8,000 random strings in each script.

   Khmer shaping is done, with the Khmer shaper of HarfBuzz. A coeng and a
   Ro, and a pre-base vowel, move before the base, and the split vowels
   split. The glyphs, the clusters and the positions are those of
   HarfBuzz 15 for more than 30,000 random strings, also with the feature
   `cfar`.

   Myanmar shaping is done, with the Myanmar shaper of HarfBuzz. The
   kinzi, the medial Ra and the pre-base vowels take their positions, and
   a font with the old tag `mymr` takes the default shaper. The glyphs,
   the clusters and the positions are those of HarfBuzz 15 for more than
   30,000 random strings.

   The Universal Shaping Engine of HarfBuzz is done, for Sinhala, Tibetan,
   Javanese, Balinese, Mongolian, N'Ko and the other scripts that HarfBuzz
   gives it. `scripts/gen-use-table.py` writes its table of categories, and
   the table is that of HarfBuzz 15 for each code point. The glyphs, the
   clusters and the positions are those of HarfBuzz 15 for Sinhala,
   Balinese, Javanese, Tibetan, Mongolian and N'Ko.

   The Hangul shaper of HarfBuzz is done. The jamo of Korean compose into
   the syllables that the font has, or get the features `ljmo`, `vjmo` and
   `tjmo`. The tone marks move before their syllable. The glyphs, the
   clusters and the positions are those of HarfBuzz 15 for more than 19,000
   random strings in Noto Sans CJK KR.

   Color emoji are done: `std/truetype` draws the glyphs of COLR
   versions 0 and 1, and `std/android/text` and the painter of
   `std/android/ui` draw them in their colors.

   Arabic shaping is done: `std/shape` gives each letter its joining
   form, and applies the GSUB and GPOS lookups of the font in the stages
   of HarfBuzz. The marks attach to their bases with the anchors of the
   font.

   Characters decompose and compose as in HarfBuzz. A ZWJ and a
   ZWNJ stop a lookup where they stop it in HarfBuzz. The glyphs and their
   positions are those of HarfBuzz, for the test words and for 20,000
   random strings. `std/android/text` shapes each run of Arabic letters.

   CFF glyphs are done:
   `std/truetype` reads CFF outlines, CID-keyed ones too, and the fonts
   of a collection. So the CJK fonts of the system draw.

   Right-to-left text is done: `std/bidi` passes all the tests of UAX #9,
   and the labels and the fields of `std/android/ui` show each line in
   its order. The layout mirrors when the language of the person is
   right to left.

   Fallback fonts are done: the font matcher of the system, or
   `fonts.xml` before Android 10, gives the font for each character.
   Localized strings are done: `android.languages` gives
   the languages of the person, and `std/i18n` chooses the texts and
   their plural forms.

8. **Developer tools.** A debugger workflow with lldb, and then a reload
   of the view and update code with the model kept. `-apk-run` already
   builds the ABI of the device only.
