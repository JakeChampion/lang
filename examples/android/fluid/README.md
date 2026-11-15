# Fluid

Fluid is a fluid simulation that fills the screen, and nothing else. Each
finger that moves on the screen pushes coloured dye through the fluid.

It is the Android app of the web page
[JakeChampion/neo](https://github.com/JakeChampion/neo). It is a
NativeActivity with no Java code of its own. The simulation runs on the GPU,
in the GLSL shaders of the page, through OpenGL ES 3.

Build it and put it on a phone:

```sh
fern -apk fluid.apk examples/android/fluid/app.fern
adb install fluid.apk
```

`fern -apk fluid.apk -apk-run examples/android/fluid/app.fern` builds the app,
installs it, starts it, and shows its log lines.

## Controls

The button in the top right corner opens a dialog. Each row of the dialog
does one thing at a tap. A keyboard does the same things with the keys of
the page.

| Row | Key | What it does |
|---|---|---|
| Pause, or Resume | `P` | Stops the fluid, or lets it move again |
| Add a splat burst | `Space` | Adds a number of random splats |
| Save this frame | `S` | Puts a PNG picture of the fluid in `Pictures/Fluid` of the gallery |
| Kid mode | `K` | Starts or stops kid mode |
| Full screen | `F` | Hides or shows the system bars |
| Bloom | `B` | Turns the glow on or off |
| Sun rays | `R` | Turns the sun rays on or off |
| Colour cycling | `C` | Lets the colour of the dye change with the time, or keeps it |

The `?` key opens and closes the dialog. The back key, the Escape key, the
close button and a tap outside the dialog close it. The back key then closes
the app. On a window that is too low for the dialog, a drag scrolls its rows.

TalkBack reads the button and each row, with the state of each switch. A
double tap does the action of the row.

## Kid mode

Kid mode is for a phone in the hands of a small child. It takes the button
and the dialog away, hides the system bars, and keeps the screen on. The
back key does nothing.

Kid mode also pins the app to the screen. A web page cannot do that. Android
asks you before it pins the app. While the app is pinned, the Home key and
the Overview key do not leave it.

To leave kid mode, hold the faint ring in the top right corner for 3
seconds. The ring fills while you hold it. The app then takes its pin off.
A keyboard leaves with `K`. TalkBack leaves with a double tap on the ring,
because a person who uses TalkBack cannot hold a button.

## Animations of the system

When the animations of the system are off, the app starts with a still
fluid. The setting is "Remove animations", or an animator duration scale of
0. A touch on the fluid, or a burst, starts the fluid. A pause that you
chose stays.

A paused fluid uses no power: the app draws a frame only after a change.

## Touch

Each finger pushes the dye, and each finger has its own colour. A mouse
pushes the dye when it moves, with no button down. One finger at a time
uses the button or the dialog. The other fingers stay on the fluid.

Android collects the places of a finger, and gives them at the next frame
of the display. On Android 11 and later, the app asks for each place at
once, with `unbatched_touch` of `std/android`. `touch_pointers` gives the
places of each finger of an event, with their times.

The places come at times that are not the times of the frames of the
display. A frame can thus get no place, and the next frame two. So each
frame asks the path of each finger for its place at a time before the
frame, between two places of the path. A finger with a constant speed
pushes the fluid by the same amount in each frame.

The time before the frame is the lag, and 1 ms. The lag is the longest
time that the newest place was old at a frame, while the finger moved. A
finger that stops does not make the lag longer. The lag gets shorter again
when the places come sooner.

### Where the dye goes

The display shows a frame some time after the app starts it: 2 refreshes
on most phones, which is 33 ms at 60 Hz. A finger that moves 1500 pixels in
a second is 50 pixels away by then. The page puts the dye at the last place
of the finger that it knows, so the dye is behind the finger.

The app puts the dye where the finger is at the time of the display.
Android tells the app when it showed each frame, with `frame_times` of
`std/android/gl`, so the app knows that time. The app estimates the place
of the finger from the curve that fits its last places best. In the
estimate, a finger that gets slower stops, and does not go back.

`fingers_test.fern` moves a finger along a line, two waves, a circle and a
reach, on displays of 60 and 120 Hz. It measures the distance from the dye
to the finger at the time of the display, on a window of 2000 pixels. For
the slow wave at 60 Hz, the usual distance is 110 pixels with no estimate.
With the estimate, it is 11 pixels. The test requires that the estimate brings the dye
3 times nearer or more for each motion.

The `touch_prediction` setting is the part of the estimate that the app
uses. With 0, the dye goes to the place that the frame took.

`fingers.fern` has these rules, and `trail.fern` has the path of a finger.

## Speed

The fluid draws at most 2 pixels across one dp, as the page does, and the
GPU makes that picture as large as the window. The controls draw at the
resolution of the window. The `max_pixel_ratio` setting changes the limit.

The app asks for the fastest mode of the display, and draws a frame at each
refresh: 60, 90 or 120 frames in a second. Each frame does one step of the
simulation, of the time of the frame. The work of the GPU for one second
is almost the same at each rate:

- A frame of 120 Hz does half of the pressure passes of a frame of 60 Hz.
- The app makes the bloom and the sun rays at most 60 times in a second.
- The background colour is a clear of the frame, not a pass.

When more than 15 frames in 100 are late for 2 seconds, the app takes one
more refresh for each frame: a lower rate with no late frame looks better
than a higher rate with late frames. After 10 seconds with no late frame,
the app tries the faster pace again.

After a late frame, Android can keep one frame more in its queue than it
needs. Each frame then shows one refresh later. The app measures the time
from a frame to the display. When that time is longer than the best that
the app measured, the app leaves one refresh with no frame. The queue then
has one frame less. `pace.fern` has these rules.

The loop of the app has the priority of a thread that draws for the
display.

Each pass that writes each pixel of its target tells the GPU so. A phone
GPU draws in tiles, and it then does not load the old pixels of the target.

The dialog shows the frame rate of the last second next to its close
button. It also shows the longest time from one frame to the next, and the
time from a frame to the display. Each 5 seconds, the app writes the same text to its log,
and what the touches were. `adb logcat -s fern` shows the lines.

## Settings

Each value of the simulation has a default. A string extra of the Intent
that starts the app sets a value, which is useful for a test:

```sh
adb shell am start -n dev.fern.fluid/android.app.NativeActivity \
  --es dye_resolution 256 --es bloom false --es back_color 101020
```

A boolean takes `true`, `false`, `1`, `0`, `yes`, `no`, `on` or `off`. A
colour takes `rrggbb`, `rgb`, or `r,g,b` with each channel from 0 to 255. A
number outside its range takes the nearest limit. `dithering_resolution`
takes the nearest power of two. The app writes a value that it cannot read
to its log, and ignores it.

| Setting | Default | Range |
|---|---|---|
| `sim_resolution` | 128 | 16 to 512 |
| `dye_resolution` | 512 | 64 to 2048 |
| `capture_resolution` (for the saved picture) | 512 | 64 to 4096 |
| `density_dissipation` | 1 | 0 to 10 |
| `velocity_dissipation` | 0.2 | 0 to 10 |
| `pressure` | 0.8 | 0 to 1 |
| `pressure_iterations` | 20 | 1 to 60 |
| `dithering_resolution` | 64 | 8 to 512 |
| `curl` (vorticity) | 30 | 0 to 200 |
| `splat_radius` | 0.25 | 0.01 to 2 |
| `splat_force` | 6000 | 0 to 20000 |
| `shading` | `true` | boolean |
| `colorful` | `true` | boolean |
| `color_update_speed` | 10 | 0 to 100 |
| `paused` | `false` | boolean |
| `back_color` | `000` | colour |
| `bloom` | `true` | boolean |
| `bloom_iterations` | 8 | 1 to 12 |
| `bloom_resolution` | 256 | 32 to 1024 |
| `bloom_intensity` | 0.4 | 0 to 2 |
| `bloom_threshold` | 0.8 | 0 to 2 |
| `bloom_soft_knee` | 0.7 | 0 to 1 |
| `sunrays` | `true` | boolean |
| `sunrays_resolution` | 196 | 32 to 1024 |
| `sunrays_weight` | 0.5 | 0 to 2 |
| `max_pixel_ratio` | 2 | 0.5 to 4 |
| `touch_prediction` | 1 | 0 to 2 |

## What is not the same as the page

| The page | The app |
|---|---|
| Two backends, WebGPU and WebGL, and a `backend` setting | One backend, OpenGL ES 3 |
| A step of 1/60 second, and a frame with no step or with two steps on some displays | One step for each frame, of the time of the frame, so the fluid moves by the same amount in each frame at 60, 90 and 120 Hz |
| The dye at the last place of the finger that the page knows | The dye at the place of the finger at the time that the display shows the frame |
| The move of the finger since the last frame, which is not the same in each frame | The move of the finger in the time of one frame, from its path |
| One colour for each touch | One colour for each finger |
| A default `dye_resolution` of 1024, and 512 on a phone | A default of 512 |
| A slower advection shader for a GPU that does not filter float textures | No such shader: each OpenGL ES 3 GPU filters half-float textures |
| A text for a browser with no WebGPU and no WebGL | A text for a device with no OpenGL ES 3, or with no half-float texture that it can draw into |

## Files

| File | Contents |
|---|---|
| `app.fern` | The activity: the window, the GPU context, the events and the system calls |
| `fluid.fern` | The behaviour: what a touch, a key and a frame do |
| `config.fern` | The values of the simulation and the reader of the settings |
| `clock.fern` | The time step of each frame |
| `fingers.fern` | The fingers on the fluid, the push of each one in a frame, and the place of its dye |
| `trail.fern` | The path of a finger, its place at one time, and the estimate of its place after the path |
| `pace.fern` | The refreshes of the display for one frame |
| `color.fern` | The colours of the dye |
| `sim.fern` | The passes of the simulation and of the display |
| `shaders.fern` | The GLSL source of each pass |
| `gles.fern` | The OpenGL ES functions that the passes call |
| `overlay.fern` | The places and the pictures of the controls |
| `screen.fern` | The frame: the fluid and the controls over it |
| `device.fern` | The JNI calls: the animation setting, the pin, the gallery. The priority of the loop |

`std/android/gl` makes the OpenGL ES context and draws the controls.
`sim.fern` draws the fluid with its own OpenGL ES calls on that context, and
`resume` of the canvas follows them.

## Tests

These tests run on the host:

- `TestAndroidFluidRules` runs `fluid_test.fern` on the host: the settings,
  the clock, and the rules of the touches, the keys, the dialog and kid
  mode.
- `TestAndroidFluidFingers` runs `fingers_test.fern` on the host: the
  estimate of the place of a finger, the lag, and the distance from the dye
  to a finger that moves.
- `TestAndroidFluidPace` runs `pace_test.fern` on the host: the pace of the
  frames at each display rate, the slower pace after late frames, and the
  queue of the frames.
- `TestAndroidFluidOverlay` runs `overlay_test.fern` on the host: the
  places of the controls and what TalkBack reads.
- `TestAndroidFluidGPU` runs `gpu_test.fern` with Mesa's OpenGL ES on the
  host: the simulation, and the controls over it.

These tests use the APK:

- `TestAndroidFluidApk` builds the APK and checks its entries.
- `emulator_check.py` drives the APK on an emulator in the Android lane of
  CI. It swipes with one finger and with two fingers.

With `FLUID_PNG` set to a directory, `overlay_test.fern` and `gpu_test.fern`
write their pictures there as PNG files.

## Credits

The simulation is from
[WebGL Fluid Simulation](https://github.com/PavelDoGreat/WebGL-Fluid-Simulation)
by Pavel Dobryakov, under the MIT license in [LICENSE](LICENSE). The GLSL
shaders of `shaders.fern` are his.
