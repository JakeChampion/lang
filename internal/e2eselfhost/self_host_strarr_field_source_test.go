package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A struct-literal `string[]` field costs the SOURCE its element walk -----
//
// `var src: string[] = mkv(i); if (..) { var p: P = P { f: src, n: i }; .. }`
// freed 450 of 650 boxes over 100 rounds where native freed all 450 of its own.
//
// THIS IS NOT #7557 ONE TYPE OVER, and the grid is what says so. For `string`
// the discriminator was the MOVE: the unconditional store leaked and the moved
// one was already clean. Here the move axis is irrelevant — every unconditional
// form was ALREADY clean, and every conditional one leaked by the same amount:
//
//	unconditional, source not read after (move)   700/700  clean
//	unconditional, source read after              700/700  clean
//	unconditional nested block { }                700/700  clean
//	`if (i >= 0)` — always entered                700/700  clean
//	`while (k < 1)` — entered once                700/700  clean
//	`if (i % 2 == 0)` — entered half the time     650/450  LEAKS
//
// The decisive pair is `if (i >= 0)` against `if (i % 2 == 0)`: `round`'s
// emitted call profile is IDENTICAL between them — same counts of rc_inc,
// __struct_drop_P, __field_reclaim_P, __fern_arr_dec, and no __fern_str_arr_free
// in either. Same code, different path coverage.
//
// WHAT WAS ACTUALLY WRONG. The retain already fired (the struct-lit ARRAY arm's
// `is_array_type_name(cfft)` covers `string[]`), and `__struct_drop_P` walked
// the elements. What was missing is the SOURCE's own DEEP release: `src` never
// earned "SARR:", so its exit sweep was a shallow `__fern_arr_dec` — buffer
// only. The program was correct by accident. The exit sweep's ARRAY loop runs
// BEFORE its struct loop, so src decs 2 -> 1 and frees nothing, then the struct
// loop finds rc 1 and does the full walk. That balances only on paths where the
// holder is actually constructed; skip it and the elements leak while the
// buffer is still freed — 200 over 50 skipped rounds, exactly the two element
// strings (box + buffer each).
//
// The fix grants `src` the DEEP class rather than delegating its walk to a
// holder that may never exist. Safe by the mechanism the SARR: block already
// states for the alias bind: __fern_str_arr_free is rc-gated, so rc>1 decs and
// leaves the elements to the other owner and only the LAST owner at rc 1 walks.
// The walk cannot run twice.
//
// Every want below was confirmed against the native x86-64 backend, which is
// clean on all of them. Exit 99 is reserved for __rc_underflow_count().

type strArrFieldSourceCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

const safsPrelude = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { var o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
`

const safsMain = `function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func strArrFieldSourceCases() []strArrFieldSourceCase {
	return []strArrFieldSourceCase{
		{
			// THE REPRO: the holder is built on half the rounds. Base 650/450.
			name: "conditional_holder",
			src: safsPrelude + `function round(i: i32): i32 {
    var src: string[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    return (t + i) % 101;
}
` + safsMain,
			want: 63, allocs: 650, frees: 650,
		},
		{
			// The same, with the source READ after the conditional — which for
			// `string` was the whole discriminator and here changes nothing.
			// Base 650/450, identical to the row above.
			name: "conditional_holder_source_read_after",
			src: safsPrelude + `function round(i: i32): i32 {
    var src: string[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    return (t + src.len() + src[0].len() + i) % 101;
}
` + safsMain,
			want: 30, allocs: 650, frees: 650,
		},
		{
			// THE ROW THAT CARRIES THE SOUNDNESS. The danger here is not a leak
			// but a DOUBLE element walk — the holder's field drop and the
			// source's sweep both walking. So this reads the source's ELEMENTS
			// back after the holder has died, with two fresh string arrays
			// allocated in between: a box freed twice is reused before the read
			// and the answer stops matching native's. Base 1850/1650.
			name: "elements_read_back_after_churn",
			src: safsPrelude + `function round(i: i32): i32 {
    var src: string[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    var c1: string[] = mkv(i + 7);
    var c2: string[] = mkv(i + 9);
    return (t + src.len() + src[0].len() + src[1].len() + c1[0].len() - c1[0].len() + c2[1].len() - c2[1].len() + i) % 101;
}
` + safsMain,
			want: 96, allocs: 1850, frees: 1850,
		},
		{
			// CONTROL, and the row that says the move axis is NOT the
			// discriminator for this type: the unconditional store was already
			// clean at 700/700 before this change. If it ever exceeds 700 the
			// forgiveness has reached a store whose retain was elided.
			name: "unconditional_holder_unchanged",
			src: safsPrelude + `function round(i: i32): i32 {
    var src: string[] = mkv(i);
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}
` + safsMain,
			want: 71, allocs: 700, frees: 700,
		},
		{
			// CONTROL: an `if` whose condition happens to always hold. Same
			// statement kind as the repro, same emitted code, and already clean
			// — which is what rules out "it is the StmtIf arm" as the cause.
			name: "always_entered_if_unchanged",
			src: safsPrelude + `function round(i: i32): i32 {
    var src: string[] = mkv(i);
    var t: i32 = 0;
    if (i >= 0) { var p: P = P { f: src, n: i }; t = (p.f.len() + p.f[0].len() + p.n) % 101; }
    return (t + src.len() + i) % 101;
}
` + safsMain,
			want: 70, allocs: 700, frees: 700,
		},
		{
			// The holder ESCAPES by return, so it earns no struct credit and the
			// source is refused. Already clean at 700/700 — pinned so that a
			// forgiveness which ignored the credited-holder condition would show
			// up as frees running ABOVE 700 rather than passing unnoticed.
			name: "escaping_holder_unchanged",
			src: safsPrelude + `function mk(i: i32): P {
    var src: string[] = mkv(i);
    var p: P = P { f: src, n: i };
    if (src.len() > 1) { return p; }
    return p;
}
function round(i: i32): i32 {
    var p: P = mk(i);
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}
` + safsMain,
			want: 71, allocs: 700, frees: 700,
		},
	}
}

// TestSelfHostStrArrFieldSourceX86_64 is the leak-accounting leg.
func TestSelfHostStrArrFieldSourceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "safs_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: both owners walked "+
					"the elements instead of only the one at rc 1)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — want frees=%d. FEWER on a conditional row means the "+
					"source lost its DEEP \"SARR:\" credit again; MORE on any control "+
					"means the forgiveness reached a store whose retain was elided or "+
					"whose holder runs no field drop", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStrArrFieldSourceWasmIR — exit codes only, so what this leg
// catches is a release that frees a LIVE box on wasm, the 99 included.
func TestSelfHostStrArrFieldSourceWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping strarr field-source wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strArrFieldSourceCases() {
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
			watFile := filepath.Join(dir, "safs_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("strarr field-source wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStrArrFieldSourceIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStrArrFieldSourceIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrFieldSourceCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "safs_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
