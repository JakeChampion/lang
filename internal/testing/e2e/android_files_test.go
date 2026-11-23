package e2e

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	for _, want := range []string{
		"dev.fern.files", "Fern Files", "android.permission.MANAGE_EXTERNAL_STORAGE", "android.permission.INTERNET",
		"provider", "dev.fern.files.Shared", "authorities", "grantUriPermissions",
	} {
		if !bytes.Contains(manifest, utf16le(want)) {
			t.Errorf("the manifest has no string %q", want)
		}
	}
	dex := readZipEntry(t, zr, "classes.dex")
	for _, want := range []string{"Ldev/fern/files/Shared;", "Landroid/content/ContentProvider;", "openFile", "getType", "query"} {
		if !bytes.Contains(dex, []byte(want)) {
			t.Errorf("classes.dex has no string %q", want)
		}
	}
	providerTree(t, out)
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

// providerTree checks the provider element of the manifest with aapt, when
// the machine has it: a @jni class that extends ContentProvider is a
// provider that is not exported, with the name of its class as its
// authority, and it can give access to one URI.
func providerTree(t *testing.T, apk string) {
	aapt, err := exec.LookPath("aapt")
	if err != nil {
		t.Log("aapt is not on PATH, so the provider element is checked by its strings only")
		return
	}
	o, err := exec.Command(aapt, "dump", "xmltree", apk, "AndroidManifest.xml").CombinedOutput()
	if err != nil {
		t.Fatalf("aapt dump xmltree: %v\n%s", err, o)
	}
	tree := string(o)
	at := strings.Index(tree, "E: provider")
	if at < 0 {
		t.Fatalf("the manifest has no provider element:\n%s", tree)
	}
	for _, want := range []string{
		`A: android:name(0x01010003)="dev.fern.files.Shared"`,
		"A: android:exported(0x01010010)=(type 0x12)0x0",
		`A: android:authorities(0x01010018)="dev.fern.files.Shared"`,
		"A: android:grantUriPermissions(0x0101001b)=(type 0x12)0xffffffff",
	} {
		if !strings.Contains(tree[at:], want) {
			t.Errorf("the provider element has no %s:\n%s", want, tree[at:])
		}
	}
}
