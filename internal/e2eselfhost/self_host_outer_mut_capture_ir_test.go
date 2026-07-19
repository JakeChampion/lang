package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// outerMutCaptureIRCases pin #5301 (+ #5300's outer-mutated-scalar shapes):
// a closure capturing an outer local that the ENCLOSING scope reassigns —
// before or after the closure is created — must observe the current binding
// at each call, matching the interpreter's by-reference capture semantics
// (the oracle per #2896). Pointer captures (array / string / struct) used to
// snapshot by value on every compiled path (native 10/38/10 where interp
// returned 42); outer-mutated scalars additionally BAILED the self-host IR
// path to AST, which snapshots at make time too. Native now boxes such
// captures into the shared 1-element cell (closureconv.boxableCapture admits
// ast.IsPointerType); the self-host param-lift no longer declines a capture
// reassigned in the enclosing body — captures are passed at each CALL SITE,
// which reads the current binding, exactly the oracle's semantics (closure-
// side writes are covered by the scalar box pass and E049).
//
// All wants are the interpreter's exit codes (kept < 126 for the wasm leg).
var outerMutCaptureIRCases = []struct {
	name string
	src  string
	want int
}{
	// The #5301 repro table: array / string / struct reassigned AFTER the
	// closure is created; the closure must read the new binding.
	{"array-reassign",
		`function main(): i32 {
    var a: i32[] = [10, 1];
    var f: () => i32 = function (): i32 { return a[0]; };
    a = [42, 1];
    return f();
}`, 42},
	{"string-reassign",
		`function main(): i32 {
    var s: string = "aa";
    var f: () => i32 = function (): i32 { return s.len(); };
    s = "abcdef";
    return f() + 36;
}`, 42},
	{"struct-reassign",
		`struct B { v: i32 }
function main(): i32 {
    var b: B = B { v: 10 };
    var f: () => i32 = function (): i32 { return b.v; };
    b = B { v: 42 };
    return f();
}`, 42},
	// The scalar control row — also by-reference (bailed to AST pre-fix).
	{"i32-outer-reassign",
		`function main(): i32 {
    var n: i32 = 10;
    var f: () => i32 = function (): i32 { return n; };
    n = 42;
    return f();
}`, 42},
	// #5300's shapes: an outer-scope mutation BEFORE the closure is created
	// (bailed the function to AST), and the loop-accumulator idiom where the
	// closure both writes one capture (boxed cell) and reads another that the
	// loop keeps advancing (0+1+2+3+4... via the live counter).
	{"outer-mutate-then-capture",
		`function main(): i32 {
    var total: i32 = 0;
    total = total + 15;
    var f: () => i32 = function (): i32 { return total + 27; };
    return f();
}`, 42},
	{"loop-accumulator",
		`function main(): i32 {
    var s: i32 = 0;
    var i: i32 = 0;
    var add: () => i32 = function (): i32 { s = s + i; return 0; };
    while (i < 4) {
        i = i + 1;
        var r: i32 = add();
    }
    return s + 32;
}`, 42},
	// Both directions on one capture: the closure writes it (boxed cell), the
	// outer scope also reassigns it — one shared cell, writes visible both ways.
	{"outer-and-inner-write",
		`function main(): i32 {
    var x: i32 = 0;
    var f: () => i32 = function (): i32 { x = x + 4; return 0; };
    x = 3;
    var r: i32 = f();
    return x + 35;
}`, 42},
	// A `.with` self-reassign on a soon-captured array (#5300's hand-boxed
	// repro — the aliasing shape that used to bail the whole function).
	{"with-selfreassign-then-capture",
		`function main(): i32 {
    var a: i32[] = [0, 1];
    a = a.with(0, 42);
    var f: () => i32 = function (): i32 { return a[0]; };
    return f();
}`, 42},
	// RC guard: reassign the captured array to another still-live local and
	// back; both bindings stay readable (an over-release corrupts one).
	{"alias-reassign-live",
		`function main(): i32 {
    var keep: i32[] = [40, 7];
    var a: i32[] = [10, 1];
    var f: () => i32 = function (): i32 { return a[0]; };
    a = keep;
    a = [1, 2];
    a = keep;
    var x: i32 = f();
    var y: i32 = keep[0];
    return x + y - 38;
}`, 42},
	// A loop growing the captured string 40 times — the closure reads the
	// final value (also exercises repeated frees of the superseded strings).
	{"loop-string-grow",
		`function main(): i32 {
    var s: string = "x";
    var f: () => i32 = function (): i32 { return s.len(); };
    var i: i32 = 0;
    while (i < 40) {
        s = s + "y";
        i = i + 1;
    }
    return f() + 1;
}`, 42},
	// #5394: ESCAPING closures (returned — the make_closure env-box path).
	// The env used to snapshot the capture at creation; the capture now boxes
	// into a shared cell (any type), so the outer reassignment stores through
	// the cell and the escaped closure reads the live value. (Closures stored
	// in ARRAY containers are excluded here — that shape hits the pre-existing
	// closure-array + array-capture crash #5405, unrelated to reassignment.)
	{"escape-array-reassign",
		`function mk(): () => i32 {
    var a: i32[] = [10, 1];
    var f: () => i32 = function (): i32 { return a[0]; };
    a = [42, 1];
    return f;
}
function main(): i32 {
    var g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-string-reassign",
		`function mk(): () => i32 {
    var s: string = "aa";
    var f: () => i32 = function (): i32 { return s.len(); };
    s = "abcdef";
    return f;
}
function main(): i32 {
    var g: () => i32 = mk();
    return g() + 36;
}`, 42},
	{"escape-struct-reassign",
		`struct B { v: i32 }
function mk(): () => i32 {
    var b: B = B { v: 10 };
    var f: () => i32 = function (): i32 { return b.v; };
    b = B { v: 42 };
    return f;
}
function main(): i32 {
    var g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-i32-reassign",
		`function mk(): () => i32 {
    var n: i32 = 10;
    var f: () => i32 = function (): i32 { return n; };
    n = 42;
    return f;
}
function main(): i32 {
    var g: () => i32 = mk();
    return g();
}`, 42},
	// The escaped closure also WRITES the shared cell: outer sets 38 before
	// returning f; each call adds 2 — the second call reads the first call's
	// write through the same cell (40 + 2 = 42).
	{"escape-mixed-write",
		`function mk(): () => i32 {
    var n: i32 = 0;
    var f: () => i32 = function (): i32 { n = n + 2; return n; };
    n = 38;
    return f;
}
function main(): i32 {
    var g: () => i32 = mk();
    var r1: i32 = g();
    return g();
}`, 42},
}

// TestSelfHostOuterMutCaptureIRX86_64 cross-checks native (now oracle-
// matching), pins the "ir" routing (these shapes all bailed to AST before),
// then runs the self-host-compiled binary.
func TestSelfHostOuterMutCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range outerMutCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
				t.Fatalf("%s native exited %d, want %d (interp oracle)", tc.name, code, tc.want)
			}
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "omc-"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host IR exited %d, want %d (10/38 = stale by-value snapshot, #5301)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostOuterMutCaptureWasmIR runs the same cases through the wasm IR
// backend (the lift is shared, so the call-site capture pass covers wasm too).
func TestSelfHostOuterMutCaptureWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping outer-mut-capture wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range outerMutCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s wasm IR exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOuterMutCaptureIRArm64 runs the pointer rows and the mixed
// write case under qemu via `asm_ir_run -ir -target arm64`.
func TestSelfHostOuterMutCaptureIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range outerMutCaptureIRCases {
		switch tc.name {
		case "array-reassign", "string-reassign", "struct-reassign", "outer-and-inner-write",
			"escape-array-reassign", "escape-mixed-write":
		default:
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-ir", "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "omc-"+tc.name+"-arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s arm64 IR exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
