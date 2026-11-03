# A push-to-talk walkie-talkie for Android

This is the walkie-talkie app of issue #14080: two phones, half-duplex
push-to-talk, an offline mode over Wi-Fi Direct and an internet mode through
rooms. It is a NativeActivity app with no Java source, and `fern -apk`
writes its APK.

The app is in layers. Each layer has its own tests on the host.

- `protocol.fern`: the control messages and their byte form.
- `session.fern`: the session engine. It holds the state machine and the
  push-to-talk floor, and it does not know the transport. `step` takes the
  session, one event and the time, and gives the next session and its
  actions. `session_test.fern` runs two engines on a simulated link.
- `secure.fern`: the secure channel. Its key exchange uses ephemeral X25519
  keys, and the initiator commits to its key before it sees the other key.
  The two users then compare a six-digit authentication string.
- In `secure.fern`, control frames and voice frames have their own keys in
  each direction, and ChaCha20-Poly1305 seals them. A voice frame opens once,
  inside a window of 64 sequence numbers.

One peer is the floor controller: the Wi-Fi Direct group owner, or the first
peer in a room. The controller decides each floor request in the order it
arrives, and the first valid request wins. A grant has a lease of 3 seconds,
and the speaker renews it each second while the button is held. So a
crashed speaker cannot keep the floor.
