package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A string handed to a struct-literal FIELD loses its own release ---------
//
// `var src: string = w("k"); var p: P = P { f: src, n: i };` with `src` read
// afterwards freed 100 of 300 boxes over 100 rounds where native freed all 300.
// The `str` column of the construction-retain matrix; the `local` cell of it.
//
// BOTH SIDES ALREADY FIRE. The literal retains the field (the ExprStructLit
// lowering's `cfft == "string" && slit_reclaim` arm) and __struct_drop_P decs it
// back. What was missing is the SOURCE's own claim: the struct-literal store
// reads as a store into a container, so `src` never earned "STR:" and the box it
// still holds at scope exit was never swept. inc 1, dec 1, and a reference
// nothing releases.
//
// The gate is the MOVE SITE, and it is what makes this a carve-out rather than a
// blanket accept. With `src` dead after the literal the store MOVES it: the box
// takes over the source's reference, moves_local_at elides the retain, and
// __struct_drop_P alone frees it — already correct, and granting the source a
// release there would be an over-release rather than a leak. So the forgiveness
// is granted only where the retain actually fires, which is the same
// co-extensivity rule the alias bind and the alias reassign are held to.
//
// Two more conditions, one per remaining way the pair could come apart. The
// holder must have earned its OWN struct credit — a RETURNED holder runs no
// field drop, so the retain would have nothing to give it back. And the type
// must route field reclaim, or __struct_drop_<T> carries no string arm at all.
//
// Ordering makes a classifier mismatch a sound leak rather than a double free:
// the exit sweep's struct loop runs BEFORE its string loop, so the field drop
// always precedes the local's free. That is the same guarantee the closure and
// tuple interlocks rest on.
//
// Every want below was confirmed against the native x86-64 backend, which is
// clean on all seven. Exit 99 is reserved for __rc_underflow_count().

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
    var t: i32 = 0;
    var r: i32 = 0;
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
    var src: string = w("k");
    var p: P = P { f: src, n: i };
    var t: i32 = (p.f.len() + p.n) % 101;
    return (t + src.len() + i) % 101;
}
` + sfssMain,
			want: 43, allocs: 300, frees: 300,
		},
		{
			// The holder in a nested BLOCK, so it dies before the source does.
			// Base 300/100.
			name: "field_source_outlives_the_holder",
			src: sfssPrelude + `function round(i: i32): i32 {
    var src: string = w("k");
    var t: i32 = 0;
    { var p: P = P { f: src, n: i }; t = (p.f.len() + p.n) % 101; }
    return (t + src.len() + i) % 101;
}
` + sfssMain,
			want: 43, allocs: 300, frees: 300,
		},
		{
			// The CONDITIONAL holder, and the reason the move site is the gate
			// rather than a liveness reading of the source. `src` is dead after
			// the `if`, but the store is only reached on half the rounds, so the
			// move analysis declines it and the retain fires — which is exactly
			// when the source needs its own release. Base 250/50.
			name: "field_source_in_a_conditional",
			src: sfssPrelude + `function round(i: i32): i32 {
    var src: string = w("k");
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = (p.f.len() + p.n) % 101; }
    return (t + i) % 101;
}
` + sfssMain,
			want: 64, allocs: 250, frees: 250,
		},
		{
			// THE ROW THAT CARRIES THE SOUNDNESS. Counts alone read 900/900
			// whether the release is correct or an over-release, so this one
			// reads the source back as a VALUE after the holder has died, with
			// three fresh strings allocated in between — a box freed too early is
			// reused before the read and the answer stops matching native's.
			name: "read_back_after_churn",
			src: sfssPrelude + `function round(i: i32): i32 {
    var src: string = w("k");
    var t: i32 = 0;
    { var p: P = P { f: src, n: i }; t = (p.f.len() + p.n) % 101; }
    var a: string = w("churn-one");
    var b: string = w("churn-two");
    var c: string = w("churn-three");
    return (t + src.len() + a.len() + b.len() + c.len() + i) % 101;
}
` + sfssMain,
			want: 25, allocs: 900, frees: 900,
		},
		{
			// THE MOVED CONTROL, and the row that says why the gate is the move
			// site. `src` is dead after the literal, so the store TRANSFERS the
			// box: no retain, and __struct_drop_P is the one release. Already
			// clean at 300/300 before this change and unchanged by it. If it ever
			// moves ABOVE 300 the forgiveness has reached a moved store and is
			// releasing a box the holder took over.
			name: "moved_field_source_unchanged",
			src: sfssPrelude + `function round(i: i32): i32 {
    var src: string = w("k");
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.n) % 101;
}
` + sfssMain,
			want: 73, allocs: 300, frees: 300,
		},
		{
			// REFUSED, and it must stay refused: the holder is RETURNED, so it
			// earns no struct credit and nothing runs its field drop. Releasing
			// the source here would free a box the caller's struct still points
			// at. 300/100, a sound leak, pinned as the gap it is.
			name: "escaping_holder_still_refused",
			src: sfssPrelude + `function mk(i: i32): P {
    var src: string = w("k");
    var p: P = P { f: src, n: i };
    if (src.len() > 3) { return p; }
    return p;
}
function round(i: i32): i32 {
    var p: P = mk(i);
    return (p.f.len() + p.n) % 101;
}
` + sfssMain,
			want: 73, allocs: 300, frees: 100,
		},
		{
			// REFUSED on the sole-use condition: `src` fills TWO string fields, so
			// one retain's worth of forgiveness does not cover the statement. The
			// walker's skip is per-STATEMENT, so a second use in the same literal
			// would be waved through with the first. 300/100, sound.
			name: "source_used_twice_still_refused",
			src: `struct P { f: string, g: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function round(i: i32): i32 {
    var src: string = w("k");
    var p: P = P { f: src, g: src, n: i };
    return (p.f.len() + p.g.len() + p.n + src.len()) % 101;
}
` + sfssMain,
			want: 11, allocs: 300, frees: 100,
		},
	}
}

// TestSelfHostStructFieldStrSourceX86_64 is the leak-accounting leg.
func TestSelfHostStructFieldStrSourceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
				t.Errorf("%s: %s — want frees=%d. FEWER means the \"SFLD:\" "+
					"forgiveness stopped applying; MORE on the moved control or "+
					"either refused row means it reached a store whose retain was "+
					"elided or whose holder runs no field drop", tc.name, summary, tc.frees)
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structFieldStrSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
