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

const filesApp = "examples/android/files"

// protocol_test.fern checks the MD4 and NTLMv2 values of the SMB sign-in
// against their specifications, reads a directory listing, and copies a
// directory in steps.
func TestAndroidFilesProtocol(t *testing.T) {
	runAppSuite(t, filesApp, "protocol_test.fern")
}

// files_test.fern runs the file explorer on the simulator of std/android/ui
// with a temporary directory as the storage of the phone. It browses,
// copies, moves, renames and deletes real files, and saves a server.
func TestAndroidFilesUI(t *testing.T) {
	runAppSuite(t, filesApp, "files_test.fern")
}

// fern -apk builds the file explorer from its fern.toml for both ABIs.
func TestAndroidFilesApk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "files.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(filesApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	manifest := readZipEntry(t, zr, "AndroidManifest.xml")
	for _, want := range []string{"dev.fern.files", "Fern Files", "android.permission.MANAGE_EXTERNAL_STORAGE", "android.permission.INTERNET"} {
		if !bytes.Contains(manifest, utf16le(want)) {
			t.Errorf("the manifest has no string %q", want)
		}
	}
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
	}
	for _, name := range []string{"classes.dex", "lib/arm64-v8a/libapp.so", "lib/x86_64/libapp.so"} {
		if !have[name] {
			t.Errorf("the APK has no %s", name)
		}
	}
}
