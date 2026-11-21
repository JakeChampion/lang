# Story Deck for Android

Story Deck is an independent Fern companion for Yoto players. The family library
shows cover art for purchased cards, linked cards, Make Your Own cards, saved
podcasts, and saved radio. Select an online player to play a title and see
its current track. Select **This phone** to stream audio on your Android device.
You can pause, resume, or stop playback from Story Deck.

## Browse and search

- Swipe the collection filters to find **My Cards**, **Playlists**, **Club
  Collection**, **Sleep Sounds**, **Podcasts**, and **Radio**.
- Select **Show groups** to filter by a Yoto family group.
- Select **Last played** or **A–Z** next to the search box to change the sort order.

Search stays at the top as you scroll past the Library header. The Club
Collection filter shows the Club titles that Yoto returns in your library.

- Use the search box to find titles, authors, narrators, chapters, tracks, and
  episodes. Story Deck reads track information in the background and shows the
  progress. The search covers the information that Yoto returns for your library.
- Select **Titles**, **Chapters**, or **Tracks** to narrow the search. The result
  shows the name and artwork of the chapter or track when Yoto supplies it.

Track lists and playback also show this artwork.

- Tap a cover to open its card and scroll through its tracks. Select **Play**
  beside a track to start there when Yoto provides its playback keys.
- Open **More options** and select **Refresh** to update the library.
  Refresh also retries the track index.

## Listen offline

- While online, select **Download** under a cover in **Library**. You can also
  open the card and select **Download to phone**. To save the library at once,
  open **Downloads** and select **Download all cards**.
- Turn on **Downloaded only** in **Library** to see the cards saved on the phone.
  When you play on the phone, Story Deck uses saved audio first.
- Wait for the cards to appear in **Downloads**. Test playback with airplane mode
  on before you travel.
- Open **Downloads** to play the saved audio without a connection. To delete a
  download, open its track list and select **Remove download**.

Story Deck can save cards when Yoto gives the signed-in account complete audio
files. Live streams, previews, cards with subscription access, and cards marked
`onlineOnly` are unavailable for download.

## Queue and travel

- Select **Play next** on a card to add it to the phone queue. Open **Queue**
  to start the queue, remove a card, or set a sleep timer.
- Story Deck saves the current track and position on the phone. To save a
  separate position, open **Queue** and select **Bookmark this spot**.
- Use **Ready for the trip** in **Queue** to check queued cards or downloads.
  Story Deck checks each saved file for a readable audio header.
- Use **Story Deck** in Android Auto to browse downloaded cards and control
  playback.

## Change player settings

Open **Players** and select **Settings** next to a player. Edit the day and night
display brightness, nightlight color, volume limits, or schedule. The available
controls depend on the player. You can also set Bluetooth preferences, when the
display dims, automatic power-off, clock format, alarms, and right button
shortcuts. Pick a service or choose a track from your library for a shortcut.

Tap **Choose** next to Morning light or Nightlight to preview a colour.
Adjust hue, saturation, and brightness, then select **Use colour**.
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
session when you open the app. Open **More options** and select **Sign out** to
clear the saved session, the library, and downloads.

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

Set `STORY_DECK_ICON` to the path of `icon.png` when you run `deck_test.fern` to
write the launcher icon from `launcher_icon.fern`.

- [Yoto public-client sign-in](https://yoto.dev/authentication/headless-cli-auth/)
- [Yoto library permissions](https://yoto.dev/authentication/scopes/)
- [Yoto player and playback commands](https://yoto.dev/players-mqtt/mqtt-docs/)
- [Yoto player status](https://yoto.dev/players-mqtt/getting-player-status/)
- [Yoto content and playable URLs](https://yoto.dev/api/content/getcontent/)
- [Yoto device configuration](https://yoto.dev/api/devices/updatedeviceconfig/)
- [Yoto family library groups](https://yoto.dev/api/family-library-groups/getgroups/)
