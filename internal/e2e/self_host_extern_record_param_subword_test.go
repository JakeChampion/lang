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

// TestSelfHostExternRecordParamSubwordCustomProvider is the self-host port of
// the sub-word record-field parameter (docs/WIT-BRING-YOUR-OWN.md, Go-side
// TestExternRecordParamSubwordCustomProvider). A self-host struct stores every
// sub-64-bit int in a 4-byte slot, but the canonical ABI flattens an s8/u16
// field to a single sign-/zero-extended i32. So the self-host wrapper reads each
// sub-word field with a width+sign-aware load (i32.load8_s for s8, i32.load16_u
// for u16, via extern_field_load_op) to produce the correct i32.
//
// The provider exports `sum-mix: func(p: record { a: s8, b: u16, c: s32 }) ->
// s32` summing the flattened (a, b, c). Values fail under the wrong load:
// a = -5 (needs load8_s, not load8_u → 251), b = 300 (needs load16_u, not
// load8_u → 44). Expected -5 + 300 + 1000 = 1295.
func TestSelfHostExternRecordParamSubwordCustomProvider(t *testing.T) {
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
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	provWit := filepath.Join(dir, "provwit")
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface sink { record mix { a: s8, b: u16, c: s32 } sum-mix: func(p: mix) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// sum-mix receives the record flattened to (a, b, c) — three i32 params (the
	// canonical ABI sign-/zero-extends s8/u16 to i32) — and returns their sum.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/sink@0.1.0#sum-mix") (param $a i32) (param $b i32) (param $c i32) (result i32)
    (i32.add (i32.add (local.get $a) (local.get $b)) (local.get $c))))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", provWit, "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps")).CombinedOutput(); err != nil {
		_ = os.MkdirAll(userWit, 0o755)
		run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
		_ = out
	}
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write user sink dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/sink@0.1.0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write user world: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "wasm.fern"} {
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
	if err := os.WriteFile(filepath.Join(dir, "sw_run.fern"), []byte(driver), 0o644); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "sw_run.fern", "sw_run")

	const want = "mix-ok"
	prog := `struct Mix { a: i8, b: u16, c: i32 }
@import("local:test/sink@0.1.0", "sum-mix")
function sum_mix(p: Mix): i32;
function main(): i32 {
    var p: Mix = Mix { a: 0 - 5 as i8, b: 300 as u16, c: 1000 };
    if (sum_mix(p) == 1295) { write("` + want + `"); } else { write("mix-bad"); }
    return 0;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	run(wasmtools, "parse", watPath, "-o", corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	userComp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	userPath := filepath.Join(dir, "user.wasm")
	if err := os.WriteFile(userPath, userComp, 0o644); err != nil {
		t.Fatalf("write user component: %v", err)
	}
	final := filepath.Join(dir, "final.wasm")
	run(wasmtools, "compose", userPath, "--definitions", provider, "-o", final)
	run(wasmtools, "validate", final)
	out, err := exec.Command(wasmtime, "run", final).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
