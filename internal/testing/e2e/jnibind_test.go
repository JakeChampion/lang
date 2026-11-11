package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func jnibindPath(t *testing.T, rel string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("../../..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// checkJnibind runs the tool on the sample JAR. The module that it writes
// must be tests/stdlib/jni_bind_sample.fern, which the stdlib test of
// std/jni/bind imports.
func checkJnibind(t *testing.T, run func(args ...string) *exec.Cmd) {
	t.Helper()
	jar := jnibindPath(t, "tools/jnibind/testdata/sample.jar")
	want, err := os.ReadFile(jnibindPath(t, "tests/stdlib/jni_bind_sample.fern"))
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd := run(jar, jnibindPath(t, "tools/jnibind/testdata/sample.spec"))
	cmd.Stderr = &stderr
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("jnibind: %v\n%s", err, stderr.String())
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("jnibind wrote a module that is not tests/stdlib/jni_bind_sample.fern. Write the file again with the tool.\n%s", got)
	}

	bad := filepath.Join(t.TempDir(), "bad.spec")
	if err := os.WriteFile(bad, []byte("dev.fern.Point nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"a member that the class does not have", []string{jar, bad}, 1, "jnibind: dev/fern/Point has no public member nothing"},
		{"a file that is not a JAR", []string{bad, bad}, 1, "zip: the archive has no central directory"},
		{"no SPEC", []string{jar}, 2, "usage: jnibind JAR SPEC"},
	} {
		out, err := run(tc.args...).CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if code != tc.code || !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: exit %d, want %d and %q\n%s", tc.name, code, tc.code, tc.want, out)
		}
	}
}

func TestJnibindX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	tool := filepath.Join(t.TempDir(), "jnibind.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", tool, jnibindPath(t, "tools/jnibind/jnibind.fern")).CombinedOutput(); err != nil {
		t.Fatalf("jnibind build failed: %v\n%s", err, o)
	}
	checkJnibind(t, func(args ...string) *exec.Cmd { return runX86Bin(qemu, tool, args...) })
}

func TestJnibindInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := jnibindPath(t, "tools/jnibind/jnibind.fern")
	checkJnibind(t, func(args ...string) *exec.Cmd {
		return exec.Command(bin, append([]string{"-interp", src, "--"}, args...)...)
	})
}
