package e2e

import (
	"archive/zip"
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const plexApp = "examples/android/plex"

// runPlexSuite builds one std/test suite of the Plex app for the host and
// requires that it passes.
func runPlexSuite(t *testing.T, file string) {
	t.Helper()
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), strings.TrimSuffix(file, ".fern"))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(plexApp, file), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", file, err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("%s: %v\n%s", file, err, out)
	}
}

// plex_test.fern reads replies in the shape that plex.tv and a server send,
// and builds the requests of the app.
func TestAndroidPlexModel(t *testing.T) {
	runPlexSuite(t, "plex_test.fern")
}

// control_test.fern runs the app on the host against a fake plex.tv and a
// fake server: the sign-in with a PIN, the connections of a server, the
// home screen, a library page by page, a search, and a playback that
// resumes, pauses and stops. The Android lane runs the app on an emulator.
func TestAndroidPlexControl(t *testing.T) {
	runPlexSuite(t, "control_test.fern")
}

// fern -apk builds the app: a NativeActivity with no Java code of its own,
// whose classes.dex holds the listener of the media player.
func TestAndroidPlexApk(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "plex.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out, "-apk-package", "dev.fern.plex", "-apk-label", "Fern for Plex",
		"-apk-min-sdk", "30", "-apk-permission", "INTERNET", "-apk-cleartext",
		e2eharness.RepoPath(plexApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, " "); got != "AndroidManifest.xml classes.dex lib/arm64-v8a/libapp.so lib/x86_64/libapp.so" {
		t.Errorf("entries = %s", got)
	}
	want := []string{"ANativeActivity_onCreate", "Java_dev_fern_plex_Media_onPrepared", "Java_dev_fern_plex_Media_onCompletion",
		"Java_dev_fern_plex_Media_onError", "Java_dev_fern_plex_Media_onInfo", "Java_dev_fern_plex_Media_onSeekComplete"}
	for _, name := range []string{"lib/arm64-v8a/libapp.so", "lib/x86_64/libapp.so"} {
		f, err := elf.NewFile(bytes.NewReader(readZipEntry(t, zr, name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		syms, err := f.DynamicSymbols()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		have := map[string]bool{}
		for _, s := range syms {
			if s.Section != elf.SHN_UNDEF {
				have[s.Name] = true
			}
		}
		for _, w := range want {
			if !have[w] {
				t.Errorf("%s does not export %s", name, w)
			}
		}
	}
	if v, ok := applicationAttr(t, readZipEntry(t, zr, "AndroidManifest.xml"), usesCleartextTrafficID); !ok || v != 0xffffffff {
		t.Errorf("android:usesCleartextTraffic = %#x (set %v), want true", v, ok)
	}
	verifyApkV2(t, raw)
}
