package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostReaderBytesRetainedShortReads(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	_, targets, _ := hostTargets()
	if wasmtime, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{wasmtime, "run", "--dir=."}})
	}
	for _, target := range targets {
		for _, tc := range []struct {
			name     string
			request  int
			sanitize bool
		}{
			{"full", 1024, false},
			{"short", 65536, false},
			{"short-sanitized", 65536, true},
		} {
			t.Run(target.target+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				data := make([]byte, 1024)
				for i := range data {
					data[i] = byte(i)
				}
				if err := os.WriteFile(filepath.Join(dir, "input"), data, 0o644); err != nil {
					t.Fatal(err)
				}
				slotBytes := 1
				if target.target == "wasm32-wasi" {
					// Primary WASM arrays have one four-byte slot per byte.
					slotBytes = 4
				}
				source := e2eharness.ReaderBytesRetainedProgram(tc.request, slotBytes, 64, tc.sanitize)
				src := mustWrite(t, dir, "retained.fern", source)
				bin := filepath.Join(dir, "retained")
				args := []string{"-target", target.target, "-o", bin}
				if target.target == "wasm32-wasi" {
					args = append(args, "-emit", "core-module")
				}
				compile := exec.Command(cli, append(args, src, stdlib)...)
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(entry, "FERN_SANITIZE=") && !strings.HasPrefix(entry, "FERN_RC_FREE_DEBUG=") {
						compile.Env = append(compile.Env, entry)
					}
				}
				compile.Env = append(compile.Env, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
				if tc.sanitize {
					compile.Env = append(compile.Env, "FERN_SANITIZE=1")
				}
				if out, err := compile.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				run := runX86_64Bin(target.runner, bin)
				run.Dir = dir
				out, err := run.CombinedOutput()
				if err != nil || bytes.Contains(out, []byte("fern-sanitizer:")) {
					t.Fatalf("retained reads: %v\n%s", err, out)
				}
				assertBalancedCensus(t, string(out))
			})
		}
	}
}
