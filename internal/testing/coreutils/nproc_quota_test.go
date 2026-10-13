package coreutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestNprocCPUQuotaParsing(t *testing.T) {
	root := repoRoot(t)
	e2eharness.TrackFernSources(t, filepath.Join(root, "coreutils", "lib"), "cpuquota.fern")
	template := `import %q;
function check(text: string, expected: u64): boolean {
    return cpuquota.count(text) == expected;
}
function main(): i32 {
    if (!check("400000 100000\n", 4 as u64)) { return 1; }
    if (!check("125000 100000", 1 as u64)) { return 2; }
    if (!check("150000 100000", 2 as u64)) { return 3; }
    if (!check("175000 100000", 2 as u64)) { return 4; }
    if (!check("275000 100000", 3 as u64)) { return 5; }
    if (!check("25000 100000", 1 as u64)) { return 6; }
    if (!check("4 3", 1 as u64) || !check("5 3", 2 as u64)) { return 7; }
    if (!check("18446744073709551615 1", 18446744073709551615 as u64)) { return 8; }
    if (!check("18446744073709551615 2", 9223372036854775808 as u64)) { return 9; }
    for invalid in ["", "max 100000", "0 100000", "1 0", "-1 100000", "junk 1", "1", "1 2 3", "18446744073709551616 1"] {
        if (!check(invalid, 0 as u64)) { return 10; }
    }
    if (cpuquota.path("0::/\n") != "/sys/fs/cgroup//cpu.max") { return 11; }
    if (cpuquota.path("2:cpu:/legacy\n0::/team/job\n") != "/sys/fs/cgroup/team/job/cpu.max") { return 12; }
    if (cpuquota.path("2:cpu:/legacy\n") != "") { return 13; }
    return 0;
}`
	targets := []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"}
	if runtime.GOOS == "darwin" {
		targets = append(targets, "arm64-darwin")
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var prefix []string
			switch target {
			case "x86-64-linux":
				prefix = e2eharness.X86_64Runner(t)
			case "arm64-linux":
				if runner := e2eharness.Arm64Runner(t); runner != "" {
					prefix = []string{runner}
				}
			case "wasm32-wasi":
				wasmtime, err := exec.LookPath("wasmtime")
				if err != nil {
					t.Fatal("wasmtime is required for the quota parser's WASI gate")
				}
				prefix = []string{wasmtime, "run"}
			}
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "prog")
			module, err := filepath.Rel(dir, filepath.Join(root, "coreutils", "lib", "cpuquota"))
			if err != nil {
				t.Fatal(err)
			}
			source := fmt.Sprintf(template, filepath.ToSlash(module))
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := e2eharness.SelfHostCompileCmd(t, target, src, bin).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append(prefix, bin)
			cmd := exec.Command(argv[0], argv[1:]...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
