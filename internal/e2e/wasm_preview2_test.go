// E2E test for the WASI Preview 2 component pipeline. Builds the lang
// CLI, asks it to emit a Component Model component (preview-1 module
// wrapped via wasm-tools + the wasi-preview1-component-adapter), and
// runs it with `wasmtime run`.
//
// Skips when either wasm-tools, the adapter (LANG_WASI_ADAPTER env), or
// wasmtime is missing so `go test ./...` stays green on developer
// machines without the full preview-2 toolchain.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWasmPreview2HelloWorld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("preview-2 toolchain not exercised on windows")
	}
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH; skipping preview-2 e2e")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping preview-2 e2e")
	}
	adapter := os.Getenv("LANG_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("LANG_WASI_ADAPTER not set; skipping preview-2 e2e (CI sets this)")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %q not readable: %v", adapter, err)
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "hello.lang")
	// Exercises both:
	//   - WASI preview-1 fd_write (the adapter routes it through
	//     wasi:io/streams under the hood);
	//   - Native preview-2 wasi:random/random.get-random-bytes —
	//     bypasses the adapter via the WIT world we embed,
	//     forcing the canonical-ABI path through `cabi_realloc`.
	if err := os.WriteFile(srcPath, []byte(`function main(): number {
    var b = random_bytes(8);
    print("hello preview2");
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// Build the lang CLI from this checkout so the test exercises
	// the in-tree post-process pipeline rather than whatever happens
	// to be on PATH.
	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/lang")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}

	componentPath := filepath.Join(dir, "hello.component.wasm")
	emit := exec.Command(bin,
		"-target", "wasm",
		"-wasi-preview2",
		"-wasi-adapter", adapter,
		"-o", componentPath,
		srcPath,
	)
	var obuf, ebuf bytes.Buffer
	emit.Stdout = &obuf
	emit.Stderr = &ebuf
	if err := emit.Run(); err != nil {
		t.Fatalf("lang -wasi-preview2: %v\nstdout:\n%s\nstderr:\n%s", err, obuf.String(), ebuf.String())
	}

	// The output should be a Component Model component, recognised
	// by `wasm-tools print` as a top-level `(component …)` form.
	dump := exec.Command("wasm-tools", "print", componentPath)
	dout, err := dump.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, dout)
	}
	if !strings.Contains(string(dout), "(component") {
		t.Fatalf("output is not a component (no `(component` token):\n%s", dout)
	}

	run := exec.Command("wasmtime", "run", componentPath)
	var sout, serr bytes.Buffer
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run %s: %v\nstdout:\n%s\nstderr:\n%s",
			componentPath, err, sout.String(), serr.String())
	}
	if got, want := strings.TrimRight(sout.String(), "\n"), "hello preview2"; got != want {
		t.Fatalf("stdout = %q; want %q (stderr=%q)", got, want, serr.String())
	}
}
