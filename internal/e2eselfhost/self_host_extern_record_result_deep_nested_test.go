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

// TestSelfHostExternRecordResultDeepNestedCustomProvider is the self-host port of
// the deeper-nesting (arbitrary depth) record `@import` *result* gate
// (docs/WIT-BRING-YOUR-OWN.md, Go-side TestExternRecordResultDeepNestedCustomProvider).
// An extern returns a record whose field is a record whose field is itself a
// record, lifted into a self-host struct tree. The canonical ABI inlines every
// nested record's leaves into the return area at each level's alignment;
// extern_record_nestable/extern_record_leaf_count recurse the gate,
// extern_canon_* recurse the layout, and extern_emit_record_fill recurses the
// materialization (one $inner local per nesting level via extern_record_depth).
//
// Shape: `record outer { l: mid, r: mid }`, `record mid { p: point, n: s32 }`,
// `record point { x: s32, y: s32 }` — three levels (outer→mid→point). The
// provider fills eight s32 leaves; the Fern side reads them through `o.l.p.x` …
// `o.r.n` and forms a checkable weighted sum.
func TestSelfHostExternRecordResultDeepNestedCustomProvider(t *testing.T) {
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

	// --- Provider component exporting local:test/src@0.1.0 make-outer. ---
	provWit := filepath.Join(dir, "provwit")
	if err := os.MkdirAll(provWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const iface = "interface src { record point { x: s32, y: s32 } record mid { p: point, n: s32 } record outer { l: mid, r: mid } make-outer: func(a: s32, b: s32, c: s32, d: s32, e: s32, f: s32, g: s32, h: s32) -> outer; }"
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	// make-outer returns the nested record indirectly: a 24-byte area, the eight
	// s32 leaves inlined in declaration order (l.p.x@0, l.p.y@4, l.n@8, r.p.x@12,
	// r.p.y@16, r.n@20) via cabi_realloc, returning its pointer.
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#make-outer") (param $a i32) (param $b i32) (param $c i32) (param $d i32) (param $e i32) (param $f i32) (param $g i32) (param $hh i32) (result i32)
    (local $r i32)
    (local.set $r (call 0 (i32.const 0) (i32.const 0) (i32.const 4) (i32.const 24)))
    (i32.store         (local.get $r) (local.get $a))
    (i32.store offset=4  (local.get $r) (local.get $b))
    (i32.store offset=8  (local.get $r) (local.get $c))
    (i32.store offset=12 (local.get $r) (local.get $d))
    (i32.store offset=16 (local.get $r) (local.get $e))
    (i32.store offset=20 (local.get $r) (local.get $f))
    (local.get $r)))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", provWit, "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	// --- User world (custom src iface + wasi stdout). ---
	userWit := filepath.Join(dir, "userwit")
	if out, err := exec.Command("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps")).CombinedOutput(); err != nil {
		_ = os.MkdirAll(userWit, 0o755)
		run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
		_ = out
	}
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write user src dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/src@0.1.0;\n}\n"), 0o644); err != nil {
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

	// --- Self-host backend: emit the core from the deep-nested-result program. ---
	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "deep-ok"
	prog := `struct Point { x: i32, y: i32 }
struct Mid { p: Point, n: i32 }
struct Outer { l: Mid, r: Mid }
@import("local:test/src@0.1.0", "make-outer")
function make_outer(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): Outer;
function main(): i32 {
    var o: Outer = make_outer(1, 2, 3, 4, 5, 6, 7, 8);
    // l.p.x=1, l.p.y=2, l.n=3, r.p.x=4, r.p.y=5, r.n=6
    // 1 + 2*10 + 3*100 + 4*1000 + 5*10000 + 6*100000 = 654321
    if (o.l.p.x + o.l.p.y * 10 + o.l.n * 100 + o.r.p.x * 1000 + o.r.p.y * 10000 + o.r.n * 100000 == 654321) { write("` + want + `"); } else { write("deep-bad"); }
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
