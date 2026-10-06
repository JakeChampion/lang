package e2ecompiler

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
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    a = [42, 1];
    return f();
}`, 42},
	{"string-reassign",
		`function main(): i32 {
    let s: string = "aa";
    let f: () => i32 = (): i32 => { return s.len(); };
    s = "abcdef";
    return f() + 36;
}`, 42},
	{"struct-reassign",
		`struct B { v: i32 }
function main(): i32 {
    let b: B = B { v: 10 };
    let f: () => i32 = (): i32 => { return b.v; };
    b = B { v: 42 };
    return f();
}`, 42},
	// The scalar control row — also by-reference (bailed to AST pre-fix).
	{"i32-outer-reassign",
		`function main(): i32 {
    let n: i32 = 10;
    let f: () => i32 = (): i32 => { return n; };
    n = 42;
    return f();
}`, 42},
	// #5300's shapes: an outer-scope mutation BEFORE the closure is created
	// (bailed the function to AST), and the loop-accumulator idiom where the
	// closure both writes one capture (boxed cell) and reads another that the
	// loop keeps advancing (0+1+2+3+4... via the live counter).
	{"outer-mutate-then-capture",
		`function main(): i32 {
    let total: i32 = 0;
    total = total + 15;
    let f: () => i32 = (): i32 => { return total + 27; };
    return f();
}`, 42},
	{"loop-accumulator",
		`function main(): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    let add: () => i32 = (): i32 => { s = s + i; return 0; };
    while (i < 4) {
        i = i + 1;
        let r: i32 = add();
    }
    return s + 32;
}`, 42},
	// Both directions on one capture: the closure writes it (boxed cell), the
	// outer scope also reassigns it — one shared cell, writes visible both ways.
	{"outer-and-inner-write",
		`function main(): i32 {
    let x: i32 = 0;
    let f: () => i32 = (): i32 => { x = x + 4; return 0; };
    x = 3;
    let r: i32 = f();
    return x + 35;
}`, 42},
	// A `.with` self-reassign on a soon-captured array (#5300's hand-boxed
	// repro — the aliasing shape that used to bail the whole function).
	{"with-selfreassign-then-capture",
		`function main(): i32 {
    let a: i32[] = [0, 1];
    a = a.with(0, 42);
    let f: () => i32 = (): i32 => { return a[0]; };
    return f();
}`, 42},
	// RC guard: reassign the captured array to another still-live local and
	// back; both bindings stay readable (an over-release corrupts one).
	{"alias-reassign-live",
		`function main(): i32 {
    let keep: i32[] = [40, 7];
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    a = keep;
    a = [1, 2];
    a = keep;
    let x: i32 = f();
    let y: i32 = keep[0];
    return x + y - 38;
}`, 42},
	// A loop growing the captured string 40 times — the closure reads the
	// final value (also exercises repeated frees of the superseded strings).
	{"loop-string-grow",
		`function main(): i32 {
    let s: string = "x";
    let f: () => i32 = (): i32 => { return s.len(); };
    let i: i32 = 0;
    while (i < 40) {
        s = s + "y";
        i = i + 1;
    }
    return f() + 1;
}`, 42},
	// #5394: ESCAPING closures (returned — the make_closure env-box path).
	// The env used to snapshot the capture at creation; the capture now boxes
	// into a shared cell (any type), so the outer reassignment stores through
	// the cell and the escaped closure reads the live value.
	// #10129: the capture is declared in a LOOP body. The cell-read desugar
	// counted a `for` statement's whole body as the loop's own binders, so the
	// cell was both a cell and a non-cell there and was left undesugared; the
	// append on its element then lowered as `i32.append`.
	{"escape-loop-body-array",
		`function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
function main(): i32 {
    let total: i32 = 0;
    let ks: i32[] = [20, 20];
    for k in ks {
        let seen: string[] = [];
        seen = seen.append("a");
        function rw(x: i32): i32 { return x + seen.len(); }
        total = total + apply(k, rw);
    }
    return total;
}`, 42},
	{"escape-loop-body-array-inner-loop",
		`struct P { name: string }
struct F { ps: P[] }
function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
function main(): i32 {
    let fs: F[] = [F { ps: [P { name: "a" }, P { name: "b" }] }, F { ps: [] }];
    let total: i32 = 38;
    for fd in fs {
        let names: string[] = [];
        for pd in fd.ps { names = names.append(pd.name); }
        function rw(x: i32): i32 { return x + names.len(); }
        total = total + apply(1, rw);
    }
    return total;
}`, 42},
	{"escape-array-reassign",
		`function mk(): () => i32 {
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    a = [42, 1];
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-string-reassign",
		`function mk(): () => i32 {
    let s: string = "aa";
    let f: () => i32 = (): i32 => { return s.len(); };
    s = "abcdef";
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g() + 36;
}`, 42},
	{"escape-struct-reassign",
		`struct B { v: i32 }
function mk(): () => i32 {
    let b: B = B { v: 10 };
    let f: () => i32 = (): i32 => { return b.v; };
    b = B { v: 42 };
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g();
}`, 42},
	{"escape-i32-reassign",
		`function mk(): () => i32 {
    let n: i32 = 10;
    let f: () => i32 = (): i32 => { return n; };
    n = 42;
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    return g();
}`, 42},
	// The escaped closure also WRITES the shared cell: outer sets 38 before
	// returning f; each call adds 2 — the second call reads the first call's
	// write through the same cell (40 + 2 = 42).
	{"escape-mixed-write",
		`function mk(): () => i32 {
    let n: i32 = 0;
    let f: () => i32 = (): i32 => { n = n + 2; return n; };
    n = 38;
    return f;
}
function main(): i32 {
    let g: () => i32 = mk();
    let r1: i32 = g();
    return g();
}`, 42},
	// Escape via an ARRAY CONTAINER (`[f]`), capture reassigned after: needs
	// BOTH the #5394 cell boxing (container storage is an escape) and the
	// #5405 closure-local array-literal classification (else `fs[0]()`
	// bare-calls the env box and SIGSEGVs).
	{"escape-container-reassign",
		`function main(): i32 {
    let a: i32[] = [10, 1];
    let f: () => i32 = (): i32 => { return a[0]; };
    let fs: (() => i32)[] = [f];
    a = [42, 1];
    return fs[0]();
}`, 42},
	// One capture shared by a direct-called closure AND a container-escaped
	// one; both read the post-reassignment buffer: 20 + 21 = 41.
	{"escape-two-closures-shared-capture",
		`function main(): i32 {
    let a: i32[] = [10, 1];
    let direct: () => i32 = (): i32 => { return a[0]; };
    let esc: () => i32 = (): i32 => { return a[0] + 1; };
    let keep: (() => i32)[] = [esc];
    a = [20, 1];
    return direct() + keep[0]();
}`, 41},
}

// TestSelfHostOuterMutCaptureIRX86_64 cross-checks the interpreter, pins the
// "ir" routing (these shapes all bailed to AST before), then runs the
// self-host-compiled binary.
func TestSelfHostOuterMutCaptureIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range outerMutCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			if code := runInterpExit(t, tc.src+"\n"); code != tc.want {
				t.Fatalf("%s interpreter exited %d, want %d", tc.name, code, tc.want)
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
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range outerMutCaptureIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
// write case under qemu via `asm_ir_run -target arm64-linux`.
func TestSelfHostOuterMutCaptureIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range outerMutCaptureIRCases {
		switch tc.name {
		case "array-reassign", "string-reassign", "struct-reassign", "outer-and-inner-write",
			"escape-array-reassign", "escape-mixed-write":
		default:
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
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
