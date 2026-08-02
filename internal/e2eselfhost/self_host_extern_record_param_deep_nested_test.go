package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExternRecordParamDeepNestedCustomProvider is the self-host port of
// the deeper-nesting (arbitrary depth) record *parameter* gate
// (docs/WIT-BRING-YOUR-OWN.md, Go-side TestExternRecordParamDeepNestedCustomProvider).
// A self-host struct whose field is a struct whose field is itself a struct is
// passed to an `@import` extern; the canonical ABI flattens every nested record
// inline, so the leaves arrive as positional core args in declaration order. A
// self-host struct field of struct type is a pointer, so a leaf nested N levels
// deep is reached by deref-ing N inner value pointers then loading the leaf —
// emitted by the recursive extern_emit_record_param_leaves.
//
// Shape: `record outer { l: mid, r: mid }`, `record mid { p: point, n: s32 }`,
// `record point { x: s32, y: s32 }` — three levels. `sum-outer: func(o: outer) ->
// s32` weights the six leaves so ordering (hence the deref recursion) is checked.
func TestSelfHostExternRecordParamDeepNestedCustomProvider(t *testing.T) {
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
	const iface = "interface sink { record point { x: s32, y: s32 } record mid { p: point, n: s32 } record outer { l: mid, r: mid } sum-outer: func(o: outer) -> s32; }"
	if err := os.WriteFile(filepath.Join(provWit, "sink.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// sum-outer receives the doubly-nested record flattened to six i32 params in
	// declaration order (l.p.x, l.p.y, l.n, r.p.x, r.p.y, r.n) and returns the
	// weighted sum a + b*10 + c*100 + d*1000 + e*10000 + f*100000.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (func (export "local:test/sink@0.1.0#sum-outer") (param $a i32) (param $b i32) (param $c i32) (param $d i32) (param $e i32) (param $f i32) (result i32)
    (i32.add (i32.add (i32.add (local.get $a) (i32.mul (local.get $b) (i32.const 10)))
                      (i32.add (i32.mul (local.get $c) (i32.const 100)) (i32.mul (local.get $d) (i32.const 1000))))
             (i32.add (i32.mul (local.get $e) (i32.const 10000)) (i32.mul (local.get $f) (i32.const 100000))))))`), 0o644); err != nil {
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

	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "od-ok"
	prog := `struct Point { x: i32, y: i32 }
struct Mid { p: Point, n: i32 }
struct Outer { l: Mid, r: Mid }
@import("local:test/sink@0.1.0", "sum-outer")
function sum_outer(o: Outer): i32;
function main(): i32 {
    var o: Outer = Outer { l: Mid { p: Point { x: 1, y: 2 }, n: 3 }, r: Mid { p: Point { x: 4, y: 5 }, n: 6 } };
    // 1 + 2*10 + 3*100 + 4*1000 + 5*10000 + 6*100000 = 654321
    if (sum_outer(o) == 654321) { write("` + want + `"); } else { write("od-bad"); }
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
