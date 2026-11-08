# Fern for Plex

A Plex client for Android, written in Fern. Its shape follows
[Plezy](https://github.com/edde746/plezy), a Plex client in Flutter. The app
has no Java code of its own. It is a NativeActivity app, and `fern -apk`
writes its APK with no Android SDK.

Browsing:

- Sign in with a PIN: the app shows a code, and you type it at
  `plex.tv/link`.
- The app finds the servers of the account. It tries the local address of
  a server first, then the remote address, then the relay of plex.tv.
- The home screen shows the hubs of the server, such as Continue Watching
  and Recently Added, as rows of posters that scroll across.
- The Libraries tab shows the movie, show and music libraries. A library is
  a grid of posters that loads 60 items at a time as you scroll.
- The detail screen of an item shows its backdrop, poster, facts, genres
  and summary, the seasons of a show and the episodes of a season. Mark an
  item watched or unwatched there.

Playback:

- Play resumes from the position on the server. The player plays the file
  as it is when Android can decode it. Otherwise the server transcodes it
  to HLS at the quality of the settings. The app reports the position every
  10 seconds, and marks an item watched at its end. The next episode of a
  season plays after the end of an episode.
- A tap on the video pauses it and shows the controls: back 10 seconds,
  play, forward 30 seconds, and a bar to seek. Music plays with the controls
  on the screen.

Search and settings:

- Search starts when you stop typing, and shows the hubs of the results.
- Settings: the account, the server, the quality from Original to 240p, and
  sign-out.

Plezy also supports Jellyfin and Emby, downloads, Live TV, Watch Together
and trackers. This app does not.

## How it works

| file | what it does |
|---|---|
| `plex.fern` | The model: the requests to plex.tv and to a server, and the records in their JSON replies. Every function is pure. |
| `ui.fern` | The look. `nodes` lays out a screen as a list of nodes, each with a rectangle, what it shows, and the action of a tap. `render` draws the nodes and `hit` finds the node under a touch, so one layout serves both. |
| `control.fern` | The behaviour on each event: the touches, the keys, the replies, the player and the window. Each input and output goes through a `Device`. |
| `app.fern` | The NativeActivity, the `Device` of the phone, and the @jni listener of the media player. |
| `plex_test.fern` | The tests of `plex.fern`, with replies in the shape that plex.tv and a server send. |
| `control_test.fern` | Runs the app on the host against a fake plex.tv and a fake server, through a fake `Device`. |
| `emulator_check.py` | Runs the APK on an emulator against a fake Plex server on the host. |

The loop thread of the app must not wait for the network: the main thread
of Android waits for the loop on each input event. So each request runs in a
worker of `std/worker`, which is a process on Android. The loop polls the
workers on its timeouts with `poll`. One worker sends the requests to the
API, one fetches a batch of posters, and one sends the reports of playback.
The network, TLS and JSON are Fern code.

The posters come from the photo transcoder of the server at the size of
their cards. `std/android/image` decodes them with the image decoder of
Android, and `std/android/draw` copies them into the frame.

`std/android/player` plays the stream with the media player of Android. A
window has one producer of pixels at a time. So while the video plays, the
player owns the window and the app does not draw. A pause takes the video off
the window, and the app draws the controls. A play gives the window back to
the player with `Window.release_cpu`.

## Build

```sh
make build
./bin/fern -apk fern-plex.apk -apk-package dev.fern.plex -apk-label "Fern for Plex" \
     -apk-min-sdk 30 -apk-permission INTERNET -apk-cleartext examples/android/plex/app.fern
adb install -r fern-plex.apk
```

The app needs Android 11 (API 30) or later, for the image decoder of the NDK.
`-apk-cleartext` lets the media player read a server at an `http://` address
on the local network.

To use a server with no sign-in at plex.tv, give its address and a token in
the intent. The extra `play` opens an item by its key and plays it:

```sh
adb shell am start -n dev.fern.plex/android.app.NativeActivity \
    --es server http://192.168.1.10:32400 --es token TOKEN --es play 1234
```

## Tests

`internal/testing/e2e/android_plex_test.go` runs three checks:

- `TestAndroidPlexModel` runs `plex_test.fern`.
- `TestAndroidPlexControl` runs `control_test.fern`: the sign-in with a PIN,
  the connections of a server, the home screen, a library page by page, a
  search, a link, and a playback that resumes, pauses and stops.
- `TestAndroidPlexApk` builds the APK and checks its entries, the exports of
  each library and the manifest.

With `PLEX_CONTROL_PNG` set to a directory, `control_test.fern` writes each
screen there as a PNG file.

The Android CI lane runs `emulator_check.py` on an x86-64 emulator. Its fake
server answers with fixed JSON, a PNG poster, a WAV track and an H.264 test
pattern that ffmpeg writes. The check requires:

- The home screen shows the posters.
- A link plays the track to its end, reports it, and marks it watched.
- A link shows the video on the window. A tap pauses it and draws the
  controls. A tap on play shows the video on the window again. The back key
  stops it.
- logcat shows no crash.
