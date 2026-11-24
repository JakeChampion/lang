# Press: make images smaller

Press converts and compresses images on the phone. A person opens an
image and chooses a format, a quality and a size. The app shows the result
beside the original before the person saves it. The image does not leave
the phone.

## Formats

The encoders of JPEG, PNG, QOI, GIF and lossless JPEG XL are in the
standard library: `std/jpeg`, `std/png`, `std/qoi`, `std/gif` and
`std/jxl`. They are written in Fern.
WebP comes from the encoder of the phone, through `image.encode` of
`std/android/image`. WebP can be lossy or lossless.

GIF has a palette of 2 to 256 colours. With the Dither box, the encoder
spreads the error of each pixel to its neighbours. The gallery of Android
does not show QOI or JPEG XL, so Press saves those files to
`Download/Press`.

A slider sets the quality from 1 to 100. A second slider sets the size from
5% to 100% of the sides. A Lanczos filter with three lobes resizes the
image before the encoder. For JPEG, a box keeps all the chroma samples
(4:4:4). Without it, a block of 2 by 2 pixels has one chroma sample (4:2:0).

## Animations

Press opens an animated GIF or WebP on Android 12 (API level 31) and later.
PNG writes an animated PNG (APNG), WebP an animated WebP, and GIF an
animated GIF, with the delay of each frame. JPEG, QOI and JPEG XL have no
animation here, so Press keeps the first frame. Press keeps at most 192 MB
of frames.

`std/webp` puts the still WebP files that the phone encodes for the frames
in one animated file.

## Comparison, save and share

The picture shows the original left of a white line and the result right
of it. A finger on the picture moves the line. Under the picture, the app
shows the size of each file and how much smaller the result is.

Save puts the file in `Pictures/Press` through the MediaStore, so the app
needs no permission. A QOI or JPEG XL file goes to `Download/Press`. Share
saves the file and then opens the share sheet of Android.

## Work beside the screen

A worker of `std/worker` resizes and encodes the image, so the screen does
not stop during the work. The phone encodes WebP on the loop thread,
because a worker cannot call C functions. The app starts a new encoder run
when the settings stay the same for two ticks of 120 ms.

## Files

- `press.fern` is the program, with no Android code: the `Model`, the
  `Msg`, the `view`, the `update`, the jobs of the workers, and `effects`.
  A `Device` gives the functions that need the phone.
- `pixels.fern` resizes images, removes and puts back the alpha, and makes
  the picture that compares two images.
- `device.fern` opens the document picker of Android, reads the chosen
  image, and saves and shares files.
- `app.fern` is the NativeActivity. Its `MainActivity` class gets the
  result of the picker in `onActivityResult`.
- `press_test.fern` runs the program on the host with
  `std/android/ui/sim` and a fake device.

Build and run it on a phone or an emulator with Android 11 (API level 30)
or later:

```sh
fern -apk press.apk -apk-run examples/android/press/app.fern
```

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-press.apk` from `main`. Each nightly uses the same key, so it installs over the one before it.

## Tests

`internal/testing/e2e/android_press_test.go` runs two checks:

- `TestAndroidPressProgram` runs `press_test.fern`. With `PRESS_UI_PNG`
  set to a directory, the test writes the screens there as PNG files.
- `TestAndroidPressApk` builds the APK and checks its entries.

The Android CI lane runs `emulator_check.py`. It taps the button that
opens the picker, and requires the picker of Android. Then it presses the
back key, and requires that the app gets the empty result of the picker.
