# Android content source permissions and diagnostics

The Android lane fails its content URI playback round on main and on the codec branches.
The HTTP round passes playback, pause, seek, reuse, and the expected source error.
The original content round reported only `playertest: FAIL source or surface`.
Issue #14744 tracks this failure.

Run 42527729073, job 128883232447, reproduces it on the FLAC optimization branch.
Artifact 13636494499 contains the fixture and the filtered player log.
The fixture URI is `content://media/external/video/media/1000000018`.
The artifact omits platform source errors and URI permissions.
That artifact did not identify the cause.

Run 42534394896, job 128923354847, supplies the platform cause.
MediaProvider rejects the URI because the test app has no read access.
UriGrantsManagerService also reports that the shell cannot grant this MediaStore URI through its activity intent.
The HTTP playback round still passes.

The test app now distinguishes a surface failure from a source failure.
The emulator check records unfiltered logcat, activity permissions, and the final media player state before force-stop.
A failed diagnostic command does not replace the original playback assertion.
The final force-stop also preserves an active playback failure.
If playback succeeds, a failed force-stop still fails the check.

The log includes the last 160 platform lines when playback fails.
All playback, PCM, timeline, and screenshot assertions remain in place.

Python compilation passes.
Host regression controls reproduce and prevent the loss of the playback failure during cleanup.
The test app compiles as an Android ARM64 shared library.
The event controls pass native sanitizers and WASI.
The host JNI lifecycle and exception controls pass.

Both test APKs now declare modern and legacy video read permissions.
The runners grant `READ_MEDIA_VIDEO` on API 33 and later, or `READ_EXTERNAL_STORAGE` on earlier releases.
The content fixture belongs to the shell, rather than either test app.
The runners no longer attempt the invalid intent URI grant.
Private video export and HTTP playback need no media permission.

Eight player runner controls and three video runner controls pass.
The controls check both sides of the API 33 boundary and stop before launch when a permission grant fails.
The workflow runs both control suites.
This session has no Android emulator, so CI must confirm content playback with the permission repair.
