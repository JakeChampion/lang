package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// runSelfHostSumTypeExportCase drives the self-host P6 sum-type result export
// flow (the Go side is runSumTypeExportCase): the self-hosted compiler emits an
// exporter core whose wrapper repacks the Fern enum box ([tag@0][payload@4])
// into the canonical (disc:u8@0, payload@4) return area; the Go composer lifts
// it; a Fern consumer `@import`s it and matches every arm, linked + run under
// wasmtime.
func runSelfHostSumTypeExportCase(t *testing.T, iface, short, fqn, dep, expFern, userFern, want string) {
	t.Helper()
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

	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_runio_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	watBytes := runCapture(t, gcc, runner, driverBin, []byte(expFern))
	expWatPath := filepath.Join(dir, "exp_core.wat")
	if err := os.WriteFile(expWatPath, watBytes, 0o644); err != nil {
		t.Fatalf("write exporter wat: %v", err)
	}
	expCorePath := filepath.Join(dir, "exp_core.wasm")
	run(wasmtools, "parse", expWatPath, "-o", expCorePath)
	expCore, err := os.ReadFile(expCorePath)
	if err != nil {
		t.Fatalf("read exporter core: %v", err)
	}

	expWit := filepath.Join(dir, "expwit")
	if err := os.MkdirAll(expWit, 0o755); err != nil {
		t.Fatalf("mkdir expwit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(expWit, "deps"))
	if err := os.MkdirAll(filepath.Join(expWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expWit, "deps", "test", dep),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expWit, "world.wit"),
		[]byte("package local:exporter@0.0.0;\nworld m {\n    import wasi:cli/stdout@0.2.0;\n    export "+fqn+";\n}\n"), 0o644); err != nil {
		t.Fatalf("write exporter wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ee.wat", "(module)"), "-o", filepath.Join(dir, "ee.wasm"))
	run(wasmtools, "component", "embed", expWit, "-w", "m", filepath.Join(dir, "ee.wasm"), "-o", filepath.Join(dir, "eembed.wasm"))
	expEmbed, err := os.ReadFile(filepath.Join(dir, "eembed.wasm"))
	if err != nil {
		t.Fatalf("read exporter embed: %v", err)
	}
	expWorld, err := componenttype.DecodeWorldBytes(extractComponentType(t, expEmbed))
	if err != nil {
		t.Fatalf("DecodeWorldBytes (exporter): %v", err)
	}
	expComp, err := component.ComposeExportsFromWorld(expCore, expWorld)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (self-host sum-type result): %v", err)
	}
	exporter := filepath.Join(dir, "exporter.wasm")
	if err := os.WriteFile(exporter, expComp, 0o644); err != nil {
		t.Fatalf("write exporter component: %v", err)
	}
	run(wasmtools, "validate", exporter)

	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatalf("mkdir userwit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", dep),
		[]byte("package local:test@0.1.0;\n"+iface+"\n"), 0o644); err != nil {
		t.Fatalf("write dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import "+fqn+";\n}\n"), 0o644); err != nil {
		t.Fatalf("write user world: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ue.wat", "(module)"), "-o", filepath.Join(dir, "ue.wasm"))
	run(wasmtools, "component", "embed", userWit, "-w", "u", filepath.Join(dir, "ue.wasm"), "-o", filepath.Join(dir, "uembed.wasm"))
	userEmbed, err := os.ReadFile(filepath.Join(dir, "uembed.wasm"))
	if err != nil {
		t.Fatalf("read user embed: %v", err)
	}
	userWorld, err := componenttype.DecodeWorldBytes(extractComponentType(t, userEmbed))
	if err != nil {
		t.Fatalf("DecodeWorldBytes (user): %v", err)
	}
	userPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(userPath, []byte(userFern), 0o644); err != nil {
		t.Fatalf("write consumer prog: %v", err)
	}
	userInfo, userProg := loadCheckMono(t, userPath)
	userCore, err := wasmbin.BuildWithOptions(userProg, userInfo, wasmbin.BuildOptions{
		ForceMemorySection: true, Preview2WASI: true, SynthCliRun: true, CliRunResult: true,
	})
	if err != nil {
		t.Fatalf("build consumer core: %v", err)
	}
	userComp, err := component.ComposeFromWorldAuto(userCore, userWorld)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto (consumer): %v", err)
	}
	consumer := filepath.Join(dir, "consumer.wasm")
	if err := os.WriteFile(consumer, userComp, 0o644); err != nil {
		t.Fatalf("write consumer component: %v", err)
	}

	final := filepath.Join(dir, "final.wasm")
	run(wasmtools, "compose", consumer, "--definitions", exporter, "-o", final)
	run(wasmtools, "validate", final)
	out, err := exec.Command(wasmtime, "run", final).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}

// TestSelfHostExportOptionResultRunsViaConsumer is the self-host parity gate for
// the Option result export (the remapped discriminant).
func TestSelfHostExportOptionResultRunsViaConsumer(t *testing.T) {
	const exp = `@export("local:test/maybe@0.1.0", "half")
function half(n: i32): Option[i32] {
	if (n % 2 == 0) { return Some(n / 2); }
	return None;
}

function main(): i32 { return 0; }`
	const user = `@import("local:test/maybe@0.1.0", "half")
function half(n: i32): Option[i32];

function main(): i32 {
	var sm: i32 = 0;
	match (half(10)) { Some(v) => { sm = v; }, None => { sm = -100; } }
	var nn: i32 = 0;
	match (half(3)) { Some(v) => { nn = 99; }, None => { nn = 7; } }
	if (sm == 5 && nn == 7) { write("opt-ok"); } else { write("opt-bad"); }
	return 0;
}`
	runSelfHostSumTypeExportCase(t,
		"interface maybe { half: func(n: s32) -> option<s32>; }",
		"maybe", "local:test/maybe@0.1.0", "maybe.wit", exp, user, "opt-ok")
}

// TestSelfHostExportResultResultRunsViaConsumer is the self-host parity gate for
// the Result result export (no discriminant remap).
func TestSelfHostExportResultResultRunsViaConsumer(t *testing.T) {
	const exp = `@export("local:test/checked@0.1.0", "checked-div")
function checked_div(a: i32, b: i32): Result[i32, i32] {
	if (b == 0) { return Err(-1); }
	return Ok(a / b);
}

function main(): i32 { return 0; }`
	const user = `@import("local:test/checked@0.1.0", "checked-div")
function checked_div(a: i32, b: i32): Result[i32, i32];

function main(): i32 {
	var ok: i32 = 0;
	match (checked_div(20, 4)) { Ok(v) => { ok = v; }, Err(e) => { ok = -100; } }
	var er: i32 = 0;
	match (checked_div(1, 0)) { Ok(v) => { er = 99; }, Err(e) => { er = e; } }
	if (ok == 5 && er == -1) { write("res-ok"); } else { write("res-bad"); }
	return 0;
}`
	runSelfHostSumTypeExportCase(t,
		"interface checked { checked-div: func(a: s32, b: s32) -> result<s32, s32>; }",
		"checked", "local:test/checked@0.1.0", "checked.wit", exp, user, "res-ok")
}
