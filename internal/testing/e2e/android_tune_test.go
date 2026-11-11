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

const tuneApp = "examples/android/tune"

// engine_test.fern reads a WAV file and a FLAC file of one tune a part at a
// time, and requires each sample of the tune. It also starts a deck at a
// part of its file.
func TestAndroidTuneEngine(t *testing.T) {
	runAppSuite(t, tuneApp, "engine_test.fern")
}

// library_test.fern finds the files of sound below a directory, and gives a
// file with no tags its artist and its album from its path.
func TestAndroidTuneLibrary(t *testing.T) {
	runAppSuite(t, tuneApp, "library_test.fern")
}

// tune_test.fern runs the music player on the simulator of std/android/ui,
// with a device that takes the samples of 20 ms at each write. It plays a
// track in time, stops it, moves the slider, plays the next track at the
// end, and stops at a file that is not sound. It reads the tags of its
// files, and shows the artists and the albums.
func TestAndroidTuneProgram(t *testing.T) {
	runAppSuite(t, tuneApp, "tune_test.fern")
}

// fern -apk builds the player from its fern.toml for both ABIs.
func TestAndroidTuneApk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "tune.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(tuneApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
	}
	for _, name := range []string{"AndroidManifest.xml", "classes.dex", "lib/arm64-v8a/libapp.so", "lib/x86_64/libapp.so"} {
		if !have[name] {
			t.Errorf("the APK has no %s", name)
		}
	}
}
