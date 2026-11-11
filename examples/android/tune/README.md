# Tune

A music player for Android with no Java code and no codec of the system.
The standard library of Fern decodes each file, and AAudio plays the
samples. The app has a library with a search field. The track that plays has a
screen of its own, with a bar that goes to a time.

The player reads WAV, FLAC and MP3 files in mono or in stereo. Its tracks
are the files in the `music` directory of the private files of the app. At
the first start that directory is empty, so the app makes three tunes
there: it makes the samples of a melody, and writes one FLAC file and two
WAV files.

- `tune.fern` is the program: the `Model`, the `Msg`, the `view` and the
  `update`. It has no Android code. `update` puts what the device must do
  in the `wants` of the state.
- `player.fern` does what the state wants on a `Device`, which is a set of
  functions. It reads the file of a track, and gives the sound its next
  samples at each tick. `ui.Program.with_effects` runs it after each
  message.
- `engine.fern` is the decoder: it gives the sound of a file a part at a
  time, and it starts at a part of the file.
- `library.fern` finds the tracks of a directory, and `demo.fern` makes
  the melodies.
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

The player keeps the whole file of the track in memory, and opens each file
of the library at the start to read its length.
