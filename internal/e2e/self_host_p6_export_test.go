package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostExportAttributeCompiles is the self-host parity gate for P6
// slice 1 (docs/WIT-BRING-YOUR-OWN.md): the self-host parser accepts the
// `@export("iface","wit-name")` attribute on a function and compiles the
// program. Slice 1 parses + consumes the binding (the export lift lands with
// the codegen slice), so the exported function compiles as an ordinary
// function — here it's called from main and the self-host emits a working core.
func TestSelfHostExportAttributeCompiles(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	const driver = `import "core/no_prelude";
import "std/io";
import "./lexer";
import "./parser";
import "./wasm";

function main(): i32 {
    var src: string = io.read_all_stdin();
    var mod: parser.Module = parser.parse_module(lexer.tokenize(src));
    write(wasm.emit_module_run_io(parser.module_with_builtins(mod)));
    return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "exp_run.fern"), []byte(driver), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "exp_run.fern", "exp_run")

	// An `@export` function, also called from main. The self-host must parse
	// the attribute and compile the program.
	prog := `@export("wasi:cli/run@0.2.0", "run")
function run(): i32 { return 42; }

function main(): i32 { return run(); }`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes for an @export program")
	}
	if !bytes.Contains(watBytes, []byte("$run")) {
		t.Errorf("emitted core is missing the @export function $run:\n%s", watBytes)
	}
}
