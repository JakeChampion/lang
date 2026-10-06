package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The builder operations are local memory operations. They need no WASI
// imports, in either a pure component or a component with stdout.
func testBuilderComponents(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	for _, tc := range []struct{ name, src string }{
		{"take", e2eharness.BuilderBytesProgram},
		{"range", e2eharness.BufByteRangeProgram},
	} {
		for _, output := range []bool{false, true} {
			name := tc.name + "/pure"
			source, want := tc.src, ""
			if output {
				name = tc.name + "/stdout"
				source = strings.Replace(source, "function main(): i32", "function byte_check(): i32", 1)
				source += "\nfunction main(): i32 { let code = byte_check(); if (code == 0) { print(\"ok\"); } return code; }\n"
				want = "ok\n"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				src, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
				if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, src, stdlib)
				cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("component compile: %v\n%s", err, out)
				}
				if out, err := exec.Command("wasmtime", "run", bin).CombinedOutput(); err != nil || string(out) != want {
					t.Fatalf("component run: %v, output %q, want %q", err, out, want)
				}
			})
		}
	}
}

func TestSelfHostBuilderComponents(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	cli := buildSelfHostCLI(t)
	testBuilderComponents(t, cli.bin, cli.runner, cli.stdlib)
}

func TestSelfHostArm64DarwinBuilderComponents(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	testBuilderComponents(t, cli, nil, e2eharness.SelfHostStdlibRoot(t))
}
