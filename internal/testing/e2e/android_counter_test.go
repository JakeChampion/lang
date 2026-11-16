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
// and the classes.dex of the accessibility classes of std/android/access and
// of the back callback and the insets listener of std/android. The app
// targets API 36 and takes the back key through the callback.
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
	classes := readZipEntry(t, zr, "classes.dex")
	for _, c := range []string{"Ldev/fern/android/Back;", "Ldev/fern/android/Insets;", "onBackInvoked", "onApplyWindowInsets"} {
		if !bytes.Contains(classes, []byte(c)) {
			t.Errorf("classes.dex has no %s", c)
		}
	}
	man := readZipEntry(t, zr, "AndroidManifest.xml")
	if v, ok := applicationAttr(t, man, enableOnBackInvokedCallbackID); !ok || v != 0xffffffff {
		t.Errorf("android:enableOnBackInvokedCallback = %#x (set %v), want true", v, ok)
	}
	if v, ok := elementAttr(t, man, minSdkVersionID, targetSdkVersionID); !ok || v != 36 {
		t.Errorf("android:targetSdkVersion = %d (set %v), want 36", v, ok)
	}
}

const (
	enableOnBackInvokedCallbackID = 0x0101066c
	minSdkVersionID               = 0x0101020c
	targetSdkVersionID            = 0x01010270
)
