# Tune

A music player for Android with no Java code and no codec of the system.
The standard library of Fern decodes each file, and AAudio plays the
samples. The app has a library with a search field. The track that plays has a
screen of its own, with a bar that goes to a time.

The player reads WAV, FLAC and MP3 files in mono or in stereo. It looks
for them in the `music` directory of the private files of the app. It also
looks in the `Music` and `Download` directories of the phone, and in each
directory below them. Android asks the person for the permission to read the music
of the phone.

The library has three lists: the songs, the artists and the albums. An
artist shows its albums, and an album shows its songs in the order of their
numbers.

`std/audio/tags` reads the title, the artist, the album and the
number from each file: an ID3 tag of an MP3 file, the Vorbis comments of a
FLAC file, or the INFO list of a WAV file. A file with no tag takes them
from its path: `Artist/Album/03 Title.flac`.

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

The player decodes on the loop thread of the app, between two frames. A
frame that takes longer than the sound that the stream holds makes a gap in
the sound. The sound stops when Android stops the activity, because the app
has no service.

The player keeps the whole file of the track in memory. It reads each
whole file of the library one time, a file at each tick, for its tags and
its length. It keeps no list of the library between two starts.
