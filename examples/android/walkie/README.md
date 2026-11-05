# A push-to-talk walkie-talkie for Android

This is the walkie-talkie app of issue #14080: two phones, half-duplex
push-to-talk, an offline mode over Wi-Fi Direct and an internet mode through
rooms. It is a NativeActivity app with no Java source, and `fern -apk`
writes its APK.

## The app

`app.fern` connects two phones in three ways: on the same Wi-Fi network, over
Wi-Fi Direct, and in a room of the room server. It sends Opus voice at
16 kHz, about 24 kbit/s. A phone with an older build of the app refuses the
session, because the older build sends raw audio.

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-walkie.apk` from `main`. Each nightly uses the same key, so it installs over the one before it.

Build and install the APK:

```sh
fern -apk walkie.apk -apk-package dev.fern.walkie -apk-label "Fern Walkie" \
     -apk-permission RECORD_AUDIO,INTERNET,ACCESS_WIFI_STATE,ACCESS_NETWORK_STATE,FOREGROUND_SERVICE,FOREGROUND_SERVICE_MICROPHONE,POST_NOTIFICATIONS,CHANGE_WIFI_STATE,NEARBY_WIFI_DEVICES,ACCESS_FINE_LOCATION,WAKE_LOCK \
     -apk-min-sdk 28 examples/android/walkie/app.fern
