package e2e

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const storyDeckApp = "examples/android/yoto"

func TestAndroidStoryDeckUI(t *testing.T) {
	runAppSuite(t, storyDeckApp, "deck_test.fern")
}

func TestAndroidStoryDeckApk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "story-deck.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", path,
		e2eharness.RepoPath(storyDeckApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Story Deck: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	manifest := readZipEntry(t, z, "AndroidManifest.xml")
	for _, want := range []string{
		"dev.fern.storydeck", "Story Deck", "android.permission.INTERNET", "android.permission.ACCESS_NETWORK_STATE", "dev.fern.storydeck.MainActivity",
		"dev.fern.storydeck.CarBrowse", "android.media.browse.MediaBrowserService",
		"dev.fern.storydeck.Playing",
		"com.google.android.gms.car.application", "android.permission.FOREGROUND_SERVICE_MEDIA_PLAYBACK",
		"android.permission.NFC", "android.permission.RECORD_AUDIO", "android.permission.READ_MEDIA_AUDIO",
		"enableOnBackInvokedCallback",
	} {
		if !bytes.Contains(manifest, utf16le(want)) {
			t.Errorf("the manifest has no string %q", want)
		}
	}
	dex := readZipEntry(t, z, "classes.dex")
	if !bytes.Contains(dex, []byte("Ldev/fern/android/Back;")) {
		t.Error("the APK does not contain the Android Back callback")
	}
	if !bytes.Contains(dex, []byte("Ldev/fern/storydeck/CardReader;")) {
		t.Error("the APK does not contain the NFC reader callback")
	}
	for _, want := range []string{
		"Ldev/fern/storydeck/MediaControls;", "onPlay", "onPause", "onSeekTo",
		"onRewind", "onCustomAction", "onSkipToNext", "onSkipToPrevious",
	} {
		if !bytes.Contains(dex, []byte(want)) {
			t.Errorf("the APK has no media callback %q", want)
		}
	}
	readZipEntry(t, z, "res/xml/automotive_app_desc.xml")
	icon, err := os.ReadFile(e2eharness.RepoPath(storyDeckApp, "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readZipEntry(t, z, "res/drawable/icon.png"), icon) {
		t.Error("the APK does not contain the Story Deck launcher icon")
	}
	readZipEntry(t, z, "lib/arm64-v8a/libapp.so")
	readZipEntry(t, z, "lib/x86_64/libapp.so")
	verifyApkV2(t, raw)
}
