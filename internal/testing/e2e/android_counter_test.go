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

const counterApp = "examples/android/counter"

// counter_test.fern runs the counter on the simulator of std/android/ui:
// its first screen, its two buttons, what an accessibility service reads,
// and the back key.
func TestAndroidCounterProgram(t *testing.T) {
	runAppSuite(t, counterApp, "counter_test.fern")
}

// fern -apk builds the counter from its fern.toml: a library for each ABI,
// and the classes.dex of the accessibility classes of std/android/access.
func TestAndroidCounterApk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "counter.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(counterApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
