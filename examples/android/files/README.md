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
- A long press starts a selection. The person can copy, move or delete
  the selection. The app pastes into the folder that shows, also from the phone
  to a server and back.
- A copy runs in steps of 1 MiB. The screen shows its progress, and a
  button stops it.
- Delete needs a second tap.
- A file has a screen with its size and its date. The app can show the
  first 64 KiB of a file as text.

Android must give the app access to all files. The home screen has a
button that opens that setting.

## SMB

`smb.fern` is an SMB 2 client over TCP. It signs in with NTLMv2
(`ntlm.fern`, `md4.fern`) and signs each message when the server asks for
that. Each operation makes its own connection, because a worker is a
process of its own and cannot share a socket.

The person gives the address of the server and the name of the share. The
app keeps the servers in a file that a key of the Android Keystore seals
(`vault.fern`).

The limits are:

- A server that accepts only SMB 3 gets a note, because the app does not
  speak that dialect.
- The app does not list the shares of a server.
- The app does not verify the signature of a reply.
- The password field shows its text.

## Tests

`protocol_test.fern` checks MD4 and NTLMv2 against their specifications,
reads a directory listing, and copies a directory in steps.
`files_test.fern` runs the app on the simulator of `std/android/ui`, with a
temporary directory as the storage of the phone.

```sh
fern -apk files.apk examples/android/files/app.fern
```
