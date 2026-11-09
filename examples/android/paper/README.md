# Paper for Android

Paper is a minimal home screen. It shows the date, the time and the
weather. It also shows a music button and your home apps, as lines of
text over an illustrated wallpaper. The app is in Fern only. It is a NativeActivity app, and
`fern -apk` writes its APK with no Android SDK.

## What it does

| Gesture | Result |
| --- | --- |
| Swipe up, left or right | Opens the app list, with the search field ready. Paper is in the list too. |
| Tap or drag a letter at either edge of the app list | Goes to the apps that start with that letter. |
| Type | Filters the list. When one app matches, it opens. |
| Enter | Opens the first match, or searches the web when no app matches. |
| Swipe down | Opens the notification shade. |
| Long-press the wallpaper | Opens the settings: the home apps, the music app, the notification filter, °F or °C, and the hidden apps. |
| Long-press an app | Add to home, move, rename, hide, block for a while, pause before opening, app info, uninstall. |
| Tap the time or the date | Opens the clock app or the calendar. |
| Tap the weather | Asks for the location, or gets the weather again. |
| Tap the round button | Opens the music app. Long-press it to choose the app. |

- The home screen holds any number of apps. The list scrolls when it is
  longer than the space under the ridge.
- "Rename" gives an app a name of your choice, such as "Email" or "Web
  browser". A search finds an app by your name and by its own name.
- "Pause before opening" shows a calm screen for 5 seconds before the app
  opens.
- "Block for a while" closes an app for 15 minutes, 1 hour, 4 hours or the
  rest of the day.
- In the dark theme of the system, the paper is dark and the ink is light.
  The sky of the wallpaper above the ridge takes the dark paper.

Paper also does these things:

- The notification filter takes notifications out of the shade, except
  from the apps that you allow. The home screen shows how many it holds.
  Calls, alarms, navigation and media always come through.
- The weather comes from Open-Meteo, which needs no key. Paper asks only
  for the approximate location.
- The home key goes back to the home screen, with Android's home-screen
  transitions.

## Where the code is

| file | what it does |
| --- | --- |
| `paper.fern` | The rules: the names and the search, the settings file, the blocks, the notification filter, the weather reply and the text of the clock. Every function is pure. |
| `face.fern` | The text: Roboto in five weights, which `std/truetype` draws at the size of each piece of text. |
| `ui.fern` | The look of each screen, with the sizes of Android views in dp and sp. `nodes` lays out a screen, `raster` draws one node into an image, `render` draws a screen on the CPU, and `hit` finds the node under a touch. |
| `gpu.fern` | The pixels on the GPU, through `std/android/gl`. Each node becomes an image once, and each frame draws the images at their places. |
| `control.fern` | The behaviour on each event: the touches, the keys, the timers and the files. Each input and output goes through a `Device`. |
| `app.fern` | The NativeActivity, the `Device` of the phone through JNI, and the three Java classes of the app: the activity, a task that sets the system bars, and the notification listener. |
| `paper_test.fern`, `ui_test.fern`, `control_test.fern` | The tests of the rules, of each screen and of the behaviour, on the host. |
| `gpu_test.fern` | The test of the GPU painter against `render`, with OpenGL ES 2 on the host. |
| `emulator_check.py` | Runs the APK on an emulator. |
| `assets/` | The wallpaper, which `std/jpeg` decodes, and the Roboto fonts, with their licence. |

## Build

```sh
make build
./bin/fern -apk paper.apk examples/android/paper/app.fern
adb install -r paper.apk
```

Then open Settings > Apps > Default apps > Home app, and select Paper.

The `[android]` table of `fern.toml` gives the settings of the APK. `home = true` makes the activity a home screen. `assets` puts the wallpaper and the fonts in the APK, where `android.asset` reads them.

## Tests

`internal/testing/e2e/android_paper_test.go` runs four checks:

- `TestAndroidPaperModel` runs `paper_test.fern`.
- `TestAndroidPaperUI` runs `ui_test.fern`. With `PAPER_UI_PNG` set to a
  directory, the test writes each screen there as a PNG file.
- `TestAndroidPaperControl` runs `control_test.fern`.
- `TestAndroidPaperApk` builds the APK and checks its entries, its
  manifest and the exports of each library.

The Android CI lane runs `emulator_check.py`. It makes Paper the home app
and opens the app list. Then it searches for Paper and opens it, and it
requires no crash in logcat.

The wallpaper art is by @codex_401. Roboto is under the SIL Open Font
License, in `assets/OFL.txt`.
