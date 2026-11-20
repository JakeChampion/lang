# Story Deck for Android

Story Deck is an independent Fern companion for Yoto players. It shows the
players linked to your account and whether they are online. Select an online
player to play a Make Your Own card, pause, resume, or stop playback.

## Connect an account

1. Create a **Public Client** in the [Yoto developer dashboard](https://dashboard.yoto.dev/).
2. Add `http://127.0.0.1:8787/callback` as a redirect URL for the client.
3. Enter the public client ID in Story Deck and tap **Connect to Yoto**.
4. Sign in and grant access in the browser. Return to Story Deck when the
   browser receives the sign-in response.

Story Deck requests access to list players, control their playback, and read
Make Your Own cards. It keeps the access token in memory for the session and
saves the public client ID on the device. Use **Sign out** to clear the
session and the player and card lists.

## Build and test

Run these commands from the repository root.

```sh
make build
./bin/fern -apk story-deck.apk examples/android/yoto/app.fern
adb install -r story-deck.apk
go test ./internal/testing/e2e -run '^TestAndroidStoryDeck'
```

The host tests cover OAuth callback validation, API response and command
formats, and player selection in the UI simulator. Live sign-in and playback
require a registered public client and access to a Yoto player.

- [Yoto public-client sign-in](https://yoto.dev/authentication/headless-cli-auth/)
- [Yoto player and playback commands](https://yoto.dev/players-mqtt/mqtt-docs/)
