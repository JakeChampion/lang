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

// TestSelfHostExportListParamRunsViaConsumer is the self-host parity gate for
// the P6 numeric-array (`list<T>`) PARAMETER export (the Go side is
// TestExportListParamRunsViaConsumer). The self-hosted compiler emits a wrapper
// that rebuilds a self-host array ([len@0], elements at +8) from the canonical
// (ptr,len) the realloc lift materialises, then calls the Fern function; the Go
// composer lifts it with the realloc lift; and a Fern consumer that @imports
// `sum(xs: list<s32>) -> s32` calls it with [10,20,30,40] and expects 100 —
// linked with `wasm-tools compose` and run under wasmtime.
func TestSelfHostExportListParamRunsViaConsumer(t *testing.T) {
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

	// self-host emits the exporter core (command with main + list-param @export).
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

	exporterSrc := `@export("local:test/nums@0.1.0", "sum")
function sum(xs: i32[]): i32 {
	var s: i32 = 0;
	for x in xs { s = s + x; }
	return s;
}

function main(): i32 { return 0; }`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(exporterSrc))
	if !bytes.Contains(watBytes, []byte("local:test/nums@0.1.0#sum")) {
		t.Fatalf("self-host core missing the surfaced list-param @export:\n%s", watBytes)
	}
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

	// exporter world: exports nums, imports wasi:cli/stdout (run-io core).
	expWit := filepath.Join(dir, "expwit")
	if err := os.MkdirAll(expWit, 0o755); err != nil {
		t.Fatalf("mkdir expwit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(expWit, "deps"))
	if err := os.MkdirAll(filepath.Join(expWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expWit, "deps", "test", "nums.wit"),
		[]byte("package local:test@0.1.0;\ninterface nums { sum: func(xs: list<s32>) -> s32; }\n"), 0o644); err != nil {
		t.Fatalf("write nums dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expWit, "world.wit"),
		[]byte("package local:exporter@0.0.0;\nworld m {\n    import wasi:cli/stdout@0.2.0;\n    export local:test/nums@0.1.0;\n}\n"), 0o644); err != nil {
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
		t.Fatalf("ComposeExportsFromWorld (self-host list param): %v", err)
	}
	exporter := filepath.Join(dir, "exporter.wasm")
	if err := os.WriteFile(exporter, expComp, 0o644); err != nil {
		t.Fatalf("write exporter component: %v", err)
	}
	run(wasmtools, "validate", exporter)

	// Go-built consumer that imports sum(xs: list<s32>) -> s32 and checks it.
	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatalf("mkdir userwit: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	if err := os.MkdirAll(filepath.Join(userWit, "deps", "test"), 0o755); err != nil {
		t.Fatalf("mkdir deps/test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "deps", "test", "nums.wit"),
		[]byte("package local:test@0.1.0;\ninterface nums { sum: func(xs: list<s32>) -> s32; }\n"), 0o644); err != nil {
		t.Fatalf("write nums dep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWit, "world.wit"),
		[]byte("package local:userworld@0.0.0;\nworld u {\n    import wasi:cli/stdout@0.2.0;\n    import local:test/nums@0.1.0;\n}\n"), 0o644); err != nil {
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
	const want = "sum-ok"
	userSrc := `@import("local:test/nums@0.1.0", "sum")
function sum(xs: i32[]): i32;

function main(): i32 {
	var xs: i32[] = [10, 20, 30, 40];
	if (sum(xs) == 100) { write("` + want + `"); } else { write("sum-bad"); }
	return 0;
}`
	userPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(userPath, []byte(userSrc), 0o644); err != nil {
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
