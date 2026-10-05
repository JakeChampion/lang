package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExternRecordResultWallClock reads wasi:clocks/wall-clock's `now`,
// a `datetime { seconds: u64, nanoseconds: u32 }` returned through a return
// area with the u32 at offset 8. The import used to be declared returning an
// i32, which no host signature matches.
func TestSelfHostExternRecordResultWallClock(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(`import "std/u64";
import "std/u32";
struct Datetime { seconds: u64, nanoseconds: u32 }
@import("wasi:clocks/wall-clock@0.2.0", "now")
function wall_now(): Datetime;
function main(): i32 {
    let d: Datetime = wall_now();
    print(d.seconds.to_string() + " " + d.nanoseconds.to_string() + "\n");
    return 0;
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_LEAKCHECK=1"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		t.Fatalf("stdout = %q, want seconds and nanoseconds", out)
	}
	secs, err1 := strconv.ParseInt(fields[0], 10, 64)
	nanos, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("stdout = %q: %v %v", out, err1, err2)
	}
	if d := secs - time.Now().Unix(); d < -60 || d > 60 {
		t.Errorf("seconds = %d, %d s from the host clock", secs, d)
	}
	if nanos < 0 || nanos >= 1e9 {
		t.Errorf("nanoseconds = %d, want [0, 1e9)", nanos)
	}
	assertBalancedCensus(t, stderr.String())
}

// TestSelfHostExternRecordWideFieldsCustomProvider passes records whose
// fields are 64-bit integers and floats to and from a provider component:
//   - `wide` mixes a u8, a u64, an f32, an f64 and an s32, so each field sits
//     at its own alignment (0, 8, 16, 24, 32) in a 40-byte return area;
//   - `outer` nests a record { f32, u8 } ahead of an s64;
//   - `one` and `onef`, a lone u64 and a lone f32, come back by value as an
//     i64 and an f32;
//   - `wide` and `outer` go back as parameters, flattened to their leaves.
func TestSelfHostExternRecordWideFieldsCustomProvider(t *testing.T) {
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
	const iface = `interface src {
  record wide { a: u8, b: u64, c: f32, d: f64, e: s32 }
  record inner { x: f32, y: u8 }
  record outer { p: inner, n: s64 }
  record one { v: u64 }
  record onef { v: f32 }
  make-wide: func() -> wide;
  make-outer: func() -> outer;
  make-one: func() -> one;
  make-onef: func() -> onef;
  sum-wide: func(w: wide) -> f64;
  sum-outer: func(o: outer) -> f64;
}`
	if err := os.WriteFile(filepath.Join(provWit, "src.wit"),
		[]byte("package local:test@0.1.0;\n"+iface+"\nworld provider { export src; }\n"), 0o644); err != nil {
		t.Fatalf("write provider wit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prov_core.wat"), []byte(`(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/src@0.1.0#make-wide") (result i32)
    (local $r i32)
    (local.set $r (call 0 (i32.const 0) (i32.const 0) (i32.const 8) (i32.const 40)))
    (i32.store8 (local.get $r) (i32.const 7))
    (i64.store offset=8 (local.get $r) (i64.const 1099511627781))
    (f32.store offset=16 (local.get $r) (f32.const 1.5))
    (f64.store offset=24 (local.get $r) (f64.const -2.25))
    (i32.store offset=32 (local.get $r) (i32.const -9))
    (local.get $r))
  (func (export "local:test/src@0.1.0#make-outer") (result i32)
    (local $r i32)
    (local.set $r (call 0 (i32.const 0) (i32.const 0) (i32.const 8) (i32.const 16)))
    (f32.store (local.get $r) (f32.const 0.75))
    (i32.store8 offset=4 (local.get $r) (i32.const 200))
    (i64.store offset=8 (local.get $r) (i64.const -4294967296))
    (local.get $r))
  (func (export "local:test/src@0.1.0#make-one") (result i64)
    (i64.const 1099511627781))
  (func (export "local:test/src@0.1.0#make-onef") (result f32)
    (f32.const 1.5))
  (func (export "local:test/src@0.1.0#sum-wide") (param $a i32) (param $b i64) (param $c f32) (param $d f64) (param $e i32) (result f64)
    (f64.add (f64.add (f64.add (f64.add (f64.convert_i32_u (local.get $a)) (f64.convert_i64_u (local.get $b)))
      (f64.promote_f32 (local.get $c))) (local.get $d)) (f64.convert_i32_s (local.get $e))))
  (func (export "local:test/src@0.1.0#sum-outer") (param $x f32) (param $y i32) (param $n i64) (result f64)
    (f64.add (f64.add (f64.promote_f32 (local.get $x)) (f64.convert_i32_u (local.get $y))) (f64.convert_i64_s (local.get $n)))))`), 0o644); err != nil {
		t.Fatalf("write provider core: %v", err)
	}
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", provWit, "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
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

	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	prog := `struct Wide { a: u8, b: u64, c: f32, d: f64, e: i32 }
struct Inner { x: f32, y: u8 }
struct Outer { p: Inner, n: i64 }
struct One { v: u64 }
struct OneF { v: f32 }
@import("local:test/src@0.1.0", "make-wide")
function make_wide(): Wide;
@import("local:test/src@0.1.0", "make-outer")
function make_outer(): Outer;
@import("local:test/src@0.1.0", "make-one")
function make_one(): One;
@import("local:test/src@0.1.0", "make-onef")
function make_onef(): OneF;
@import("local:test/src@0.1.0", "sum-wide")
function sum_wide(w: Wide): f64;
@import("local:test/src@0.1.0", "sum-outer")
function sum_outer(o: Outer): f64;
function main(): i32 {
    let w: Wide = make_wide();
    if (w.a != 7 as u8) { write("bad a\n"); }
    if (w.b != 1099511627781 as u64) { write("bad b\n"); }
    if ((w.c as f64) != 1.5) { write("bad c\n"); }
    if (w.d != 0.0 - 2.25) { write("bad d\n"); }
    if (w.e != 0 - 9) { write("bad e\n"); }
    let o: Outer = make_outer();
    if ((o.p.x as f64) != 0.75) { write("bad p.x\n"); }
    if (o.p.y != 200 as u8) { write("bad p.y\n"); }
    if (o.n != (0 - 4294967296) as i64) { write("bad n\n"); }
    if (make_one().v != 1099511627781 as u64) { write("bad one\n"); }
    if ((make_onef().v as f64) != 1.5) { write("bad onef\n"); }
    if (sum_wide(w) != 1099511627778.25) { write("bad sum-wide\n"); }
    if (sum_outer(o) != 0.0 - 4294967095.25) { write("bad sum-outer\n"); }
    write("wide-done\n");
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
	if string(out) != "wide-done\n" {
		t.Fatalf("stdout = %q, want only %q", out, "wide-done\n")
	}
	if !bytes.Contains(watBytes, []byte(`"make-one" (func $make_one__import (result i64))`)) {
		t.Errorf("make-one is not imported returning its lone u64 by value")
	}
}

// TestSelfHostExternRecordOutsideTheBridgeIsRefused compiles an `@import` whose
// record carries a string, as a result and as a parameter. The bridge flattens
// only scalar leaves, so the build stops with a diagnostic naming the import
// rather than declaring a core import no host signature matches.
func TestSelfHostExternRecordOutsideTheBridgeIsRefused(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, decl, call, want string }{
		{"result", "function make_named(): Named;", "make_named().id", "@import make_named returns record Named, which wasm cannot lower"},
		{"param", "function take_named(n: Named): i32;", `take_named(Named { id: 1, name: "a" })`, "@import take_named takes record Named, which wasm cannot lower"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			prog := "struct Named { id: i32, name: string }\n@import(\"local:test/src@0.1.0\", \"f\")\n" + tc.decl + "\nfunction main(): i32 { return " + tc.call + "; }\n"
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, form := range [][]string{nil, {"-emit", "core-module"}} {
				args := append([]string{"-target", "wasm32-wasi"}, form...)
				args = append(args, "-o", filepath.Join(dir, "main.wasm"), src, cli.stdlib)
				out, err := runX86_64Bin(cli.runner, cli.bin, args...).CombinedOutput()
				if err == nil {
					t.Fatalf("%v: compiled; want a refusal", form)
				}
				if !strings.Contains(string(out), tc.want) {
					t.Fatalf("%v: output = %q, want it to contain %q", form, out, tc.want)
				}
			}
		})
	}
}
