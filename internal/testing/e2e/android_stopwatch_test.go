package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const stopwatchApp = "examples/android/stopwatch"

// stopwatchShared builds native.fern for target, and writes the Core.java of
// its @jni declarations under the returned java directory.
func stopwatchShared(t *testing.T, target string) (so, java string) {
	t.Helper()
	dir := t.TempDir()
	so = filepath.Join(dir, "libfern.so")
	java = filepath.Join(dir, "java")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-shared", "-jni-java", java, "-o", so,
		e2eharness.RepoPath(stopwatchApp, "native.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build libfern.so for %s: %v\n%s", target, err, out)
	}
	return so, java
}

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

// Every native method of the generated Core.java has an export in the
// arm64 library, and the library is one that Android's loader accepts.
func TestAndroidStopwatchExports(t *testing.T) {
	so, java := stopwatchShared(t, "arm64-android")
	core, err := os.ReadFile(filepath.Join(java, "dev", "fern", "stopwatch", "Core.java"))
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, m := range regexp.MustCompile(`static native [\w\[\]]+ (\w+)\(`).FindAllSubmatch(core, -1) {
		declared = append(declared, "Java_dev_fern_stopwatch_Core_"+string(m[1]))
	}
	if len(declared) == 0 {
		t.Fatalf("the generated Core.java declares no native method:\n%s", core)
	}
	raw, err := os.ReadFile(so)
	if err != nil {
		t.Fatal(err)
	}
	sharedImageCheck(t, raw, elf.EM_AARCH64)
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	syms, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range syms {
		if elf.ST_TYPE(s.Info) == elf.STT_FUNC && s.Section != elf.SHN_UNDEF {
			have[s.Name] = true
		}
	}
	for _, name := range declared {
		if !have[name] {
			t.Errorf("Core.java declares %s, and the library does not export it", name)
		}
	}
	if len(have) != len(declared) {
		t.Errorf("the library exports %d functions, and Core.java declares %d", len(have), len(declared))
	}
}

// HostCheck calls every native method in a desktop JVM, through the same
// JNI entry points the APK uses, on the host build of libfern.so.
func TestAndroidStopwatchHostJVM(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skipf("-shared does not build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac not on PATH")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not on PATH")
	}
	so, javaDir := stopwatchShared(t, e2eharness.HostSelfHostTarget())
	classes := t.TempDir()
	if out, err := exec.Command(javac, "-d", classes,
		filepath.Join(javaDir, "dev", "fern", "stopwatch", "Core.java"),
		e2eharness.RepoPath(stopwatchApp, "host/HostCheck.java")).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	out, err := exec.Command(java, "-Djava.library.path="+filepath.Dir(so), "-cp", classes,
		"dev.fern.stopwatch.HostCheck").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("HostCheck: %v\n%s", err, out)
	}
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
