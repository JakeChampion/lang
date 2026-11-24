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

const pressApp = "examples/android/press"

// press_test.fern runs the image compressor on the simulator of
// std/android/ui: it opens a photo, encodes it as JPEG, PNG and WebP in
// workers, moves the quality, the size and the split of the comparison,
// and saves and shares the file through a fake device.
func TestAndroidPressProgram(t *testing.T) {
	runAppSuite(t, pressApp, "press_test.fern")
}

// fern -apk builds Press from its fern.toml, with the activity class that
// takes the result of the document picker in classes.dex.
func TestAndroidPressApk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "press.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(pressApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	if !bytes.Contains(readZipEntry(t, zr, "classes.dex"), []byte("dev/fern/press/MainActivity")) {
		t.Error("classes.dex has no activity class of Press")
	}
}
