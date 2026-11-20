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
	for _, want := range []string{"dev.fern.storydeck", "Story Deck", "android.permission.INTERNET"} {
		if !bytes.Contains(manifest, utf16le(want)) {
			t.Errorf("the manifest has no string %q", want)
		}
	}
	readZipEntry(t, z, "lib/arm64-v8a/libapp.so")
	readZipEntry(t, z, "lib/x86_64/libapp.so")
	verifyApkV2(t, raw)
}
