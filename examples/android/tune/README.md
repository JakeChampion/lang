# Tune

A music player for Android with no Java code and no codec of the system.
The standard library of Fern decodes each file, and AAudio plays the
samples. The app has a library with a search field. The track that plays has a
screen of its own, with a bar that goes to a time.

The player reads WAV, FLAC, MP3, M4A (AAC or ALAC), Ogg Vorbis and Opus files in
mono or in stereo. It looks
for them in the `music` directory of the private files of the app. It also
looks in the `Music` and `Download` directories of the phone, and in each
directory below them. Android asks the person for the permission to read the music
of the phone.

An album has its picture from the tag of one of its files, or from an
image file of its directory, such as `cover.jpg`. `std/audio/tags` gives the
bytes of the picture, and `std/android/image` decodes them. A list shows the
picture at a small size, and the screen of the track shows it at a large
size. An album with no picture shows rings in the colors of its name. The media
session gets the picture also, so Android shows it with the media controls
and on the lock screen.

A tap on a song makes the list that shows the queue of the player. A long
press on a song shows its menu: "Play next" and "Add to queue".

While the menu shows, a tap on a different song puts that song in the menu also.
The menu then acts on each of its songs. The menu of one song goes to its
album and to its artist, and "Details" shows its tags and its path. The screen
of the track has the "Queue" button, which shows the queue.

The screen of the track has three more buttons. "Speed" plays the track
faster or slower with the same pitch, and "Pitch" makes it higher or lower
with the same speed: `std/audio/tempo` does the two. "Sleep" stops the
player after 15, 30 or 60 minutes of sound.

A track shows its lyrics in the place of its picture. They come from
an LRC file with the name of the track, or from the tags of its file. For
lines with times, the screen shows the line that the track is at.

A track that ends goes on to the next one in the same stream of sound.
So no sound is lost between two tracks that have the same rate. With
"Loudness" of the settings at "Track", a track plays at the ReplayGain
gain of its file. So the tracks have the same loudness. At
"Album", a track plays at the gain of its album, so an album keeps its
steps of loudness. "Crossfade" fades the end of a track into the next one.

"Equalizer" of the settings has a slider for each of five bands, from
-12 to 12 dB, and the presets Flat, Bass, Vocal and Treble. The screen of
the track shows the spectrum of the sound under the cover.

The player keeps its queue, its track, the time in the track, shuffle and
repeat. At the next start, the track is there at that time, stopped.

"Edit tags" of the details of a song changes its tags: the title, the
artist, the album, the artist of the album, the number, the year and the
genre.
`std/audio/tags` writes them to an MP3 or a FLAC file, and the file keeps
its other tags. For a file of another app, Android asks the person to
let Tune change it, with `std/android/media`. The player writes the file
again when the app comes back. It tells when it cannot write a file.

Shuffle puts
the songs after the one that plays in a new order. Repeat has three
states: off, the queue again, and the track again.

The player has a media session of `std/android/session`. So Android shows
its track with the media controls of the notification panel and of the
lock screen. The buttons there play, stop, go to the next track and go to
a time. The player asks for the audio focus of `std/android/focus`: a
different app that plays stops, and the player stops for a call. A foreground service of `std/android/service` and a wake lock keep
the sound on while the window of the app is away.

The "Settings" button shows the settings of the player. "Theme" chooses
the light colors, the dark colors, or those of the system, with
`with_theme_of` of `std/android/ui`. "Scan" reads the directories again.
Each list but the songs has a button that takes its tab away. The player
keeps the settings for its next start.

"Choose" shows the directories of the phone. The person goes
into a directory and taps "Use". The library then has the files of that
folder only, and the player keeps the folder for its next start.

A folder of the list of the folders has the "Hide" button. The library
then has no file of that directory, and the scan does not read it. The
settings show each hidden folder with a button that puts it back.

The library has six lists: the songs, the albums, the artists, the
genres, the playlists and the folders. An artist shows its albums, and an
album shows its songs in the order of their numbers. A genre and a folder
show their songs. A track with more than one artist, such as `Ada; Bo` or
`Ada feat. Bo`, is under each of them.

