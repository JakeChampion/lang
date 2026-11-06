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

const stopwatchApp = "examples/android/stopwatch"

func TestAndroidStopwatchLogic(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), "stopwatch_test")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(stopwatchApp, "stopwatch_test.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stopwatch_test.fern: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("stopwatch_test.fern: %v\n%s", err, out)
	}
}

// control_test.fern gives the behaviour of the app a sequence of touches,
// keys and times on the host, through a fake device: a tap starts the
// stopwatch, a long press resets it, and a drag scrolls the settings. The
// Android lane runs the same app on an emulator.
func TestAndroidStopwatchControl(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), "control_test")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(stopwatchApp, "control_test.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build control_test.fern: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("control_test.fern: %v\n%s", err, out)
	}
}

// ui_test.fern draws each screen of the app on a frame in memory, and the
// launcher icon it draws is the icon.png that the APK carries.
func TestAndroidStopwatchUI(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "ui_test")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(stopwatchApp, "ui_test.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build ui_test.fern: %v\n%s", err, out)
	}
	icon := filepath.Join(dir, "icon.png")
	run := exec.Command(bin)
	run.Env = append(os.Environ(), "STOPWATCH_ICON="+icon)
	out, err := run.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("ui_test.fern: %v\n%s", err, out)
	}
	got, err := os.ReadFile(icon)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(e2eharness.RepoPath(stopwatchApp, "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("icon.png is not the icon that ui.icon draws; write it again with STOPWATCH_ICON=%s", e2eharness.RepoPath(stopwatchApp, "icon.png"))
	}
}

// fern -apk builds the app, a NativeActivity with no Java code, with its
// launcher icon.
func TestAndroidStopwatchApk(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "stopwatch.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out, "-apk-package", "dev.fern.stopwatch", "-apk-label", "Fern Stopwatch",
		"-apk-icon", e2eharness.RepoPath(stopwatchApp, "icon.png"), "-apk-min-sdk", "28",
		e2eharness.RepoPath(stopwatchApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
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
	if got := strings.Join(names, " "); got != "AndroidManifest.xml resources.arsc res/drawable/icon.png lib/arm64-v8a/libapp.so lib/x86_64/libapp.so" {
		t.Errorf("entries = %s", got)
	}
	for _, name := range []string{"lib/arm64-v8a/libapp.so", "lib/x86_64/libapp.so"} {
		lib := readZipEntry(t, zr, name)
		f, err := elf.NewFile(bytes.NewReader(lib))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		syms, err := f.DynamicSymbols()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		found := false
		for _, s := range syms {
			if s.Name == "ANativeActivity_onCreate" && s.Section != elf.SHN_UNDEF {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not export ANativeActivity_onCreate", name)
		}
	}
	verifyApkV2(t, raw)
}

// A Java_* export takes the JNIEnv* and a jobject or jclass as usize, then
// arguments and a result whose types have the width of a JNI type.
func TestJNIExportWidths(t *testing.T) {
	cases := []struct{ sig, want string }{
		{"(env: usize, cls: usize, n: i32): i32", ""},
		{"(env: usize, cls: usize, on: u8, d: f64, r: usize): i64", ""},
		{"(env: usize, cls: usize): void", ""},
		{"(env: usize): i32", "a JNI method takes the JNIEnv* and a jobject or jclass first, each as usize"},
		{"(env: i32, cls: usize): i32", "a JNI method takes the JNIEnv* and a jobject or jclass first, each as usize"},
		{"(env: usize, cls: usize, on: boolean): i32", "parameter on is boolean, which has no JNI type of its width"},
		{"(env: usize, cls: usize, s: string): i32", "parameter s is string, which has no JNI type of its width"},
		{"(env: usize, cls: usize): boolean", "the result is boolean, which has no JNI type of its width"},
	}
	for _, c := range cases {
		t.Run(c.sig, func(t *testing.T) {
			body := "return 0;"
			if strings.HasSuffix(c.sig, "void") {
				body = ""
			} else if strings.HasSuffix(c.sig, "boolean") {
				body = "return true;"
			}
			src := "pub function Java_dev_fern_t_T_m" + c.sig + " { " + body + " }\nfunction main(): i32 { return 0; }\n"
			dir := t.TempDir()
			path := filepath.Join(dir, "m.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", "arm64-android", "-shared",
				"-export", "Java_dev_fern_t_T_m", "-o", filepath.Join(dir, "libm.so"), path, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if c.want == "" {
				if err != nil {
					t.Fatalf("a JNI method of these widths was refused: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), c.want) {
				t.Fatalf("want a refusal with %q, got %v:\n%s", c.want, err, out)
			}
		})
	}
}
