# Story Deck for Android

Story Deck is an independent Fern companion for Yoto players. The family library
shows cover art for purchased cards, linked cards, Make Your Own cards, saved
podcasts, and saved radio. Select an online player to play a title, pause, resume,
or stop playback.

## Browse and search

- Open **Library** to see the titles available to your family. Use the tabs to
  filter cards, Make Your Own, podcasts, or radio.
- Use the search box to find titles, authors, narrators, chapters, tracks, and
  episodes. Story Deck reads track information in the background and shows the
  progress. The search covers the information that Yoto returns for your library.
- Tap a result to open its card. Select **Play** beside a track to start at that
  track when Yoto provides its playback keys.
- Use **Refresh** to update the library and retry the track index.

## Connect an account

1. Create a **Public Client** in the [Yoto developer dashboard](https://dashboard.yoto.dev/).
2. Add `http://127.0.0.1:8787/callback` as a redirect URL for the client.
3. Enter the public client ID in Story Deck and tap **Connect to Yoto**.
4. Sign in and grant access in the browser. Return to Story Deck when the
   browser receives the sign-in response.

Enable `family:library:view` for the public client to read the family library.
Sign out and connect again to grant this permission after an upgrade. Story
Deck also requests access to Make Your Own cards and player playback. It keeps
the access token in memory for the session and saves the public client ID on
the device. Use **Sign out** to clear the session and the library.

## Build and test

Run these commands from the repository root.

```sh
make build
./bin/fern -apk story-deck.apk examples/android/yoto/app.fern
adb install -r story-deck.apk
go test ./internal/testing/e2e -run '^TestAndroidStoryDeck'
```

The host tests cover OAuth callback validation, library data, track search,
and playback requests in the UI simulator. Live sign-in and playback require a
registered public client and access to a Yoto player.

- [Yoto public-client sign-in](https://yoto.dev/authentication/headless-cli-auth/)
- [Yoto library permissions](https://yoto.dev/authentication/scopes/)
- [Yoto player and playback commands](https://yoto.dev/players-mqtt/mqtt-docs/)
