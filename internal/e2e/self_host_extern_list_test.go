package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExternListResultRunsUnderWasmtime is the self-host P4c gate for
// composite results (docs/WIT-BRING-YOUR-OWN.md): the self-hosted wasm backend
// lowers a string/list<u8>-returning `@import` extern via the canonical
// return-area convention — the raw import gains a trailing return-area pointer
// (no core result), and a generated wrapper lifts the host (data,len) bytes
// into a self-host `[len][bytes]` string, with cabi_realloc exported. Mirror of
// the Go gate TestExternListResultRunsUnderWasmtime.
//
// `get-random-bytes: func(len: u64) -> list<u8>` declared as `: string` must
// produce a string of exactly the requested byte length (deterministic despite
// the random contents).
func TestSelfHostExternListResultRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
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
	if err := os.WriteFile(filepath.Join(dir, "extern_run.fern"), []byte(driver), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "extern_run.fern", "extern_run")

	const want = "len-ok"
	prog := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): string;
function main(): i32 {
    // Pre-allocate on the heap so the bump cursor is at an odd offset; the
    // wrapper's return area must still be aligned (regression guard for the
    // canonical "pointer not aligned" trap).
    var pad: string = string_from_bytes([65 as u8]);
    var s: string = rand_bytes(16 as u64);
    if (pad.len() == 1 && s.len() == 16) { write("` + want + `"); } else { write("len-bad"); }
    return 0;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	if !bytes.Contains(watBytes, []byte("get-random-bytes")) {
		t.Errorf("emitted core is missing the extern import")
	}
	if !bytes.Contains(watBytes, []byte("cabi_realloc")) {
		t.Errorf("emitted core does not export cabi_realloc")
	}

	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "extern-list.component.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
