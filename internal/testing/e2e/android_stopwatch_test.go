package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const stopwatchApp = "examples/android/stopwatch"

// The JNI exports of native.fern, the way build.sh finds them.
func stopwatchExports(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(e2eharness.RepoPath(stopwatchApp, "native.fern"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^pub function (Java_[A-Za-z0-9_]+)`).FindAllSubmatch(src, -1) {
		names = append(names, string(m[1]))
	}
	if len(names) == 0 {
		t.Fatal("native.fern has no Java_ functions")
	}
	return names
}

func stopwatchShared(t *testing.T, target string) string {
	t.Helper()
	so := filepath.Join(t.TempDir(), "libfern.so")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", target, "-shared",
		"-export", strings.Join(stopwatchExports(t), ","), "-o", so,
		e2eharness.RepoPath(stopwatchApp, "native.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build libfern.so for %s: %v\n%s", target, err, out)
	}
	return so
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

// Every native method of Core.java has an export, and every export has a
// native method, so the APK cannot fail with an UnsatisfiedLinkError.
func TestAndroidStopwatchExports(t *testing.T) {
	java, err := os.ReadFile(e2eharness.RepoPath(stopwatchApp, "java/dev/fern/stopwatch/Core.java"))
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, m := range regexp.MustCompile(`static native \w+ (\w+)\(`).FindAllSubmatch(java, -1) {
		declared = append(declared, "Java_dev_fern_stopwatch_Core_"+string(m[1]))
	}
	exported := stopwatchExports(t)
	sort.Strings(declared)
	sort.Strings(exported)
	if strings.Join(declared, "\n") != strings.Join(exported, "\n") {
		t.Fatalf("Core.java natives and native.fern exports differ\nCore.java:\n%s\n\nnative.fern:\n%s",
			strings.Join(declared, "\n"), strings.Join(exported, "\n"))
	}

	raw, err := os.ReadFile(stopwatchShared(t, "arm64-android"))
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
	for _, name := range exported {
		if !have[name] {
			t.Errorf("the dynamic symbol table has no function %s", name)
		}
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
	so := stopwatchShared(t, e2eharness.HostSelfHostTarget())
	classes := t.TempDir()
	if out, err := exec.Command(javac, "-d", classes,
		e2eharness.RepoPath(stopwatchApp, "java/dev/fern/stopwatch/Core.java"),
		e2eharness.RepoPath(stopwatchApp, "host/HostCheck.java")).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	out, err := exec.Command(java, "-Djava.library.path="+filepath.Dir(so), "-cp", classes,
		"dev.fern.stopwatch.HostCheck").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("HostCheck: %v\n%s", err, out)
	}
}
