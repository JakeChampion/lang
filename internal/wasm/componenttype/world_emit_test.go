package componenttype

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmitInterfaceTypeImport gates the first P2 emission slice: emitting a
// self-contained interface (wasi:io/error) from the decoded world produces a
// component that `wasm-tools` validates and whose reconstructed WIT shows the
// full interface (the `error` resource). The gate is "validates + the WIT
// round-trips", per the P2 direction (not byte-identity).
func TestEmitInterfaceTypeImport(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := w.ComponentWithImport("wasi:io/error@0.2.0")
	if err != nil {
		t.Fatalf("ComponentWithImport: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "io-error.wasm")
	if err := os.WriteFile(path, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}

	if out, err := exec.Command(wasmtools, "validate", path).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}

	wit, err := exec.Command(wasmtools, "component", "wit", path).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	got := string(wit)
	if !strings.Contains(got, "wasi:io/error@0.2.0") {
		t.Errorf("WIT missing the io/error import:\n%s", got)
	}
	if !strings.Contains(got, "resource error") {
		t.Errorf("WIT missing the error resource:\n%s", got)
	}
}

// TestEmitInterfaceTypeImportRejectsShared confirms an interface that pulls in
// shared types (io/streams aliases io/error's error) is refused until the
// surfacing slice lands.
func TestEmitInterfaceTypeImportRejectsShared(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	if _, err := w.EmitInterfaceTypeImport("wasi:io/streams@0.2.0"); err == nil {
		t.Fatal("expected io/streams emission to be refused (shared types), got nil error")
	}
}

// TestEmitWorldImports gates the full-world emission: replaying the world's
// decls as component sections yields a component that wasm-tools validates and
// whose reconstructed WIT imports every interface of the world (with their
// shared types resolved across interfaces). Run for both shipped worlds.
func TestEmitWorldImports(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	type wc struct {
		world string
		want  []string
	}
	for _, c := range []wc{
		{"fern", []string{
			"wasi:io/error@0.2.0", "wasi:io/streams@0.2.0", "wasi:cli/stdout@0.2.0",
			"wasi:filesystem/types@0.2.0", "wasi:sockets/tcp@0.2.0", "wasi:random/random@0.2.0",
		}},
		{"http", []string{"wasi:http/types@0.2.0", "wasi:io/streams@0.2.0"}},
	} {
		t.Run(c.world, func(t *testing.T) {
			w, err := DecodeWorld(c.world)
			if err != nil {
				t.Fatalf("DecodeWorld: %v", err)
			}
			comp, err := w.ComponentWithWorldImports()
			if err != nil {
				t.Fatalf("ComponentWithWorldImports: %v", err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, c.world+"-imports.wasm")
			if err := os.WriteFile(path, comp, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if out, err := exec.Command(wasmtools, "validate", path).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools validate: %v\n%s", err, out)
			}
			wit, err := exec.Command(wasmtools, "component", "wit", path).CombinedOutput()
			if err != nil {
				t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
			}
			for _, iface := range c.want {
				if !strings.Contains(string(wit), iface) {
					t.Errorf("%s: WIT missing import %q", c.world, iface)
				}
			}
		})
	}
}
