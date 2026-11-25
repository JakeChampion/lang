# Keystore on a physical device

The emulator of CI has no TEE and no StrongBox. This procedure runs
`std/android/keystore` and the sealed identity of `examples/android/walkie`
on a phone. Do it on each new phone model. Do it again before a change to
the Keystore module or to the identity store of Walkie merges. Record each run
in "Device runs of the Keystore procedure" in `docs/ANDROID-STANDING.md`.

## The test app

`app.fern` is a NativeActivity app. At each start it does these steps, logs
one line for each step, and then stops:

1. `restored`: the envelope in the data directory, under the alias
   `fern.device.plain`. Android backs up this directory, but not the key.
2. `plain`: an envelope in the no-backup directory, under the same alias.
   The key has no requirements.
3. `strongbox`: an envelope under `fern.device.strongbox`. The key must be
   in StrongBox.
4. `auth`: an envelope under `fern.device.auth`. The key needs the user to
   authenticate for each use.

A line has this form:

```
keystore-device: NAME key HAD open OPENED [provision ERROR | seal SEALED]
```

- `HAD` is `true` when the alias had a key at the start of the step.
- `OPENED` is `none` when there was no envelope, `ok` when the envelope
  opened to the value that the app sealed, and otherwise the error.
- When there was no envelope, the app makes a key if the alias has none,
  and seals a new envelope. `provision` gives the error of the key. `seal`
  gives `ok` or the error of the seal.

The app does not write over an envelope. To start again from no keys and
no envelopes, clear the data of the app.

## Prepare

1. Write the phone model, the Android version, the security patch level and
   the commit of this repository in the record.
2. Connect the phone with USB debugging on. Do `adb devices` and make sure
   that it shows one device.
3. Read the hardware features of the phone:

   ```sh
   adb shell pm list features | grep -E 'hardware_keystore|strongbox_keystore'
   ```

   Write the two features and their versions in the record. A phone with no
   `android.hardware.strongbox_keystore` has no StrongBox.
4. Set a PIN as the screen lock.
5. Build the app, install it and start it:

   ```sh
   make build
   ./bin/fern -apk build/keystore.apk -apk-package dev.fern.keystoretest \
     -apk-backup -apk-run tests/android/keystore/app.fern
   ```

   To start the app again, do this:

   ```sh
   adb shell am start -W -n dev.fern.keystoretest/android.app.NativeActivity
   adb logcat -d -s fern | grep keystore-device
   ```

## Steps

Each step gives the lines that must be in the log. Copy the lines of each
step into the record.

### 1. A new key in the TEE and in StrongBox

Start the app one time after the install.

```
keystore-device: restored key false open none seal ok
keystore-device: plain key true open none seal ok
keystore-device: strongbox key false open none seal ok
keystore-device: auth key false open none seal ...
```

- The `restored` step makes the key of `fern.device.plain`. Thus the
  `plain` step finds a key.
- On a phone with no StrongBox, the `strongbox` line has `provision` and an
  error that names `StrongBoxUnavailableException`. This is the correct
  result for such a phone.
- The `auth` line has an error after `seal`, because no user authenticated
  for this use. Write the error in the record.

### 2. Open the envelopes again

Start the app again.

```
keystore-device: restored key true open ok
keystore-device: plain key true open ok
keystore-device: strongbox key true open ok
keystore-device: auth key true open none seal ...
```

On a phone with no StrongBox, the `strongbox` line is the same as in step 1.

### 3. A reboot

Do `adb reboot`. Unlock the phone with the PIN, and start the app. The lines
must be the lines of step 2.

### 4. A key that Android made invalid

1. Remove the screen lock: set it to None.
2. Start the app.

The `restored`, `plain` and `strongbox` lines must be the lines of step 2.
These keys do not need the user to authenticate.

The `auth` line must change. Android either keeps the key and refuses it, or
deletes it:

- `key true open none seal` and an error that names
  `KeyPermanentlyInvalidatedException`.
- `key false open none provision` and an error that tells that the phone
  has no secure screen lock.

Write which of the two the phone gives. Then set the PIN again.

### 5. A key that is missing after a restore

The data directory goes into a backup, and the key does not. Do these
commands. Backup must be on in the settings of the phone.

```sh
adb shell bmgr enable true
adb shell bmgr backupnow dev.fern.keystoretest
adb uninstall dev.fern.keystoretest
adb install build/keystore.apk
```

The install restores the data directory. Start the app.

```
keystore-device: restored key false open keystore: missing key
keystore-device: plain key false open none seal ok
```

If the `restored` line has `open none`, the restore did not occur. Write
that in the record, and do not record step 5 as a pass.

### 6. An upgrade of the OS

Do this step only when the phone has an OS update to install. Do steps 1
and 2 on the old version. Install the update, unlock the phone and start the
app. The lines must be the lines of step 2. When no update is available,
write "not run" in the record.

## Walkie

Walkie seals its identity seed and its list of known phones with Keystore
keys. A second phone with Walkie is necessary to make a known phone.

### 7. The upgrade from a plain identity file

Before #16609, Walkie kept its identity seed and its known phones in plain
files. Commit `9f606a99dac9a8d7d206c703359cea6b4d6eb96c` is the last
commit of `main` before that change. Use one signing key for both builds,
so that the second install is an upgrade.

1. Build the old Walkie:

   ```sh
   git worktree add ../walkie-old 9f606a99dac9a8d7d206c703359cea6b4d6eb96c
   (cd ../walkie-old && make build && ./bin/fern -apk build/walkie-old.apk \
     -apk-key ../lang/build/walkie.key.pem -apk-debuggable \
     examples/android/walkie/app.fern)
   ```

2. Uninstall Walkie from the phone. Install `walkie-old.apk`. Connect to
   the second phone and tap MATCH on both phones.
3. Close Walkie and open it again. Find this line in `adb logcat -s fern`
   and write it in the record:

   ```
   walkie: identity XXXXXXXX, 1 known phones
   ```

4. Build the current Walkie with the same key, and install it over the old
   one:

   ```sh
   ./bin/fern -apk build/walkie.apk -apk-key build/walkie.key.pem \
     -apk-debuggable examples/android/walkie/app.fern
   adb install -r build/walkie.apk
   ```

5. Open Walkie. The log must have the same `walkie: identity` line as in
   step 3, with the same eight hex digits. It must not have
   `walkie: cannot load identity` or `walkie: cannot load known phones`.
6. Connect to the second phone. The phones must connect with no code to
   compare.
7. List the files of Walkie:

   ```sh
   adb shell run-as dev.fern.walkie ls files no_backup
   ```

   `no_backup` must have `identity.sealed` and `peers.sealed`. No directory
   can have a file with the name `identity` or `peers`.

### 8. Walkie after a reboot, a key made invalid, and an OS upgrade

After steps 3, 4 and 6, open Walkie. The `walkie: identity` line must be the
line of step 7. Walkie's keys do not need the user to authenticate, so the
removal of the screen lock must not change them.

### 9. Walkie on a new phone

Android does not copy the no-backup directory or the Keystore keys to a new
phone. Move the apps to a new phone with the transfer of the setup of
Android. Open Walkie on the new phone.

- The log has a `walkie: identity` line with eight hex digits that are not
  the digits of step 7.
- The line has `0 known phones`.
- When the new phone connects to the second phone, the phones show a code
  to compare.

When no new phone is available, write "not run" in the record.
