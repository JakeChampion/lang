# A push-to-talk walkie-talkie for Android

This is the walkie-talkie app of issue #14080: two phones, half-duplex
push-to-talk, an offline mode over Wi-Fi Direct and an internet mode through
rooms. It is a NativeActivity app with no Java source, and `fern -apk`
writes its APK.

## The LAN build

`app.fern` is a first build for two phones on the same Wi-Fi network. It
sends Opus voice at 16 kHz, about 24 kbit/s. Wi-Fi Direct and the internet
rooms come after it. A phone with an older build of the app refuses the
session, because the older build sends raw audio.

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-walkie.apk` from `main`. Each nightly uses the same key, so it installs over the one before it.

Build and install the APK:

```sh
fern -apk walkie.apk -apk-package dev.fern.walkie -apk-label "Fern Walkie" \
     -apk-permission RECORD_AUDIO,INTERNET,ACCESS_WIFI_STATE,ACCESS_NETWORK_STATE,FOREGROUND_SERVICE,FOREGROUND_SERVICE_MICROPHONE,POST_NOTIFICATIONS,CHANGE_WIFI_STATE,NEARBY_WIFI_DEVICES,ACCESS_FINE_LOCATION \
     -apk-min-sdk 28 examples/android/walkie/app.fern
adb install walkie.apk
```

Use it:

1. Connect both phones to the same Wi-Fi network, and open the app on each.
   Allow the microphone when the app asks.
2. Each phone sends a UDP beacon to the broadcast address on port 47120. When
   the phones find each other, they make keys and show a six-digit code.
3. Compare the codes, and tap SAME on each phone when they are the same. Tap
   DIFFERENT when they are not: then a third device can be in the middle.
4. The first phone to confirm shows WAITING until the other phone confirms.
5. Hold the button to talk. The other phone shows LISTENING and plays the
   voice. Only one phone can talk at a time.
6. When the connection is lost, tap the button to search again.

With no shared Wi-Fi network, the phones connect over Wi-Fi Direct:

1. Open the app on each phone, and allow the nearby devices when the app
   asks. Before Android 13, the app asks for the location instead.
2. Each phone lists the other phones that it finds, under the status line.
   Tap the other phone on one of them.
3. The other phone asks its user to accept the invitation. Accept it.
4. The two phones then make a group. They send the beacons on the group
   too, and the code check and the talk button work as on a Wi-Fi network.

To test discovery and the code check on one phone, build the app a second
time with another `-apk-package` and `-apk-label`. Install both. The second
copy opens port 47121, and each copy sends its beacons to both ports. Show
both copies in split screen: Android can freeze an app that is not on the
screen, and then the other copy loses its peer.

Only one app gets the microphone, so this test does not check the sound.

A network that isolates its clients, such as many guest and public
networks, blocks the beacons. A phone hotspot works: connect the other phone
to it. Logs go to logcat with the tag `fern`.

The app is in layers. Each layer has its own tests on the host.

## `app.fern` and `node.fern`

`node.fern` is one phone without its Android parts: it joins the transport,
the key exchange, the session and the voice frames. It takes datagrams,
touches, microphone samples and the time, and it gives the datagrams to send,
the frames to play and the log lines. `app.fern` does the input and output:
the socket, AAudio, the touches and the screen.

`node_test.fern` runs two nodes on a simulated network with a clock that
steps 10 ms at a time. The network drops datagrams and delays them by random
times, so some arrive out of order. The tests pair the nodes and confirm the
code late on one side. Then the nodes talk in turn for a minute and a half,
with no loss and with 5% and 20% loss. Each node must stay connected and hear
most of the voice of the other. The two must never talk at the same time.

`emulator_check.py` runs two copies of the APK on the Android emulator in CI,
with the freezer of cached apps off. It taps SAME in each copy, holds the
talk button of each, and reads the result in logcat.

## `protocol.fern`

The control messages of the protocol, and their byte form.

## `session.fern`

The session engine. It holds the state machine and the push-to-talk floor,
and it does not know the transport. `step` takes the session, one event and
the time, and gives the next session and its actions. `session_test.fern`
runs two engines on a simulated link.

## `lan.fern`, `pcm.fern` and `screen.fern`

The LAN transport: the datagram format, the beacons, and the key exchange
with a resend of each message that has no answer. A linear resampler between
the rate of the device and 16 kHz. The layout of the screen. `lan_test.fern`,
`pcm_test.fern` and `screen_test.fern` test them.

## `voice.fern`

The voice codec. Each 20 ms frame of 320 samples at 16 kHz is one Opus packet
with one CELT frame. The encoder of `std/opus/celt_encoder` codes the bands
up to 8 kHz with the constrained VBR at 24 kbit/s. The decoder of
`std/opus/celt` gives the samples back. A packet of another codec gives no
samples.

The secure channel gives the sequence number of each voice frame. When
frames are lost, the receiver plays the concealment of the decoder for each
lost frame, up to five frames, before the next frame. A frame that arrives
after a later frame comes too late, and the receiver does not play it.
`voice_test.fern` checks the round trip, the rate, the packet format and the
concealment.

## `secure.fern`

The secure channel. Its key exchange uses ephemeral X25519 keys, and the
initiator commits to its key before it sees the other key. The two users then
compare a six-digit authentication string.

Control frames and voice frames have their own keys in each direction, and
ChaCha20-Poly1305 seals them. Both go over datagrams, so a frame can be lost
or arrive out of order. Each frame opens once, inside a window of 64
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
and the speaker renews it each half second while the button is held. So a
crashed speaker cannot keep the floor.

Each message can be lost, so each one is sent again or ends by itself:

- A side sends its Hello every half second until the peer sends a message.
  The peer then confirmed the code too. A side that waits a minute for that
  ends the session.
- A request with no reply goes again after half a second, as a new request.
- The controller sends a grant with each renewal of its own lease. When the
  lease of the last grant ends, the other side stops LISTENING, also when
  the release is lost.
- A heartbeat goes each half second. A peer that sends nothing for 5 seconds
  is lost.
