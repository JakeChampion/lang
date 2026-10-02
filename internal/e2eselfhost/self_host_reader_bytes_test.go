package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
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

// Primary components do not yet provide stdin, including the existing text
// Reader operations. Keep the refusal explicit until their Preview 2 imports
// and resource lifecycle are implemented; core WASM execution is tested above.
func testReaderBytesComponentRefusal(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	t.Run("component", func(t *testing.T) {
		dir := t.TempDir()
		src, bin := filepath.Join(dir, "reader.fern"), filepath.Join(dir, "reader.wasm")
		if err := os.WriteFile(src, []byte(e2eharness.ReaderBytesProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", src, stdlib, "-o", bin)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "read_chunk_bytes is not supported in a wasm component") {
			t.Fatalf("expected explicit unsupported-component diagnostic: %v\n%s", err, out)
		}
	})
}

func TestSelfHostReaderBytesComponentRefusal(t *testing.T) {
	cli := buildSelfHostCLI(t)
	testReaderBytesComponentRefusal(t, cli.bin, cli.runner, cli.stdlib)
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
	stdlib, err := filepath.Abs("../../internal/stdlib")
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
			compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
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
	testReaderBytesComponentRefusal(t, cli, nil, stdlib)
}
