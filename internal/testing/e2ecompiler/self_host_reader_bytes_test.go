package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostReaderBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfStdin(t, e2eharness.ReaderBytesProgram, target, e2eharness.ReaderBytesInput(), "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("reader bytes: exit %d\n%s", code, stderr)
			}
		})
	}
}

// The same program as a preview-2 component, whose stdin() reads wasi:cli/stdin
// through the component's handle table (#11110).
func testReaderBytesComponent(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	t.Run("component", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("requires wasmtime")
		}
		dir := t.TempDir()
		src, bin := filepath.Join(dir, "reader.fern"), filepath.Join(dir, "reader.wasm")
		if err := os.WriteFile(src, []byte(e2eharness.ReaderBytesProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, src, stdlib)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("component build: %v\n%s", err, out)
		}
		run := exec.Command("wasmtime", "run", bin)
		run.Stdin = bytes.NewReader(e2eharness.ReaderBytesInput())
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("reader bytes component: %v\n%s", err, out)
		}
	})
}

func TestSelfHostReaderBytesComponent(t *testing.T) {
	cli := buildSelfHostCLI(t)
	testReaderBytesComponent(t, cli.bin, cli.runner, cli.stdlib)
}

func TestSelfHostReaderBytesOwnership(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			// Require the production ownership lowering to release error payloads.
			stderr, code := cli.exitOfStdin(t, e2eharness.ReaderBytesProgram, target, e2eharness.ReaderBytesInput(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("reader bytes: exit %d\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "leakcheck:") || !strings.Contains(stderr, "live_bytes=0") {
				t.Fatalf("missing zero-live-byte census\n%s", stderr)
			}
		})
	}
}

func TestSelfHostArm64DarwinReaderBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "reader.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ReaderBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, checked := range []bool{true, false} {
		t.Run(map[bool]string{true: "checked", false: "plain"}[checked], func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "reader")
			compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
			compile.Env = os.Environ()
			if checked {
				compile.Env = append(compile.Env, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
			}
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			cmd := exec.Command(bin)
			cmd.Stdin = bytes.NewReader(e2eharness.ReaderBytesInput())
			out, err := cmd.CombinedOutput()
			if err != nil || strings.Contains(string(out), "fern-sanitizer:") {
				t.Fatalf("reader bytes: %v\n%s", err, out)
			}
			if checked {
				assertBalancedCensus(t, string(out))
			}
		})
	}
	testReaderBytesComponent(t, cli, nil, stdlib)
}
