# A push-to-talk walkie-talkie for Android

This is the walkie-talkie app of issue #14080: two phones, half-duplex
push-to-talk, an offline mode over Wi-Fi Direct and an internet mode through
rooms. It is a NativeActivity app with no Java source, and `fern -apk`
writes its APK.

## The LAN build

`app.fern` is a first build for two phones on the same Wi-Fi network. It
sends raw 16-bit audio at 16 kHz, about 256 kbit/s, until the Opus encoder is
ready. Wi-Fi Direct and the internet rooms come after it.

Build and install the APK:

```sh
fern -apk walkie.apk -apk-package dev.fern.walkie -apk-label "Fern Walkie" \
     -apk-permission RECORD_AUDIO,INTERNET,ACCESS_WIFI_STATE,ACCESS_NETWORK_STATE \
     -apk-min-sdk 28 examples/android/walkie/app.fern
adb install walkie.apk
```

Use it:

1. Connect both phones to the same Wi-Fi network, and open the app on each.
   Allow the microphone when the app asks.
2. Each phone sends a UDP beacon to the broadcast address on port 47120. When
   the phones find each other, they make keys and show a six-digit code.
3. Compare the codes. Tap SAME on each phone when they are the same. Tap
   DIFFERENT when they are not: then a third device can be in the middle.
4. Hold the button to talk. The other phone shows LISTENING and plays the
   voice. Only one phone can talk at a time.
5. When the connection is lost, tap the button to search again.

To test discovery and the code check on one phone, build the app a second
time with another `-apk-package` and `-apk-label`, and install both. The
second copy opens port 47121, and each copy sends its beacons to both ports.
Only the app in front gets the microphone, so this does not test voice.

A network that isolates its clients, such as many guest and public
networks, blocks the beacons. A phone hotspot works: connect the other phone
to it. Logs go to logcat with the tag `fern`.

The app is in layers. Each layer has its own tests on the host.

## `protocol.fern`

The control messages of the protocol, and their byte form.

## `session.fern`

The session engine. It holds the state machine and the push-to-talk floor,
and it does not know the transport. `step` takes the session, one event and
the time, and gives the next session and its actions. `session_test.fern`
runs two engines on a simulated link.

## `lan.fern`, `pcm.fern` and `screen.fern`

The LAN transport: the datagram format, the beacons, and the key exchange
with a resend of each message that has no answer. The raw audio: 16-bit
samples and a linear resampler between the rate of the device and 16 kHz.
The layout of the screen. `lan_test.fern`, `pcm_test.fern` and
`screen_test.fern` test them.

## `secure.fern`

The secure channel. Its key exchange uses ephemeral X25519 keys, and the
initiator commits to its key before it sees the other key. The two users then
compare a six-digit authentication string.

Control frames and voice frames have their own keys in each direction, and
ChaCha20-Poly1305 seals them. A voice frame opens once, inside a window of 64
sequence numbers.

Channel copies share their counters, replay window and key slots. Use one
event loop for all operations on a channel. Closing a channel invalidates
every copy. The counter stops before signed overflow.

Call `keys` once after the exchange. It clears the ephemeral private key.
Compare the bundle's `sas` with the peer before calling `channel`. Call
`close_keys` if the comparison fails. Call `close_handshake` on a cancelled
exchange and `close` when the session ends. Do not retain raw secret arrays
outside these handles.

## The floor

One peer is the floor controller: the Wi-Fi Direct group owner, or the first
peer in a room. The controller decides each floor request in the order it
arrives, and the first valid request wins. A grant has a lease of 3 seconds,
and the speaker renews it each second while the button is held. So a
crashed speaker cannot keep the floor.