adb install walkie.apk
```

Use it:

1. Connect both phones to the same Wi-Fi network, and open the app on each.
   Allow the microphone when the app asks.
2. Each phone sends a UDP beacon to the broadcast address on port 47120. When
   the phones find each other, they make keys and show a six-digit code.
3. Compare the codes, and tap MATCH on each phone when they are the same. Tap
   NO MATCH when they are not: then a third device can be in the middle.
4. The first phone to confirm shows WAITING until the other phone confirms.
5. Hold the button to talk. The other phone shows LISTENING and plays the
   voice on its loudspeaker, at the media volume. Only one phone can talk at
   a time.
6. When the connection is lost, tap the button to search again.

With no shared Wi-Fi network, the phones connect over Wi-Fi Direct:

1. Open the app on each phone, and allow the nearby devices when the app
   asks. Before Android 13, the app asks for the location instead.
2. Each phone lists the other phones that it finds, under the status line.
   Tap the other phone on one of them.
3. The other phone asks its user to accept the invitation. Accept it.
4. The two phones then make a group. They send the beacons on the group
   too, and the code check and the talk button work as on a Wi-Fi network.

When the phones are on different networks, they meet in a room on the room
server. The app shows the room buttons when its build names a server (see
`server_host.fern`).

1. On one phone, tap NEW ROOM. The phone shows the code of the room in its
   title.
2. Tell the code to the other user. On the other phone, tap JOIN ROOM, type
   the code on the keypad, and tap JOIN.
3. The phones find each other through the relay of the server. The code
   check and the talk button work as on a Wi-Fi network.

While the phone searches, the MODE button at the top right opens the
settings. They choose the way to find the other phone:

- AUTOMATIC uses all three ways at once. It is the default.
- SAME WI-FI uses only the beacons on the Wi-Fi network.
- WI-FI DIRECT uses only Wi-Fi Direct. The phone sends no beacon on the
  Wi-Fi network, and it takes datagrams only from its group.
- INTERNET ROOM uses only the rooms of the server. The phone sends and takes
  nothing until it is in a room. With no server, the phone cannot choose it.

Below the modes, the SERVER card shows the room server. Tap it to type the
host name or the IPv4 address of your own server, and tap SAVE. CLR and SAVE
select the server of the build again.

PHONE KEYBOARD opens the keyboard of the phone, and ENTER on it saves. A keyboard
that sends words and not keys, as in some swipe modes, types nothing: use the
keys on the screen then. The server must serve the room port of
`rooms.fern`.

A new mode starts the search again. The app keeps the mode and the server in
its private files, so the next start uses them. A test of one way does not need the other
ways turned off on the phone.

BACK and HOME move the app to the background. The foreground service keeps
the connection, and the phone still hears the other phone, also with the
screen off. A wake lock and a Wi-Fi lock keep the CPU and the radio awake
while the service runs. DISCONNECT in the notification ends the session.

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
with the freezer of cached apps off. It taps MATCH in each copy, holds the
talk button of each, presses BACK in one copy, and reads the result in
logcat.

## `protocol.fern`

The control messages of the protocol, and their byte form.

## `session.fern`

The session engine. It holds the state machine and the push-to-talk floor,
and it does not know the transport. `step` takes the session, one event and
the time, and gives the next session and its actions. `session_test.fern`
runs two engines on a simulated link.

## `ui.fern`

The look of the screen: a black background, white and grey type, and red only
for what is live. Titles, button labels and the code use the dot font of
`matrix.fern`, 5 by 7 round dots for each character. The round talk button is
a ring of dots. It fills red while the phone talks, and white while it
listens. A dot beside the status has the colour of the state. The code to
compare shows in two groups of three digits, over a MATCH pill and a NO MATCH
outline.

`ui.fern` draws a view of the app on a frame and does no other I/O.
`ui_test.fern` draws each screen on a frame in memory and checks the colours
of its buttons. With `WALKIE_UI_PNG` set to a directory, it also writes each
screen there as a PNG file.

## `lan.fern`, `pcm.fern` and `screen.fern`

The LAN transport: the datagram format, the beacons, and the key exchange
with a resend of each message that has no answer. A linear resampler between
the rate of the device and 16 kHz. The layout of the screen. `lan_test.fern`,
`pcm_test.fern` and `screen_test.fern` test them.

`pcm.fern` also has a gain for the microphone. It raises quiet speech up to
8 times, and it falls at once for a loud word. It also keeps the samples
that the speaker did not take yet, for the next write.

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

The secure channel. Its key exchange is a hybrid of ephemeral X25519 and
ML-KEM-768. The initiator commits to its X25519 key before it sees the other
key, and it sends its ML-KEM encapsulation key in the first message. The
responder sends an ML-KEM ciphertext with its X25519 key. Both shared secrets
go into the key derivation, so a recording of the traffic stays secret while
one of the two problems stays hard. The two users then compare a six-digit
authentication string.

Each side fixes its values before it sees the values of the other side. So a
party in the middle cannot try keys until the two strings match. For this
reason the initiator holds the ML-KEM key pair: the side that encapsulates
chooses its value after it sees the key. The first message is 1217 bytes, so
a phone with an older build cannot pair with this build.

Each transmission gives the voice of the speaker a new key. When the
speaker presses the button, its phone offers a new X25519 public key on the
control channel. The other phone accepts with a new key of its own, and both
put the shared secret into the key chain of that direction. Then both erase
the private keys, the old chain and the key before the last. A voice frame
carries the epoch of its key, so the frames in flight still open.

The offer goes again each half second until the accept comes. The two
directions rekey on their own.

So a recording of a transmission stays secret when a phone is taken later.
A party that took the keys and only listens loses them at the next
transmission. The control keys stay the same for the session. Thus a party
that took them and changes the traffic can still take part in a rekey.

Each phone keeps an Ed25519 identity key. After the exchange, each phone
signs the transcript of the exchange with its identity key and sends the
signature in a sealed control frame. The frame also tells whether the phone
knows the identity of the other phone. When the users tap MATCH, each phone
adds the identity of the other phone to its list of known phones.

When two phones meet again and each one knows the other, they connect with
no string to compare. A phone that knows the other phone requires a valid
signature from it. When only one phone knows the other, both show the
string. So a party in the middle must have the identity key of a phone to
skip the comparison.

The app keeps the seed of the identity key and the list of known phones in
its private storage, not in the Android Keystore. So a party that can read
the files of the app can copy the identity of the phone.

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

## `rooms.fern` and `server.fern`

The server of the internet mode. `server.fern` serves the room requests over
TLS on a TCP port. It relays the voice datagrams on the UDP port of the same
number. The default port is 47130. The server needs a certificate and its
key in PEM files:

```sh
fern -o walkie-server examples/android/walkie/server.fern
./walkie-server cert.pem key.pem 47130
```

Open TCP port 47130 and UDP port 47130 in the firewall. The key can be P-256,
P-384 or Ed25519. The server cannot sign with an RSA key.

Before it accepts new clients, the server reads the two files again. A new
pair takes effect with no restart. A pair that does not load, such as a
renewal that is half written, keeps the old pair. The showcase deployment
runs the server next to its Caddy proxy, with the certificate of Caddy. See
the walkie-talkie section of `examples/showcase/deploy/README.md`.

A client sends one request line, and the server sends one reply line:

- `CREATE` opens a room. The reply is `ROOM`, the code and a token in hex.
- `JOIN` and a code adds the client to the room. The reply is `JOINED` and a
  token in hex.
- When the server refuses a request, the reply is `ERROR` and the reason.

A code has 6 characters. It has no 0, O, 1, I or L, so a user can read it to
the other user. A room with one member closes after 10 minutes. A room with
two members closes after 2 minutes with no datagram. Each address can send 10
requests in a minute, so it is slow to guess a code. A room has two members
at most.

A phone sends each datagram of the LAN transport to the relay, after the byte
248 and its token. The relay sends the datagram to the other member of the
room. The relay gets the address of each member from the token, so a phone
that changes network keeps its room. The relay cannot read the voice,
because the phones seal it with the keys of their own exchange. A relay that
changes the datagrams of the key exchange gives the two phones different
codes. The users then see that the codes are not the same.

`internet.fern` is the side of the app. It asks the server for a room over
TLS. It puts the token in front of each datagram to the relay. In a room, the
app takes a datagram only from the relay. So a phone on the same network
cannot take the place of the peer. `internet_test.fern` runs two nodes in one
room through the relay table, and one node moves to a new address.

The nightly build writes `server_host.fern` from the `WALKIE_SERVER`
repository variable. Set the variable to the DNS name of the server, and the
next nightly APK shows the room buttons. A user can also type a server on the
settings screen.

`rooms.fern` holds the room table and does no I/O. `rooms_test.fern` tests
it. `TestWalkieRelayServer` runs the server on the host. Two clients make and
join a room over TLS, and the relay sends their datagrams. Then one client
moves to a new address. Last, the test replaces the certificate and the key
while the server runs, and new clients get the new certificate.

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
