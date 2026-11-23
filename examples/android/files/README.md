# Fern Files

A file explorer for Android with no Java code. It shows the storage of the
phone and the shares of SMB servers, such as a NAS or a Windows computer.

The background is black, or a light grey in the light theme. The text is
white and grey. Red marks what is live: a button, a selected item, and a
saved server. The headings are words of round dots.

## What it does

- The home screen shows each storage with its free room, the usual folders
  of the phone, and the saved servers.
- A folder shows each directory before the files. The person can sort by
  name, by size or by date, show the hidden files, and filter by a part of
  a name.

- The person can pin a folder to the home screen. The home screen also
  shows the last 8 files that the person opened.
- A search looks in each folder below the folder that shows.
- **Sizes** measures each folder of the folder that shows, and sorts by
  size, so the person finds what fills the storage.
- The app keeps the sort order, the hidden files setting, the pins and the
  recent files from one start to the next (`prefs.fern`).

- A folder shows as a list or as a grid of tiles. An image file of the
  phone shows a small picture of itself. An image file of a server does
  not, because each small picture is a whole download.

A long press starts a selection:

- One button selects each item of the folder.
- The person can copy, move or delete the selection. Delete needs a second
  tap.
- The app pastes into the folder that shows, also from the phone to a
  server and back.
- **Zip** makes an archive of the selection in the folder that shows.

A delete on the phone moves the items to the trash, a folder at the top
of the storage. The trash restores an item to its folder, or deletes it
for good. A delete on a server removes the item at once.

A copy runs in steps of 8 MiB:

- The screen shows its progress, and a button stops it.
- A foreground service keeps the app at work while the window of the app
  is away, and its notification says what the copy does.
- A step that fails starts again from its byte, 3 times at most, after
  waits of 1.5 s, 3 s and 4.5 s. So a connection that drops for a moment
  does not end the copy.

The app does more for one file:

- A file has a screen with its size and its date. The app shows a JPEG,
  PNG, GIF, WebP or BMP file as a picture, and the first 64 KiB of each
  other file as text.
- **Open** shows the file in another app of the phone. A film or sound
  of a server plays as a stream, and each other file of a server goes to
  the cache of the app first.
- **Extract here** puts the files of a zip archive of at most 256 MiB in
  a new folder beside it (`pack.fern`).
- **Share** gives the file to an app that the person picks, such as a
  mail app.
- A file shows a picture of dots for what it holds: an image, sound, a
  film or other data.

Android must give the app access to all files. The home screen has a
button that opens that setting.

## SMB

`smb.fern` is an SMB client over TCP for the dialects from 2.0.2 to 3.1.1.
It signs in with NTLMv2 (`ntlm.fern`, `md4.fern`). `smb3.fern` has the
parts of SMB 3.

- With 3.1.1 the client encrypts each message with AES-128-GCM when the
  server has that cipher. It signs with AES-GMAC or AES-CMAC otherwise.
- With 3.0 and 3.0.2 it signs each message with AES-CMAC.
- With 2.0.2 and 2.1 it signs each message with HMAC-SHA256.
- It verifies the signature or the tag of each reply.

A server from 2.1 takes up to 4 MiB in one read or one write. The client
pays one credit for each 64 KiB. Through a link with a round trip of 4 ms,
a file of 200 MiB moves at about 80 MiB/s.

Each operation makes its own connection, because a worker is a process of
its own and cannot share a socket.

The person gives the address of the server, or picks it from a scan of
the network of the phone (`scan.fern`). The app asks the server for its
disk shares (the call NetrShareEnum on the `srvsvc` pipe), and the person
picks one or types its name. The password field shows dots. The app keeps
the servers in a file that a key of the Android Keystore seals
(`vault.fern`).

## Other apps

Android lets another app read a file only through a content provider.
`open.fern` has one: `dev.fern.files.Shared`, a @jni class that extends
`ContentProvider`. `fern -apk` puts each such class in the manifest as a
provider that is not exported, with the name of the class as its authority.
The app gives access to one URI with each intent, for reading only.

The installer of Android ignores an APK from an app that does not hold
`REQUEST_INSTALL_PACKAGES`. The app holds that permission, so Open on an
APK starts the installation.

## Streams

A player reads a film a part at a time, and moves in it. `stream.fern`
is an HTTP server on a port of the phone that answers range requests, and
reads each part of 4 MiB from the share. The app gives the player the
address of the file on that server. The address has a token that is new
for each file, so no other program finds the file. The server listens
on 127.0.0.1 only, so no other machine of the network reaches it. A new stream ends the
server of the stream before it.

## The file picker of the system

`docs.fern` is a documents provider: the file picker of Android shows
each saved server, so each app of the phone can open a file of a server.
`fern -apk` puts a @jni class that extends `DocumentsProvider` in the
manifest as a provider that only the picker can ask.

The provider gives documents for reading only. A file goes to the cache
of the app in full before the other app gets it.

## Tests

`protocol_test.fern` checks MD4 and NTLMv2 against their specifications,
reads a directory listing and a list of shares, and verifies a signature.
It also copies a directory in steps.
`files_test.fern` runs the app on the simulator of `std/android/ui`, with a
temporary directory as the storage of the phone.

`smb_live.fern` checks the SMB client against a real server, and
`stream_live.fern` serves a file of a share for a check of the stream.
`scripts/files-samba-check` runs it against Samba with each set of
dialects and signature rules, and the Android lane of CI runs that script.

`emulator_check.py` operates the APK on an Android emulator through the
accessibility tree. It reads a text file, copies a picture, deletes and
restores a file, reads the two content providers, and opens a file. The
Android lane of CI runs it.

```sh
fern -apk files.apk examples/android/files/app.fern
```