The button next to the search field changes the order of the songs and of
the albums: that of the library, by the name, by the year, or by the
length.

The menu of a song shows "Add to playlist", and the queue shows "Save". Each
of them shows the playlists and a field for the name of a new one. The
"Favorite" button of the screen of the track puts the track in the
playlist "Favorites", or takes it out. A long press on a song of a
playlist takes it out. The player keeps the playlists in its private files.

The scan reads each M3U file that it finds as a playlist with the name of
the file. "Export" writes a playlist as an M3U file in the folder of the
library.

`std/audio/tags` reads the title, the artist, the album, the genre and
the number from each file. An MP3 file has an ID3 tag, and a FLAC or an Ogg
file has Vorbis comments. An M4A file has an `ilst` box, and a WAV file has
an INFO list. A file with no tag takes them
from its path: `Artist/Album/03 Title.flac`.

A long press on the icon of the app in the launcher shows two shortcuts,
from `std/android/shortcuts`. "Shuffle all" plays each track of the
library in a new order, and "Favorites" plays that playlist. A shortcut
also works while the app runs: the activity, `dev.fern.tune.Main`, keeps
the intent in `onNewIntent`, and the player does its action at the next
Resume.

The app has a widget for the home screen: the track that plays, with the
buttons "Prev", "Play" or "Pause", and "Next". `fern -apk` makes its
resources from the `widget` list of `fern.toml`, and `std/android/widget`
gives it its texts. A tap on the track opens the app. While the player
runs, the buttons send their commands to it. When the app closes, the
buttons start the app, and it plays from the place that it kept.

The back key does not stop a track that plays. At the top screen, it puts
the app behind the other apps, and the track plays on. Then the buttons of
the widget, of the notification and of a headset work with no window. When
no track plays, the back key closes the app.

A swipe of the app out of the recent apps destroys its activity. While a
track plays, the loop of the app lives on with no window, and the next
start of the app takes that loop over. A stop with no window ends the
loop.

When the player finds no file, it makes three tunes in its own directory:
it makes the samples of a melody, and writes one FLAC file and two WAV
files.

- `tune.fern` is the program: the `Model`, the `Msg`, the `view` and the
  `update`. It has no Android code. `update` puts what the device must do
  in the `wants` of the state.

- `player.fern` does what the state wants on a `Device`, which is a set of
  functions. It reads the file of a track, and gives the sound its next
  samples at each tick. `ui.Program.with_effects` runs it after each
  message.

- `engine.fern` is the decoder: it gives the sound of a file a part at a
  time, and it starts at a part of the file.

- `library.fern` finds the files of sound below a directory and makes the
  track of each one, and `demo.fern` makes the melodies.

- `app.fern` is the NativeActivity. It gives the player the files of the
  phone and a stream of `std/android/audio`.

- `tune_test.fern` and `engine_test.fern` run on the host with no phone.
  The device of the test plays nothing. It takes the samples of 20 ms at
  each write, so the time of a track goes on as it does on a phone.

Build and run it on a phone or an emulator:

```bash
fern -apk tune.apk -apk-run examples/android/tune/app.fern
```

Run its tests on the host:

```bash
fern -run examples/android/tune/tune_test.fern
```

## Limits

The player decodes on the loop thread of the app, between two frames, into
the ring of the stream of `std/android/audio`. The audio thread of the
device takes the samples from the ring, so a slow frame does not stop the
sound. The ring holds a second of sound: a frame that takes longer than that
makes a gap. The time and the bars of the spectrum are those of the sound
that plays, not of the samples that the decoder gave last.

The player reads a WAV, a FLAC, an MP3 and an M4A file a window of 512 KB
at a time. So a long file starts at once. The index of an M4A file comes
from its boxes alone. An Ogg track stays in memory whole, because its index
needs the whole file.

For the library, the player reads one directory or the first 128 KB of one file at a
time, for 8 ms of each tick. A file gives its tags and its length. It reads
more only for a tag that is longer, or for a tag after the sound.

The player keeps the library in its private files, so it
shows the tracks and the pictures of the albums at its next start. It then
reads the directories again: a file that is new comes in, and a track
whose file is not there leaves the library.
