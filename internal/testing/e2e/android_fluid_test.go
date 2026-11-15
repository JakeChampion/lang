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

const fluidApp = "examples/android/fluid"

// fluid_test.fern checks the rules of the app on the host: the settings of
// the simulation, its clock, and what a touch, a key and a frame do to the
// fluid, to the dialog and to kid mode.
func TestAndroidFluidRules(t *testing.T) {
	runAppSuite(t, fluidApp, "fluid_test.fern")
}

// pace_test.fern checks the pace of the frames: a frame for each refresh of
// a display of 60 or 90 Hz, a frame for each second refresh at 120 Hz, and
// a slower pace when many frames are late.
func TestAndroidFluidPace(t *testing.T) {
	runAppSuite(t, fluidApp, "pace_test.fern")
}

// overlay_test.fern checks the places of the controls on a phone that
// stands and on a phone on its side, the pictures of the exit button, and
// the items that an accessibility service reads.
func TestAndroidFluidOverlay(t *testing.T) {
	runAppSuite(t, fluidApp, "overlay_test.fern")
}

// The simulation runs in its shaders on Mesa's OpenGL ES: a splat shows
// dye, steps move it, a new window size keeps it, the bloom and the sun
// rays change the picture, and the capture reads it. The canvas of
// std/android/gl then draws the controls over the fluid. A window with
// many pixels for each dp shows the fluid from a smaller picture.
func TestAndroidFluidGPU(t *testing.T) {
	runGLCheck(t, "examples/android/fluid/gpu_test.fern", "111111111")
}

// fern -apk builds the app from its fern.toml: a library for each ABI, the
// icon, and the classes.dex of the accessibility classes of
// std/android/access.
func TestAndroidFluidApk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fluid.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(fluidApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	for _, name := range []string{"AndroidManifest.xml", "classes.dex", "res/drawable/icon.png", "lib/arm64-v8a/libapp.so", "lib/x86_64/libapp.so"} {
		if !have[name] {
			t.Errorf("the APK has no %s", name)
		}
	}
}
