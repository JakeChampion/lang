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

Phone playback requires an internet connection. Story Deck uses the audio URLs
that Yoto gives to the signed-in account for the selected card.

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
session when you open the app. Use **Sign out** to clear the saved session and
the library.

## Build and test

Run these commands from the repository root.

```sh
make build
./bin/fern -apk story-deck.apk examples/android/yoto/app.fern
adb install -r story-deck.apk
go test ./internal/testing/e2e -run '^TestAndroidStoryDeck'
```

The host tests cover OAuth callback validation, library navigation, track search,
and playback in the UI simulator. Live playback requires a registered public
client and access to a Yoto account.

- [Yoto public-client sign-in](https://yoto.dev/authentication/headless-cli-auth/)
- [Yoto library permissions](https://yoto.dev/authentication/scopes/)
- [Yoto player and playback commands](https://yoto.dev/players-mqtt/mqtt-docs/)
- [Yoto player status](https://yoto.dev/players-mqtt/getting-player-status/)
- [Yoto content and playable URLs](https://yoto.dev/api/content/getcontent/)
