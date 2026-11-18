# Fern for Plex

A Plex client for Android, written in Fern. Its shape follows
[Plezy](https://github.com/edde746/plezy), a Plex client in Flutter. The app
has no Java code of its own. It is a NativeActivity app, and `fern -apk`
writes its APK with no Android SDK.

The look is the dot-matrix style of the [stopwatch](../stopwatch/README.md):
a black screen, grey cards with round corners, and white and grey text.
The titles, the tabs, the buttons and the headings of the rows are in the
dot font of `../matrix.fern`. The text from the server is in the text font.
Red shows only what is live: the current tab, the progress of an item, a
request in flight, the play button and the position on the seek bar.

Browsing:

- Sign in with a PIN: the app shows a code, and you type it at
  `plex.tv/link`.
- The app finds the servers of the account. It asks every address of a
  server at once, and takes the local one, else the remote one, else the
  relay of plex.tv.
- The home screen shows the hubs of the server, such as Continue Watching
  and Recently Added, as rows of posters that scroll across.
- The Libraries tab shows the movie, show and music libraries. A library is
  a grid of posters that loads the pages of 60 items near the screen.
- The detail screen of an item shows its backdrop, poster, facts, genres
  and summary, the seasons of a show and the episodes of a season. Mark an
  item watched or unwatched there.

A finger that rests on a poster for half a second opens its menu: Play,
Mark watched or Mark unwatched, Go to show for an episode, and Cancel.

A chip at the top of a library sets its order: title, year, date added or
rating. A tap moves it to the next order. The Unwatched chip shows only the
items not watched. In the title order, a strip at the right shows the
first letters of the titles. A tap or a drag on a letter shows its first
item, and the app asks only for that page.

A local address on a private network also gets a plain HTTP address, such
as `http://100.101.102.103:32400`. It needs no name lookup. Thus it works
through a VPN such as Tailscale whose resolver refuses a `plex.direct`
name. Plezy does the same.

At each start, the app opens the saved server at once and asks all of its
addresses again. When the saved address does not answer, the app changes
to the address that answers. A request that reaches no server starts the
same race, at most once in 10 seconds.

Playback:

- Play resumes from the position on the server, and a seek goes to the
  nearest sync frame.
- The app reports the position every 10 seconds, and marks an item watched
  at its end.

- A tap on the video shows the controls over it for 4 seconds while the
  video plays on. They are back 10 seconds, play, and forward 30 seconds.
  A bar seeks as a finger drags along it, with a preview thumbnail of the
  time. A tap outside the controls hides them, and the play button pauses.

- The speed button at the top plays at 1x, 1.25x, 1.5x, 2x, 0.5x or
  0.75x. The pitch of the sound stays the same. Each tap gives the next
  speed, and the app keeps it for the next items.

An album plays its tracks in order, and the Next button goes to the next
track. Shuffle plays the tracks
that wait in a random order. Repeat plays the album again after its last
track, or plays one track again. The settings keep shuffle and repeat. The
sleep timer pauses the music after 15, 30 or 60 minutes, or stops it at the
end of the track.

The next track plays with no gap. While a track plays, a second media
player prepares the track after it. Android starts the second player at the
end of the first one. A change of shuffle, repeat, the sleep timer or the
queue prepares the next track again.

Music plays with the controls on the screen. It plays on while the app is
away, with the media controls of Android in the notification panel and on
the lock screen. On Android 13 and later, the first track asks for the
permission to post notifications. A foreground service and a wake lock keep the stream at
work while the screen is off. A video pauses when the app goes away, unless Android shows it in a
picture-in-picture window. From Android 12, a video that plays goes into
that window when the user goes home.

Back on the player of music shows the screen before, and the music plays
on. A bar above the tabs shows the track, with a button that pauses and
plays it. A tap on the bar opens the player again. Back on a video stops
it.

The back button and the back gesture of Android do what the back arrow or
the close control of the screen does. Back first closes the menu of a
poster, the keypad of a PIN, the sheet of tracks or the code of a sign-in.
Then it goes to the screen before. From a tab other than Home, it goes to
Home, and on Home it closes the app.

Each play asks Android for the audio focus, so another app that plays
stops. A call or another app that plays pauses the app. When the focus
comes back, the play goes on, unless the person paused it.

Gestures on the video:

- Two taps at the left of the video seek back 10 seconds, and two taps at
  the right seek forward 10 seconds. Each tap after that seeks again.
- A drag up or down the left half sets the brightness of the window. A drag
  on the right half sets the volume. The brightness of the system comes
  back when the video stops.

In the credits of an episode, or 20 seconds before its end, a card shows
the next episode of the season. It counts down from 10 seconds, and then
the next episode plays. Play on the card starts it at once. Cancel hides
the card, and the episode plays to its end.

In the intro or the credits of a video, a Skip button shows over it. The
app asks the server for the markers of the item when it starts to play.

The Tracks button of the player chooses the sound and the subtitles, and
the server remembers the choice.

The media player of Android plays the sound that a file marks as its
default. It does not change the sound of a stream well. So a sound that
is not the default of the file plays as a transcode, in which the server
copies the video and that sound. A play from a row of the home screen
takes the sound that the server remembers when all of its metadata comes.
A download plays that sound too.

A transcode takes its sound from the choice that the server remembers. So
a new transcode starts after the server has the choice, and the old one
stops.

The app draws text subtitles over the video itself, so the
file plays as it is. These are SubRip, WebVTT, ASS and SSA, in a file beside
the video or inside it. The server gives the text of a track inside the
file at the address of its stream. Other subtitles, such as PGS, burn into
a transcode.

The app reads the list of the decoders of the phone from Android. The
player plays the file as it is when the phone decodes its video and its
sound. When the phone decodes only the video, the server copies the video
and converts the sound. Otherwise the server transcodes the file to HLS at
the quality of the settings. A transcode is of the whole item, and the
player seeks in it, as Plezy does. While the player is paused, the app pings
the transcode so that the server keeps it.

A phone can list a decoder that the media player does not use. Thus a file
that plays as it is can have no sound. Then the app plays it again
from the same place as a transcode that converts the sound. It does this
when the player selects no sound track, or when it reports that the sound
stopped. The player shows "Direct play" or "Transcode" after the title.

The app runs on Android TV. `fern.toml` sets `tv`, so the app is on the
launcher of the TV, with `banner.png` as its banner, and it needs no touch
screen. The D-pad of the remote
moves a ring of focus to the nearest control in its direction, and the
list scrolls to show it. The centre key presses the focused control. On
the video, left and right seek as the buttons do, and the centre shows the
controls. The media keys play, pause and seek the video, and a touch hides
the ring.

On Android TV, a video that stops part of the way goes on the Watch Next
row of the home screen. The row shows its poster and its place. The row opens the
video in the app. A video that plays to its end goes off the row.

The app also has a channel row of its own on the home screen of Android
TV. It shows the items that came to the server last, from the rows of the
home screen that the server names "recent". A tap on one opens it in the
app. When the person removes the channel, the app adds it again.

The Search key of a remote opens the search with the keyboard. The
microphone of the keyboard takes the words by voice.

Download in the menu of a poster gives the file of the item to the
download manager of Android. A file that the phone does not play as it is
downloads as a transcode at the quality of the settings. The server
makes one Matroska file with H.264 video and AAC sound. The menu shows the progress, and a second tap
stops the download. A done download shows in the last row of the home
screen and plays from the phone with no server. Remove download deletes
the file.

The sheet of tracks of a download lists the tracks of its file. A file as
it is keeps each of its sound tracks, and a choice prepares the file
again with the new sound. A transcode keeps only the sound that it played. The download
also keeps the text of each text subtitle, so subtitles show with no
server. An image subtitle such as PGS does not show from a file. A
choice never stops the file to play a stream of the server.

When no server hears the place where a play stopped, or that an item
was watched, the app keeps it in a file. The server hears it when it
answers again.

Download all in the menu of a show, a season, an artist or an album
downloads each of its episodes or tracks. Downloads in the settings
opens the list of the downloads, with the state of each one. A tap on a
done download plays it, and its button removes it.

TalkBack reads each screen. The app gives the screen reader each button
by its text and each poster by its title. A double tap presses the
control, and a double tap and hold on a poster opens its menu.

Search and settings:

- Search starts when you stop typing, and shows the hubs of the results.
- Settings: the user, the server, the quality from Original to 240p or
  Auto, and sign-out. Auto starts at Original. When the stream stalls two
  times in a minute, it plays the next lower quality as a transcode from
  the same place.

The User row of the settings lists the users of the Plex Home of the
account. A user with a PIN gets a keypad. After the switch, the app uses
the token of that user and finds the servers of that user.

The texts are in English, German, French or Spanish: the first language of
the phone that the app has. A text that a language does not have shows in
English. The names of the server, such as titles, stay as they are.

Plezy also supports Jellyfin and Emby, downloads, Live TV, Watch Together
and trackers. This app does not.

## How it works

| file | what it does |
|---|---|
| `plex.fern` | The model: the requests to plex.tv and to a server, and the records in their JSON replies. Every function is pure. |
| `ui.fern` | The look. `nodes` lays out a screen as a list of nodes of `std/android/ui/scene`, each with a rectangle, what it shows, and the action of a tap. `std/android/ui/painter` draws the nodes with the GPU, and the scene module finds the node under a touch, so one layout serves both. |
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
their cards. The app keeps each poster in the cache directory of Android,
so it comes from the network once. `std/android/image` decodes them with
the image decoder of Android, and `std/android/draw` copies them into the
frame.

The painter of `std/android/ui/painter` draws each screen with OpenGL ES 2.
It keeps the image of each node on the GPU, so a scroll moves images and
draws nothing again. A change makes the nodes of the screen at once, and the
painter draws them at the next vsync. A list moves on after a swipe, along
the fling of `std/android/motion`, and a touch stops it. A device with no
OpenGL ES 2 draws with the CPU at each change.

`std/android/player` plays the stream with the media player of Android. The
player draws the video into a `SurfaceTexture` on an external texture of
`std/android/gl`. At each vsync, the painter draws the newest frame in the
middle of the window, with the controls over it. Black bars keep the shape
of the video. Until the texture holds a frame of the stream, the
video is black. A device with no external textures shows the video on the
window, which stretches it, and a tap there pauses.

## Build

```sh
make build
./bin/fern -apk fern-plex.apk -apk-package dev.fern.plex -apk-label "Fern for Plex" \
     -apk-min-sdk 30 -apk-cleartext -apk-tv -apk-banner examples/android/plex/banner.png \
     -apk-permission INTERNET,FOREGROUND_SERVICE,FOREGROUND_SERVICE_MEDIA_PLAYBACK,POST_NOTIFICATIONS,WAKE_LOCK \
     examples/android/plex/app.fern
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
- Down and the centre key of a remote open the first poster.
- After the home key sends the app away, the track plays on to its end, and
  the media session of Android shows the app.
- A link shows the video on a texture that the app draws. The 4:3 test
  pattern fills the width with black bars above and below it, and it is
  upright. A tap shows the controls over the video with no pause, and a tap
  on play pauses it. A tap on play shows the video again, upright. The back
  key stops it.
- logcat shows no crash.
