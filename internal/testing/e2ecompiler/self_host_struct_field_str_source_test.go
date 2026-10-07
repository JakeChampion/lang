package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A string handed to a struct-literal FIELD keeps its own release ---------
//
// `let src: string = w("k"); let p: P = P { f: src, n: i };` with `src` read
// afterwards: the literal retains the field and the struct's drop decs it back,
// while `src` keeps its own reference and releases it at scope exit. The `str`
// column of the construction-retain matrix; the `local` cell of it.
//
// With `src` dead after the literal the store MOVES it: the struct takes over
// the source's reference, no retain fires, and the struct's drop alone frees it
// — releasing the source as well would be an over-release. A RETURNED holder
// runs no field drop, so the field's reference travels with it.
//
// Exit 99 is reserved for __rc_underflow_count().
//
// Counts here are ONE block per heap string: #7351 fused the box into the
// buffer's reserved header. A pre-fusion number quoted in a row note below is
// twice its pin.

type structFieldStrSourceCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const sfssPrelude = `struct P { f: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
`

const sfssMain = `function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func structFieldStrSourceCases() []structFieldStrSourceCase {
	return []structFieldStrSourceCase{
		{
			// THE REPRO. Base: 300 allocs / 100 frees, native 200/200.
			name: "struct_lit_string_field",
			src: sfssPrelude + `function round(i: i32): i32 {
    let src: string = w("k");
    let p: P = P { f: src, n: i };
    let t: i32 = (p.f.len() + p.n) % 101;
    return (t + src.len() + i) % 101;
}
` + sfssMain,
			want: 43, allocs: 200, frees: 200,
		},
		{
			// The holder in a nested BLOCK, so it dies before the source does.
			// Base 300/100.
			name: "field_source_outlives_the_holder",
			src: sfssPrelude + `function round(i: i32): i32 {
    let src: string = w("k");
    let t: i32 = 0;
    { let p: P = P { f: src, n: i }; t = (p.f.len() + p.n) % 101; }
    return (t + src.len() + i) % 101;
}
` + sfssMain,
			want: 43, allocs: 200, frees: 200,
		},
		{
			// The CONDITIONAL holder, and the reason the move site is the gate
			// rather than a liveness reading of the source. `src` is dead after
			// the `if`, but the store is only reached on half the rounds, so the
			// move analysis declines it and the retain fires — which is exactly
			// when the source needs its own release. Base 250/50.
			name: "field_source_in_a_conditional",
			src: sfssPrelude + `function round(i: i32): i32 {
    let src: string = w("k");
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = (p.f.len() + p.n) % 101; }
    return (t + i) % 101;
}
` + sfssMain,
			want: 64, allocs: 150, frees: 150,
		},
		{
			// THE ROW THAT CARRIES THE SOUNDNESS. Counts alone read 900/900
			// whether the release is correct or an over-release, so this one
			// reads the source back as a VALUE after the holder has died, with
			// three fresh strings allocated in between — a box freed too early is
			// reused before the read and the answer stops matching native's.
			name: "read_back_after_churn",
			src: sfssPrelude + `function round(i: i32): i32 {
    let src: string = w("k");
    let t: i32 = 0;
    { let p: P = P { f: src, n: i }; t = (p.f.len() + p.n) % 101; }
    let a: string = w("churn-one");
    let b: string = w("churn-two");
    let c: string = w("churn-three");
    return (t + src.len() + a.len() + b.len() + c.len() + i) % 101;
}
` + sfssMain,
			want: 25, allocs: 500, frees: 500,
		},
		{
			// THE MOVED CONTROL, and the row that says why the gate is the move
			// site. `src` is dead after the literal, so the store TRANSFERS the
			// box: no retain, and P's drop is the one release. Releasing the
			// source as well would free a box the holder took over (exit 99).
			name: "moved_field_source_unchanged",
			src: sfssPrelude + `function round(i: i32): i32 {
    let src: string = w("k");
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.n) % 101;
}
` + sfssMain,
			want: 73, allocs: 200, frees: 200,
		},
		{
			// The holder is RETURNED, so a release of the source here would
			// free a box the caller's struct still points at; the exit
			// guards it.
			name: "escaping_holder_still_refused",
			src: sfssPrelude + `function mk(i: i32): P {
    let src: string = w("k");
    let p: P = P { f: src, n: i };
    if (src.len() > 3) { return p; }
    return p;
}
function round(i: i32): i32 {
    let p: P = mk(i);
    return (p.f.len() + p.n) % 101;
}
` + sfssMain,
			want: 73, allocs: 200, frees: 200,
		},
		{
			// `src` fills TWO string fields in one literal.
			name: "source_used_twice_still_refused",
			src: `struct P { f: string, g: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function round(i: i32): i32 {
    let src: string = w("k");
    let p: P = P { f: src, g: src, n: i };
    return (p.f.len() + p.g.len() + p.n + src.len()) % 101;
}
` + sfssMain,
			want: 11, allocs: 200, frees: 200,
		},
	}
}

// TestSelfHostStructFieldStrSourceX86_64 is the leak-accounting leg.
func TestSelfHostStructFieldStrSourceX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structFieldStrSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "sfss_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the source was "+
					"released as well as the holder's field drop)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStructFieldStrSourceWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostStructFieldStrSourceWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct-field string-source wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range structFieldStrSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "sfss_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct-field string-source wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStructFieldStrSourceIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStructFieldStrSourceIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structFieldStrSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "sfss_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
