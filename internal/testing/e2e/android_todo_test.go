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

const todoApp = "examples/android/todo"

// todo_test.fern runs the list of things to do on the simulator of
// std/android/ui: it types into the field, adds items with the button and
// with the action key, marks one done, removes one, and scrolls a long
// list.
func TestAndroidTodoProgram(t *testing.T) {
	runAppSuite(t, todoApp, "todo_test.fern")
}

// fern -apk builds the app from its fern.toml, with the classes.dex of
// the accessibility classes and of the text field of the keyboard.
func TestAndroidTodoApk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "todo.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(todoApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	if !bytes.Contains(readZipEntry(t, zr, "classes.dex"), []byte("dev/fern/android/TextField")) {
		t.Error("classes.dex has no class of the text field of the keyboard")
	}
}
