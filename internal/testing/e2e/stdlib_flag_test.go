package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// -stdlib DIR compiles against the standard library in DIR. A module that
// only the copy in DIR has resolves with the flag, and does not resolve
// against the stdlib of the repository.
func TestStdlibFlag(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := t.TempDir()
	lib := filepath.Join(dir, "stdlib")
	if out, err := exec.Command("cp", "-r", e2eharness.SelfHostStdlibRoot(t), lib).CombinedOutput(); err != nil {
		t.Fatalf("copy the stdlib: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(lib, "std", "zz_probe.fern"), []byte("pub function probe(): i32 {\n  return 7;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	prog := "import \"std/zz_probe\";\n\nfunction main(): i32 {\n  return zz_probe.probe() - 7;\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "main")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-stdlib", lib, "-target", host, "-o", bin, src)
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -stdlib: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("the program: %v\n%s", err, out)
	}

	cmd = exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "zz_probe") {
		t.Fatalf("without -stdlib, std/zz_probe must not resolve; got %v\n%s", err, out)
	}
}
