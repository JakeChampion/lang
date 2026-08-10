// E2E tests for the experimental SSA-direct wasm backend
// (`-target wasm -backend ssa`). Builds the lang CLI from this
// checkout, compiles a small Lang program with the new target,
// validates the emitted module via wasm-tools, then runs it
// under wasmtime.
//
// SKIPs when wasmtime / wasm-tools aren't on PATH so the suite
// stays green on developer machines without the toolchain.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestWasmSSACliRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wasm-ssa not exercised on windows")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-ssa e2e")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-ssa e2e")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "sum.fern")
	// Triangular sum: returns n*(n-1)/2 via a while loop —
	// exercises arithmetic, control flow, and the relooper's
	// loop-handling end-to-end through the CLI.
	if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
  var s: i32 = 0;
  var i: i32 = 0;
  var n: i32 = 10;
  while (i < n) {
    s = s + i;
    i = i + 1;
  }
  return s;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	outWasm := filepath.Join(dir, "sum.wasm")
	emit := exec.Command(bin,
		"-target", "wasm32-wasi", "-backend", "ssa",
		"-o", outWasm,
		srcPath,
	)
	var obuf, ebuf bytes.Buffer
	emit.Stdout = &obuf
	emit.Stderr = &ebuf
	if err := emit.Run(); err != nil {
		t.Fatalf("fern -target wasm -backend ssa: %v\nstdout:\n%s\nstderr:\n%s", err, obuf.String(), ebuf.String())
	}

	// wasm-tools validate confirms the emitted module is
	// structurally valid wasm.
	validate := exec.Command("wasm-tools", "validate", outWasm)
	if out, err := validate.CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}

	// wasmtime --invoke main returns the i32 result on stdout
	// (with an experimental-API warning on stderr).
	run := exec.Command("wasmtime", "run", "--invoke", "main", outWasm)
	var so bytes.Buffer
	run.Stdout = &so
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(so.String()))
	if err != nil {
		t.Fatalf("parse wasmtime stdout %q: %v", so.String(), err)
	}
	// sum(0..9) = 45.
	if got != 45 {
		t.Errorf("wasm-ssa output = %d, want 45", got)
	}
}

// TestWasmSSAComponentWrapCli — `-target wasm -backend ssa
// -component-wrap-cli` lifts the wasm-ssa core module as a
// preview-2 component implementing wasi:cli/run@0.2.0. Such
// a component runs under plain `wasmtime run prog.wasm` (no
// --invoke). The i32 return value translates to the host
// exit code: 0 = success, non-zero = failure (1 from wasmtime
// when the result<_,_> lift sees an Err tag).
func TestWasmSSAComponentWrapCli(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wasm-ssa not exercised on windows")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-ssa cli-wrap e2e")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-ssa cli-wrap e2e")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	// Two programs: one that returns 0 (host exit 0) and one
	// that returns non-zero (host exit 1 from the canonical lift).
	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{
			name: "ok",
			src: `function main(): i32 {
  return 0;
}
`,
			wantExit: 0,
		},
		{
			name: "err",
			src: `function main(): i32 {
  return 7;
}
`,
			wantExit: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			outWasm := filepath.Join(dir, c.name+".wasm")
			emit := exec.Command(bin,
				"-target", "wasm32-wasi", "-backend", "ssa",
				"-component-wrap-cli",
				"-o", outWasm,
				srcPath,
			)
			var obuf, ebuf bytes.Buffer
			emit.Stdout = &obuf
			emit.Stderr = &ebuf
			if err := emit.Run(); err != nil {
				t.Fatalf("fern -target wasm -backend ssa -component-wrap-cli: %v\nstdout:\n%s\nstderr:\n%s", err, obuf.String(), ebuf.String())
			}
			if out, err := exec.Command("wasm-tools", "validate", outWasm).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, out)
			}
			cmd := exec.Command("wasmtime", "run", outWasm)
			cmd.Run()
			got := cmd.ProcessState.ExitCode()
			if got != c.wantExit {
				t.Errorf("wasmtime exit = %d, want %d", got, c.wantExit)
			}
		})
	}
}
