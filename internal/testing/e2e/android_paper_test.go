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

const paperApp = "examples/android/paper"

// The assets of Paper: the wallpaper and the five weights of Roboto.
var paperAssets = []string{"wallpaper.jpg", "Roboto-Regular.ttf", "Roboto-Medium.ttf", "Roboto-SemiBold.ttf", "Roboto-Bold.ttf", "Roboto-ExtraBold.ttf"}

// runPaperSuite builds one std/test suite of Paper for the host and requires
// that it passes. The suites read the fonts from the directory of the app.
func runPaperSuite(t *testing.T, file string) {
	t.Helper()
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), strings.TrimSuffix(file, ".fern"))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(paperApp, file), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", file, err, out)
	}
	run := exec.Command(bin)
	run.Dir = e2eharness.RepoPath(paperApp)
	out, err := run.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("%s: %v\n%s", file, err, out)
	}
}

// paper_test.fern checks the rules: the names and the search, with Paper
// in the list, the settings file, the blocks, the notification filter, the
// weather reply and the text of the clock.
func TestAndroidPaperModel(t *testing.T) {
	runPaperSuite(t, "paper_test.fern")
}

// ui_test.fern draws each screen with the real wallpaper, which std/jpeg
// decodes, and the Roboto fonts, which std/truetype draws, and checks its
// layout.
func TestAndroidPaperUI(t *testing.T) {
	runPaperSuite(t, "ui_test.fern")
}

// control_test.fern runs the app on the host through a fake device: the
// swipes, a search that opens Paper itself, any number of home apps, a
// rename, a block, the pause before an app and the held notifications.
func TestAndroidPaperControl(t *testing.T) {
	runPaperSuite(t, "control_test.fern")
}

// fern -apk builds Paper as a home screen from the [android] table of its
// fern.toml, with no -apk- flag: the home categories, the wallpaper theme,
// the notification listener service, the fonts and the wallpaper under
// assets/, and the native methods of its three classes.
func TestAndroidPaperApk(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "paper.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out,
		e2eharness.RepoPath(paperApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	want := "AndroidManifest.xml classes.dex resources.arsc res/drawable/icon.png"
	for _, a := range paperAssets {
		want += " assets/" + a
	}
	want += " lib/arm64-v8a/libapp.so lib/x86_64/libapp.so"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("entries = %s, want %s", got, want)
	}
	for _, a := range paperAssets {
		disk, err := os.ReadFile(e2eharness.RepoPath(paperApp, "assets", a))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(readZipEntry(t, zr, "assets/"+a), disk) {
			t.Errorf("assets/%s differs from the file", a)
		}
	}
	man := readZipEntry(t, zr, "AndroidManifest.xml")
	for _, s := range []string{"dev.fern.paper.Home", "android.intent.category.HOME", "android.intent.category.DEFAULT",
		"dev.fern.paper.Filter", "android.permission.BIND_NOTIFICATION_LISTENER_SERVICE", "android.service.notification.NotificationListenerService"} {
		if !bytes.Contains(man, utf16le(s)) {
			t.Errorf("the manifest has no string %q", s)
		}
	}
	if v, ok := applicationAttr(t, man, themeID); !ok || v != homeThemeID {
		t.Errorf("android:theme = %#x (set %v), want Theme.DeviceDefault.Wallpaper.NoTitleBar", v, ok)
	}
	classes := readZipEntry(t, zr, "classes.dex")
	checkDex(t, classes)
	exports := []string{"ANativeActivity_onCreate", "Java_dev_fern_paper_Home_onNewIntent", "Java_dev_fern_paper_Bars_run",
		"Java_dev_fern_paper_Filter_onNotificationPosted", "Java_dev_fern_paper_Filter_onListenerConnected"}
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
		for _, w := range exports {
			if !have[w] {
				t.Errorf("%s does not export %s", name, w)
			}
		}
	}
	verifyApkV2(t, raw)
}
