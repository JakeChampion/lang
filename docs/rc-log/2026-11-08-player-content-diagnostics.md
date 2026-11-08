# Android content source diagnostics

The Android lane fails its content URI playback round on main and on the codec branches.
The HTTP round passes playback, pause, seek, reuse, and the expected source error.
The content round reports only `playertest: FAIL source or surface`.
Issue #14744 tracks this failure.

Run 42527729073, job 128883232447, reproduces it on the FLAC optimization branch.
Artifact 13636494499 contains the fixture and the filtered player log.
The fixture URI is `content://media/external/video/media/1000000018`.
The artifact omits platform source errors and URI permissions.
The evidence does not yet identify the cause.

The test app now distinguishes a surface failure from a source failure.
The emulator check records unfiltered logcat, activity permissions, and the final media player state before force-stop.
A failed diagnostic command does not replace the original playback assertion.
The log includes the last 160 platform lines when playback fails.
All playback, PCM, timeline, and screenshot assertions remain in place.

Python compilation passes.
The test app compiles as an Android ARM64 shared library.
The event controls pass native sanitizers and WASI.
The host JNI lifecycle and exception controls pass.
This session has no Android emulator, so CI must supply the next device evidence.
This change collects evidence and does not claim to repair content playback.
