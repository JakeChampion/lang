package componenttype

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestEmitInterfaceTypeImportRejectsShared(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	if _, err := w.EmitInterfaceTypeImport("wasi:io/streams@0.2.0"); err == nil {
		t.Fatal("expected io/streams emission to be refused (shared types), got nil error")
	}
}

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

func TestPrefixLayout(t *testing.T) {
	w, err := DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	pl := w.PrefixLayout()
	if pl.Types != 32 || pl.Instances != 19 {
		t.Errorf("PrefixLayout = {Types:%d Instances:%d}, want {32 19}", pl.Types, pl.Instances)
	}
	for name, want := range map[string]int{
		"wasi:io/error@0.2.0":      0,
		"wasi:io/streams@0.2.0":    2,
		"wasi:cli/stdout@0.2.0":    4,
		"wasi:cli/stderr@0.2.0":    5,
		"wasi:random/random@0.2.0": 18,
		"wasi:not/a-thing@9.9.9":   -1,
	} {
		if got := w.ImportInstanceIndex(name); got != want {
			t.Errorf("ImportInstanceIndex(%q) = %d, want %d", name, got, want)
		}
	}

	secs, err := w.EmitWorldImports()
	if err != nil {
		t.Fatalf("EmitWorldImports: %v", err)
	}
	var nTypeAlias, nImport uint32
	for pos := 0; pos < len(secs); {
		id := secs[pos]
		pos++
		size, n, err := readULEB(secs[pos:])
		if err != nil {
			t.Fatalf("section size: %v", err)
		}
		pos += n + int(size)
		switch id {
		case compSecType, compSecAlias:
			nTypeAlias++
		case compSecImport:
			nImport++
		}
	}
	if nTypeAlias != pl.Types {
		t.Errorf("emitted type+alias sections = %d, PrefixLayout.Types = %d", nTypeAlias, pl.Types)
	}
	if nImport != pl.Instances {
		t.Errorf("emitted import sections = %d, PrefixLayout.Instances = %d", nImport, pl.Instances)
	}
}
