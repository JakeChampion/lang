# A push-to-talk walkie-talkie for Android

This is the walkie-talkie app of issue #14080: two phones, half-duplex
push-to-talk, an offline mode over Wi-Fi Direct and an internet mode through
rooms. It is a NativeActivity app with no Java source, and `fern -apk`
writes its APK.

The app is in layers. Each layer has its own tests on the host.

- `protocol.fern`: the control messages and their encoding.
- `session.fern`: the session engine. It holds the state machine and the
  push-to-talk floor, and it does not know the transport. `step` takes the
  session, one event and the time, and gives the next session and its
  actions. `session_test.fern` runs two engines on a simulated link.

One peer is the floor controller: the Wi-Fi Direct group owner, or the first
peer in a room. The controller decides each floor request in the order it
arrives, and the first valid request wins. A grant has a lease of 3 seconds,
and the speaker renews it each second while the button is held. So a
crashed speaker cannot keep the floor.
