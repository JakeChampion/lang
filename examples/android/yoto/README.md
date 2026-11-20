# Story Deck for Android

Story Deck is an independent Fern companion for Yoto players. The family library
shows cover art for purchased cards, linked cards, Make Your Own cards, saved
podcasts, and saved radio. Select an online player to play a title and see
its current track. Select **This phone** to stream audio on your Android device.
You can pause, resume, or stop playback from Story Deck.

## Browse and search

- Scroll through the large covers in **Library** to see the titles available to
  your family. Use the tabs to filter cards, Make Your Own, podcasts, or radio.
- Use the search box to find titles, authors, narrators, chapters, tracks, and
  episodes. Story Deck reads track information in the background and shows the
  progress. The search covers the information that Yoto returns for your library.
- Tap a cover to open its card and scroll through its tracks. Select **Play**
  beside a track to start there when Yoto provides its playback keys.
- Use **Refresh** to update the library and retry the track index.

## Listen offline

- While online, open a card and select **Download to phone**. To save several
  titles, open **Downloads** and select **Download all cards**.
- Wait for the cards to appear in **Downloads**. Test playback with airplane mode
  on before you travel.
- Open **Downloads** to play the saved audio without a connection. To delete a
  download, open its track list and select **Remove download**.

Story Deck can save cards when Yoto gives the signed-in account complete audio
files. Live streams, previews, cards with subscription access, and cards marked
`onlineOnly` are unavailable for download.

## Change player settings

Open **Players** and select **Settings** next to a player. Edit the day and night
display brightness, nightlight color, volume limits, or schedule. The available
controls depend on the player. You can also set Bluetooth preferences, when the
display dims, automatic power-off, clock format, alarms, and right button
shortcuts.
Select **Save** to send the changes to Yoto.

Enable `family:devices:manage` for the public client. If Story Deck prompts you to
reconnect, sign in again to grant the new permission.

## Connect an account

1. Create a **Public Client** in the [Yoto developer dashboard](https://dashboard.yoto.dev/).
2. Add `http://127.0.0.1:8787/callback` as a redirect URL for the client.
3. Enter the public client ID in Story Deck and tap **Connect to Yoto**.
4. Sign in and grant access in the browser. Return to Story Deck when the
   browser receives the sign-in response.

Enable `family:library:view` for the public client to read the family library.
Story Deck also requests access to Make Your Own cards and player playback.
Sign in once after an upgrade to grant `offline_access`. Story Deck uses
Android Keystore to save the refresh token on this device. It restores the
session when you open the app. Use **Sign out** to clear the saved session, the
library, and downloads.

## Build and test

Run these commands from the repository root.

```sh
make build
./bin/fern -apk story-deck.apk examples/android/yoto/app.fern
adb install -r story-deck.apk
go test ./internal/testing/e2e -run '^TestAndroidStoryDeck'
```

The host tests cover OAuth callback validation, library navigation, track search,
download recovery, and player settings in the UI simulator. Live downloads and
player settings require a registered public client and access to a Yoto account.

- [Yoto public-client sign-in](https://yoto.dev/authentication/headless-cli-auth/)
- [Yoto library permissions](https://yoto.dev/authentication/scopes/)
- [Yoto player and playback commands](https://yoto.dev/players-mqtt/mqtt-docs/)
- [Yoto player status](https://yoto.dev/players-mqtt/getting-player-status/)
- [Yoto content and playable URLs](https://yoto.dev/api/content/getcontent/)
- [Yoto device configuration](https://yoto.dev/api/devices/updatedeviceconfig/)
