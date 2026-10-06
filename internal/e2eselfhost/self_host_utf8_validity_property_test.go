package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Run the closing string-validity property with the primary compiler, as
// well as the reference interpreter's TestRunnerUtf8ValidityProperty.
func TestSelfHostUtf8ValidityProperty(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := langSrcAbs(t, "tests/stdlib/utf8_validity_property_test.fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, cli.arm64Binary(t, src))
			case "wasm32-wasi":
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", cli.emit(t, src, target))
			}
			output, err := cmd.CombinedOutput()
			out := string(output)
			if err != nil || !strings.Contains(out, "# pass 5") || !strings.Contains(out, "# fail 0") || !strings.Contains(out, "1..5") {
				t.Fatalf("string validity property: %v\n%s", err, out)
			}
		})
	}
}

func TestSelfHostArm64DarwinUtf8ValidityProperty(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	bin := filepath.Join(t.TempDir(), "utf8-validity")
	src := langSrcAbs(t, "tests/stdlib/utf8_validity_property_test.fern")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
		t.Fatalf("compile property: %v\n%s", err, out)
	}
	output, err := exec.Command(bin).CombinedOutput()
	out := string(output)
	if err != nil || !strings.Contains(out, "# pass 5") || !strings.Contains(out, "# fail 0") || !strings.Contains(out, "1..5") {
		t.Fatalf("string validity property: %v\n%s", err, out)
	}
}
