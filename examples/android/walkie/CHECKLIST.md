# Two-phone acceptance checklist

The host tests and the emulator check of CI cannot test the radio, the
microphone or a real network. Do these checks on two phones before you close
#14086 or #14087. Record the date, the two phone models, the Android
versions and the commit of the APK.

The specification of #14080 is not in this repository. The letters B, C and
G are the letters that the issues give. The issues do not describe tests A,
D, E, F and H. The other checks come from the issues and from `README.md`.

Prepare the phones:

1. Install the same nightly `fern-walkie.apk` on both phones.
2. Connect each phone to a computer, and run `adb logcat -s fern` to read
   the log. The lines of the app start with `walkie:`.
3. Open the app on each phone, and allow the microphone and the nearby
   devices. Before Android 13, allow the location.

## Wi-Fi Direct (#14086)

Set the mode to WI-FI DIRECT on both phones.

### Find the other phone

- [ ] **Discovery.** Each phone shows the other phone under the status line
  in 30 seconds.
- [ ] **Connect and accept.** Tap the other phone on phone 1. Phone 2 asks
  to accept the invitation. Accept it. The log of each phone shows
  `Wi-Fi Direct group`. The owner shows `this phone owns it true`.
### Compare the code

- [ ] **Code.** Both phones show the same six-digit code. Tap MATCH on each
  phone. Both phones show HOLD TO TALK.
- [ ] **NO MATCH.** Connect the phones again, and tap NO MATCH on one
  phone. The session ends on both phones. No voice goes between them.

### Talk

- [ ] **Talk.** Hold the button on phone 1 and speak. Phone 2 shows LISTENING
  and plays the voice. Then talk from phone 2 to phone 1. The speech is
  clear.
- [ ] **One speaker (C).** Press the button on both phones at the same time,
  ten times. Each time, only one phone shows TALKING.

### Lose and find the peer

- [ ] **Control stream.** The log of the group owner shows
  `control stream from` and the address of the other phone.
- [ ] **Lost peer.** While phone 1 talks, turn off the Wi-Fi of phone 2.
  In 5 seconds, phone 1 stops TALKING and shows TAP TO SEARCH AGAIN.
- [ ] **Known phones.** Close the app on both phones and open it again.
  Connect again. The phones connect with no code to compare.

## The same Wi-Fi network

Set the mode to SAME WI-FI on both phones. Connect both phones to one
network that does not isolate its clients, such as a phone hotspot.

- [ ] **Beacons.** The phones find each other and show a code in 10
  seconds.
- [ ] **Talk.** The talk checks of the Wi-Fi Direct section pass.

## Internet rooms (#14087)

Use a build that names a room server. Connect phone 1 to Wi-Fi and phone 2
to mobile data, so that the phones are on different networks.

- [ ] **Room (B).** Tap NEW ROOM on phone 1. Type its code with JOIN ROOM
  on phone 2. The phones show a code, and the talk checks pass.

### A wrong code

- [ ] **Wrong code.** JOIN with a code that no room has. The phone shows
  `no room:` and the reason.
### Changes of network

- [ ] **Short network change.** While the phones are connected, turn off
  the Wi-Fi of phone 1 so that it uses mobile data. The session continues,
  and the voice continues in a few seconds.
- [ ] **Long network change.** Put phone 1 in airplane mode for 10 seconds,
  then turn airplane mode off. Each phone shows `reconnecting:`. The phones
  connect again in the same room with no code to compare. While they are not
  connected, the button sends no voice.
### Leave the room

- [ ] **Leave the room.** While a phone waits in a room, tap TAP TO LEAVE
  ROOM. The room buttons show again.

## Both modes

- [ ] **Background.** Press HOME on phone 2. Phone 2 still plays the voice
  of phone 1, also with the screen off.
- [ ] **Talk from the notification.** On phone 2 in the background, tap TALK
  in the notification. Phone 1 hears phone 2 until you tap STOP TALKING.
- [ ] **Call.** While phone 1 talks, call phone 1 from a third phone. Phone 1
  stops TALKING and shows `a call or another app took the audio`. During
  the call, a press on phone 1 shows `a call or another app has the audio`.
- [ ] **Disconnect.** Tap DISCONNECT in the notification. The session ends
  on both phones.
- [ ] **Third phone (G).** Open the app on a third phone near a connected
  pair. The third phone does not hear the pair, and the pair does not hear
  it.
